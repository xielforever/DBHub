package target

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// rawQuery is reserved for parameterized, application-owned metadata queries.
func rawQuery(ctx context.Context, r *RelDB, query string, args ...any) ([]string, [][]any, error) {
	out := [][]any{}
	cols := []string{}
	if r.pgConn != nil {
		rows, e := r.pgConn.Query(ctx, query, args...)
		if e != nil {
			return nil, nil, e
		}
		defer rows.Close()
		for _, f := range rows.FieldDescriptions() {
			cols = append(cols, string(f.Name))
		}
		for rows.Next() {
			v, e := rows.Values()
			if e != nil {
				return nil, nil, e
			}
			out = append(out, v)
		}
		return cols, out, rows.Err()
	}
	rows, e := r.sqlConn.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, nil, e
	}
	defer rows.Close()
	cols, e = rows.Columns()
	if e != nil {
		return nil, nil, e
	}
	for rows.Next() {
		v := make([]any, len(cols))
		ptr := make([]any, len(v))
		for i := range v {
			ptr[i] = &v[i]
		}
		if e = rows.Scan(ptr...); e != nil {
			return nil, nil, e
		}
		for i, x := range v {
			if b, ok := x.([]byte); ok {
				v[i] = string(b)
			}
		}
		out = append(out, v)
	}
	return cols, out, rows.Err()
}
func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func integer(v any) int64 { n, _ := strconv.ParseInt(str(v), 10, 64); return n }
func boolean(v any) bool {
	return v == true || strings.EqualFold(str(v), "YES") || str(v) == "1" || str(v) == "true"
}
func stringsOf(v any) []string {
	out := []string{}
	switch a := v.(type) {
	case []string:
		return a
	case []any:
		for _, x := range a {
			out = append(out, str(x))
		}
	}
	return out
}

func Databases(ctx context.Context, r *RelDB) ([]DatabaseItem, error) {
	q := "SELECT datname FROM pg_database WHERE datallowconn AND NOT datistemplate ORDER BY datname"
	if r.Conn.Type == "mysql" {
		q = "SHOW DATABASES"
	}
	_, rows, e := rawQuery(ctx, r, q)
	if e != nil {
		return nil, e
	}
	out := []DatabaseItem{}
	for _, row := range rows {
		if IsUserDatabase(r.Conn.Type, str(row[0])) {
			out = append(out, DatabaseItem{Name: str(row[0])})
		}
	}
	return out, nil
}
func Schemas(ctx context.Context, r *RelDB) ([]string, error) {
	if r.Conn.Type == "mysql" {
		return []string{r.Conn.Database}, nil
	}
	_, rows, e := rawQuery(ctx, r, "SELECT nspname FROM pg_namespace WHERE nspname NOT LIKE 'pg_%' AND nspname <> 'information_schema' ORDER BY nspname")
	if e != nil {
		return nil, e
	}
	out := []string{}
	for _, row := range rows {
		out = append(out, str(row[0]))
	}
	return out, nil
}
func Tables(ctx context.Context, r *RelDB, database, schema string) ([]TableItem, error) {
	q := "SELECT c.relname, CASE WHEN c.relkind IN ('v','m') THEN 'view' ELSE 'table' END, COALESCE(obj_description(c.oid),'') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','p','v','m','f') ORDER BY c.relname"
	args := []any{firstNonEmpty(schema, "public")}
	if r.Conn.Type == "mysql" {
		q = "SELECT table_name,CASE WHEN table_type='VIEW' THEN 'view' ELSE 'table' END,COALESCE(table_comment,'') FROM information_schema.tables WHERE table_schema=? ORDER BY table_name"
		args = []any{firstNonEmpty(database, r.Conn.Database)}
	}
	_, rows, e := rawQuery(ctx, r, q, args...)
	if e != nil {
		return nil, e
	}
	out := []TableItem{}
	for _, row := range rows {
		out = append(out, TableItem{Name: str(row[0]), Type: str(row[1]), Comment: str(row[2])})
	}
	return out, nil
}
func Columns(ctx context.Context, r *RelDB, database, schema, table string) ([]ColumnItem, error) {
	cols, e := columnMetadata(ctx, r, database, schema, table)
	if e != nil {
		return nil, e
	}
	out := []ColumnItem{}
	for _, c := range cols {
		out = append(out, ColumnItem{Name: c.Name, DataType: c.DataType, IsPrimary: c.IsPrimary, IsNullable: c.IsNullable, Ordinal: c.Ordinal, Comment: c.Comment, Default: c.Default})
	}
	return out, nil
}
func columnMetadata(ctx context.Context, r *RelDB, database, schema, table string) ([]ColumnMeta, error) {
	q := `SELECT a.attname,format_type(a.atttypid,a.atttypmod),NOT a.attnotnull,a.attnum,
 EXISTS(SELECT 1 FROM pg_index i WHERE i.indrelid=c.oid AND i.indisprimary AND a.attnum=ANY(i.indkey)),
 COALESCE(pg_get_expr(d.adbin,d.adrelid),''),COALESCE(col_description(c.oid,a.attnum),'')
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid
 LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
 WHERE n.nspname=$1 AND c.relname=$2 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`
	args := []any{firstNonEmpty(schema, "public"), table}
	if r.Conn.Type == "mysql" {
		q = `SELECT column_name,column_type,is_nullable,ordinal_position,column_key='PRI',COALESCE(column_default,''),COALESCE(column_comment,'') FROM information_schema.columns WHERE table_schema=? AND table_name=? ORDER BY ordinal_position`
		args = []any{firstNonEmpty(database, r.Conn.Database), table}
	}
	_, rows, e := rawQuery(ctx, r, q, args...)
	if e != nil {
		return nil, e
	}
	out := []ColumnMeta{}
	for _, v := range rows {
		out = append(out, ColumnMeta{Name: str(v[0]), DataType: str(v[1]), IsNullable: boolean(v[2]), Ordinal: int(integer(v[3])), IsPrimary: boolean(v[4]), Default: str(v[5]), Comment: str(v[6])})
	}
	return out, nil
}

