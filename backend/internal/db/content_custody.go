package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (r *UserRepository) captureContentCustody(ctx context.Context, tx pgx.Tx, user, sequence int64) error {
	if err := r.iamIsolation(ctx, tx); err != nil {
		if errors.Is(err, ErrManagedDenied) {
			return nil
		}
		return err
	}
	// Capture only governance metadata. An unavailable data source, card or
	// account cannot delay the membership restriction.
	_, err := tx.Exec(ctx, `INSERT INTO sys_content_custody_items(workspace_id,user_id,freeze_sequence,report_id,captured)
		SELECT m.workspace_id,m.owner_id,$3,m.id,jsonb_build_object(
			'name',m.draft->>'name','draft_version',m.draft_version,'latest_version',m.latest_version,
			'publication_no',m.publication_no,'active_review_id',re.review_id,'dataset',b.dataset_key,
			'draft_source_id',m.definition_revision_id,'approved_account_id',rv.account_id,
			'pending_reviews',COALESCE((SELECT jsonb_agg(jsonb_build_object('review_id',pr.id,
				'version',pv.number) ORDER BY pv.number,pr.id)
				FROM sys_report_versions pv JOIN sys_report_reviews pr ON pr.version_id=pv.id
				WHERE pv.workspace_id=m.workspace_id AND pv.report_id=m.id AND pr.status='pending'),'[]'))
		FROM sys_managed_reports m JOIN sys_iam_isolated_report_reviews rr
			ON rr.workspace_id=m.workspace_id AND rr.report_id=m.id
		JOIN sys_iam_isolated_business_reports b ON b.workspace_id=m.workspace_id AND b.report_id=m.id AND b.isolation_id=$4
		LEFT JOIN sys_report_releases re ON re.id=m.active_release_id
		LEFT JOIN sys_report_reviews rv ON rv.id=re.review_id
		WHERE m.workspace_id=$1 AND m.owner_id=$2 ON CONFLICT DO NOTHING`, WorkspaceID(ctx), user, sequence, r.iamIsolationID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sys_content_custody_items(workspace_id,user_id,freeze_sequence,dashboard_id,captured)
		SELECT d.workspace_id,d.owner_id,$3,d.id,jsonb_build_object(
			'name',d.draft->>'name','draft_version',d.draft_version,'latest_version',d.latest_version,
			'publication_no',d.publication_no,'active_review_id',re.review_id,'lifecycle',d.lifecycle,
			'pending_reviews',COALESCE((SELECT jsonb_agg(jsonb_build_object('review_id',pr.id,
				'version',pv.number,'reviewer_id',pr.reviewer_id) ORDER BY pv.number,pr.id)
				FROM sys_dashboard_reviews pr JOIN sys_dashboard_versions pv ON pv.id=pr.version_id
				WHERE pr.workspace_id=d.workspace_id AND pr.dashboard_id=d.id AND pr.status='pending'),'[]'),
			'report_dependencies',COALESCE((SELECT jsonb_agg(DISTINCT jsonb_build_object(
				'report_id',t->'report_id','contract_hash',t->'contract_hash','required',t->'required',
				'publication_no',m.publication_no))
				FROM jsonb_array_elements(
					CASE WHEN jsonb_typeof(d.draft->'tiles')='array' THEN d.draft->'tiles' ELSE '[]'::jsonb END ||
					CASE WHEN jsonb_typeof(v.definition->'tiles')='array' THEN v.definition->'tiles' ELSE '[]'::jsonb END
				) t LEFT JOIN sys_managed_reports m ON m.workspace_id=d.workspace_id
					AND to_jsonb(m.id)=t->'report_id'),'[]'))
		FROM sys_managed_dashboards d JOIN sys_iam_isolated_dashboard_reviews s
			ON s.workspace_id=d.workspace_id AND s.dashboard_id=d.id AND s.isolation_id=$4
		LEFT JOIN sys_dashboard_releases re ON re.id=d.active_release_id
		LEFT JOIN sys_dashboard_reviews rv ON rv.id=re.review_id
		LEFT JOIN sys_dashboard_versions v ON v.id=rv.version_id
		WHERE d.workspace_id=$1 AND d.owner_id=$2 AND d.lifecycle<>'deleted' ON CONFLICT DO NOTHING`,
		WorkspaceID(ctx), user, sequence, r.iamIsolationID)
	return err
}

func (r *UserRepository) checkPlannedCustodyExit(ctx context.Context, tx pgx.Tx, target int64) error {
	if err := r.iamIsolation(ctx, tx); err != nil {
		return err
	}
	var continuity bool
	err := tx.QueryRow(ctx, `WITH RECURSIVE valid AS (
		SELECT delegation_id,version,delegate_id,parent_id,parent_version,governance_actions
		FROM sys_iam_delegations WHERE workspace_id=dbhub_workspace()
		  AND delegate_id<>$1 AND issuer_id<>$1 AND revoked_at IS NULL
		  AND valid_from<=statement_timestamp() AND valid_until>statement_timestamp()
		  AND dbhub_iam_qualified(issuer_id) AND dbhub_iam_qualified(delegate_id)
	), continuing AS (
		SELECT * FROM valid WHERE parent_id IS NULL
		UNION ALL
		SELECT child.* FROM valid child JOIN continuing parent
		  ON child.parent_id=parent.delegation_id AND child.parent_version=parent.version
	) SELECT EXISTS(SELECT 1 FROM continuing WHERE 'member.freeze'=ANY(governance_actions)
			AND dbhub_iam_control_allowed(delegate_id,'space.members.exit'))
		AND EXISTS(SELECT 1 FROM continuing WHERE 'assignment.review'=ANY(governance_actions)
			AND dbhub_iam_control_allowed(delegate_id,'space.assign.manage'))`, target).Scan(&continuity)
	if err != nil {
		return err
	}
	if !continuity {
		return iamConflict("计划退出前须保留具备当前资格的其他治理人")
	}
	return nil
}

func guardCustodyTransfer(ctx context.Context, tx pgx.Tx, o availabilityObject) error {
	var held bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_content_custody_items WHERE `+sameAvailabilityObject+
		` AND state<>'transferred')`, o.report, o.dashboard).Scan(&held); err != nil {
		return err
	}
	if held {
		return fmt.Errorf("%w: 托管资产须经过候选接受和独立复核", ErrManagedConflict)
	}
	return nil
}

func guardCustodyPublication(ctx context.Context, tx pgx.Tx, o availabilityObject, review int64) error {
	var blocked bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_content_custody_items WHERE `+sameAvailabilityObject+
		` AND (state<>'transferred' OR completed_at >= CASE WHEN report_id IS NOT NULL THEN
			(SELECT v.created_at FROM sys_report_reviews rv JOIN sys_report_versions v ON v.id=rv.version_id WHERE rv.id=$3)
			ELSE (SELECT v.created_at FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE rv.id=$3)
			END))`, o.report, o.dashboard, review).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		return fmt.Errorf("%w: 资产托管期间暂停新发布，接任后请重新送审", ErrManagedConflict)
	}
	return nil
}
