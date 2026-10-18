import request from '../utils/request'
import { newRequestId } from '../utils/request-id'
import type { DataContract } from './managed-report'
import type {
  ChartKind,
  ChartConfig,
} from '../components/business/chart/ChartCard.vue'
import type { ExecResult } from './workbench'
export interface DashboardTile {
  key: string
  report_id: number
  required: boolean
  width: 'full' | 'half' | 'wide' | 'narrow'
  comparison_group: string
  contract_hash: string
  binding_policy: 'follow_published'
}
export interface DashboardQuality {
  allow_unknown_freshness: boolean
  reason: string
  max_watermark_skew_seconds: number
}
export interface DashboardDefinition {
  name: string
  description: string
  tiles: DashboardTile[]
  user_ids: number[]
  group_ids: number[]
  quality: DashboardQuality
}
export interface ManagedDashboard {
  id: number
  active_release_id: number | null
  owner_id: number
  owner_name: string
  draft: DashboardDefinition
  draft_version: number
  latest_version: number
  publication_no: number
  lifecycle: 'active' | 'archived' | 'deleted'
  lifecycle_epoch: number
  updated_at: string
}
export interface DashboardReport {
  id: number
  name: string
  publication_no: number
  version: number
  chart_type: ChartKind
  chart_config: ChartConfig
  data_contract: { declaration: DataContract; hash: string }
}
export interface DashboardReview {
  id: number
  version_id: number
  number: number
  definition: DashboardDefinition
  submitted_by: number | null
  submitter_name: string
  reviewer_id: number
  status: string
  comment: string
  expires_at: string
  validated_at: string | null
  lifecycle_epoch: number
  was_published: boolean
}
export interface DashboardViewTile extends DashboardTile {
  status: 'ready' | 'restricted' | 'contract_changed'
  report?: DashboardReport
  execution_id?: string
}
export interface DashboardView {
  id: number
  version: number
  name: string
  description: string
  publication_no: number
  lifecycle_epoch: number
  tiles: DashboardViewTile[]
  quality: DashboardQuality
}
export interface DashboardRefresh extends DashboardView {
  refresh_id: string
  mode: string
  server_time: string
  expires_at: string
}
export interface DashboardTileResult {
  dashboard_id: number
  dashboard_version: number
  dashboard_publication_no: number
  lifecycle_epoch: number
  refresh_id: string
  tile_key: string
  report_id: number
  result: ExecResult
  quality: {
    query_completed_at: string
    data_as_of: string | null
    freshness: 'fresh' | 'stale' | 'unknown'
    contract_hash: string
  }
  report_publication_no: number
  execution_id: string
}
export interface AccessRequest {
  id: number
  target_user_id: number | null
  target_group_id: number | null
  decision_reason: string
  requester_id: number
  report_id?: number
  dashboard_id: number | null
  reason: string
  status: string
  version: number
  assigned_to: number | null
  review_id: number | null
  actual_publication_no: number | null
  expires_at: string
}
const options = { silentError: true }
export const managedDashboardApi = {
  list: (params: Record<string, unknown> = {}) =>
    request.get<unknown, { items: ManagedDashboard[]; total: number }>(
      '/managed-dashboards',
      { params, ...options },
    ),
  get: (id: number) =>
    request.get<unknown, ManagedDashboard>(
      `/managed-dashboards/${id}`,
      options,
    ),
  save: (
    id: number,
    version: number,
    definition: DashboardDefinition,
    key: string,
  ) =>
    id
      ? request.put<unknown, { id: number; version: number }>(
          `/managed-dashboards/${id}/draft`,
          { request_id: key, version, definition },
          options,
        )
      : request.post<unknown, { id: number; version: number }>(
          '/managed-dashboards',
          { request_id: key, definition },
          options,
        ),
  catalog: () =>
    request.get<unknown, { items: DashboardReport[] }>(
      '/dashboard-report-catalog',
      options,
    ),
  reviews: (id: number) =>
    request.get<unknown, { items: DashboardReview[] }>(
      `/managed-dashboards/${id}/versions`,
      options,
    ),
  published: (params: Record<string, unknown> = {}) =>
    request.get<unknown, { items: DashboardView[]; total: number }>(
      '/published-dashboards',
      { params, ...options },
    ),
  publishedGet: (id: number, signal?: AbortSignal) =>
    request.get<unknown, DashboardView>(`/published-dashboards/${id}`, {
      ...options,
      signal,
    }),
  command: (
    id: number,
    action: string,
    payload: Record<string, unknown>,
    key: string,
  ) =>
    request.post<unknown, { id: number; version: number }>(
      `/managed-dashboards/${id}/${action}`,
      { ...payload, request_id: key },
      options,
    ),
  reviewCommand: (
    id: number,
    action: string,
    payload: Record<string, unknown>,
    key: string,
  ) =>
    request.post<unknown, { id: number; version: number }>(
      `/dashboard-reviews/${id}/${action}`,
      { ...payload, request_id: key },
      options,
    ),
  refresh: (
    id: number,
    mode: 'published' | 'draft' | 'review',
    review_id: number,
    signal?: AbortSignal,
  ) =>
    request.post<unknown, DashboardRefresh>(
      `/managed-dashboards/${id}/refreshes`,
      { mode, review_id },
      { ...options, signal },
    ),
  execute: (id: string, tile: string, signal?: AbortSignal) =>
    request.post<unknown, DashboardTileResult>(
      `/dashboard-refreshes/${id}/tiles/${encodeURIComponent(tile)}/execute`,
      {},
      { ...options, signal, timeout: 90000 },
    ),
  requestAudienceAccess: (
    id: number,
    payload: Record<string, unknown>,
    key: string,
  ) =>
    request.post(
      `/managed-dashboards/${id}/access-requests`,
      { ...payload, request_id: key },
      options,
    ),
  requestAccess: (id: number, tile_key: string, reason: string) =>
    request.post(
      `/published-dashboards/${id}/access-requests`,
      { request_id: newRequestId(), tile_key, reason },
      options,
    ),
  accessRequests: () =>
    request.get<unknown, { items: AccessRequest[] }>(
      '/access-requests',
      options,
    ),
  accessAction: (
    id: number,
    action: string,
    payload: Record<string, unknown>,
    key: string,
  ) =>
    request.post(
      `/access-requests/${id}/${action}`,
      { ...payload, request_id: key },
      options,
    ),
  dashboardAudience: (id: number) =>
    request.get<
      unknown,
      { base_publication_no: number; user_ids: number[]; group_ids: number[] }
    >(`/managed-dashboards/${id}/current-audience`, options),
  changeDashboardAudience: (
    id: number,
    payload: Record<string, unknown>,
    key: string,
  ) =>
    request.post(
      `/managed-dashboards/${id}/audience-changes`,
      { ...payload, request_id: key },
      options,
    ),
  currentAudience: (id: number) =>
    request.get<
      unknown,
      {
        base_publication_no: number
        account_id: number
        user_ids: number[]
        group_ids: number[]
      }
    >(`/managed-reports/${id}/current-audience`, options),
  audienceChange: (
    id: number,
    payload: {
      base_publication_no: number
      user_ids: number[]
      group_ids: number[]
      request_ids: number[]
    },
    key: string,
  ) =>
    request.post<unknown, { id: number; version: number }>(
      `/managed-reports/${id}/audience-changes`,
      { ...payload, request_id: key },
      options,
    ),
}
