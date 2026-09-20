/**
 * SQL 工作台 API：执行、元数据浏览、表数据预览、查询历史
 */
import request from '../utils/request'
import type { DbType } from './datasource'

export interface ExecResult {
  kind: 'query' | 'write'
  columns?: string[]
  rows?: unknown[][]
  affected_rows?: number
  truncated?: boolean
  duration_ms: number
}

export interface TableInfo {
  schema: string
  name: string
  type: 'table' | 'view' | string
}

export interface ColumnInfo {
  name: string
  data_type: string
  is_nullable: boolean
  is_primary: boolean
  default?: string
  ordinal: number
}

export interface PreviewResult {
  columns: string[]
  rows: unknown[][]
  total: number
  page: number
  page_size: number
  has_more: boolean
}

export interface QueryHistoryItem {
  id: number
  connection_id: number
  user_id: number
  database_name: string
  sql_text: string
  status: number
  affected_rows?: number
  execution_time_ms?: number
  row_count?: number
  error_message?: string
  created_at: string
  connection_name?: string
}

export interface RedisOverview {
  version: string
  mode: string
  os: string
  uptime_days: number
  connected_clients: number
  used_memory_mb: number
  total_commands: number
  keyspaces: { db: string; keys: number; expires: number }[]
}

export interface RedisKey {
  key: string
  type: string
  ttl: number
}

export interface RedisValue {
  key?: string
  type: string
  ttl: number
  size: number
  value: unknown
}

export const workbenchApi = {
  execute(connectionId: number, sql: string, database?: string, signal?: AbortSignal) {
    return request.post<unknown, ExecResult>('/query/execute', {
      connection_id: connectionId,
      sql,
      database: database || '',
    }, {
      signal,
    } as any)
  },
  databases(connectionId: number) {
    return request.get<unknown, { items: { name: string }[] }>('/metadata/databases', {
      params: { connection_id: connectionId },
    })
  },
  schemas(connectionId: number, database?: string) {
    return request.get<unknown, { items: string[] }>('/metadata/schemas', {
      params: { connection_id: connectionId, database },
    })
  },
  tables(connectionId: number, params: { database?: string; schema?: string }) {
    return request.get<unknown, { items: TableInfo[] }>('/metadata/tables', {
      params: { connection_id: connectionId, ...params },
    })
  },
  columns(connectionId: number, params: { database?: string; schema?: string; table: string }) {
    return request.get<unknown, { items: ColumnInfo[] }>('/metadata/columns', {
      params: { connection_id: connectionId, ...params },
    })
  },
  preview(
    connectionId: number,
    params: { database?: string; schema?: string; table: string; page: number; page_size: number },
  ) {
    return request.get<unknown, PreviewResult>('/data/preview', {
      params: { connection_id: connectionId, ...params },
    })
  },
  history(params: { connection_id?: number; status?: string; keyword?: string; q?: string; page: number; page_size: number }) {
    return request.get<unknown, {
      items: QueryHistoryItem[]
      total: number
      page: number
      page_size: number
    }>('/query/history', { params })
  },
  deleteHistory(id: number) {
    return request.delete<unknown, { id: number }>(`/query/history/${id}`)
  },
  clearHistory() {
    return request.delete<unknown, { ok: boolean }>('/query/history')
  },
  redisOverview(connectionId: number) {
    return request.get<unknown, RedisOverview>('/redis/overview', {
      params: { connection_id: connectionId },
    })
  },
  redisKeys(connectionId: number, pattern = '*', limit = 200, type = 'all') {
    return request.get<unknown, { items: RedisKey[]; returned: number; total: number }>('/redis/keys', {
      params: { connection_id: connectionId, pattern, limit, type },
    })
  },
  redisValue(connectionId: number, key: string) {
    return request.get<unknown, RedisValue>('/redis/value', {
      params: { connection_id: connectionId, key },
    })
  },
  redisDeleteKey(connectionId: number, key: string) {
    return request.delete<unknown, { key: string; deleted: boolean }>('/redis/key', {
      data: { connection_id: connectionId, key },
      params: { connection_id: connectionId, key },
    } as any)
  },
  redisUpdateTTL(connectionId: number, key: string, ttl: number) {
    return request.put<unknown, { key: string; ttl: number }>('/redis/key/ttl', {
      connection_id: connectionId,
      key,
      ttl,
    })
  },
}

export type { DbType }
