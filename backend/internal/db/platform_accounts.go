package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auth"
)

const platformCredentialLifetime = 15 * time.Minute

var (
	ErrPlatformCredentialInvalid = errors.New("凭据无效、已使用或已过期")
	ErrPlatformAccountAudit      = errors.New("身份审计暂不可用")
	platformUsername             = regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}$")
	platformCredentialInReason   = regexp.MustCompile("[a-zA-Z0-9_-]{43}")
)

// PlatformAccount describes an identity without including credentials or membership data.
type PlatformAccount struct {
	ID              int64      `json:"id"`
	Username        string     `json:"username"`
	Email           string     `json:"email"`
	Status          string     `json:"status"`
	IsPlatformAdmin bool       `json:"is_platform_admin"`
	LocalLogin      bool       `json:"local_login"`
	SSOLogin        bool       `json:"sso_login"`
	MFAEnabled      bool       `json:"mfa_enabled"`
	LastLoginAt     *time.Time `json:"last_login_at"`
	CreatedAt       time.Time  `json:"created_at"`
	Version         int64      `json:"version"`
}

// PlatformAccountCredential is returned only by the issuing command.
type PlatformAccountCredential struct {
	Token     string    `json:"token"`
	Purpose   string    `json:"purpose"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PlatformAccountEnrollment returns the issued credential with the current account version.
type PlatformAccountEnrollment struct {
	Account    *PlatformAccount          `json:"account"`
	Credential PlatformAccountCredential `json:"credential"`
}

const platformAccountProjection = `SELECT u.id,u.username,COALESCE(u.email,'') AS email,
	CASE WHEN u.is_active THEN 'active'
		WHEN u.platform_account_managed AND u.password_hash='' THEN 'pending'
		ELSE 'inactive' END AS status,
	u.is_platform_admin,u.password_hash<>'' AS local_login,
	EXISTS(SELECT 1 FROM sys_external_identities e WHERE e.user_id=u.id AND e.enabled) AS sso_login,
	EXISTS(SELECT 1 FROM sys_user_mfa m WHERE m.user_id=u.id AND m.enabled) AS mfa_enabled,
	u.last_login_at,u.created_at,u.platform_account_version AS version
	FROM sys_users u WHERE u.workspace_account_id IS NULL`

type platformAccountScanner interface {
	Scan(...any) error
}

func scanPlatformAccount(row platformAccountScanner) (*PlatformAccount, error) {
	var a PlatformAccount
	err := row.Scan(&a.ID, &a.Username, &a.Email, &a.Status, &a.IsPlatformAdmin, &a.LocalLogin,
		&a.SSOLogin, &a.MFAEnabled, &a.LastLoginAt, &a.CreatedAt, &a.Version)
	return &a, err
}

func platformAccountActor(ctx context.Context, tx pgx.Tx) error {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT is_active AND is_platform_admin FROM sys_users
		WHERE id=$1 AND workspace_account_id IS NULL FOR SHARE`, ActorID(ctx)).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !allowed) {
		return ErrManagedDenied
	}
	return err
}

type platformAccountState struct {
	user    auth.User
	version int64
	managed bool
	admin   bool
}

func platformAccountForUpdate(ctx context.Context, tx pgx.Tx, id int64) (platformAccountState, error) {
	var a platformAccountState
	a.user.ID = id
	err := tx.QueryRow(ctx, `SELECT password_hash,auth_version,platform_account_version,platform_account_managed,
		is_active,is_platform_admin FROM sys_users WHERE id=$1 AND workspace_account_id IS NULL FOR UPDATE`, id).
		Scan(&a.user.PasswordHash, &a.user.AuthVersion, &a.version, &a.managed, &a.user.IsActive, &a.admin)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (a platformAccountState) status() string {
	if a.user.IsActive {
		return "active"
	}
	if a.managed && a.user.PasswordHash == "" {
		return "pending"
	}
	return "inactive"
}

func (a platformAccountState) permitsCredential(purpose string) bool {
	return purpose == "activate" && a.status() == "pending" ||
		purpose == "reset" && a.user.IsActive && a.user.PasswordHash != ""
}

func normalizePlatformAccountReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if !utf8.ValidString(reason) || reason == "" || utf8.RuneCountInString(reason) > 300 ||
		strings.ContainsFunc(reason, unicode.IsControl) {
		return "", ErrManagedInput
	}
	return platformCredentialInReason.ReplaceAllString(reason, "[已遮盖]"), nil
}

