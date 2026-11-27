<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import { computed, markRaw, nextTick, onBeforeUnmount, onMounted, ref, shallowReactive, shallowRef, useId, watch, type ComponentPublicInstance } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import { CheckCircle2, ChevronDown, CircleAlert, FileCode2, LoaderCircle, MoreHorizontal, PanelBottomClose, PanelBottomOpen, PanelLeftClose, PanelLeftOpen, Play, Square, Table2 } from 'lucide-vue-next'
import { workbenchApi } from '../../api/workbench'
import { matchesScriptType, scriptTypes, workbenchScripts, type ScriptTarget, type ScriptType, type WorkbenchScript } from '../../api/workbench-scripts'
import { useUserStore } from '../../stores/user'
import WorkspaceCatalogPanel from '../../components/catalog/WorkspaceCatalogPanel.vue'
import SqlMonaco from './components/SqlMonaco.vue'
import ResultGrid from './components/ResultGrid.vue'
import { formatSql } from './formatSql'
import { useSqlExecution, type ExecutionState } from './useSqlExecution'
import { advancedApi, type QuerySettings } from '../../api/advanced'
import QuerySettingsDialog from './components/QuerySettingsDialog.vue'
import ColumnAuthPane from './components/ColumnAuthPane.vue'
import ExplainPlan from './components/ExplainPlan.vue'
import ResultChartPane from './components/ResultChartPane.vue'
import WorkbenchResizeHandle from './components/WorkbenchResizeHandle.vue'
import WorkbenchScriptDirectory from './components/WorkbenchScriptDirectory.vue'
import WorkbenchScriptTabs from './components/WorkbenchScriptTabs.vue'
import { scriptTabCloseKeys, scriptTabCloseLabels, type ScriptTabCloseAction } from './script-tab-close'
import ScriptSaveDialog from './components/ScriptSaveDialog.vue'
import ScriptTargetDialog from './components/ScriptTargetDialog.vue'
import { environmentLabel, type QueryTarget } from './useQueryTarget'
import { isExplainStatement } from './explain'
import type { ChartConfig } from '../../components/business/chart/ChartCard.vue'

const props = defineProps<{
  target: QueryTarget | null
  locked: boolean
  restoringDefault: boolean
  defaultError: string
  applyScriptTarget: (target: QueryTarget) => Promise<void>
  restoreScriptTarget: (script?: WorkbenchScript, previousTarget?: Omit<ScriptTarget, 'state'>, draftType?: ScriptType) => Promise<void>
}>()
const emit = defineEmits<{
  blocked: [reason: string]
  'choose-execution': [trigger?: HTMLElement]
}>()
const route = useRoute()
const userStore = useUserStore()
interface ScriptTab {
  key: number
  script?: WorkbenchScript
  draft?: { type: ScriptType; name: string; redisTarget?: Omit<ScriptTarget, 'state'> }
  sql: string
  savedSQL: string
  acceptedTarget: string
  target?: Omit<ScriptTarget, 'state'>
  targetDirty: boolean
  outcome?: ExecutionState
  error: string
  status: string
}
let lastTabKey = 0
let draftNumber = 0
function makeTab(script?: WorkbenchScript, sql = script?.content ?? ''): ScriptTab {
  return { key: ++lastTabKey, script, sql, savedSQL: script?.content ?? '', acceptedTarget: '', targetDirty: false, error: '', status: '' }
}
const activeTab = ref(makeTab(undefined, typeof route.query.sql === 'string' ? route.query.sql : ''))
const scriptTabs = ref([activeTab.value])
const editorPanelId = useId()
const tabBar = ref<InstanceType<typeof WorkbenchScriptTabs>>()
const scriptDirectory = ref<InstanceType<typeof WorkbenchScriptDirectory>>()
const editors = shallowReactive(new Map<number, InstanceType<typeof SqlMonaco>>())
const editor = computed(() => editors.get(activeTab.value.key))
function setEditor(key: number, instance: Element | ComponentPublicInstance | null) {
  if (instance) editors.set(key, instance as InstanceType<typeof SqlMonaco>)
  else editors.delete(key)
}
const sql = computed({ get: () => activeTab.value.sql, set: value => { activeTab.value.sql = value } })
const activeScript = computed({
  get: () => activeTab.value.script,
  set: value => { activeTab.value.script = value },
})
const scriptType = computed(() => activeScript.value?.script_type ?? activeTab.value.draft?.type)
const scriptDefaultTarget = computed(() => activeScript.value?.default_target ?? activeTab.value.draft?.redisTarget)
const tabKeys = computed(() => scriptTabs.value.map(tab => tab.key))
function tabName(tab: ScriptTab) {
  return tab.script?.name ?? tab.draft?.name ?? '未命名'
}
const resourceTab = ref<'scripts' | 'catalog'>('scripts')
const catalogVisited = ref(false)
const catalogPanelId = useId()
const canBrowseCatalog = computed(() => !!userStore.workspaceId && userStore.can('catalog.metadata'))
const scriptTargetOpen = ref(false)
const targetButton = ref<HTMLButtonElement>()
const editorActionsButton = ref<HTMLButtonElement>()
let scriptTargetTrigger: HTMLElement | undefined
const scriptSave = shallowRef<{
  initialName: string
  initialFolderId: number | null
  trigger?: HTMLElement
  save: (name: string, folderId: number | null) => Promise<void>
  resolve: (saved: boolean) => void
}>()
const savingScript = ref(false)
const scriptStatus = computed({ get: () => activeTab.value.status, set: value => { activeTab.value.status = value } })
const scriptError = computed({ get: () => activeTab.value.error, set: value => { activeTab.value.error = value } })
const closingTab = ref(false)
type TabCloseChoice = 'keep' | 'close' | 'save'
const tabClosePrompt = shallowRef<{
  name: string
  modified: boolean
  resolve: (choice: TabCloseChoice) => void
}>()
const keepTabButton = ref<HTMLButtonElement>()
const switchingTab = ref(false)
const directoryBusy = computed(() => scriptDirectory.value?.busy ?? false)
const dirty = computed(() => hasContentChanges(activeTab.value))
function hasContentChanges(tab: ScriptTab) {
  return !tab.script?.read_only && tab.sql !== tab.savedSQL
}
const connection = computed(() => props.target?.source)
const connectionId = computed(() => connection.value?.id)
const database = computed(() => props.target?.database ?? '')
const actualTargetKey = computed(() => JSON.stringify([connectionId.value, database.value, connection.value?.updated_at]))
const targetLabel = computed(() => connection.value ? `${connection.value.name} / ${database.value}` : '选择执行目标')
const scriptTargetIssue = computed(() => {
  const script = activeScript.value
  const type = scriptType.value
  if (!type) return props.defaultError
  if (script?.read_only) return '只读分享。复制或另存后，可使用自己的数据源。'
  if (type === 'redis') return 'Redis 脚本可以保存和分享文本。执行请使用 Redis 工作台的键操作。'
  if (props.restoringDefault) return '正在核对脚本默认连接…'
  if (!connection.value) return props.defaultError
  if (!matchesScriptType(connection.value, type)) return '当前查询目标与脚本类型不匹配。请选择匹配的数据源。'
  if (activeTab.value.acceptedTarget === actualTargetKey.value) return ''
  const target = script?.default_target
  if (target?.state === 'unavailable') return '脚本默认目标已不可用，请重新选择查询目标。'
  if (target && (target.connection_id !== connectionId.value || target.database !== database.value)) return '本次目标与脚本默认目标不同，请确认后再执行。'
  return ''
})
const selectedLength = computed(() => editor.value?.selectedLength ?? 0)
const formatting = ref(false)
const confirming = ref(false)
const formatError = ref('')
const desktopMedia = typeof window !== 'undefined' ? window.matchMedia('(min-width: 1024px)') : undefined
const desktop = ref(desktopMedia?.matches ?? false)
const showResources = ref(true)
const showResults = ref(true)
type WorkView = 'editor' | 'result'
type MobileView = WorkView | 'resources'
const mobileView = ref<MobileView>('editor')
const resourceReturn = ref<WorkView>('editor')
const resourceWidth = ref(272)
const editorHeight = ref(276)
const layout = ref<HTMLElement>()
const layoutHeight = ref(500)
const editorLimit = computed(() => Math.max(156, layoutHeight.value - 164))
const scriptOpenTrigger = ref<HTMLButtonElement>()
const scriptCloseTrigger = ref<HTMLButtonElement>()
const resultOpenTrigger = ref<HTMLButtonElement>()
const resultCloseTrigger = ref<HTMLButtonElement>()
const resultToolbarId = useId()
const mobileResultStatusId = useId()
let layoutObserver: ResizeObserver | undefined
const resultCategory = ref('result')
const chartType = ref<'bar' | 'line'>('bar')
const chartConfig = ref<ChartConfig>({})
const chartExpanded = ref(false)
const settingsOpen=ref(false),policiesOpen=ref(false),settings=ref<QuerySettings>(),settingsError=ref(''),settingsLoading=ref(true)
const policySupported = computed(() => ['postgres', 'mysql'].includes(connection.value?.template?.dialect || connection.value?.type || ''))
function settingsSaved(value:QuerySettings){settings.value=value;settingsError.value=''}
const sqlDialect = computed(() => connection.value?.template?.dialect || (connection.value?.type === 'mysql' || scriptType.value === 'mysql' ? 'mysql' : 'postgres'))
let disposed = false


