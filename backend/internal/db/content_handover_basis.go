package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func (r *UserRepository) custodyHandoverBasis(ctx context.Context, tx pgx.Tx, item contentCustodyItem) ([]any, error) {
	kind := item.asset.family()
	var memberVersion int64
	err := tx.QueryRow(ctx, `SELECT version FROM sys_workspace_members WHERE workspace_id=dbhub_workspace()
		AND user_id=$1 AND dbhub_iam_has_action($1,$2) FOR SHARE`, item.candidate, kind+".design").Scan(&memberVersion)
	if err == pgx.ErrNoRows {
		return nil, iamDenied("候选人缺少本空间当前开发资格")
	}
	if err != nil {
		return nil, err
	}
	var draftNo, publication, owner int64
	var scope any
	if item.asset.report > 0 {
		var source, review int64
		err = tx.QueryRow(ctx, `SELECT m.owner_id,m.draft_version,m.publication_no,m.definition_revision_id,COALESCE(re.review_id,0)
			FROM sys_managed_reports m LEFT JOIN sys_report_releases re ON re.id=m.active_release_id
			WHERE m.workspace_id=dbhub_workspace() AND m.id=$1 FOR UPDATE OF m`, item.asset.report).
			Scan(&owner, &draftNo, &publication, &source, &review)
		if err == nil {
			scope, err = r.custodyReportScope(ctx, tx, item.asset.report, item.candidate, source, review)
		}
	} else {
		var draft DashboardDefinition
		var live *DashboardDefinition
		err = tx.QueryRow(ctx, `SELECT d.owner_id,d.draft_version,d.publication_no,d.draft,v.definition
			FROM sys_managed_dashboards d LEFT JOIN sys_dashboard_releases re ON re.id=d.active_release_id
			LEFT JOIN sys_dashboard_reviews rv ON rv.id=re.review_id AND rv.status='approved'
			LEFT JOIN sys_dashboard_versions v ON v.id=rv.version_id
			WHERE d.workspace_id=dbhub_workspace() AND d.id=$1 AND d.lifecycle<>'deleted' FOR UPDATE OF d`, item.asset.dashboard).
			Scan(&owner, &draftNo, &publication, &draft, &live)
		if err == nil {
			scope, err = r.custodyDashboardScope(ctx, tx, item.candidate, draft, live)
		}
	}
	if err != nil {
		return nil, managedError(err)
	}
	if owner != item.owner {
		return nil, iamConflict("当前责任人已变化")
	}
	return []any{memberVersion, owner, draftNo, publication, scope}, nil
}

func (r *UserRepository) custodyReportScope(ctx context.Context, tx pgx.Tx, report, candidate, source, review int64) ([]any, error) {
	var dataset string
	err := tx.QueryRow(ctx, `SELECT b.dataset_key
		FROM sys_iam_isolated_report_reviews rr
		JOIN sys_iam_isolated_business_reports b USING(workspace_id,report_id)
		WHERE rr.workspace_id=dbhub_workspace() AND rr.report_id=$1 AND b.isolation_id=$2`, report, r.iamIsolationID).Scan(&dataset)
	if err != nil {
		return nil, managedError(err)
	}
	if err = lockBusinessReportPolicy(ctx, tx, dataset); err != nil {
		return nil, err
	}
	row, err := readBusinessRowColumnPolicy(ctx, tx, dataset)
	if err != nil {
		return nil, err
	}
	if source == 0 {
		source = row.definitionID
	}
	var entitled bool
	err = tx.QueryRow(ctx, `SELECT dbhub_source_visible($1,$2) AND EXISTS(SELECT 1 FROM sys_source_definitions
		WHERE workspace_id=dbhub_workspace() AND id=$1 AND enabled AND visibility='global')`, source, candidate).Scan(&entitled)
	if err != nil {
		return nil, err
	}
	if !entitled || !row.current || row.definitionID != source {
		return nil, iamDenied("候选人缺少当前来源资格或受管数据范围")
	}
	member, err := reportReviewMember(ctx, tx, row, candidate)
	if err != nil {
		return nil, err
	}
	result := []any{row.DatasetVersion, row.Version, member}
	if review == 0 {
		return result, nil
	}
	approved, err := readReportReviewPolicy(ctx, tx, review)
	if err != nil {
		return nil, err
	}
	output, err := readBusinessOutputPolicy(ctx, tx, dataset)
	if err != nil {
		return nil, err
	}
	if !output.current || approved.boundary.RowPolicy != row.Version || approved.boundary.OutputPolicy != output.Version ||
		approved.boundary.DatasetVersion != row.DatasetVersion || approved.boundary.Source != source ||
		approved.boundary.Account != row.accountID || approved.boundary.Version != row.accountVersion {
		return nil, iamConflict("正式报表当前数据或执行账号边界已变化")
	}
	if err = lockAudienceGroups(ctx, tx, approved.boundary.Groups); err != nil {
		return nil, err
	}
	viewers, err := reportAudienceMembers(ctx, tx, approved.boundary.Users, approved.boundary.Groups)
	if err != nil {
		return nil, err
	}
	current := approved.boundary
	current.Audience = nil
	for _, viewer := range viewers {
		scope, err := reportReviewMember(ctx, tx, row, viewer)
		if err != nil {
			return nil, err
		}
		if !subset(scope.Scopes, member.Scopes) {
			return nil, iamDenied("候选人当前数据范围无法覆盖正式对象")
		}
		current.Audience = append(current.Audience, scope)
	}
	if reportReviewExpanded(approved.boundary, current) {
		return nil, iamConflict("当前获准受众或数据范围已扩大，请重新复核报表")
	}
	return append(result, review, current.Audience, output.Version), nil
}

func (r *UserRepository) custodyDashboardScope(ctx context.Context, tx pgx.Tx, candidate int64, draft DashboardDefinition, live *DashboardDefinition) ([]any, error) {
	dashboard := ManagedDashboardRepository{}
	definitions := []DashboardDefinition{draft}
	if live != nil {
		definitions = append(definitions, *live)
	}
	var results []any
	for _, definition := range definitions {
		if len(definition.Tiles) == 0 {
			continue
		}
		refs, err := dashboard.checkDefinition(ctx, tx, definition, candidate)
		if err != nil {
			return nil, err
		}
		for i, ref := range refs {
			if !ref.governed {
				return nil, iamConflict("候选所接任的仪表盘须引用已复核的受管报表")
			}
			if _, err = governedDashboardParameters(definition.Parameters, definition.Tiles[i].ParameterBindings, ref.Parameters); err != nil {
				return nil, err
			}
			scope, err := r.custodyReportScope(ctx, tx, ref.ID, candidate, ref.manifest.DefinitionID, ref.reviewID)
			if err != nil {
				return nil, err
			}
			results = append(results, []any{ref.ID, ref.PublicationNo, ref.Contract.Hash, scope})
		}
	}
	return results, nil
}
