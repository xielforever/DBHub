package db

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

var errCustodyReadBoundary = fmt.Errorf("%w: 当前数据或依赖超出托管发布的原审批约束", ErrManagedConflict)

type custodyDashboardTile struct {
	id, publication int64
	key, contract   string
}

// A transfer preserves the original approval. A newly approved publication
// uses its own boundary, while a restore of the captured review keeps this guard.
func custodyProtectsReview(ctx context.Context, tx pgx.Tx, object availabilityObject, review int64) (bool, error) {
	var protected bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_content_custody_items
		WHERE workspace_id=dbhub_workspace() AND (report_id=$1 OR dashboard_id=$2)
		AND (state<>'transferred' OR captured->>'active_review_id'=$3::bigint::text))`,
		object.report, object.dashboard, review).Scan(&protected)
	return protected, err
}

func checkCustodyReportBoundary(ctx context.Context, tx pgx.Tx, x *ManagedExecution, row BusinessRowColumnPolicy, output BusinessOutputPolicy, scope OrganizationScope) error {
	if x.Validation {
		return nil
	}
	protected, err := custodyProtectsReview(ctx, tx, availabilityObject{report: x.ReportID}, x.ReviewID)
	if err != nil {
		return err
	}
	if protected {
		check, err := readReportReviewPolicy(ctx, tx, x.ReviewID)
		if errors.Is(err, ErrManagedConflict) {
			return errCustodyReadBoundary
		}
		if err != nil {
			return err
		}
		if check.requiresData && check.dataReviewer == nil ||
			!custodyReadCovered(check.boundary, x, row, output, scope) {
			return errCustodyReadBoundary
		}
	}
	if tile := x.custodyDashboard; tile != nil {
		return tile.check(ctx, tx, x, row, output, scope)
	}
	return nil
}

func (tile custodyDashboardTile) check(ctx context.Context, tx pgx.Tx, x *ManagedExecution, row BusinessRowColumnPolicy, output BusinessOutputPolicy, scope OrganizationScope) error {
	var review, assignment int64
	err := tx.QueryRow(ctx, `SELECT rv.id,rv.assignment_version FROM sys_managed_dashboards d
		JOIN sys_dashboard_releases re ON re.id=d.active_release_id
		JOIN sys_dashboard_reviews rv ON rv.id=re.review_id AND rv.status='approved'
		WHERE d.workspace_id=dbhub_workspace() AND d.id=$1 AND d.publication_no=$2 AND d.lifecycle='active'`,
		tile.id, tile.publication).Scan(&review, &assignment)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrManagedConflict
	}
	if err != nil {
		return err
	}
	protected, err := custodyProtectsReview(ctx, tx, availabilityObject{dashboard: tile.id}, review)
	if err != nil || !protected {
		return err
	}
	check, err := readDashboardReviewPolicy(ctx, tx, review, assignment)
	if errors.Is(err, ErrManagedConflict) {
		return errCustodyReadBoundary
	}
	if err != nil {
		return err
	}
	i := slices.IndexFunc(check.boundary.Cards, func(card dashboardReviewCard) bool { return card.Key == tile.key })
	if i < 0 || check.requiresData && check.dataReviewer == nil {
		return errCustodyReadBoundary
	}
	card := check.boundary.Cards[i]
	if card.ReportID != x.ReportID || card.Publication != x.PublicationNo || card.Contract != tile.contract ||
		!custodyReadCovered(card.Scope, x, row, output, scope) {
		return errCustodyReadBoundary
	}
	return nil
}

func custodyReadCovered(approved reportReviewBoundary, x *ManagedExecution, row BusinessRowColumnPolicy, output BusinessOutputPolicy, scope OrganizationScope) bool {
	if approved.Dataset != row.DatasetKey || approved.DatasetVersion != row.DatasetVersion ||
		approved.RowPolicy != row.Version || approved.OutputPolicy != output.Version ||
		approved.Source != x.Definition.DefinitionID || approved.Account != x.AccountID ||
		approved.Version != x.AccountVersion || approved.Generation != x.Generation ||
		approved.Definition != x.Manifest.ContentHash || approved.Manifest != x.Hash {
		return false
	}
	i := slices.IndexFunc(approved.Audience, func(member reportReviewAudience) bool { return member.UserID == scope.UserID })
	if i < 0 {
		return false
	}
	member := approved.Audience[i]
	return member.IdentityVersion == scope.IdentityVersion && subset(effectiveBusinessScopes(row.Definition, scope), member.Scopes)
}
