import request from '../utils/request'
import type { ConnectionItem } from './datasource'

export type ScriptType = 'mysql' | 'postgres' | 'redis'
export const scriptTypes: Record<ScriptType, string> = { mysql: 'MySQL', postgres: 'PostgreSQL', redis: 'Redis' }

export function matchesScriptType(source: ConnectionItem, type: ScriptType) {
  if (type === 'redis') return source.type === 'redis' && !!source.capabilities?.keys
  return !!source.capabilities?.sql && (source.template?.dialect || source.type) === type
}

export interface ScriptTarget {
  connection_id: number
  database: string
  state?: 'available' | 'unavailable'
}
export interface ScriptFolder { id: number; parent_id: number | null; name: string }
export interface ScriptEntry {
  id: number
  name: string
  script_type: ScriptType
  folder_id?: number | null
  read_only: boolean
  shared?: boolean
  owner_name?: string
  updated_at: string
}
export interface WorkbenchScript extends ScriptEntry {
  content: string
  version?: number
  default_target?: ScriptTarget
}
export interface ScriptReader { user_id: number; display_name: string }
type ScriptUpdate = { name?: string; folder_id?: number | null; content?: string; default_target?: Omit<ScriptTarget, 'state'> | null }

const scripts = '/query/scripts'
const folders = '/query/script-folders'
export const workbenchScripts = {
  folders(signal?: AbortSignal) {
    return request.get<unknown, { items: ScriptFolder[] }>(folders, { signal, silentError: true })
  },
  createFolder(name: string, parent_id: number | null) {
    return request.post<unknown, ScriptFolder>(folders, { name, parent_id }, { silentError: true })
  },
  renameFolder(id: number, name: string) {
    return request.patch<unknown, ScriptFolder>(`${folders}/${id}`, { name }, { silentError: true })
  },
  list(params: { view: 'mine' | 'shared'; folder_id?: number; q?: string; page: number; page_size: number }, signal?: AbortSignal) {
    return request.get<unknown, { items: ScriptEntry[]; total: number }>(scripts, { params, signal, silentError: true })
  },
  get(id: number, signal?: AbortSignal) {
    return request.get<unknown, WorkbenchScript>(`${scripts}/${id}`, { signal, silentError: true })
  },
  create(data: { name: string; script_type: ScriptType; content: string } & ScriptUpdate) {
    return request.post<unknown, WorkbenchScript>(scripts, data, { silentError: true })
  },
  save(id: number, version: number, data: ScriptUpdate) {
    return request.patch<unknown, WorkbenchScript>(`${scripts}/${id}`, { ...data, version }, { silentError: true })
  },
  readers(id: number, candidates: boolean, signal?: AbortSignal) {
    const route = candidates ? 'share-candidates' : 'readers'
    return request.get<unknown, { items: ScriptReader[] }>(`${scripts}/${id}/${route}`, { signal, silentError: true })
  },
  grant(id: number, user: number, grant: boolean) {
    const route = `${scripts}/${id}/readers/${user}`
    return grant ? request.put(route, undefined, { silentError: true }) : request.delete(route, { silentError: true })
  },
}
