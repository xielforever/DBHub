package db

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	businessRevealRequestSeconds = 24 * 60 * 60
	businessRevealGrantSeconds   = 8 * 60 * 60
)

var businessRevealID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// BusinessRevealRecord identifies a specific immutable revision of a current row.
type BusinessRevealRecord struct {
	Key     string `json:"key"`
	Version string `json:"version"`
}

// BusinessRevealInput describes a bounded, self-requested disclosure.
type BusinessRevealInput struct {
	BusinessReadPlanInput
	OutputPolicyVersion int64                  `json:"output_policy_version"`
	Purpose             string                 `json:"purpose"`
	Records             []BusinessRevealRecord `json:"records"`
	TTLSeconds          int                    `json:"ttl_seconds"`
}

// BusinessRevealRequest contains approval metadata only.
type BusinessRevealRequest struct {
	ID                   string                 `json:"id"`
	DatasetKey           string                 `json:"dataset_key"`
	RequesterID          int64                  `json:"requester_id"`
	IdentityVersion      int64                  `json:"identity_version"`
	DatasetVersion       int64                  `json:"dataset_version"`
	ScopeVersion         int64                  `json:"scope_version"`
	PolicyVersion        int64                  `json:"policy_version"`
	OutputPolicyVersion  int64                  `json:"output_policy_version"`
	Purpose              string                 `json:"purpose"`
	Records              []BusinessRevealRecord `json:"records"`
	Columns              []string               `json:"columns"`
	RequestedUntil       time.Time              `json:"requested_until"`
	ApprovedUntil        *time.Time             `json:"approved_until,omitempty"`
	ReviewerID           *int64                 `json:"reviewer_id,omitempty"`
	ReviewerScopeVersion *int64                 `json:"reviewer_scope_version,omitempty"`
	State                string                 `json:"state"`
	current              bool
}

const businessRevealColumns = `id::text, dataset_key, requester_id, identity_version, dataset_version,
	scope_version, policy_version, output_version, purpose, records, columns, requested_until,
	approved_until, reviewer_id, reviewer_scope_version, state,
	COALESCE(approved_until > statement_timestamp(), false)`

func scanBusinessReveal(row pgx.Row) (BusinessRevealRequest, error) {
	var grant BusinessRevealRequest
	err := row.Scan(&grant.ID, &grant.DatasetKey, &grant.RequesterID, &grant.IdentityVersion, &grant.DatasetVersion,
		&grant.ScopeVersion, &grant.PolicyVersion, &grant.OutputPolicyVersion, &grant.Purpose, &grant.Records,
		&grant.Columns, &grant.RequestedUntil, &grant.ApprovedUntil, &grant.ReviewerID,
		&grant.ReviewerScopeVersion, &grant.State, &grant.current)
	if errors.Is(err, pgx.ErrNoRows) {
		return grant, ErrBusinessReadDenied
	}
	return grant, err
}

func validBusinessRevealRecords(records []BusinessRevealRecord) bool {
	if len(records) == 0 || len(records) > 100 {
		return false
	}
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		if seen[record.Key] || !validBusinessText(record.Key) || !validBusinessText(record.Version) {
			return false
		}
		seen[record.Key] = true
	}
	return true
}

func businessRevealFields(row BusinessRowColumnPolicy, fields map[string]businessFieldProtection, cols []string) bool {
	allowed, _ := businessIdentifierSet(row.Definition.AllowedColumns)
	denied, _ := businessIdentifierSet(row.Definition.DeniedColumns)
	for _, col := range cols {
		_, ok := allowed[col]
		_, blocked := denied[col]
		if !ok || blocked || fields[col] != (businessFieldProtection{class: businessClassSensitive, original: businessProtectionNone}) {
			return false
		}
	}
	return len(cols) != 0
}

