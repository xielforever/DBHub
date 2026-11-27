import request from '../utils/request'
import type { ApiResponse } from '../types/api'
import type { Baseline, MenuConfiguration, Role } from '../types/iam'

export type PlatformAccountStatus = 'pending' | 'active' | 'inactive'
export type AccountCredentialPurpose = 'activate' | 'reset'

export interface PlatformAccount {
  id: number
  username: string
  email: string
  status: PlatformAccountStatus
  is_platform_admin: boolean
  local_login: boolean
  sso_login: boolean
  mfa_enabled: boolean
  last_login_at: string | null
  created_at: string
  version: number
}

export interface AccountCredential {
  token: string
  purpose: AccountCredentialPurpose
  expires_at: string
}

interface CredentialReceipt {
  account: PlatformAccount
  credential: AccountCredential
}

export interface PlatformWorkspace {
  id: number
  name: string
  enabled: boolean
  created_at: string
  founding_admin?: { id: number; username: string }
}

export interface PlatformRoleTemplate {
  id: string
  code: string
  name: string
  actions: string[]
}

interface WorkspaceMenuUpdate {
  baseline: Baseline
  configuration: MenuConfiguration
  request_id: string
}

export interface PlatformDirectoryQuery {
  keyword: string
  page: number
}

export interface PlatformAccountQuery extends PlatformDirectoryQuery {
  status: PlatformAccountStatus | ''
  login_method: 'local' | 'sso' | ''
}

export interface PlatformDirectoryPage<T> {
  items: T[]
  total: number
}

interface VersionedReason {
  expected_version: number
  reason: string
}

export const platformDirectoryPageSize = 15

export const platformManagementApi = {
  roleTemplates(signal: AbortSignal) {
    return request.get<unknown, { items: PlatformRoleTemplate[] }>('/platform/role-templates', { signal, silentError: true })
  },
  workspaceRoles(id: number, signal: AbortSignal) {
    return request.get<unknown, Role[]>(`/workspaces/${id}/iam/roles`, { signal, silentError: true, platformWorkspaceMenu: true })
  },
  saveWorkspaceMenus(id: number, input: WorkspaceMenuUpdate, signal: AbortSignal) {
    return request.put<unknown, { version: number }>(`/workspaces/${id}/iam/menus`, input,
      { signal, silentError: true, platformWorkspaceMenu: true })
  },
  accounts(query: PlatformAccountQuery, signal: AbortSignal) {
    return request.get<unknown, PlatformDirectoryPage<PlatformAccount>>('/platform/accounts', {
      params: { ...query, page_size: platformDirectoryPageSize },
      signal,
      silentError: true,
    })
  },
  createAccount(payload: { username: string; email: string; reason: string }, signal: AbortSignal) {
    return request.post<unknown, CredentialReceipt>('/platform/accounts', payload, { signal, silentError: true })
  },
  issueCredential(id: number, payload: VersionedReason & { purpose: AccountCredentialPurpose }, signal: AbortSignal) {
    return request.post<unknown, CredentialReceipt>(`/platform/accounts/${id}/credential`, payload, {
      signal,
      silentError: true,
    })
  },
  changeStatus(id: number, payload: VersionedReason & { active: boolean }, signal: AbortSignal) {
    return request.patch<unknown, PlatformAccount>(`/platform/accounts/${id}/status`, payload, {
      signal,
      silentError: true,
    })
  },
  revokeSessions(id: number, payload: VersionedReason, signal: AbortSignal) {
    return request.post<unknown, PlatformAccount>(`/platform/accounts/${id}/revoke-sessions`, payload, {
      signal,
      silentError: true,
    })
  },
  workspaces(query: PlatformDirectoryQuery, signal: AbortSignal) {
    return request.get<unknown, PlatformDirectoryPage<PlatformWorkspace>>('/platform/workspaces', {
      params: { ...query, page_size: platformDirectoryPageSize },
      signal,
      silentError: true,
    })
  },
  createWorkspace(name: string, requestId: string, signal: AbortSignal) {
    return request.post<unknown, Pick<PlatformWorkspace, 'id' | 'name'>>('/workspaces', {
      name,
      request_id: requestId,
      permission_matrix: true,
    }, { signal, silentError: true })
  },
}

export async function redeemAccountCredential(token: string, password: string, signal: AbortSignal) {
  const response = await fetch('/api/v1/auth/account-credential', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'omit',
    cache: 'no-store',
    body: JSON.stringify({ token, password }),
    signal,
  })
  const result: ApiResponse<{ purpose?: AccountCredentialPurpose }> | undefined =
    await response.json().catch(() => undefined)
  const purpose = result?.data?.purpose
  if (response.ok && result?.code === 0 && (purpose === 'activate' || purpose === 'reset')) return purpose
  if (response.status === 400 || response.status === 401 || response.status === 409) {
    throw new Error('无法使用这份凭证。请核对是否过期或已使用，必要时联系超级管理员重新签发。')
  }
  if (response.status === 429) throw new Error('提交过于频繁，请稍后重试。')
  throw new Error('暂时无法设置密码，请稍后重试。')
}
