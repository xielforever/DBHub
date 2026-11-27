package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

type dashboardReviewSnapshot struct {
	dashboard, publication, submitter, reviewer, assignment int64
	definition                                              DashboardDefinition
}

type dashboardReviewCard struct {
	Key         string               `json:"key"`
	ReportID    int64                `json:"report_id"`
	Publication int64                `json:"publication_no"`
	Contract    string               `json:"contract_sha256"`
	Scope       reportReviewBoundary `json:"scope"`
	Business    reportReviewAudience `json:"business_coverage"`
	Bindings    map[string]string    `json:"parameter_bindings,omitempty"`
}

type dashboardReviewBoundary struct {
	Definition  string                `json:"definition_sha256"`
	Publication int64                 `json:"base_publication_no"`
	Assignment  int64                 `json:"assignment_version"`
	Sensitive   bool                  `json:"sensitive"`
	Audience    []int64               `json:"audience"`
	GroupIDs    []int64               `json:"group_ids"`
	Groups      json.RawMessage       `json:"groups"`
	Parameters  []ReportParameter     `json:"parameters,omitempty"`
	Cards       []dashboardReviewCard `json:"cards"`
}

type dashboardReviewPolicyCheck struct {
	boundary     dashboardReviewBoundary
	requiresData bool
	business     int64
	comment      string
	dataReviewer *int64
	dataStamp    *string
}

// governedDashboard checks both the registered object and the isolated database.
func (r *ManagedDashboardRepository) governedDashboard(ctx context.Context, q reportQueryer, dashboard int64) (bool, error) {
	var enabled bool
	err := q.QueryRow(ctx, `SELECT s.isolation_id=$2 AND EXISTS (
		SELECT 1 FROM sys_iam_isolation_guard i WHERE i.isolation_id=s.isolation_id AND i.database_name=current_database())
		FROM sys_iam_isolated_dashboard_reviews s WHERE s.workspace_id=dbhub_workspace() AND s.dashboard_id=$1`,
		dashboard, r.reports.phase2ReportIsolation).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err == nil && !enabled {
		err = ErrManagedDenied
	}
	return enabled, err
}

func (r *ManagedDashboardRepository) requireGovernedDashboardReports(ctx context.Context, q reportQueryer, dashboard int64, d DashboardDefinition, refs []DashboardReport) error {
	governed, err := r.governedDashboard(ctx, q, dashboard)
	if err != nil || !governed {
		return err
	}
	for i, p := range refs {
		if !p.governed {
			return fmt.Errorf("%w: 仪表盘必需及可选卡片均须引用已通过阶段 3 复核的受管报表", ErrManagedConflict)
		}
		if _, err = governedDashboardParameters(d.Parameters, d.Tiles[i].ParameterBindings, p.Parameters); err != nil {
			return err
		}
	}
	return nil
}

func governedDashboardParameters(global []ReportParameter, bindings map[string]string, report []ReportParameter) ([]ReportParameter, error) {
	var used []ReportParameter
	for _, p := range report {
		name := bindings[p.Name]
		if name == "" {
			continue
		}
		i := slices.IndexFunc(global, func(p ReportParameter) bool { return p.Name == name })
		if i < 0 || !businessReportDomainContains(p, global[i]) {
			return nil, fmt.Errorf("%w: 全局筛选允许范围超出卡片报表的批准参数 %s", ErrManagedConflict, p.Label)
		}
		used = append(used, global[i])
	}
	return used, nil
}

type dashboardCardReviewInput struct {
	tile      DashboardTile
	report    DashboardReport
	execution *ManagedExecution
}

