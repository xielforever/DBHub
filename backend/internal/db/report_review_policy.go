package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"
)

type reportReviewAudience struct {
	UserID          int64                    `json:"user_id"`
	IdentityVersion int64                    `json:"identity_version"`
	Version         int64                    `json:"scope_version"`
	Scopes          []OrganizationScopeEntry `json:"scopes"`
}

type reportReviewBoundary struct {
	Dataset        string                           `json:"dataset_key"`
	Schema         string                           `json:"schema"`
	View           string                           `json:"view"`
	Publication    int64                            `json:"base_publication_no"`
	DatasetVersion int64                            `json:"dataset_version"`
	Source         int64                            `json:"source_revision_id"`
	Account        int64                            `json:"account_id"`
	Version        int64                            `json:"account_version"`
	Generation     int64                            `json:"account_generation"`
	RowPolicy      int64                            `json:"row_policy_version"`
	OutputPolicy   int64                            `json:"output_policy_version"`
	Definition     string                           `json:"definition_sha256"`
	Manifest       string                           `json:"manifest_sha256"`
	Columns        []string                         `json:"columns"`
	MaskedColumns  []string                         `json:"masked_columns,omitempty"`
	Parameters     []BusinessReportParameterBinding `json:"parameters,omitempty"`
	Users          []int64                          `json:"user_ids"`
	Groups         []int64                          `json:"group_ids"`
	Audience       []reportReviewAudience           `json:"audience"`
}

type reportReviewPolicyCheck struct {
	boundary                reportReviewBoundary
	businessStamp           string
	requiresData            bool
	dataReviewer, dataGrant *int64
	dataStamp               *string
}

