package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ContentHandoverCommand advances the handover of one frozen asset.
type ContentHandoverCommand struct {
	RequestID         string `json:"request_id"`
	Version           int64  `json:"version"`
	Action            string `json:"action"`
	Reason            string `json:"reason"`
	CandidateID       int64  `json:"candidate_id,omitempty"`
	DelegationID      string `json:"delegation_id,omitempty"`
	DelegationVersion int64  `json:"delegation_version,omitempty"`
}

type contentCustodyItem struct {
	id, owner, version, candidate, nominator int64
	asset                                    availabilityObject
	state, stamp                             string
}

// IAMContentCustody returns metadata only. Candidates see their own nomination.
func (r *UserRepository) IAMContentCustody(ctx context.Context, nominations bool) (json.RawMessage, error) {
	capability := "space.members.view"
	if nominations {
		capability = ""
	}
	tx, err := r.iamReadBegin(ctx, capability)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = r.iamIsolation(ctx, tx); err != nil {
		return nil, err
	}
	var out json.RawMessage
	err = tx.QueryRow(ctx, `SELECT jsonb_build_object('items',COALESCE(jsonb_agg(jsonb_build_object(
			'id',id,'user_id',user_id,'freeze_sequence',freeze_sequence,'report_id',report_id,
			'dashboard_id',dashboard_id,'captured',captured,'candidate_id',candidate_id,
			'nominated_by',nominated_by,'decided_by',decided_by,'version',version,'state',state) ORDER BY id),'[]'))
		FROM sys_content_custody_items WHERE workspace_id=dbhub_workspace()
		  AND (NOT $1 OR candidate_id=dbhub_actor())`, nominations).Scan(&out)
	return out, err
}

// IAMContentHandover checks the nomination, consent and independent approval
// against current membership, the original custody row and the exact asset.
func (r *UserRepository) IAMContentHandover(ctx context.Context, itemID int64, in ContentHandoverCommand) (json.RawMessage, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if itemID <= 0 || in.Version <= 0 || in.Reason == "" || len([]rune(in.Reason)) > 1000 || strings.ContainsRune(in.Reason, 0) ||
		in.CandidateID < 0 || (in.Action == "nominate") != (in.CandidateID > 0) {
		return nil, iamInput("交接请求无效")
	}
	var capability, operation string
	switch in.Action {
	case "nominate":
		capability, operation = "space.members.exit", "member.freeze"
	case "approve":
		capability, operation = "space.assign.manage", "assignment.review"
	case "accept", "reject":
		if in.DelegationID != "" || in.DelegationVersion != 0 {
			return nil, iamInput("候选人须以本人身份答复")
		}
	default:
		return nil, iamInput("未知交接动作")
	}
	tx, err := r.iamBegin(ctx, capability)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = r.iamIsolation(ctx, tx); err != nil {
		return nil, err
	}
	logOperation := "content.custody." + in.Action
	known, hash, err := iamReceipt(ctx, tx, in.RequestID, logOperation, []any{itemID, in})
	if err != nil || known != nil {
		return known, err
	}
	item := contentCustodyItem{id: itemID}
	err = tx.QueryRow(ctx, `SELECT user_id,version,COALESCE(candidate_id,0),COALESCE(nominated_by,0),state,
			COALESCE(nomination_stamp,''),COALESCE(report_id,0),COALESCE(dashboard_id,0)
		FROM sys_content_custody_items WHERE workspace_id=dbhub_workspace() AND id=$1
		  AND ($2 OR candidate_id=dbhub_actor()) FOR UPDATE`, itemID, capability != "").
		Scan(&item.owner, &item.version, &item.candidate, &item.nominator, &item.state, &item.stamp, &item.asset.report, &item.asset.dashboard)
	if err != nil {
		return nil, managedError(err)
	}
	if item.version != in.Version || item.state == "transferred" {
		return nil, iamConflict("交接状态已变化，请读取当前版本")
	}
	if in.Action == "nominate" {
		item.candidate = in.CandidateID
	}
	if capability != "" {
		d, err := iamDelegationTx(ctx, tx, in.DelegationID, in.DelegationVersion, ActorID(ctx))
		if err != nil {
			return nil, err
		}
		if err = iamCeiling(d, operation, nil, []int64{item.owner, item.candidate}, nil, time.Now()); err != nil {
			return nil, err
		}
	}
	state, draft, err := r.advanceHandover(ctx, tx, item, in.Action)
	if err != nil {
		return nil, err
	}
	return iamCommit(ctx, tx, in.RequestID, logOperation, hash, map[string]any{
		"id": item.id, "version": item.version + 1, "state": state, "report_id": item.asset.report,
		"dashboard_id": item.asset.dashboard, "candidate_id": item.candidate, "reason": in.Reason, "draft_version": draft,
		"delegation_id": in.DelegationID, "delegation_version": in.DelegationVersion,
	})
}