func (r *ManagedDashboardRepository) dashboardReviewCards(ctx context.Context, tx pgx.Tx, s dashboardReviewSnapshot, refs []DashboardReport) ([]dashboardCardReviewInput, error) {
	cards := make([]dashboardCardReviewInput, 0, len(refs))
	for i, p := range refs {
		if !p.governed {
			return nil, fmt.Errorf("%w: 卡片引用报表尚无阶段 3 复核", ErrManagedConflict)
		}
		conn, _, account, err := r.reports.loadAccount(ctx, tx, p.AccountID)
		if err != nil {
			return nil, err
		}
		x, _, err := reportReviewExecution(ctx, tx, p.reviewID, p.manifest, p.manifestHash, conn, account)
		if err != nil {
			return nil, err
		}
		x.PublicationNo = p.PublicationNo
		if err = r.reports.resolveBusinessReportBinding(ctx, tx, x); err != nil {
			return nil, err
		}
		cards = append(cards, dashboardCardReviewInput{s.definition.Tiles[i], p, x})
	}
	// Both a report publication and a dashboard review hold report row locks
	// before dataset locks. Across dashboards the dataset order is stable.
	slices.SortStableFunc(cards, func(a, b dashboardCardReviewInput) int {
		return strings.Compare(a.execution.businessBinding, b.execution.businessBinding)
	})
	return cards, nil
}

func (r *ManagedDashboardRepository) buildDashboardReviewBoundary(ctx context.Context, tx pgx.Tx, s dashboardReviewSnapshot, refs []DashboardReport, refresh string, dataReviewer int64, publish bool) (dashboardReviewBoundary, string, error) {
	d := s.definition
	boundary := dashboardReviewBoundary{Definition: reportDigest(d), Publication: s.publication, Assignment: s.assignment, GroupIDs: normalizedIDs(d.GroupIDs)}
	var err error
	boundary.Audience, err = reportAudienceMembers(ctx, tx, d.UserIDs, d.GroupIDs)
	if err != nil {
		return boundary, "", err
	}
	if containsID(boundary.Audience, s.reviewer) ||
		dataReviewer != 0 && (dataReviewer == s.reviewer || containsID(boundary.Audience, dataReviewer)) {
		return boundary, "", ErrManagedDenied
	}
	cards, err := r.dashboardReviewCards(ctx, tx, s, refs)
	if err != nil {
		return boundary, "", err
	}
	groups := append([]int64{}, d.GroupIDs...)
	for _, p := range refs {
		groups = append(groups, p.GroupIDs...)
	}
	if err = lockAudienceGroups(ctx, tx, groups); err != nil {
		return boundary, "", err
	}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_object_agg(id::text,version),'{}'::jsonb)
		FROM sys_user_groups WHERE id=ANY($1::bigint[])`, groups).Scan(&boundary.Groups); err != nil {
		return boundary, "", err
	}
	var dataCoverage []any
	for _, item := range cards {
		x, p, tile := item.execution, item.report, item.tile
		enrolled, err := r.reports.useReportReviewPolicy(ctx, tx, x)
		if err != nil {
			return boundary, "", err
		}
		if !enrolled {
			return boundary, "", ErrManagedConflict
		}
		scope, row, sensitive, err := r.reports.reportReviewShape(ctx, tx, x)
		if err != nil {
			return boundary, "", err
		}
		boundary.Sensitive = boundary.Sensitive || sensitive
		used, err := governedDashboardParameters(d.Parameters, tile.ParameterBindings, p.Parameters)
		if err != nil {
			return boundary, "", err
		}
		boundary.Parameters = append(boundary.Parameters, used...)
		for _, uid := range boundary.Audience {
			if !dashboardGrant(ctx, tx, p.UserIDs, p.GroupIDs, uid) {
				if tile.Required {
					return boundary, "", ErrManagedDenied
				}
				continue
			}
			member, err := reportReviewMember(ctx, tx, row, uid)
			if errors.Is(err, ErrManagedDenied) && !tile.Required {
				continue
			}
			if err != nil {
				return boundary, "", err
			}
			scope.Audience = append(scope.Audience, member)
		}
		approved, err := readReportReviewPolicy(ctx, tx, x.ReviewID)
		if err != nil {
			return boundary, "", err
		}
		if reportReviewExpanded(approved.boundary, scope) || approved.boundary.DatasetVersion != scope.DatasetVersion ||
			approved.boundary.RowPolicy != scope.RowPolicy || approved.boundary.OutputPolicy != scope.OutputPolicy ||
			approved.boundary.Version != scope.Version || approved.boundary.Generation != scope.Generation {
			return boundary, "", fmt.Errorf("%w: 卡片报表的当前数据边界已超出或变更，请重新复核报表", ErrManagedConflict)
		}
		business, err := reportReviewCoverage(ctx, tx, scope, row, s.reviewer, s.submitter, false)
		if err != nil {
			return boundary, "", err
		}
		if publish {
			if _, err = reportReviewCoverage(ctx, tx, scope, row, ActorID(ctx), s.submitter, true); err != nil {
				return boundary, "", err
			}
		}
		if dataReviewer != 0 {
			grant, err := reportDataReviewerGrant(ctx, tx, scope.Dataset, dataReviewer)
			if err != nil {
				return boundary, "", err
			}
			covered, err := reportReviewCoverage(ctx, tx, scope, row, dataReviewer, s.submitter, false)
			if err != nil {
				return boundary, "", err
			}
			dataCoverage = append(dataCoverage, []any{tile.Key, scope.Dataset, grant, covered})
		}
		if refresh != "" {
			if err = r.reports.prepareBusinessReport(ctx, tx, x); err != nil {
				return boundary, "", err
			}
			var ok bool
			err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sys_dashboard_refresh_tiles
				WHERE refresh_id=$1::uuid AND tile_key=$2 AND status='succeeded' AND business_policy_stamp=$3)`,
				refresh, tile.Key, x.BusinessPolicyStamp()).Scan(&ok)
			if err != nil {
				return boundary, "", err
			}
			if !ok {
				return boundary, "", fmt.Errorf("%w: 卡片实际预览的当前数据策略已变化", ErrManagedConflict)
			}
		}
		boundary.Cards = append(boundary.Cards, dashboardReviewCard{tile.Key, p.ID, p.PublicationNo, p.Contract.Hash, scope, business, tile.ParameterBindings})
	}
	return boundary, reportDigest(dataCoverage), nil
}

