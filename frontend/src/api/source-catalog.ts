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
  access_mode: 'global' | 'restricted'
  access_version: number
  profile_version: number
  category: string
  tags: string[]
  maintainer: string
  contact: string
  favorite: boolean
  last_used_at?: string
  my_connection_id?: number
  my_revision_id?: number
}
export interface DefinitionChange { field: string; label: string; before: string; after: string }
export interface DefinitionRevision extends SourceDefinition {
  binding_count?: number
  previous_revision?: number
  changes: DefinitionChange[]
}
export interface DefinitionPayload { name: string; description: string; visibility: Visibility; access_mode?: 'global' | 'restricted'; config: SourceConfig }
export interface PersonalIdentity { name: string; username: string; password: string; confirmed: boolean }

export const sourceCatalogApi = {
  list: (params?: {keyword?: string; category?: string; view?: string; page?: number; page_size?: number}) => request.get<unknown, { items: SourceDefinition[]; total: number }>('/datasource-definitions', {params}),
  categories: () => request.get<unknown, {items: string[]}>('/datasource-definitions/categories'),
  access: (id: number) => request.get<unknown, {mode: 'global' | 'restricted'; user_ids: number[]; group_ids: number[]; version: number}>(`/datasource-definitions/${id}/access`),
  setAccess: (id: number, payload: {mode: string; user_ids: number[]; group_ids: number[]; expected_version: number}) => request.put(`/datasource-definitions/${id}/access`, payload),
  profile: (id: number, payload: {category: string; tags: string[]; maintainer: string; contact: string; expected_version: number}) => request.put(`/datasource-definitions/${id}/profile`, payload),
  favorite: (id: number, favorite: boolean) => request.put(`/datasource-definitions/${id}/favorite`, {favorite}),
  create: (payload: DefinitionPayload) => request.post<unknown, SourceDefinition>('/datasource-definitions', payload),
  publish: (connectionId: number, visibility: Visibility) => request.post<unknown, SourceDefinition>(`/connections/${connectionId}/publish`, { visibility }),
  revise: (id: number, payload: DefinitionPayload) => request.post<unknown, SourceDefinition>(`/datasource-definitions/${id}/revisions`, payload),
  revisions: (id: number, page = 1) => request.get<unknown, {items: DefinitionRevision[]; total: number; page: number; page_size: number}>(`/datasource-definitions/${id}/revisions`, {params: {page, page_size: 10}}),
  status: (id: number, enabled: boolean, expectedBindings?: number) => request.patch(`/datasource-definitions/${id}/status`, { enabled, ...(!enabled ? {confirmed: true, expected_binding_count: expectedBindings} : {}) }),
  add: (id: number, payload: PersonalIdentity) => request.post<unknown, ConnectionItem>(`/datasource-definitions/${id}/connections`, payload),
  test: (id: number, payload: PersonalIdentity) => request.post<unknown, TestResult>(`/datasource-definitions/${id}/connections?test=true`, payload, { timeout: 25_000 }),
  upgrade: (connectionId: number, definitionId: number, payload: PersonalIdentity) => request.post<unknown, ConnectionItem>(`/connections/${connectionId}/revision`, { ...payload, definition_revision_id: definitionId }),
}
