package db

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

const (
	businessClassInternal = iota + 1
	businessClassSensitive
	businessClassHighSensitive
	businessClassCredentialSecret
)

const (
	businessProtectionNone = iota
	businessProtectionMasked
	businessProtectionHidden
)

// BusinessFieldClassification records source metadata; output is selected separately.
type BusinessFieldClassification struct {
	Classification   string   `json:"classification"`
	LegacyLabel      string   `json:"legacy_label,omitempty"`
	LegacyProtection string   `json:"legacy_protection,omitempty"`
	ReviewReference  string   `json:"review_reference,omitempty"`
	DependsOn        []string `json:"depends_on,omitempty"`
}

type businessFieldProtection struct {
	class, original int
}

var businessClasses = map[string]int{
	"internal": businessClassInternal, "sensitive": businessClassSensitive,
	"high_sensitive": businessClassHighSensitive, "credential_secret": businessClassCredentialSecret,
}

var businessOriginalProtections = map[string]int{
	"": businessProtectionNone, "none": businessProtectionNone,
	"masked": businessProtectionMasked, "hidden": businessProtectionHidden,
}

// BusinessOutputDefinition contains column classification and opt-in review metadata.
type BusinessOutputDefinition struct {
	Fields              map[string]BusinessFieldClassification `json:"fields"`
	RecordKeyColumn     string                                 `json:"record_key_column,omitempty"`
	RecordVersionColumn string                                 `json:"record_version_column,omitempty"`
	ReviewerIDs         []int64                                `json:"reviewer_ids,omitempty"`
	ReportID            int64                                  `json:"report_id,omitempty"`
	PublicationNo       int64                                  `json:"publication_no,omitempty"`
}

func (d BusinessOutputDefinition) protections(allowed []string) (map[string]businessFieldProtection, error) {
	if len(d.Fields) == 0 || len(d.Fields) > 200 || len(d.ReviewerIDs) > 100 ||
		d.ReportID < 0 || d.PublicationNo < 0 || (d.ReportID == 0) != (d.PublicationNo == 0) {
		return nil, ErrBusinessMappingInput
	}
	for _, col := range allowed {
		if _, classified := d.Fields[col]; !classified {
			return nil, ErrBusinessMappingInput
		}
	}
	reviewers := make(map[int64]bool, len(d.ReviewerIDs))
	for _, id := range d.ReviewerIDs {
		if id <= 0 || reviewers[id] {
			return nil, ErrBusinessMappingInput
		}
		reviewers[id] = true
	}
	if len(reviewers) > 0 || d.RecordKeyColumn != "" || d.RecordVersionColumn != "" {
		_, keyExists := d.Fields[d.RecordKeyColumn]
		_, versionExists := d.Fields[d.RecordVersionColumn]
		if !keyExists || !versionExists || d.RecordKeyColumn == d.RecordVersionColumn {
			return nil, ErrBusinessMappingInput
		}
	}

	resolved := make(map[string]businessFieldProtection, len(d.Fields))
	visiting := make(map[string]bool, len(d.Fields))
	var visit func(string) (businessFieldProtection, bool)
	visit = func(col string) (businessFieldProtection, bool) {
		if p, ok := resolved[col]; ok {
			return p, true
		}
		f, exists := d.Fields[col]
		class := businessClasses[f.Classification]
		original, validOriginal := businessOriginalProtections[f.LegacyProtection]
		_, dependenciesOK := businessIdentifierSet(f.DependsOn)
		if visiting[col] || !exists || !businessPolicyIdentifier.MatchString(col) || class == 0 || !validOriginal || !dependenciesOK {
			return businessFieldProtection{}, false
		}
		if f.ReviewReference != "" && !validBusinessText(f.ReviewReference) {
			return businessFieldProtection{}, false
		}
		switch f.LegacyLabel {
		case "", "normal":
		case "sensitive", "confidential":
			if class < businessClassSensitive || (f.LegacyLabel == "confidential" && f.ReviewReference == "") {
				return businessFieldProtection{}, false
			}
		default:
			return businessFieldProtection{}, false
		}
		visiting[col] = true
		for _, input := range f.DependsOn {
			p, ok := visit(input)
			if !ok || class < p.class {
				return businessFieldProtection{}, false
			}
			original = max(original, p.original)
		}
		visiting[col] = false
		p := businessFieldProtection{class: class, original: original}
		resolved[col] = p
		return p, true
	}
	for col := range d.Fields {
		if _, ok := visit(col); !ok {
			return nil, ErrBusinessMappingInput
		}
	}
	for _, col := range []string{d.RecordKeyColumn, d.RecordVersionColumn} {
		if col != "" && resolved[col] != (businessFieldProtection{class: businessClassInternal, original: businessProtectionNone}) {
			return nil, ErrBusinessMappingInput
		}
	}
	return resolved, nil
}

