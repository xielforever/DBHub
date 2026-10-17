package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// SourceConfig is deliberately a closed allowlist, with no authentication or
// credential-bearing proxy/tunnel fields. Public definitions never copy secrets.
type SourceConfig struct {
	Type               string            `json:"type"`
	Host               string            `json:"host"`
	Port               int               `json:"port"`
	Database           string            `json:"database"`
	SSLMode            string            `json:"ssl_mode"`
	ConnectionTimeout  int               `json:"connection_timeout"`
	Environment        string            `json:"environment"`
	TemplateRevisionID *int64            `json:"template_revision_id,omitempty"`
	Parameters         map[string]string `json:"parameters,omitempty"`
}

func ConfigOf(c *Connection) SourceConfig {
	return SourceConfig{c.Type, c.Host, c.Port, c.Database, c.SSLMode, c.ConnectionTimeout, c.Environment, c.TemplateRevisionID, c.Parameters}
}
func (s SourceConfig) Connection() *Connection {
	return &Connection{Type: s.Type, Host: s.Host, Port: s.Port, Database: s.Database, SSLMode: s.SSLMode, ConnectionTimeout: s.ConnectionTimeout, Environment: s.Environment, TemplateRevisionID: s.TemplateRevisionID, Parameters: s.Parameters}
}
func sameSourceConfig(a, b SourceConfig) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

