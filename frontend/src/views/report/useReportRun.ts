import { onScopeDispose, shallowRef } from 'vue'

export type ReportRunState<T> = { status: 'idle' | 'loading' | 'error'; message?: string } | { status: 'success'; data: T }

/** Each selection owns its response. Aborting transport also invalidates its
 * generation, since a response may already be resolving when abort runs. */
export function useReportRun<T>() {
  const state = shallowRef<ReportRunState<T>>({ status: 'idle' })
  let controller: AbortController | undefined
  let generation = 0
  let activeKey = ''
  let disposed = false
  function reset() {
    generation++
    controller?.abort()
    controller = undefined
    activeKey = ''
    state.value = { status: 'idle' }
  }
  async function run(key: string, request: (signal: AbortSignal) => Promise<T>) {
    if (disposed || (state.value.status === 'loading' && activeKey === key)) return false
    reset()
    const run = generation
    const current = new AbortController()
    controller = current
    activeKey = key
    state.value = { status: 'loading' }
    try {
      const data = await request(current.signal)
      if (disposed || run !== generation) return false
      state.value = { status: 'success', data }
      return true
    } catch (error) {
      if (disposed || run !== generation) return false
      state.value = { status: 'error', message: error instanceof Error ? error.message : '报表查询失败，请检查连接和 SQL' }
      return false
    } finally {
      if (run === generation) controller = undefined
    }
  }
  onScopeDispose(() => { reset(); disposed = true })
  return { state, run, reset }
}
