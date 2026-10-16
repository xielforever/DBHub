import { computed, onScopeDispose, shallowRef } from 'vue'
import type { ExecResult } from '../../api/workbench'

export interface ExecutionContext {
  connectionId: number
  connectionName: string
  database: string
  sql: string
  selected: boolean
}

export type ExecutionStatus = 'idle' | 'running' | 'canceling' | 'success' | 'error' | 'canceled'

interface ExecutionState {
  status: ExecutionStatus
  context: Readonly<ExecutionContext> | null
  result: ExecResult | null
  error: string
}

type Executor = (context: Readonly<ExecutionContext>, signal: AbortSignal) => Promise<ExecResult>

/** 单编辑器只允许一个在途请求，结果始终绑定执行时的连接、库和 SQL。 */
export function useSqlExecution(execute: Executor) {
  const state = shallowRef<ExecutionState>({ status: 'idle', context: null, result: null, error: '' })
  let active: { controller: AbortController; canceled: boolean } | null = null
  let disposed = false
  const busy = computed(() => state.value.status === 'running' || state.value.status === 'canceling')

  async function run(context: ExecutionContext): Promise<boolean> {
    if (active || disposed) return false
    const request = { controller: new AbortController(), canceled: false }
    const snapshot = Object.freeze({ ...context })
    active = request
    state.value = { status: 'running', context: snapshot, result: null, error: '' }

    try {
      const result = await execute(snapshot, request.controller.signal)
      if (disposed || active !== request) return true
      state.value = request.canceled
        ? { status: 'canceled', context: snapshot, result: null, error: '' }
        : { status: 'success', context: snapshot, result, error: '' }
    } catch (error: unknown) {
      if (disposed || active !== request) return true
      state.value = request.canceled
        ? { status: 'canceled', context: snapshot, result: null, error: '' }
        : {
            status: 'error', context: snapshot, result: null,
            error: error instanceof Error ? error.message : '执行失败，请检查 SQL 或数据库连接。',
          }
    } finally {
      if (active === request) active = null
    }
    return true
  }

  function cancel() {
    if (!active || active.canceled) return
    active.canceled = true
    state.value = { ...state.value, status: 'canceling' }
    active.controller.abort()
  }

  onScopeDispose(() => {
    disposed = true
    active?.controller.abort()
    active = null
  })

  return { state, busy, run, cancel }
}
