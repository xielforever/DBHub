/**
 * 数据资产模块（M1）：字典同步、资产目录、表详情、人工标注、收藏与全局搜索
 */
import request from '../utils/request'

export type EnvKind = 'dev' | 'test' | 'prod'
export type Sensitivity = 'normal' | 'sensitive' | 'confidential'

/** 列结构快照 */
export interface MetaColumn {
  name: string
  ordinal: number
  data_type: string
  is_nullable: boolean
  is_primary: boolean
  default?: string
  comment?: string
}

/** 索引快照 */
export interface MetaIndex {
  name: string
  columns: string[]
  is_unique: boolean
  is_primary: boolean
}

/** 主键 / 外键 / 唯一键 */
export interface MetaKey {
  name: string
  kind: 'primary_key' | 'foreign_key' | 'unique'
  columns: string[]
  ref_schema?: string
  ref_table?: string
  ref_columns?: string[]
}

/** 元数据采集快照 */
export interface MetaSnapshot {
  id: number
  connection_id: number
  database_name: string
  schema_name: string
  table_name: string
  table_type: string
  table_comment: string
  estimated_rows: number
  data_bytes: number
  raw_columns: MetaColumn[]
  raw_indexes: MetaIndex[]
  raw_keys: MetaKey[]
  ddl_text: string
  synced_at: string
}

/** 人工标注（column_name 为空串表示表级） */
export interface AssetAnnotation {
  id: number
  connection_id: number
  database_name: string
  schema_name: string
  table_name: string
  column_name: string
  owner_user_id: number | null
  business_desc: string
  tags: string[]
  sensitivity: Sensitivity
  starred_by: number[]
  updated_by: number | null
  updated_at: string
}

/** 资产列表行：快照 + 表级标注 + 热度 */
export interface TableAssetRow {
  snapshot: MetaSnapshot
  connection_name: string
  owner_user_id: number | null
  owner_name: string
  business_desc: string
  tags: string[]
  sensitivity: Sensitivity
  starred: boolean
  query_count_30d: number
  sensitive_columns: number
}

export interface OverviewCounts {
  connection_total: number
  table_total: number
  view_total: number
  owned_tables: number
  sensitive_fields: number
}

// ---- 资产树 ----
export interface TreeTable {
  name: string
  type: string
}
export interface TreeSchema {
  name: string
  tables: TreeTable[]
}
export interface TreeDB {
  name: string
  schemas: TreeSchema[]
}
export interface TreeConnection {
  id: number
  name: string
  type: string
  environment: EnvKind
  databases: TreeDB[]
}

// ---- 同步 ----
export interface DbSyncStat {
  database: string
  schemas: number
  tables: number
  views: number
}
export interface SyncResult {
  connection_id: number
  connection_name: string
  type: string
  skipped?: boolean
  databases: string[]
  tables: number
  views: number
  errors?: string[]
  skipped_databases?: string[]
  by_database: DbSyncStat[]
}
export interface SyncSummary {
  connections: SyncResult[]
  tables: number
  views: number
}

// ---- 详情 ----
export interface ColumnAnnotation {
  column_name: string
  owner_user_id: number | null
  business_desc: string
  tags: string[]
  sensitivity: Sensitivity
}
export interface TableDetail {
  snapshot: MetaSnapshot
  connection_name: string
  environment: EnvKind
  table_annotation: AssetAnnotation | null
  column_annotations: ColumnAnnotation[]
  starred: boolean
  query_count_30d: number
}

// ---- 搜索 ----
export interface SearchConnectionHit {
  id: number
  name: string
  type: string
  environment: EnvKind
}
export interface SearchTableHit {
  connection_id: number
  connection_name: string
  environment: EnvKind
  database: string
  schema: string
  name: string
  type: string
  comment: string
}
export interface SearchColumnHit {
  connection_id: number
  connection_name: string
  database: string
  schema: string
  table: string
  column: string
  data_type: string
  comment: string
}
export interface SearchHistoryHit {
  id: number
  connection_id: number
  connection_name: string
  database_name: string
  sql_text: string
  created_at: string
}
export interface SearchGroups {
  connections: SearchConnectionHit[]
  tables: SearchTableHit[]
  columns: SearchColumnHit[]
  reports: unknown[]
  history: SearchHistoryHit[]
}

export interface BriefUser {
  id: number
  username: string
  role: string
}

export interface TableListParams {
  connection_id?: number
  database?: string
  schema?: string
  q?: string
  type?: string
  sensitivity?: string
  no_owner?: 1 | 0
  starred?: 1 | 0
  page?: number
  page_size?: number
}

export interface AnnotationPayload {
  connection_id: number
  database: string
  schema: string
  table: string
  column?: string
  owner_user_id?: number | null
  business_desc?: string
  tags?: string[]
  sensitivity?: Sensitivity
}

export const assetApi = {
  /** 同步字典；body 为空同步全部关系型数据源，指定 connection_id 同步单源 */
  sync(connectionId?: number) {
    return request.post<unknown, SyncSummary>(
      '/assets/sync',
      connectionId ? { connection_id: connectionId } : {},
      { timeout: 250_000 },
    )
  },
  overview() {
    return request.get<unknown, OverviewCounts>('/assets/overview')
  },
  tree(connectionId?: number) {
    return request.get<unknown, { items: TreeConnection[] }>('/assets/tree', {
      params: connectionId ? { connection_id: connectionId } : {},
    })
  },
  tables(params: TableListParams) {
    return request.get<unknown, { items: TableAssetRow[]; total: number; page: number; page_size: number }>(
      '/assets/tables',
      { params },
    )
  },
  table(loc: { connection_id: number; database: string; schema: string; table: string }) {
    return request.get<unknown, TableDetail>('/assets/table', { params: loc })
  },
  putAnnotation(payload: AnnotationPayload) {
    return request.put<unknown, { ok: boolean }>('/assets/annotations', payload)
  },
  star(loc: { connection_id: number; database: string; schema: string; table: string }, star: boolean) {
    return request.post<unknown, { starred: boolean }>('/assets/star', { ...loc, star })
  },
  search(q: string) {
    return request.get<unknown, SearchGroups>('/assets/search', { params: { q } })
  },
  briefUsers() {
    return request.get<unknown, { items: BriefUser[] }>('/users/brief')
  },
}
