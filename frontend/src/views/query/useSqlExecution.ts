import { computed, onScopeDispose, shallowRef } from 'vue'
import type { ExecResult, ExecutionReceipt } from '../../api/workbench'
import { newRequestId } from '../../utils/request-id.ts'

export interface ExecutionContext {
  connectionId: number
  connectionName: string
  database: string
  sql: string
  selected: boolean
}

export interface ExecutionSnapshot extends ExecutionContext { requestId: string }
export type ExecutionStatus = 'idle' | 'running' | 'canceling' | 'checking' | 'success' | 'completed' | 'error' | 'canceled' | 'unknown'
export interface ExecutionRecoveryStore {
  read(): string | null
  write(id: string): void
  clear(): void
}
interface ExecutionState {
  status: ExecutionStatus
  requestId: string | null
  context: Readonly<ExecutionSnapshot> | null
  result: ExecResult | null
  receipt: ExecutionReceipt | null
  error: string
}
type Executor = (context: Readonly<ExecutionSnapshot>, signal: AbortSignal) => Promise<ExecResult>
type Lookup = (id: string, signal: AbortSignal) => Promise<ExecutionReceipt>
const unknownMessage = '执行结果待确认。取消、超时或断网不代表写入已回滚，请先核实目标数据库，再发起新的执行。'

function receiptOf(value: unknown, id: string): ExecutionReceipt | null {
  if (!value || typeof value !== 'object') return null
  const receipt = value as ExecutionReceipt
  return receipt.request_id === id && ['queued', 'running', 'succeeded', 'failed', 'rejected', 'unknown'].includes(receipt.status)
    ? receipt : null
}

/** 同一次执行沿用编号；通信异常只查回执，绝不自动重新发送 SQL。 */
export function useSqlExecution(execute: Executor, lookup: Lookup, recovery?: ExecutionRecoveryStore) {
  const state = shallowRef<ExecutionState>({ status: 'idle', requestId: null, context: null, result: null, receipt: null, error: '' })
  let active: { controller: AbortController; canceled: boolean } | null = null
  let disposed = false
  const busy = computed(() => ['running', 'canceling', 'checking'].includes(state.value.status))
  const needsReview = computed(() => state.value.status === 'unknown')

  try {
    const id = recovery?.read()
    if (id) state.value = { ...state.value, status: 'unknown', requestId: id, error: unknownMessage }
  } catch {
    state.value = { ...state.value, status: 'error', error: '无法读取执行恢复记录，请检查浏览器存储设置。' }
  }

  function settle(status: ExecutionStatus, result: ExecResult | null, error = '') {
    // 清除失败时保留记录：刷新后仍可核实，不把存储故障当作执行故障。
    if (status !== 'unknown') { try { recovery?.clear() } catch { /* 保留编号 */ } }
    state.value = { ...state.value, status, result, error }
  }

  function applyReceipt(receipt: ExecutionReceipt, canceled: boolean) {
    state.value = { ...state.value, receipt }
    if (receipt.status === 'succeeded') {
      if (receipt.kind === 'write' && !receipt.transactional) {
        settle('success', { kind: 'write', affected_rows: receipt.affected_rows, duration_ms: receipt.duration_ms })
      } else {
        settle('completed', null, receipt.transactional
          ? '该语句已执行，事务是否提交需要单独核实。'
          : '服务端已确认执行完成。查询结果未保存，本次不会重新执行 SQL。')
      }
    } else if (receipt.status === 'rejected' || receipt.status === 'failed') {
      settle(canceled ? 'canceled' : 'error', null, receipt.message || '服务端已确认本次执行未完成。')
    } else {
      settle('unknown', null, (receipt.status === 'running' || receipt.status === 'queued')
        ? '服务端仍在处理本次请求。请稍后核实状态，当前不会重新发送 SQL。' : unknownMessage)
    }
  }

  async function resolveOutcome(id: string, canceled: boolean) {
    const controller = new AbortController()
    active = { controller, canceled }
    state.value = { ...state.value, status: 'checking' }
    const timer = setTimeout(() => controller.abort(), 8000)
    try {
      const receipt = receiptOf(await lookup(id, controller.signal), id)
      if (disposed) return
      if (receipt) applyReceipt(receipt, canceled)
      else settle('unknown', null, unknownMessage)
    } catch {
      // 404 也不能证明未执行：原请求可能仍在登记或回执服务暂时不可达。
      if (!disposed) settle('unknown', null, unknownMessage)
    } finally {
      clearTimeout(timer)
    }
  }

  async function run(context: ExecutionContext): Promise<boolean> {
    if (active || disposed || needsReview.value) return false
    let requestId: string
    try { requestId = newRequestId() } catch {
      settle('error', null, '浏览器无法生成安全的执行编号，请检查浏览器设置后再运行。')
      return false
    }
    const request = { controller: new AbortController(), canceled: false }
    active = request
    const snapshot = Object.freeze({ ...context, requestId })
    state.value = { status: 'running', requestId: snapshot.requestId, context: snapshot, result: null, receipt: null, error: '' }
    try {
      // 只持久化编号。保存失败时禁止发送，避免刷新后丢失恢复线索。
      try { recovery?.write(snapshot.requestId) } catch {
        settle('error', null, '无法保存执行恢复编号，请检查浏览器存储设置后再运行。')
        return false
      }
      const result = await execute(snapshot, request.controller.signal)
      if (disposed) return true
      if (request.canceled) await resolveOutcome(snapshot.requestId, true)
      else settle('success', result)
    } catch (error: unknown) {
      if (disposed) return true
      const failure = error as { message?: string; status?: number; data?: unknown } | null
      const receipt = receiptOf(failure?.data, snapshot.requestId)
      if (receipt) {
        applyReceipt(receipt, request.canceled)
        if (state.value.status === 'error' && failure?.message) {
          state.value = { ...state.value, error: failure.message }
        }
      } else if (!request.canceled && failure?.status && [400, 401, 403, 404, 409, 429].includes(failure.status)) {
        settle('error', null, failure.message || '请求未通过服务端检查。')
      } else {
        await resolveOutcome(snapshot.requestId, request.canceled)
      }
    } finally {
      active = null
    }
    return true
  }

  async function check() {
    if (active || disposed || !needsReview.value || !state.value.requestId) return
    try { await resolveOutcome(state.value.requestId, false) } finally { active = null }
  }

  /** 页面须在用户明确确认已核实后调用；这不声明目标写入已回滚。 */
  function acknowledge() {
    if (active || disposed || !needsReview.value) return
    settle('idle', null)
  }

  function cancel() {
    if (!active || active.canceled || state.value.status === 'checking') return
    active.canceled = true
    state.value = { ...state.value, status: 'canceling' }
    active.controller.abort()
  }

  onScopeDispose(() => {
    disposed = true
    active?.controller.abort()
    active = null
  })

  return { state, busy, needsReview, run, cancel, check, acknowledge }
}
