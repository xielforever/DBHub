package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrScriptReadOnly = errors.New("只读脚本不能修改或转授")
	ErrScriptVersion  = errors.New("脚本已被修改，请核对最新版本后再保存")
)

type ScriptFolder struct {
	ID       int64  `json:"id"`
	ParentID *int64 `json:"parent_id"`
	Name     string `json:"name"`
}

type ScriptTarget struct {
	ConnectionID int64  `json:"connection_id"`
	Database     string `json:"database"`
	State        string `json:"state,omitempty"`
	Signature    string `json:"-"`
}

type Script struct {
	ID        int64         `json:"id"`
	Name      string        `json:"name"`
	Type      string        `json:"script_type"`
	FolderID  *int64        `json:"folder_id,omitempty"`
	Shared    bool          `json:"shared,omitempty"`
	ReadOnly  bool          `json:"read_only"`
	OwnerName string        `json:"owner_name,omitempty"`
	UpdatedAt time.Time     `json:"updated_at"`
	Content   *string       `json:"content,omitempty"`
	Version   int64         `json:"version,omitempty"`
	Target    *ScriptTarget `json:"default_target,omitempty"`
}

type ScriptReader struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"display_name"`
}

type SnippetRepository struct{ pool *ScopedPool }

func NewSnippetRepository(pool *pgxpool.Pool) *SnippetRepository {
	return &SnippetRepository{pool: scopedPool(pool)}
}

func (r *SnippetRepository) Folders(ctx context.Context) ([]ScriptFolder, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,parent_id,name FROM sys_script_folders
	WHERE workspace_id=$1 AND user_id=$2 ORDER BY lower(name),id`, WorkspaceID(ctx), ActorID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[ScriptFolder])
}

func (r *SnippetRepository) NewFolder(ctx context.Context, parent *int64, name string) (ScriptFolder, error) {
	row := r.pool.QueryRow(ctx, `INSERT INTO sys_script_folders (workspace_id,user_id,parent_id,name)
	SELECT $1,$2,$3,$4 WHERE $3::bigint IS NULL OR EXISTS(
		SELECT 1 FROM sys_script_folders WHERE workspace_id=$1 AND user_id=$2 AND id=$3)
	RETURNING id,parent_id,name`, WorkspaceID(ctx), ActorID(ctx), parent, name)
	return readFolder(row)
}

func (r *SnippetRepository) RenameFolder(ctx context.Context, id int64, name string) (ScriptFolder, error) {
	return readFolder(r.pool.QueryRow(ctx, `UPDATE sys_script_folders SET name=$4,updated_at=now()
	WHERE id=$1 AND workspace_id=$2 AND user_id=$3 RETURNING id,parent_id,name`, id, WorkspaceID(ctx), ActorID(ctx), name))
}

