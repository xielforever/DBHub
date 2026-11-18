const buttonSelector = 'button, [role="button"], [role="tab"], [role="menuitem"], a.liquid-button, a.ghost-button, a.nav-item, .segmented-control > a'
const unavailableSelector = ':disabled, [disabled], [aria-disabled="true"], [aria-busy="true"], .is-disabled, .is-loading'
const feedbackAttribute = 'data-ui-click-feedback'

/** 旁听原生按钮激活，只更新短暂视觉状态，不改变点击、提交或防重复处理。 */
export function installButtonFeedback(root: Document = document): () => void {
  const view = root.defaultView
  if (!view) return () => {}

  const phases = new WeakMap<Element, 'a' | 'b'>()
  const frames = new Set<number>()
  const timers = new Map<Element, number>()
  let disposed = false

  function available(control: Element): boolean {
    return control.isConnected && !control.matches(unavailableSelector)
      && !control.closest('[aria-disabled="true"], .is-disabled, .is-loading, .monaco-editor')
  }

  function clearFeedback(control: Element): void {
    const timer = timers.get(control)
    if (timer !== undefined) view!.clearTimeout(timer)
    timers.delete(control)
    control.removeAttribute(feedbackAttribute)
  }

  function clearPending(): void {
    frames.forEach(frame => view!.cancelAnimationFrame(frame))
    frames.clear()
    Array.from(timers.keys()).forEach(clearFeedback)
  }

  function onClick(event: Event): void {
    if (disposed || root.hidden || !(event.target instanceof Element)) return
    const control = event.target.closest(buttonSelector)
    if (!control || !available(control)) return

    // 等当前点击处理及Vue状态更新完成，提交中/已移除的按钮不回放旧激活。
    const frame = view!.requestAnimationFrame(() => {
      frames.delete(frame)
      if (disposed || root.hidden || !available(control) || !control.getClientRects().length) return
      if (view!.matchMedia('(prefers-reduced-motion: reduce)').matches) return
      const value = view!.getComputedStyle(control).getPropertyValue('--db-motion-button-click').trim()
      const duration = Number.parseFloat(value) * (value.endsWith('ms') ? 1 : 1000)
      if (!Number.isFinite(duration) || duration <= 0) return

      clearFeedback(control)
      const phase = phases.get(control) === 'a' ? 'b' : 'a'
      phases.set(control, phase)
      control.setAttribute(feedbackAttribute, phase)
      // 兜底清理隐藏/移除节点上无法冒泡的动画结束事件，时长仍来自共享令牌。
      timers.set(control, view!.setTimeout(() => {
        if (control.getAttribute(feedbackAttribute) === phase) clearFeedback(control)
      }, duration + 80))
    })
    frames.add(frame)
  }

  function onAnimationDone(event: Event): void {
    if (!(event instanceof AnimationEvent) || !(event.target instanceof Element)) return
    const phase = event.target.getAttribute(feedbackAttribute)
    if (phase && event.animationName === `db-button-click-${phase}`) clearFeedback(event.target)
  }

  function onVisibilityChange(): void {
    if (root.hidden) clearPending()
  }

  root.addEventListener('click', onClick, { capture: true, passive: true })
  root.addEventListener('animationend', onAnimationDone, true)
  root.addEventListener('animationcancel', onAnimationDone, true)
  root.addEventListener('visibilitychange', onVisibilityChange)

  return () => {
    if (disposed) return
    disposed = true
    clearPending()
    root.removeEventListener('click', onClick, true)
    root.removeEventListener('animationend', onAnimationDone, true)
    root.removeEventListener('animationcancel', onAnimationDone, true)
    root.removeEventListener('visibilitychange', onVisibilityChange)
  }
}
