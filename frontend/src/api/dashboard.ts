/**
 * 仪表盘汇总指标 API
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
