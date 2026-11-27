import { onScopeDispose, ref } from 'vue'

/** Read presentation only. Callers retain response ordering and authorization checks. */
export function useReadActivity() {
  const busy = ref(false)
  let pending = 0
  let alive = true
  onScopeDispose(() => { alive = false; busy.value = false })
  async function run<T>(read: () => Promise<T>): Promise<T> {
    pending++
    if (alive) busy.value = true
    try { return await read() }
    finally { pending--; if (alive) busy.value = pending > 0 }
  }
  return { busy, run }
}
