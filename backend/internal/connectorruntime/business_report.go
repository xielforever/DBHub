package connectorruntime

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/db"
)

// VerifyBusinessReportView confirms the approved view and account in the same read-only transaction.
func VerifyBusinessReportView(ctx context.Context, handle *RelDB, ref db.BusinessReportTarget, username string) error {
	// The zero-row statement's relation lock persists through the read-only
	// transaction, so the verified view cannot be swapped before dispatch.
	qualified := pgx.Identifier{ref.Schema, ref.View}.Sanitize()
	option := ExecuteOptions{MaxRows: 1}
	if _, err := ExecuteWithOptions(ctx, handle, "SELECT 1 FROM "+qualified+" LIMIT 0", true, option); err != nil {
		return db.ErrBusinessReadDenied
	}
	type filterColumn struct {
		Name string `json:"column_name"`
		Kind string `json:"kind"`
	}
	columns := make([]filterColumn, 0, len(ref.Filters))
	for _, filter := range ref.Filters {
		columns = append(columns, filterColumn{filter.Column, filter.Domain.Type})
	}
	required, err := json.Marshal(columns)
	if err != nil {
		return err
	}
	option.Args = []any{ref.Schema, ref.View, username, string(required)}
	out, err := ExecuteWithOptions(ctx, handle, `
		SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
		          WHERE n.nspname=$1 AND c.relname=$2 AND c.relkind='v'),
		       current_user::text=$3 AND NOT r.rolsuper AND NOT r.rolbypassrls,
		       current_setting('transaction_read_only')='on',
		       NOT EXISTS (
		           SELECT 1 FROM jsonb_to_recordset($4::jsonb) AS p(column_name text,kind text)
		           WHERE NOT EXISTS (
		               SELECT 1 FROM pg_catalog.pg_attribute a
		               JOIN pg_catalog.pg_class c ON c.oid=a.attrelid
		               JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
		               JOIN pg_catalog.pg_type t ON t.oid=a.atttypid AND t.typnamespace='pg_catalog'::regnamespace
		               WHERE n.nspname=$1 AND c.relname=$2 AND c.relkind='v'
		                 AND a.attnum>0 AND NOT a.attisdropped AND a.attname=p.column_name
		                 AND CASE p.kind
		                     WHEN 'enum' THEN t.typname IN ('text','varchar')
		                     WHEN 'boolean' THEN t.typname='bool'
		                     WHEN 'integer' THEN t.typname IN ('int2','int4','int8','numeric')
		                     WHEN 'decimal' THEN t.typname IN ('int2','int4','int8','numeric')
		                     WHEN 'date' THEN t.typname='date'
		                     ELSE false END))
		FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`, true, option)
	if err != nil || out == nil || len(out.Rows) != 1 || len(out.Rows[0]) != 4 ||
		out.Rows[0][0] != true || out.Rows[0][1] != true || out.Rows[0][2] != true || out.Rows[0][3] != true {
		return db.ErrBusinessReadDenied
	}
	return nil
}