const legacyRecoveryKey = `dbhub:pending-execution:${userStore.user?.id ?? 'anonymous'}`
const recoveryKey = `${legacyRecoveryKey}:${userStore.workspaceId}`
const legacyRequest = ref<string | null>(null)
const legacyError = ref('')
const legacyChecking = ref(false)
let legacyLookup: AbortController | undefined
try { legacyRequest.value = sessionStorage.getItem(legacyRecoveryKey) }
catch { /* 恢复存储不可用时沿用执行组件中的错误处理。 */ }
const { state: liveExecution, busy, needsReview, run, cancel, check, adoptReceipt, acknowledge, reset } = useSqlExecution((context, signal) =>
  workbenchApi.execute(context.connectionId, context.sql, context.database, signal, undefined, {
    request_id: context.requestId, script_id: context.scriptId, ...(settings.value ? {timeout_ms:settings.value.timeout_ms,max_rows:settings.value.max_rows} : {timeout_ms:120000}),
  }),
  (id, signal) => workbenchApi.execution(id, signal),
  {
    read: () => sessionStorage.getItem(recoveryKey),
    write: id => sessionStorage.setItem(recoveryKey, id),
    clear: () => sessionStorage.removeItem(recoveryKey),
  },
)
const state = computed(() => {
  const live = liveExecution.value
  if (live.status !== 'idle' || props.restoringDefault || switchingTab.value) return live
  const outcome = activeTab.value.outcome
  return outcome?.context && outcome.context.connectionId === connectionId.value && outcome.context.database === database.value
    ? outcome : live
})
const activityBlockedReason = computed(() => needsReview.value ? '请先核实上次执行结果' : busy.value || legacyChecking.value ? '执行、取消或核实处理中，暂不能切换目标' : confirming.value ? '请先完成执行确认' : scriptSave.value || savingScript.value ? '请先完成脚本保存' : settingsOpen.value || policiesOpen.value ? '请先关闭查询设置或访问策略' : formatting.value ? '正在格式化 SQL' : '')
const blockedReason = computed(() => activityBlockedReason.value || (closingTab.value ? '请先完成关闭脚本操作' : ''))
const runButton = ref<HTMLButtonElement>()
const cancelButton = ref<HTMLButtonElement>()
const resultCancelButton = ref<HTMLButtonElement>()
const checkButton = ref<HTMLButtonElement>()
const resultHeading = ref<HTMLElement>()
const editorHeading = ref<HTMLElement>()
watch(scriptTargetOpen, async open => {
  if (open) return
  await nextTick()
  const trigger = scriptTargetTrigger?.isConnected ? scriptTargetTrigger : targetButton.value
  trigger?.focus({ preventScroll: true })
  scriptTargetTrigger = undefined
})
onMounted(() => {
  desktopMedia?.addEventListener('change', updateDesktop)
  if (!layout.value) return
  layoutObserver = new ResizeObserver(() => {
    if (!desktop.value) return
    layoutHeight.value = layout.value?.clientHeight ?? 500
    editorHeight.value = Math.min(editorHeight.value, editorLimit.value)
  })
  layoutObserver.observe(layout.value)
})
onBeforeUnmount(() => {
  desktopMedia?.removeEventListener('change', updateDesktop)
  layoutObserver?.disconnect()
})
function updateDesktop(event: MediaQueryListEvent) {
  desktop.value = event.matches
  if (event.matches && mobileView.value === 'resources') selectMobileView(resourceReturn.value)
}
async function toggleScripts() {
  showResources.value = !showResources.value
  await nextTick()
  const next = showResources.value ? scriptCloseTrigger.value : scriptOpenTrigger.value
  next?.focus({ preventScroll: true })
}
async function toggleResults() {
  showResults.value = !showResults.value
  await nextTick()
  const next = showResults.value ? resultCloseTrigger.value : resultOpenTrigger.value
  next?.focus({ preventScroll: true })
}

function selectMobileView(view: MobileView) {
  const previous = mobileView.value
  if (view === 'resources' && previous !== 'resources') resourceReturn.value = previous
  if (view === 'editor') chartExpanded.value = false
  mobileView.value = view
}

async function focusWorkView(view: WorkView) {
  await nextTick()
  if (!desktop.value && mobileView.value === view) (view === 'editor' ? editorHeading.value : resultHeading.value)?.focus()
}

function returnFromResources() {
  const view = resourceReturn.value
  selectMobileView(view)
  void focusWorkView(view)
}

function showEditor() {
  selectMobileView('editor')
  void focusWorkView('editor')
}

watch(canBrowseCatalog, allowed => {
  if (allowed) return
  resourceTab.value = 'scripts'
  catalogVisited.value = false
})

watch(blockedReason, reason => emit('blocked', reason), { immediate: true, flush: 'sync' })
watch(busy, async isBusy => {
  if (isBusy || (document.activeElement !== cancelButton.value && document.activeElement !== resultCancelButton.value)) return
  await nextTick()
  if (disposed || document.activeElement !== document.body) return
  const target = needsReview.value ? checkButton.value : resultHeading.value
  target?.focus()
})
const canRequestRun = computed(() => !!sql.value.trim() && !activeScript.value?.read_only && scriptType.value !== 'redis'
  && !busy.value && !legacyChecking.value && !needsReview.value && !confirming.value && (!connection.value || !settingsLoading.value)
  && !settingsOpen.value && !policiesOpen.value && !formatting.value && !props.locked && !scriptTargetOpen.value && !scriptSave.value && !savingScript.value
  && !directoryBusy.value && !closingTab.value && !switchingTab.value)

