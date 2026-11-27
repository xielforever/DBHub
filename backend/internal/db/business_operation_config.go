package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func operationCapability(ctx context.Context, tx pgx.Tx, cap string) error {
	var ok bool
	if e := tx.QueryRow(ctx, `SELECT dbhub_scope_allowed() AND dbhub_has_capability($1,$2)`, ActorID(ctx), cap).Scan(&ok); e != nil {
		return e
	}
	if !ok {
		return ErrManagedDenied
	}
	return nil
}
func (r *BusinessOperationRepository) Source(ctx context.Context, definition int64, username string) (*Connection, error) {
	if !ActorCapability(ctx, "datasource_manage") {
		return nil, ErrManagedDenied
	}
	var raw []byte
	if e := r.pool.QueryRow(ctx, `SELECT to_jsonb(d) FROM sys_source_definitions d WHERE id=$1 AND enabled AND visibility='global' AND dbhub_source_visible(id,$2)`, definition, ActorID(ctx)).Scan(&raw); e != nil {
		return nil, managedError(e)
	}
	var d SourceDefinition
	if e := json.Unmarshal(raw, &d); e != nil {
		return nil, e
	}
	c := d.Config.Connection()
	if c.Type != "postgres" || c.SSHTunnelID != nil || c.ProxyID != nil {
		return nil, fmt.Errorf("%w: 操作协议v1需要直连原生PostgreSQL数据源", ErrManagedInput)
	}
	c.Username = username
	c.Definition = &d
	c.DefinitionRevisionID = &d.ID
	c.DefinitionFamilyID = d.FamilyID
	return c, nil
}
func (r *BusinessOperationRepository) Accounts(ctx context.Context) ([]OperationAccount, error) {
	if !ActorCapability(ctx, "datasource_manage") {
		return nil, ErrManagedDenied
	}
	rows, e := r.pool.Query(ctx, `SELECT id,name,definition_revision_id,username,installation_id::text,enabled,version FROM sys_operation_accounts ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []OperationAccount{}
	for rows.Next() {
		var a OperationAccount
		if e = rows.Scan(&a.ID, &a.Name, &a.DefinitionID, &a.Username, &a.InstallationID, &a.Enabled, &a.Version); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (r *BusinessOperationRepository) AccountPassword(ctx context.Context, id int64) (string, error) {
	if !ActorCapability(ctx, "datasource_manage") {
		return "", ErrManagedDenied
	}
	var encrypted string
	if e := r.pool.QueryRow(ctx, `SELECT password FROM sys_operation_accounts WHERE id=$1`, id).Scan(&encrypted); e != nil {
		return "", managedError(e)
	}
	return r.cipher.Decrypt(encrypted)
}
func (r *BusinessOperationRepository) SaveAccount(ctx context.Context, key string, a OperationAccount, password string) (ReportCommandResult, error) {
	if strings.TrimSpace(a.Name) == "" || len(a.Name) > 100 || strings.TrimSpace(a.Username) == "" || len(a.Username) > 100 || a.DefinitionID <= 0 || a.InstallationID == "" {
		return ReportCommandResult{}, ErrManagedInput
	}
	return r.commands.command(ctx, key, "operation-account-save", []any{a, password}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: a.ID}
		if e := operationCapability(ctx, tx, "datasource_manage"); e != nil {
			return out, e
		}
		if e := managedDefinition(ctx, tx, a.DefinitionID); e != nil {
			return out, e
		}
		encrypted, e := r.cipher.Encrypt(password)
		if e != nil {
			return out, e
		}
		if a.ID == 0 {
			if password == "" {
				return out, ErrManagedInput
			}
			e = tx.QueryRow(ctx, `INSERT INTO sys_operation_accounts(name,definition_revision_id,username,password,installation_id,enabled) VALUES($1,$2,$3,$4,$5::uuid,$6) RETURNING id,version`, a.Name, a.DefinitionID, a.Username, encrypted, a.InstallationID, a.Enabled).Scan(&out.ID, &out.Version)
		} else {
			e = tx.QueryRow(ctx, `UPDATE sys_operation_accounts SET name=$3,enabled=$4,password=CASE WHEN $5='' THEN password ELSE $5 END,version=version+1 WHERE id=$1 AND version=$2 AND definition_revision_id=$6 AND username=$7 AND installation_id=$8::uuid RETURNING id,version`, a.ID, a.Version, a.Name, a.Enabled, encrypted, a.DefinitionID, a.Username, a.InstallationID).Scan(&out.ID, &out.Version)
			if e == pgx.ErrNoRows {
				return out, ErrManagedConflict
			}
		}
		return out, e
	})
}

const operationAudienceSQL = `(t.user_ids @> ARRAY[$2]::bigint[] OR EXISTS(SELECT 1 FROM sys_user_group_members gm WHERE gm.group_id=ANY(t.group_ids) AND gm.user_id=$2))`

func (r *BusinessOperationRepository) Templates(ctx context.Context, manage bool) ([]OperationTemplate, error) {
	if manage && !ActorAdmin(ctx) {
		return nil, ErrManagedDenied
	}
	rows, e := r.pool.Query(ctx, `SELECT t.id,t.account_id,t.kind,t.user_ids,t.group_ids,t.enabled,t.version FROM sys_operation_templates t JOIN sys_operation_accounts a ON a.id=t.account_id AND a.workspace_id=t.workspace_id JOIN sys_source_definitions d ON d.id=a.definition_revision_id AND d.workspace_id=a.workspace_id WHERE ($1 OR (t.enabled AND a.enabled AND d.enabled AND `+operationAudienceSQL+`)) ORDER BY t.id`, manage, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []OperationTemplate{}
	for rows.Next() {
		var t OperationTemplate
		if e = rows.Scan(&t.ID, &t.AccountID, &t.Kind, &t.UserIDs, &t.GroupIDs, &t.Enabled, &t.Version); e != nil {
			return nil, e
		}
		t.Spec, _ = OperationSpecification(t.Kind)
		if !manage {
			t.AccountID = 0
			t.UserIDs = nil
			t.GroupIDs = nil
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (r *BusinessOperationRepository) SaveTemplate(ctx context.Context, key string, t OperationTemplate) (ReportCommandResult, error) {
	if !ActorAdmin(ctx) {
		return ReportCommandResult{}, ErrManagedDenied
	}
	if _, e := OperationSpecification(t.Kind); e != nil {
		return ReportCommandResult{}, e
	}
	return r.commands.command(ctx, key, "operation-template-save", t, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: t.ID}
		if e := audience(ctx, tx, t.UserIDs, t.GroupIDs); e != nil {
			return out, e
		}
		var valid bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_operation_accounts a JOIN sys_source_definitions d ON d.id=a.definition_revision_id WHERE a.id=$1 AND a.enabled AND d.enabled)`, t.AccountID).Scan(&valid); e != nil {
			return out, e
		}
		if !valid {
			return out, ErrManagedDenied
		}
		var e error
		if t.ID == 0 {
			e = tx.QueryRow(ctx, `INSERT INTO sys_operation_templates(account_id,kind,user_ids,group_ids,enabled) VALUES($1,$2,$3,$4,$5) RETURNING id,version`, t.AccountID, t.Kind, nonNilIDs(t.UserIDs), nonNilIDs(t.GroupIDs), t.Enabled).Scan(&out.ID, &out.Version)
		} else {
			e = tx.QueryRow(ctx, `UPDATE sys_operation_templates SET user_ids=$3,group_ids=$4,enabled=$5,version=version+1 WHERE id=$1 AND version=$2 AND account_id=$6 AND kind=$7 RETURNING id,version`, t.ID, t.Version, nonNilIDs(t.UserIDs), nonNilIDs(t.GroupIDs), t.Enabled, t.AccountID, t.Kind).Scan(&out.ID, &out.Version)
			if e == pgx.ErrNoRows {
				return out, ErrManagedConflict
			}
		}
		return out, e
	})
}

