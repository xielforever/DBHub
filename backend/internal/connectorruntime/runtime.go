// Package connectorruntime coordinates target connectors with application policy.
package connectorruntime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/connector/jdbc"
	"github.com/user/dbhub/internal/connector/native"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/sqlparse"
	legacy "github.com/user/dbhub/internal/target"
)

type Runtime struct {
	RecordUsage func(context.Context, *db.Connection)
	registry    *connector.Registry
	jdbc        *jdbc.Client
}

func New() *Runtime {
	r := &Runtime{registry: connector.NewRegistry()}
	for _, kind := range []string{"mysql", "postgres", "redis"} {
		if err := r.registry.Register(native.Driver{Kind: kind}); err != nil {
			panic(err)
		}
	}
	return r
}
func NewConfigured(endpoint, token string) *Runtime {
	r := New()
	if endpoint != "" && token != "" {
		r.jdbc = jdbc.NewClient(endpoint, token)
		if e := r.registry.Register(jdbc.Driver{Client: r.jdbc}); e != nil {
			panic(e)
		}
	}
	return r
}
func (r *Runtime) AvailableArtifacts(ctx context.Context) ([]jdbc.Artifact, error) {
	return r.jdbc.Artifacts(ctx)
}

// ValidateArtifactTemplate checks driver acceptance without contacting a target.
func (r *Runtime) ValidateArtifactTemplate(ctx context.Context, t *db.DatasourceTemplate) error {
	copyTemplate := *t
	copyTemplate.Enabled = true
	copyTemplate.Driver.Enabled = true
	params := map[string]string{}
	for _, f := range t.Fields {
		value := "sample"
		if f.Name == "host" {
			value = "localhost"
		}
		if f.Name == "port" {
			value = "1"
		}
		params[f.Name] = value
	}
	c := &db.Connection{Type: "jdbc", Template: &copyTemplate, Parameters: params, Database: ProfileDatabase(params)}
	spec, e := BuildJDBCSpec(c)
	if e != nil {
		return e
	}
	return r.jdbc.Call(ctx, "validate-template", map[string]any{"artifact_id": spec.ArtifactID, "url": spec.URL}, nil)
}
func (r *Runtime) Capabilities(c *db.Connection) connector.Capabilities {
	caps := c.SupportedCapabilities()
	if c.Type == "jdbc" && r.jdbc == nil {
		return connector.Capabilities{Reason: "JDBC 服务未配置"}
	}
	return caps
}
func Default(runtimes []*Runtime) *Runtime {
	if len(runtimes) > 0 && runtimes[0] != nil {
		return runtimes[0]
	}
	return New()
}
func (r *Runtime) spec(c *db.Connection, password string, t *db.SSHTunnel) connector.OpenSpec {
	s := connector.OpenSpec{Source: connector.Source{ID: c.ID, Type: c.Type, Database: c.Database}, Host: c.Host, Port: c.Port, Username: c.Username, Password: password, SSLMode: c.SSLMode, Timeout: c.ConnectionTimeout}
	if p := c.Proxy; p != nil {
		s.Network.Proxy = &connector.Proxy{Type: p.Type, Host: p.Host, Port: p.Port, Username: p.Username, Password: p.Password, Status: p.Status}
	}
	if t != nil {
		s.Network.Tunnel = &connector.Tunnel{Host: t.Host, Port: t.Port, Username: t.Username, AuthType: t.AuthType, HostKey: t.HostKey, PrivateKey: t.PrivateKey, Passphrase: t.Passphrase, Password: t.Password}
	}
	return s
}
func (r *Runtime) OpenRelational(ctx context.Context, c *db.Connection, password string, t *db.SSHTunnel) (*RelDB, error) {
	if c == nil {
		return nil, fmt.Errorf("数据源不能为空")
	}
	if c.Type == "redis" {
		return nil, connector.Unsupported("Redis 请使用键值操作")
	}
	if c.Definition != nil && c.Definition.Visibility == "global" && c.Database != c.Definition.Config.Database {
		return nil, connector.Unsupported("共享定义已固定数据库，请确认新的数据源修订后再切换")
	}
	if e := ValidateDestination(c); e != nil {
		return nil, e
	}
	key := c.Type + ".native"
	spec := r.spec(c, password, t)
	if c.Type == "jdbc" {
		key = "jdbc"
		var e error
		spec.JDBC, e = BuildJDBCSpec(c)
		if e != nil {
			return nil, e
		}
	}
	if t != nil {
		if e := ValidateDestination(&db.Connection{Host: t.Host, Port: t.Port}); e != nil {
			return nil, e
		}
	}
	driver, err := r.registry.Resolve(key)
	if err != nil {
		return nil, err
	}
	r.auditTarget(ctx, c, false)
	session, err := driver.Open(ctx, spec)
	if err != nil {
		return nil, err
	}
	r.auditTarget(ctx, c, true)
	if r.RecordUsage != nil {
		r.RecordUsage(ctx, c)
	}
	return &RelDB{Conn: c, session: session, Capabilities: r.Capabilities(c)}, nil
}
func (r *Runtime) OpenRedis(ctx context.Context, c *db.Connection, password string, t *db.SSHTunnel) (*RedisClient, error) {
	if c == nil || c.Type != "redis" {
		return nil, connector.Unsupported("不是键值数据源")
	}
	if e := ValidateDestination(c); e != nil {
		return nil, e
	}
	if t != nil {
		if e := ValidateDestination(&db.Connection{Host: t.Host, Port: t.Port}); e != nil {
			return nil, e
		}
	}
	d, _ := r.registry.Resolve("redis.native")
	r.auditTarget(ctx, c, false)
	s, e := d.Open(ctx, r.spec(c, password, t))
	if e != nil {
		return nil, e
	}
	r.auditTarget(ctx, c, true)
	if r.RecordUsage != nil {
		r.RecordUsage(ctx, c)
	}
	return &RedisClient{session: s}, nil
}
func (r *Runtime) Test(ctx context.Context, c *db.Connection, password string, t *db.SSHTunnel) (*TestInfo, error) {
	if c.Type == "redis" {
		s, e := r.OpenRedis(ctx, c, password, t)
		if e != nil {
			return nil, e
		}
		defer s.Close()
		return s.session.Probe(ctx)
	}
	s, e := r.OpenRelational(ctx, c, password, t)
	if e != nil {
		return nil, e
	}
	defer s.Close()
	return s.session.Probe(ctx)
}