// useReportReviewPolicy activates the extra checks only for an explicitly enrolled report.
func (r *ManagedReportRepository) useReportReviewPolicy(ctx context.Context, tx pgx.Tx, x *ManagedExecution) (bool, error) {
	var enrolled bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_iam_isolated_report_reviews
		WHERE workspace_id=dbhub_workspace() AND report_id=$1)`, x.ReportID).Scan(&enrolled)
	if err != nil || !enrolled {
		return false, err
	}
	if err = r.useBusinessReportBinding(ctx, tx, x); err != nil {
		return true, err
	}
	if x.businessBinding == "" {
		return true, ErrManagedDenied
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('dbhub.isolated_report_review_id',$1,true)`, strconv.FormatInt(x.ReviewID, 10)); err != nil {
		return true, err
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT COALESCE(dbhub_isolated_report_review_dataset()=$1,false)`, x.businessBinding).Scan(&allowed)
	if err == nil && !allowed {
		err = ErrManagedDenied
	}
	if err != nil {
		return true, err
	}
	return true, lockBusinessReportPolicy(ctx, tx, x.businessBinding)
}

func reportReviewMember(ctx context.Context, tx pgx.Tx, row BusinessRowColumnPolicy, userID int64) (reportReviewAudience, error) {
	scope, err := readOrganizationScope(ctx, tx, userID, row.DatasetKey)
	if errors.Is(err, ErrNotFound) {
		return reportReviewAudience{}, ErrManagedDenied
	}
	if err != nil {
		return reportReviewAudience{}, err
	}
	scopes := effectiveBusinessScopes(row.Definition, scope)
	if len(scopes) == 0 || scope.DatasetVersion != row.DatasetVersion {
		return reportReviewAudience{}, ErrManagedDenied
	}
	return reportReviewAudience{UserID: userID, IdentityVersion: scope.IdentityVersion, Version: scope.Version, Scopes: scopes}, nil
}

func (r *ManagedReportRepository) reportReviewShape(ctx context.Context, tx pgx.Tx, x *ManagedExecution) (reportReviewBoundary, BusinessRowColumnPolicy, bool, error) {
	row, output, fields, err := r.businessReportPolicy(ctx, tx, x)
	if err != nil {
		return reportReviewBoundary{}, row, false, err
	}
	d := row.Definition
	filters, err := businessReportParameters(d, x.Definition.Parameters)
	if err != nil {
		return reportReviewBoundary{}, row, false, err
	}
	boundary := reportReviewBoundary{
		Dataset: row.DatasetKey, Schema: d.Schema, View: d.View,
		Publication: x.PublicationNo - 1, DatasetVersion: row.DatasetVersion,
		Source: x.Definition.DefinitionID, Account: x.AccountID, Version: x.AccountVersion, Generation: x.Generation,
		RowPolicy: row.Version, OutputPolicy: output.Version, Definition: x.Manifest.ContentHash, Manifest: x.Hash,
		Columns: d.AllowedColumns, Parameters: filters, Users: nonNilIDs(x.Manifest.UserIDs), Groups: nonNilIDs(x.Manifest.GroupIDs),
	}
	for _, column := range d.AllowedColumns {
		if p := fields[column]; p.class >= businessClassSensitive || p.original == businessProtectionMasked {
			boundary.MaskedColumns = append(boundary.MaskedColumns, column)
		}
	}
	return boundary, row, len(boundary.MaskedColumns) > 0, nil
}

func (r *ManagedReportRepository) reportReviewBoundary(ctx context.Context, tx pgx.Tx, x *ManagedExecution) (reportReviewBoundary, BusinessRowColumnPolicy, bool, error) {
	boundary, row, sensitive, err := r.reportReviewShape(ctx, tx, x)
	if err != nil {
		return boundary, row, false, err
	}
	if err = audience(ctx, tx, boundary.Users, boundary.Groups); err != nil {
		return boundary, row, false, err
	}
	ids, err := reportAudienceMembers(ctx, tx, boundary.Users, boundary.Groups)
	if err != nil {
		return boundary, row, false, err
	}
	if len(ids) == 0 {
		return boundary, row, false, fmt.Errorf("%w: 报表当前没有有效受众", ErrManagedConflict)
	}
	for _, id := range ids {
		member, err := reportReviewMember(ctx, tx, row, id)
		if err != nil {
			return boundary, row, false, err
		}
		boundary.Audience = append(boundary.Audience, member)
	}
	return boundary, row, sensitive, nil
}

func reportAudienceMembers(ctx context.Context, tx pgx.Tx, users, groups []int64) ([]int64, error) {
	rows, err := tx.Query(ctx, `SELECT uid FROM (
		SELECT unnest($1::bigint[]) AS uid
		UNION SELECT user_id FROM sys_user_group_members WHERE group_id=ANY($2::bigint[])
	) a WHERE dbhub_member_role(uid) IS NOT NULL ORDER BY uid`, nonNilIDs(users), nonNilIDs(groups))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

func reportReviewCoverage(ctx context.Context, tx pgx.Tx, boundary reportReviewBoundary, row BusinessRowColumnPolicy, reviewer, submitter int64, publishing bool) (reportReviewAudience, error) {
	if reviewer <= 0 || submitter <= 0 || reviewer == submitter ||
		(!publishing && slices.ContainsFunc(boundary.Audience, func(member reportReviewAudience) bool { return member.UserID == reviewer })) {
		return reportReviewAudience{}, ErrManagedDenied
	}
	member, err := reportReviewMember(ctx, tx, row, reviewer)
	if err != nil {
		return member, err
	}
	for _, viewer := range boundary.Audience {
		for _, scope := range viewer.Scopes {
			if !slices.Contains(member.Scopes, scope) {
				return member, ErrManagedDenied
			}
		}
	}
	return member, nil
}

func reportReviewExpanded(previous, next reportReviewBoundary) bool {
	if previous.Dataset != next.Dataset || previous.Source != next.Source || previous.Account != next.Account ||
		previous.Schema != next.Schema || previous.View != next.View {
		return true
	}
	for _, column := range next.Columns {
		if !slices.Contains(previous.Columns, column) {
			return true
		}
	}
	for _, column := range previous.MaskedColumns {
		if slices.Contains(next.Columns, column) && !slices.Contains(next.MaskedColumns, column) {
			return true
		}
	}
	for _, group := range next.Groups {
		if !slices.Contains(previous.Groups, group) {
			return true
		}
	}
	for _, before := range previous.Parameters {
		index := slices.IndexFunc(next.Parameters, func(current BusinessReportParameterBinding) bool {
			return current.Column == before.Column && current.Operator == before.Operator && current.Domain.Name == before.Domain.Name
		})
		if index < 0 || !businessReportDomainContains(before.Domain, next.Parameters[index].Domain) {
			return true
		}
	}
	for _, viewer := range next.Audience {
		index := slices.IndexFunc(previous.Audience, func(prior reportReviewAudience) bool { return prior.UserID == viewer.UserID })
		if index < 0 {
			return true
		}
		for _, scope := range viewer.Scopes {
			if !slices.Contains(previous.Audience[index].Scopes, scope) {
				return true
			}
		}
	}
	return false
}

func readReportReviewPolicy(ctx context.Context, tx pgx.Tx, review int64) (reportReviewPolicyCheck, error) {
	var out reportReviewPolicyCheck
	err := tx.QueryRow(ctx, `SELECT boundary,business_stamp,requires_data,data_reviewer_id,data_grant_id,data_stamp
		FROM sys_report_review_checks WHERE review_id=$1`, review).Scan(&out.boundary, &out.businessStamp, &out.requiresData, &out.dataReviewer, &out.dataGrant, &out.dataStamp)
	if errors.Is(err, pgx.ErrNoRows) {
		err = fmt.Errorf("%w: 当前版本缺少阶段 3 复核记录", ErrManagedConflict)
	}
	return out, err
}

func reportDataReviewerGrant(ctx context.Context, tx pgx.Tx, dataset string, reviewer int64) (int64, error) {
	var grant int64
	err := tx.QueryRow(ctx, `SELECT grant_id FROM sys_iam_isolated_report_data_reviewers
		WHERE workspace_id=dbhub_workspace() AND dataset_key=$1 AND user_id=$2
		  AND valid_until>statement_timestamp() AND dbhub_member_role(user_id) IS NOT NULL`, dataset, reviewer).Scan(&grant)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrManagedDenied
	}
	return grant, err
}

func reportReviewExecution(ctx context.Context, tx pgx.Tx, review int64, manifest ReportManifest, hash string, conn *Connection, account *ReportAccount) (*ManagedExecution, int64, error) {
	x := &ManagedExecution{ReviewID: review, AccountID: account.ID, AccountVersion: account.Version, Generation: account.Generation, Connection: conn, Manifest: manifest, Hash: hash}
	var raw []byte
	var submitter int64
	err := tx.QueryRow(ctx, `SELECT r.id,r.publication_no+1,v.definition,COALESCE(v.submitted_by,0)
		FROM sys_report_reviews rv JOIN sys_report_versions v ON v.id=rv.version_id
		JOIN sys_managed_reports r ON r.id=v.report_id WHERE rv.id=$1 FOR SHARE OF r`, review).Scan(&x.ReportID, &x.PublicationNo, &raw, &submitter)
	if err != nil {
		return nil, 0, managedError(err)
	}
	if err = json.Unmarshal(raw, &x.Definition); err != nil {
		return nil, 0, err
	}
	if manifest.ContentHash != reportDigest(x.Definition) || hash != reportDigest(manifest) {
		return nil, 0, ErrManagedConflict
	}
	return x, submitter, nil
}

func (r *ManagedReportRepository) signBusinessReportReview(ctx context.Context, tx pgx.Tx, review int64, manifest ReportManifest, hash, previewStamp, validationID, comment string, conn *Connection, account *ReportAccount) (bool, error) {
	x, submitter, err := reportReviewExecution(ctx, tx, review, manifest, hash, conn, account)
	if err != nil {
		return false, err
	}
	active, err := r.useReportReviewPolicy(ctx, tx, x)
	if err != nil || !active {
		return false, err
	}
	if err = r.prepareBusinessReport(ctx, tx, x); err != nil {
		return false, err
	}
	if previewStamp == "" || x.BusinessPolicyStamp() != previewStamp {
		return false, fmt.Errorf("%w: 数据策略已变化，请重新预览报表", ErrManagedConflict)
	}
	boundary, row, sensitive, err := r.reportReviewBoundary(ctx, tx, x)
	if err != nil {
		return false, err
	}
	business, err := reportReviewCoverage(ctx, tx, boundary, row, ActorID(ctx), submitter, false)
	if err != nil {
		return false, err
	}
	var previous reportReviewBoundary
	err = tx.QueryRow(ctx, `SELECT c.boundary FROM sys_managed_reports m
		JOIN sys_report_releases re ON re.id=m.active_release_id
		JOIN sys_report_review_checks c ON c.workspace_id=m.workspace_id AND c.review_id=re.review_id
		WHERE m.id=$1`, x.ReportID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	requiresData := errors.Is(err, pgx.ErrNoRows) || sensitive || reportReviewExpanded(previous, boundary)
	tag, err := tx.Exec(ctx, `INSERT INTO sys_report_review_checks(workspace_id,review_id,boundary,business_stamp,requires_data,business_reviewer_id,validation_id,business_comment)
		VALUES(dbhub_workspace(),$1,$2::jsonb,$3,$4,$5,$6::uuid,$7) ON CONFLICT DO NOTHING`,
		review, string(canonicalReportJSON(boundary)), reportDigest(business), requiresData, ActorID(ctx), validationID, comment)
	if err == nil && tag.RowsAffected() != 1 {
		err = ErrManagedConflict
	}
	return requiresData, err
}

func checkBusinessReviewSignature(ctx context.Context, tx pgx.Tx, x *ManagedExecution, validation string, business, submitter int64, boundary reportReviewBoundary, row BusinessRowColumnPolicy, check reportReviewPolicyCheck) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT dbhub_has_capability($1,'report_review') AND EXISTS(
		SELECT 1 FROM sys_report_validations WHERE id=$2::uuid AND review_id=$3 AND performed_by=$1
		  AND manifest_hash=$4 AND account_version=$5 AND account_generation=$6
		  AND business_policy_stamp<>'' AND created_at>statement_timestamp()-interval '15 minutes')`,
		business, validation, x.ReviewID, x.Hash, x.AccountVersion, x.Generation).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid || reportDigest(boundary) != reportDigest(check.boundary) {
		return fmt.Errorf("%w: 发布基线、复核资格或预览已变化", ErrManagedConflict)
	}
	cover, err := reportReviewCoverage(ctx, tx, boundary, row, business, submitter, false)
	if err != nil {
		return err
	}
	if reportDigest(cover) != check.businessStamp {
		return fmt.Errorf("%w: 业务复核人的数据范围已变化", ErrManagedConflict)
	}
	return nil
}