const executionTarget = computed(() => {
  if (state.value.context) return `${state.value.context.connectionName} / ${state.value.context.database}`
  const receipt = state.value.receipt
  if (!receipt) return ''
  const name = connection.value?.id === receipt.connection_id ? connection.value.name : `连接 ${receipt.connection_id}`
  return `${name} / ${receipt.database || '数据库名称未记录'}`
})

const statusText = computed(() => ({
  idle: desktop.value ? '就绪' : '尚未运行', running: '执行中', canceling: '取消中', checking: '核实中', success: '成功', completed: '已完成', error: '失败', canceled: '已取消', unknown: '待确认',
})[state.value.status])
const statusClass = computed(() => ({
  idle: 'text-muted', running: 'text-accent', canceling: 'text-warning',
  checking: 'text-warning', success: 'text-success', completed: 'text-success', error: 'text-danger', canceled: 'text-warning', unknown: 'text-warning',
})[state.value.status])
const explain = computed(() => {
  const { status, context, result } = state.value
  return status === 'success' && result?.kind === 'query' && context && isExplainStatement(context.sql, context.dialect)
    ? { sql: context.sql, columns: result.columns ?? [], rows: result.rows ?? [] }
    : undefined
})
const queryResult = computed(() => state.value.status === 'success' && state.value.result?.kind === 'query' && !explain.value
  ? state.value.result : undefined)
const compactResults = computed(() => desktop.value && !!queryResult.value && !!state.value.context)
const resultTabs = computed(() => [
  { id: 'result', name: '结果' },
  ...(queryResult.value ? [{ id: 'chart', name: '图表' }] : []),
  { id: 'execution', name: '执行信息' },
])
watch(() => [state.value.requestId, state.value.status], () => { resultCategory.value = 'result' })
watch(queryResult, result => {
  const columns = result?.columns ?? []
  const metric = columns.find((_, index) => index > 0 && result?.rows?.some(row => typeof row[index] === 'number'))
  chartType.value = 'bar'
  chartConfig.value = { dimension: columns[0], metrics: metric ? [metric] : [] }
  chartExpanded.value = false
})
watch(resultCategory, category => { if (category !== 'chart') chartExpanded.value = false })

// 同类目标变化仅清理结果，保留SQL模型、选区和撤销历史。
watch([connectionId, database], () => { reset(); formatError.value = '' }, { flush: 'sync' })
function openSettings() { if (!tabsLocked.value) settingsOpen.value = true }
function openPolicies() { if (!tabsLocked.value && database.value) policiesOpen.value = true }
const scriptOperationLocked = computed(() => !!activityBlockedReason.value || !!props.locked || scriptTargetOpen.value || switchingTab.value)
const scriptLocked = computed(() => scriptOperationLocked.value || closingTab.value)
const tabsLocked = computed(() => scriptLocked.value || directoryBusy.value)
const pendingDefaultTarget = computed(() => {
  const target = props.target
  const script = activeScript.value
  if (scriptType.value === 'redis') return activeTab.value.draft?.redisTarget
  if (!target?.database || script?.read_only ||
    !matchesScriptType(target.source, scriptType.value ?? sqlDialect.value) ||
    (script && activeTab.value.acceptedTarget !== actualTargetKey.value)) return
  const saved = script?.default_target
  if (saved?.state !== 'unavailable' && saved?.connection_id === target.source.id && saved.database === target.database) return
  return { connection_id: target.source.id, database: target.database }
})
const unavailableTargetChange = computed(() => activeTab.value.targetDirty && !!props.defaultError && !connection.value)
const hasScriptChanges = computed(() => dirty.value || !!pendingDefaultTarget.value || unavailableTargetChange.value)
const canWriteScript = computed(() => !activeScript.value?.read_only &&
  (activeScript.value ? dirty.value || !!pendingDefaultTarget.value : !!activeTab.value.draft || !!sql.value.trim()))
const canSaveScript = computed(() => !tabsLocked.value && canWriteScript.value)
function tabHasChanges(tab: ScriptTab) {
  return tab.key === activeTab.value.key ? hasScriptChanges.value : hasContentChanges(tab) || tab.targetDirty
}
const tabLabels = computed(() => scriptTabs.value.map(tab => ({
  key: tab.key,
  name: tabName(tab),
  dirty: tabHasChanges(tab),
})))
const canFormat = computed(() => !!connection.value?.capabilities?.format && !!sql.value.trim() &&
  !tabsLocked.value && !activeScript.value?.read_only && scriptType.value !== 'redis')
const editorActions = {
  format: formatCurrent,
  save: () => saveScript(),
  duplicate: () => saveScript(true),
  copy: copySharedScript,
  current: () => closeScriptTabs(activeTab.value.key, 'current'),
  right: () => closeScriptTabs(activeTab.value.key, 'right'),
  others: () => closeScriptTabs(activeTab.value.key, 'others'),
  all: () => closeScriptTabs(activeTab.value.key, 'all'),
  settings: openSettings,
  policies: openPolicies,
}

async function activateTab(tab: ScriptTab, keepTarget = false) {
  const previous = activeTab.value
  if (tab.key === previous.key) return
  const currentTarget = props.target
  if (!props.restoringDefault) {
    if (currentTarget) previous.target = { connection_id: currentTarget.source.id, database: currentTarget.database }
    previous.targetDirty = !!pendingDefaultTarget.value || unavailableTargetChange.value
  }
  if (liveExecution.value.context) previous.outcome = markRaw(liveExecution.value)
  switchingTab.value = true
  activeTab.value = tab
  if (mobileView.value !== 'resources') selectMobileView('editor')
  let restore: Promise<void> | undefined
  if (keepTarget && currentTarget && tab.script && matchesScriptType(currentTarget.source, tab.script.script_type)) {
    activeTab.value.acceptedTarget = actualTargetKey.value
  } else {
    restore = props.restoreScriptTarget(tab.script, tab.target, tab.draft?.type)
  }
  reset()
  await nextTick()
  switchingTab.value = false
  if (restore) {
    await restore
    await nextTick()
  }
}

function selectScriptTab(key: number) {
  if (tabsLocked.value) return
  const tab = scriptTabs.value.find(item => item.key === key)
  if (tab) void activateTab(tab)
}

function addScriptTab(tab: ScriptTab) {
  const initial = activeTab.value
  if (!initial.script && !initial.draft && !initial.sql && !props.target && !initial.target && !initial.outcome) {
    scriptTabs.value = scriptTabs.value.filter(entry => entry.key !== initial.key)
  }
  scriptTabs.value.push(tab)
}

function openScript(item: WorkbenchScript, keepResources?: boolean, keepTarget = false) {
  let tab = scriptTabs.value.find(entry => entry.script?.id === item.id)
  if (!tab) {
    tab = makeTab(item)
    addScriptTab(tab)
  }
  void activateTab(tab, keepTarget)
  if (!keepResources && !desktop.value && mobileView.value === 'resources') showEditor()
}

function updateScript(item: WorkbenchScript) {
  const tab = scriptTabs.value.find(entry => entry.script?.id === item.id)
  if (!tab || item.version === undefined || item.version < (tab.script?.version ?? 0)) return
  const edited = hasContentChanges(tab)
  if (edited && tab.savedSQL !== item.content) return
  if (!edited) tab.sql = item.content
  tab.savedSQL = item.content
  tab.script = item
}

async function createScriptFromTabs(type: ScriptType) {
  if (tabsLocked.value || disposed) return
  const tab = makeTab()
  tab.draft = { type, name: `${scriptTypes[type]} 草稿 ${++draftNumber}` }
  addScriptTab(tab)
  await activateTab(tab)
  await tabBar.value?.focusActive()
}

