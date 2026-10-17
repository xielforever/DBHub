import request from '../utils/request'
import type { ConnectionItem, ConnectionPayload, TestResult } from './datasource'

export type Visibility = 'private' | 'global'
export type SourceConfig = Pick<ConnectionPayload, 'type' | 'host' | 'port' | 'database' | 'ssl_mode' | 'connection_timeout' | 'environment' | 'template_revision_id' | 'parameters'>
export interface SourceDefinition {
  id: number
  family_id: string
  revision: number
  name: string
  description: string
  owner_id: number
  visibility: Visibility
  enabled: boolean
  config: SourceConfig
  my_connection_id?: number
  my_revision_id?: number
}
export interface DefinitionPayload { name: string; description: string; visibility: Visibility; config: SourceConfig }
export interface PersonalIdentity { name: string; username: string; password: string; confirmed: boolean }

export const sourceCatalogApi = {
  list: () => request.get<unknown, { items: SourceDefinition[]; total: number }>('/datasource-definitions'),
  create: (payload: DefinitionPayload) => request.post<unknown, SourceDefinition>('/datasource-definitions', payload),
  publish: (connectionId: number, visibility: Visibility) => request.post<unknown, SourceDefinition>(`/connections/${connectionId}/publish`, { visibility }),
  revise: (id: number, payload: DefinitionPayload) => request.post<unknown, SourceDefinition>(`/datasource-definitions/${id}/revisions`, payload),
  status: (id: number, enabled: boolean) => request.patch(`/datasource-definitions/${id}/status`, { enabled }),
  add: (id: number, payload: PersonalIdentity) => request.post<unknown, ConnectionItem>(`/datasource-definitions/${id}/connections`, payload),
  test: (id: number, payload: PersonalIdentity) => request.post<unknown, TestResult>(`/datasource-definitions/${id}/connections?test=true`, payload, { timeout: 25_000 }),
  upgrade: (connectionId: number, definitionId: number, payload: PersonalIdentity) => request.post<unknown, ConnectionItem>(`/connections/${connectionId}/revision`, { ...payload, definition_revision_id: definitionId }),
}
