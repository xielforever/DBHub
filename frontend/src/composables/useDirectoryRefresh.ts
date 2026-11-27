import type { Ref } from 'vue'

export function useDirectoryRefresh(
  refreshButton: Readonly<Ref<HTMLButtonElement | undefined>>,
  load: () => Promise<void>,
  isLoading: () => boolean,
) {
  function refresh() {
    if (!isLoading()) void load()
  }

  function retry(event: MouseEvent) {
    if (isLoading()) return
    if (document.activeElement === event.currentTarget) refreshButton.value?.focus()
    refresh()
  }

  return { refresh, retry }
}