func IntrospectSchema(ctx context.Context, r *RelDB, database, schema string) ([]TableMeta, error) {
	tables, e := Tables(ctx, r, database, schema)
	if e != nil {
		return nil, e
	}
	out := []TableMeta{}
	for _, t := range tables {
		m := TableMeta{Name: t.Name, Schema: firstNonEmpty(schema, "public"), Type: t.Type, Comment: t.Comment, Indexes: []IndexMeta{}, Keys: []KeyMeta{}}
		if r.Conn.Type == "mysql" {
			m.Schema = firstNonEmpty(database, r.Conn.Database)
		}
		m.Columns, e = columnMetadata(ctx, r, database, schema, t.Name)
		if e != nil {
			return nil, e
		}
		if r.Conn.Type == "postgres" {
			e = pgDetails(ctx, r, &m)
		} else {
			e = mysqlDetails(ctx, r, &m)
		}
		if e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, nil
}

// DescribeTable reads exactly one table without scanning every table in a schema.
func DescribeTable(ctx context.Context, r *RelDB, database, schema, name, kind string) (TableMeta, error) {
	m := TableMeta{Name: name, Schema: firstNonEmpty(schema, "public"), Type: kind, Indexes: []IndexMeta{}, Keys: []KeyMeta{}}
	if r.Conn.Type == "mysql" {
		m.Schema = firstNonEmpty(database, r.Conn.Database)
	}
	var err error
	m.Columns, err = columnMetadata(ctx, r, database, schema, name)
	if err != nil {
		return m, err
	}
	if r.Conn.Type == "postgres" {
		err = pgDetails(ctx, r, &m)
	} else {
		err = mysqlDetails(ctx, r, &m)
	}
	return m, err
}

func pgDetails(ctx context.Context, r *RelDB, m *TableMeta) error {
	_, rows, e := rawQuery(ctx, r, `SELECT GREATEST(c.reltuples,0)::bigint,pg_total_relation_size(c.oid) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2`, m.Schema, m.Name)
	if e != nil {
		return e
	}
	if len(rows) > 0 {
		m.EstimatedRows = integer(rows[0][0])
		m.DataBytes = integer(rows[0][1])
	}
	_, rows, e = rawQuery(ctx, r, `SELECT ci.relname,i.indisunique,i.indisprimary,ARRAY(SELECT a.attname FROM unnest(i.indkey) WITH ORDINALITY k(num,ord) JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.num ORDER BY k.ord) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_index i ON i.indrelid=c.oid JOIN pg_class ci ON ci.oid=i.indexrelid WHERE n.nspname=$1 AND c.relname=$2 ORDER BY ci.relname`, m.Schema, m.Name)
	if e != nil {
		return e
	}
	for _, v := range rows {
		m.Indexes = append(m.Indexes, IndexMeta{Name: str(v[0]), IsUnique: boolean(v[1]), IsPrimary: boolean(v[2]), Columns: stringsOf(v[3])})
	}
	_, rows, e = rawQuery(ctx, r, `SELECT k.conname,k.contype::text,ARRAY(SELECT a.attname FROM unnest(k.conkey) WITH ORDINALITY x(num,ord) JOIN pg_attribute a ON a.attrelid=k.conrelid AND a.attnum=x.num ORDER BY x.ord),COALESCE(rn.nspname,''),COALESCE(rc.relname,''),ARRAY(SELECT a.attname FROM unnest(k.confkey) WITH ORDINALITY x(num,ord) JOIN pg_attribute a ON a.attrelid=k.confrelid AND a.attnum=x.num ORDER BY x.ord),pg_get_constraintdef(k.oid) FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_class rc ON rc.oid=k.confrelid LEFT JOIN pg_namespace rn ON rn.oid=rc.relnamespace WHERE n.nspname=$1 AND c.relname=$2 ORDER BY k.conname`, m.Schema, m.Name)
	if e != nil {
		return e
	}
	definitions := []string{}
	for _, c := range m.Columns {
		line := quote(r.Conn.Type, c.Name) + " " + c.DataType
		if c.Default != "" {
			line += " DEFAULT " + c.Default
		}
		if !c.IsNullable {
			line += " NOT NULL"
		}
		definitions = append(definitions, line)
	}
	for _, v := range rows {
		kind := map[string]string{"p": "primary_key", "u": "unique", "f": "foreign_key"}[str(v[1])]
		if kind != "" {
			m.Keys = append(m.Keys, KeyMeta{Name: str(v[0]), Kind: kind, Columns: stringsOf(v[2]), RefSchema: str(v[3]), RefTable: str(v[4]), RefColumns: stringsOf(v[5])})
		}
		definitions = append(definitions, "CONSTRAINT "+quote(r.Conn.Type, str(v[0]))+" "+str(v[6]))
	}
	if m.Type == "view" {
		_, v, e := rawQuery(ctx, r, "SELECT pg_get_viewdef(c.oid,true) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2", m.Schema, m.Name)
		if e != nil {
			return e
		}
		if len(v) > 0 {
			m.DDL = "CREATE VIEW " + qualified(r, m.Schema, m.Name) + " AS " + str(v[0][0])
		}
	} else {
		m.DDL = "CREATE TABLE " + qualified(r, m.Schema, m.Name) + " (\n  " + strings.Join(definitions, ",\n  ") + "\n);"
	}
	return nil
}

func mysqlDetails(ctx context.Context, r *RelDB, m *TableMeta) error {
	_, rows, e := rawQuery(ctx, r, "SELECT COALESCE(table_rows,0),COALESCE(data_length,0)+COALESCE(index_length,0) FROM information_schema.tables WHERE table_schema=? AND table_name=?", m.Schema, m.Name)
	if e != nil {
		return e
	}
	if len(rows) > 0 {
		m.EstimatedRows = integer(rows[0][0])
		m.DataBytes = integer(rows[0][1])
	}
	_, rows, e = rawQuery(ctx, r, "SELECT index_name,column_name,non_unique FROM information_schema.statistics WHERE table_schema=? AND table_name=? ORDER BY index_name,seq_in_index", m.Schema, m.Name)
	if e != nil {
		return e
	}
	indexes := map[string]int{}
	for _, v := range rows {
		name := str(v[0])
		pos, ok := indexes[name]
		if !ok {
			pos = len(m.Indexes)
			indexes[name] = pos
			m.Indexes = append(m.Indexes, IndexMeta{Name: name, Columns: []string{}, IsUnique: integer(v[2]) == 0, IsPrimary: name == "PRIMARY"})
		}
		if v[1] != nil {
			m.Indexes[pos].Columns = append(m.Indexes[pos].Columns, str(v[1]))
		}
	}
	_, rows, e = rawQuery(ctx, r, `SELECT k.constraint_name,t.constraint_type,k.column_name,COALESCE(k.referenced_table_schema,''),COALESCE(k.referenced_table_name,''),COALESCE(k.referenced_column_name,'') FROM information_schema.key_column_usage k JOIN information_schema.table_constraints t ON t.constraint_schema=k.constraint_schema AND t.table_name=k.table_name AND t.constraint_name=k.constraint_name WHERE k.table_schema=? AND k.table_name=? ORDER BY k.constraint_name,k.ordinal_position`, m.Schema, m.Name)
	if e != nil {
		return e
	}
	keys := map[string]int{}
	for _, v := range rows {
		name := str(v[0])
		pos, ok := keys[name]
		if !ok {
			pos = len(m.Keys)
			keys[name] = pos
			kind := map[string]string{"PRIMARY KEY": "primary_key", "UNIQUE": "unique", "FOREIGN KEY": "foreign_key"}[str(v[1])]
			m.Keys = append(m.Keys, KeyMeta{Name: name, Kind: kind, Columns: []string{}, RefSchema: str(v[3]), RefTable: str(v[4]), RefColumns: []string{}})
		}
		m.Keys[pos].Columns = append(m.Keys[pos].Columns, str(v[2]))
		if str(v[5]) != "" {
			m.Keys[pos].RefColumns = append(m.Keys[pos].RefColumns, str(v[5]))
		}
	}
	_, rows, e = rawQuery(ctx, r, "SHOW CREATE TABLE "+qualified(r, m.Schema, m.Name))
	if e != nil {
		return e
	}
	if len(rows) > 0 && len(rows[0]) > 1 {
		m.DDL = str(rows[0][1])
	}
	return nil
}

func quote(dialect, name string) string {
	if dialect == "mysql" {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return pgx.Identifier{name}.Sanitize()
}
func qualified(r *RelDB, schema, table string) string {
	return quote(r.Conn.Type, firstNonEmpty(schema, firstNonEmpty(r.Conn.Database, "public"))) + "." + quote(r.Conn.Type, table)
}
func PreviewTable(ctx context.Context, r *RelDB, database, schema, table string, limit, offset int) ([]string, [][]any, int64, error) {
	if table == "" {
		return nil, nil, 0, fmt.Errorf("表名不能为空")
	}
	if limit < 1 || limit > 10000 {
		limit = 100
	}
	if offset < 0 {
		return nil, nil, 0, fmt.Errorf("分页参数无效")
	}
	if r.Conn.Type == "mysql" {
		schema = firstNonEmpty(database, r.Conn.Database)
	} else {
		schema = firstNonEmpty(schema, "public")
	}
	tableSQL := qualified(r, schema, table)
	_, count, e := rawQuery(ctx, r, "SELECT COUNT(*) FROM "+tableSQL)
	if e != nil {
		return nil, nil, 0, e
	}
	total := int64(0)
	if len(count) > 0 {
		total = integer(count[0][0])
	}
	pagination := " LIMIT $1 OFFSET $2"
	if r.Conn.Type == "mysql" {
		pagination = " LIMIT ? OFFSET ?"
	}
	cols, rows, e := rawQuery(ctx, r, "SELECT * FROM "+tableSQL+pagination, limit, offset)
	return cols, rows, total, e
}
