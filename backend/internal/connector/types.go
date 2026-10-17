// Package connector defines driver-independent target contracts.
package connector

type Compatibility struct {
	Engine        string `json:"engine"`
	Product       string `json:"product"`
	Version       string `json:"version"`
	Branch        string `json:"branch"`
	Status        string `json:"status"`
	TestedVersion string `json:"tested_version,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Message       string `json:"message"`
}

type DatabaseItem struct {
	Name string `json:"name"`
}

type TableItem struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // table | view
	Comment string `json:"comment"`
}

type ColumnItem struct {
	Name       string `json:"name"`
	DataType   string `json:"data_type"`
	IsPrimary  bool   `json:"is_primary"`
	IsNullable bool   `json:"is_nullable"`
	Ordinal    int    `json:"ordinal"`
	Comment    string `json:"comment"`
	Default    string `json:"default"`
}

type ColumnMeta struct {
	Name       string `json:"name"`
	DataType   string `json:"data_type"`
	IsPrimary  bool   `json:"is_primary"`
	IsNullable bool   `json:"is_nullable"`
	Ordinal    int    `json:"ordinal"`
	Comment    string `json:"comment"`
	Default    string `json:"default"`
}

type IndexMeta struct {
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	IsUnique  bool     `json:"is_unique"`
	IsPrimary bool     `json:"is_primary"`
}

type KeyMeta struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"` // foreign_key | primary_key
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"ref_schema"`
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns"`
}

type TableMeta struct {
	Name           string            `json:"name"`
	Schema         string            `json:"schema"`
	Type           string            `json:"type"`
	Comment        string            `json:"comment"`
	EstimatedRows  int64             `json:"estimated_rows"`
	DataBytes      int64             `json:"data_bytes"`
	Columns        []ColumnMeta      `json:"columns"`
	Indexes        []IndexMeta       `json:"indexes"`
	Keys           []KeyMeta         `json:"keys"`
	DDL            string            `json:"ddl"`
	MetadataStatus map[string]string `json:"metadata_status,omitempty"`
}

type ExecResult struct {
	Kind         string           `json:"kind"` // query | write
	Columns      []string         `json:"columns"`
	Rows         [][]any          `json:"rows"`
	AffectedRows int64            `json:"affected_rows"`
	DurationMS   int64            `json:"duration_ms"`
	Truncated    bool             `json:"truncated"`
	ColumnTypes  []map[string]any `json:"column_types,omitempty"`
}

type TestInfo struct {
	Type          string        `json:"type"`
	Version       string        `json:"version"`
	Compatibility Compatibility `json:"compatibility"`
}

type RedisInfo struct {
	Version          string `json:"version"`
	Mode             string `json:"mode"`
	OS               string `json:"os"`
	UptimeDays       int    `json:"uptime_days"`
	ConnectedClients int    `json:"connected_clients"`
	UsedMemoryMB     int    `json:"used_memory_mb"`
	UsedMemoryHuman  string `json:"used_memory_human"`
	TotalCommands    int64  `json:"total_commands"`
	TotalKeys        int64  `json:"total_keys"`
	Keyspaces        []any  `json:"keyspaces"`
}

type RedisKeyItem struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	TTL  int64  `json:"ttl"`
}

type RedisValue struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	TTL   int64  `json:"ttl"`
	Size  int64  `json:"size"`
	Value any    `json:"value"`
}
