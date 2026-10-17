// Package jdbc implements internal protocol v1 without retrying target operations.
package jdbc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/sqlparse"
)

type Artifact struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	ClassName string `json:"class_name"`
	SHA256    string `json:"sha256"`
	Ready     bool   `json:"ready"`
}
type Client struct {
	endpoint, token string
	http            *http.Client
}

func NewClient(endpoint, token string) *Client {
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), token: token, http: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: 16, MaxIdleConnsPerHost: 16, IdleConnTimeout: time.Minute}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Call(ctx context.Context, method string, input any, out any) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
	}
	if c == nil || c.endpoint == "" || c.token == "" {
		return &connector.Error{Kind: "unavailable", Message: "JDBC 服务未配置"}
	}
	raw, e := json.Marshal(input)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/"+method, bytes.NewReader(raw))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, e := c.http.Do(req)
	if e != nil {
		return &connector.Error{Kind: "transport", Message: "JDBC 服务通信失败；已发送操作的结果需核实", Cause: e}
	}
	defer res.Body.Close()
	var packet struct {
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Kind     string `json:"kind"`
			Message  string `json:"message"`
			SQLState string `json:"sql_state"`
			Definite bool   `json:"definite"`
		} `json:"error"`
	}
	decoder := json.NewDecoder(io.LimitReader(res.Body, 16<<20))
	if e = decoder.Decode(&packet); e != nil {
		return &connector.Error{Kind: "transport", Message: "JDBC 服务响应不完整", Cause: e}
	}
	if packet.Error != nil {
		p := packet.Error
		return &connector.Error{Kind: p.Kind, Message: p.Message, SQLState: p.SQLState, Definite: p.Definite}
	}
	if res.StatusCode != 200 || len(packet.Data) == 0 {
		return &connector.Error{Kind: "transport", Message: "JDBC 服务协议响应无效"}
	}
	if out != nil {
		return json.Unmarshal(packet.Data, out)
	}
	return nil
}
func (c *Client) Artifacts(ctx context.Context) ([]Artifact, error) {
	var out struct {
		Protocol int        `json:"protocol"`
		Items    []Artifact `json:"items"`
	}
	e := c.Call(ctx, "artifacts", struct{}{}, &out)
	if e == nil && out.Protocol != 1 {
		e = connector.Unsupported("JDBC 协议版本不匹配")
	}
	return out.Items, e
}

type Driver struct{ Client *Client }

func (d Driver) Descriptor() connector.Descriptor {
	return connector.Descriptor{ID: "jdbc", Implementation: "jdbc", Capabilities: connector.Capabilities{Metadata: true}}
}
func budget(ctx context.Context, defaultMS int) int {
	if end, ok := ctx.Deadline(); ok {
		n := int(time.Until(end).Milliseconds())
		if n < 1 {
			return 1
		}
		if n < defaultMS {
			return n
		}
	}
	return defaultMS
}
func id() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func (d Driver) Open(ctx context.Context, s connector.OpenSpec) (connector.Session, error) {
	timeout := s.Timeout
	if timeout <= 0 || timeout > 30 {
		timeout = 10
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	if s.JDBC == nil {
		return nil, connector.Unsupported("JDBC 模板缺失")
	}
	if s.Network.Proxy != nil || s.Network.Tunnel != nil {
		return nil, connector.Unsupported("JDBC 首轮尚未支持代理或 SSH")
	}
	owner := fmt.Sprintf("source:%d:%s", s.Source.ID, id())
	input := map[string]any{"artifact_id": s.JDBC.ArtifactID, "sha256": s.JDBC.SHA256, "url": s.JDBC.URL, "dialect": s.JDBC.Dialect, "owner": owner, "database": s.Source.Database, "username": s.Username, "password": s.Password, "ssl_mode": s.SSLMode, "budget_ms": budget(ctx, timeout*1000)}
	var response struct {
		ID       string `json:"session_id"`
		Protocol int    `json:"protocol"`
	}
	if e := d.Client.Call(ctx, "open", input, &response); e != nil {
		return nil, e
	}
	if response.ID == "" || response.Protocol != 1 {
		return nil, connector.Unsupported("JDBC 会话协议不匹配")
	}
	return &Session{client: d.Client, id: response.ID, owner: owner, spec: s}, nil
}

type Session struct {
	client    *Client
	id, owner string
	spec      connector.OpenSpec
	once      sync.Once
	closeErr  error
}

func (s *Session) payload(ctx context.Context) map[string]any {
	return map[string]any{"session_id": s.id, "owner": s.owner, "budget_ms": budget(ctx, 20000)}
}
func (s *Session) Probe(ctx context.Context) (*connector.TestInfo, error) {
	var info connector.TestInfo
	e := s.client.Call(ctx, "probe", s.payload(ctx), &info)
	return &info, e
}
func (s *Session) Close(ctx context.Context) error {
	s.once.Do(func() { s.closeErr = s.client.Call(ctx, "close", s.payload(ctx), nil) })
	return s.closeErr
}
func (s *Session) Begin(ctx context.Context, o connector.TransactionOptions) (connector.Transaction, error) {
	if !s.spec.JDBC.Capabilities.Transactions {
		return nil, connector.Unsupported("此模板未启用事务")
	}
	p := s.payload(ctx)
	p["read_only"] = o.ReadOnly
	if e := s.client.Call(ctx, "begin", p, nil); e != nil {
		return nil, e
	}
	return &transaction{s: s}, nil
}

type transaction struct{ s *Session }

func (t *transaction) Execute(ctx context.Context, q connector.ExecuteRequest) (*connector.ExecResult, error) {
	return t.s.Execute(ctx, q)
}
func (t *transaction) Commit(ctx context.Context) error {
	return t.s.client.Call(ctx, "commit", t.s.payload(ctx), nil)
}
func (t *transaction) Rollback(ctx context.Context) error {
	return t.s.client.Call(ctx, "rollback", t.s.payload(ctx), nil)
}

type wireValue struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Value bool   `json:"value"`
}

