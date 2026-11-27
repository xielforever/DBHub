import request from '../utils/request'
import { workbenchApi } from './workbench'

export interface CatalogSource {
  id: number
  name: string
  type: string
}

export interface CatalogObject {
  name: string
  type: 'table' | 'view'
  schema: string
  comment?: string
}

export const catalogApi = {
  sources(signal: AbortSignal) {
    return request.get<unknown, { items: CatalogSource[] }>('/metadata/catalog/sources', { signal, silentError: true })
  },
  databases(connection: number, signal: AbortSignal) {
    return workbenchApi.databases(connection, signal, true)
  },
  objects(connection: number, database: string, signal: AbortSignal) {
    return request.get<unknown, { items: CatalogObject[]; incomplete: boolean }>('/metadata/catalog/objects', {
      params: { connection_id: connection, database },
      signal,
      timeout: 25000,
      silentError: true,
    })
  },
  columns(connection: number, object: { database: string; schema: string; name: string }, signal: AbortSignal) {
    return workbenchApi.columns(connection, {
      database: object.database,
      schema: object.schema,
      table: object.name,
    }, { signal, silentError: true })
  },
}