// target is called only with the authenticated actor or a stored request owner.
// Shared locks are held through execution, so disabling an account/template or
// removing an audience member cannot race between this check and the DB write.
func (r *BusinessOperationRepository) target(ctx context.Context, tx pgx.Tx, id, owner int64) (*OperationTarget, error) {
	out := &OperationTarget{}
	var raw []byte
	var encrypted string
	e := tx.QueryRow(ctx, `SELECT t.id,t.account_id,t.kind,t.user_ids,t.group_ids,t.enabled,t.version,a.id,a.name,a.definition_revision_id,a.username,a.installation_id::text,a.enabled,a.version,a.password,to_jsonb(d) FROM sys_operation_templates t JOIN sys_operation_accounts a ON a.id=t.account_id AND a.workspace_id=t.workspace_id JOIN sys_source_definitions d ON d.id=a.definition_revision_id AND d.workspace_id=a.workspace_id WHERE t.id=$1 AND t.enabled AND a.enabled AND d.enabled AND d.visibility='global' AND dbhub_member_role($2) IS NOT NULL AND `+operationAudienceSQL+` FOR SHARE OF t,a,d`, id, owner).Scan(&out.Template.ID, &out.Template.AccountID, &out.Template.Kind, &out.Template.UserIDs, &out.Template.GroupIDs, &out.Template.Enabled, &out.Template.Version, &out.Account.ID, &out.Account.Name, &out.Account.DefinitionID, &out.Account.Username, &out.Account.InstallationID, &out.Account.Enabled, &out.Account.Version, &encrypted, &raw)
	if e != nil {
		return nil, managedError(e)
	}
	if _, e = tx.Exec(ctx, `SELECT 1 FROM sys_user_groups WHERE id=ANY($1::bigint[]) FOR SHARE`, nonNilIDs(out.Template.GroupIDs)); e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `SELECT 1 FROM sys_user_group_members WHERE group_id=ANY($1::bigint[]) AND user_id=$2 FOR SHARE`, nonNilIDs(out.Template.GroupIDs), owner); e != nil {
		return nil, e
	}
	var d SourceDefinition
	if e = json.Unmarshal(raw, &d); e != nil {
		return nil, e
	}
	c := d.Config.Connection()
	if c.Type != "postgres" {
		return nil, ErrManagedDenied
	}
	c.Username = out.Account.Username
	c.ID = -out.Account.ID
	c.Definition = &d
	c.DefinitionRevisionID = &d.ID
	c.DefinitionFamilyID = d.FamilyID
	out.Connection = c
	out.Password, e = r.cipher.Decrypt(encrypted)
	return out, e
}
func (r *BusinessOperationRepository) PreviewTarget(ctx context.Context, id, version int64) (*OperationTarget, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	target, e := r.target(ctx, tx, id, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	if target.Template.Version != version {
		return nil, ErrManagedConflict
	}
	return target, nil
}