async function saveScript(copy = false, duringClose = false) {
  if (disposed || scriptOperationLocked.value || directoryBusy.value || (closingTab.value && !duringClose)) return false
  const tab = activeTab.value
  const script = activeScript.value
  if (copy ? !script : !canWriteScript.value) return false
  scriptError.value = ''
  scriptStatus.value = ''
  if (copy || !script) return openScriptSave(script)
  if (script.version === undefined) {
    scriptError.value = '无法取得脚本版本。请保留当前修改，重新打开脚本后核对。'
    return false
  }
  const target = pendingDefaultTarget.value
  const changes = {
    ...(dirty.value ? { content: sql.value } : {}),
    ...(target ? { default_target: target } : {}),
  }
  savingScript.value = true
  try {
    const updated = await workbenchScripts.save(script.id, script.version, changes)
    if (disposed) return false
    tab.script = updated
    tab.savedSQL = updated.content
    if (target || !props.defaultError) tab.targetDirty = false
    tab.status = tab.sql === tab.savedSQL ? '脚本已保存。' : '已保存提交时的内容，当前还有未保存修改。'
    return true
  } catch (cause) {
    scriptError.value = cause instanceof Error ? cause.message : '保存脚本失败，请重试。'
    return false
  } finally { savingScript.value = false }
}

function onSaveShortcut(event: KeyboardEvent) {
  if (event.key.toLowerCase() !== 's' || !(event.ctrlKey || event.metaKey) || event.altKey || event.shiftKey || event.isComposing) return
  event.preventDefault()
  event.stopPropagation()
  if (!event.repeat) void saveScript()
}

function openScriptSave(script?: WorkbenchScript) {
  const tab = activeTab.value
  const draft = sql.value
  const kind = script?.script_type ?? tab.draft?.type ?? sqlDialect.value
  const original = script?.read_only ? undefined : script?.default_target
  const target = !script ? pendingDefaultTarget.value
    : original?.state === 'available' ? { connection_id: original.connection_id, database: original.database } : undefined
  const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
  return new Promise<boolean>(resolve => {
    scriptSave.value = {
      initialName: script ? `${script.name} 副本` : tab.draft?.name ?? '',
      initialFolderId: script?.read_only ? null : script?.folder_id ?? null,
      trigger,
      save: async (name, folderId) => {
        const content = script?.read_only ? (await workbenchScripts.get(script.id)).content : draft
        if (disposed) return
        const created = await workbenchScripts.create({
          name, script_type: kind, content, folder_id: folderId, default_target: target,
        })
        if (disposed) return
        if (script) openScript(created, false, !script.read_only)
        else {
          tab.script = created
          tab.draft = undefined
          tab.savedSQL = created.content
          tab.targetDirty = false
          tab.acceptedTarget = target ? actualTargetKey.value : ''
        }
        scriptStatus.value = '脚本已保存。'
      },
      resolve,
    }
  })
}

async function closeScriptSave(saved: boolean) {
  const operation = scriptSave.value
  if (!operation) return
  scriptSave.value = undefined
  operation.resolve(saved)
  if (disposed) return
  await nextTick()
  if (disposed || scriptSave.value || closingTab.value) return
  const trigger = operation.trigger
  const focus = trigger?.isConnected && trigger.getClientRects().length ? trigger : editorActionsButton.value
  if (focus?.getClientRects().length) focus.focus({ preventScroll: true })
}

async function prepareScriptSwitch() {
  return !disposed && !scriptLocked.value
}

function finishTabClosePrompt(choice: TabCloseChoice) {
  const prompt = tabClosePrompt.value
  tabClosePrompt.value = undefined
  prompt?.resolve(choice)
}

async function confirmTabClose(tab: ScriptTab) {
  if (disposed) return false
  const modified = tabHasChanges(tab)
  const restore = activateTab(tab)
  await nextTick()
  if (disposed) return false
  const choice = await new Promise<TabCloseChoice>(resolve => {
    tabClosePrompt.value = { name: tabName(tab), modified, resolve }
  })
  if (choice === 'keep' || disposed) return false
  if (choice === 'close') return true
  await restore
  if (disposed) return false
  if (unavailableTargetChange.value) {
    scriptError.value = '上次选择的执行目标无法恢复，请重新选择后保存。'
    return false
  }
  if (!tabHasChanges(tab)) return true
  return await saveScript(false, true) && !tabHasChanges(tab)
}

async function closeScriptTabs(key: number, action: ScriptTabCloseAction = 'current') {
  if (tabsLocked.value || disposed) return
  const keys = new Set(scriptTabCloseKeys(tabKeys.value, key, action))
  if (!keys.size) return
  const original = scriptTabs.value
  const activeIndex = original.findIndex(tab => tab.key === activeTab.value.key)
  const confirmations = original.filter(tab => keys.has(tab.key) && (tab.script || tabHasChanges(tab)))
  closingTab.value = true
  try {
    for (const tab of confirmations) {
      if (!await confirmTabClose(tab)) return
    }
    if (disposed) return
    const remaining = original.filter(tab => !keys.has(tab.key))
    const next = original.slice(activeIndex).find(tab => !keys.has(tab.key)) ?? remaining[remaining.length - 1] ?? makeTab()
    scriptTabs.value = remaining.length ? remaining : [next]
    await activateTab(next)
  } finally {
    closingTab.value = false
    if (!disposed) await tabBar.value?.focusActive()
  }
}
function chooseExecution(trigger?: HTMLElement) {
  if (tabsLocked.value || activeScript.value?.read_only) return
  if (!scriptType.value) { emit('choose-execution', trigger); return }
  scriptTargetTrigger = trigger
  scriptTargetOpen.value = true
}
async function applyScriptSelection(target: QueryTarget) {
  const tab = activeTab.value
  const script = activeScript.value
  const type = scriptType.value
  if (disposed || !type || script?.read_only || !matchesScriptType(target.source, type)) throw new Error('脚本或数据源已变化，请重新选择')
  if (type === 'redis') {
    const binding = { connection_id: target.source.id, database: target.database }
    if (tab.draft) {
      tab.draft.redisTarget = binding
      return
    }
    if (!script || script.version === undefined) throw new Error('脚本或数据源已变化，请重新选择')
    const saved = await workbenchScripts.save(script.id, script.version, { default_target: binding })
    if (disposed || activeScript.value?.id !== script.id) throw new Error('编辑上下文已切换，请重新打开脚本')
    activeScript.value = saved
    return
  }
  await props.applyScriptTarget(target)
  if (disposed || tab.key !== activeTab.value.key) throw new Error('编辑上下文已切换，请重新打开脚本')
  tab.acceptedTarget = JSON.stringify([target.source.id, target.database, target.source.updated_at])
  tab.status = ''
}
async function copySharedScript() {
  const tab = activeTab.value
  const script = activeScript.value
  if (!script?.read_only) return
  try {
    await navigator.clipboard.writeText((await workbenchScripts.get(script.id)).content)
    tab.status = '已复制脚本。'
  } catch (cause) {
    tab.error = cause instanceof Error ? cause.message : '复制失败，请重试。'
  }
}
async function runQuery() {
  if (!canRequestRun.value) return
  if (!connection.value || !database.value || scriptTargetIssue.value) {
    chooseExecution(document.activeElement instanceof HTMLElement ? document.activeElement : runButton.value)
    return
  }
  const startedFromButton = document.activeElement === runButton.value
  const selected = editor.value?.getSelection().trim() ?? ''
  const query = selected || sql.value.trim()
  if (!query) return
  showResults.value = true
  const context = {
    connectionId: connection.value.id,
    connectionName: connection.value.name,
    database: database.value,
    dialect: sqlDialect.value,
    sql: query,
    selected: !!selected,
    scriptId: activeScript.value?.id,
  }
  if (connection.value.environment === 'prod' && /\b(INSERT|UPDATE|DELETE|CREATE|DROP|TRUNCATE|ALTER|REPLACE|MERGE|GRANT|REVOKE|CALL|DO|COPY)\b/i.test(query)) {
    confirming.value = true
    try {
      await ElMessageBox.confirm(
        `将在生产环境 ${context.connectionName} / ${context.database} 执行写操作，请确认执行目标与 SQL。`,
        '生产环境执行确认',
        { type: 'warning', confirmButtonText: '确认执行', cancelButtonText: '取消' },
      )
    } catch { return } finally { confirming.value = false }
  }
  if (disposed) return
  activeTab.value.outcome = undefined
  selectMobileView('result')
  const execution = run(context)
  if (!desktop.value) void focusWorkView('result')
  await execution
  await nextTick()
  if (!disposed && startedFromButton && needsReview.value && document.activeElement === runButton.value) {
    checkButton.value?.focus()
  }
}

