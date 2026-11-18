import { computed, onScopeDispose, ref, shallowRef } from 'vue'
import type { ConnectionItem } from '../../api/datasource'

export interface QueryTarget { source: ConnectionItem; database: string }
export function targetKind(source: ConnectionItem) {
  return source.capabilities?.sql ? 'sql' : source.capabilities?.keys ? 'keys' : source.capabilities?.metadata ? 'metadata' : 'unavailable'
}
export function environmentLabel(source: ConnectionItem) {
  return ({ prod: '生产环境', test: '测试环境', dev: '开发环境' })[source.environment] || '环境未标注'
}
export function sameTarget(a: QueryTarget | null, b: QueryTarget | null) {
  return !!a && !!b && a.source.id === b.source.id && a.database === b.database
}

/** 草稿读取与正式目标隔离；每次确认复核权限候选，不执行查询。 */
export function useQueryTarget(deps: {
  sources: (signal: AbortSignal) => Promise<ConnectionItem[]>
  databases: (id: number, signal: AbortSignal) => Promise<string[]>
  context: () => string
  blocked: () => string
  navigate: (target: QueryTarget) => Promise<void>
}) {
  const current = shallowRef<QueryTarget | null>(null), draftSource = shallowRef<ConnectionItem | null>(null)
  const sources = shallowRef<ConnectionItem[]>([]), databases = ref<string[]>([]), draftDatabase = ref('')
  const open = ref(false), sourceLoading = ref(false), databaseLoading = ref(false), submitting = ref(false)
  const sourceError = ref(''), databaseError = ref(''), submitError = ref('')
  let alive = true, sourceRun = 0, databaseRun = 0, commitRun = 0, openRun = 0
  let sourceRequest: AbortController | undefined, databaseRequest: AbortController | undefined, commitRequest: AbortController | undefined
  const draft = computed<QueryTarget | null>(() => draftSource.value ? { source: draftSource.value, database: draftDatabase.value } : null)
  const valid = computed(() => !!draft.value && targetKind(draft.value.source) !== 'unavailable'
    && (targetKind(draft.value.source) === 'keys' || databases.value.includes(draft.value.database)))
  const disabledReason = computed(() => deps.blocked() || (submitting.value ? '正在核对查询目标…' : '')
    || (sourceLoading.value || databaseLoading.value ? '正在加载候选目标…' : '')
    || (sourceError.value || databaseError.value ? '请先重新加载候选目标' : '')
    || (!valid.value ? '请选择有效的数据源与数据库' : '')
    || (sameTarget(current.value, draft.value) ? '已是当前查询目标' : ''))

  function cancelDraft() {
    databaseRun++; databaseRequest?.abort(); databaseLoading.value = false
    draftSource.value = null; draftDatabase.value = ''; databases.value = []
    databaseError.value = ''; submitError.value = ''
  }
  function close() {
    if (submitting.value) return
    openRun++; sourceRun++; sourceRequest?.abort(); sourceLoading.value = false
    open.value = false; cancelDraft()
  }
  /** 仅根据成功读取的来源列表清理失效上下文，不把网络故障当作撤权。 */
  function reconcileCurrent(result: ConnectionItem[]) {
    const target = current.value
    if (!target) return
    const source = result.find(item => item.id === target.source.id)
    if (!source || targetKind(source) !== targetKind(target.source)
      || (targetKind(source) === 'keys' && source.database !== target.database)) current.value = null
  }
  async function loadSources() {
    if (submitting.value) return
    const run = ++sourceRun, context = deps.context()
    sourceRequest?.abort(); const request = sourceRequest = new AbortController()
    cancelDraft(); sources.value = []; sourceLoading.value = true; sourceError.value = ''
    try {
      const result = await deps.sources(request.signal)
      if (!alive || run !== sourceRun || context !== deps.context()) return
      sources.value = result
      reconcileCurrent(result)
    } catch (error) {
      if (alive && run === sourceRun && context === deps.context() && !request.signal.aborted) sourceError.value = error instanceof Error ? error.message : '数据源加载失败'
    } finally { if (alive && run === sourceRun) sourceLoading.value = false }
  }
  async function chooseSource(id: number, preferred = '') {
    if (submitting.value) return
    cancelDraft()
    const source = sources.value.find(item => item.id === id)
    if (!source) return
    draftSource.value = source
    if (targetKind(source) === 'keys') { draftDatabase.value = source.database; return }
    if (targetKind(source) === 'unavailable') { databaseError.value = source.capabilities?.reason || '该数据源没有可用的工作台能力'; return }
    const run = ++databaseRun, context = deps.context(), request = databaseRequest = new AbortController()
    databaseLoading.value = true
    try {
      const result = await deps.databases(id, request.signal)
      if (!alive || run !== databaseRun || context !== deps.context() || draftSource.value?.id !== id) return
      databases.value = [...new Set(result)]
      const candidate = preferred || source.database
      draftDatabase.value = databases.value.includes(candidate) ? candidate : ''
    } catch (error) {
      if (alive && run === databaseRun && context === deps.context() && !request.signal.aborted) databaseError.value = error instanceof Error ? error.message : '数据库加载失败'
    } finally { if (alive && run === databaseRun) databaseLoading.value = false }
  }
  function chooseDatabase(name: string) {
    if (submitting.value || databaseLoading.value || !databases.value.includes(name)) return
    draftDatabase.value = name; submitError.value = ''
  }
  async function show(sourceId?: number, database?: string) {
    if (submitting.value) return
    const run = ++openRun, context = deps.context()
    open.value = true
    if (!sources.value.length) await loadSources()
    if (!alive || !open.value || run !== openRun || context !== deps.context()) return
    const id = sourceId ?? current.value?.source.id
    if (id !== undefined) await chooseSource(id, database ?? (id === current.value?.source.id ? current.value.database : ''))
  }
  async function confirm() {
    if (disabledReason.value || !draft.value) return false
    const target = draft.value, context = deps.context(), run = ++commitRun
    const request = commitRequest = new AbortController()
    submitting.value = true; submitError.value = ''
    const fresh = () => alive && run === commitRun && context === deps.context() && open.value
    try {
      const result = await deps.sources(request.signal)
      if (!fresh()) return false
      reconcileCurrent(result)
      const source = result.find(item => item.id === target.source.id)
      if (!source || targetKind(source) !== targetKind(target.source)) throw new Error('数据源权限或能力已变化，请重新选择')
      const names = targetKind(source) === 'keys' ? [source.database] : await deps.databases(source.id, request.signal)
      if (!fresh()) return false
      if (!names.includes(target.database)) throw new Error('数据库已不可用，请重新选择')
      if (deps.blocked()) throw new Error(deps.blocked())
      const confirmed = { source, database: target.database }
      await deps.navigate(confirmed)
      if (!fresh()) return false
      current.value = confirmed; sources.value = result; open.value = false; cancelDraft()
      return true
    } catch (error) {
      if (fresh()) submitError.value = error instanceof Error ? error.message : '切换失败，请重新核对目标'
      return false
    } finally { if (alive && run === commitRun) submitting.value = false }
  }
  function invalidate() {
    openRun++; sourceRun++; databaseRun++; commitRun++
    sourceRequest?.abort(); databaseRequest?.abort(); commitRequest?.abort()
    open.value = false; current.value = null; sources.value = []; sourceError.value = ''
    sourceLoading.value = false; submitting.value = false; cancelDraft()
  }
  onScopeDispose(() => { alive = false; invalidate() })
  return { current, draft, draftSource, draftDatabase, sources, databases, open, sourceLoading, databaseLoading, submitting,
    sourceError, databaseError, submitError, disabledReason, loadSources, chooseSource, chooseDatabase, show, close, confirm, invalidate }
}
