package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// TechnicalElementFilter 控制清单范围。位置参数用于详情中的相关元素浏览。
type TechnicalElementFilter struct {
	ConnectionID int64
	Database     string
	Schema       *string
	Table        string
	Keyword      string
	Kinds        []string
	Levels       []string
	Tags         []string
	Offset       int
	Limit        int
}

type TechnicalElement struct {
	Kind           string          `json:"kind"`
	ConnectionID   int64           `json:"connection_id"`
	ConnectionName string          `json:"connection_name"`
	ConnectionType string          `json:"connection_type"`
	Database       string          `json:"database"`
	Schema         string          `json:"schema"`
	Table          string          `json:"table"`
	Name           string          `json:"name"`
	Comment        string          `json:"comment"`
	Level          string          `json:"level"`
	Tags           []string        `json:"tags"`
	OwnerName      string          `json:"owner_name"`
	SyncedAt       time.Time       `json:"synced_at"`
	Properties     json.RawMessage `json:"properties"`
}

type TechnicalElementSource struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type TechnicalElementPage struct {
	Items         []TechnicalElement       `json:"items"`
	Total         int64                    `json:"total"`
	Sources       []TechnicalElementSource `json:"sources"`
	AvailableTags []string                 `json:"available_tags"`
}

// ListTechnicalElements 展开当前用户可见快照，按单个元素筛选并分页。
func (r *MetadataRepository) ListTechnicalElements(ctx context.Context, f TechnicalElementFilter) (TechnicalElementPage, error) {
	var result TechnicalElementPage
	err := r.pool.QueryRow(ctx, technicalElementsSQL, ActorID(ctx), f.ConnectionID, f.Database, f.Schema,
		f.Table, f.Keyword, f.Kinds, f.Levels, f.Tags, f.Offset, f.Limit).
		Scan(&result.Items, &result.Total, &result.Sources, &result.AvailableTags)
	if err != nil {
		return result, fmt.Errorf("读取技术元素: %w", err)
	}
	return result, nil
}