// BusinessOutputPolicyInput pins classification to the current row/column policy.
type BusinessOutputPolicyInput struct {
	BusinessMappingSource
	BusinessOutputDefinition
	ExpectedVersion        *int64 `json:"expected_version"`
	DatasetVersion         int64  `json:"dataset_version"`
	RowColumnPolicyVersion int64  `json:"row_column_policy_version"`
}

// BusinessOutputPolicy is the stored dataset classification.
type BusinessOutputPolicy struct {
	BusinessMappingSource
	BusinessOutputDefinition
	DatasetKey             string `json:"dataset_key"`
	Version                int64  `json:"version"`
	DatasetVersion         int64  `json:"dataset_version"`
	RowColumnPolicyVersion int64  `json:"row_column_policy_version"`
	current                bool
}

func readBusinessOutputPolicy(ctx context.Context, tx pgx.Tx, key string) (BusinessOutputPolicy, error) {
	var out BusinessOutputPolicy
	err := tx.QueryRow(ctx, `
		SELECT o.dataset_key, o.version, o.dataset_version, o.row_column_policy_version,
		       o.authority, o.source_version, o.valid_from, o.valid_until, o.definition,
		       o.valid_from <= statement_timestamp() AND statement_timestamp() < o.valid_until
		       AND o.row_column_policy_version = p.version AND o.dataset_version = p.dataset_version
		FROM sys_business_output_policies o
		JOIN sys_business_row_column_policies p ON p.workspace_id = o.workspace_id AND p.dataset_key = o.dataset_key
		WHERE o.workspace_id = dbhub_workspace() AND o.dataset_key = $1`, key).Scan(
		&out.DatasetKey, &out.Version, &out.DatasetVersion, &out.RowColumnPolicyVersion,
		&out.Authority, &out.SourceVersion, &out.ValidFrom, &out.ValidUntil, &out.BusinessOutputDefinition, &out.current)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}

