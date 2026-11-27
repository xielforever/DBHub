package db

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrBusinessMappingDenied   = errors.New("当前空间没有业务映射管理权限")
	ErrBusinessMappingInput    = errors.New("业务映射参数无效")
	ErrBusinessMappingConflict = errors.New("业务键重复，或成员、数据集、账号版本或状态已变化，请重新核对")
)

var businessDatasetKey = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)

// BusinessMappingSource identifies the authority, revision, and validity window.
type BusinessMappingSource struct {
	Authority     string    `json:"authority"`
	SourceVersion string    `json:"source_version"`
	ValidFrom     time.Time `json:"valid_from"`
	ValidUntil    time.Time `json:"valid_until"`
}

func (s BusinessMappingSource) valid(expected *int64) bool {
	return expected != nil && *expected >= 0 && validBusinessText(s.Authority) && validBusinessText(s.SourceVersion) &&
		!s.ValidFrom.IsZero() && s.ValidUntil.Truncate(time.Microsecond).After(s.ValidFrom.Truncate(time.Microsecond))
}

func validBusinessText(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) &&
		utf8.RuneCountInString(value) <= 128 && strings.IndexFunc(value, unicode.IsControl) == -1
}

// BusinessDatasetInput pins a reviewed version of a managed execution account.
type BusinessDatasetInput struct {
	BusinessMappingSource
	ExpectedVersion *int64 `json:"expected_version"`
	AccountID       int64  `json:"account_id"`
	AccountVersion  int64  `json:"account_version"`
}

// BusinessDataset is workspace-scoped metadata without connection credentials.
type BusinessDataset struct {
	BusinessMappingSource
	Key                  string `json:"key"`
	Version              int64  `json:"version"`
	AccountID            int64  `json:"account_id"`
	AccountVersion       int64  `json:"account_version"`
	DefinitionRevisionID int64  `json:"definition_revision_id"`
	Available            bool   `json:"available"`
}

// BusinessDatasetVersion records the explicit version confirmed for a member.
type BusinessDatasetVersion struct {
	Key     string `json:"key"`
	Version int64  `json:"version"`
}

// BusinessIdentityInput replaces the member's entire mapping in one transaction.
type BusinessIdentityInput struct {
	BusinessMappingSource
	ExpectedVersion *int64                   `json:"expected_version"`
	BusinessKey     string                   `json:"business_key"`
	Datasets        []BusinessDatasetVersion `json:"datasets"`
}

// BusinessDatasetMapping reports whether the recorded binding still resolves.
type BusinessDatasetMapping struct {
	BusinessDatasetVersion
	Available bool `json:"available"`
}

// BusinessIdentity returns the current business identity and its dataset bindings.
type BusinessIdentity struct {
	BusinessMappingSource
	UserID      int64                    `json:"user_id"`
	BusinessKey string                   `json:"business_key"`
	Version     int64                    `json:"version"`
	Available   bool                     `json:"available"`
	Datasets    []BusinessDatasetMapping `json:"datasets"`
}

