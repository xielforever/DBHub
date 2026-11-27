export type LoadingPhase = 'idle' | 'pending' | 'visible' | 'long'

/** Presentation only: ending a request never waits for an animation. */
export function createLoadingFeedback(
  publish: (phase: LoadingPhase) => void,
  delay = 150,
  longWait = 5000,
) {
  let timers: ReturnType<typeof setTimeout>[] = []
  let active = false
  function clear() { timers.forEach(clearTimeout); timers = [] }
  return {
    set(busy: boolean) {
      if (busy === active) return
      active = busy
      clear()
      publish(busy ? 'pending' : 'idle')
      if (busy) timers = [
        setTimeout(() => publish('visible'), delay),
        setTimeout(() => publish('long'), longWait),
      ]
    },
    dispose() { active = false; clear() },
  }
}

export function loadingTimings() {
  const style = getComputedStyle(document.documentElement)
  const read = (name: string, fallback: number) => {
    const value = parseFloat(style.getPropertyValue(name))
    return Number.isFinite(value) && value >= 0 ? value : fallback
  }
  return [read('--db-loading-indicator-delay', 150), read('--db-loading-long-wait', 5000)] as const
}
