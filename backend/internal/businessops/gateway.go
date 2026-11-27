// Package businessops implements a closed, versioned business-operation protocol.
// Neither SQL, actor identity nor an execution proof is accepted from a browser.
package businessops

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
)

//go:embed protocol.sql
var ProtocolSQL string

func SigningKey(master []byte) string {
	mac := hmac.New(sha256.New, master)
	_, _ = mac.Write([]byte("dbhub-controlled-operations-v1"))
	return hex.EncodeToString(mac.Sum(nil))
}
func mac(key, text string) string {
	h := hmac.New(sha256.New, []byte(key))
	_, _ = h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}
func decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(out)
}

type Gateway interface {
	Health(context.Context, *db.Connection, string, string) error
	Preview(context.Context, *db.OperationTarget, db.OperationPreview) (db.OperationPreview, error)
	Apply(context.Context, *db.OperationTarget, *db.OperationRequest) (db.OperationReceipt, error)
	Lookup(context.Context, *db.OperationTarget, *db.OperationRequest) (*db.OperationReceipt, error)
}
type TargetGateway struct {
	Runtime *connectorruntime.Runtime
	Key     string
}

func NewGateway(runtime *connectorruntime.Runtime, key []byte) *TargetGateway {
	return &TargetGateway{runtime, SigningKey(key)}
}

type TargetError struct {
	Dispatched bool
	Definite   bool
	Cause      error
}

func (e *TargetError) Error() string { return "目标业务操作尚未成功核实" }
func (e *TargetError) Unwrap() error { return e.Cause }
func DefiniteFailure(err error) bool {
	var target *TargetError
	return errors.As(err, &target) && (!target.Dispatched || target.Definite)
}
func (g *TargetGateway) call(ctx context.Context, c *db.Connection, password string, p map[string]any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p["protocol"] = 1
	p["expires"] = time.Now().Add(20 * time.Second).Unix()
	raw, e := json.Marshal(p)
	if e != nil {
		return e
	}
	handle, e := g.Runtime.OpenRelational(ctx, c, password, nil)
	if e != nil {
		return &TargetError{false, true, e}
	}
	defer handle.Close()
	write := p["purpose"] == "apply"
	if e = handle.Begin(ctx, !write); e != nil {
		return &TargetError{false, true, e}
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = handle.Rollback(clean)
	}()
	dispatched := false
	result, e := connectorruntime.ExecuteWithOptions(ctx, handle, `SELECT dbhub_ops_api_v1.dispatch_v1($1::text,$2::text) AS operation_proof`, false, connectorruntime.ExecuteOptions{MaxRows: 1, MaxBytes: 65536, Timeout: 8 * time.Second, Args: []any{string(raw), mac(g.Key, "request:"+string(raw))}, BeforeExecute: func() error {
		if e := auditctx.Checkpoint(ctx); e != nil {
			return e
		}
		dispatched = true
		return nil
	}})
	if e != nil {
		var pg *pgconn.PgError
		var ce *connector.Error
		definite := errors.As(e, &pg) && pg.Code == "P0001" || errors.As(e, &ce) && ce.Definite
		return &TargetError{dispatched, definite, e}
	}
	var envelope struct {
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if result.Truncated || len(result.Rows) != 1 || len(result.Rows[0]) != 1 {
		return &TargetError{dispatched, false, fmt.Errorf("invalid proof envelope")}
	}
	text, ok := result.Rows[0][0].(string)
	if !ok || len(text) > 65536 || json.Unmarshal([]byte(text), &envelope) != nil || !hmac.Equal([]byte(envelope.Signature), []byte(mac(g.Key, "response:"+envelope.Payload))) {
		return &TargetError{dispatched, false, fmt.Errorf("unverified target response")}
	}
	if e = decode([]byte(envelope.Payload), out); e != nil {
		return &TargetError{dispatched, false, e}
	}
	if write {
		if e = handle.Commit(ctx); e != nil {
			return &TargetError{true, false, e}
		}
	}
	return nil
}
func (g *TargetGateway) Health(ctx context.Context, c *db.Connection, password, installation string) error {
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	var reply struct {
		Installation string `json:"installation_id"`
		Nonce        string `json:"nonce"`
		Protocol     int    `json:"protocol"`
		Safe         bool   `json:"role_safe"`
	}
	e := g.call(ctx, c, password, map[string]any{"purpose": "health", "installation_id": installation, "nonce": nonce}, &reply)
	if e != nil {
		return e
	}
	if reply.Installation != installation || reply.Nonce != nonce || reply.Protocol != 1 || !reply.Safe {
		return fmt.Errorf("账号须为已安装协议的独立EXECUTE账号，禁止表访问和高权限角色")
	}
	return nil
}
func previewPayload(p db.OperationPreview) map[string]any {
	return map[string]any{"purpose": "preview", "installation_id": p.InstallationID, "workspace_id": p.WorkspaceID, "actor_id": p.ActorID, "kind": p.Kind, "target_key": p.TargetKey, "amount": p.Amount, "preview_at": p.PreviewAt}
}
func (g *TargetGateway) Preview(ctx context.Context, target *db.OperationTarget, p db.OperationPreview) (db.OperationPreview, error) {
	var reply db.OperationPreview
	e := g.call(ctx, target.Connection, target.Password, previewPayload(p), &reply)
	if e != nil {
		return reply, e
	}
	if reply.InstallationID != p.InstallationID || reply.WorkspaceID != p.WorkspaceID || reply.ActorID != p.ActorID || reply.Kind != p.Kind || reply.TargetKey != p.TargetKey || reply.Amount != p.Amount || reply.PreviewAt != p.PreviewAt || len(reply.BeforeHash) != 64 || len(reply.AfterHash) != 64 || reply.TargetVersion == "" {
		return reply, db.ErrManagedConflict
	}
	return reply, nil
}
func (g *TargetGateway) Apply(ctx context.Context, target *db.OperationTarget, x *db.OperationRequest) (db.OperationReceipt, error) {
	p := previewPayload(x.Preview)
	p["purpose"] = "apply"
	p["operation_id"] = x.OperationID
	p["reviewer_id"] = x.ReviewerID
	p["before_sha256"] = x.Preview.BeforeHash
	p["after_sha256"] = x.Preview.AfterHash
	p["target_version"] = x.Preview.TargetVersion
	var out db.OperationReceipt
	e := g.call(ctx, target.Connection, target.Password, p, &out)
	return out, e
}
func (g *TargetGateway) Lookup(ctx context.Context, target *db.OperationTarget, x *db.OperationRequest) (*db.OperationReceipt, error) {
	var out struct {
		Found   bool                 `json:"found"`
		Receipt *db.OperationReceipt `json:"receipt"`
	}
	e := g.call(ctx, target.Connection, target.Password, map[string]any{"purpose": "lookup", "installation_id": x.Preview.InstallationID, "workspace_id": x.Preview.WorkspaceID, "actor_id": x.UserID, "operation_id": x.OperationID}, &out)
	if e != nil {
		return nil, e
	}
	if !out.Found {
		return nil, nil
	}
	if out.Receipt == nil || out.Receipt.OperationID != x.OperationID {
		return nil, db.ErrManagedConflict
	}
	return out.Receipt, nil
}