func (r *ManagedReportRepository) confirmReportPublicationPolicy(ctx context.Context, tx pgx.Tx, x *ManagedExecution, business, submitter int64, validation string) error {
	active, err := r.useReportReviewPolicy(ctx, tx, x)
	if err != nil || !active {
		return err
	}
	check, err := readReportReviewPolicy(ctx, tx, x.ReviewID)
	if err != nil {
		return err
	}
	boundary, row, _, err := r.reportReviewBoundary(ctx, tx, x)
	if err != nil {
		return err
	}
	if _, err = reportReviewCoverage(ctx, tx, boundary, row, ActorID(ctx), submitter, true); err != nil {
		return err
	}
	if err = checkBusinessReviewSignature(ctx, tx, x, validation, business, submitter, boundary, row, check); err != nil {
		return err
	}
	if !check.requiresData {
		return nil
	}
	if check.dataReviewer == nil || check.dataGrant == nil || check.dataStamp == nil || *check.dataReviewer == business {
		return fmt.Errorf("%w: 高风险报表缺少独立数据复核", ErrManagedConflict)
	}
	grant, err := reportDataReviewerGrant(ctx, tx, boundary.Dataset, *check.dataReviewer)
	if err != nil {
		return err
	}
	cover, err := reportReviewCoverage(ctx, tx, boundary, row, *check.dataReviewer, submitter, false)
	if err != nil {
		return err
	}
	if grant != *check.dataGrant || reportDigest(cover) != *check.dataStamp {
		return fmt.Errorf("%w: 数据复核授权或范围已变化", ErrManagedConflict)
	}
	return nil
}