// RequestBusinessReveal records exact fields, record revisions, and purpose.
func (r *UserRepository) RequestBusinessReveal(ctx context.Context, key string, in BusinessRevealInput) (BusinessRevealRequest, error) {
	actor := ActorID(ctx)
	if !in.BusinessReadPlanInput.valid(actor, key) || in.OutputPolicyVersion <= 0 || !validBusinessText(in.Purpose) ||
		!validBusinessRevealRecords(in.Records) || in.TTLSeconds < 1 || in.TTLSeconds > businessRevealRequestSeconds {
		return BusinessRevealRequest{}, ErrBusinessMappingInput
	}
	tx, err := r.beginBusinessMapping(ctx, true)
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	row, scope, err := readBusinessPlanBasis(ctx, tx, actor, key, in.BusinessReadPlanInput)
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	out, fields, err := currentBusinessOutput(ctx, tx, key, in.OutputPolicyVersion, row)
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	if len(out.ReviewerIDs) == 0 || len(effectiveBusinessScopes(row.Definition, scope)) == 0 || !businessRevealFields(row, fields, in.Columns) {
		return BusinessRevealRequest{}, ErrBusinessReadDenied
	}
	records, err := json.Marshal(in.Records)
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	cols, err := json.Marshal(in.Columns)
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	grant, err := scanBusinessReveal(tx.QueryRow(ctx, `
		INSERT INTO sys_business_reveal_requests (workspace_id, dataset_key, requester_id,
		    identity_version, dataset_version, scope_version, policy_version, output_version,
		    purpose, records, columns, requested_until)
		VALUES (dbhub_workspace(), $1, dbhub_actor(), $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb,
		    LEAST(statement_timestamp() + make_interval(secs => $10::int), $11, $12, $13))
		RETURNING `+businessRevealColumns, key, scope.IdentityVersion, scope.DatasetVersion,
		scope.Version, row.Version, out.Version, in.Purpose, string(records), string(cols),
		in.TTLSeconds, scope.ValidUntil, row.ValidUntil, out.ValidUntil))
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	return grant, tx.Commit(ctx)
}

func reviewerBusinessScope(ctx context.Context, tx pgx.Tx, reviewer int64, row BusinessRowColumnPolicy, requester OrganizationScope, out BusinessOutputPolicy) (OrganizationScope, error) {
	if reviewer == requester.UserID || !slices.Contains(out.ReviewerIDs, reviewer) {
		return OrganizationScope{}, ErrBusinessReadDenied
	}
	s, err := readOrganizationScope(ctx, tx, reviewer, out.DatasetKey)
	if err != nil {
		return s, businessPlanLookupError(err)
	}
	coverage := make(map[OrganizationScopeEntry]bool)
	for _, e := range effectiveBusinessScopes(row.Definition, s) {
		coverage[e] = true
	}
	requested := effectiveBusinessScopes(row.Definition, requester)
	if len(requested) == 0 {
		return s, ErrBusinessReadDenied
	}
	for _, e := range requested {
		if !coverage[e] {
			return s, ErrBusinessReadDenied
		}
	}
	return s, nil
}

// ApproveBusinessReveal requires the explicitly designated independent reviewer
// and records their current scope version.
func (r *UserRepository) ApproveBusinessReveal(ctx context.Context, id string, seconds int) (BusinessRevealRequest, error) {
	if !businessRevealID.MatchString(id) || seconds < 1 || seconds > businessRevealGrantSeconds {
		return BusinessRevealRequest{}, ErrBusinessMappingInput
	}
	tx, err := r.beginBusinessMapping(ctx, false)
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	grant, err := scanBusinessReveal(tx.QueryRow(ctx, `
		SELECT `+businessRevealColumns+` FROM sys_business_reveal_requests
		WHERE workspace_id = dbhub_workspace() AND id = $1::uuid FOR UPDATE`, id))
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	in := BusinessReadPlanInput{ScopeVersion: grant.ScopeVersion, PolicyVersion: grant.PolicyVersion, Columns: grant.Columns}
	row, scope, err := readBusinessPlanBasis(ctx, tx, grant.RequesterID, grant.DatasetKey, in)
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	out, fields, err := currentBusinessOutput(ctx, tx, grant.DatasetKey, grant.OutputPolicyVersion, row)
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	if grant.IdentityVersion != scope.IdentityVersion || grant.DatasetVersion != scope.DatasetVersion ||
		!businessRevealFields(row, fields, grant.Columns) || !validBusinessRevealRecords(grant.Records) {
		return BusinessRevealRequest{}, ErrBusinessReadDenied
	}
	reviewer, err := reviewerBusinessScope(ctx, tx, ActorID(ctx), row, scope, out)
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	approved, err := scanBusinessReveal(tx.QueryRow(ctx, `
		UPDATE sys_business_reveal_requests
		SET state = 'approved', reviewer_id = dbhub_actor(), reviewer_scope_version = $2,
		    approved_until = LEAST(requested_until, statement_timestamp() + make_interval(secs => $3::int), $4)
		WHERE workspace_id = dbhub_workspace() AND id = $1::uuid AND state = 'pending'
		  AND requested_until > statement_timestamp()
		RETURNING `+businessRevealColumns, id, reviewer.Version, seconds, reviewer.ValidUntil))
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	return approved, tx.Commit(ctx)
}

