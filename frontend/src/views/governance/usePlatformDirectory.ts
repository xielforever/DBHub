import { computed, nextTick, onScopeDispose, ref, shallowRef, type Ref } from 'vue'
import { platformDirectoryPageSize, type PlatformDirectoryPage, type PlatformDirectoryQuery } from '../../api/platform-management'

export function usePlatformDirectory<T, Query extends PlatformDirectoryQuery>(
  query: Ref<Query>,
  fetchPage: (query: Query, signal: AbortSignal) => Promise<PlatformDirectoryPage<T>>,
) {
  const items = shallowRef<T[]>([])
  const total = ref(0)
  const loading = ref(true)
  const error = ref('')
  const lastPage = computed(() => Math.max(1, Math.ceil(total.value / platformDirectoryPageSize)))
  let request: AbortController | undefined
  let alive = true

  async function load() {
    if (!alive) return
    request?.abort()
    const read = new AbortController()
    request = read
    loading.value = true
    error.value = ''
    items.value = []
    try {
      const result = await fetchPage(query.value, read.signal)
      if (read.signal.aborted) return
      total.value = result.total
      if (query.value.page > lastPage.value) {
        await changeQuery({ ...query.value, page: lastPage.value })
      } else {
        items.value = result.items
      }
    } catch (failure) {
      if (read.signal.aborted) return
      total.value = 0
      error.value = failure instanceof Error ? failure.message : '暂时无法读取目录，请重试。'
    } finally {
      if (!read.signal.aborted) loading.value = false
    }
  }

  async function changeQuery(next: Query) {
    query.value = next
    await nextTick()
    await load()
  }

  function goTo(page: number) {
    if (loading.value || page < 1 || page > lastPage.value || page === query.value.page) return
    void changeQuery({ ...query.value, page })
  }

  onScopeDispose(() => {
    alive = false
    request?.abort()
  })
  return { items, total, lastPage, loading, error, load, changeQuery, goTo }
}