func (r *UserRepository) beginBusinessMapping(ctx context.Context, snapshot bool) (pgx.Tx, error) {
	if WorkspaceID(ctx) <= 0 || !ActorAdmin(ctx) || !ActorAction(ctx, "source.definition.manage", ActorCapability(ctx, "datasource_manage")) {
		return nil, ErrBusinessMappingDenied
	}
	begin := r.pool.Begin
	if snapshot {
		begin = r.pool.BeginSnapshot
	}
	tx, err := begin(ctx)
	if err != nil {
		return nil, err
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT dbhub_scope_allowed() AND dbhub_member_role(dbhub_actor()) = 'admin'
		AND dbhub_has_capability(dbhub_actor(), 'datasource_manage')`).Scan(&allowed)
	if err != nil || !allowed {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		if err != nil {
			return nil, err
		}
		return nil, ErrBusinessMappingDenied
	}
	return tx, nil
}

const businessAccountSource = `
	FROM sys_report_accounts a
	JOIN sys_source_definitions s ON s.id = a.definition_revision_id AND s.workspace_id = a.workspace_id
	WHERE a.enabled AND s.enabled AND s.visibility = 'global'
	    AND (s.template_revision_id IS NULL OR EXISTS (
	        SELECT 1 FROM sys_datasource_templates t JOIN sys_jdbc_drivers j ON j.id = t.driver_id
	        WHERE t.id = s.template_revision_id AND t.enabled AND j.enabled
	            AND t.visibility = 'global' AND j.visibility = 'global'))`

const businessDatasetAccountCurrent = `
	EXISTS (SELECT 1` + businessAccountSource + `
	    AND a.workspace_id = d.workspace_id AND a.id = d.account_id
	    AND a.version = d.account_version AND s.id = d.definition_revision_id)`

const businessDatasetAvailable = `
	d.valid_from <= statement_timestamp() AND statement_timestamp() < d.valid_until
	AND ` + businessDatasetAccountCurrent

func readBusinessDataset(ctx context.Context, tx pgx.Tx, key string) (BusinessDataset, error) {
	var out BusinessDataset
	err := tx.QueryRow(ctx, `
		SELECT d.dataset_key, d.version, d.authority, d.source_version, d.valid_from, d.valid_until,
		       d.account_id, d.account_version, d.definition_revision_id, `+businessDatasetAvailable+`
		FROM sys_business_datasets d
		WHERE d.workspace_id = dbhub_workspace() AND d.dataset_key = $1`, key).Scan(
		&out.Key, &out.Version, &out.Authority, &out.SourceVersion, &out.ValidFrom, &out.ValidUntil,
		&out.AccountID, &out.AccountVersion, &out.DefinitionRevisionID, &out.Available)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}

// BusinessDataset reads the registered dataset in the current workspace.
func (r *UserRepository) BusinessDataset(ctx context.Context, key string) (BusinessDataset, error) {
	if !businessDatasetKey.MatchString(key) {
		return BusinessDataset{}, ErrBusinessMappingInput
	}
	tx, err := r.beginBusinessMapping(ctx, false)
	if err != nil {
		return BusinessDataset{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	return readBusinessDataset(ctx, tx, key)
}

// SaveBusinessDataset writes only against the reviewed registry and account versions.
func (r *UserRepository) SaveBusinessDataset(ctx context.Context, key string, in BusinessDatasetInput) (BusinessDataset, error) {
	if !businessDatasetKey.MatchString(key) || !in.valid(in.ExpectedVersion) || in.AccountID <= 0 || in.AccountVersion <= 0 {
		return BusinessDataset{}, ErrBusinessMappingInput
	}
	tx, err := r.beginBusinessMapping(ctx, false)
	if err != nil {
		return BusinessDataset{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = lockBusinessReportPolicy(ctx, tx, key); err != nil {
		return BusinessDataset{}, err
	}
	var version int64
	err = tx.QueryRow(ctx, `
		INSERT INTO sys_business_datasets
		    (workspace_id, dataset_key, account_id, account_version, definition_revision_id,
		     authority, source_version, valid_from, valid_until)
		SELECT dbhub_workspace(), $1, a.id, a.version, s.id, $5, $6, $7, $8`+businessAccountSource+`
		AND a.workspace_id = dbhub_workspace() AND a.id = $3 AND a.version = $4
		AND ($2 = 0 OR EXISTS (SELECT 1 FROM sys_business_datasets
		    WHERE workspace_id = dbhub_workspace() AND dataset_key = $1 AND version = $2))
		ON CONFLICT (workspace_id, dataset_key) DO UPDATE SET
		    account_id = EXCLUDED.account_id, account_version = EXCLUDED.account_version,
		    definition_revision_id = EXCLUDED.definition_revision_id,
		    authority = EXCLUDED.authority, source_version = EXCLUDED.source_version,
		    valid_from = EXCLUDED.valid_from, valid_until = EXCLUDED.valid_until,
		    version = sys_business_datasets.version + 1
		WHERE sys_business_datasets.version = $2
		RETURNING version`, key, *in.ExpectedVersion, in.AccountID, in.AccountVersion,
		in.Authority, in.SourceVersion, in.ValidFrom, in.ValidUntil).Scan(&version)
	if err != nil {
		return BusinessDataset{}, mapBusinessWriteError(err)
	}
	out, err := readBusinessDataset(ctx, tx, key)
	if err != nil {
		return BusinessDataset{}, err
	}
	return out, tx.Commit(ctx)
}

const businessIdentityAvailable = `
	i.valid_from <= statement_timestamp() AND statement_timestamp() < i.valid_until
	AND m.version = i.member_version AND dbhub_member_role(i.user_id) IS NOT NULL`

func readBusinessIdentity(ctx context.Context, tx pgx.Tx, userID int64) (BusinessIdentity, error) {
	var out BusinessIdentity
	err := tx.QueryRow(ctx, `
		SELECT i.user_id, i.business_key, i.version, i.authority, i.source_version, i.valid_from, i.valid_until,
		       (`+businessIdentityAvailable+`), COALESCE((
		    SELECT jsonb_agg(jsonb_build_object('key', b.dataset_key, 'version', b.dataset_version,
		        'available', ((`+businessIdentityAvailable+`) AND d.version = b.dataset_version AND (`+businessDatasetAvailable+`)))
		        ORDER BY b.dataset_key)
		    FROM sys_business_identity_datasets b
		    JOIN sys_business_datasets d ON d.workspace_id = b.workspace_id AND d.dataset_key = b.dataset_key
		    WHERE b.workspace_id = i.workspace_id AND b.user_id = i.user_id
		), '[]'::jsonb)
		FROM sys_business_identities i
		JOIN sys_workspace_members m ON m.workspace_id = i.workspace_id AND m.user_id = i.user_id
		WHERE i.workspace_id = dbhub_workspace() AND i.user_id = $1`, userID).Scan(
		&out.UserID, &out.BusinessKey, &out.Version, &out.Authority, &out.SourceVersion,
		&out.ValidFrom, &out.ValidUntil, &out.Available, &out.Datasets)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}

// BusinessIdentity reads the member's explicit bindings and current availability.
func (r *UserRepository) BusinessIdentity(ctx context.Context, userID int64) (BusinessIdentity, error) {
	if userID <= 0 {
		return BusinessIdentity{}, ErrBusinessMappingInput
	}
	tx, err := r.beginBusinessMapping(ctx, false)
	if err != nil {
		return BusinessIdentity{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	return readBusinessIdentity(ctx, tx, userID)
}

// SaveBusinessIdentity records the explicit identity and replaces its dataset bindings atomically.
func (r *UserRepository) SaveBusinessIdentity(ctx context.Context, userID int64, in BusinessIdentityInput) (BusinessIdentity, error) {
	if userID <= 0 || !in.valid(in.ExpectedVersion) || !validBusinessText(in.BusinessKey) || in.Datasets == nil || len(in.Datasets) > 100 {
		return BusinessIdentity{}, ErrBusinessMappingInput
	}
	keys := make(map[string]struct{}, len(in.Datasets))
	for _, dataset := range in.Datasets {
		_, duplicate := keys[dataset.Key]
		if duplicate || !businessDatasetKey.MatchString(dataset.Key) || dataset.Version <= 0 {
			return BusinessIdentity{}, ErrBusinessMappingInput
		}
		keys[dataset.Key] = struct{}{}
	}
	datasets, err := json.Marshal(in.Datasets)
	if err != nil {
		return BusinessIdentity{}, err
	}
	tx, err := r.beginBusinessMapping(ctx, false)
	if err != nil {
		return BusinessIdentity{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var memberVersion int64
	err = tx.QueryRow(ctx, `SELECT version FROM sys_workspace_members
	    WHERE workspace_id = dbhub_workspace() AND user_id = $1 AND dbhub_member_role(user_id) IS NOT NULL`, userID).Scan(&memberVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return BusinessIdentity{}, ErrNotFound
	}
	if err != nil {
		return BusinessIdentity{}, err
	}
	var version int64
	err = tx.QueryRow(ctx, `
		INSERT INTO sys_business_identities
		    (workspace_id, user_id, member_version, authority, business_key, source_version, valid_from, valid_until)
		SELECT dbhub_workspace(), $1, $3, $4, $5, $6, $7, $8
		WHERE $2 = 0 OR EXISTS (SELECT 1 FROM sys_business_identities
		    WHERE workspace_id = dbhub_workspace() AND user_id = $1 AND version = $2)
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET
		    member_version = EXCLUDED.member_version, authority = EXCLUDED.authority,
		    business_key = EXCLUDED.business_key, source_version = EXCLUDED.source_version,
		    valid_from = EXCLUDED.valid_from, valid_until = EXCLUDED.valid_until,
		    version = sys_business_identities.version + 1
		WHERE sys_business_identities.version = $2
		RETURNING version`, userID, *in.ExpectedVersion, memberVersion, in.Authority, in.BusinessKey,
		in.SourceVersion, in.ValidFrom, in.ValidUntil).Scan(&version)
	if err != nil {
		return BusinessIdentity{}, mapBusinessWriteError(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sys_business_identity_datasets WHERE workspace_id = dbhub_workspace() AND user_id = $1`, userID); err != nil {
		return BusinessIdentity{}, err
	}
	result, err := tx.Exec(ctx, `
		INSERT INTO sys_business_identity_datasets (workspace_id, user_id, dataset_key, dataset_version)
		SELECT d.workspace_id, $1, d.dataset_key, d.version
		FROM jsonb_to_recordset($2::jsonb) AS requested(key text, version bigint)
		JOIN sys_business_datasets d ON d.workspace_id = dbhub_workspace()
		    AND d.dataset_key = requested.key AND d.version = requested.version
		WHERE statement_timestamp() < d.valid_until AND `+businessDatasetAccountCurrent, userID, string(datasets))
	if err != nil {
		return BusinessIdentity{}, err
	}
	if result.RowsAffected() != int64(len(in.Datasets)) {
		return BusinessIdentity{}, ErrBusinessMappingConflict
	}
	out, err := readBusinessIdentity(ctx, tx, userID)
	if err != nil {
		return BusinessIdentity{}, err
	}
	return out, tx.Commit(ctx)
}

func mapBusinessWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == "23505") {
		return ErrBusinessMappingConflict
	}
	return err
}
