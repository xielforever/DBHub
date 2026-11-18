package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/iam"
)

func iamTryExclusive(ctx context.Context, tx pgx.Tx, wid int64) (bool, error) {
	var target, legacy bool
	if e := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended(current_schema()||':iam:target:'||$1,0))`, fmt.Sprint(wid)).Scan(&target); e != nil || !target {
		return false, e
	}
	e := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended(current_schema()||':iam:'||$1,0))`, fmt.Sprint(wid)).Scan(&legacy)
	return legacy, e
}
func iamRecordSettlement(ctx context.Context, tx pgx.Tx, wid, actor int64, id, fingerprint string) error {
	_, e := tx.Exec(ctx, `INSERT INTO sys_iam_restriction_settlements(workspace_id,actor_id,request_id,bridge_version,route_fingerprint) VALUES($1,$2,$3::uuid,$4,$5) ON CONFLICT DO NOTHING`, wid, actor, id, iam.RuntimeBridge, fingerprint)
	return e
}

// Settle checks the recorded command, never a client-supplied permission patch.
// The cutoff remains in force if an old process or an admitted operation keeps
// the execution fence unconfirmed. No business write is retried or reversed.
func (r *UserRepository) iamSettle(ctx context.Context, wid, actor int64, id string) (bool, error) {
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return false, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var exists, settled bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_iam_commands WHERE workspace_id=$1 AND actor_id=$2 AND request_id=$3::uuid AND operation='governance.restrict' AND result->>'execution_fence'='pending'),EXISTS(SELECT 1 FROM sys_iam_restriction_settlements WHERE workspace_id=$1 AND actor_id=$2 AND request_id=$3::uuid)`, wid, actor, id).Scan(&exists, &settled)
	if e != nil || settled {
		return settled, e
	}
	if !exists {
		return false, nil
	}
	held, e := iamTryExclusive(ctx, tx, wid)
	if e != nil || !held {
		return false, e
	}
	scoped := WithWorkspace(ctx, wid)
	if e = r.iamFleet(scoped, tx); e != nil {
		return false, nil
	}
	if e = iamRecordSettlement(ctx, tx, wid, actor, id, r.iamRouteFingerprint); e != nil {
		return false, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO sys_iam_events(workspace_id,actor_id,request_id,operation,payload) VALUES($1,$2,$3::uuid,'governance.restrict.settled',jsonb_build_object('execution_fence','confirmed','business_writes_retried',false))`, wid, actor, id)
	if e != nil {
		return false, e
	}
	if e = tx.Commit(ctx); e != nil {
		return false, e
	}
	return true, nil
}

func (r *UserRepository) IAMSettleRestrictions(ctx context.Context) error {
	rows, e := r.pool.maintenance.Query(ctx, `SELECT c.workspace_id,c.actor_id,c.request_id::text FROM sys_iam_commands c WHERE c.operation='governance.restrict' AND c.result->>'execution_fence'='pending' AND NOT EXISTS(SELECT 1 FROM sys_iam_restriction_settlements x WHERE (x.workspace_id,x.actor_id,x.request_id)=(c.workspace_id,c.actor_id,c.request_id)) ORDER BY c.created_at LIMIT 20`)
	if e != nil {
		return e
	}
	type key struct {
		wid, actor int64
		id         string
	}
	list := []key{}
	for rows.Next() {
		var k key
		if e = rows.Scan(&k.wid, &k.actor, &k.id); e != nil {
			rows.Close()
			return e
		}
		list = append(list, k)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, k := range list {
		if _, e = r.iamSettle(ctx, k.wid, k.actor, k.id); e != nil {
			return e
		}
	}
	return nil
}

// Own receipts are identity-scoped and remain readable after a workspace
// freeze. Only this caller's restriction IDs/status are returned, never another
// member's business assets, connection credentials or a governance directory.
func (r *UserRepository) IAMOwnRestrictionStatus(ctx context.Context, wid int64, id string) (json.RawMessage, error) {
	if ActorID(ctx) <= 0 || wid < 0 || id != "" && !iamUUID.MatchString(id) {
		return nil, ErrManagedInput
	}
	if id != "" {
		if _, e := r.iamSettle(ctx, wid, ActorID(ctx), id); e != nil {
			return nil, e
		}
	}
	var raw []byte
	e := r.pool.maintenance.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('workspace_id',c.workspace_id,'request_id',c.request_id,'kind',c.result->>'kind','state',c.result->>'state','execution_fence',CASE WHEN c.result->>'execution_fence'='confirmed' OR x.request_id IS NOT NULL THEN 'confirmed' ELSE 'pending' END,'created_at',c.created_at,'settled_at',x.settled_at) ORDER BY c.created_at DESC),'[]') FROM (SELECT workspace_id,actor_id,request_id,result,created_at FROM sys_iam_commands WHERE actor_id=$1 AND operation='governance.restrict' AND ($2::bigint=0 OR workspace_id=$2) AND ($3='' OR request_id::text=$3) ORDER BY created_at DESC LIMIT 20)c LEFT JOIN sys_iam_restriction_settlements x USING(workspace_id,actor_id,request_id)`, ActorID(ctx), wid, id).Scan(&raw)
	return raw, e
}
