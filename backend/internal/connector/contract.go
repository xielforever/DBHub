package connector

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Capabilities are implemented behavior, never permissions granted by a client.
type Capabilities struct {
	SQL              bool   `json:"sql"`
	Format           bool   `json:"format"`
	Metadata         bool   `json:"metadata"`
	Collect          bool   `json:"collect"`
	Keys             bool   `json:"keys"`
	Transactions     bool   `json:"transactions"`
	EnforcedReadOnly bool   `json:"enforced_read_only"`
	Reason           string `json:"reason,omitempty"`
}

type Descriptor struct {
	ID             string       `json:"id"`
	Implementation string       `json:"implementation"`
	Capabilities   Capabilities `json:"capabilities"`
}
type Source struct {
	ID             int64
	Type, Database string
}

// Network is a transport plan, not a process-local dial function.
type Network struct {
	Proxy  *Proxy
	Tunnel *Tunnel
}
type Proxy struct {
	Type, Host, Username, Password, Status string
	Port                                   int
}
type Tunnel struct {
	Host, Username, AuthType, HostKey, PrivateKey, Passphrase, Password string
	Port                                                                int
}
type OpenSpec struct {
	Source   Source
	Host     string
	Port     int
	Username string
	Password string `json:"-"`
	SSLMode  string
	Timeout  int
	Network  Network
	JDBC     *JDBCSpec
}
type JDBCSpec struct {
	ArtifactID, SHA256, URL, Dialect string
	Properties                       map[string]string
	Capabilities                     Capabilities
}
type Namespace struct{ Database, Catalog, Schema string }
type ObjectRef struct {
	Namespace
	Name, Kind string
}
type ExecuteOptions struct {
	// LosslessNumbers keeps exact decimal / large integer text on the JSON wire.
	LosslessNumbers  bool
	MaxRows          int
	MaxBytes         int
	Timeout          time.Duration
	Args             []any
	AllowUnsafeWrite bool
	BeforeExecute    func() error
	ExecutionKey     string
}
type ExecuteRequest struct {
	SQL      string
	ReadOnly bool
	Query    bool
	Options  ExecuteOptions
}
type TransactionOptions struct{ ReadOnly bool }

type Connector interface {
	Descriptor() Descriptor
	Open(context.Context, OpenSpec) (Session, error)
}
type Session interface {
	Probe(context.Context) (*TestInfo, error)
	Close(context.Context) error
}
type SQLExecutor interface {
	Execute(context.Context, ExecuteRequest) (*ExecResult, error)
}
type MetadataReader interface {
	Databases(context.Context) ([]DatabaseItem, error)
	Schemas(context.Context, Namespace) ([]string, error)
	Tables(context.Context, Namespace) ([]TableItem, error)
	Columns(context.Context, ObjectRef) ([]ColumnItem, error)
	DescribeTable(context.Context, ObjectRef) (TableMeta, error)
}
type TransactionBeginner interface {
	Begin(context.Context, TransactionOptions) (Transaction, error)
}
type Transaction interface {
	SQLExecutor
	Commit(context.Context) error
	Rollback(context.Context) error
}
type KeyReader interface {
	Overview(context.Context) (*RedisInfo, error)
	Scan(context.Context, string, int64) ([]RedisKeyItem, error)
	Inspect(context.Context, string) (*RedisValue, error)
}
type KeyMutation struct {
	Key, Type     string
	Value         any
	TTL           *int64
	Field, Member string
	Score         float64
	Direction     string
}
type KeyWriter interface {
	Mutate(context.Context, string, KeyMutation) (map[string]any, error)
}

// Error preserves internal cause without leaking target configuration or SQL.
type Error struct {
	Kind, Message, SQLState string
	Definite                bool
	Cause                   error
}

func (e *Error) Error() string         { return e.Message }
func (e *Error) Unwrap() error         { return e.Cause }
func Unsupported(message string) error { return &Error{Kind: "unsupported", Message: message} }

// Registry is assembled at startup. Duplicate and unknown IDs fail closed.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]Connector
}

func NewRegistry() *Registry { return &Registry{entries: map[string]Connector{}} }
func (r *Registry) Register(c Connector) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := c.Descriptor().ID
	if id == "" {
		return fmt.Errorf("连接器 ID 不能为空")
	}
	if _, ok := r.entries[id]; ok {
		return fmt.Errorf("连接器 ID 重复: %s", id)
	}
	r.entries[id] = c
	return nil
}
func (r *Registry) Resolve(id string) (Connector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.entries[id]
	if !ok {
		return nil, Unsupported("未注册的数据源连接器")
	}
	return c, nil
}
