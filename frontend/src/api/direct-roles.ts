import request from '../utils/request'

export interface DirectRole {
  code: string
  name: string
  actions: string[]
}

export interface DirectRoleMember {
  user_id: number
  username: string
  member_role: string
  version: number
  role_codes: string[]
  active?: boolean
}

export interface DirectRoleDirectory {
  workspace_id: number
  workspace_name: string
  enabled: boolean
  can_manage_members: boolean
  can_manage_admins: boolean
  roles: DirectRole[]
  members: DirectRoleMember[]
}

export interface WorkspaceMemberCandidate {
  user_id: number
  username: string
}

export type WorkspaceMembershipOperation = 'remove' | 'appoint_admin' | 'revoke_admin'

export function canAssignWorkspaceBusinessRoles(directory: DirectRoleDirectory | undefined, actorId: number | undefined, member: DirectRoleMember | undefined) {
  return !!directory?.can_manage_members && !!actorId && !!member && member.active !== false &&
    (directory.can_manage_admins || (member.member_role !== 'admin' && member.user_id !== actorId))
}

export const directRoleApi = {
  list(workspaceId: number, signal?: AbortSignal) {
    return request.get<unknown, DirectRoleDirectory>(`/role-assignments/workspaces/${workspaceId}/members`, { signal, silentError: true })
  },
  save(workspaceId: number, memberId: number, payload: { request_id: string; expected_version: number; role_codes: string[] }) {
    return request.put<unknown, DirectRoleMember>(`/role-assignments/workspaces/${workspaceId}/members/${memberId}`, payload, { silentError: true })
  },
  candidates(workspaceId: number, keyword: string, signal: AbortSignal) {
    return request.get<unknown, WorkspaceMemberCandidate[]>(`/role-assignments/workspaces/${workspaceId}/member-candidates`, {
      params: { keyword }, signal, silentError: true,
    })
  },
  join(workspaceId: number, payload: { request_id: string; user_id: number }) {
    return request.post<unknown, DirectRoleMember>(`/role-assignments/workspaces/${workspaceId}/members`, payload, { silentError: true })
  },
  changeMembership(workspaceId: number, memberId: number, payload: {
    request_id: string; expected_version: number; operation: WorkspaceMembershipOperation
  }) {
    return request.patch<unknown, DirectRoleMember>(`/role-assignments/workspaces/${workspaceId}/members/${memberId}/membership`, payload, { silentError: true })
  },
}
