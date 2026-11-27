import request from '../utils/request'
import type { ExecResult } from './workbench'

export type DashboardDeliveryKind = 'export' | 'personal' | 'team'

export interface DashboardDeliveryReceipt {
  id: number
  dashboard_id: number
  plan_id: number
  kind: DashboardDeliveryKind
  status: string
  slot: string
  expires_at?: string
}

export interface DashboardDeliveredCard {
  tile_key: string
  status: 'ready' | 'restricted' | 'unavailable'
  quality?: { data_as_of?: string | null }
  snapshot?: { name: string; created_at: string; result: ExecResult }
}

export interface DashboardDelivery {
  dashboard_id: number
  publication_no: number
  tile_key?: string
  kind: DashboardDeliveryKind
  format?: 'zip' | 'csv' | 'json' | 'xlsx'
  expires_at: string
  snapshot?: { cards: DashboardDeliveredCard[] }
}

export interface DashboardMetricEvent {
  id: number
  active: boolean
  observed_value?: string
  observed_at: string
  disposition?: string
  disposed_at?: string
}

const quiet = { silentError: true }

export const dashboardDeliveryApi = {
  list: (signal: AbortSignal) => request.get<unknown, { items: DashboardDeliveryReceipt[] }>(
    '/dashboard-deliveries', { ...quiet, signal },
  ),
  get: (id: number, signal: AbortSignal) => request.get<unknown, DashboardDelivery>(
    `/dashboard-deliveries/${id}`, { ...quiet, signal },
  ),
  credential: (id: number, signal: AbortSignal) => request.post<unknown, { credential: string; expires_at: string }>(
    `/dashboard-deliveries/${id}/credential`, {}, { ...quiet, signal },
  ),
  redeem: (id: number, credential: string, signal: AbortSignal) => request.post<unknown, Blob>(
    `/dashboard-deliveries/${id}/redeem`, { credential }, { ...quiet, signal, responseType: 'blob' },
  ),
  alerts: (plan: number, signal: AbortSignal) => request.get<unknown, { items: DashboardMetricEvent[] | null }>(
    `/dashboard-delivery-plans/${plan}/alerts`, { ...quiet, signal },
  ),
  disposeAlert: (plan: number, event: number, note: string, key: string, signal: AbortSignal) => request.post(
    `/dashboard-delivery-plans/${plan}/alerts/${event}/disposition`, { note, request_id: key }, { ...quiet, signal },
  ),
}
