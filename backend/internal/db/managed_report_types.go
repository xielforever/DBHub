package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	crypto "github.com/user/dbhub/pkg/crypto"
)

var ErrManagedConflict = errors.New("报表、审批或发布状态已变化，请刷新后核对")
var ErrManagedInput = errors.New("正式报表参数无效")
var ErrManagedDenied = errors.New("没有此报表操作权限，或相关账号与数据源已停用")

type ManagedReportRepository struct {
	pool   *pgxpool.Pool
	cipher *crypto.Cipher
}

func (r *ConnectionRepository) ManagedReports() *ManagedReportRepository {
	return &ManagedReportRepository{r.pool, r.cipher}
}

// ReportDefinition never contains personal connection IDs or authentication.
type ReportDefinition struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	DefinitionID int64           `json:"definition_revision_id"`
	Database     string          `json:"database_name"`
	SQL          string          `json:"sql_text"`
	ChartType    string          `json:"chart_type"`
	ChartConfig  json.RawMessage `json:"chart_config"`
}
type ManagedReport struct {
	ID              int64            `json:"id"`
	OwnerID         int64            `json:"owner_id"`
	OwnerName       string           `json:"owner_name"`
	Draft           ReportDefinition `json:"draft"`
	DraftVersion    int64            `json:"draft_version"`
	LatestVersion   int64            `json:"latest_version"`
	PublicationNo   int64            `json:"publication_no"`
	ActiveReleaseID *int64           `json:"active_release_id"`
	UpdatedAt       time.Time        `json:"updated_at"`
	ConnectionID    int64            `json:"connection_id"`
	PendingCount    int64            `json:"pending_count"`
	ActiveReviewID  *int64           `json:"active_review_id"`
	ActiveVersion   *int64           `json:"active_version"`
}
type ReportAccount struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	DefinitionID int64  `json:"definition_revision_id"`
	SourceName   string `json:"source_name"`
	Username     string `json:"username"`
	Enabled      bool   `json:"enabled"`
	Version      int64  `json:"version"`
	Generation   int64  `json:"credential_generation"`
	ReportCount  int64  `json:"report_count"`
}
type ReportVersion struct {
	ID            int64            `json:"id"`
	ReportID      int64            `json:"report_id"`
	Number        int64            `json:"number"`
	Definition    ReportDefinition `json:"definition"`
	ContentHash   string           `json:"content_hash"`
	SubmitterID   *int64           `json:"submitted_by"`
	SubmitterName string           `json:"submitter_name"`
	CreatedAt     time.Time        `json:"created_at"`
	ReviewID      int64            `json:"review_id"`
	Status        string           `json:"status"`
	DeciderName   string           `json:"decider_name"`
	Comment       string           `json:"comment"`
	AccountID     *int64           `json:"account_id"`
	AccountName   string           `json:"account_name"`
	Manifest      *ReportManifest  `json:"manifest,omitempty"`
	WasPublished  bool             `json:"was_published"`
}
type ReportManifest struct {
	VersionID    int64   `json:"version_id"`
	ContentHash  string  `json:"content_hash"`
	AccountID    int64   `json:"account_id"`
	DefinitionID int64   `json:"definition_revision_id"`
	UserIDs      []int64 `json:"user_ids"`
	GroupIDs     []int64 `json:"group_ids"`
	MaxRows      int     `json:"max_rows"`
	TimeoutMS    int     `json:"timeout_ms"`
}
type ReportRelease struct {
	ID        int64     `json:"id"`
	Number    int64     `json:"number"`
	Kind      string    `json:"kind"`
	ReviewID  *int64    `json:"review_id"`
	Version   *int64    `json:"version"`
	Performer string    `json:"performer_name"`
	CreatedAt time.Time `json:"created_at"`
}
type PublishedReport struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Version       int64           `json:"version"`
	PublicationNo int64           `json:"publication_no"`
	ChartType     string          `json:"chart_type"`
	ChartConfig   json.RawMessage `json:"chart_config"`
}