type RelDB struct {
	Conn         *db.Connection
	Capabilities connector.Capabilities
	session      connector.Session
	tx           connector.Transaction
	readOnly     bool
}

func (r *RelDB) Close() {
	ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if e := r.session.Close(ctx); e != nil {
		slog.Warn("关闭目标会话失败", "error", e)
	}
}
func (r *RelDB) Begin(ctx context.Context, ro bool) error {
	if r.tx != nil {
		return fmt.Errorf("事务已开启")
	}
	b, ok := r.session.(connector.TransactionBeginner)
	if !ok || !r.Capabilities.Transactions {
		return connector.Unsupported("此数据源尚不支持事务")
	}
	tx, e := b.Begin(ctx, connector.TransactionOptions{ReadOnly: ro})
	if e == nil {
		r.tx = tx
		r.readOnly = ro
	}
	return e
}
func (r *RelDB) Commit(ctx context.Context) error {
	if r.tx == nil {
		return fmt.Errorf("事务未开启")
	}
	tx := r.tx
	r.tx = nil
	r.readOnly = false
	return tx.Commit(ctx)
}
func (r *RelDB) Rollback(ctx context.Context) error {
	if r.tx == nil {
		return fmt.Errorf("事务未开启")
	}
	tx := r.tx
	r.tx = nil
	r.readOnly = false
	return tx.Rollback(ctx)
}

type ExecuteOptions = connector.ExecuteOptions

func ExecuteWithOptions(ctx context.Context, r *RelDB, query string, readOnly bool, opt ExecuteOptions) (*ExecResult, error) {
	if r == nil || !r.Capabilities.SQL {
		return nil, connector.Unsupported("此数据源尚未启用 SQL 方言与执行能力")
	}
	ast, e := sqlparse.Parse(query, r.Conn.SQLDialect())
	if e != nil {
		return nil, e
	}
	if (readOnly || r.readOnly) && !ast.ReadOnly {
		return nil, fmt.Errorf("只读角色禁止写操作和有副作用的语句")
	}
	if readOnly && !r.readOnly && r.tx != nil {
		return nil, fmt.Errorf("角色已变更，请重新开启只读事务")
	}
	if ast.UnsafeWrite && !opt.AllowUnsafeWrite {
		return nil, fmt.Errorf("禁止执行没有 WHERE 条件或条件明显恒真的 UPDATE/DELETE")
	}
	x, ok := r.session.(connector.SQLExecutor)
	if !ok {
		return nil, connector.Unsupported("数据源不支持 SQL")
	}
	if r.tx != nil {
		x = r.tx
	}
	return x.Execute(ctx, connector.ExecuteRequest{SQL: query, ReadOnly: readOnly || r.readOnly, Query: ast.ReadOnly, Options: opt})
}
func reader(r *RelDB) (connector.MetadataReader, error) {
	m, ok := r.session.(connector.MetadataReader)
	if !ok || !r.Capabilities.Metadata {
		return nil, connector.Unsupported("数据源不支持结构浏览")
	}
	return m, nil
}
func Databases(ctx context.Context, r *RelDB) ([]DatabaseItem, error) {
	m, e := reader(r)
	if e != nil {
		return nil, e
	}
	if r.Conn.Definition != nil && r.Conn.Definition.Visibility == "global" {
		return []DatabaseItem{{Name: r.Conn.Database}}, nil
	}
	return m.Databases(ctx)
}
func Schemas(ctx context.Context, r *RelDB) ([]string, error) {
	m, e := reader(r)
	if e != nil {
		return nil, e
	}
	return m.Schemas(ctx, connector.Namespace{Database: r.Conn.Database})
}
func Tables(ctx context.Context, r *RelDB, d, s string) ([]TableItem, error) {
	m, e := reader(r)
	if e != nil {
		return nil, e
	}
	return m.Tables(ctx, connector.Namespace{Database: d, Schema: s})
}
func Columns(ctx context.Context, r *RelDB, d, s, t string) ([]ColumnItem, error) {
	m, e := reader(r)
	if e != nil {
		return nil, e
	}
	return m.Columns(ctx, connector.ObjectRef{Namespace: connector.Namespace{Database: d, Schema: s}, Name: t})
}
func IntrospectSchema(ctx context.Context, r *RelDB, d, s string) ([]TableMeta, error) {
	m, e := reader(r)
	if e != nil {
		return nil, e
	}
	tables, e := m.Tables(ctx, connector.Namespace{Database: d, Schema: s})
	if e != nil {
		return nil, e
	}
	out := []TableMeta{}
	for _, t := range tables {
		v, e := m.DescribeTable(ctx, connector.ObjectRef{Namespace: connector.Namespace{Database: d, Schema: s}, Name: t.Name, Kind: t.Type})
		if e != nil {
			return nil, e
		}
		if v.MetadataStatus == nil {
			v.MetadataStatus = map[string]string{}
		}
		if v.Comment == "" {
			v.Comment = t.Comment
		}
		out = append(out, v)
	}
	return out, nil
}