func dashboardReviewExpanded(previous, next dashboardReviewBoundary) bool {
	if next.Sensitive {
		return true
	}
	for _, uid := range next.Audience {
		if !containsID(previous.Audience, uid) {
			return true
		}
	}
	for _, gid := range next.GroupIDs {
		if !containsID(previous.GroupIDs, gid) {
			return true
		}
	}
	for _, card := range next.Cards {
		i := slices.IndexFunc(previous.Cards, func(p dashboardReviewCard) bool { return p.Key == card.Key && p.ReportID == card.ReportID })
		if i < 0 || previous.Cards[i].Contract != card.Contract || reportReviewExpanded(previous.Cards[i].Scope, card.Scope) ||
			reportDigest(previous.Cards[i].Bindings) != reportDigest(card.Bindings) {
			return true
		}
	}
	for _, p := range next.Parameters {
		i := slices.IndexFunc(previous.Parameters, func(o ReportParameter) bool { return o.Name == p.Name })
		if i < 0 || !businessReportDomainContains(previous.Parameters[i], p) {
			return true
		}
	}
	return false
}

func readDashboardReviewPolicy(ctx context.Context, tx pgx.Tx, review, assignment int64) (dashboardReviewPolicyCheck, error) {
	var out dashboardReviewPolicyCheck
	err := tx.QueryRow(ctx, `SELECT boundary,requires_data,business_reviewer_id,business_comment,data_reviewer_id,data_stamp
		FROM sys_dashboard_review_checks WHERE review_id=$1 AND assignment_version=$2`, review, assignment).
		Scan(&out.boundary, &out.requiresData, &out.business, &out.comment, &out.dataReviewer, &out.dataStamp)
	if errors.Is(err, pgx.ErrNoRows) {
		err = fmt.Errorf("%w: 当前指派缺少阶段 3 复核记录", ErrManagedConflict)
	}
	return out, err
}

