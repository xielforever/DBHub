package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

var (
	ErrDefinitionImpactChanged = errors.New("绑定人数已变化，请刷新修订记录后重新确认")
	ErrDefinitionConfirmation  = errors.New("停用修订需要确认影响范围和当前绑定人数")
)

type DefinitionChange struct {
	Field  string `json:"field"`
	Label  string `json:"label"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type DefinitionRevision struct {
	SourceDefinition
	BindingCount     *int64             `json:"binding_count,omitempty"`
	PreviousRevision *int               `json:"previous_revision,omitempty"`
	Changes          []DefinitionChange `json:"changes"`
}

// DefinitionRevisions compares only rows visible to the caller. A private
// revision preceding a global publication must not leak through its diff.
// Aggregate binding counts are management information, never credential access.
func (r *ConnectionRepository) DefinitionRevisions(ctx context.Context, id int64, limit, offset int) ([]DefinitionRevision, int64, error) {
	d, err := r.Definition(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM sys_source_definitions WHERE family_id=$1::uuid AND (visibility='global' OR owner_id=$2)`, d.FamilyID, ActorID(ctx)).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `WITH history AS (
 SELECT d.*, lag(to_jsonb(d)) OVER(ORDER BY revision) AS previous
 FROM sys_source_definitions d WHERE family_id=$1::uuid AND (visibility='global' OR owner_id=$2))
 SELECT to_jsonb(h)-'previous',h.previous,
 CASE WHEN (h.visibility='private' AND h.owner_id=$2) OR (h.visibility='global' AND $3)
 THEN (SELECT count(*) FROM sys_connections c WHERE c.definition_revision_id=h.id) END
 FROM history h ORDER BY revision DESC LIMIT $4 OFFSET $5`, d.FamilyID, ActorID(ctx), ActorAdmin(ctx), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []DefinitionRevision{}
	for rows.Next() {
		var raw, previous []byte
		v := DefinitionRevision{Changes: []DefinitionChange{}}
		if err = rows.Scan(&raw, &previous, &v.BindingCount); err != nil {
			return nil, 0, err
		}
		if err = json.Unmarshal(raw, &v.SourceDefinition); err != nil {
			return nil, 0, err
		}
		if len(previous) > 0 {
			var old SourceDefinition
			if err = json.Unmarshal(previous, &old); err != nil {
				return nil, 0, err
			}
			v.PreviousRevision = &old.Revision
			v.Changes = definitionChanges(&old, &v.SourceDefinition)
		}
		items = append(items, v)
	}
	return items, total, rows.Err()
}

func definitionChanges(a, b *SourceDefinition) []DefinitionChange {
	out := []DefinitionChange{}
	add := func(field, label string, old, next any) {
		x, y := fmt.Sprint(old), fmt.Sprint(next)
		if x != y {
			out = append(out, DefinitionChange{field, label, x, y})
		}
	}
	add("name", "名称", a.Name, b.Name)
	add("description", "说明", a.Description, b.Description)
	add("visibility", "可见范围", a.Visibility, b.Visibility)
	add("type", "数据源类型", a.Config.Type, b.Config.Type)
	add("host", "主机", a.Config.Host, b.Config.Host)
	add("port", "端口", a.Config.Port, b.Config.Port)
	add("database", "数据库 / 服务", a.Config.Database, b.Config.Database)
	add("ssl_mode", "TLS", a.Config.SSLMode, b.Config.SSLMode)
	add("connection_timeout", "连接超时（秒）", a.Config.ConnectionTimeout, b.Config.ConnectionTimeout)
	add("environment", "环境", a.Config.Environment, b.Config.Environment)
	profile := func(id *int64) string {
		if id == nil {
			return ""
		}
		return fmt.Sprint(*id)
	}
	add("template_revision_id", "驱动规则修订 ID", profile(a.Config.TemplateRevisionID), profile(b.Config.TemplateRevisionID))
	keys := map[string]bool{}
	for k := range a.Config.Parameters {
		keys[k] = true
	}
	for k := range b.Config.Parameters {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		add("parameters."+k, "连接参数 · "+k, a.Config.Parameters[k], b.Config.Parameters[k])
	}
	return out
}
