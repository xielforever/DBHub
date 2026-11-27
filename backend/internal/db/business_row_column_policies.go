package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ErrBusinessReadDenied indicates the reviewed metadata cannot authorize a plan.
var ErrBusinessReadDenied = errors.New("当前行列策略没有可用范围，或请求列不可用")

var businessPolicyIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// OrganizationDimensionColumns maps the authoritative key dimensions to an approved view.
type OrganizationDimensionColumns struct {
	Department string `json:"department_key,omitempty"`
	Warehouse  string `json:"warehouse_key,omitempty"`
	Transport  string `json:"transport_org_key,omitempty"`
}

// BusinessRowColumnDefinition defines one approved view, row ceiling, and column projection.
type BusinessRowColumnDefinition struct {
	Schema            string                           `json:"schema"`
	View              string                           `json:"view"`
	Dimensions        OrganizationDimensionColumns     `json:"dimension_columns"`
	AllowedScopes     []OrganizationScopeEntry         `json:"allowed_scopes"`
	DeniedScopes      []OrganizationScopeEntry         `json:"denied_scopes"`
	AllowedColumns    []string                         `json:"allowed_columns"`
	DeniedColumns     []string                         `json:"denied_columns"`
	ParameterBindings []BusinessReportParameterBinding `json:"parameter_bindings,omitempty"`
}

func (d OrganizationDimensionColumns) ordered() [3]string {
	return [3]string{d.Department, d.Warehouse, d.Transport}
}

func businessIdentifierSet(cols []string) (map[string]struct{}, bool) {
	if len(cols) > 100 {
		return nil, false
	}
	out := make(map[string]struct{}, len(cols))
	for _, col := range cols {
		if _, duplicate := out[col]; duplicate || !businessPolicyIdentifier.MatchString(col) {
			return nil, false
		}
		out[col] = struct{}{}
	}
	return out, true
}

func businessPolicyScopeSet(entries []OrganizationScopeEntry, axes OrganizationDimensionColumns) (map[OrganizationScopeEntry]struct{}, bool) {
	if len(entries) > 100 {
		return nil, false
	}
	out := make(map[OrganizationScopeEntry]struct{}, len(entries))
	for _, e := range entries {
		_, duplicate := out[e]
		if duplicate || !e.valid() || (e.DepartmentKey != "" && axes.Department == "") ||
			(e.WarehouseKey != "" && axes.Warehouse == "") || (e.TransportOrgKey != "" && axes.Transport == "") {
			return nil, false
		}
		out[e] = struct{}{}
	}
	return out, true
}

func (d BusinessRowColumnDefinition) valid() bool {
	if !businessPolicyIdentifier.MatchString(d.Schema) || !businessPolicyIdentifier.MatchString(d.View) ||
		len(d.AllowedScopes) == 0 || len(d.AllowedColumns) == 0 {
		return false
	}
	var columns []string
	for _, col := range d.Dimensions.ordered() {
		if col != "" {
			columns = append(columns, col)
		}
	}
	_, dimensionsOK := businessIdentifierSet(columns)
	_, allowRowsOK := businessPolicyScopeSet(d.AllowedScopes, d.Dimensions)
	_, denyRowsOK := businessPolicyScopeSet(d.DeniedScopes, d.Dimensions)
	_, allowColumnsOK := businessIdentifierSet(d.AllowedColumns)
	_, denyColumnsOK := businessIdentifierSet(d.DeniedColumns)
	return len(columns) != 0 && dimensionsOK && allowRowsOK && denyRowsOK && allowColumnsOK && denyColumnsOK &&
		validBusinessParameterBindings(d.ParameterBindings, d.AllowedColumns)
}

// BusinessRowColumnPolicyInput pins a policy to the reviewed dataset version.
type BusinessRowColumnPolicyInput struct {
	BusinessMappingSource
	ExpectedVersion *int64                      `json:"expected_version"`
	DatasetVersion  int64                       `json:"dataset_version"`
	Definition      BusinessRowColumnDefinition `json:"definition"`
}

