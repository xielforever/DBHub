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

export interface AuditLog {
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

interface Paged<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export const adminApi = {
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