func readFolder(row pgx.Row) (ScriptFolder, error) {
	var folder ScriptFolder
	err := row.Scan(&folder.ID, &folder.ParentID, &folder.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return folder, err
}

const scriptShared = `EXISTS(SELECT 1 FROM sys_script_read_grants g JOIN sys_workspace_members m
	ON (m.workspace_id,m.user_id,m.version)=(g.workspace_id,g.reader_user_id,g.reader_version)
	WHERE g.workspace_id=s.workspace_id AND g.script_id=s.id AND dbhub_member_role(m.user_id) IS NOT NULL)`

const scriptListColumns = `s.id,s.name,s.script_type,s.folder_id,s.updated_at,s.user_id<>$2,
	CASE WHEN s.user_id=$2 THEN '' ELSE u.username END,` + scriptShared

func (r *SnippetRepository) List(ctx context.Context, shared bool, folder *int64, needle string, limit, offset int) ([]Script, int64, error) {
	tx, err := r.pool.BeginSnapshot(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if !shared && folder != nil {
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_script_folders WHERE workspace_id=$1 AND user_id=$2 AND id=$3)`,
			WorkspaceID(ctx), ActorID(ctx), folder).Scan(&exists)
		if err != nil {
			return nil, 0, err
		}
		if !exists {
			return nil, 0, ErrNotFound
		}
	}
	const where = ` FROM sys_sql_snippets s JOIN sys_users u ON u.id=s.user_id
	WHERE s.workspace_id=$1 AND (s.user_id<>$2)=$3 AND
	($3 OR s.folder_id IS NOT DISTINCT FROM $4::bigint)
	AND ($5='' OR strpos(lower(s.name),lower($5))>0 OR strpos(lower(s.sql_text),lower($5))>0)`
	args := []any{WorkspaceID(ctx), ActorID(ctx), shared, folder, needle}
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*)`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT `+scriptListColumns+where+` ORDER BY s.updated_at DESC,s.id DESC LIMIT $6 OFFSET $7`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Script{}
	for rows.Next() {
		item, err := readScript(rows, false)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, tx.Commit(ctx)
}

func readScript(row pgx.Row, detail bool) (Script, error) {
	var record Script
	fields := []any{&record.ID, &record.Name, &record.Type, &record.FolderID, &record.UpdatedAt,
		&record.ReadOnly, &record.OwnerName, &record.Shared}
	var targetID *int64
	target := ScriptTarget{}
	if detail {
		fields = append(fields, &record.Content, &record.Version, &targetID, &target.Database, &target.Signature)
	}
	if err := row.Scan(fields...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Script{}, ErrNotFound
		}
		return Script{}, err
	}
	if record.ReadOnly {
		record.FolderID, record.Version, record.Shared = nil, 0, false
	} else if targetID != nil {
		target.ConnectionID = *targetID
		record.Target = &target
	}
	return record, nil
}

func readScriptDetail(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64) (Script, error) {
	return readScript(q.QueryRow(ctx, `SELECT `+scriptListColumns+
		`,s.sql_text,s.version,s.bound_connection_id,s.database_name,s.binding_signature FROM sys_sql_snippets s
		JOIN sys_users u ON u.id=s.user_id WHERE s.workspace_id=$1 AND s.id=$3`,
		WorkspaceID(ctx), ActorID(ctx), id), true)
}

func (r *SnippetRepository) Get(ctx context.Context, id int64) (Script, error) {
	return readScriptDetail(ctx, r.pool, id)
}

func (r *SnippetRepository) Save(ctx context.Context, record Script) (Script, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Script{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var targetID *int64
	database, signature := "", ""
	if record.Target != nil {
		targetID, database, signature = &record.Target.ConnectionID, record.Target.Database, record.Target.Signature
	}
	if record.FolderID != nil {
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_script_folders
		WHERE workspace_id=$1 AND user_id=$2 AND id=$3)`, WorkspaceID(ctx), ActorID(ctx), record.FolderID).Scan(&exists)
		if err != nil {
			return Script{}, err
		}
		if !exists {
			return Script{}, ErrNotFound
		}
	}
	if record.ID == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO sys_sql_snippets
		(workspace_id,user_id,name,script_type,sql_text,folder_id,connection_id,bound_connection_id,database_name,binding_signature)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$8,$9) RETURNING id`,
			WorkspaceID(ctx), ActorID(ctx), record.Name, record.Type, record.Content, record.FolderID, targetID, database, signature).Scan(&record.ID)
	} else {
		tag, updateErr := tx.Exec(ctx, `UPDATE sys_sql_snippets SET name=$5,sql_text=$6,folder_id=$7,
		connection_id=CASE WHEN bound_connection_id IS DISTINCT FROM $8::bigint OR binding_signature<>$10 THEN $8 ELSE connection_id END,
		bound_connection_id=$8,database_name=$9,binding_signature=$10,version=version+1,updated_at=now()
		WHERE workspace_id=$1 AND user_id=$2 AND id=$3 AND version=$4`, WorkspaceID(ctx), ActorID(ctx), record.ID, record.Version,
			record.Name, record.Content, record.FolderID, targetID, database, signature)
		err = updateErr
		if err == nil && tag.RowsAffected() == 0 {
			return Script{}, ErrScriptVersion
		}
	}
	if err != nil {
		return Script{}, err
	}
	saved, err := readScriptDetail(ctx, tx, record.ID)
	if err != nil {
		return Script{}, err
	}
	return saved, tx.Commit(ctx)
}

func (r *SnippetRepository) Readers(ctx context.Context, id int64, candidates bool, needle string) ([]ScriptReader, error) {
	tx, err := r.pool.BeginSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = checkOwnedScript(ctx, tx, id); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT m.user_id,u.username FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id
	WHERE m.workspace_id=$1 AND m.user_id<>$2 AND dbhub_member_role(m.user_id) IS NOT NULL
	AND ($4 OR EXISTS (SELECT 1 FROM sys_script_read_grants g WHERE g.workspace_id=m.workspace_id
		AND g.script_id=$3 AND g.reader_user_id=m.user_id AND g.reader_version=m.version))
	AND ($5='' OR strpos(lower(u.username),lower($5))>0)
	ORDER BY lower(u.username),m.user_id`,
		WorkspaceID(ctx), ActorID(ctx), id, candidates, needle)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ScriptReader])
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

func checkOwnedScript(ctx context.Context, tx pgx.Tx, id int64) error {
	var owned bool
	err := tx.QueryRow(ctx, `SELECT user_id=$2 FROM sys_sql_snippets WHERE workspace_id=$1 AND id=$3`,
		WorkspaceID(ctx), ActorID(ctx), id).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err == nil && !owned {
		return ErrScriptReadOnly
	}
	return err
}

func (r *SnippetRepository) GrantReader(ctx context.Context, id, reader int64, grant bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = checkOwnedScript(ctx, tx, id); err != nil {
		return err
	}
	var changed int64
	if grant {
		var version int64
		err = tx.QueryRow(ctx, `SELECT version FROM sys_workspace_members WHERE workspace_id=$1 AND user_id=$2
		AND dbhub_member_role(user_id) IS NOT NULL`, WorkspaceID(ctx), reader).Scan(&version)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO sys_script_read_grants (workspace_id,owner_user_id,script_id,reader_user_id,reader_version)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (workspace_id,script_id,reader_user_id)
		DO UPDATE SET reader_version=excluded.reader_version,granted_at=now()
		WHERE sys_script_read_grants.reader_version<>excluded.reader_version`, WorkspaceID(ctx), ActorID(ctx), id, reader, version)
		if err != nil {
			return err
		}
		changed = tag.RowsAffected()
	} else {
		tag, err := tx.Exec(ctx, `DELETE FROM sys_script_read_grants
		WHERE workspace_id=$1 AND owner_user_id=$2 AND script_id=$3 AND reader_user_id=$4`, WorkspaceID(ctx), ActorID(ctx), id, reader)
		if err != nil {
			return err
		}
		changed = tag.RowsAffected()
	}
	if changed > 0 {
		action := "SCRIPT_READ_REVOKE"
		if grant {
			action = "SCRIPT_READ_GRANT"
		}
		_, err = tx.Exec(ctx, `INSERT INTO sys_audit_logs(event_id,created_at,workspace_id,user_id,action,resource_type,status,context)
		VALUES ($1::uuid,now(),$2,$3,$4,'WORKBENCH_SCRIPT',1,
			jsonb_build_object('outcome','succeeded','script_id',$5::bigint,'reader_user_id',$6::bigint))`,
			dashboardUUID(), WorkspaceID(ctx), ActorID(ctx), action, id, reader)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
