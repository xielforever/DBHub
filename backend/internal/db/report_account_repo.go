package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type reportQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (r *ManagedReportRepository) loadAccount(ctx context.Context, q reportQueryer, id int64) (*Connection, string, *ReportAccount, error) {
	a := &ReportAccount{}
	var encrypted string
	var raw, template []byte
	query := `SELECT a.id,a.name,a.definition_revision_id,d.name,a.username,a.enabled,a.version,a.credential_generation,a.password,to_jsonb(d),
 (SELECT to_jsonb(t)||jsonb_build_object('driver',to_jsonb(jd)) FROM sys_datasource_templates t JOIN sys_jdbc_drivers jd ON jd.id=t.driver_id WHERE t.id=d.template_revision_id)
 FROM sys_report_accounts a JOIN sys_source_definitions d ON d.id=a.definition_revision_id
 WHERE a.id=$1 AND a.enabled AND d.enabled AND d.visibility='global'`
	if _, ok := q.(pgx.Tx); ok {
		query += ` FOR SHARE OF a,d`
	}
	e := q.QueryRow(ctx, query, id).Scan(&a.ID, &a.Name, &a.DefinitionID, &a.SourceName, &a.Username, &a.Enabled, &a.Version, &a.Generation, &encrypted, &raw, &template)
	if e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, "", nil, ErrManagedDenied
		}
		return nil, "", nil, e
	}
	var d SourceDefinition
	if e = json.Unmarshal(raw, &d); e != nil {
		return nil, "", nil, e
	}
	c := d.Config.Connection()
	c.ID = -a.ID
	c.Username = a.Username
	c.DefinitionRevisionID = &d.ID
	c.DefinitionFamilyID = d.FamilyID
	c.Definition = &d
	if len(template) > 0 {
		c.Template = &DatasourceTemplate{}
		if e = json.Unmarshal(template, c.Template); e != nil {
			return nil, "", nil, e
		}
		if !c.Template.Enabled || !c.Template.Driver.Enabled || c.Template.Visibility != "global" || c.Template.Driver.Visibility != "global" {
			return nil, "", nil, ErrManagedDenied
		}
	}
	caps := c.SupportedCapabilities()
	if !caps.SQL || !caps.EnforcedReadOnly {
		return nil, "", nil, ErrManagedDenied
	}
	password, e := r.cipher.Decrypt(encrypted)
	return c, password, a, e
}

// AccountConnection is admin-only. Formal executions resolve their own approved
// account through ResolvePublished instead of this maintenance entry point.
func (r *ManagedReportRepository) AccountConnection(ctx context.Context, id int64) (*Connection, string, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, "", e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if e = reportActor(ctx, tx, true, false); e != nil {
		return nil, "", e
	}
	c, p, _, e := r.loadAccount(ctx, tx, id)
	return c, p, e
}
func (r *ManagedReportRepository) Accounts(ctx context.Context) ([]ReportAccount, error) {
	if !ActorAdmin(ctx) {
		return nil, ErrManagedDenied
	}
	rows, e := r.pool.Query(ctx, `SELECT a.id,a.name,a.definition_revision_id,d.name,a.username,a.enabled,a.version,a.credential_generation,
 (SELECT count(*) FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id JOIN sys_report_reviews rv ON rv.id=re.review_id WHERE rv.account_id=a.id)
 FROM sys_report_accounts a JOIN sys_source_definitions d ON d.id=a.definition_revision_id ORDER BY a.id DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ReportAccount{}
	for rows.Next() {
		var a ReportAccount
		if e = rows.Scan(&a.ID, &a.Name, &a.DefinitionID, &a.SourceName, &a.Username, &a.Enabled, &a.Version, &a.Generation, &a.ReportCount); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (r *ManagedReportRepository) CreateAccount(ctx context.Context, key, name string, definitionID int64, username, password string) (ReportCommandResult, error) {
	return r.command(ctx, key, "account-create", []any{name, definitionID, username, password}, true, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		if e := managedDefinition(ctx, tx, definitionID); e != nil {
			return out, e
		}
		encrypted, e := r.cipher.Encrypt(password)
		if e != nil {
			return out, e
		}
		e = tx.QueryRow(ctx, `INSERT INTO sys_report_accounts(name,definition_revision_id,username,password,created_by) VALUES($1,$2,$3,$4,$5) RETURNING id,version`, name, definitionID, username, encrypted, ActorID(ctx)).Scan(&out.ID, &out.Version)
		return out, e
	})
}
func (r *ManagedReportRepository) UpdateAccount(ctx context.Context, key string, id, expected int64, enabled *bool, password string, expectedReports *int64) (ReportCommandResult, error) {
	return r.command(ctx, key, "account-update", []any{id, expected, enabled, password, expectedReports}, true, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		var version int64
		if e := tx.QueryRow(ctx, `SELECT version FROM sys_report_accounts WHERE id=$1 FOR UPDATE`, id).Scan(&version); e != nil {
			return out, e
		}
		if version != expected {
			return out, ErrManagedConflict
		}
		if enabled != nil && !*enabled {
			var count int64
			if e := tx.QueryRow(ctx, `SELECT count(*) FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id JOIN sys_report_reviews rv ON rv.id=re.review_id WHERE rv.account_id=$1`, id).Scan(&count); e != nil {
				return out, e
			}
			if expectedReports == nil || count != *expectedReports {
				return out, fmt.Errorf("%w: 请确认当前受影响的已发布报表数量", ErrManagedConflict)
			}
		}
		var secret *string
		if password != "" {
			value, e := r.cipher.Encrypt(password)
			if e != nil {
				return out, e
			}
			secret = &value
		}
		e := tx.QueryRow(ctx, `UPDATE sys_report_accounts SET enabled=COALESCE($2,enabled),password=COALESCE($3,password),credential_generation=credential_generation+CASE WHEN $3::text IS NULL THEN 0 ELSE 1 END,version=version+1,updated_at=now() WHERE id=$1 RETURNING version`, id, enabled, secret).Scan(&out.Version)
		return out, e
	})
}
