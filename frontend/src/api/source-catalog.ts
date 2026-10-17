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
  created_at: string
  config: SourceConfig
  my_connection_id?: number
  my_revision_id?: number
}
export interface DefinitionChange { field: string; label: string; before: string; after: string }
export interface DefinitionRevision extends SourceDefinition {
  binding_count?: number
  previous_revision?: number
  changes: DefinitionChange[]
}
export interface DefinitionPayload { name: string; description: string; visibility: Visibility; config: SourceConfig }
export interface PersonalIdentity { name: string; username: string; password: string; confirmed: boolean }

export const sourceCatalogApi = {
  list: () => request.get<unknown, { items: SourceDefinition[]; total: number }>('/datasource-definitions'),
  create: (payload: DefinitionPayload) => request.post<unknown, SourceDefinition>('/datasource-definitions', payload),
  publish: (connectionId: number, visibility: Visibility) => request.post<unknown, SourceDefinition>(`/connections/${connectionId}/publish`, { visibility }),
  revise: (id: number, payload: DefinitionPayload) => request.post<unknown, SourceDefinition>(`/datasource-definitions/${id}/revisions`, payload),
  revisions: (id: number, page = 1) => request.get<unknown, {items: DefinitionRevision[]; total: number; page: number; page_size: number}>(`/datasource-definitions/${id}/revisions`, {params: {page, page_size: 10}}),
  status: (id: number, enabled: boolean, expectedBindings?: number) => request.patch(`/datasource-definitions/${id}/status`, { enabled, ...(!enabled ? {confirmed: true, expected_binding_count: expectedBindings} : {}) }),
  add: (id: number, payload: PersonalIdentity) => request.post<unknown, ConnectionItem>(`/datasource-definitions/${id}/connections`, payload),
  test: (id: number, payload: PersonalIdentity) => request.post<unknown, TestResult>(`/datasource-definitions/${id}/connections?test=true`, payload, { timeout: 25_000 }),
  upgrade: (connectionId: number, definitionId: number, payload: PersonalIdentity) => request.post<unknown, ConnectionItem>(`/connections/${connectionId}/revision`, { ...payload, definition_revision_id: definitionId }),
}
