package db

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// OrganizationScopeEntry is a single explicit tuple. Empty dimensions are absent.
type OrganizationScopeEntry struct {
	DepartmentKey   string `json:"department_key,omitempty"`
	WarehouseKey    string `json:"warehouse_key,omitempty"`
	TransportOrgKey string `json:"transport_org_key,omitempty"`
}

func (e OrganizationScopeEntry) valid() bool {
	return e != (OrganizationScopeEntry{}) &&
		(e.DepartmentKey == "" || validBusinessText(e.DepartmentKey)) &&
		(e.WarehouseKey == "" || validBusinessText(e.WarehouseKey)) &&
		(e.TransportOrgKey == "" || validBusinessText(e.TransportOrgKey))
}

// OrganizationScopeInput replaces the scope for the reviewed upstream versions.
type OrganizationScopeInput struct {
	BusinessMappingSource
	ExpectedVersion *int64                   `json:"expected_version"`
	IdentityVersion int64                    `json:"identity_version"`
	DatasetVersion  int64                    `json:"dataset_version"`
	Scopes          []OrganizationScopeEntry `json:"scopes"`
}

// OrganizationScope exposes registered tuples and their currently usable subset.
type OrganizationScope struct {
	BusinessMappingSource
	UserID          int64                    `json:"user_id"`
	DatasetKey      string                   `json:"dataset_key"`
	Version         int64                    `json:"version"`
	IdentityVersion int64                    `json:"identity_version"`
	DatasetVersion  int64                    `json:"dataset_version"`
	Scopes          []OrganizationScopeEntry `json:"scopes"`
	EffectiveScopes []OrganizationScopeEntry `json:"effective_scopes"`
}

func readOrganizationScope(ctx context.Context, tx pgx.Tx, userID int64, key string) (OrganizationScope, error) {
	var out OrganizationScope
	var effective bool
	err := tx.QueryRow(ctx, `
		SELECT o.user_id, o.dataset_key, o.version, o.identity_version, o.dataset_version,
		       o.authority, o.source_version, o.valid_from, o.valid_until,
		       o.valid_from <= statement_timestamp() AND statement_timestamp() < o.valid_until
		       AND i.version = o.identity_version AND d.version = o.dataset_version
		       AND COALESCE(b.dataset_version = o.dataset_version, false)
		       AND (`+businessIdentityAvailable+`) AND (`+businessDatasetAvailable+`),
		       COALESCE((
		           SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
		               'department_key', NULLIF(e.department_key, ''),
		               'warehouse_key', NULLIF(e.warehouse_key, ''),
		               'transport_org_key', NULLIF(e.transport_org_key, '')))
		               ORDER BY e.department_key, e.warehouse_key, e.transport_org_key)
		           FROM sys_business_organization_scope_entries e
		           WHERE e.workspace_id = o.workspace_id AND e.user_id = o.user_id AND e.dataset_key = o.dataset_key
		       ), '[]'::jsonb)
		FROM sys_business_organization_scopes o
		JOIN sys_business_identities i ON i.workspace_id = o.workspace_id AND i.user_id = o.user_id
		JOIN sys_workspace_members m ON m.workspace_id = i.workspace_id AND m.user_id = i.user_id
		JOIN sys_business_datasets d ON d.workspace_id = o.workspace_id AND d.dataset_key = o.dataset_key
		LEFT JOIN sys_business_identity_datasets b ON b.workspace_id = o.workspace_id
		        AND b.user_id = o.user_id AND b.dataset_key = o.dataset_key
		WHERE o.workspace_id = dbhub_workspace() AND o.user_id = $1 AND o.dataset_key = $2`, userID, key).Scan(
		&out.UserID, &out.DatasetKey, &out.Version, &out.IdentityVersion, &out.DatasetVersion,
		&out.Authority, &out.SourceVersion, &out.ValidFrom, &out.ValidUntil, &effective, &out.Scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationScope{}, ErrNotFound
	}
	if err != nil {
		return OrganizationScope{}, err
	}
	out.EffectiveScopes = []OrganizationScopeEntry{}
	if effective {
		out.EffectiveScopes = out.Scopes
	}
	return out, nil
}