// 父级从同一可见范围归并。先应用连接和对象定位，再展开快照的声明式结构。
const technicalElementsSQL = `
WITH sources AS MATERIALIZED (
	SELECT c.id, c.name, c.type
	FROM sys_connections c
	WHERE c.user_id = $1 AND dbhub_source_visible(c.definition_revision_id, c.user_id)
), snapshots AS MATERIALIZED (
	SELECT s.connection_id, s.database_name, s.schema_name, s.table_name, s.table_type,
	       s.synced_at, s.table_comment,
	       CASE WHEN jsonb_typeof(s.raw_columns) = 'array' THEN s.raw_columns ELSE '[]'::jsonb END AS columns,
	       CASE WHEN jsonb_typeof(s.raw_indexes) = 'array' THEN s.raw_indexes ELSE '[]'::jsonb END AS indexes,
	       CASE WHEN jsonb_typeof(s.raw_keys) = 'array' THEN s.raw_keys ELSE '[]'::jsonb END AS keys
	FROM sys_meta_snapshots s JOIN sources c ON c.id = s.connection_id
	WHERE ($2::bigint = 0 OR s.connection_id = $2)
	  AND ($3::text = '' OR s.database_name = $3)
	  AND ($4::text IS NULL OR s.schema_name = $4)
	  AND ($5::text = '' OR s.table_name = $5)
), elements (connection_id, database, schema, "table", name, kind, kind_order, ordinal,
              synced_at, comment, column_name, properties) AS (
	SELECT s.connection_id, s.database_name, '', '', s.database_name, 'database', 0, 0,
	       MAX(s.synced_at), '', NULL::text,
	       jsonb_build_object('schema_count', COUNT(DISTINCT NULLIF(s.schema_name, '')))
	FROM snapshots s
	WHERE $3 = '' AND (COALESCE(cardinality($7::text[]), 0) = 0 OR 'database' = ANY($7::text[]))
	GROUP BY s.connection_id, s.database_name
	UNION ALL
	SELECT s.connection_id, s.database_name, s.schema_name, '', s.schema_name, 'schema', 1, 0,
	       MAX(s.synced_at), '', NULL, jsonb_build_object('object_count', COUNT(*))
	FROM snapshots s
	WHERE s.schema_name <> '' AND $4 IS NULL
	  AND (COALESCE(cardinality($7::text[]), 0) = 0 OR 'schema' = ANY($7::text[]))
	GROUP BY s.connection_id, s.database_name, s.schema_name
	UNION ALL
	SELECT s.connection_id, s.database_name, s.schema_name, s.table_name, e.name, e.kind, e.kind_order,
	       e.ordinal, s.synced_at, e.comment, e.column_name, e.properties
	FROM snapshots s
	CROSS JOIN LATERAL (
		SELECT s.table_name::text AS name, CASE WHEN s.table_type = 'view' THEN 'view' ELSE 'table' END AS kind,
		       2 AS kind_order, 0::bigint AS ordinal, s.table_comment::text AS comment, ''::text AS column_name,
		       jsonb_build_object('column_count', jsonb_array_length(s.columns)) AS properties
		WHERE COALESCE(cardinality($7::text[]), 0) = 0
		   OR (CASE WHEN s.table_type = 'view' THEN 'view' ELSE 'table' END) = ANY($7::text[])
		UNION ALL
		SELECT x.value->>'name', 'field', 3, x.ordinality, COALESCE(x.value->>'comment', ''),
		       x.value->>'name', jsonb_strip_nulls(jsonb_build_object(
		         'data_type', x.value->'data_type', 'ordinal', x.value->'ordinal', 'is_nullable', x.value->'is_nullable'))
		FROM jsonb_array_elements(s.columns) WITH ORDINALITY AS x(value, ordinality)
		WHERE COALESCE(cardinality($7::text[]), 0) = 0 OR 'field' = ANY($7::text[])
		UNION ALL
		SELECT x.value->>'name', 'index', 4, x.ordinality, '', NULL,
		       jsonb_strip_nulls(jsonb_build_object('columns', x.value->'columns', 'is_unique', x.value->'is_unique'))
		FROM jsonb_array_elements(s.indexes) WITH ORDINALITY AS x(value, ordinality)
		WHERE COALESCE(cardinality($7::text[]), 0) = 0 OR 'index' = ANY($7::text[])
		UNION ALL
		SELECT x.value->>'name', 'constraint', 5, x.ordinality, '', NULL,
		       jsonb_strip_nulls(jsonb_build_object('kind', x.value->'kind', 'columns', x.value->'columns',
		         'ref_schema', x.value->'ref_schema', 'ref_table', x.value->'ref_table', 'ref_columns', x.value->'ref_columns'))
		FROM jsonb_array_elements(s.keys) WITH ORDINALITY AS x(value, ordinality)
		WHERE COALESCE(cardinality($7::text[]), 0) = 0 OR 'constraint' = ANY($7::text[])
	) e
	WHERE e.name <> ''
), filtered AS MATERIALIZED (
	SELECT e.connection_id, c.name AS connection_name, c.type AS connection_type, e.kind,
	       e.database, e.schema, e."table", e.name, e.properties, e.synced_at,
	       visible.comment, visible.level, visible.tags, COALESCE(u.username, '') AS owner_name,
	       e.kind_order, e.ordinal,
	       CASE WHEN $6::text = '' OR lower(e.name) = lower($6) THEN 0
	            WHEN strpos(lower(e.name), lower($6)) > 0 THEN 1 ELSE 2 END AS match_rank
	FROM elements e
	JOIN sources c ON c.id = e.connection_id
	LEFT JOIN sys_asset_annotations a
	  ON a.connection_id = e.connection_id AND a.database_name = e.database AND a.schema_name = e.schema
	  AND a.table_name = e."table" AND a.column_name = e.column_name
	LEFT JOIN sys_users u ON u.id = a.owner_user_id
	CROSS JOIN LATERAL (
		SELECT COALESCE(NULLIF(a.business_desc, ''), e.comment) AS comment,
		       COALESCE(a.sensitivity, 'unclassified') AS level,
		       COALESCE(a.tags, '{}'::text[]) AS tags
	) visible
	WHERE ($6::text = '' OR strpos(lower(e.name), lower($6)) > 0
	       OR strpos(lower(concat_ws('.', c.name, e.database, NULLIF(e.schema, ''), NULLIF(e."table", ''))), lower($6)) > 0
	       OR strpos(lower(visible.comment), lower($6)) > 0)
	  AND (COALESCE(cardinality($8::text[]), 0) = 0 OR visible.level = ANY($8::text[]))
	  AND (COALESCE(cardinality($9::text[]), 0) = 0 OR visible.tags && $9::text[])
)
SELECT COALESCE((SELECT jsonb_agg(to_jsonb(p) - 'kind_order' - 'ordinal' - 'match_rank') FROM (
	SELECT * FROM filtered
	ORDER BY match_rank, connection_name, connection_id, database, schema, "table", kind_order, ordinal, name
	OFFSET $10 LIMIT $11
) p), '[]'::jsonb),
(SELECT COUNT(*) FROM filtered),
COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY c.name, c.id) FROM sources c
          WHERE $3 = '' AND EXISTS (SELECT 1 FROM sys_meta_snapshots s WHERE s.connection_id = c.id)), '[]'::jsonb),
ARRAY(
	SELECT DISTINCT tag
	FROM snapshots s
	JOIN sys_asset_annotations a ON a.connection_id = s.connection_id AND a.database_name = s.database_name
	  AND a.schema_name = s.schema_name AND a.table_name = s.table_name
	CROSS JOIN LATERAL unnest(a.tags) tag
	WHERE $3 = '' AND (a.column_name = '' OR s.columns @> jsonb_build_array(jsonb_build_object('name', a.column_name)))
	ORDER BY tag
)
`