async function checkFromButton() {
  const startedFromButton = document.activeElement === checkButton.value
  await check()
  await nextTick()
  if (disposed || !startedFromButton || document.activeElement !== document.body) return
  const target = needsReview.value ? checkButton.value : resultHeading.value
  target?.focus()
}

async function checkLegacyReceipt(event: MouseEvent) {
  const id = legacyRequest.value
  if (!id || busy.value || needsReview.value || legacyChecking.value || props.locked) return
  const trigger = event.currentTarget
  const controller = new AbortController()
  legacyLookup = controller
  legacyChecking.value = true
  legacyError.value = ''
  const timeout = setTimeout(() => controller.abort(), 8000)
  try {
    const receipt = await workbenchApi.execution(id, controller.signal)
    if (disposed || controller.signal.aborted) return
    if (!adoptReceipt(id, receipt)) {
      legacyError.value = '未取得匹配的回执，旧编号已保留。'
      return
    }
    const restoreFocus = document.activeElement === trigger
    sessionStorage.removeItem(legacyRecoveryKey)
    legacyRequest.value = null
    await nextTick()
    if (!disposed && restoreFocus && document.activeElement === document.body) {
      const target = needsReview.value ? checkButton.value : resultHeading.value
      target?.focus()
    }
  } catch {
    if (!disposed) legacyError.value = '当前空间未能核实旧版回执。请确认原工作空间后重试。'
  } finally {
    clearTimeout(timeout)
    legacyLookup = undefined
    if (!disposed) legacyChecking.value = false
  }
}

async function acknowledgeOutcome() {
  if (!needsReview.value || confirming.value) return
  confirming.value = true
  try {
    await ElMessageBox.confirm(
      '请先通过目标数据库或审计记录核实上次执行结果。继续后可以发起新的执行，重复运行已提交的写操作可能再次修改数据。',
      '确认已核实执行结果',
      { type: 'warning', confirmButtonText: '我已核实，继续', cancelButtonText: '返回核实' },
    )
    if (!disposed) acknowledge()
  } catch { /* 保持待确认状态 */ } finally { confirming.value = false }
}

async function formatCurrent() {
  if (!canFormat.value) return
  const original = sql.value
  const targetId = connectionId.value
  const dialect = sqlDialect.value
  formatting.value = true
  formatError.value = ''
  try {
    const formatted = await formatSql(original, dialect)
    if (!disposed && sql.value === original && connectionId.value === targetId) sql.value = formatted
  } catch {
    if (!disposed && sql.value === original) formatError.value = '格式化失败，原 SQL 已保留。请检查语法与数据库类型。'
  } finally {
    if (!disposed) formatting.value = false
  }
}

watch(sql, () => {
  formatError.value = ''
  if (dirty.value) scriptStatus.value = ''
})

onMounted(() => {
  if (needsReview.value) void check()
  void advancedApi.settings().then(value=>{if(!disposed)settings.value=value}).catch(e=>{if(!disposed)settingsError.value=e instanceof Error?e.message:'读取查询限制失败'}).finally(()=>{if(!disposed)settingsLoading.value=false})
})

onBeforeUnmount(() => {
  legacyLookup?.abort()
  disposed = true
  finishTabClosePrompt('keep')
  void closeScriptSave(false)
  if (confirming.value) ElMessageBox.close()
})
</script>