func parameter(v any) (wireValue, error) {
	switch x := v.(type) {
	case nil:
		return wireValue{Kind: "null"}, nil
	case string:
		return wireValue{Kind: "string", Text: x}, nil
	case bool:
		return wireValue{Kind: "boolean", Value: x}, nil
	case int:
		return wireValue{Kind: "integer", Text: fmt.Sprint(x)}, nil
	case int64:
		return wireValue{Kind: "integer", Text: fmt.Sprint(x)}, nil
	case float64:
		return wireValue{Kind: "decimal", Text: fmt.Sprint(x)}, nil
	default:
		return wireValue{}, fmt.Errorf("参数类型不支持")
	}
}
func (s *Session) Execute(ctx context.Context, q connector.ExecuteRequest) (*connector.ExecResult, error) {
	if !s.spec.JDBC.Capabilities.SQL {
		return nil, connector.Unsupported("此模板未启用 SQL 方言")
	}
	opt := q.Options
	if opt.MaxRows <= 0 || opt.MaxRows > 10000 {
		opt.MaxRows = 1000
	}
	if opt.Timeout <= 0 || opt.Timeout > 120*time.Second {
		opt.Timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()
	query, args := q.SQL, opt.Args
	var e error
	if s.spec.JDBC.Dialect == "postgres" {
		query, args, e = sqlparse.RebindJDBC(query, args)
		if e != nil {
			return nil, e
		}
	}
	values := []wireValue{}
	for _, v := range args {
		w, e := parameter(v)
		if e != nil {
			return nil, e
		}
		values = append(values, w)
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	if opt.BeforeExecute != nil {
		if e = opt.BeforeExecute(); e != nil {
			return nil, e
		}
	}
	key := opt.ExecutionKey
	if key == "" {
		key = "internal:" + id()
	}
	p := s.payload(ctx)
	p["execution_key"] = key
	p["sql"] = query
	p["args"] = values
	p["read_only"] = q.ReadOnly
	p["cursor"] = q.Query
	p["max_rows"] = opt.MaxRows
	p["budget_ms"] = budget(ctx, 120000)
	var result struct {
		Kind         string           `json:"kind"`
		Columns      []string         `json:"columns"`
		Rows         [][]wireValue    `json:"rows"`
		ColumnTypes  []map[string]any `json:"column_types"`
		AffectedRows int64            `json:"affected_rows"`
		DurationMS   int64            `json:"duration_ms"`
		Truncated    bool             `json:"truncated"`
	}
	e = s.client.Call(ctx, "execute", p, &result)
	if e != nil {
		if ctx.Err() != nil {
			cleanup, c := context.WithTimeout(context.Background(), 2*time.Second)
			defer c()
			request := s.payload(cleanup)
			request["execution_key"] = key
			_ = s.client.Call(cleanup, "cancel", request, nil)
		}
		return nil, e
	}
	out := &connector.ExecResult{Kind: result.Kind, Columns: result.Columns, Rows: [][]any{}, AffectedRows: result.AffectedRows, DurationMS: result.DurationMS, Truncated: result.Truncated, ColumnTypes: result.ColumnTypes}
	for _, row := range result.Rows {
		values := []any{}
		for _, v := range row {
			switch v.Kind {
			case "null":
				values = append(values, nil)
			case "boolean":
				values = append(values, v.Value)
			case "float":
				values = append(values, json.Number(v.Text))
			case "binary":
				values = append(values, map[string]string{"encoding": "base64", "value": v.Text})
			default:
				values = append(values, v.Text)
			}
		}
		out.Rows = append(out.Rows, values)
	}
	return out, nil
}
func (s *Session) metadata(ctx context.Context, op string, n connector.Namespace, table, kind string, out any) error {
	p := s.payload(ctx)
	p["operation"] = op
	p["database"] = n.Database
	p["schema"] = n.Schema
	p["table"] = table
	p["table_type"] = kind
	return s.client.Call(ctx, "metadata", p, out)
}
func (s *Session) Databases(ctx context.Context) ([]connector.DatabaseItem, error) {
	out := []connector.DatabaseItem{}
	e := s.metadata(ctx, "databases", connector.Namespace{}, "", "", &out)
	return out, e
}
func (s *Session) Schemas(ctx context.Context, n connector.Namespace) ([]string, error) {
	out := []string{}
	e := s.metadata(ctx, "schemas", n, "", "", &out)
	return out, e
}
func (s *Session) Tables(ctx context.Context, n connector.Namespace) ([]connector.TableItem, error) {
	out := []connector.TableItem{}
	e := s.metadata(ctx, "tables", n, "", "", &out)
	return out, e
}
func (s *Session) Columns(ctx context.Context, o connector.ObjectRef) ([]connector.ColumnItem, error) {
	out := []connector.ColumnItem{}
	e := s.metadata(ctx, "columns", o.Namespace, o.Name, o.Kind, &out)
	return out, e
}
func (s *Session) DescribeTable(ctx context.Context, o connector.ObjectRef) (connector.TableMeta, error) {
	var out connector.TableMeta
	e := s.metadata(ctx, "describe", o.Namespace, o.Name, o.Kind, &out)
	return out, e
}