// BusinessOutputPolicy reads the classification for a dataset in the current workspace.
func (r *UserRepository) BusinessOutputPolicy(ctx context.Context, key string) (BusinessOutputPolicy, error) {
	if !businessDatasetKey.MatchString(key) {
		return BusinessOutputPolicy{}, ErrBusinessMappingInput
	}
	tx, err := r.beginBusinessMapping(ctx, false)
	if err != nil {
		return BusinessOutputPolicy{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	return readBusinessOutputPolicy(ctx, tx, key)
}

// SaveBusinessOutputPolicy replaces classification only for the reviewed row/column policy.
func (r *UserRepository) SaveBusinessOutputPolicy(ctx context.Context, key string, in BusinessOutputPolicyInput) (BusinessOutputPolicy, error) {
	if !businessDatasetKey.MatchString(key) || !in.valid(in.ExpectedVersion) || in.DatasetVersion <= 0 || in.RowColumnPolicyVersion <= 0 {
		return BusinessOutputPolicy{}, ErrBusinessMappingInput
	}
	tx, err := r.beginBusinessMapping(ctx, false)
	if err != nil {
		return BusinessOutputPolicy{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = lockBusinessReportPolicy(ctx, tx, key); err != nil {
		return BusinessOutputPolicy{}, err
	}
	policy, err := readBusinessRowColumnPolicy(ctx, tx, key)
	if errors.Is(err, ErrNotFound) || err == nil && (!policy.current || policy.Version != in.RowColumnPolicyVersion || policy.DatasetVersion != in.DatasetVersion) {
		return BusinessOutputPolicy{}, ErrBusinessMappingConflict
	}
	if err != nil {
		return BusinessOutputPolicy{}, err
	}
	if _, err = in.protections(policy.Definition.AllowedColumns); err != nil {
		return BusinessOutputPolicy{}, err
	}
	definition, err := json.Marshal(in.BusinessOutputDefinition)
	if err != nil {
		return BusinessOutputPolicy{}, err
	}
	var version int64
	err = tx.QueryRow(ctx, `
		INSERT INTO sys_business_output_policies
		    (workspace_id, dataset_key, dataset_version, row_column_policy_version, authority, source_version, valid_from, valid_until, definition)
		SELECT p.workspace_id, p.dataset_key, p.dataset_version, p.version, $5, $6, $7, $8, $9::jsonb
		FROM sys_business_row_column_policies p
		WHERE p.workspace_id = dbhub_workspace() AND p.dataset_key = $1 AND p.version = $3 AND p.dataset_version = $4
		  AND ($2 = 0 OR EXISTS (SELECT 1 FROM sys_business_output_policies o
		      WHERE o.workspace_id = p.workspace_id AND o.dataset_key = p.dataset_key AND o.version = $2))
		ON CONFLICT (workspace_id, dataset_key) DO UPDATE SET
		    dataset_version = EXCLUDED.dataset_version, row_column_policy_version = EXCLUDED.row_column_policy_version,
		    authority = EXCLUDED.authority, source_version = EXCLUDED.source_version,
		    valid_from = EXCLUDED.valid_from, valid_until = EXCLUDED.valid_until, definition = EXCLUDED.definition,
		    version = sys_business_output_policies.version + 1
		WHERE sys_business_output_policies.version = $2 RETURNING version`,
		key, *in.ExpectedVersion, in.RowColumnPolicyVersion, in.DatasetVersion,
		in.Authority, in.SourceVersion, in.ValidFrom, in.ValidUntil, string(definition)).Scan(&version)
	if err != nil {
		return BusinessOutputPolicy{}, mapBusinessWriteError(err)
	}
	out, err := readBusinessOutputPolicy(ctx, tx, key)
	if err != nil {
		return BusinessOutputPolicy{}, err
	}
	return out, tx.Commit(ctx)
}

// BusinessOutputPlanInput carries an optional record-bound approval for an interactive check.
type BusinessOutputPlanInput struct {
	BusinessReadPlanInput
	OutputPolicyVersion int64  `json:"output_policy_version"`
	RevealRequestID     string `json:"reveal_request_id,omitempty"`
	Purpose             string `json:"purpose,omitempty"`
	Channel             string `json:"channel,omitempty"`
}

// BusinessOutputPlan exposes only the projection and masking summary.
type BusinessOutputPlan struct {
	BusinessReadPlan
	OutputPolicyVersion int64                `json:"output_policy_version"`
	MaskedColumns       []string             `json:"masked_columns"`
	ReportTarget        BusinessReportTarget `json:"-"`
}

// BusinessReportTarget pins an isolated verification to the approved version and source.
type BusinessReportTarget struct {
	ReportID, PublicationNo, AccountID, AccountVersion, DefinitionID int64
	Schema, View, ApprovedSQL                                        string
	Filters                                                          []BusinessReportParameterBinding `json:"filters,omitempty"`
}

func currentBusinessOutput(ctx context.Context, tx pgx.Tx, key string, version int64, row BusinessRowColumnPolicy) (BusinessOutputPolicy, map[string]businessFieldProtection, error) {
	out, err := readBusinessOutputPolicy(ctx, tx, key)
	if err != nil {
		return out, nil, businessPlanLookupError(err)
	}
	if out.Version != version {
		return out, nil, ErrBusinessMappingConflict
	}
	if !out.current || out.RowColumnPolicyVersion != row.Version || out.DatasetVersion != row.DatasetVersion {
		return out, nil, ErrBusinessReadDenied
	}
	protections, err := out.protections(row.Definition.AllowedColumns)
	if err != nil {
		return out, nil, ErrBusinessReadDenied
	}
	return out, protections, nil
}

// CompileBusinessOutputPlan applies classification after the row/column ceiling.
func (r *UserRepository) CompileBusinessOutputPlan(ctx context.Context, userID int64, key string, in BusinessOutputPlanInput) (BusinessOutputPlan, error) {
	return r.compileBusinessOutputPlan(ctx, userID, key, in, false)
}

// CompileBusinessReportVerification resolves the current actor's plan only in a provisioned isolated database.
func (r *UserRepository) CompileBusinessReportVerification(ctx context.Context, key string, in BusinessOutputPlanInput) (BusinessOutputPlan, error) {
	return r.compileBusinessOutputPlan(ctx, ActorID(ctx), key, in, true)
}

func (r *UserRepository) compileBusinessOutputPlan(ctx context.Context, userID int64, key string, in BusinessOutputPlanInput, isolated bool) (BusinessOutputPlan, error) {
	if !in.BusinessReadPlanInput.valid(userID, key) || in.OutputPolicyVersion <= 0 ||
		(in.RevealRequestID == "" && in.Purpose != "") {
		return BusinessOutputPlan{}, ErrBusinessMappingInput
	}
	tx, err := r.beginBusinessMapping(ctx, true)
	if err != nil {
		return BusinessOutputPlan{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if isolated {
		if err = r.iamIsolation(ctx, tx); err != nil {
			if errors.Is(err, ErrManagedDenied) {
				return BusinessOutputPlan{}, ErrBusinessReadDenied
			}
			return BusinessOutputPlan{}, err
		}
	}
	row, scope, err := readBusinessPlanBasis(ctx, tx, userID, key, in.BusinessReadPlanInput)
	if err != nil {
		return BusinessOutputPlan{}, err
	}
	out, fields, err := currentBusinessOutput(ctx, tx, key, in.OutputPolicyVersion, row)
	if err != nil {
		return BusinessOutputPlan{}, err
	}
	var reveal *businessRevealFilter
	if in.RevealRequestID != "" {
		reveal, err = authorizedBusinessReveal(ctx, tx, userID, in, row, scope, out, fields)
		if err != nil {
			return BusinessOutputPlan{}, err
		}
	}
	masked := make(map[string]bool, len(in.Columns))
	maskList := make([]string, 0)
	for _, col := range in.Columns {
		p, known := fields[col]
		if !known || p.class == businessClassCredentialSecret || p.original == businessProtectionHidden {
			return BusinessOutputPlan{}, ErrBusinessReadDenied
		}
		if reveal == nil && (p.class >= businessClassSensitive || p.original == businessProtectionMasked) {
			masked[col] = true
			maskList = append(maskList, col)
		}
	}
	plan, err := compileBusinessReadPlan(row, scope, in.Columns, masked, reveal, BusinessReportMaxRows, nil)
	if err != nil {
		return BusinessOutputPlan{}, err
	}
	result := BusinessOutputPlan{BusinessReadPlan: plan, OutputPolicyVersion: out.Version, MaskedColumns: maskList}
	if isolated {
		if out.ReportID == 0 || out.PublicationNo == 0 {
			return BusinessOutputPlan{}, ErrBusinessReadDenied
		}
		result.ReportTarget = businessReportTarget(row, out.ReportID, out.PublicationNo)
	}
	return result, nil
}
