package db

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/iam"
	appcrypto "github.com/user/dbhub/pkg/crypto"
)

func recoveryActions() []string {
	return []string{"space.page", "space.members.view", "space.members.exit", "space.menus.view", "space.menus.manage", "space.roles.view", "space.roles.manage", "space.assign.view", "space.assign.manage", "space.policy.view"}
}

func (r *UserRepository) HasRecoveryRegistration(ctx context.Context)bool{
 var available bool;e:=r.pool.maintenance.QueryRow(ctx,`SELECT EXISTS(SELECT 1 FROM sys_iam_recovery_roster r JOIN sys_users u ON u.id=r.user_id WHERE r.user_id=$1 AND r.active AND u.is_active)`,ActorID(ctx)).Scan(&available);return e==nil&&available
}

type IAMRecoveryCommand struct {
	RequestID   string `json:"request_id"`
	WorkspaceID int64  `json:"workspace_id"`
	TargetID    int64  `json:"target_id"`
	Reason      string `json:"reason"`
}
type IAMRecoveryVerification struct {
	RequestID   string `json:"request_id"`
	WorkspaceID int64  `json:"workspace_id"`
	Password    string `json:"password"`
	Code        string `json:"code"`
}

func iamRecoveryRoster(ctx context.Context, tx pgx.Tx, wid int64) (string, error) {
	var hash string
	e := tx.QueryRow(ctx, `SELECT md5(COALESCE(string_agg(user_id::text||':'||version::text,',' ORDER BY user_id),'')) FROM sys_iam_recovery_roster WHERE workspace_id=$1 AND active`, wid).Scan(&hash)
	return hash, e
}
func iamRecoveryNoGovernor(ctx context.Context, tx pgx.Tx, wid int64) error {
	var exists bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_workspace_members m WHERE workspace_id=$1 AND dbhub_iam_qualified(user_id) AND
 (NOT dbhub_iam_is_target(user_id) AND role_code='admin' OR dbhub_iam_is_target(user_id) AND (dbhub_iam_has_action(user_id,'space.roles.manage') OR dbhub_iam_has_action(user_id,'space.assign.manage'))))`, wid).Scan(&exists)
	if e != nil {
		return e
	}
	if exists {
		return iamDenied("当前仍有有效治理人，请使用正常治理审批")
	}
	return nil
}
func iamRecoveryEligible(ctx context.Context, tx pgx.Tx, wid int64) error {
	var allowed bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_users u WHERE u.id=$1 AND u.is_active AND
 (u.is_platform_admin OR EXISTS(SELECT 1 FROM sys_iam_recovery_roster rr WHERE rr.workspace_id=$2 AND rr.user_id=u.id AND rr.active)))`, ActorID(ctx), wid).Scan(&allowed)
	if e != nil {
		return e
	}
	if !allowed {
		return iamDenied("仅预登记核验身份或平台治理人可发起恢复")
	}
	return nil
}
func (r *UserRepository) IAMRecoveryList(ctx context.Context) (json.RawMessage, error) {
	var raw []byte
	e := r.pool.maintenance.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('workspace_id',w.id,'name',w.name,'requests',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',q.request_id,'requester_id',q.requester_id,'target_id',q.target_id,'reason',q.reason,'state',CASE WHEN q.state='pending' AND q.expires_at<=now() THEN 'expired' ELSE q.state END,'expires_at',q.expires_at,'verified_count',(SELECT count(*) FROM sys_iam_recovery_verifications v WHERE v.workspace_id=q.workspace_id AND v.request_id=q.request_id),'self_verified',EXISTS(SELECT 1 FROM sys_iam_recovery_verifications v WHERE v.workspace_id=q.workspace_id AND v.request_id=q.request_id AND v.verifier_id=$1))) FROM sys_iam_recovery_requests q WHERE q.workspace_id=w.id AND (q.requester_id=$1 OR q.target_id=$1 OR EXISTS(SELECT 1 FROM sys_iam_recovery_roster r WHERE r.workspace_id=w.id AND r.user_id=$1 AND r.active))),'[]'))),'[]') FROM sys_workspaces w WHERE w.enabled AND (EXISTS(SELECT 1 FROM sys_iam_recovery_roster r WHERE r.workspace_id=w.id AND r.user_id=$1 AND r.active) OR EXISTS(SELECT 1 FROM sys_iam_recovery_requests q WHERE q.workspace_id=w.id AND (q.requester_id=$1 OR q.target_id=$1)) OR EXISTS(SELECT 1 FROM sys_users u WHERE u.id=$1 AND u.is_active AND u.is_platform_admin))`, ActorID(ctx)).Scan(&raw)
	return raw, e
}
func (r *UserRepository) IAMRecoveryRequest(ctx context.Context, in IAMRecoveryCommand) (json.RawMessage, error) {
	if in.WorkspaceID <= 0 || in.TargetID <= 0 || strings.TrimSpace(in.Reason) == "" || len([]rune(in.Reason)) > 1000 {
		return nil, iamInput("恢复空间、接收人及原因必填")
	}
	ctx = WithWorkspace(ctx, in.WorkspaceID)
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if e = iamExclusive(ctx, tx); e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `SELECT set_config('dbhub.workspace_id',$1,true)`, fmt.Sprint(in.WorkspaceID)); e != nil {
		return nil, e
	}
	if e = iamRecoveryEligible(ctx, tx, in.WorkspaceID); e != nil {
		return nil, e
	}
	previous, hash, e := iamReceipt(ctx, tx, in.RequestID, "governance.recovery.request", in)
	if e != nil || previous != nil {
		return previous, e
	}
	if e = iamRecoveryNoGovernor(ctx, tx, in.WorkspaceID); e != nil {
		return nil, e
	}
	var active bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_users WHERE id=$1 AND is_active)`, in.TargetID).Scan(&active)
	if e != nil {
		return nil, e
	}
	if !active {
		return nil, iamDenied("恢复接收身份不可用")
	}
	var count int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM sys_iam_recovery_roster r JOIN sys_users u ON u.id=r.user_id WHERE r.workspace_id=$1 AND r.active AND u.is_active AND r.user_id<>$2 AND r.user_id<>$3`, in.WorkspaceID, ActorID(ctx), in.TargetID).Scan(&count)
	if e != nil {
		return nil, e
	}
	if count < 2 {
		return nil, iamDenied("缺少两名不同且回避申请人和受益人的预登记核验身份")
	}
	roster, e := iamRecoveryRoster(ctx, tx, in.WorkspaceID)
	if e != nil {
		return nil, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO sys_iam_recovery_requests(workspace_id,request_id,requester_id,target_id,reason,baseline_epoch,roster_version,expires_at) SELECT $1,$2::uuid,$3,$4,$5,authorization_epoch,$6,now()+interval '10 minutes' FROM sys_iam_workspace_state WHERE workspace_id=$1`, in.WorkspaceID, in.RequestID, ActorID(ctx), in.TargetID, strings.TrimSpace(in.Reason), roster)
	if e != nil {
		return nil, e
	}
	return iamCommit(ctx, tx, in.RequestID, "governance.recovery.request", hash, map[string]any{"request_id": in.RequestID, "state": "pending", "required_independent_verifiers": 2, "business_permissions_restored": false})
}
func (r *UserRepository) IAMRecoveryVerify(ctx context.Context, in IAMRecoveryVerification, key []byte, sessions *auth.SessionStore) (json.RawMessage, error) {
	c, _ := auth.ClaimsFromContext(ctx)
	if c == nil || c.SessionID == "" || !iamUUID.MatchString(in.RequestID) || in.WorkspaceID <= 0 {
		return nil, iamDenied("恢复核验需要当前有效登录会话")
	}
	// Failed password attempts consume a durable budget even if the proof
	// transaction rolls back. Passwords and factors are never receipt contents.
	var attempts int
	e := r.pool.maintenance.QueryRow(ctx, `INSERT INTO sys_iam_recovery_attempts(workspace_id,user_id,attempts) SELECT $1,$2,1 WHERE EXISTS(SELECT 1 FROM sys_iam_recovery_roster WHERE workspace_id=$1 AND user_id=$2 AND active)
 ON CONFLICT(workspace_id,user_id) DO UPDATE SET attempts=CASE WHEN sys_iam_recovery_attempts.window_at<now()-interval '15 minutes' THEN 1 ELSE sys_iam_recovery_attempts.attempts+1 END,window_at=CASE WHEN sys_iam_recovery_attempts.window_at<now()-interval '15 minutes' THEN now() ELSE sys_iam_recovery_attempts.window_at END RETURNING attempts`, in.WorkspaceID, ActorID(ctx)).Scan(&attempts)
	if e != nil || attempts > 5 {
		return nil, iamDenied("核验身份无效或尝试过于频繁，请稍后重试")
	}
	ctx = WithWorkspace(ctx, in.WorkspaceID)
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if e = iamExclusive(ctx, tx); e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `SELECT set_config('dbhub.workspace_id',$1,true)`, fmt.Sprint(in.WorkspaceID)); e != nil {
		return nil, e
	}
	var requester, target, epoch, currentEpoch int64
	var roster, state string
	var expiry time.Time
	e = tx.QueryRow(ctx, `SELECT q.requester_id,q.target_id,q.baseline_epoch,s.authorization_epoch,q.roster_version,q.state,q.expires_at FROM sys_iam_recovery_requests q JOIN sys_iam_workspace_state s USING(workspace_id) WHERE q.workspace_id=$1 AND q.request_id=$2::uuid FOR UPDATE OF q,s`, in.WorkspaceID, in.RequestID).Scan(&requester, &target, &epoch, &currentEpoch, &roster, &state, &expiry)
	if e != nil {
		return nil, managedError(e)
	}
	if requester == ActorID(ctx) || target == ActorID(ctx) {
		return nil, iamDenied("申请人及受益人不能核验自己的恢复申请")
	}
	if state != "pending" || !expiry.After(time.Now()) || epoch != currentEpoch {
		return nil, iamConflict("恢复申请已结束、到期或基线变化")
	}
	currentRoster, e := iamRecoveryRoster(ctx, tx, in.WorkspaceID)
	if e != nil {
		return nil, e
	}
	if currentRoster != roster {
		return nil, iamConflict("预登记核验名单已变化")
	}
	if e = iamRecoveryNoGovernor(ctx, tx, in.WorkspaceID); e != nil {
		return nil, e
	}
	var user auth.User
	var rosterVersion int64
	e = tx.QueryRow(ctx, `SELECT u.id,u.username,u.password_hash,u.auth_version,r.version FROM sys_iam_recovery_roster r JOIN sys_users u ON u.id=r.user_id WHERE r.workspace_id=$1 AND r.user_id=$2 AND r.active AND u.is_active FOR SHARE OF r,u`, in.WorkspaceID, ActorID(ctx)).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.AuthVersion, &rosterVersion)
	if e != nil {
		return nil, iamDenied("当前身份不在有效核验名单")
	}
	user.IsActive = true
	if e = sessions.Validate(ctx, c, &user); e != nil {
		return nil, iamDenied("登录会话已失效")
	}
	valid, e := auth.VerifyPassword(user.PasswordHash, in.Password)
	if e != nil || !valid {
		return nil, iamDenied("身份核验失败")
	}
	var enabled bool
	var encrypted string
	var counter, version int64
	e = tx.QueryRow(ctx, `SELECT enabled,encrypted_secret,last_counter,version FROM sys_user_mfa WHERE user_id=$1 FOR UPDATE`, user.ID).Scan(&enabled, &encrypted, &counter, &version)
	if e != nil && e != pgx.ErrNoRows {
		return nil, e
	}
	if enabled {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte("dbhub-identity-mfa-v1"))
		cipher, e := appcrypto.NewCipher(mac.Sum(nil))
		if e != nil {
			return nil, e
		}
		secret, e := cipher.Decrypt(encrypted)
		if e != nil {
			return nil, iamDenied("身份核验失败")
		}
		prefix := fmt.Sprint(user.ID) + ":"
		if !strings.HasPrefix(secret, prefix) {
			return nil, iamDenied("身份核验失败")
		}
		next, valid := auth.VerifyGovernanceTOTP(strings.TrimPrefix(secret, prefix), in.Code, counter, time.Now())
		if !valid {
			return nil, iamDenied("需要新的动态验证码")
		}
		if e = consumeMFAFactor(ctx, tx, user.ID, version, next, ""); e != nil {
			return nil, e
		}
	}
	tag, e := tx.Exec(ctx, `INSERT INTO sys_iam_recovery_verifications(workspace_id,request_id,verifier_id,session_id,roster_version,auth_version) VALUES($1,$2::uuid,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, in.WorkspaceID, in.RequestID, user.ID, c.SessionID, rosterVersion, user.AuthVersion)
	if e != nil {
		return nil, e
	}
	if tag.RowsAffected() != 1 {
		return nil, iamConflict("此身份已核验本次申请，不能重复计票")
	}
	rows, e := tx.Query(ctx, `SELECT v.verifier_id,v.session_id FROM sys_iam_recovery_verifications v JOIN sys_iam_recovery_roster r ON r.workspace_id=v.workspace_id AND r.user_id=v.verifier_id JOIN sys_users u ON u.id=v.verifier_id WHERE v.workspace_id=$1 AND v.request_id=$2::uuid AND r.active AND r.version=v.roster_version AND u.is_active AND u.auth_version=v.auth_version AND v.verified_at>now()-interval '10 minutes'`, in.WorkspaceID, in.RequestID)
	if e != nil {
		return nil, e
	}
	type proof struct {
		uid int64
		sid string
	}
	proofs := []proof{}
	for rows.Next() {
		var p proof
		if e = rows.Scan(&p.uid, &p.sid); e != nil {
			break
		}
		proofs = append(proofs, p)
	}
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, p := range proofs {
		var u auth.User
		e = tx.QueryRow(ctx, `SELECT id,username,password_hash,auth_version FROM sys_users WHERE id=$1 AND is_active`, p.uid).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.AuthVersion)
		if e != nil {
			return nil, iamDenied("先前核验人的身份已失效")
		}
		if e = sessions.Validate(ctx, &auth.Claims{Subject: p.uid, SessionID: p.sid}, &u); e != nil {
			return nil, iamDenied("先前核验人的会话已失效")
		}
	}
	if len(proofs) >= 2 {
		if _, e = tx.Exec(ctx, `UPDATE sys_iam_recovery_requests SET state='applied' WHERE workspace_id=$1 AND request_id=$2::uuid`, in.WorkspaceID, in.RequestID); e != nil {
			return nil, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO sys_iam_recovery_grants(workspace_id,user_id,request_id,valid_until) VALUES($1,$2,$3::uuid,now()+interval '15 minutes')`, in.WorkspaceID, target, in.RequestID); e != nil {
			return nil, e
		}
		state = "applied"
		if e = iamBump(ctx, tx); e != nil {
			return nil, e
		}
	}
	// Each identity gets its own verification receipt; no reusable password hash
	// or factor is included in the command hash or audit payload.
	return iamCommit(ctx, tx, dashboardUUID(), "governance.recovery.verify", reportDigest(in.RequestID+fmt.Sprint(user.ID)), map[string]any{"request_id": in.RequestID, "state": state, "verified_count": len(proofs), "new_login_required": state == "applied", "business_permissions_restored": false})
}

func (r *UserRepository) iamRecoverySpaces(ctx context.Context) ([]Workspace, error) {
	c, _ := auth.ClaimsFromContext(ctx)
	if c == nil || c.SessionCreatedAt.IsZero() {
		return nil, nil
	}
	rows, e := r.pool.maintenance.Query(ctx, `SELECT w.id,w.name,s.config_version,s.authorization_epoch,g.request_id::text,g.valid_until FROM sys_iam_recovery_grants g JOIN sys_workspaces w ON w.id=g.workspace_id AND w.enabled JOIN sys_iam_workspace_state s ON s.workspace_id=w.id JOIN sys_users u ON u.id=g.user_id AND u.is_active WHERE g.user_id=$1 AND g.valid_until>now() AND g.issued_at<$2 AND (SELECT count(*) FROM sys_iam_recovery_verifications v JOIN sys_iam_recovery_roster rr ON rr.workspace_id=v.workspace_id AND rr.user_id=v.verifier_id JOIN sys_users vu ON vu.id=v.verifier_id WHERE v.workspace_id=g.workspace_id AND v.request_id=g.request_id AND rr.active AND rr.version=v.roster_version AND vu.is_active AND vu.auth_version=v.auth_version)>=2 ORDER BY g.valid_until DESC`, ActorID(ctx), c.SessionCreatedAt)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Workspace{}
	seen := map[int64]bool{}
	for rows.Next() {
		var w Workspace
		var request string
		var expiry time.Time
		if e = rows.Scan(&w.ID, &w.Name, &w.ConfigVersion, &w.AuthorizationEpoch, &request, &expiry); e != nil {
			return nil, e
		}
		if seen[w.ID] {
			continue
		}
		seen[w.ID] = true
		w.Version = 1
		w.PermissionMode = "GOVERNANCE_RECOVERY"
		w.IAM = &iam.Snapshot{Subject: ActorID(ctx), WorkspaceID: w.ID, MembershipVersion: 1, ConfigVersion: w.ConfigVersion, AuthorizationEpoch: w.AuthorizationEpoch, Mode: "GOVERNANCE_RECOVERY", Target: true, GovernanceOnly: true, Actions: recoveryActions(), ExpiresAt: &expiry, Sources: []iam.Source{{ID: request, Kind: "independent_recovery", RoleName: "临时治理恢复", Actions: recoveryActions(), ExpiresAt: &expiry}}}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (r *UserRepository) iamRecoveryCurrent(ctx context.Context) error {
	c, _ := auth.ClaimsFromContext(ctx)
	if c == nil || c.IAM == nil || !c.IAM.GovernanceOnly {
		return ErrManagedDenied
	}
	spaces, e := r.iamRecoverySpaces(ctx)
	if e != nil {
		return e
	}
	for _, w := range spaces {
		if w.ID == WorkspaceID(ctx) && w.ConfigVersion == c.IAM.ConfigVersion && w.AuthorizationEpoch == c.IAM.AuthorizationEpoch {
			return nil
		}
	}
	return ErrManagedDenied
}

func iamRecoveryCurrentTx(ctx context.Context, tx pgx.Tx) error {
	c, _ := auth.ClaimsFromContext(ctx)
	if c == nil || c.IAM == nil || c.SessionCreatedAt.IsZero() {
		return ErrManagedDenied
	}
	var valid bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_iam_recovery_grants g JOIN sys_workspaces w ON w.id=g.workspace_id AND w.enabled JOIN sys_users u ON u.id=g.user_id AND u.is_active JOIN sys_iam_workspace_state s ON s.workspace_id=w.id WHERE g.workspace_id=$1 AND g.user_id=$2 AND g.valid_until>now() AND g.issued_at<$3 AND s.config_version=$4 AND s.authorization_epoch=$5 AND (SELECT count(*) FROM sys_iam_recovery_verifications v JOIN sys_iam_recovery_roster rr ON rr.workspace_id=v.workspace_id AND rr.user_id=v.verifier_id JOIN sys_users vu ON vu.id=v.verifier_id WHERE v.workspace_id=g.workspace_id AND v.request_id=g.request_id AND rr.active AND rr.version=v.roster_version AND vu.is_active AND vu.auth_version=v.auth_version)>=2)`, WorkspaceID(ctx), ActorID(ctx), c.SessionCreatedAt, c.IAM.ConfigVersion, c.IAM.AuthorizationEpoch).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return ErrManagedDenied
	}
	return nil
}

