import { createVNode, render, type ObjectDirective } from 'vue'
import { createLoadingFeedback, loadingTimings } from './loading-feedback'
import LoadingState from '../components/base/LoadingState.vue'

interface Region {
  host: HTMLDivElement
  hidden: Map<HTMLElement, boolean>
  live: HTMLDivElement
  feedback: ReturnType<typeof createLoadingFeedback>
  busy: boolean
  priorBusy: string | null
  priorTab: string | null
}
const regions = new WeakMap<HTMLElement, Region>()

/** Protect stale data immediately; business requests and cancellation stay with the caller. */
function update(el: HTMLElement, busy: boolean) {
  const region = regions.get(el)!
  if (busy) {
    if (!region.busy) {
      region.priorBusy = el.getAttribute('aria-busy')
      region.priorTab = el.getAttribute('tabindex')
      el.style.setProperty('--db-loading-region-height', `${Math.max(el.getBoundingClientRect().height, el.dataset.loadingVariant === 'detail' ? 96 : 300)}px`)
      if (el.contains(document.activeElement)) { el.tabIndex = -1; el.focus() }
      const header = el.querySelector('thead')
      const headerHeight = header ? header.getBoundingClientRect().bottom - el.getBoundingClientRect().top : 0
      el.style.setProperty('--db-loading-header-height', `${Math.max(0, headerHeight)}px`)
      el.classList.add('db-loading-region')
      el.append(region.host)
      region.feedback.set(true)
      render(createVNode(LoadingState, {
        label: el.dataset.loadingLabel || '正在读取数据…',
        variant: el.dataset.loadingVariant || 'table',
      }), region.host)
    }
    el.setAttribute('aria-busy', 'true')
    for (const child of Array.from(el.children)) {
      if (child instanceof HTMLElement && child !== region.host && !region.hidden.has(child)) {
        region.hidden.set(child, child.inert)
        child.inert = true
      }
    }
  } else if (region.busy) {
    for (const [child, inert] of region.hidden) child.inert = inert
    region.hidden.clear()
    el.classList.remove('db-loading-region')
    el.style.removeProperty('--db-loading-region-height')
    el.style.removeProperty('--db-loading-header-height')
    if (region.priorBusy === null) el.removeAttribute('aria-busy')
    else el.setAttribute('aria-busy', 'false')
    if (region.priorTab === null) el.removeAttribute('tabindex')
    else el.setAttribute('tabindex', region.priorTab)
    render(null, region.host)
    region.host.remove()
    region.feedback.set(false)

  }
  region.busy = busy
}

export const dataLoading: ObjectDirective<HTMLElement, boolean> = {
  mounted(el, binding) {
    const host = document.createElement('div')
    host.className = 'db-loading-region-status'
    host.setAttribute('aria-hidden', 'true')
    const live = document.createElement('div')
    live.className = 'sr-only'
    live.setAttribute('role', 'status')
    live.setAttribute('aria-live', 'polite')
    const feedback = createLoadingFeedback(phase => {
      live.textContent = phase === 'visible' ? el.dataset.loadingLabel || '正在读取数据…' : phase === 'long' ? '仍在等待响应，可使用其他可用区域。' : ''
    }, ...loadingTimings())
    el.after(live)
    regions.set(el, { host, live, feedback, hidden: new Map(), busy: false, priorBusy: null, priorTab: null })
    update(el, !!binding.value)
  },
  updated(el, binding) { update(el, !!binding.value) },
  beforeUnmount(el) { update(el, false); regions.get(el)?.feedback.dispose(); regions.get(el)?.live.remove(); regions.delete(el) },
}