<template>
  <div class="sql-workspace min-w-0" data-testid="sql-workbench" @keydown.capture="onSaveShortcut">
    <div v-if="!desktop" class="sql-mobile-views segmented-control" role="group" aria-label="工作台视图">
      <button type="button" class="ghost-button ui-control-target" aria-controls="sql-editor" :aria-pressed="mobileView === 'editor'" @click="selectMobileView('editor')">编辑</button>
      <button type="button" class="ghost-button ui-control-target" aria-controls="sql-results" :aria-pressed="mobileView === 'result'"
        :aria-describedby="busy || needsReview ? mobileResultStatusId : undefined" @click="selectMobileView('result')">
        结果<span v-if="busy || needsReview" :id="mobileResultStatusId" aria-hidden="true" class="ml-1 text-xs" :class="statusClass">{{ statusText }}</span>
      </button>
      <button type="button" class="ghost-button ui-control-target" aria-controls="sql-resources" :aria-pressed="mobileView === 'resources'" @click="selectMobileView('resources')">资源</button>
    </div>
    <div ref="layout" class="sql-layout" :class="{ 'sql-layout--resources': desktop && showResources }"
      :style="{ '--resource-width': resourceWidth + 'px', '--editor-height': editorHeight + 'px' }">
    <aside v-show="desktop ? showResources : mobileView === 'resources'" id="sql-resources"
      class="glass-panel min-h-0 min-w-0 overflow-hidden" :class="{ 'sql-mobile-resources': !desktop }"
      :aria-label="desktop ? '脚本目录' : '工作台资源'">
      <div v-if="!desktop" class="segmented-control shrink-0" role="group" aria-label="工作台资源分类">
        <button type="button" class="ghost-button ui-control-target" :aria-pressed="resourceTab === 'scripts'" @click="resourceTab = 'scripts'">脚本</button>
        <button v-if="canBrowseCatalog" type="button" class="ghost-button ui-control-target" :aria-pressed="resourceTab === 'catalog'"
          @click="resourceTab = 'catalog'; catalogVisited = true">数据目录</button>
      </div>
      <div v-show="desktop || resourceTab === 'scripts'" class="sql-script-pane h-full min-h-0">
        <WorkbenchScriptDirectory ref="scriptDirectory" desktop :touch="!desktop" :active="activeScript" :locked="scriptLocked"
          :visible="desktop ? showResources : mobileView === 'resources' && resourceTab === 'scripts'"
          :prepare="prepareScriptSwitch" @activate="openScript" @updated="updateScript">
          <template v-if="desktop" #toolbar>
            <button ref="scriptCloseTrigger" type="button" class="ghost-button ui-icon-button" aria-label="收起脚本区" title="收起脚本区"
              aria-controls="sql-resources" aria-expanded="true" @click="toggleScripts"><PanelLeftClose :size="16" aria-hidden="true" /></button>
          </template>
        </WorkbenchScriptDirectory>
      </div>
      <WorkspaceCatalogPanel v-if="!desktop && catalogVisited && canBrowseCatalog" v-show="resourceTab === 'catalog'"
        :id="catalogPanelId" :pinned="false" inline @close="returnFromResources" />
      <div v-if="!desktop" class="shrink-0 border-t border-stroke p-2">
        <button type="button" class="ghost-button ui-control-target" @click="returnFromResources">{{ resourceReturn === 'editor' ? '返回编辑' : '返回结果' }}</button>
      </div>
    </aside>
    <WorkbenchResizeHandle v-if="desktop && showResources" v-model="resourceWidth" direction="vertical" label="调整脚本区宽度" :min="200" :max="360" />
    <div v-show="desktop || mobileView !== 'resources'" class="sql-workbench min-w-0" :class="{ 'sql-workbench--results-hidden': !showResults, 'sql-workbench--chart-expanded': chartExpanded }">
    <section v-show="desktop ? !chartExpanded : mobileView === 'editor'" id="sql-editor"
      class="sql-editor-surface glass-panel flex min-h-0 flex-col overflow-hidden" aria-labelledby="sql-editor-title">
      <WorkbenchScriptTabs ref="tabBar" :tabs="tabLabels" :active-key="activeTab.key" :panel-id="editorPanelId"
        :desktop="desktop" :locked="tabsLocked" @activate="selectScriptTab" @close="closeScriptTabs" @create="createScriptFromTabs" />
      <header data-catalog-toolbar class="sql-editor-header flex flex-wrap shrink-0 items-center gap-x-2 gap-y-1 border-b border-stroke px-3 py-1.5">
        <button v-if="desktop && !showResources" ref="scriptOpenTrigger" type="button" class="ghost-button ui-icon-button" aria-label="展开脚本区" title="展开脚本区"
          aria-controls="sql-resources" aria-expanded="false" @click="toggleScripts"><PanelLeftOpen :size="16" aria-hidden="true" /></button>
        <FileCode2 v-if="!scriptType" class="h-4 w-4 shrink-0 text-accent" aria-hidden="true" />
        <h2 id="sql-editor-title" ref="editorHeading" tabindex="-1" class="min-w-0 max-w-40 truncate ui-focus-target" :title="tabName(activeTab)">
          <template v-if="scriptType">
            <span class="sr-only">{{ tabName(activeTab) }}，</span>
            <span class="text-xs text-muted">{{ scriptTypes[scriptType] }} · {{ activeScript?.read_only ? '只读' : !activeScript || hasScriptChanges ? '未保存' : '已保存' }}</span>
          </template>
          <span v-else class="text-sm text-content">SQL 编辑器</span>
        </h2>
        <div class="flex-1" />
        <button v-if="desktop && !showResults" ref="resultOpenTrigger" type="button" class="ghost-button ui-icon-button" aria-label="展开结果区" title="展开结果区"
          aria-controls="sql-results" aria-expanded="false" @click="toggleResults"><PanelBottomOpen :size="16" aria-hidden="true" /></button>
        <div v-if="!activeScript?.read_only" class="sql-execution-target flex min-w-0 items-center gap-1.5">
          <button ref="targetButton" type="button" class="ghost-button query-target-trigger ui-control-target text-xs"
            :title="scriptType === 'redis' ? '关联固定逻辑库' : targetLabel" aria-haspopup="dialog" :disabled="tabsLocked"
            @click="chooseExecution($event.currentTarget as HTMLElement)">
            <span class="truncate">{{ scriptType === 'redis' ? scriptDefaultTarget ? `默认逻辑库 ${scriptDefaultTarget.database}` : '关联固定逻辑库' : targetLabel }}</span>
            <ChevronDown :size="16" class="shrink-0" aria-hidden="true" />
          </button>
          <span v-if="connection" class="shrink-0 text-xs" :class="connection.environment === 'prod' ? 'text-danger' : 'text-muted'">{{ environmentLabel(connection) }}</span>
        </div>
        <el-dropdown trigger="click" placement="bottom-end" :disabled="tabsLocked" @command="(action: keyof typeof editorActions) => editorActions[action]()">
          <button ref="editorActionsButton" type="button" class="ghost-button ui-control-target !px-2.5" aria-label="更多操作" :disabled="tabsLocked">
            <MoreHorizontal :size="18" aria-hidden="true" />
          </button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item v-if="!activeScript?.read_only" command="save" :disabled="!canSaveScript"
                aria-keyshortcuts="Control+S Meta+S">保存脚本</el-dropdown-item>
              <el-dropdown-item v-if="activeScript?.read_only" command="copy">复制脚本</el-dropdown-item>
              <el-dropdown-item v-if="activeScript" command="duplicate">另存为脚本</el-dropdown-item>
              <el-dropdown-item v-for="(label, action) in scriptTabCloseLabels" :key="action" :command="action"
                :disabled="!scriptTabCloseKeys(tabKeys, activeTab.key, action).length">{{ label }}</el-dropdown-item>
              <el-dropdown-item v-if="!activeScript?.read_only && scriptType !== 'redis'" command="format" :disabled="!canFormat">格式化</el-dropdown-item>
              <el-dropdown-item command="settings">查询限制</el-dropdown-item>
              <el-dropdown-item v-if="policySupported" command="policies">{{ userStore.hasCapability('datasource_manage') ? '行列访问策略' : '查看访问策略' }}</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <button v-if="desktop && !activeScript?.read_only" type="button" class="ghost-button ui-control-target text-xs"
          aria-label="保存脚本" aria-keyshortcuts="Control+S Meta+S" :disabled="!canSaveScript"
          :aria-busy="savingScript" @click="saveScript()">{{ savingScript ? '保存中…' : '保存' }}</button>
        <button
          v-if="busy && state.status !== 'checking'"
          ref="cancelButton"
          type="button"
          class="ghost-button flex items-center gap-1.5 !px-3 !py-2 text-xs aria-disabled:opacity-50 ui-control-target"
          :aria-disabled="state.status === 'canceling' || undefined"
          @click="cancel"
        >
          <Square class="h-3.5 w-3.5" aria-hidden="true" />{{ state.status === 'canceling' ? '取消中…' : '取消' }}
        </button>
        <button
          v-if="!activeScript?.read_only && scriptType !== 'redis'"
          ref="runButton"
          class="liquid-button flex items-center gap-1.5 !px-3 !py-2 text-xs ui-control-target"
          :disabled="!canRequestRun && !busy && !needsReview"
          :aria-disabled="busy || needsReview || undefined"
          aria-keyshortcuts="Control+Enter Meta+Enter"
          @click="runQuery"
        >
          <LoaderCircle v-if="busy" class="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
          <Play v-else class="h-3.5 w-3.5" aria-hidden="true" />
          {{ busy ? '执行中' : '运行' }}
        </button>
      </header>
      <p v-if="scriptStatus" class="px-3 py-1 text-xs text-muted" role="status">{{ scriptStatus }}</p>
      <p v-if="settingsError" class="px-3 py-1 text-xs text-warning" role="alert">{{ settingsError }}；查询将使用服务端保存的限制。</p>
      <p v-if="scriptTargetIssue" class="shrink-0 px-3 py-1 text-xs text-muted" role="status">{{ scriptTargetIssue }}</p>
      <p v-if="scriptError" class="shrink-0 px-3 py-1 text-xs text-danger" role="alert">{{ scriptError }}</p>
      <div :id="editorPanelId" role="tabpanel" :aria-labelledby="`${editorPanelId}-tab-${activeTab.key}`" class="min-h-0 flex-1">
        <SqlMonaco v-for="tab in scriptTabs" v-show="tab.key === activeTab.key" :key="tab.key"
          :ref="instance => setEditor(tab.key, instance)" v-model="tab.sql" :read-only="tab.script?.read_only"
          :language="(tab.script?.script_type ?? tab.draft?.type) === 'redis' ? 'plaintext' : 'sql'" @run="runQuery" @format="formatCurrent" />
      </div>
      <p
        v-if="formatError"
        class="shrink-0 border-t border-stroke px-4 py-2 text-xs text-warning"
        role="alert"
      >{{ formatError }}</p>
      <footer v-if="scriptType !== 'redis'" class="flex flex-wrap shrink-0 items-center justify-between gap-2 border-t border-stroke px-4 py-1.5 text-xs text-muted">
        <span>每次执行一条 SQL</span>
        <span>{{ selectedLength ? '已选 ' + selectedLength + ' 字符' : '未选中时执行全部内容' }}</span>
      </footer>
    </section>

    <WorkbenchResizeHandle v-if="showResults && !chartExpanded" v-model="editorHeight" class="!hidden lg:!grid" direction="horizontal" label="调整编辑区与结果区高度" :min="156" :max="editorLimit" />
    <section
      v-show="desktop ? showResults : mobileView === 'result'"
      id="sql-results"
      class="glass-panel flex min-h-0 flex-col overflow-hidden"
      :class="{ 'sql-result-surface--data': queryResult && (queryResult.rows?.length || resultCategory !== 'result') || explain }"
      aria-labelledby="sql-result-title"
      data-testid="query-result"
    >
      <header :class="compactResults ? 'sr-only' : 'flex shrink-0 items-center gap-3 border-b border-stroke px-4 py-3'">
        <Table2 class="h-4 w-4 shrink-0 text-accent" aria-hidden="true" />
        <h2 id="sql-result-title" ref="resultHeading" tabindex="-1" class="shrink-0 text-sm text-content ui-focus-target">结果</h2>
        <span v-if="!compactResults && executionTarget" class="min-w-0 truncate text-xs text-muted" :title="executionTarget">
          {{ executionTarget }}
        </span>
        <span class="flex-1" />
        <span v-if="!compactResults" class="shrink-0 text-xs" :class="statusClass" role="status">
          <span v-if="state.status === 'unknown'" class="sr-only">执行结果</span><span :aria-hidden="state.status === 'error' || undefined">{{ statusText }}</span>
        </span>
        <button v-if="!desktop && busy && state.status !== 'checking'" ref="resultCancelButton" type="button"
          class="ghost-button ui-control-target shrink-0 text-xs aria-disabled:opacity-50"
          :aria-disabled="state.status === 'canceling' || undefined" @click="cancel">
          {{ state.status === 'canceling' ? '取消中…' : '取消查询' }}
        </button>
        <button v-if="desktop && !compactResults" ref="resultCloseTrigger" type="button" class="ghost-button ui-control-target inline-flex items-center gap-1 text-xs"
          aria-controls="sql-results" aria-expanded="true" @click="toggleResults"><PanelBottomClose :size="16" aria-hidden="true" />收起结果区</button>
      </header>
      <div v-if="state.context && !compactResults" class="shrink-0 truncate border-b border-stroke bg-surface-muted px-4 py-1.5 font-mono text-xs text-muted" :title="state.context.sql">
        {{ state.context.selected ? '选中执行' : '执行 SQL' }}：{{ state.context.sql.replace(/\s+/g, ' ') }}
      </div>
      <p v-if="state.requestId && ['unknown', 'checking', 'completed'].includes(state.status)" class="shrink-0 break-all border-b border-stroke px-4 py-1.5 text-xs text-muted">
        执行编号：<span class="select-text font-mono">{{ state.requestId }}</span>
      </p>
      <div v-if="legacyRequest && !needsReview" class="shrink-0 space-y-2 border-b border-stroke px-4 py-2 text-xs">
        <p class="text-muted">有一条旧版执行记录。确认已切换到原工作空间后，可在此核实。</p>
        <button type="button" class="ghost-button ui-control-target"
          :disabled="busy || locked" :aria-disabled="legacyChecking || undefined" @click="checkLegacyReceipt">
          {{ legacyChecking ? '正在核实旧版回执…' : '在当前空间核实旧版回执' }}
        </button>
        <p v-if="legacyError" role="alert" class="text-warning">{{ legacyError }}</p>
      </div>

      <ExplainPlan v-if="explain" :key="state.requestId ?? ''" v-bind="explain" />
      <div v-else-if="queryResult && state.context" class="sql-result-tabs flex min-h-0 flex-1 flex-col">
        <PageCategoryTabs label="SQL 结果分类" :categories="resultTabs" :active-id="resultCategory" @select="resultCategory = $event">
          <template v-if="compactResults" #actions>
            <div class="ml-auto flex items-center gap-2">
              <span class="text-xs" :class="statusClass" role="status">{{ statusText }}</span>
              <div :id="resultToolbarId" class="flex items-center gap-2" />
              <button ref="resultCloseTrigger" type="button" class="ghost-button ui-control-target inline-flex items-center gap-1 text-xs" :disabled="chartExpanded"
                aria-controls="sql-results" aria-expanded="true" @click="toggleResults"><PanelBottomClose :size="16" aria-hidden="true" />收起结果区</button>
            </div>
          </template>
          <template v-if="resultCategory === 'result' || resultCategory === 'chart'">
            <ResultChartPane v-model:chart-type="chartType" v-model:config="chartConfig" :result="queryResult"
              :table="resultCategory === 'result'" :expanded="chartExpanded" :sql="state.context.sql"
              :toolbar-target="compactResults ? '#' + resultToolbarId : undefined"
              :connection-id="state.context.connectionId" :database="state.context.database" @expand="chartExpanded = !chartExpanded" />
            <template v-if="resultCategory === 'result'">
              <ResultGrid v-if="desktop || queryResult.rows?.length" :columns="queryResult.columns ?? []" :rows="queryResult.rows ?? []" />
              <p v-else class="px-4 py-3 text-sm text-muted" role="status">查询完成，返回 0 行</p>
            </template>
          </template>
          <dl v-else class="min-h-0 overflow-auto grid gap-2 p-4 text-sm sm:grid-cols-2">
            <div><dt class="text-muted">执行目标</dt><dd class="break-all">{{ executionTarget }}</dd></div>
            <div><dt class="text-muted">执行耗时</dt><dd>{{ queryResult.duration_ms }} ms</dd></div>
            <div><dt class="text-muted">执行编号</dt><dd class="break-all">{{ state.requestId }}</dd></div>
            <div><dt class="text-muted">返回行数</dt><dd>{{ queryResult.rows?.length ?? 0 }}<span v-if="queryResult.truncated">（结果已截断）</span></dd></div>
            <div v-if="compactResults" class="sm:col-span-2"><dt class="text-muted">{{ state.context.selected ? '选中执行' : '执行 SQL' }}</dt><dd><pre class="whitespace-pre-wrap break-words font-mono">{{ state.context.sql }}</pre></dd></div>
          </dl>
        </PageCategoryTabs>
      </div>
      <div v-else-if="state.status === 'error'" class="min-h-0 flex-1 overflow-auto p-4" role="alert">
        <p class="mb-2 flex items-center gap-2 text-sm text-danger"><CircleAlert class="h-4 w-4" aria-hidden="true" />执行失败</p>
        <pre class="whitespace-pre-wrap break-words font-mono text-xs leading-6 text-danger">{{ state.error }}</pre>
        <button v-if="!desktop" type="button" class="ghost-button ui-control-target mt-3" @click="showEditor">返回编辑</button>
      </div>
      <div v-else class="sql-result-message flex min-h-0 flex-1 flex-col items-center justify-center gap-3 overflow-auto px-5 py-6 text-center" :aria-busy="busy">
        <template v-if="state.status === 'success' && state.result?.kind === 'write'">
          <CheckCircle2 class="h-7 w-7 text-success" aria-hidden="true" />
          <p class="text-sm text-content">执行成功，影响 {{ state.result.affected_rows ?? 0 }} 行</p>
        </template>
        <template v-else-if="busy">
          <LoadingState variant="detail" :announce="false" :label="state.status === 'checking' ? '正在核实执行状态…' : state.status === 'canceling' ? '正在取消请求并核实执行状态…' : '正在执行 SQL…'" />
        </template>
        <template v-else-if="state.status === 'unknown'">
          <CircleAlert class="h-7 w-7 shrink-0 text-warning" aria-hidden="true" />
          <p class="text-sm text-warning">执行结果待确认</p>
          <p class="max-w-xl text-xs leading-5 text-muted">{{ state.error }}</p>
          <div class="flex flex-wrap justify-center gap-2">
            <button ref="checkButton" class="ghost-button !px-3 !py-2 text-xs ui-control-target" @click="checkFromButton">核实状态</button>
            <button class="ghost-button !px-3 !py-2 text-xs ui-control-target" @click="acknowledgeOutcome">已核实，继续</button>
          </div>
        </template>
        <template v-else-if="state.status === 'completed'">
          <CheckCircle2 class="h-7 w-7 text-success" aria-hidden="true" />
          <p class="text-sm text-content">服务端已确认执行完成</p>
          <p class="max-w-xl text-xs leading-5 text-muted">{{ state.error }}</p>
        </template>
        <template v-else-if="state.status === 'canceled'">
          <Square class="h-6 w-6 text-warning" aria-hidden="true" />
          <p class="text-sm text-muted">请求已取消</p>
          <p class="text-xs text-muted">{{ state.error }}</p>
        </template>
        <template v-else>
          <Table2 class="h-7 w-7 text-subtle" aria-hidden="true" />
          <p class="text-sm text-muted">{{ desktop ? '查询结果将在这里显示' : '当前没有查询结果' }}</p>
          <p class="text-xs text-muted">确认查询目标，输入 SQL 后运行。</p>
          <button v-if="!desktop" type="button" class="ghost-button ui-control-target" @click="showEditor">返回编辑</button>
        </template>
      </div>

      <footer v-if="state.result" class="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-t border-stroke px-4 py-2 text-xs text-muted">
        <span>{{ state.result.kind === 'query' ? '返回' : '影响' }} {{ state.result.kind === 'query' ? (state.result.rows?.length ?? 0) : (state.result.affected_rows ?? 0) }} 行</span>
        <span>耗时 {{ state.result.duration_ms }} ms</span>
        <span v-if="state.result.truncated" class="text-warning">结果已截断，仅显示前 {{ state.result.rows?.length ?? 0 }} 行</span>
      </footer>
    </section>

    </div>
    <ScriptTargetDialog v-if="scriptTargetOpen && scriptType" :tab-key="activeTab.key" :script-type="scriptType" :default-target="scriptDefaultTarget" :actual="target"
      :apply="applyScriptSelection" @close="scriptTargetOpen = false" />
    </div>
    <ScriptSaveDialog v-if="scriptSave" :initial-name="scriptSave.initialName" :initial-folder-id="scriptSave.initialFolderId"
      :save="scriptSave.save" @close="closeScriptSave" />
    <el-dialog v-if="tabClosePrompt" :model-value="true" append-to-body title="关闭标签页确认"
      width="min(460px, calc(100vw - 24px))" class="glass-dialog" :close-on-click-modal="false"
      @opened="keepTabButton?.focus({ preventScroll: true })" @close="finishTabClosePrompt('keep')">
      <p class="script-close-risk">
        <CircleAlert :size="20" class="shrink-0" aria-hidden="true" />
        <span v-if="tabClosePrompt.modified">「{{ tabClosePrompt.name }}」有未保存的修改。放弃并关闭会丢失本次修改。</span>
        <span v-else>将关闭「{{ tabClosePrompt.name }}」的标签页。之后可以从脚本目录重新打开。</span>
      </p>
      <template #footer>
        <div class="flex flex-wrap justify-end gap-2">
          <button ref="keepTabButton" type="button" class="ghost-button ui-control-target" @click="finishTabClosePrompt('keep')">继续编辑</button>
          <button type="button" class="ghost-button ui-control-target script-close-danger" @click="finishTabClosePrompt('close')">
            {{ tabClosePrompt.modified ? '放弃并关闭' : '关闭标签页' }}
          </button>
          <button v-if="tabClosePrompt.modified" type="button" class="liquid-button ui-control-target"
            @click="finishTabClosePrompt('save')">保存并关闭</button>
        </div>
      </template>
    </el-dialog>
    <QuerySettingsDialog v-if="settingsOpen" @close="settingsOpen=false" @saved="settingsSaved" />
    <el-dialog v-if="policiesOpen && connection" :model-value="true" title="行列访问策略" width="min(840px, 94vw)" :close-on-click-modal="false" @close="policiesOpen=false">
      <ColumnAuthPane :connection-id="connection.id" :database="database" :editable="connection.user_id===userStore.user?.id" />
    </el-dialog>
  </div>