type SourceDefinition struct {
	ID             int64        `json:"id"`
	FamilyID       string       `json:"family_id"`
	Revision       int          `json:"revision"`
	Name           string       `json:"name"`
	Description    string       `json:"description"`
	OwnerID        int64        `json:"owner_id"`
	Visibility     string       `json:"visibility"`
	Enabled        bool         `json:"enabled"`
	Config         SourceConfig `json:"config"`
	AccessMode     string       `json:"access_mode"`
	AccessVersion  int64        `json:"access_version"`
	ProfileVersion int64        `json:"profile_version"`
	Category       string       `json:"category"`
	Tags           []string     `json:"tags"`
	Maintainer     string       `json:"maintainer"`
	Contact        string       `json:"contact"`
	Favorite       bool         `json:"favorite"`
	LastUsedAt     *time.Time   `json:"last_used_at,omitempty"`
	AccessDenied   bool         `json:"access_denied,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	MyConnectionID *int64       `json:"my_connection_id,omitempty"`
	MyRevisionID   *int64       `json:"my_revision_id,omitempty"`
}

func scanDefinition(row pgx.Row) (*SourceDefinition, error) {
	d := &SourceDefinition{}
	var raw []byte
	if err := row.Scan(&raw); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, d); err != nil {
		return nil, err
	}
	return d, nil
}

const visibleDefinition = `dbhub_source_visible(d.id,$2)`

func (r *ConnectionRepository) Definition(ctx context.Context, id int64) (*SourceDefinition, error) {
	if ActorID(ctx) == 0 {
		return nil, ErrNotFound
	}
	return scanDefinition(r.pool.QueryRow(ctx, `SELECT to_jsonb(d)||`+familyJSON+` FROM sys_source_definitions d JOIN sys_source_families f USING(family_id) WHERE d.id=$1 AND `+visibleDefinition, id, ActorID(ctx)))
}
func (r *ConnectionRepository) Definitions(ctx context.Context) ([]SourceDefinition, error) {
	items, _, err := r.SearchDefinitions(ctx, DirectoryFilter{Limit: 100})
	return items, err
}
func canManageDefinition(ctx context.Context, d *SourceDefinition) bool {
	return d.Visibility == "private" && d.OwnerID == ActorID(ctx) || d.Visibility == "global" && ActorAdmin(ctx)
}

// InsertDefinition serializes append revisions and validates the current owner
// in the same transaction. Callers validate the closed configuration contract.
func insertDefinition(ctx context.Context, tx pgx.Tx, d *SourceDefinition, previous int64) error {
	if ActorID(ctx) == 0 || d.Visibility != "private" && d.Visibility != "global" {
		return ErrNotFound
	}
	if d.Visibility == "global" && !ActorAdmin(ctx) {
		return ErrNotFound
	}
	d.OwnerID = ActorID(ctx)
	d.Revision = 1
	if d.AccessMode == "" {
		d.AccessMode = "global"
	}
	if d.AccessMode != "global" && d.AccessMode != "restricted" {
		return fmt.Errorf("%w: 发布范围无效", ErrSourceInput)
	}
	if previous > 0 {
		old, e := scanDefinition(tx.QueryRow(ctx, `SELECT to_jsonb(d) FROM sys_source_definitions d WHERE id=$1`, previous))
		if e != nil {
			return e
		}
		if !canManageDefinition(ctx, old) {
			return ErrNotFound
		}
		if old.Visibility == "global" && d.Visibility != "global" {
			return fmt.Errorf("全局定义请使用停用修订撤回，不可改为私有")
		}
		d.FamilyID = old.FamilyID
		d.OwnerID = old.OwnerID
		// A family-wide lock also serializes append attempts based on older revisions.
		if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('source-definition:'||$1,0))`, d.FamilyID); e != nil {
			return e
		}
		var published bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_source_definitions WHERE family_id=$1::uuid AND visibility='global')`, d.FamilyID).Scan(&published); e != nil {
			return e
		}
		if published && !ActorAdmin(ctx) {
			return ErrNotFound
		}
		latest, e := scanDefinition(tx.QueryRow(ctx, `SELECT to_jsonb(d) FROM sys_source_definitions d WHERE family_id=$1::uuid ORDER BY revision DESC LIMIT 1`, d.FamilyID))
		if e != nil {
			return e
		}
		if latest.Enabled && latest.Visibility == d.Visibility && latest.Name == d.Name && latest.Description == d.Description && sameSourceConfig(latest.Config, d.Config) {
			*d = *latest
			return nil
		}
		if e = tx.QueryRow(ctx, `SELECT max(revision)+1 FROM sys_source_definitions WHERE family_id=$1::uuid`, d.FamilyID).Scan(&d.Revision); e != nil {
			return e
		}
	}
	if previous == 0 {
		if e := tx.QueryRow(ctx, `INSERT INTO sys_source_families(family_id,access_mode) VALUES(gen_random_uuid(),$1) RETURNING family_id::text,access_version,profile_version`, d.AccessMode).Scan(&d.FamilyID, &d.AccessVersion, &d.ProfileVersion); e != nil {
			return e
		}
	}
	raw, e := json.Marshal(d.Config)
	if e != nil {
		return e
	}
	return catalogError(tx.QueryRow(ctx, `INSERT INTO sys_source_definitions(family_id,revision,name,description,owner_id,visibility,config)
 VALUES(COALESCE(NULLIF($1,'')::uuid,gen_random_uuid()),$2,$3,$4,$5,$6,$7::jsonb) RETURNING id,family_id::text,enabled,created_at`, d.FamilyID, d.Revision, d.Name, d.Description, d.OwnerID, d.Visibility, string(raw)).Scan(&d.ID, &d.FamilyID, &d.Enabled, &d.CreatedAt))
}
func (r *ConnectionRepository) SaveDefinition(ctx context.Context, d *SourceDefinition, previous int64) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if e = insertDefinition(ctx, tx, d, previous); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	saved, e := r.Definition(ctx, d.ID)
	if e == nil {
		*d = *saved
	}
	return e
}
func (r *ConnectionRepository) SetDefinitionEnabled(ctx context.Context, id int64, enabled bool, expectedBindings *int64) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	// Bind/rebind takes a shared lock on the definition. Holding this exclusive
	// lock makes the impact check and stop atomic with admission of new bindings.
	d, e := scanDefinition(tx.QueryRow(ctx, `SELECT to_jsonb(d) FROM sys_source_definitions d WHERE id=$1 FOR UPDATE`, id))
	if e != nil {
		return e
	}
	if !canManageDefinition(ctx, d) {
		return ErrNotFound
	}
	if !enabled {
		if expectedBindings == nil || *expectedBindings < 0 {
			return ErrDefinitionConfirmation
		}
		var count int64
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM sys_connections WHERE definition_revision_id=$1`, id).Scan(&count); e != nil {
			return e
		}
		if count != *expectedBindings {
			return ErrDefinitionImpactChanged
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE sys_source_definitions SET enabled=$2 WHERE id=$1`, id, enabled); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// ResolvePersonal is for an explicitly authorized shared report/ticket. It finds
// only the actor's own binding to the exact published revision, never its secret.
func (r *ConnectionRepository) ResolvePersonal(ctx context.Context, sourceID int64) (int64, error) {
	var id int64
	e := r.pool.QueryRow(ctx, `SELECT mine.id FROM sys_connections source
 JOIN sys_source_definitions d ON d.id=source.definition_revision_id
 JOIN sys_connections mine ON mine.definition_revision_id=d.id AND mine.user_id=$2
 WHERE source.id=$1 AND d.visibility='global' AND d.enabled AND dbhub_source_visible(d.id,$2)`, sourceID, ActorID(ctx)).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, e
}

// ValidateCurrent is used again after queue admission and immediately before SQL
// dispatch. It never reacquires execution rights or silently changes identities.
func (r *ConnectionRepository) ValidateCurrent(ctx context.Context, c *Connection) error {
	if e := requireCurrentActor(ctx, r.pool); e != nil {
		return e
	}
	latest, _, e := r.GetSecrets(ctx, c.ID)
	if e != nil {
		return e
	}
	if !latest.UpdatedAt.Equal(c.UpdatedAt) {
		return ErrConnectionChanged
	}
	return nil
}

func (r *ConnectionRepository) prepareDefinition(ctx context.Context, tx pgx.Tx, c *Connection, old *Connection) error {
	if ActorID(ctx) == 0 || c.UserID != ActorID(ctx) {
		return ErrNotFound
	}
	if c.DefinitionRevisionID == nil {
		d := &SourceDefinition{Name: c.Name, Visibility: "private", Config: ConfigOf(c)}
		if e := insertDefinition(ctx, tx, d, 0); e != nil {
			return e
		}
		c.DefinitionRevisionID = &d.ID
		c.DefinitionFamilyID = d.FamilyID
		return nil
	}
	d, e := scanDefinition(tx.QueryRow(ctx, `SELECT to_jsonb(d) FROM sys_source_definitions d WHERE d.id=$1 AND `+visibleDefinition+` FOR SHARE`, *c.DefinitionRevisionID, ActorID(ctx)))
	if e != nil {
		return e
	}
	// Private target edits may append a revision. Take the append lock before
	// the family read lock to match management's lock order.
	if d.Visibility == "private" && !sameSourceConfig(ConfigOf(c), d.Config) {
		if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('source-definition:'||$1,0))`, d.FamilyID); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, `SELECT 1 FROM sys_source_families WHERE family_id=$1::uuid FOR SHARE`, d.FamilyID); e != nil {
		return e
	}
	var allowed bool
	if e = tx.QueryRow(ctx, `SELECT dbhub_source_visible($1,$2)`, d.ID, ActorID(ctx)).Scan(&allowed); e != nil {
		return e
	}
	if !allowed {
		return ErrSourceAccessDenied
	}
	if !d.Enabled {
		return ErrCatalogDisabled
	}
	if old != nil && old.DefinitionFamilyID != "" && old.DefinitionFamilyID != d.FamilyID {
		return fmt.Errorf("不能更换为其他数据源，请新建个人连接")
	}
	if !sameSourceConfig(ConfigOf(c), d.Config) {
		if d.Visibility != "private" || d.OwnerID != ActorID(ctx) {
			return fmt.Errorf("共享定义的连接目标不可覆盖，请选择并确认新的修订")
		}
		next := &SourceDefinition{Name: d.Name, Description: d.Description, Visibility: "private", Config: ConfigOf(c)}
		if e = insertDefinition(ctx, tx, next, d.ID); e != nil {
			return e
		}
		d = next
		c.DefinitionRevisionID = &d.ID
	}
	c.DefinitionFamilyID = d.FamilyID
	return nil
}
