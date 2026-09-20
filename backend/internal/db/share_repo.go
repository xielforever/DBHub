package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ShareToken struct {
	ID          int64      `json:"id"`
	SubjectType string     `json:"subject_type"` // report | dashboard
	SubjectID   int64      `json:"subject_id"`
	TokenHash   string     `json:"-"`
	CreatedBy   int64      `json:"created_by"`
	ExpireAt    *time.Time `json:"expire_at"`
	AccessCount int        `json:"access_count"`
	Revoked     bool       `json:"revoked"`
	CreatedAt   time.Time  `json:"created_at"`
}

type ShareRepository struct {
	pool *pgxpool.Pool
}

func NewShareRepository(pool *pgxpool.Pool) *ShareRepository {
	return &ShareRepository{pool: pool}
}

func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (r *ShareRepository) Create(ctx context.Context, s *ShareToken, plainToken string) error {
	s.TokenHash = HashToken(plainToken)
	return r.pool.QueryRow(ctx, `
		INSERT INTO sys_share_tokens (subject_type, subject_id, token_hash, created_by, expire_at)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, access_count, revoked, created_at`,
		s.SubjectType, s.SubjectID, s.TokenHash, s.CreatedBy, s.ExpireAt,
	).Scan(&s.ID, &s.AccessCount, &s.Revoked, &s.CreatedAt)
}

func (r *ShareRepository) GetByHash(ctx context.Context, hash string) (*ShareToken, error) {
	s := &ShareToken{}
	err := r.pool.QueryRow(ctx, `
		SELECT id, subject_type, subject_id, token_hash, created_by, expire_at, access_count, revoked, created_at
		FROM sys_share_tokens WHERE token_hash=$1`, hash).Scan(
		&s.ID, &s.SubjectType, &s.SubjectID, &s.TokenHash, &s.CreatedBy, &s.ExpireAt, &s.AccessCount, &s.Revoked, &s.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return s, err
}

func (r *ShareRepository) ListBySubject(ctx context.Context, subjectType string, subjectID int64) ([]ShareToken, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, subject_type, subject_id, token_hash, created_by, expire_at, access_count, revoked, created_at
		FROM sys_share_tokens WHERE subject_type=$1 AND subject_id=$2 ORDER BY created_at DESC`, subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ShareToken{}
	for rows.Next() {
		var s ShareToken
		if err := rows.Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.TokenHash, &s.CreatedBy, &s.ExpireAt, &s.AccessCount, &s.Revoked, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *ShareRepository) IncrementAccess(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE sys_share_tokens SET access_count = access_count + 1 WHERE id=$1`, id)
	return err
}

func (r *ShareRepository) Revoke(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `UPDATE sys_share_tokens SET revoked=true WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *ShareRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sys_share_tokens WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("not found")
	}
	return nil
}

func (r *ShareRepository) IsExpired(s *ShareToken) bool {
	if s.Revoked {
		return true
	}
	if s.ExpireAt != nil && time.Now().After(*s.ExpireAt) {
		return true
	}
	return false
}
