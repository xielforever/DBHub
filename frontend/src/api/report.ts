/**
 * 报表中心 M3/M4
 */
import request from '../utils/request'
import type { ChartKind, ChartConfig } from '../components/business/chart/ChartCard.vue'

export interface ReportItem {
  id: number
  name: string
  description: string
  connection_id: number
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
    return request.post<unknown, ReportItem>('/reports', payload)
  },
  list(params: { q?: string; scope?: 'mine' | 'starred' | 'shared' | 'all'; connection_id?: number; page?: number; page_size?: number }) {
    return request.get<unknown, { items: ReportItem[]; total: number; page: number; page_size: number }>('/reports', { params })
  },
  get(id: number) {
    return request.get<unknown, ReportItem>(`/reports/${id}`)
  },
  update(id: number, payload: Partial<ReportPayload>) {
    return request.put<unknown, ReportItem>(`/reports/${id}`, payload)
  },
  remove(id: number) {
    return request.delete<unknown, { id: number }>(`/reports/${id}`)
  },
  star(id: number, star: boolean) {
    return request.post<unknown, { starred: boolean }>(`/reports/${id}/star`, { star })
  },
}
