import test from 'node:test'
import assert from 'node:assert/strict'
import {
  dashboardQuality,
  tileFreshness,
  usableResult,
  mustClearPrevious,
  retainOnFailure,
  PAGE_FRESH_MS,
  RETAIN_MS,
} from '../src/views/dashboard/dashboard-quality.ts'
import {
  workspaceGeneration,
  workspaceSignal,
  changeWorkspaceContext,
} from '../src/utils/workspace-context.ts'
const now = Date.parse('2026-10-18T09:30:00Z'),
  policy = {
    allow_unknown_freshness: false,
    reason: '',
    max_watermark_skew_seconds: 300,
  }
const state = (overrides: any = {}) => ({
  tile: {
    key: 'a',
    report_id: 1,
    required: true,
    width: 'half',
    comparison_group: '',
    contract_hash: 'hash',
    binding_policy: 'follow_published',
    status: 'ready',
    report: {
      publication_no: 1,
      data_contract: {
        declaration: { freshness: { max_data_age_seconds: 900 } },
      },
    },
  },
  status: 'success',
  result: {
    report_publication_no: 1,
    quality: {
      query_completed_at: new Date(now).toISOString(),
      data_as_of: new Date(now - 60_000).toISOString(),
      freshness: 'fresh',
      contract_hash: 'hash',
    },
    result: {
      columns: ['value'],
      rows: [['9007199254740993']],
      truncated: false,
    },
  },
  ...overrides,
})
test('required denominator includes denied, failed and optional cards correctly', () => {
  const a = state(),
    denied = state({
      tile: { ...state().tile, key: 'b' },
      status: 'restricted',
      result: undefined,
    }),
    optional = state({
      tile: { ...state().tile, key: 'c', required: false },
      status: 'error',
      result: undefined,
    })
  const q = dashboardQuality([a, denied, optional], policy, now)
  assert.equal(q.total, 2)
  assert.equal(q.available, 1)
  assert.equal(q.complete, false)
})
test('full results and fresh data are separate states', () => {
  const a = state()
  a.result.quality.data_as_of = new Date(now - 1800_000).toISOString()
  const q = dashboardQuality([a], policy, now)
  assert.equal(q.complete, true)
  assert.equal(q.freshness, 'stale')
})
test('unknown watermarks never become query time', () => {
  const a = state()
  a.result.quality.data_as_of = null
  assert.equal(tileFreshness(a, now), 'unknown')
  assert.equal(a.result.quality.data_as_of, null)
})
test('page age expires without automatic execution', () => {
  assert.equal(tileFreshness(state(), now + PAGE_FRESH_MS + 1), 'stale')
  assert.equal(usableResult(state(), now + RETAIN_MS), false)
})
test('last good results retained only for transient failure and never look fresh', () => {
  const a = state({ retained: true, status: 'error' })
  assert.equal(tileFreshness(a, now), 'stale')
  for (const status of [403, 404, 409, 400])
    assert.equal(retainOnFailure(status), false)
  for (const status of [429, 503, undefined])
    assert.equal(retainOnFailure(status), true)
})
test('publication, contract and permission changes immediately invalidate previous results', () => {
  const a = state()
  assert.equal(mustClearPrevious(a, a.tile), false)
  assert.equal(mustClearPrevious(a, { ...a.tile, status: 'restricted' }), true)
  assert.equal(mustClearPrevious(a, { ...a.tile, contract_hash: 'new' }), true)
  assert.equal(
    mustClearPrevious(a, {
      ...a.tile,
      report: { ...a.tile.report, publication_no: 2 },
    }),
    true,
  )
})
test('comparison watermarks are checked without aggregating numeric result rows', () => {
  const a = state(),
    b = state()
  a.tile.comparison_group = 'stock'
  b.tile.comparison_group = 'stock'
  b.result.quality.data_as_of = new Date(now - 600_000).toISOString()
  assert.equal(dashboardQuality([a, b], policy, now).misaligned, true)
  assert.equal(a.result.result.rows[0][0], '9007199254740993')
})
test('workspace switch cancels in-flight queries and changes response generation', () => {
  const old = workspaceSignal(),
    generation = workspaceGeneration()
  changeWorkspaceContext()
  assert.equal(old.aborted, true)
  assert.equal(workspaceGeneration(), generation + 1)
  assert.notEqual(workspaceSignal(), old)
  assert.equal(workspaceSignal().aborted, false)
})

test('old results do not count as success in the current full refresh', () => {
  const old = state({ retained: true, status: 'error', cycle: 'old' }),
    fresh = state({ cycle: 'new' })
  const q = dashboardQuality([old, fresh], policy, now, 'new')
  assert.equal(q.available, 1)
  assert.equal(q.total, 2)
  assert.equal(q.complete, false)
})
test('a single-card refresh never reports a full-page success', () => {
  const a = state({ cycle: 'single' })
  const q = dashboardQuality([a], policy, now, 'full', true)
  assert.equal(q.available, 1)
  assert.equal(q.complete, false)
})