func signDashboardBusinessReview(ctx context.Context, tx pgx.Tx, s dashboardReviewSnapshot, review int64, basis dashboardReviewBoundary, comment string) (bool, error) {
	var previous dashboardReviewBoundary
	err := tx.QueryRow(ctx, `SELECT c.boundary FROM sys_managed_dashboards d
		JOIN sys_dashboard_releases re ON re.id=d.active_release_id
		JOIN sys_dashboard_reviews rv ON rv.id=re.review_id
		JOIN sys_dashboard_review_checks c ON c.workspace_id=rv.workspace_id AND c.review_id=rv.id AND c.assignment_version=rv.assignment_version
		WHERE d.id=$1`, s.dashboard).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	requiresData := errors.Is(err, pgx.ErrNoRows) || dashboardReviewExpanded(previous, basis)
	tag, err := tx.Exec(ctx, `INSERT INTO sys_dashboard_review_checks(workspace_id,review_id,assignment_version,boundary,
		business_reviewer_id,business_comment,requires_data)
		VALUES(dbhub_workspace(),$1,$2,$3::jsonb,$4,$5,$6) ON CONFLICT DO NOTHING`,
		review, s.assignment, string(canonicalReportJSON(basis)), s.reviewer, comment, requiresData)
	if err == nil && tag.RowsAffected() != 1 {
		err = ErrManagedConflict
	}
	return requiresData, err
}

func (r *ManagedDashboardRepository) dashboardReviewBasis(ctx context.Context, tx pgx.Tx, review int64, d DashboardDefinition) (dashboardReviewSnapshot, bool, string, error) {
	s := dashboardReviewSnapshot{definition: d}
	var stamp string
	err := tx.QueryRow(ctx, `SELECT rv.dashboard_id,d.publication_no,COALESCE(v.submitted_by,0),rv.reviewer_id,
		rv.assignment_version,rv.governed_validation_stamp
		FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id
		JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id WHERE rv.id=$1`, review).
		Scan(&s.dashboard, &s.publication, &s.submitter, &s.reviewer, &s.assignment, &stamp)
	if err != nil {
		return s, false, "", managedError(err)
	}
	governed, err := r.governedDashboard(ctx, tx, s.dashboard)
	return s, governed, stamp, err
}