func (r *UserRepository) advanceHandover(ctx context.Context, tx pgx.Tx, item contentCustodyItem, action string) (string, int64, error) {
	actor := ActorID(ctx)
	switch action {
	case "reject":
		if item.state != "nominated" && item.state != "accepted" {
			return "", 0, ErrManagedConflict
		}
		_, err := tx.Exec(ctx, `UPDATE sys_content_custody_items SET state='held',candidate_id=NULL,nominated_by=NULL,
			nomination_stamp=NULL,accepted_at=NULL,version=version+1 WHERE id=$1`, item.id)
		return "held", 0, err
	case "accept":
		if item.state != "nominated" {
			return "", 0, ErrManagedConflict
		}
	case "approve":
		if item.state != "accepted" {
			return "", 0, ErrManagedConflict
		}
		if actor == item.owner || actor == item.nominator || actor == item.candidate {
			return "", 0, iamDenied("批准人须独立于提名人、候选人和退出人")
		}
	}
	if item.candidate == item.owner || item.candidate <= 0 {
		return "", 0, iamInput("候选人必须是原空间的其他当前成员")
	}
	if action != "nominate" {
		var current bool
		if err := tx.QueryRow(ctx, `SELECT dbhub_iam_control_allowed($1,'space.members.exit')`, item.nominator).Scan(&current); err != nil {
			return "", 0, err
		}
		if !current {
			return "", 0, iamConflict("提名人的治理资格已变化，请重新提名")
		}
	}
	basis, err := r.custodyHandoverBasis(ctx, tx, item)
	if err != nil {
		return "", 0, err
	}
	stamp := reportDigest(basis)
	if action != "nominate" && item.stamp != stamp {
		return "", 0, iamConflict("候选资格、资产或依赖已变化，请重新提名")
	}
	switch action {
	case "nominate":
		_, err = tx.Exec(ctx, `UPDATE sys_content_custody_items SET state='nominated',candidate_id=$2,
			nominated_by=$3,nomination_stamp=$4,accepted_at=NULL,version=version+1 WHERE id=$1`, item.id, item.candidate, actor, stamp)
		return "nominated", 0, err
	case "accept":
		_, err = tx.Exec(ctx, `UPDATE sys_content_custody_items SET state='accepted',accepted_at=clock_timestamp(),
			version=version+1 WHERE id=$1`, item.id)
		return "accepted", 0, err
	}
	draft, err := completeHandover(ctx, tx, item.asset, item.owner, item.candidate)
	if err != nil {
		return "", 0, err
	}
	_, err = tx.Exec(ctx, `UPDATE sys_content_custody_items SET state='transferred',decided_by=$2,
		completed_at=clock_timestamp(),version=version+1 WHERE id=$1`, item.id, actor)
	return "transferred", draft, err
}

func completeHandover(ctx context.Context, tx pgx.Tx, object availabilityObject, owner, candidate int64) (int64, error) {
	var draft int64
	var err error
	if object.report > 0 {
		err = tx.QueryRow(ctx, `UPDATE sys_managed_reports SET owner_id=$3,draft_version=draft_version+1,updated_at=now()
			WHERE workspace_id=dbhub_workspace() AND id=$1 AND owner_id=$2 RETURNING draft_version`, object.report, owner, candidate).Scan(&draft)
		if err == nil {
			_, err = tx.Exec(ctx, `WITH withdrawn AS (
				UPDATE sys_report_reviews rv SET status='withdrawn',comment='责任人接任，请重新送审',decided_at=now()
				FROM sys_report_versions v WHERE rv.version_id=v.id AND rv.workspace_id=dbhub_workspace()
					AND v.report_id=$1 AND rv.status='pending' RETURNING rv.id)
				UPDATE sys_access_requests SET status='cancelled',decision_reason='责任人接任，原审阅已撤回',version=version+1
				WHERE workspace_id=dbhub_workspace() AND report_id=$1
					AND (requester_id=$2 OR review_id IN (SELECT id FROM withdrawn))
					AND status IN ('pending','processing','approved_pending_release')`, object.report, owner)
		}
	} else {
		err = tx.QueryRow(ctx, `UPDATE sys_managed_dashboards SET owner_id=$3,draft_version=draft_version+1,updated_at=now()
			WHERE workspace_id=dbhub_workspace() AND id=$1 AND owner_id=$2 AND lifecycle<>'deleted' RETURNING draft_version`, object.dashboard, owner, candidate).Scan(&draft)
		if err == nil {
			_, err = tx.Exec(ctx, `WITH withdrawn AS (
				UPDATE sys_dashboard_reviews SET status='withdrawn',comment='责任人接任，请重新送审',decided_at=now()
				WHERE workspace_id=dbhub_workspace() AND dashboard_id=$1 AND status='pending' RETURNING id)
				UPDATE sys_access_requests SET status='cancelled',decision_reason='责任人接任，原申请已失效',version=version+1
				WHERE workspace_id=dbhub_workspace() AND dashboard_id=$1 AND requester_id=$2
					AND status IN ('pending','processing','approved_pending_release')`, object.dashboard, owner)
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrManagedConflict
	}
	return draft, err
}