func iamRecoveryDelegationTx(ctx context.Context, tx pgx.Tx, id string, version, delegate int64) (*iamDelegation, bool, error) {
	if version != 1 {
		return nil, false, nil
	}
	var expiry time.Time
	e := tx.QueryRow(ctx, `SELECT g.valid_until FROM sys_iam_recovery_grants g JOIN sys_users u ON u.id=g.user_id AND u.is_active WHERE g.workspace_id=$1 AND g.user_id=$2 AND g.request_id=$3::uuid AND g.valid_until>now() AND (SELECT count(*) FROM sys_iam_recovery_verifications v JOIN sys_iam_recovery_roster rr ON rr.workspace_id=v.workspace_id AND rr.user_id=v.verifier_id JOIN sys_users vu ON vu.id=v.verifier_id WHERE v.workspace_id=g.workspace_id AND v.request_id=g.request_id AND rr.active AND rr.version=v.roster_version AND vu.is_active AND vu.auth_version=v.auth_version)>=2`, WorkspaceID(ctx), delegate, id).Scan(&expiry)
	if e == pgx.ErrNoRows {
		return nil, false, nil
	}
	if e != nil {
		return nil, true, e
	}
	var targets []int64
	e = tx.QueryRow(ctx, `SELECT ARRAY(SELECT user_id FROM sys_workspace_members WHERE workspace_id=$1 AND dbhub_iam_qualified(user_id) ORDER BY user_id)`, WorkspaceID(ctx)).Scan(&targets)
	if e != nil {
		return nil, true, e
	}
	return &iamDelegation{ID: id, Version: 1, Delegate: delegate, Actions: []string{"role.propose", "role.review", "assignment.propose", "assignment.review", "menu.configure", "member.freeze", "restriction.revoke"}, Grantable: recoveryActions(), Targets: targets, Sources: []string{}, Until: expiry}, true, nil
}
