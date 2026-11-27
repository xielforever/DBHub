package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auditctx"
)

// A new report's proposed business owner belongs to its immutable first review.
// Once the report is published, its current owner and revision govern reviews.
type reportBusinessOwnerBasis struct {
	current, proposed *int64
	version           int64
}

func (b reportBusinessOwnerBasis) required() bool {
	return b.current != nil || b.proposed != nil
}

func (b reportBusinessOwnerBasis) supervisor() *int64 {
	if b.current != nil {
		return b.current
	}
	return b.proposed
}

func (b reportBusinessOwnerBasis) checkValidation(version *int64) error {
	if b.required() && (version == nil || *version != b.version) {
		return fmt.Errorf("%w: 业务负责人已变化，请按当前负责人重新预览并送审", ErrManagedConflict)
	}
	return nil
}

func sameReportBusinessOwnerVersion(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func reportBusinessOwnerForReview(ctx context.Context, tx pgx.Tx, reviewID int64) (reportBusinessOwnerBasis, error) {
	var basis reportBusinessOwnerBasis
	err := tx.QueryRow(ctx, `SELECT m.business_owner_id,m.business_owner_version,v.proposed_business_owner_id
		FROM sys_report_reviews rv JOIN sys_report_versions v ON v.id=rv.version_id
		JOIN sys_managed_reports m ON m.id=v.report_id WHERE rv.id=$1 FOR SHARE OF m`, reviewID).
		Scan(&basis.current, &basis.version, &basis.proposed)
	return basis, managedError(err)
}

func reportBusinessReviewer(ctx context.Context, q reportQueryer, basis reportBusinessOwnerBasis, actor int64) error {
	if !basis.required() {
		return nil
	}
	var allowed bool
	err := q.QueryRow(ctx, `SELECT dbhub_report_platform_admin($1) OR
		($1=$2 AND dbhub_report_business_supervisor($1))`, actor, basis.supervisor()).Scan(&allowed)
	if err == nil && !allowed {
		err = ErrManagedDenied
	}
	return err
}

func checkReportBusinessSupervisor(ctx context.Context, tx pgx.Tx, id int64) error {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT dbhub_report_business_supervisor($1)`, id).Scan(&allowed)
	if err == nil && !allowed {
		err = fmt.Errorf("%w: 请选择本空间当前有效的业务主管", ErrManagedInput)
	}
	return err
}

// ReportBusinessOwnerCandidate identifies a current supervisor in the report's space.
type ReportBusinessOwnerCandidate struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
}

func (r *ManagedReportRepository) BusinessOwnerCandidates(ctx context.Context, id int64) ([]ReportBusinessOwnerCandidate, error) {
	var allowed bool
	err := r.pool.QueryRow(ctx, `SELECT dbhub_report_owner_mode() AND
		((m.owner_id=$2 AND m.business_owner_id IS NULL) OR dbhub_report_platform_admin($2))
		FROM sys_managed_reports m WHERE m.id=$1`, id, ActorID(ctx)).Scan(&allowed)
	if err != nil {
		return nil, managedError(err)
	}
	if !allowed {
		return nil, ErrManagedDenied
	}
	rows, err := r.pool.Query(ctx, `SELECT u.id,u.username FROM sys_workspace_members m
		JOIN sys_users u ON u.id=m.user_id WHERE m.workspace_id=dbhub_workspace()
		AND dbhub_report_business_supervisor(u.id) ORDER BY u.username,u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReportBusinessOwnerCandidate{}
	for rows.Next() {
		var entry ReportBusinessOwnerCandidate
		if err = rows.Scan(&entry.UserID, &entry.Username); err != nil {
			return nil, err
		}
		items = append(items, entry)
	}
	return items, rows.Err()
}

func (r *ManagedReportRepository) TransferBusinessOwner(ctx context.Context, key string, id, expected, owner int64) (ReportCommandResult, error) {
	return r.command(ctx, key, "business-owner-transfer", []int64{id, expected, owner}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		var out ReportCommandResult
		var current *int64
		var version int64
		var enabled bool
		err := tx.QueryRow(ctx, `SELECT business_owner_id,business_owner_version,dbhub_report_owner_mode()
			FROM sys_managed_reports WHERE id=$1 FOR UPDATE`, id).Scan(&current, &version, &enabled)
		if err != nil {
			return out, err
		}
		if !enabled {
			return out, ErrManagedDenied
		}
		if expected != version {
			return out, ErrManagedConflict
		}
		if err = checkReportBusinessSupervisor(ctx, tx, owner); err != nil {
			return out, err
		}
		if current != nil && *current == owner {
			return ReportCommandResult{ID: id, Version: version, BusinessOwnerID: current, PreviousBusinessOwnerID: current}, nil
		}
		auditctx.Update(ctx, func(d *auditctx.Data) {
			d.ReportOwnerID = owner
			d.ReportVersion = version + 1
		})
		err = tx.QueryRow(ctx, `UPDATE sys_managed_reports SET business_owner_id=$2,
			business_owner_version=business_owner_version+1,updated_at=now() WHERE id=$1
			RETURNING id,business_owner_version`, id, owner).Scan(&out.ID, &out.Version)
		out.BusinessOwnerID, out.PreviousBusinessOwnerID = &owner, current
		return out, err
	})
}