// RevokeBusinessReveal closes a request for the requester or its recorded reviewer.
func (r *UserRepository) RevokeBusinessReveal(ctx context.Context, id string) (BusinessRevealRequest, error) {
	if !businessRevealID.MatchString(id) {
		return BusinessRevealRequest{}, ErrBusinessMappingInput
	}
	tx, err := r.beginBusinessMapping(ctx, false)
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	grant, err := scanBusinessReveal(tx.QueryRow(ctx, `
		UPDATE sys_business_reveal_requests SET state = 'revoked'
		WHERE workspace_id = dbhub_workspace() AND id = $1::uuid
		  AND (requester_id = dbhub_actor() OR reviewer_id = dbhub_actor())
		RETURNING `+businessRevealColumns, id))
	if err != nil {
		return BusinessRevealRequest{}, err
	}
	return grant, tx.Commit(ctx)
}

type businessRevealFilter struct {
	key, version string
	records      string
}

func (f businessRevealFilter) predicate() string {
	return ` AND EXISTS (SELECT 1 FROM jsonb_to_recordset($2::jsonb) AS approved(key text, version text) WHERE ` +
		pgx.Identifier{"v", f.key}.Sanitize() + `::text COLLATE "C" = approved.key COLLATE "C" AND ` +
		pgx.Identifier{"v", f.version}.Sanitize() + `::text COLLATE "C" = approved.version COLLATE "C")`
}

func authorizedBusinessReveal(ctx context.Context, tx pgx.Tx, userID int64, in BusinessOutputPlanInput,
	row BusinessRowColumnPolicy, scope OrganizationScope, out BusinessOutputPolicy, fields map[string]businessFieldProtection) (*businessRevealFilter, error) {
	if userID != ActorID(ctx) || in.Channel != "interactive_check" || !validBusinessText(in.Purpose) ||
		!businessRevealID.MatchString(in.RevealRequestID) || !businessRevealFields(row, fields, in.Columns) {
		return nil, ErrBusinessReadDenied
	}
	grant, err := scanBusinessReveal(tx.QueryRow(ctx, `
		SELECT `+businessRevealColumns+` FROM sys_business_reveal_requests
		WHERE workspace_id = dbhub_workspace() AND id = $1::uuid AND requester_id = dbhub_actor()`, in.RevealRequestID))
	if err != nil {
		return nil, err
	}
	if grant.State != "approved" || !grant.current || grant.ReviewerID == nil || grant.ReviewerScopeVersion == nil ||
		grant.DatasetKey != out.DatasetKey || grant.Purpose != in.Purpose || grant.ScopeVersion != scope.Version ||
		grant.PolicyVersion != row.Version || grant.OutputPolicyVersion != out.Version ||
		grant.IdentityVersion != scope.IdentityVersion || grant.DatasetVersion != scope.DatasetVersion ||
		!validBusinessRevealRecords(grant.Records) {
		return nil, ErrBusinessReadDenied
	}
	for _, col := range in.Columns {
		if !slices.Contains(grant.Columns, col) {
			return nil, ErrBusinessReadDenied
		}
	}
	reviewer, err := reviewerBusinessScope(ctx, tx, *grant.ReviewerID, row, scope, out)
	if err != nil {
		return nil, err
	}
	if reviewer.Version != *grant.ReviewerScopeVersion {
		return nil, ErrBusinessReadDenied
	}
	records, err := json.Marshal(grant.Records)
	if err != nil {
		return nil, err
	}
	return &businessRevealFilter{key: out.RecordKeyColumn, version: out.RecordVersionColumn, records: string(records)}, nil
}