func dashboardReviewFresh(ctx context.Context, tx pgx.Tx, review int64, reviewer int64) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sys_dashboard_reviews rv
		JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id
		WHERE rv.id=$1 AND rv.reviewer_id=$2 AND dbhub_dashboard_reviewer_eligible($2)
		AND (NOT dbhub_iam_dashboard_matrix_enabled()
			OR rv.business_review_authority=dbhub_dashboard_review_authority($2))
		AND d.lifecycle='active' AND d.lifecycle_epoch=rv.lifecycle_epoch
		AND rv.expires_at>statement_timestamp() AND rv.validated_at>statement_timestamp()-interval '15 minutes')`, review, reviewer).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("%w: 仪表盘复核资格或逐卡预览已过期", ErrManagedConflict)
	}
	return nil
}

func (r *ManagedDashboardRepository) confirmDashboardPublicationPolicy(ctx context.Context, tx pgx.Tx, review int64, d DashboardDefinition, refs []DashboardReport) error {
	s, governed, stamp, err := r.dashboardReviewBasis(ctx, tx, review, d)
	if err != nil || !governed {
		return err
	}
	if s.submitter == ActorID(ctx) {
		return ErrManagedDenied
	}
	check, err := readDashboardReviewPolicy(ctx, tx, review, s.assignment)
	if err != nil {
		return err
	}
	if check.business != s.reviewer || stamp == "" {
		return ErrManagedConflict
	}
	var dataReviewer int64
	if check.requiresData {
		if check.dataReviewer == nil || check.dataStamp == nil {
			return fmt.Errorf("%w: 仪表盘缺少覆盖全部数据集的独立数据复核", ErrManagedConflict)
		}
		dataReviewer = *check.dataReviewer
	}
	basis, dataStamp, err := r.buildDashboardReviewBoundary(ctx, tx, s, refs, "", dataReviewer, true)
	if err != nil {
		return err
	}
	if got := reportDigest(basis); got != stamp || got != reportDigest(check.boundary) ||
		check.requiresData && dataStamp != *check.dataStamp {
		return fmt.Errorf("%w: 仪表盘发布基线、依赖或签署人的范围已变化", ErrManagedConflict)
	}
	return dashboardReviewFresh(ctx, tx, review, s.reviewer)
}

// DashboardDataReview is the frozen dashboard and the authorized cross-dataset evidence.
type DashboardDataReview struct {
	DashboardID    int64                   `json:"dashboard_id"`
	ReviewID       int64                   `json:"review_id"`
	ManifestSHA256 string                  `json:"manifest_sha256"`
	Definition     DashboardDefinition     `json:"definition"`
	Basis          dashboardReviewBoundary `json:"basis"`
}

func (r *ManagedDashboardRepository) dashboardDataReview(ctx context.Context, tx pgx.Tx, review int64) (DashboardDataReview, string, dashboardReviewPolicyCheck, error) {
	var out DashboardDataReview
	var check dashboardReviewPolicyCheck
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT v.definition FROM sys_dashboard_reviews rv
		JOIN sys_dashboard_versions v ON v.id=rv.version_id JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id
		WHERE rv.id=$1 AND rv.status='pending' AND rv.lifecycle_epoch=d.lifecycle_epoch AND d.lifecycle='active'
		AND rv.expires_at>statement_timestamp() FOR UPDATE OF rv,d`, review).Scan(&raw)
	if err != nil {
		return out, "", check, managedError(err)
	}
	if err = json.Unmarshal(raw, &out.Definition); err != nil {
		return out, "", check, err
	}
	s, governed, stamp, err := r.dashboardReviewBasis(ctx, tx, review, out.Definition)
	if err != nil {
		return out, "", check, err
	}
	if !governed {
		return out, "", check, ErrNotFound
	}
	if s.reviewer == ActorID(ctx) || s.submitter == ActorID(ctx) {
		return out, "", check, ErrManagedDenied
	}
	check, err = readDashboardReviewPolicy(ctx, tx, review, s.assignment)
	if err != nil {
		return out, "", check, err
	}
	if !check.requiresData || check.business != s.reviewer || check.dataReviewer != nil || stamp == "" {
		return out, "", check, ErrManagedConflict
	}
	refs, err := r.checkDefinition(ctx, tx, out.Definition, s.reviewer)
	if err != nil {
		return out, "", check, err
	}
	basis, dataStamp, err := r.buildDashboardReviewBoundary(ctx, tx, s, refs, "", ActorID(ctx), false)
	if err != nil {
		return out, "", check, err
	}
	if got := reportDigest(basis); got != stamp || got != reportDigest(check.boundary) {
		return out, "", check, ErrManagedConflict
	}
	if err = dashboardReviewFresh(ctx, tx, review, s.reviewer); err != nil {
		return out, "", check, err
	}
	out.DashboardID, out.ReviewID, out.ManifestSHA256, out.Basis = s.dashboard, review, stamp, basis
	return out, dataStamp, check, nil
}

func (r *ManagedDashboardRepository) DataReview(ctx context.Context, review int64) (DashboardDataReview, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DashboardDataReview{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	out, _, _, err := r.dashboardDataReview(ctx, tx, review)
	return out, err
}

func (r *ManagedDashboardRepository) DecideDataReview(ctx context.Context, key string, review int64, hash, decision, comment string) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-data-review", []any{review, hash, decision, comment}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: review}
		detail, stamp, check, err := r.dashboardDataReview(ctx, tx, review)
		if err != nil {
			return out, err
		}
		if detail.ManifestSHA256 != hash {
			return out, ErrManagedConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE sys_dashboard_review_checks SET data_reviewer_id=$3,data_stamp=$4,
			data_signed_at=statement_timestamp(),data_comment=$5 WHERE review_id=$1 AND assignment_version=$2`,
			review, detail.Basis.Assignment, ActorID(ctx), stamp, comment); err != nil {
			return out, err
		}
		decider := ActorID(ctx)
		if decision == "approved" {
			decider, comment = check.business, check.comment
		}
		_, err = tx.Exec(ctx, `UPDATE sys_dashboard_reviews SET status=$2,comment=$3,decided_by=$4,
			decided_at=statement_timestamp() WHERE id=$1`, review, decision, comment, decider)
		return out, err
	})
}
