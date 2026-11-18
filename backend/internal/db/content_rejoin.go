package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auth"
)

// ContentRejoinCommand grants a fresh workspace subject to a frozen person's
// existing platform account. The old subject stays frozen.
type ContentRejoinCommand struct {
	RequestID         string `json:"request_id"`
	ProposalID        string `json:"proposal_id,omitempty"`
	Action            string `json:"action"`
	TargetID          int64  `json:"target_id"`
	Reason            string `json:"reason"`
	DelegationID      string `json:"delegation_id"`
	DelegationVersion int64  `json:"delegation_version"`
}

// IAMContentRejoin records a new, independently approved read-only membership.
func (r *UserRepository) IAMContentRejoin(ctx context.Context, in ContentRejoinCommand) (json.RawMessage, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if in.TargetID <= 0 || in.Reason == "" || len([]rune(in.Reason)) > 1000 || strings.ContainsRune(in.Reason, 0) {
		return nil, iamInput("重新加入请求无效")
	}
	var delegationOp string
	switch in.Action {
	case "propose":
		if in.ProposalID != "" {
			return nil, iamInput("新提案不能指定既有提案号")
		}
		delegationOp = "assignment.propose"
	case "approve":
		if !iamUUID.MatchString(in.ProposalID) {
			return nil, iamInput("请指定有效提案号")
		}
		delegationOp = "assignment.review"
	default:
		return nil, iamInput("未知重新加入动作")
	}
	tx, err := r.iamBegin(ctx, "space.assign.manage")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = r.iamIsolation(ctx, tx); err != nil {
		return nil, err
	}
	op := "content.custody.rejoin." + in.Action
	result, hash, err := iamReceipt(ctx, tx, in.RequestID, op, in)
	if err != nil || result != nil {
		return result, err
	}
	d, err := iamDelegationTx(ctx, tx, in.DelegationID, in.DelegationVersion, ActorID(ctx))
	if err != nil {
		return nil, err
	}
	var starterActions []string
	err = tx.QueryRow(ctx, `SELECT COALESCE(array_agg(action_id ORDER BY action_id),'{}')
		FROM sys_iam_capabilities WHERE scope='workspace' AND implementation_state='legacy'
		  AND 'readonly'=ANY(legacy_roles)`).Scan(&starterActions)
	if err != nil {
		return nil, err
	}
	if len(starterActions) == 0 {
		return nil, iamConflict("只读成员的当前许可目录无法核验")
	}
	if err = iamCeiling(d, delegationOp, starterActions, []int64{in.TargetID}, nil, time.Now()); err != nil {
		return nil, err
	}
	account, frozen, version, err := readRejoinBasis(ctx, tx, in.TargetID)
	if err != nil {
		return nil, err
	}
	var actorAccount int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(workspace_account_id,id) FROM sys_users WHERE id=dbhub_actor()`).Scan(&actorAccount); err != nil {
		return nil, err
	}
	if account == actorAccount {
		return nil, iamDenied("不得为本人重新加入提名或批准")
	}
	if in.Action == "propose" {
		err = tx.QueryRow(ctx, `INSERT INTO sys_iam_isolated_custody_rejoins(workspace_id,retired_user_id,freeze_sequence,
			retired_member_version,account_id,isolation_id,proposed_by,proposed_reason)
			VALUES(dbhub_workspace(),$1,$2,$3,$4,$5,dbhub_actor(),$6)
			ON CONFLICT DO NOTHING RETURNING id::text`, in.TargetID, frozen, version, account, r.iamIsolationID, in.Reason).Scan(&in.ProposalID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamConflict("本次冻结已登记中的重新加入提案")
		}
		if err != nil {
			return nil, err
		}
		return iamCommit(ctx, tx, in.RequestID, op, hash, map[string]any{"id": in.ProposalID, "state": "pending", "target_id": in.TargetID, "reason": in.Reason})
	}
	var proposerAccount int64
	err = tx.QueryRow(ctx, `SELECT COALESCE(u.workspace_account_id,u.id) FROM sys_iam_isolated_custody_rejoins j
		JOIN sys_users u ON u.id=j.proposed_by
		WHERE j.workspace_id=dbhub_workspace() AND j.id=$1::uuid AND j.retired_user_id=$2 AND j.freeze_sequence=$3
		  AND j.retired_member_version=$4 AND j.account_id=$5 AND j.isolation_id=$6 AND j.approved_at IS NULL
		  AND dbhub_iam_control_allowed(j.proposed_by,'space.assign.manage') FOR UPDATE OF j`,
		in.ProposalID, in.TargetID, frozen, version, account, r.iamIsolationID).Scan(&proposerAccount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, iamConflict("重新加入提案或当前冻结基线已变化")
	}
	if err != nil {
		return nil, err
	}
	if actorAccount == proposerAccount {
		return nil, iamDenied("重新加入须由另一名治理人独立批准")
	}
	var subject int64
	err = tx.QueryRow(ctx, `INSERT INTO sys_users(username,password_hash,role_id,workspace_account_id)
		SELECT 'workspace-member-'||gen_random_uuid()::text,'',id,$1 FROM sys_roles WHERE code='readonly' RETURNING id`, account).Scan(&subject)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_iam_isolated_custody_rejoins SET approved_by=dbhub_actor(),
		approved_at=clock_timestamp(),new_subject_id=$2 WHERE id=$1::uuid`, in.ProposalID, subject); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sys_workspace_members(workspace_id,user_id,role_code)
		VALUES(dbhub_workspace(),$1,'readonly')`, subject); err != nil {
		return nil, err
	}
	if err = iamBump(ctx, tx); err != nil {
		return nil, err
	}
	return iamCommit(ctx, tx, in.RequestID, op, hash, map[string]any{
		"id": in.ProposalID, "state": "approved", "target_id": in.TargetID,
		"member_id": subject, "role": "readonly", "reason": in.Reason,
	})
}

func readRejoinBasis(ctx context.Context, tx pgx.Tx, target int64) (account, sequence, version int64, err error) {
	err = tx.QueryRow(ctx, `SELECT account.id,
		(SELECT MAX(v.sequence) FROM sys_iam_revocations v WHERE v.workspace_id=m.workspace_id
			AND v.target_id=m.user_id AND v.kind='member_freeze'),m.version
		FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id
		JOIN sys_users account ON account.id=COALESCE(u.workspace_account_id,u.id)
		WHERE m.workspace_id=dbhub_workspace() AND m.user_id=$1 AND account.is_active AND account.workspace_account_id IS NULL
		  AND NOT EXISTS(SELECT 1 FROM sys_workspace_members active JOIN sys_users identity ON identity.id=active.user_id
		    WHERE active.workspace_id=m.workspace_id AND COALESCE(identity.workspace_account_id,identity.id)=account.id
		      AND dbhub_iam_qualified(active.user_id))
		  AND EXISTS(SELECT 1 FROM sys_iam_custody c WHERE c.workspace_id=m.workspace_id AND c.user_id=m.user_id)
		FOR SHARE OF m,account`, target).Scan(&account, &sequence, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = iamConflict("原成员尚未完成冻结，或平台账号已有当前成员身份")
	}
	return
}

type rejoinedWorkspace struct {
	workspaceID int64
	subject     int64
}

func (r *UserRepository) rejoinedWorkspaces(ctx context.Context, workspace int64) ([]rejoinedWorkspace, error) {
	c, _ := auth.ClaimsFromContext(ctx)
	if r.iamIsolationID == "" || c == nil || c.SessionID == "" || c.SessionCreatedAt.IsZero() || WorkspaceID(ctx) != 0 {
		return nil, nil
	}
	rows, err := r.pool.maintenance.Query(ctx, `SELECT j.workspace_id,j.new_subject_id FROM sys_iam_isolated_custody_rejoins j
		JOIN sys_iam_isolation_guard i ON i.isolation_id=j.isolation_id AND i.database_name=current_database()
		JOIN sys_users account ON account.id=j.account_id AND account.is_active AND account.workspace_account_id IS NULL
		JOIN sys_users member ON member.id=j.new_subject_id AND member.is_active AND member.workspace_account_id=account.id
		WHERE j.account_id=$1 AND ($2::bigint=0 OR j.workspace_id=$2) AND j.approved_at<$3 AND j.isolation_id=$4
		ORDER BY j.approved_at DESC,j.id DESC`, c.Subject, workspace, c.SessionCreatedAt, r.iamIsolationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rejoinedWorkspace
	for rows.Next() {
		var item rejoinedWorkspace
		if err = rows.Scan(&item.workspaceID, &item.subject); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func rejoinedSubjectContext(ctx context.Context, subject int64) context.Context {
	claims, _ := auth.ClaimsFromContext(ctx)
	copy := *claims
	copy.Subject = subject
	return auth.WithClaims(ctx, &copy)
}

func (r *UserRepository) withRejoinedWorkspaces(ctx context.Context, spaces []Workspace) ([]Workspace, error) {
	items, err := r.rejoinedWorkspaces(ctx, 0)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		found := false
		for _, space := range spaces {
			found = found || space.ID == item.workspaceID
		}
		if found {
			continue
		}
		space, err := r.bindWorkspaceCurrent(rejoinedSubjectContext(ctx, item.subject), item.workspaceID)
		if err != nil {
			return nil, err
		}
		if space != nil {
			spaces = append(spaces, *space)
		}
	}
	return spaces, nil
}