// OrganizationScope reads the saved and effective organization ranges in the current workspace.
func (r *UserRepository) OrganizationScope(ctx context.Context, userID int64, key string) (OrganizationScope, error) {
	if userID <= 0 || !businessDatasetKey.MatchString(key) {
		return OrganizationScope{}, ErrBusinessMappingInput
	}
	tx, err := r.beginBusinessMapping(ctx, false)
	if err != nil {
		return OrganizationScope{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	return readOrganizationScope(ctx, tx, userID, key)
}

// SaveOrganizationScope atomically replaces the complete tuples, guarded by all three versions.
func (r *UserRepository) SaveOrganizationScope(ctx context.Context, userID int64, key string, in OrganizationScopeInput) (OrganizationScope, error) {
	if userID <= 0 || !businessDatasetKey.MatchString(key) || !in.valid(in.ExpectedVersion) ||
		in.IdentityVersion <= 0 || in.DatasetVersion <= 0 || in.Scopes == nil || len(in.Scopes) > 100 {
		return OrganizationScope{}, ErrBusinessMappingInput
	}
	seen := make(map[OrganizationScopeEntry]struct{}, len(in.Scopes))
	for _, entry := range in.Scopes {
		_, duplicate := seen[entry]
		if duplicate || !entry.valid() {
			return OrganizationScope{}, ErrBusinessMappingInput
		}
		seen[entry] = struct{}{}
	}
	scopes, err := json.Marshal(in.Scopes)
	if err != nil {
		return OrganizationScope{}, err
	}
	tx, err := r.beginBusinessMapping(ctx, false)
	if err != nil {
		return OrganizationScope{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = lockBusinessReportPolicy(ctx, tx, key); err != nil {
		return OrganizationScope{}, err
	}
	var version int64
	err = tx.QueryRow(ctx, `
		INSERT INTO sys_business_organization_scopes
		    (workspace_id, user_id, dataset_key, identity_version, dataset_version,
		     authority, source_version, valid_from, valid_until)
		SELECT i.workspace_id, i.user_id, d.dataset_key, i.version, d.version, $6, $7, $8, $9
		FROM sys_business_identities i
		JOIN sys_business_identity_datasets b ON b.workspace_id = i.workspace_id AND b.user_id = i.user_id
		JOIN sys_business_datasets d ON d.workspace_id = b.workspace_id
		    AND d.dataset_key = b.dataset_key AND d.version = b.dataset_version
		WHERE i.workspace_id = dbhub_workspace() AND i.user_id = $1 AND d.dataset_key = $2
		    AND i.version = $4 AND d.version = $5
		    AND ($3 = 0 OR EXISTS (SELECT 1 FROM sys_business_organization_scopes o
		         WHERE o.workspace_id = i.workspace_id AND o.user_id = i.user_id AND o.dataset_key = d.dataset_key
		     AND o.version = $3))
		ON CONFLICT (workspace_id, user_id, dataset_key) DO UPDATE SET
		    identity_version = EXCLUDED.identity_version, dataset_version = EXCLUDED.dataset_version,
		    authority = EXCLUDED.authority, source_version = EXCLUDED.source_version,
		    valid_from = EXCLUDED.valid_from, valid_until = EXCLUDED.valid_until,
		    version = sys_business_organization_scopes.version + 1
		WHERE sys_business_organization_scopes.version = $3
		RETURNING version`, userID, key, *in.ExpectedVersion, in.IdentityVersion, in.DatasetVersion,
		in.Authority, in.SourceVersion, in.ValidFrom, in.ValidUntil).Scan(&version)
	if err != nil {
		return OrganizationScope{}, mapBusinessWriteError(err)
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM sys_business_organization_scope_entries
		WHERE workspace_id = dbhub_workspace() AND user_id = $1 AND dataset_key = $2`, userID, key); err != nil {
		return OrganizationScope{}, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO sys_business_organization_scope_entries
		    (workspace_id, user_id, dataset_key, department_key, warehouse_key, transport_org_key)
		SELECT dbhub_workspace(), $1, $2, COALESCE(department_key, ''), COALESCE(warehouse_key, ''), COALESCE(transport_org_key, '')
		FROM jsonb_to_recordset($3::jsonb) AS entry(department_key text, warehouse_key text, transport_org_key text)`,
		userID, key, string(scopes)); err != nil {
		return OrganizationScope{}, err
	}
	out, err := readOrganizationScope(ctx, tx, userID, key)
	if err != nil {
		return OrganizationScope{}, err
	}
	return out, tx.Commit(ctx)
}
