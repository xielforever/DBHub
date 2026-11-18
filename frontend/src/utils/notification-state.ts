export interface NotificationSummaryState {
  unread: number
  known: boolean
  loading: boolean
  failed: boolean
}

/** 只发布当前资格上下文的摘要；传输层即使忽略取消也不能回填旧空间数量。 */
export function createNotificationSummaryLoader(
  fetchSummary: (signal: AbortSignal) => Promise<{ unread: number }>,
  publish: (state: NotificationSummaryState) => void,
  canFetch: () => boolean = () => true,
) {
  let context: string | null = null
  let generation = 0
  let disposed = false
  let busy = false
  let queued = false
  let controller: AbortController | undefined
  let state: NotificationSummaryState = { unread: 0, known: false, loading: false, failed: false }
  const update = (patch: Partial<NotificationSummaryState>) => { state = { ...state, ...patch }; publish({ ...state }) }

  async function refresh(): Promise<void> {
    if (disposed || !context || !canFetch()) return
    if (busy) { queued = true; return }
    const run = generation
    const current = new AbortController()
    controller = current
    busy = true
    update({ loading: true })
    try {
      const summary = await fetchSummary(current.signal)
      if (disposed || run !== generation) return
      if (!Number.isSafeInteger(summary.unread) || summary.unread < 0) throw new Error('Invalid unread summary')
      update({ unread: summary.unread, known: true, failed: false })
    } catch {
      if (!disposed && run === generation) update({ unread: 0, known: false, failed: true })
    } finally {
      if (!disposed && run === generation) {
        busy = false
        controller = undefined
        update({ loading: false })
        if (queued) { queued = false; void refresh() }
      }
    }
  }

  function setContext(next: string | null): Promise<void> {
    if (disposed || next === context) return Promise.resolve()
    generation++
    controller?.abort()
    controller = undefined
    context = next
    busy = false
    queued = false
    update({ unread: 0, known: false, loading: false, failed: false })
    return refresh()
  }

  function dispose() {
    disposed = true
    generation++
    controller?.abort()
    controller = undefined
  }

  return { refresh, setContext, dispose }
}
