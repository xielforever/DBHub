/**
 * 数据源（目标数据库连接）与 SSH 隧道 API
 */
import request from '../utils/request'

export type DbType = 'mysql' | 'postgres' | 'redis'
export type EnvKind = 'dev' | 'test' | 'prod'

export interface ConnectionItem {
  id: number
  user_id: number
  ssh_tunnel_id: number | null
  proxy_id: number | null
  name: string
  type: DbType
  host: string
  port: number
  database: string
  username: string
  ssl_mode: string
  connection_timeout: number
  color_label: string
  environment: EnvKind
  has_password: boolean
  tunnel_name?: string
  proxy_name?: string
  created_at: string
  updated_at: string
}

export interface ConnectionPayload {
  name: string
  type: DbType
  host: string
  port: number
  database?: string
  username?: string
  /** 编辑时留空表示不修改已保存口令 */
  password?: string
  ssh_tunnel_id?: number | null
  proxy_id?: number | null
  ssl_mode: string
  connection_timeout: number
  color_label?: string
  environment?: EnvKind
}

export interface SSHTunnelItem {
  id: number
  user_id: number
  name: string
  host: string
  port: number
  username: string
  auth_type: 'password' | 'private_key'
  has_private_key: boolean
  has_passphrase: boolean
  has_password: boolean
  created_at: string
  updated_at: string
}

export interface SSHTunnelPayload {
  name: string
  host: string
  port: number
  username: string
  auth_type: 'password' | 'private_key'
  private_key?: string
  passphrase?: string
  password?: string
}

export interface TestResult {
  ok: boolean
  type: string
  version: string
  elapsed_ms: number
}

interface ListWrap<T> {
  items: T[]
  total: number
}

export const connectionApi = {
  list(params?: { type?: string; keyword?: string }) {
    return request.get<unknown, ListWrap<ConnectionItem>>('/connections', { params })
  },
  create(payload: ConnectionPayload) {
    return request.post<unknown, ConnectionItem>('/connections', payload)
  },
  update(id: number, payload: ConnectionPayload) {
    return request.put<unknown, ConnectionItem>(`/connections/${id}`, payload)
  },
  remove(id: number) {
    return request.delete<unknown, { id: number }>(`/connections/${id}`)
  },
  testSaved(id: number) {
    return request.post<unknown, TestResult>(`/connections/${id}/test`)
  },
  test(payload: ConnectionPayload) {
    return request.post<unknown, TestResult>('/connections/test', payload)
  },
}

export const tunnelApi = {
  list() {
    return request.get<unknown, ListWrap<SSHTunnelItem>>('/ssh-tunnels')
  },
  create(payload: SSHTunnelPayload) {
    return request.post<unknown, SSHTunnelItem>('/ssh-tunnels', payload)
  },
  update(id: number, payload: SSHTunnelPayload) {
    return request.put<unknown, SSHTunnelItem>(`/ssh-tunnels/${id}`, payload)
  },
  remove(id: number) {
    return request.delete<unknown, { id: number }>(`/ssh-tunnels/${id}`)
  },
  test(payload: SSHTunnelPayload) {
    return request.post<unknown, { ok: boolean }>('/ssh-tunnels/test', payload)
  },
}
