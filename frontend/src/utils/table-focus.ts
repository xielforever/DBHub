export function revealTableAction(event: FocusEvent) {
  const region = event.currentTarget
  const action = event.target
  if (!(region instanceof HTMLElement) || !(action instanceof HTMLButtonElement)) return
  if (region.scrollWidth <= region.clientWidth) return

  const left = region.getBoundingClientRect().left + region.clientLeft
  const right = left + region.clientWidth
  const bounds = action.getBoundingClientRect()
  // The shared button outline extends 2px plus a 3px offset.
  const focusMargin = 5
  const overflowLeft = Math.min(0, bounds.left - left - focusMargin)
  const overflowRight = Math.max(0, bounds.right - right + focusMargin)
  if (overflowLeft || overflowRight) {
    region.scrollTo({ left: region.scrollLeft + overflowLeft + overflowRight, behavior: 'instant' })
  }
}

export function scrollTableByKeyboard(event: KeyboardEvent) {
  const region = event.currentTarget
  if (!(region instanceof HTMLElement) || region.scrollWidth <= region.clientWidth ||
      event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return

  event.preventDefault()
  region.scrollBy({ left: event.key === 'ArrowLeft' ? -40 : 40, behavior: 'instant' })
}