type RedisClient struct{ session connector.Session }

func (r *RedisClient) Close() {
	ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if e := r.session.Close(ctx); e != nil {
		slog.Warn("关闭键值会话失败", "error", e)
	}
}
func RedisOverviewInfo(ctx context.Context, r *RedisClient) (*RedisInfo, error) {
	m, ok := r.session.(connector.KeyReader)
	if !ok {
		return nil, connector.Unsupported("不支持键值浏览")
	}
	return m.Overview(ctx)
}
func ScanKeys(ctx context.Context, r *RedisClient, p string, max int64) ([]RedisKeyItem, error) {
	m, ok := r.session.(connector.KeyReader)
	if !ok {
		return nil, connector.Unsupported("不支持键扫描")
	}
	return m.Scan(ctx, p, max)
}
func InspectKey(ctx context.Context, r *RedisClient, k string) (*RedisValue, error) {
	m, ok := r.session.(connector.KeyReader)
	if !ok {
		return nil, connector.Unsupported("不支持键读取")
	}
	return m.Inspect(ctx, k)
}
func MutateKey(ctx context.Context, r *RedisClient, op string, in connector.KeyMutation) (map[string]any, error) {
	m, ok := r.session.(connector.KeyWriter)
	if !ok {
		return nil, connector.Unsupported("不支持键值修改")
	}
	return m.Mutate(ctx, op, in)
}
func IsUserDatabase(typ, name string) bool {
	n := strings.ToLower(name)
	if typ == "mysql" {
		return n != "mysql" && n != "information_schema" && n != "performance_schema" && n != "sys"
	}
	return n != "template0" && n != "template1"
}
func IsUserSchema(typ, name string) bool {
	return typ != "postgres" || !strings.HasPrefix(name, "pg_") && name != "information_schema"
}
func DefiniteStatementError(err error) bool {
	var e *connector.Error
	return errors.As(err, &e) && e.Definite
}

type TestInfo = connector.TestInfo
type ExecResult = connector.ExecResult
type DatabaseItem = connector.DatabaseItem
type TableItem = connector.TableItem
type ColumnItem = connector.ColumnItem
type TableMeta = connector.TableMeta
type RedisInfo = connector.RedisInfo
type RedisKeyItem = connector.RedisKeyItem
type RedisValue = connector.RedisValue

func TestedVersions() []legacy.TestedVersion { return legacy.TestedVersions() }

func (r *Runtime) auditTarget(ctx context.Context, c *db.Connection, opened bool) {
	auditctx.Update(ctx, func(d *auditctx.Data) {
		d.ConnectionID = c.ID
		d.SourceFamilyID = c.DefinitionFamilyID
		d.DatabaseAccount = c.Username
		d.Driver = c.Type
		d.TargetOpened = opened
		if c.DefinitionRevisionID != nil {
			d.DefinitionID = *c.DefinitionRevisionID
		}
		if c.Definition != nil {
			d.Revision = c.Definition.Revision
		}
		if c.Template != nil {
			d.Driver = c.Template.Driver.ArtifactID
		}
	})
}
