import request from '../utils/request'
export interface QuerySettings { timeout_ms: number; max_rows: number; block_unsafe_write: boolean }
export interface RowCondition { column: string; operator: string; value?: string | number | boolean | null }
export interface QueryPolicy { id?: number; connection_id: number; database_name: string; schema_name: string; table_name: string; role_code: string; column_permissions: Record<string, 'visible' | 'masked' | 'hidden'>; row_conditions: RowCondition[] }
export interface QueryJob { id: string; user_id: number; connection_id: number; sql: string; status: string; created_at: string; error?: string }
export interface ExportRequest {id:number;user_id:number;format:string;rows:number;reason:string;status:'pending'|'approved'|'rejected';created_at:string}
export interface QueryShare { id: number; name: string; expire_at: string; revoked: boolean; access_count: number; created_at: string }
export const advancedApi = {
  exports: () => request.get<unknown, {items: ExportRequest[]}>('/query/exports'),
  createExport: (payload: {connection_id:number;database:string;sql:string;params:Record<string,string>;format:string;reason:string}) => request.post('/query/exports', payload, {timeout:65000}),
  reviewExport: (id:number,status:string) => request.post(`/query/exports/${id}/review`,{status}),
  downloadExport: (id:number) => request.get<unknown,{format:string;result:{columns:string[];rows:unknown[][]}}>(`/query/exports/${id}/download`),
  aiStatus: () => request.get<unknown, { configured: boolean; model: string }>('/ai/status'),
  ai: (payload: { prompt: string; sql?: string; dialect?: string; tables?: string[]; mode?: string; explain?: string }) => request.post<unknown, { content: string; model: string }>('/ai/complete', payload, { timeout: 65000 }),
  settings: () => request.get<unknown, QuerySettings>('/query/settings'),
  saveSettings: (payload: QuerySettings) => request.put<unknown, QuerySettings>('/query/settings', payload),
  policies: (id: number) => request.get<unknown, { items: QueryPolicy[] }>('/query/policies', { params: { connection_id: id } }),
  savePolicy: (payload: QueryPolicy) => request.put<unknown, QueryPolicy>('/query/policies', payload),
  deletePolicy: (id: number) => request.delete(`/query/policies/${id}`),
  jobs: () => request.get<unknown, { items: QueryJob[]; max_concurrent: number }>('/query/jobs'),
  cancelJob: (id: string) => request.post(`/query/jobs/${id}/cancel`),
  presence: (id: number) => request.post<unknown, { items: { name: string; self: boolean }[] }>('/query/presence', { connection_id: id }),
  shares: () => request.get<unknown, { items: QueryShare[] }>('/query/shares'),
  createShare: (payload: { name: string; connection_id: number; database: string; sql: string; params?: Record<string, string>; expire_days: number; include_result: boolean }) => request.post<unknown, { share: QueryShare; token: string }>('/query/shares', payload, {timeout:25000}),
  revokeShare: (id: number) => request.delete(`/query/shares/${id}`),
}
