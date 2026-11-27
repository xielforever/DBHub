package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

const BusinessReportMaxRows = 1000

func lockBusinessReportPolicy(ctx context.Context, tx pgx.Tx, dataset string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("business-report-policy:%d:%s", WorkspaceID(ctx), dataset))
	return err
}

func (r *ManagedReportRepository) resolveBusinessReportBinding(ctx context.Context, q reportQueryer, x *ManagedExecution) error {
	var enabled bool
	err := q.QueryRow(ctx, `SELECT b.dataset_key, b.isolation_id = $2 AND EXISTS (
		SELECT 1 FROM sys_iam_isolation_guard i
		WHERE i.isolation_id = b.isolation_id AND i.database_name = current_database())
		FROM sys_iam_isolated_business_reports b
		WHERE b.workspace_id = dbhub_workspace() AND b.report_id = $1`, x.ReportID, r.phase2ReportIsolation).Scan(&x.businessBinding, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err == nil && !enabled {
		return ErrManagedDenied
	}
	return err
}

func (r *ManagedReportRepository) useBusinessReportBinding(ctx context.Context, tx pgx.Tx, x *ManagedExecution) error {
	if err := r.resolveBusinessReportBinding(ctx, tx, x); err != nil {
		return err
	}
	if x.businessBinding != "" {
		_, err := tx.Exec(ctx, `SELECT set_config('dbhub.isolated_business_report_id', $1, true),
			set_config('dbhub.isolated_business_report_key', $2, true)`, strconv.FormatInt(x.ReportID, 10), r.phase2ReportIsolation)
		return err
	}
	return nil
}

func businessReportTarget(row BusinessRowColumnPolicy, reportID, publicationNo int64) BusinessReportTarget {
	d := row.Definition
	columns := make([]string, 0, len(d.AllowedColumns))
	for _, col := range d.AllowedColumns {
		columns = append(columns, pgx.Identifier{col}.Sanitize())
	}
	return BusinessReportTarget{
		ReportID: reportID, PublicationNo: publicationNo, AccountID: row.accountID,
		AccountVersion: row.accountVersion, DefinitionID: row.definitionID, Schema: d.Schema, View: d.View,
		ApprovedSQL: "SELECT " + strings.Join(columns, ", ") + " FROM " + pgx.Identifier{d.Schema, d.View}.Sanitize(),
	}
}

func businessReportTargetWithParameters(row BusinessRowColumnPolicy, x *ManagedExecution) (BusinessReportTarget, error) {
	ref := businessReportTarget(row, x.ReportID, x.PublicationNo)
	filters, err := businessReportParameters(row.Definition, x.Definition.Parameters)
	if err != nil {
		return ref, err
	}
	ref.Filters = filters
	if len(filters) > 0 {
		ref.ApprovedSQL += " WHERE " + strings.Join(businessReportPredicates(filters, false), " AND ")
	}
	return ref, nil
}

func (r *ManagedReportRepository) businessReportPolicy(ctx context.Context, tx pgx.Tx, x *ManagedExecution) (BusinessRowColumnPolicy, BusinessOutputPolicy, map[string]businessFieldProtection, error) {
	reject := fmt.Errorf("%w: 阶段 2 报表的批准视图、行列策略或字段分类不完整", ErrManagedConflict)
	row, err := readBusinessRowColumnPolicy(ctx, tx, x.businessBinding)
	if errors.Is(err, ErrNotFound) {
		return row, BusinessOutputPolicy{}, nil, reject
	}
	if err != nil {
		return row, BusinessOutputPolicy{}, nil, err
	}
	ref, err := businessReportTargetWithParameters(row, x)
	if err != nil {
		return row, BusinessOutputPolicy{}, nil, err
	}
	approvedSQL := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(x.Definition.SQL), ";"))
	if !row.current || !row.Definition.valid() || ref.ApprovedSQL != approvedSQL ||
		ref.AccountID != x.AccountID || ref.AccountVersion != x.AccountVersion || ref.DefinitionID != x.Definition.DefinitionID ||
		x.Connection == nil || x.Connection.Type != "postgres" || x.Connection.Template != nil ||
		(x.Definition.Database != "" && x.Definition.Database != x.Connection.Database) ||
		x.Manifest.MaxRows <= 0 || x.Manifest.TimeoutMS <= 0 ||
		(x.Definition.ExportPolicy != nil && (x.Definition.ExportPolicy.PageSizeParameter != "" || x.Definition.ExportPolicy.OffsetParameter != "")) {
		return row, BusinessOutputPolicy{}, nil, reject
	}
	out, err := readBusinessOutputPolicy(ctx, tx, x.businessBinding)
	if errors.Is(err, ErrNotFound) {
		return row, out, nil, reject
	}
	if err != nil {
		return row, out, nil, err
	}
	fields, err := out.protections(row.Definition.AllowedColumns)
	if err != nil || !out.current || out.RowColumnPolicyVersion != row.Version || out.DatasetVersion != row.DatasetVersion ||
		(out.ReportID != 0 && (out.ReportID != x.ReportID || out.PublicationNo != x.PublicationNo)) {
		return row, out, nil, reject
	}
	for _, column := range row.Definition.AllowedColumns {
		p := fields[column]
		if p.class == businessClassCredentialSecret || p.original == businessProtectionHidden || slices.Contains(row.Definition.DeniedColumns, column) {
			return row, out, nil, reject
		}
	}
	for _, filter := range ref.Filters {
		p := fields[filter.Column]
		if p.class != businessClassInternal || p.original != businessProtectionNone {
			return row, out, nil, reject
		}
	}
	if !validBusinessReportContract(x.Manifest.Contract, row.Definition.AllowedColumns, fields) {
		return row, out, nil, fmt.Errorf("%w: 报表契约与受管脱敏投影或时效字段不兼容", ErrManagedConflict)
	}
	return row, out, fields, nil
}