// BusinessRowColumnPolicy is the persisted strategy for one dataset.
type BusinessRowColumnPolicy struct {
	BusinessMappingSource
	DatasetKey                              string                      `json:"dataset_key"`
	Version                                 int64                       `json:"version"`
	DatasetVersion                          int64                       `json:"dataset_version"`
	Definition                              BusinessRowColumnDefinition `json:"definition"`
	current                                 bool
	accountID, accountVersion, definitionID int64
}

const businessNativePostgres = `
	EXISTS (SELECT 1 FROM sys_source_definitions s WHERE s.workspace_id = d.workspace_id
	    AND s.id = d.definition_revision_id AND s.config->>'type' = 'postgres' AND s.template_revision_id IS NULL)`

func readBusinessRowColumnPolicy(ctx context.Context, tx pgx.Tx, key string) (BusinessRowColumnPolicy, error) {
	var out BusinessRowColumnPolicy
	err := tx.QueryRow(ctx, `
		SELECT p.dataset_key, p.version, p.dataset_version, p.authority, p.source_version,
		       p.valid_from, p.valid_until, p.definition,
		       p.valid_from <= statement_timestamp() AND statement_timestamp() < p.valid_until
		       AND p.dataset_version = d.version AND (`+businessNativePostgres+`)
		       AND (`+businessDatasetAvailable+`), d.account_id, d.account_version, d.definition_revision_id
		FROM sys_business_row_column_policies p
		JOIN sys_business_datasets d ON d.workspace_id = p.workspace_id AND d.dataset_key = p.dataset_key
		WHERE p.workspace_id = dbhub_workspace() AND p.dataset_key = $1`, key).Scan(
		&out.DatasetKey, &out.Version, &out.DatasetVersion, &out.Authority, &out.SourceVersion,
		&out.ValidFrom, &out.ValidUntil, &out.Definition, &out.current, &out.accountID, &out.accountVersion, &out.definitionID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}

// BusinessRowColumnPolicy reads the current workspace's stored policy.
func (r *UserRepository) BusinessRowColumnPolicy(ctx context.Context, key string) (BusinessRowColumnPolicy, error) {
	if !businessDatasetKey.MatchString(key) {
		return BusinessRowColumnPolicy{}, ErrBusinessMappingInput
	}
	tx, err := r.beginBusinessMapping(ctx, false)
	if err != nil {
		return BusinessRowColumnPolicy{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	return readBusinessRowColumnPolicy(ctx, tx, key)
}

// SaveBusinessRowColumnPolicy atomically replaces the reviewed policy.
func (r *UserRepository) SaveBusinessRowColumnPolicy(ctx context.Context, key string, in BusinessRowColumnPolicyInput) (BusinessRowColumnPolicy, error) {
	if !businessDatasetKey.MatchString(key) || !in.valid(in.ExpectedVersion) || in.DatasetVersion <= 0 || !in.Definition.valid() {
		return BusinessRowColumnPolicy{}, ErrBusinessMappingInput
	}
	definition, err := json.Marshal(in.Definition)
	if err != nil {
		return BusinessRowColumnPolicy{}, err
	}
	tx, err := r.beginBusinessMapping(ctx, false)
	if err != nil {
		return BusinessRowColumnPolicy{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = lockBusinessReportPolicy(ctx, tx, key); err != nil {
		return BusinessRowColumnPolicy{}, err
	}
	var version int64
	err = tx.QueryRow(ctx, `
		INSERT INTO sys_business_row_column_policies
		    (workspace_id, dataset_key, dataset_version, authority, source_version, valid_from, valid_until, definition)
		SELECT d.workspace_id, d.dataset_key, d.version, $4, $5, $6, $7, $8::jsonb
		FROM sys_business_datasets d
		WHERE d.workspace_id = dbhub_workspace() AND d.dataset_key = $1 AND d.version = $3
		  AND (`+businessNativePostgres+`) AND (`+businessDatasetAccountCurrent+`)
		  AND ($2 = 0 OR EXISTS (SELECT 1 FROM sys_business_row_column_policies p
		       WHERE p.workspace_id = d.workspace_id AND p.dataset_key = d.dataset_key AND p.version = $2))
		ON CONFLICT (workspace_id, dataset_key) DO UPDATE SET
		    dataset_version = EXCLUDED.dataset_version, authority = EXCLUDED.authority, source_version = EXCLUDED.source_version,
		    valid_from = EXCLUDED.valid_from, valid_until = EXCLUDED.valid_until, definition = EXCLUDED.definition,
		    version = sys_business_row_column_policies.version + 1
		WHERE sys_business_row_column_policies.version = $2
		RETURNING version`, key, *in.ExpectedVersion, in.DatasetVersion, in.Authority, in.SourceVersion,
		in.ValidFrom, in.ValidUntil, string(definition)).Scan(&version)
	if err != nil {
		return BusinessRowColumnPolicy{}, mapBusinessWriteError(err)
	}
	out, err := readBusinessRowColumnPolicy(ctx, tx, key)
	if err != nil {
		return BusinessRowColumnPolicy{}, err
	}
	return out, tx.Commit(ctx)
}

// BusinessReadPlanInput states the reviewed versions and explicit requested columns.
type BusinessReadPlanInput struct {
	ScopeVersion  int64    `json:"scope_version"`
	PolicyVersion int64    `json:"policy_version"`
	Columns       []string `json:"columns"`
}

// BusinessReadPlan carries a fixed-shape statement for internal verification.
// Only the version and projection summary is exposed through the management API.
type BusinessReadPlan struct {
	DatasetKey      string   `json:"dataset_key"`
	IdentityVersion int64    `json:"identity_version"`
	DatasetVersion  int64    `json:"dataset_version"`
	ScopeVersion    int64    `json:"scope_version"`
	PolicyVersion   int64    `json:"policy_version"`
	Columns         []string `json:"columns"`
	ScopeCount      int      `json:"scope_count"`
	Statement       string   `json:"-"`
	Args            []any    `json:"-"`
}

// CompileBusinessReadPlan resolves the policy and the organizational bindings from one snapshot.
func (r *UserRepository) CompileBusinessReadPlan(ctx context.Context, userID int64, key string, in BusinessReadPlanInput) (BusinessReadPlan, error) {
	if !in.valid(userID, key) {
		return BusinessReadPlan{}, ErrBusinessMappingInput
	}
	tx, err := r.beginBusinessMapping(ctx, true)
	if err != nil {
		return BusinessReadPlan{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	policy, scope, err := readBusinessPlanBasis(ctx, tx, userID, key, in)
	if err != nil {
		return BusinessReadPlan{}, err
	}
	return compileBusinessReadPlan(policy, scope, in.Columns, nil, nil, BusinessReportMaxRows, nil)
}

func (in BusinessReadPlanInput) valid(userID int64, key string) bool {
	_, columnsOK := businessIdentifierSet(in.Columns)
	return userID > 0 && businessDatasetKey.MatchString(key) && in.ScopeVersion > 0 && in.PolicyVersion > 0 &&
		len(in.Columns) != 0 && columnsOK
}

func readBusinessPlanBasis(ctx context.Context, tx pgx.Tx, userID int64, key string, in BusinessReadPlanInput) (BusinessRowColumnPolicy, OrganizationScope, error) {
	policy, err := readBusinessRowColumnPolicy(ctx, tx, key)
	if err != nil {
		return policy, OrganizationScope{}, businessPlanLookupError(err)
	}
	scope, err := readOrganizationScope(ctx, tx, userID, key)
	if err != nil {
		return policy, scope, businessPlanLookupError(err)
	}
	if scope.Version != in.ScopeVersion || policy.Version != in.PolicyVersion {
		return policy, scope, ErrBusinessMappingConflict
	}
	if !policy.current || policy.DatasetVersion != scope.DatasetVersion || !policy.Definition.valid() {
		return policy, scope, ErrBusinessReadDenied
	}
	return policy, scope, nil
}

func businessPlanLookupError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return ErrBusinessReadDenied
	}
	return err
}

func effectiveBusinessScopes(d BusinessRowColumnDefinition, scope OrganizationScope) []OrganizationScopeEntry {
	upper, _ := businessPolicyScopeSet(d.AllowedScopes, d.Dimensions)
	deny, _ := businessPolicyScopeSet(d.DeniedScopes, d.Dimensions)
	effective := make([]OrganizationScopeEntry, 0, len(scope.EffectiveScopes))
	for _, entry := range scope.EffectiveScopes {
		_, allowed := upper[entry]
		_, denied := deny[entry]
		if allowed && !denied {
			effective = append(effective, entry)
		}
	}
	return effective
}

func compileBusinessReadPlan(p BusinessRowColumnPolicy, scope OrganizationScope, requested []string, masked map[string]bool, reveal *businessRevealFilter, rowLimit int, filters []BusinessReportParameterBinding) (BusinessReadPlan, error) {
	d := p.Definition
	allowedColumns, _ := businessIdentifierSet(d.AllowedColumns)
	deniedColumns, _ := businessIdentifierSet(d.DeniedColumns)
	projection := make([]string, 0, len(requested))
	for _, col := range requested {
		_, allowed := allowedColumns[col]
		_, denied := deniedColumns[col]
		if !allowed || denied {
			return BusinessReadPlan{}, ErrBusinessReadDenied
		}
		if masked[col] {
			projection = append(projection, "NULL AS "+pgx.Identifier{col}.Sanitize())
		} else {
			projection = append(projection, pgx.Identifier{"v", col}.Sanitize())
		}
	}
	effective := effectiveBusinessScopes(d, scope)
	if len(effective) == 0 {
		return BusinessReadPlan{}, ErrBusinessReadDenied
	}
	values, err := json.Marshal(effective)
	if err != nil {
		return BusinessReadPlan{}, err
	}
	keyNames := [3]string{"department_key", "warehouse_key", "transport_org_key"}
	var predicates []string
	for index, col := range d.Dimensions.ordered() {
		if col == "" {
			continue
		}
		predicates = append(predicates, pgx.Identifier{"v", col}.Sanitize()+`::text COLLATE "C" IS NOT DISTINCT FROM `+
			pgx.Identifier{"g", keyNames[index]}.Sanitize()+` COLLATE "C"`)
	}
	statement := "SELECT " + strings.Join(projection, ", ") + " FROM " + pgx.Identifier{d.Schema, d.View}.Sanitize() +
		" AS v WHERE EXISTS (SELECT 1 FROM jsonb_to_recordset($1::jsonb)" +
		" AS g(department_key text, warehouse_key text, transport_org_key text) WHERE " + strings.Join(predicates, " AND ") + ")"
	args := []any{string(values)}
	if len(filters) > 0 {
		statement += " AND " + strings.Join(businessReportPredicates(filters, true), " AND ")
	}
	if reveal != nil {
		statement += reveal.predicate() + " LIMIT 100"
		args = append(args, reveal.records)
	} else {
		statement += fmt.Sprintf(" LIMIT %d", rowLimit)
	}
	return BusinessReadPlan{
		DatasetKey: p.DatasetKey, IdentityVersion: scope.IdentityVersion, DatasetVersion: scope.DatasetVersion,
		ScopeVersion: scope.Version, PolicyVersion: p.Version, Columns: requested, ScopeCount: len(effective),
		Statement: statement, Args: args,
	}, nil
}