// ReportDataReview is the independently authorized review of one frozen manifest.
type ReportDataReview struct {
	ReportID       int64                `json:"report_id"`
	ReviewID       int64                `json:"review_id"`
	ManifestSHA256 string               `json:"manifest_sha256"`
	Definition     ReportDefinition     `json:"definition"`
	Basis          reportReviewBoundary `json:"basis"`
}

type reportDataReviewState struct {
	detail ReportDataReview
	grant  int64
	stamp  string
}

func (r *ManagedReportRepository) reportDataReview(ctx context.Context, tx pgx.Tx, review int64) (reportDataReviewState, error) {
	var state reportDataReviewState
	var raw []byte
	var hash, validation string
	var accountID, business int64
	var ownerVersion *int64
	err := tx.QueryRow(ctx, `SELECT v.account_id,c.business_reviewer_id,v.manifest,v.manifest_hash,c.validation_id::text,v.business_owner_version
		FROM sys_report_reviews rv JOIN sys_report_review_checks c ON c.workspace_id=rv.workspace_id AND c.review_id=rv.id
		JOIN sys_report_validations v ON v.id=c.validation_id AND v.review_id=rv.id
		WHERE rv.id=$1 AND rv.status='pending' AND c.requires_data FOR UPDATE OF rv`, review).
		Scan(&accountID, &business, &raw, &hash, &validation, &ownerVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, ErrNotFound
	}
	if err != nil {
		return state, err
	}
	basis, err := reportBusinessOwnerForReview(ctx, tx, review)
	if err != nil {
		return state, err
	}
	if err = basis.checkValidation(ownerVersion); err != nil {
		return state, err
	}
	if err = reportBusinessReviewer(ctx, tx, basis, business); err != nil {
		return state, err
	}
	var manifest ReportManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return state, err
	}
	conn, _, account, err := r.loadAccount(ctx, tx, accountID)
	if err != nil {
		return state, err
	}
	x, submitter, err := reportReviewExecution(ctx, tx, review, manifest, hash, conn, account)
	if err != nil {
		return state, err
	}
	active, err := r.useReportReviewPolicy(ctx, tx, x)
	if err != nil {
		return state, err
	}
	if !active || ActorID(ctx) == business {
		return state, ErrManagedDenied
	}
	state.grant, err = reportDataReviewerGrant(ctx, tx, x.businessBinding, ActorID(ctx))
	if err != nil {
		return state, err
	}
	check, err := readReportReviewPolicy(ctx, tx, review)
	if err != nil {
		return state, err
	}
	boundary, row, _, err := r.reportReviewBoundary(ctx, tx, x)
	if err != nil {
		return state, err
	}
	cover, err := reportReviewCoverage(ctx, tx, boundary, row, ActorID(ctx), submitter, false)
	if err != nil {
		return state, err
	}
	if err = checkBusinessReviewSignature(ctx, tx, x, validation, business, submitter, boundary, row, check); err != nil {
		return state, err
	}
	state.stamp = reportDigest(cover)
	state.detail = ReportDataReview{ReportID: x.ReportID, ReviewID: review, ManifestSHA256: x.Hash, Definition: x.Definition, Basis: boundary}
	return state, nil
}

