// Package native adapts the existing verified Go target drivers.
package native

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/target"
)

type Driver struct{ Kind string }

func (d Driver) Descriptor() connector.Descriptor {
	c := connector.Capabilities{SQL: true, Format: true, Metadata: true, Collect: true, Transactions: true, EnforcedReadOnly: true}
	if d.Kind == "redis" {
		c = connector.Capabilities{Keys: true}
	}
	return connector.Descriptor{ID: d.Kind + ".native", Implementation: "native", Capabilities: c}
}
func (d Driver) Open(ctx context.Context, s connector.OpenSpec) (connector.Session, error) {
	c := &db.Connection{ID: s.Source.ID, Type: d.Kind, Database: s.Source.Database, Username: s.Username, SSLMode: s.SSLMode, ConnectionTimeout: s.Timeout}
	// Endpoint is carried in JDBC-independent source configuration.
	c.Host = s.Host
	c.Port = s.Port
	var tunnel *db.SSHTunnel
	if p := s.Network.Proxy; p != nil {
		c.Proxy = &db.Proxy{Type: p.Type, Host: p.Host, Port: p.Port, Username: p.Username, Password: p.Password, Status: p.Status}
	}
	if t := s.Network.Tunnel; t != nil {
		tunnel = &db.SSHTunnel{Host: t.Host, Port: t.Port, Username: t.Username, AuthType: t.AuthType, HostKey: t.HostKey, PrivateKey: t.PrivateKey, Passphrase: t.Passphrase, Password: t.Password}
	}
	if d.Kind == "redis" {
		r, e := target.OpenRedis(ctx, c, s.Password, tunnel)
		if e != nil {
			return nil, normalize(e)
		}
		return &keySession{r: r, spec: s}, nil
	}
	r, e := target.OpenRelational(ctx, c, s.Password, tunnel)
	if e != nil {
		return nil, normalize(e)
	}
	return &sqlSession{r: r, spec: s}, nil
}
func normalize(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, redis.Nil) {
		return &connector.Error{Kind: "not_found", Message: "键不存在", Cause: err}
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		definite := p.Code != "40003" && len(p.Code) == 5 && strings.Contains("|22|23|25|40|42|0A|", "|"+p.Code[:2]+"|")
		return &connector.Error{Kind: "statement_rejected", Message: "目标数据库返回语句错误（" + p.Code + "）", SQLState: p.Code, Definite: definite, Cause: err}
	}
	return err
}

type sqlSession struct {
	r    *target.RelDB
	spec connector.OpenSpec
}

func (s *sqlSession) Probe(ctx context.Context) (*connector.TestInfo, error) {
	return target.ProbeRelational(ctx, s.r)
}
func (s *sqlSession) Close(context.Context) error { s.r.Close(); return nil }
func (s *sqlSession) Execute(ctx context.Context, q connector.ExecuteRequest) (*connector.ExecResult, error) {
	o := q.Options
	r, e := target.ExecuteWithOptions(ctx, s.r, q.SQL, q.ReadOnly, target.ExecuteOptions{MaxRows: o.MaxRows, Timeout: o.Timeout, Args: o.Args, AllowUnsafeWrite: o.AllowUnsafeWrite, BeforeExecute: o.BeforeExecute})
	return r, normalize(e)
}
func (s *sqlSession) Databases(c context.Context) ([]connector.DatabaseItem, error) {
	return target.Databases(c, s.r)
}
func (s *sqlSession) Schemas(c context.Context, n connector.Namespace) ([]string, error) {
	return target.Schemas(c, s.r)
}
func (s *sqlSession) Tables(c context.Context, n connector.Namespace) ([]connector.TableItem, error) {
	return target.Tables(c, s.r, n.Database, n.Schema)
}
func (s *sqlSession) Columns(c context.Context, o connector.ObjectRef) ([]connector.ColumnItem, error) {
	return target.Columns(c, s.r, o.Database, o.Schema, o.Name)
}
func (s *sqlSession) DescribeTable(c context.Context, o connector.ObjectRef) (connector.TableMeta, error) {
	return target.DescribeTable(c, s.r, o.Database, o.Schema, o.Name, o.Kind)
}
func (s *sqlSession) Begin(c context.Context, o connector.TransactionOptions) (connector.Transaction, error) {
	if e := s.r.Begin(c, o.ReadOnly); e != nil {
		return nil, normalize(e)
	}
	return &transaction{s: s}, nil
}

type transaction struct{ s *sqlSession }

func (t *transaction) Execute(c context.Context, q connector.ExecuteRequest) (*connector.ExecResult, error) {
	return t.s.Execute(c, q)
}
func (t *transaction) Commit(c context.Context) error   { return normalize(t.s.r.Commit(c)) }
func (t *transaction) Rollback(c context.Context) error { return normalize(t.s.r.Rollback(c)) }

type keySession struct {
	r    *target.RedisClient
	spec connector.OpenSpec
}

func (s *keySession) Probe(c context.Context) (*connector.TestInfo, error) {
	return target.ProbeRedis(c, s.r)
}
func (s *keySession) Close(context.Context) error { s.r.Close(); return nil }
func (s *keySession) Overview(c context.Context) (*connector.RedisInfo, error) {
	return target.RedisOverviewInfo(c, s.r.Client)
}
func (s *keySession) Scan(c context.Context, p string, m int64) ([]connector.RedisKeyItem, error) {
	return target.ScanKeys(c, s.r.Client, p, m)
}
func (s *keySession) Inspect(c context.Context, k string) (*connector.RedisValue, error) {
	r, e := target.InspectKey(c, s.r.Client, k)
	return r, normalize(e)
}