func validBusinessReportContract(contract *ApprovedContract, columns []string, fields map[string]businessFieldProtection) bool {
	if contract == nil {
		return true
	}
	c := contract.Declaration
	if len(c.Schema) != len(columns) {
		return false
	}
	for i, column := range c.Schema {
		if columns[i] != column.Name {
			return false
		}
		p := fields[column.Name]
		masked := p.class >= businessClassSensitive || p.original == businessProtectionMasked
		if masked && (!column.Nullable || c.Freshness.Mode == "watermark_column" && c.Freshness.Column == column.Name) {
			return false
		}
	}
	return true
}

// PrepareBusinessReport compiles a default-masked plan for the current actor.
// The caller may only use this after resolving the normal report or review access.
func (r *ManagedReportRepository) PrepareBusinessReport(ctx context.Context, x *ManagedExecution) error {
	if x.businessBinding == "" {
		return nil
	}
	tx, err := r.pool.BeginSnapshot(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	current := *x
	current.businessBinding = ""
	if err = r.useBusinessReportBinding(ctx, tx, &current); err != nil {
		return err
	}
	if current.businessBinding != x.businessBinding {
		return ErrManagedConflict
	}
	return r.prepareBusinessReport(ctx, tx, x)
}

func (r *ManagedReportRepository) prepareBusinessReport(ctx context.Context, tx pgx.Tx, x *ManagedExecution) error {
	row, out, fields, err := r.businessReportPolicy(ctx, tx, x)
	if err != nil {
		return err
	}
	scope, err := readOrganizationScope(ctx, tx, ActorID(ctx), x.businessBinding)
	if errors.Is(err, ErrNotFound) {
		return ErrManagedDenied
	}
	if err != nil {
		return err
	}
	if row.DatasetVersion != scope.DatasetVersion {
		return ErrManagedConflict
	}
	if err = checkCustodyReportBoundary(ctx, tx, x, row, out, scope); err != nil {
		return err
	}
	columns := row.Definition.AllowedColumns
	masked := make(map[string]bool, len(columns))
	for _, column := range columns {
		p := fields[column]
		masked[column] = p.class >= businessClassSensitive || p.original == businessProtectionMasked
	}
	ref, err := businessReportTargetWithParameters(row, x)
	if err != nil {
		return err
	}
	plan, err := compileBusinessReadPlan(row, scope, columns, masked, nil, BusinessReportMaxRows+1, ref.Filters)
	if errors.Is(err, ErrBusinessReadDenied) {
		return ErrManagedDenied
	}
	if err != nil {
		return err
	}
	x.BusinessPlan = &BusinessOutputPlan{BusinessReadPlan: plan, OutputPolicyVersion: out.Version, ReportTarget: ref}
	return nil
}

// BusinessPolicyStamp identifies the exact scope, versions and projection of a result.
func (x *ManagedExecution) BusinessPolicyStamp() string {
	if x.BusinessPlan == nil {
		return ""
	}
	p := x.BusinessPlan
	return reportDigest([]any{p, p.ReportTarget, p.Statement, p.Args})
}

func (r *ManagedReportRepository) checkBusinessReportSnapshot(ctx context.Context, x *ManagedExecution, stamp string) error {
	if err := r.PrepareBusinessReport(ctx, x); err != nil {
		return err
	}
	if x.BusinessPolicyStamp() != stamp {
		return ErrManagedConflict
	}
	return nil
}