func (r *ManagedReportRepository) ReportDataReview(ctx context.Context, review int64) (ReportDataReview, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ReportDataReview{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	state, err := r.reportDataReview(ctx, tx, review)
	return state.detail, err
}

func (r *ManagedReportRepository) DecideReportDataReview(ctx context.Context, key string, review int64, hash, decision, comment string) (ReportCommandResult, error) {
	return r.command(ctx, key, "report-data-review", []any{review, hash, decision, comment}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: review}
		state, err := r.reportDataReview(ctx, tx, review)
		if err != nil {
			return out, err
		}
		if state.detail.ManifestSHA256 != hash {
			return out, ErrManagedConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE sys_report_review_checks SET data_reviewer_id=$2,data_grant_id=$3,data_stamp=$4,
			data_comment=$5,data_signed_at=statement_timestamp() WHERE review_id=$1`, review, ActorID(ctx), state.grant, state.stamp, comment); err != nil {
			return out, err
		}
		if decision == "approved" {
			_, err = tx.Exec(ctx, `UPDATE sys_report_reviews rv SET status='approved',decided_by=c.business_reviewer_id,
				decider_name=(SELECT username FROM sys_users WHERE id=c.business_reviewer_id),comment=c.business_comment,
				account_id=v.account_id,manifest=v.manifest,manifest_hash=v.manifest_hash,validation_id=v.id,decided_at=statement_timestamp()
				FROM sys_report_review_checks c JOIN sys_report_validations v ON v.id=c.validation_id AND v.review_id=c.review_id
				WHERE rv.id=$1 AND c.review_id=rv.id AND c.workspace_id=rv.workspace_id`, review)
		} else {
			_, err = tx.Exec(ctx, `UPDATE sys_report_reviews SET status='rejected',decided_by=$2,
				decider_name=(SELECT username FROM sys_users WHERE id=$2),comment=$3,decided_at=statement_timestamp() WHERE id=$1`,
				review, ActorID(ctx), comment)
		}
		if err != nil {
			return out, err
		}
		return out, updateReportAccessAfterReview(ctx, tx, review, decision, comment)
	})
}
