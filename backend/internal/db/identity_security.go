package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auth"
)

func (r *UserRepository) MFAState(ctx context.Context, uid int64) (*auth.MFAState, error) {
	s := &auth.MFAState{LastCounter: -1}
	err := r.pool.maintenance.QueryRow(ctx, `SELECT enabled,encrypted_secret,last_counter,cardinality(recovery_hashes),COALESCE(pending_id::text,''),encrypted_pending,pending_expires,version FROM sys_user_mfa WHERE user_id=$1`, uid).Scan(&s.Enabled, &s.EncryptedSecret, &s.LastCounter, &s.RecoveryCount, &s.PendingID, &s.EncryptedPending, &s.PendingExpires, &s.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	return s, err
}
func identityUserLock(ctx context.Context, tx pgx.Tx, u *auth.User) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT is_active AND password_hash=$2 AND auth_version=$3 FROM sys_users WHERE id=$1 FOR UPDATE`, u.ID, u.PasswordHash, u.AuthVersion).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !valid {
		return auth.ErrMFAInvalid
	}
	return err
}
func (r *UserRepository) StartMFASetup(ctx context.Context, u *auth.User, id, encrypted string, expires time.Time) error {
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = identityUserLock(ctx, tx, u); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO sys_user_mfa(user_id,pending_id,encrypted_pending,pending_expires,version) VALUES($1,$2::uuid,$3,$4,1) ON CONFLICT(user_id) DO UPDATE SET pending_id=$2::uuid,encrypted_pending=$3,pending_expires=$4,version=sys_user_mfa.version+1,updated_at=now() WHERE NOT sys_user_mfa.enabled`, u.ID, id, encrypted, expires)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return auth.ErrMFAInvalid
	}
	return tx.Commit(ctx)
}
func (r *UserRepository) EnableMFA(ctx context.Context, u *auth.User, id string, version, counter int64, hashes []string) error {
	if len(hashes) != 10 || counter < 0 {
		return auth.ErrMFAInvalid
	}
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = identityUserLock(ctx, tx, u); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE sys_user_mfa SET enabled=true,encrypted_secret=encrypted_pending,encrypted_pending='',pending_id=NULL,pending_expires=NULL,last_counter=$4,recovery_hashes=$5,version=version+1,updated_at=now() WHERE user_id=$1 AND pending_id=$2::uuid AND version=$3 AND NOT enabled AND encrypted_pending<>'' AND pending_expires>now()`, u.ID, id, version, counter, hashes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return auth.ErrMFAInvalid
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_users SET auth_version=auth_version+1 WHERE id=$1`, u.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *UserRepository) CreateMFAChallenge(ctx context.Context, u *auth.User, hash string, expires time.Time) error {
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = identityUserLock(ctx, tx, u); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sys_mfa_challenges WHERE token_hash IN(SELECT token_hash FROM sys_mfa_challenges WHERE expires_at<now() ORDER BY expires_at LIMIT 500)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_mfa_challenges SET used_at=now() WHERE user_id=$1 AND used_at IS NULL AND token_hash IN(SELECT token_hash FROM sys_mfa_challenges WHERE user_id=$1 AND used_at IS NULL ORDER BY created_at DESC OFFSET 2)`, u.ID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO sys_mfa_challenges(token_hash,user_id,password_version,expires_at) SELECT $1,$2,$3,$4 WHERE EXISTS(SELECT 1 FROM sys_user_mfa WHERE user_id=$2 AND enabled)`, hash, u.ID, auth.PasswordVersion(u), expires)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return auth.ErrMFAInvalid
	}
	return tx.Commit(ctx)
}
func (r *UserRepository) AttemptMFAChallenge(ctx context.Context, hash string) (*auth.MFAChallenge, error) {
	value := &auth.MFAChallenge{}
	err := r.pool.maintenance.QueryRow(ctx, `UPDATE sys_mfa_challenges c SET attempts=attempts+1 FROM sys_users u WHERE c.token_hash=$1 AND c.user_id=u.id AND u.is_active AND c.used_at IS NULL AND c.expires_at>now() AND c.attempts<5 RETURNING u.username,u.id,c.password_version`, hash).Scan(&value.Username, &value.UserID, &value.PasswordVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrMFAInvalid
	}
	return value, err
}
func consumeMFAFactor(ctx context.Context, tx pgx.Tx, uid, version, counter int64, recovery string) error {
	tag, err := tx.Exec(ctx, `UPDATE sys_user_mfa SET last_counter=CASE WHEN $3>=0 THEN $3 ELSE last_counter END,recovery_hashes=array_remove(recovery_hashes,$4),version=version+1,updated_at=now() WHERE user_id=$1 AND enabled AND version=$2 AND (($3>=0 AND $3>last_counter AND $4='') OR ($3=-1 AND $4<>'' AND $4=ANY(recovery_hashes)))`, uid, version, counter, recovery)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return auth.ErrMFAInvalid
	}
	return nil
}
func (r *UserRepository) ConsumeMFAChallenge(ctx context.Context, u *auth.User, hash string, version, counter int64, recovery string) error {
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = identityUserLock(ctx, tx, u); err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT used_at IS NULL AND expires_at>now() AND attempts BETWEEN 1 AND 5 AND password_version=$3 FROM sys_mfa_challenges WHERE token_hash=$1 AND user_id=$2 FOR UPDATE`, hash, u.ID, auth.PasswordVersion(u)).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !valid {
		return auth.ErrMFAInvalid
	}
	if err != nil {
		return err
	}
	if err = consumeMFAFactor(ctx, tx, u.ID, version, counter, recovery); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_mfa_challenges SET used_at=now() WHERE token_hash=$1`, hash); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *UserRepository) DisableMFA(ctx context.Context, u *auth.User, version, counter int64, recovery string) error {
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = identityUserLock(ctx, tx, u); err != nil {
		return err
	}
	if err = consumeMFAFactor(ctx, tx, u.ID, version, counter, recovery); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_user_mfa SET enabled=false,encrypted_secret='',recovery_hashes='{}',encrypted_pending='',pending_id=NULL,pending_expires=NULL,version=version+1,updated_at=now() WHERE user_id=$1`, u.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_users SET auth_version=auth_version+1 WHERE id=$1`, u.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