func platformAccountAudit(ctx context.Context, operation string, target int64, reason, before, after string) error {
	auditctx.Update(ctx, func(d *auditctx.Data) {
		d.CommandOperation = operation
		d.PlatformAccountID = target
		d.PlatformAccountReason = reason
		d.PlatformAccountPreviousStatus = before
		d.PlatformAccountStatus = after
	})
	if err := auditctx.Checkpoint(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrPlatformAccountAudit, err)
	}
	return nil
}

// Serialize platform owner mutations before locking actor and target accounts.
// This also prevents two owners from concurrently disabling each other.
func (r *UserRepository) beginPlatformAccountCommand(ctx context.Context) (pgx.Tx, error) {
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':platform-account',0))`); err == nil {
		err = platformAccountActor(ctx, tx)
	}
	if err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, err
	}
	return tx, nil
}

// CreatePlatformAccount enrolls an inactive identity without creating workspace membership.
func (r *UserRepository) CreatePlatformAccount(ctx context.Context, username, email, reason string) (*PlatformAccountEnrollment, error) {
	reason, err := normalizePlatformAccountReason(reason)
	if err != nil || !platformUsername.MatchString(username) || len(email) > 255 {
		return nil, ErrManagedInput
	}
	tx, err := r.beginPlatformAccountCommand(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO sys_users(username,email,password_hash,role_id,is_active,platform_account_managed)
		SELECT $1,NULLIF($2,''),'',id,false,true FROM sys_roles WHERE code='readonly'
		RETURNING sys_users.id`, username, email).Scan(&id)
	var dbErr *pgconn.PgError
	if errors.As(err, &dbErr) && dbErr.Code == "23505" {
		return nil, ErrManagedConflict
	}
	if err != nil {
		return nil, err
	}
	if err = platformAccountAudit(ctx, "platform-account.create", id, reason, "", "pending"); err != nil {
		return nil, err
	}
	return issuePlatformCredential(ctx, tx, id, auth.PasswordVersion(&auth.User{}), "activate")
}

// IssuePlatformAccountCredential replaces the account's previous unused credential.
func (r *UserRepository) IssuePlatformAccountCredential(ctx context.Context, id, version int64, purpose, reason string) (*PlatformAccountEnrollment, error) {
	reason, err := normalizePlatformAccountReason(reason)
	if err != nil || version <= 0 || (purpose != "activate" && purpose != "reset") {
		return nil, ErrManagedInput
	}
	tx, err := r.beginPlatformAccountCommand(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	a, err := managedPlatformAccountTarget(ctx, tx, id, version)
	if err != nil {
		return nil, err
	}
	if !a.permitsCredential(purpose) {
		return nil, ErrManagedConflict
	}
	if err = platformAccountAudit(ctx, "platform-account.issue-"+purpose, id, reason, a.status(), a.status()); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_users SET platform_account_version=platform_account_version+1 WHERE id=$1`, id); err != nil {
		return nil, err
	}
	return issuePlatformCredential(ctx, tx, id, auth.PasswordVersion(&a.user), purpose)
}

func issuePlatformCredential(ctx context.Context, tx pgx.Tx, id int64, passwordVersion, purpose string) (*PlatformAccountEnrollment, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256(raw)
	credential := PlatformAccountCredential{Token: token, Purpose: purpose}
	err := tx.QueryRow(ctx, `INSERT INTO sys_platform_account_credentials(user_id,token_hash,password_version,purpose,expires_at)
		VALUES($1,$2,$3,$4,clock_timestamp()+$5::double precision*interval '1 second')
		ON CONFLICT (user_id) DO UPDATE SET token_hash=EXCLUDED.token_hash,password_version=EXCLUDED.password_version,
			purpose=EXCLUDED.purpose,created_at=EXCLUDED.created_at,expires_at=EXCLUDED.expires_at,used_at=NULL
		RETURNING expires_at`, id, hex.EncodeToString(digest[:]), passwordVersion, purpose, int64(platformCredentialLifetime/time.Second)).
		Scan(&credential.ExpiresAt)
	if err != nil {
		return nil, err
	}
	account, err := commitPlatformAccount(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return &PlatformAccountEnrollment{Account: account, Credential: credential}, nil
}

// RedeemPlatformAccountCredential consumes a one-time proof and changes the
// password in the same transaction. Input secrets never enter the audit context.
func (r *UserRepository) RedeemPlatformAccountCredential(ctx context.Context, token, password string) (string, error) {
	if len(token) != 43 {
		return "", ErrPlatformCredentialInvalid
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 32 {
		return "", ErrPlatformCredentialInvalid
	}
	if len(password) > 4096 || !utf8.ValidString(password) || utf8.RuneCountInString(password) < 15 ||
		strings.TrimSpace(password) == "" {
		return "", ErrManagedInput
	}
	digest := sha256.Sum256(raw)
	key := hex.EncodeToString(digest[:])
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var id int64
	err = tx.QueryRow(ctx, `SELECT user_id FROM sys_platform_account_credentials
		WHERE token_hash=$1 AND used_at IS NULL AND expires_at>clock_timestamp()`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPlatformCredentialInvalid
	}
	if err != nil {
		return "", err
	}
	// Lock the account before consuming the credential, the same order as issue.
	a, err := platformAccountForUpdate(ctx, tx, id)
	if errors.Is(err, ErrNotFound) {
		return "", ErrPlatformCredentialInvalid
	}
	if err != nil {
		return "", err
	}
	var purpose string
	err = tx.QueryRow(ctx, `UPDATE sys_platform_account_credentials SET used_at=clock_timestamp()
		WHERE user_id=$1 AND token_hash=$2 AND password_version=$3 AND used_at IS NULL
		AND expires_at>clock_timestamp() RETURNING purpose`, id, key, auth.PasswordVersion(&a.user)).Scan(&purpose)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPlatformCredentialInvalid
	}
	if err != nil {
		return "", err
	}
	if !a.permitsCredential(purpose) {
		return "", ErrPlatformCredentialInvalid
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}
	if err = platformAccountAudit(ctx, "platform-account.redeem-"+purpose, id, "", a.status(), "active"); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_users SET password_hash=$2,is_active=true,auth_version=auth_version+1,
		platform_account_version=platform_account_version+1 WHERE id=$1`, id, hash); err != nil {
		return "", err
	}
	return purpose, tx.Commit(ctx)
}

