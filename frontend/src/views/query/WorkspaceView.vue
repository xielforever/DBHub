<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, reactive, ref, watch } from 'vue'
import { isNavigationFailure, NavigationFailureType, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { ArrowLeft, ChevronDown } from 'lucide-vue-next'
import PageHeader from '../../components/base/PageHeader.vue'
import { connectionApi } from '../../api/datasource'
import { workbenchApi } from '../../api/workbench'
import { matchesScriptType, type ScriptTarget, type ScriptType, type WorkbenchScript } from '../../api/workbench-scripts'
import { useUserStore } from '../../stores/user'
import SqlWorkspace from './SqlWorkspace.vue'
import MetadataWorkspace from './MetadataWorkspace.vue'
import RedisWorkspace from './RedisWorkspace.vue'
import QueryTargetPicker from './components/QueryTargetPicker.vue'
import { environmentLabel, targetKind, useQueryTarget, type QueryTarget } from './useQueryTarget'

type BrowseKind = 'keys' | 'metadata'
const browseLabels = { keys: 'Redis 键', metadata: '元数据' }
const route = useRoute(), router = useRouter(), user = useUserStore()
const executionBlocked = ref(''), browserBlocked = ref(''), securityGeneration = ref(0)
const routeTargetError = ref('')
const restoringDefault = ref(false), defaultError = ref('')
const tool = ref<'editor' | 'keys' | 'metadata'>('editor')
const browseKind = ref<BrowseKind>('keys')
const browseTargets = ref<Partial<Record<BrowseKind, QueryTarget>>>({})
const toolTarget = computed(() => tool.value === 'editor' ? undefined : browseTargets.value[tool.value])
const context = computed(() => `${user.user?.id ?? ''}:${user.identityEpoch()}:${user.permissionKey}`)
let ownNavigation = false
let restoreRequest: AbortController | undefined
let routeRequest: AbortController | undefined
let pickerTrigger: HTMLElement | undefined

function readSources(signal: AbortSignal) {
  return connectionApi.list(undefined, { signal, silentError: true }).then(result => result.items)
}
function readDatabases(id: number, signal: AbortSignal) {
  return workbenchApi.databases(id, signal, true).then(result => result.items.map(item => item.name))
}
async function navigate(target: QueryTarget) {
  ownNavigation = true
  try {
    const failure = await router.replace({ path: '/query', query: { ...route.query, connection: String(target.source.id), database: target.database } })
    if (isNavigationFailure(failure) && !isNavigationFailure(failure, NavigationFailureType.duplicated)) throw new Error('目标地址切换未完成，请重试')
    routeTargetError.value = ''
  } finally { ownNavigation = false }
}
const execution = useQueryTarget({
  sources: async signal => (await readSources(signal)).filter(source => targetKind(source) === 'sql'),
  databases: readDatabases,
  context: () => context.value,
  blocked: () => executionBlocked.value,
  navigate,
})
const browser = useQueryTarget({
  sources: async signal => (await readSources(signal)).filter(source => targetKind(source) === browseKind.value),
  databases: readDatabases,
  context: () => context.value + ':' + browseKind.value,
  blocked: () => browserBlocked.value,
  navigate: async () => { routeTargetError.value = '' },
})
const executionPicker = reactive(execution), browserPicker = reactive(browser)
const activePicker = computed(() => browserPicker.open ? browserPicker : executionPicker)
const pickerOpen = computed(() => executionPicker.open || browserPicker.open)
const busy = computed(() => !!(executionBlocked.value || browserBlocked.value || pickerOpen.value || restoringDefault.value))
watch(browser.current, target => {
  if (target) browseTargets.value[browseKind.value] = target
  else delete browseTargets.value[browseKind.value]
}, { flush: 'sync' })
watch(pickerOpen, async open => {
  if (open) return
  const trigger = pickerTrigger
  pickerTrigger = undefined
  await nextTick()
  if (!pickerOpen.value && trigger?.isConnected) trigger.focus({ preventScroll: true })
})

function openExecution(trigger?: HTMLElement) {
  if (busy.value) return
  routeRequest?.abort()
  pickerTrigger = trigger
  void execution.show()
}
function prepareBrowse(kind: BrowseKind) {
  browseKind.value = kind
  browser.current.value = browseTargets.value[kind] ?? null
  browser.sources.value = []
}
function chooseBrowse(kind: BrowseKind, trigger?: HTMLElement) {
  if (busy.value) return
  routeRequest?.abort()
  pickerTrigger = trigger
  prepareBrowse(kind)
  void browser.show()
}
function closeTool() {
  if (busy.value) return
  tool.value = 'editor'
}

function stopRestore() {
  restoreRequest?.abort()
  restoreRequest = undefined
  restoringDefault.value = false
  defaultError.value = ''
}
async function restoreScriptTarget(script?: WorkbenchScript, previousTarget?: Omit<ScriptTarget, 'state'>, draftType?: ScriptType) {
  routeRequest?.abort()
  stopRestore()
  execution.current.value = null
  const type = script?.script_type ?? draftType
  if (script?.read_only || type === 'redis') return
  const saved: ScriptTarget | undefined = previousTarget ?? script?.default_target
  if (!saved) return
  if (saved.state === 'unavailable') { defaultError.value = '脚本默认连接已不可用，请选择执行目标。'; return }
  const request = restoreRequest = new AbortController()
  const sameContext = context.value
  const fresh = () => !request.signal.aborted && sameContext === context.value
  restoringDefault.value = true
  try {
    const source = (await readSources(request.signal)).find(item => item.id === saved.connection_id &&
      (type ? matchesScriptType(item, type) : targetKind(item) === 'sql'))
    if (!fresh()) return
    if (!source || !(await readDatabases(source.id, request.signal)).includes(saved.database)) throw new Error('默认连接或数据库不可用')
    if (!fresh()) return
    execution.current.value = { source, database: saved.database }
  } catch {
    if (fresh()) defaultError.value = previousTarget
      ? '未能恢复上次选择的执行目标，请重新选择。'
      : '未能恢复脚本默认连接，请重新选择执行目标。'
  } finally {
    if (restoreRequest === request) restoringDefault.value = false
  }
}
async function applyScriptTarget(target: QueryTarget) {
  if (executionBlocked.value || targetKind(target.source) !== 'sql') throw new Error('当前无法切换脚本执行目标')
  stopRestore()
  execution.current.value = target
}
watch(execution.current, target => {
  if (!target) return
  defaultError.value = ''
})

async function loadRouteTarget() {
  routeRequest?.abort()
  const raw = route.query.connection
  routeTargetError.value = ''
  // 刷新时让用户先核实原执行，避免自动弹出的目标选择器遮住回执入口。
  if (raw === undefined || executionBlocked.value) return
  const id = typeof raw === 'string' && /^[1-9][0-9]*$/.test(raw) && Number.isSafeInteger(Number(raw)) ? Number(raw) : undefined
  const request = routeRequest = new AbortController()
  const requestedContext = context.value, requestedRoute = route.fullPath
  const fresh = () => !request.signal.aborted && requestedContext === context.value && requestedRoute === route.fullPath
  try {
    const sources = await readSources(request.signal)
    if (!fresh() || executionBlocked.value) return
    const source = sources.find(item => item.id === id)
    if (!source || targetKind(source) === 'unavailable') { routeTargetError.value = '指定数据源不可用，请重新选择。'; return }
    const database = typeof route.query.database === 'string' ? route.query.database : ''
    const kind = targetKind(source)
    if (kind === 'sql') {
      tool.value = 'editor'
      execution.sources.value = sources.filter(item => targetKind(item) === 'sql')
      await execution.show(source.id, database)
      if (fresh() && !execution.current.value && database && execution.draft.value?.database === database) await execution.confirm()
    } else if (kind === 'keys' || kind === 'metadata') {
      tool.value = kind
      prepareBrowse(kind)
      browser.sources.value = sources.filter(item => targetKind(item) === kind)
      await browser.show(source.id, database)
      if (fresh() && !browser.current.value && database && browser.draft.value?.database === database) await browser.confirm()
    }
  } catch {
    if (fresh()) routeTargetError.value = '无法核对链接中的数据源，可重新选择目标。'
  }
}

onBeforeRouteUpdate((to, from) => {
  if (!ownNavigation && (to.query.connection !== from.query.connection || to.query.database !== from.query.database) && busy.value) return false
})
watch(() => [route.query.connection, route.query.database], () => {
  if (!ownNavigation) void loadRouteTarget()
})
watch(context, () => {
  routeRequest?.abort()
  stopRestore()
  execution.invalidate()
  browser.invalidate()
  browseTargets.value = {}
  securityGeneration.value++
  executionBlocked.value = ''
  browserBlocked.value = ''
  routeTargetError.value = ''
  tool.value = 'editor'
}, { flush: 'sync' })
onMounted(() => { void loadRouteTarget() })
onScopeDispose(() => { routeRequest?.abort(); restoreRequest?.abort() })
</script>

<template>
  <div class="workbench-page min-w-0" :class="{ 'workbench-page--editor': tool === 'editor' }">
    <PageHeader title="数据工作台" />
    <p v-if="routeTargetError" role="alert" class="text-sm text-danger">{{ routeTargetError }}</p>
    <SqlWorkspace v-show="tool === 'editor'" :key="`sql-${securityGeneration}`" :target="executionPicker.current"
      :locked="pickerOpen || restoringDefault || tool !== 'editor'"
      :restoring-default="restoringDefault" :default-error="defaultError" :apply-script-target="applyScriptTarget"
      :restore-script-target="restoreScriptTarget" @choose-execution="openExecution" @blocked="executionBlocked = $event" />
    <div v-if="tool !== 'editor'" class="space-y-3">
      <div class="flex flex-wrap items-center gap-2">
        <button type="button" class="ghost-button ui-control-target inline-flex items-center gap-1" :disabled="busy" @click="closeTool">
          <ArrowLeft :size="16" aria-hidden="true" />返回编辑
        </button>
        <button type="button" class="ghost-button query-target-trigger ui-control-target" aria-haspopup="dialog"
          :disabled="busy" @click="chooseBrowse(tool, $event.currentTarget as HTMLElement)">
          <span class="truncate">{{ toolTarget ? `${toolTarget.source.name} / ${toolTarget.database || '配置的逻辑库'}` : `选择${browseLabels[tool]}来源` }}</span>
          <ChevronDown :size="16" class="shrink-0" aria-hidden="true" />
        </button>
        <span v-if="toolTarget" class="text-xs shrink-0" :class="toolTarget.source.environment === 'prod' ? 'text-danger' : 'text-muted'">{{ environmentLabel(toolTarget.source) }}</span>
      </div>
      <p v-if="browserBlocked" role="status" class="text-xs text-muted">{{ browserBlocked }}</p>
      <RedisWorkspace v-if="tool === 'keys' && toolTarget" :key="`redis-${securityGeneration}-${toolTarget.source.id}`"
        :source="toolTarget.source" @blocked="browserBlocked = $event" />
      <MetadataWorkspace v-else-if="tool === 'metadata' && toolTarget" :key="`meta-${securityGeneration}-${toolTarget.source.id}-${toolTarget.database}`"
        :source="toolTarget.source" :database="toolTarget.database" @blocked="browserBlocked = $event" />
    </div>
    <QueryTargetPicker :open="pickerOpen" :title="browserPicker.open ? `选择${browseLabels[browseKind]}来源` : '选择执行目标'"
      :current-target="activePicker.current" :draft-target="activePicker.draft" :sources="activePicker.sources" :databases="activePicker.databases"
      :source-loading="activePicker.sourceLoading" :database-loading="activePicker.databaseLoading" :source-error="activePicker.sourceError"
      :database-error="activePicker.databaseError" :submit-error="activePicker.submitError" :submitting="activePicker.submitting"
      :disabled-reason="activePicker.disabledReason"
      @update:open="value => { if (!value) activePicker.close() }" @select-source="activePicker.chooseSource($event)"
      @select-database="activePicker.chooseDatabase" @retry-sources="activePicker.loadSources"
      @retry-databases="activePicker.draftSource && activePicker.chooseSource(activePicker.draftSource.id)" @confirm="activePicker.confirm" />
  </div>
</template>

<style scoped>
.workbench-page { display: grid; gap: 12px; }
@media (max-width: 1023px) {
  .workbench-page--editor { gap: 8px; }
  .workbench-page--editor > :deep(.page-header) { margin-bottom: 0; }
  .workbench-page--editor > :deep(.page-header__title) { font-size: 22px; }
}
@media (min-width: 1024px) {
  :global(.editorial-shell:has(> .editorial-content > .workbench-page--editor)) { height: 100dvh; }
  :global(.editorial-shell:has(> .editorial-content > .workbench-page--editor) > .editorial-masthead) { flex-shrink: 0; }
  :global(main:has(> .workbench-page--editor)) { display: flex; flex-direction: column; min-height: 0; }
  .workbench-page--editor { display: flex; flex: 1; flex-direction: column; min-height: 0; }
  .workbench-page--editor > :deep(.page-header) { margin-bottom: 0; }
}
</style>