</template>



<style scoped>
.script-close-risk { display: flex; align-items: flex-start; gap: 9px; padding: 12px; border-radius: var(--db-radius-control); color: var(--db-danger); background: var(--db-danger-fill); overflow-wrap: anywhere; }
.script-close-danger { color: var(--db-danger); }
.sql-layout { position: relative; display: grid; grid-template-columns: minmax(0, 1fr); min-height: 0; }
@media (min-width: 1024px) {
  .sql-workspace { display: flex; flex-direction: column; flex: 1; min-height: 420px; }
  .sql-layout { flex: 1; grid-template-rows: minmax(0, 1fr); }
  .sql-layout--resources { grid-template-columns: var(--resource-width) 8px minmax(0, 1fr); }
  .sql-workbench { position: relative; display: grid; min-height: 0; grid-template-rows: var(--editor-height) 8px minmax(0, 1fr); }
  .sql-workbench--results-hidden, .sql-workbench--chart-expanded { grid-template-rows: minmax(0, 1fr); }
  .sql-execution-target { max-width: 44%; }
}
.sql-result-tabs :deep([role='tabpanel']:not([hidden])) { display: flex; flex: 1; min-height: 0; flex-direction: column; }
@media (max-width: 1023px) {
  .sql-mobile-views {
    display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); margin-bottom: 8px;
    border: 1px solid var(--db-border); border-radius: var(--db-radius-control); background: var(--db-surface-solid);
  }
  .sql-mobile-views button[aria-pressed='true'] { background: var(--db-selected); }
  .sql-mobile-resources { display: flex; flex-direction: column; height: clamp(360px, calc(100dvh - 13rem), 680px); }
  .sql-script-pane { height: auto; flex: 1; }
  .sql-editor-surface, .sql-result-surface--data { height: clamp(360px, calc(100dvh - 13rem), 680px); }
  .sql-result-message { align-items: flex-start; justify-content: flex-start; text-align: left; }
  .sql-execution-target { order: 1; flex-basis: 100%; }
  .sql-execution-target .query-target-trigger { flex: 0 1 auto; }
  .sql-editor-header h2 { max-width: min(10rem, calc(100% - 10rem)); }
}
@media (prefers-reduced-motion: reduce) {
  .animate-spin { animation: none; }
}
</style>
