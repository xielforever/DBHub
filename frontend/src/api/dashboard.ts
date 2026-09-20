/**
 * 仪表盘：运营概览指标（M1） + 报表仪表盘 CRUD（M4）
 */
import request from '../utils/request'
import type { QueryHistoryItem } from './workbench'

export interface DailyTrend {
  date: string
  total: number
  failed: number
  slow: number
}

export interface ConnRank {
  connection_id: number
  name: string
  count: number
}

export interface MetricsOverview {
  kpi: {
    connections: number
    today_queries: number
    today_active_users: number
    audit_events: number
  }
  connection_types: Record<string, number>
  trend: DailyTrend[]
  rank: ConnRank[]
  recent: QueryHistoryItem[]
  days: number
}

export const dashboardApi = {
  overview(days = 14) {
    return request.get<unknown, MetricsOverview>('/metrics/overview', { params: { days } })
  },
}

// M4 报表仪表盘
export interface DashboardLayoutItem {
  report_id: number
  x?: number
  y?: number
  w?: number
  h?: number
}

export interface DashboardItem {
  id: number
  owner_user_id: number
  owner_name: string
  name: string
  description: string
  visibility: 'private' | 'shared'
  layout: DashboardLayoutItem[]
  created_at: string
  updated_at: string
  starred: boolean
}

export interface DashboardList {
  items: DashboardItem[]
  total: number
  page: number
  page_size: number
}

export const reportDashboardApi = {
  create(payload: { name: string; description?: string; visibility?: 'private' | 'shared'; layout?: unknown }) {
    return request.post<unknown, { id: number }>('/dashboards', payload)
  },
  list(params?: { q?: string; keyword?: string; scope?: string; page?: number; page_size?: number; visibility?: string }) {
    return request.get<unknown, DashboardList>('/dashboards', { params })
  },
  get(id: number) {
    return request.get<unknown, DashboardItem>(`/dashboards/${id}`)
  },
  update(id: number, payload: { name?: string; description?: string; visibility?: 'private' | 'shared'; layout?: unknown }) {
    return request.put<unknown, DashboardItem>(`/dashboards/${id}`, payload)
  },
  remove(id: number) {
    return request.delete<unknown, { deleted: boolean }>(`/dashboards/${id}`)
  },
  star(id: number, starred: boolean) {
    return request.post<unknown, { starred: boolean }>(`/dashboards/${id}/star`, { starred })
  },
}
