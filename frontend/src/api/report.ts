/**
 * 报表 M3/M4
 */
import request from '../utils/request'
import { newRequestId } from '../utils/request-id'
import type { ExecResult } from './workbench'
import type { ChartKind, ChartConfig } from '../components/business/chart/ChartCard.vue'

export interface ReportItem {
	parameters?: import('../views/report/reportParameters').ReportParameter[]
  id: number
  name: string
  description: string
  connection_id: number
  definition_revision_id: number
  source_name: string
  source_family_id: string
  source_revision: number
  version: number
  database_name: string
  sql_text: string
  chart_type: ChartKind
  chart_config: ChartConfig
  visibility: 'private' | 'shared'
  owner_user_id: number
  owner_name: string
  starred_by: number[]
  starred: boolean
  created_at: string
  updated_at: string
}

export interface ReportPayload {
	export_policy?: import('./report-exports').ReportExportPolicy
	parameters?: import('../views/report/reportParameters').ReportParameter[]
  data_contract?:import('./managed-report').DataContract
  request_id?: string
  name: string
  description?: string
  connection_id: number
  database?: string
  sql: string
  chart_type: ChartKind
  chart_config: ChartConfig
  visibility: 'private' | 'shared'
}

export const reportApi = {
  create(payload: ReportPayload) {
    return request.post<unknown, ReportItem>('/reports', { ...payload, request_id: payload.request_id ?? newRequestId() }, { silentError: true })
  },
  list(params: { q?: string; scope?: 'mine' | 'starred' | 'shared' | 'all'; connection_id?: number; page?: number; page_size?: number }, signal?: AbortSignal, silentError = false) {
    return request.get<unknown, { items: ReportItem[]; total: number; page: number; page_size: number }>('/reports', { params, signal, silentError })
  },
  get(id: number, signal?: AbortSignal, silentError = false) {
    return request.get<unknown, ReportItem>(`/reports/${id}`, { signal, silentError })
  },
  update(id: number, payload: Partial<ReportPayload> & { version: number }) {
    return request.put<unknown, ReportItem>(`/reports/${id}`, payload, { silentError: true })
  },
  preview(payload: { connection_id: number; database: string; sql: string; data_contract?: import('./managed-report').DataContract; parameters?: import('../views/report/reportParameters').ReportParameter[]; params?: import('../views/report/reportParameters').ParameterValues }, signal?: AbortSignal) {
    return request.post<unknown, ExecResult>('/reports/preview', { ...payload, request_id: newRequestId() }, { signal, timeout: 90000, silentError: true })
  },
  execute(id: number, version: number, signal?: AbortSignal, params?: import('../views/report/reportParameters').ParameterValues) {
    return request.post<unknown, ExecResult>(`/reports/${id}/execute`, { version, request_id: newRequestId(), params }, { signal, timeout: 90000, silentError: true })
  },
  remove(id: number) {
    return request.delete<unknown, { id: number }>(`/reports/${id}`)
  },
  star(id: number, star: boolean, signal?: AbortSignal, silentError = false) {
    return request.post<unknown, { starred: boolean }>(`/reports/${id}/star`, { star }, { signal, silentError })
  },
}
