/**
 * 用户/角色（RBAC）与审计日志 API（均需 admin 角色）
 */
import request from '../utils/request'

export interface Role {
  id: number
  code: string
  name: string
  description?: string
}

export interface UserItem {
  id: number
  username: string
  email: string
  role_id: number
  role: 'admin' | 'developer' | 'readonly'
  role_name: string
  is_active: boolean
  last_login_at?: string
  created_at: string
}

export interface UserGroup {id: number; name: string; description: string; version: number; member_ids: number[]; updated_at: string}
export interface AuditLog {
 context: {actor_id?: number; actor_username?: string; actor_role?: string; connection_id?: number; source_family_id?: string; definition_revision_id?: number; revision?: number; database_account?: string; driver?: string; target_opened?: boolean; execution_request_id?: string; transaction_id?: string; job_id?: string; export_id?: number; http_status?: number; response_failed?: boolean; auth_failure?: string; route?: string; outcome?: string}
  event_id?: string
  id: number
  user_id: number | null
  connection_id: number | null
  trace_id: string
  action: string
  resource_type: string
  resource_name: string
  ip_address: string
  user_agent: string
  status: number
  error_message?: string
  duration_ms?: number
  params?: Record<string, unknown>
  created_at: string
  username: string
}

export interface AuditDelivery {ready: boolean; pending: number; capacity: number; oldest?: string; last_delivered?: string; delivery_failures: number; storage_failures: number; last_error?: string}

interface Paged<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export const adminApi = {
 auditDelivery: () => request.get<unknown, AuditDelivery>('/audit-logs/delivery'),
 groups: (params: {keyword?: string; page: number; page_size: number}) => request.get<unknown, Paged<UserGroup>>('/user-groups', {params}),
 saveGroup: (id: number, payload: {name: string; description: string; member_ids: number[]; expected_version: number}) => {
  const body = {name: payload.name, description: payload.description, member_ids: payload.member_ids, expected_version: payload.expected_version}
  return id ? request.put<unknown, UserGroup>(`/user-groups/${id}`, body) : request.post<unknown, UserGroup>('/user-groups', body)
 },
 deleteGroup: (id: number, expectedVersion: number) => request.delete(`/user-groups/${id}`, {data: {expected_version: expectedVersion}}),
  roles() {
    return request.get<unknown, { items: Role[] }>('/roles')
  },
  users(params: { keyword?: string; page: number; page_size: number }) {
    return request.get<unknown, Paged<UserItem>>('/users', { params })
  },
  createUser(payload: { username: string; password: string; role: string; email?: string }) {
    return request.post<unknown, UserItem>('/users', payload)
  },
  updateUser(id: number, payload: { role: string; email?: string }) {
    return request.put<unknown, { id: number }>(`/users/${id}`, payload)
  },
  setUserStatus(id: number, isActive: boolean) {
    return request.patch<unknown, { id: number; is_active: boolean }>(`/users/${id}/status`, {
      is_active: isActive,
    })
  },
  resetPassword(id: number, newPassword: string) {
    return request.post<unknown, { id: number }>(`/users/${id}/reset-password`, {
      new_password: newPassword,
    })
  },
  deleteUser(id: number) {
    return request.delete<unknown, { id: number }>(`/users/${id}`)
  },
  changePassword(oldPassword: string, newPassword: string) {
    return request.post<unknown, { ok: boolean }>('/auth/change-password', {
      old_password: oldPassword,
      new_password: newPassword,
    })
  },
  auditLogs(params: {
    database_account?: string
    outcome?: string
    username?: string
    action?: string
    resource_type?: string
    status?: string
    days?: number
    page: number
    page_size: number
  }) {
    return request.get<unknown, Paged<AuditLog>>('/audit-logs', { params })
  },
}