func managedPlatformAccountTarget(ctx context.Context, tx pgx.Tx, id, version int64) (platformAccountState, error) {
	if id <= 0 || version <= 0 {
		return platformAccountState{}, ErrManagedInput
	}
	if id == ActorID(ctx) {
		return platformAccountState{}, ErrManagedDenied
	}
	a, err := platformAccountForUpdate(ctx, tx, id)
	if err == nil && a.version != version {
		return a, ErrManagedConflict
	}
	return a, err
}

func commitPlatformAccount(ctx context.Context, tx pgx.Tx, id int64) (*PlatformAccount, error) {
	account, err := scanPlatformAccount(tx.QueryRow(ctx, platformAccountProjection+" AND u.id=$1", id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return account, nil
}

// SetPlatformAccountStatus changes global sign-in availability and invalidates older sessions.
func (r *UserRepository) SetPlatformAccountStatus(ctx context.Context, id, version int64, active bool, reason string) (*PlatformAccount, error) {
	reason, err := normalizePlatformAccountReason(reason)
	if err != nil {
		return nil, err
	}
	tx, err := r.beginPlatformAccountCommand(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	a, err := managedPlatformAccountTarget(ctx, tx, id, version)
	if err != nil {
		return nil, err
	}
	if a.status() == "pending" {
		return nil, ErrManagedConflict
	}
	if a.admin && !active {
		var otherAdmin bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_users
			WHERE id<>$1 AND workspace_account_id IS NULL AND is_active AND is_platform_admin)`, id).Scan(&otherAdmin)
		if err != nil {
			return nil, err
		}
		if !otherAdmin {
			return nil, ErrManagedDenied
		}
	}
	operation, state := "platform-account.disable", "inactive"
	if active {
		operation, state = "platform-account.enable", "active"
	}
	if err = platformAccountAudit(ctx, operation, id, reason, a.status(), state); err != nil {
		return nil, err
	}
	if a.user.IsActive != active {
		if _, err = tx.Exec(ctx, `UPDATE sys_users SET is_active=$2,auth_version=auth_version+1,
			platform_account_version=platform_account_version+1 WHERE id=$1`, id, active); err != nil {
			return nil, err
		}
	}
	return commitPlatformAccount(ctx, tx, id)
}

// RevokePlatformAccountSessions invalidates pre-existing sessions without changing activation.
func (r *UserRepository) RevokePlatformAccountSessions(ctx context.Context, id, version int64, reason string) (*PlatformAccount, error) {
	reason, err := normalizePlatformAccountReason(reason)
	if err != nil {
		return nil, err
	}
	tx, err := r.beginPlatformAccountCommand(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	a, err := managedPlatformAccountTarget(ctx, tx, id, version)
	if err != nil {
		return nil, err
	}
	if !a.user.IsActive {
		return nil, ErrManagedConflict
	}
	if err = platformAccountAudit(ctx, "platform-account.revoke-sessions", id, reason, a.status(), a.status()); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_users SET auth_version=auth_version+1,
		platform_account_version=platform_account_version+1 WHERE id=$1`, id); err != nil {
		return nil, err
	}
	return commitPlatformAccount(ctx, tx, id)
}