// ManagedExecution is internal. Never serialize credentials or a frozen SQL
// definition into a viewer response, execution receipt, or ordinary history.
type ManagedExecution struct {
	ReportID, Version, PublicationNo, ReviewID, AccountID int64
	AccountVersion, Generation                            int64
	Manifest                                              ReportManifest
	Hash                                                  string
	Definition                                            ReportDefinition
	Connection                                            *Connection
	Password                                              string
	Validation                                            bool
	UserIDs, GroupIDs                                     []int64
	Author, Approver, Publisher                           string
}
type ReportCommandResult struct {
	ID      int64 `json:"id"`
	Version int64 `json:"version,omitempty"`
}

func canonicalReportJSON(value any) []byte {
	b, _ := json.Marshal(value)
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var normalized any
	_ = dec.Decode(&normalized)
	out, _ := json.Marshal(normalized)
	return out
}
func reportDigest(value any) string {
	b := canonicalReportJSON(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func managedError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func reportActor(ctx context.Context, tx pgx.Tx, admin, writer bool) error {
	var role string
	if e := tx.QueryRow(ctx, `SELECT r.code FROM sys_users u JOIN sys_roles r ON r.id=u.role_id WHERE u.id=$1 AND u.is_active FOR SHARE OF u`, ActorID(ctx)).Scan(&role); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrManagedDenied
		}
		return e
	}
	if admin && role != "admin" || writer && role != "admin" && role != "developer" {
		return ErrManagedDenied
	}
	return nil
}
func (r *ManagedReportRepository) command(ctx context.Context, key, op string, input any, admin, writer bool, fn func(pgx.Tx) (ReportCommandResult, error)) (ReportCommandResult, error) {
	var result ReportCommandResult
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return result, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if e = reportActor(ctx, tx, admin, writer); e != nil {
		return result, e
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("report-command:%d:%s", ActorID(ctx), key)); e != nil {
		return result, e
	}
	hash := r.cipher.Fingerprint(canonicalReportJSON(input))
	var previousOp, previousHash string
	var raw []byte
	e = tx.QueryRow(ctx, `SELECT operation,request_hash,result FROM sys_report_commands WHERE actor_id=$1 AND request_id=$2::uuid`, ActorID(ctx), key).Scan(&previousOp, &previousHash, &raw)
	if e == nil {
		if op != previousOp || hash != previousHash {
			return result, ErrManagedConflict
		}
		e = json.Unmarshal(raw, &result)
		return result, e
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return result, e
	}
	result, e = fn(tx)
	if e != nil {
		return result, managedError(e)
	}
	raw, e = json.Marshal(result)
	if e != nil {
		return result, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO sys_report_commands(actor_id,request_id,operation,request_hash,result) VALUES($1,$2::uuid,$3,$4,$5::jsonb)`, ActorID(ctx), key, op, hash, string(raw))
	if e != nil {
		return result, e
	}
	return result, tx.Commit(ctx)
}
func audience(ctx context.Context, tx pgx.Tx, users, groups []int64) error {
	if !validateSubjects(users) || !validateSubjects(groups) || len(users)+len(groups) == 0 {
		return fmt.Errorf("%w: 至少选择一个有效用户或用户组", ErrManagedInput)
	}
	var valid bool
	e := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM sys_users WHERE id=ANY($1::bigint[]) AND is_active)=cardinality($1::bigint[]) AND (SELECT count(*) FROM sys_user_groups WHERE id=ANY($2::bigint[]))=cardinality($2::bigint[])`, nonNilIDs(users), nonNilIDs(groups)).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return fmt.Errorf("%w: 用户已停用、不存在或用户组不存在", ErrManagedInput)
	}
	return nil
}
func normalizedIDs(ids []int64) []int64 {
	out := append([]int64{}, ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
