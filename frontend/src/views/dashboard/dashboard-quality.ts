import type {
  DashboardViewTile,
  DashboardTileResult,
  DashboardQuality,
} from '../../api/managed-dashboard'
export const PAGE_FRESH_MS = 5 * 60_000,
  RETAIN_MS = 30 * 60_000
export interface TileState {
  tile: DashboardViewTile
  status:
    | 'idle'
    | 'loading'
    | 'success'
    | 'error'
    | 'restricted'
    | 'contract_changed'
  result?: DashboardTileResult
  message?: string
  cycle?: string
  retained?: boolean
}
export function usableResult(state: TileState, now: number) {
  return (
    !!state.result &&
    now - Date.parse(state.result.quality.query_completed_at) < RETAIN_MS &&
    state.status !== 'restricted' &&
    state.status !== 'contract_changed'
  )
}
export function tileFreshness(state: TileState, now: number) {
  if (!usableResult(state, now)) return 'missing'
  if (
    state.retained ||
    now - Date.parse(state.result!.quality.query_completed_at) > PAGE_FRESH_MS
  )
    return 'stale'
  const watermark = state.result!.quality.data_as_of
  if (!watermark) return 'unknown'
  const maxAge =
    state.tile.report?.data_contract.declaration.freshness
      .max_data_age_seconds || 0
  return now - Date.parse(watermark) > maxAge * 1000 ? 'stale' : 'fresh'
}
export function dashboardQuality(
  states: TileState[],
  policy: DashboardQuality,
  now: number,
  cycle?: string,
  partial = false,
) {
  const successful = (s: TileState) =>
    usableResult(s, now) &&
    s.status === 'success' &&
    !s.retained &&
    (partial || !cycle || s.cycle === cycle)
  const required = states.filter((s) => s.tile.required),
    optional = states.filter((s) => !s.tile.required),
    available = required.filter(successful).length
  const statuses = required.map((s) => tileFreshness(s, now))
  let freshness = statuses.includes('missing')
    ? 'missing'
    : statuses.includes('stale')
      ? 'stale'
      : statuses.includes('unknown')
        ? 'unknown'
        : 'fresh'
  const groups = new Map<string, number[]>()
  for (const state of states) {
    if (!state.tile.comparison_group || !usableResult(state, now)) continue
    const watermark = state.result?.quality.data_as_of
    if (!watermark) {
      freshness = 'unknown'
      continue
    }
    const values = groups.get(state.tile.comparison_group) || []
    values.push(Date.parse(watermark))
    groups.set(state.tile.comparison_group, values)
  }
  const misaligned = [...groups.values()].some(
    (values) =>
      Math.max(...values) - Math.min(...values) >
      policy.max_watermark_skew_seconds * 1000,
  )
  if (misaligned && freshness === 'fresh') freshness = 'stale'
  return {
    available,
    total: required.length,
    optionalAvailable: optional.filter(successful).length,
    optionalTotal: optional.length,
    complete: !partial && required.length > 0 && available === required.length,
    freshness,
    misaligned,
  }
}
export function mustClearPrevious(before: TileState, next: DashboardViewTile) {
  return (
    next.status !== 'ready' ||
    before.tile.contract_hash !== next.contract_hash ||
    before.result?.report_publication_no !== next.report?.publication_no
  )
}
export function retainOnFailure(status?: number) {
  return status === undefined || status === 429 || status >= 500
}
