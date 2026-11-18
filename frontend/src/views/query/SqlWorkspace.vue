<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import { CheckCircle2, CircleAlert, FileCode2, LoaderCircle, Play, Square, Table2, Wand2 } from 'lucide-vue-next'
import { type ConnectionItem } from '../../api/datasource'
import { workbenchApi } from '../../api/workbench'
import { useUserStore } from '../../stores/user'
import SqlMonaco from './components/SqlMonaco.vue'
import ResultGrid from './components/ResultGrid.vue'
import { formatSql } from './formatSql'
import { useSqlExecution } from './useSqlExecution'
import { advancedApi, type QuerySettings } from '../../api/advanced'
import QuerySettingsDialog from './components/QuerySettingsDialog.vue'
import ColumnAuthPane from './components/ColumnAuthPane.vue'
import ExplainPlan from './components/ExplainPlan.vue'
import ResultChartPane from './components/ResultChartPane.vue'
import SqlObjectDirectory from './components/SqlObjectDirectory.vue'
import { isExplainStatement } from './explain'

const props=defineProps<{source:ConnectionItem; database:string; locked?:boolean}>()
const emit=defineEmits<{blocked:[reason:string]}>()
const route = useRoute()
const userStore = useUserStore()
const editor = ref<InstanceType<typeof SqlMonaco> | null>(null)
const sql = ref(typeof route.query.sql === 'string' ? route.query.sql : '')
const selectedLength = computed(() => editor.value?.selectedLength ?? 0)
const connections = computed(() => [props.source])
const connectionId = computed(() => props.source.id)
const database = computed(() => props.database)
const formatting = ref(false)
const confirming = ref(false)
const formatError = ref('')
const proportions = ref<'balanced' | 'editor' | 'result'>('balanced')
const showObjects = ref(typeof window !== 'undefined' && window.matchMedia('(min-width: 1024px)').matches)
const resultCategory = ref('result')
const settingsOpen=ref(false),policiesOpen=ref(false),settings=ref<QuerySettings>(),settingsError=ref(''),settingsLoading=ref(true)
const policySupported=computed(()=>['postgres','mysql'].includes(props.source.type)||['postgres','mysql'].includes(props.source.template?.dialect||''))
function settingsSaved(value:QuerySettings){settings.value=value;settingsError.value=''}
const connection = computed(() => connections.value.find(item => item.id === connectionId.value))
const sqlDialect = computed(() => props.source.template?.dialect || (props.source.type === 'mysql' ? 'mysql' : 'postgres'))
let disposed = false


const legacyRecoveryKey = `dbhub:pending-execution:${userStore.user?.id ?? 'anonymous'}`
const recoveryKey = `${legacyRecoveryKey}:${userStore.workspaceId}`
const legacyRequest = ref<string | null>(null)
const legacyError = ref('')
const legacyChecking = ref(false)
let legacyLookup: AbortController | undefined
try { legacyRequest.value = sessionStorage.getItem(legacyRecoveryKey) }
catch { /* 恢复存储不可用时沿用执行组件中的错误处理。 */ }
const { state, busy, needsReview, run, cancel, check, adoptReceipt, acknowledge, reset } = useSqlExecution((context, signal) =>
  workbenchApi.execute(context.connectionId, context.sql, context.database, signal, undefined, {
    request_id: context.requestId, ...(settings.value ? {timeout_ms:settings.value.timeout_ms,max_rows:settings.value.max_rows} : {timeout_ms:120000}),
  }),
  (id, signal) => workbenchApi.execution(id, signal),
  {
    read: () => sessionStorage.getItem(recoveryKey),
    write: id => sessionStorage.setItem(recoveryKey, id),
    clear: () => sessionStorage.removeItem(recoveryKey),
  },
)
const blockedReason = computed(() => needsReview.value ? '请先核实上次执行结果' : busy.value || legacyChecking.value ? '执行、取消或核实处理中，暂不能切换目标' : confirming.value ? '请先完成执行确认' : settingsOpen.value || policiesOpen.value ? '请先关闭查询设置或访问策略' : formatting.value ? '正在格式化 SQL' : '')
const runButton = ref<HTMLButtonElement>()
const cancelButton = ref<HTMLButtonElement>()
const checkButton = ref<HTMLButtonElement>()
const resultHeading = ref<HTMLElement>()
watch(blockedReason, reason => emit('blocked', reason), { immediate: true, flush: 'sync' })
watch(busy, async isBusy => {
  if (isBusy || document.activeElement !== cancelButton.value) return
  await nextTick()
  if (disposed || document.activeElement !== document.body) return
  const target = needsReview.value ? checkButton.value : resultHeading.value
  target?.focus()
})
const canRun = computed(() => !!connection.value && !!database.value && !!sql.value.trim()
  && !busy.value && !legacyChecking.value && !needsReview.value && !confirming.value && !settingsLoading.value && !settingsOpen.value && !policiesOpen.value && !formatting.value && !props.locked)

const executionTarget = computed(() => {
  if (state.value.context) return `${state.value.context.connectionName} / ${state.value.context.database}`
  const receipt = state.value.receipt
  if (!receipt) return ''
  const name = connections.value.find(item => item.id === receipt.connection_id)?.name ?? `连接 ${receipt.connection_id}`
  return `${name} / ${receipt.database || '数据库名称未记录'}`
})

const statusText = computed(() => ({
  idle: '就绪', running: '执行中', canceling: '取消中', checking: '核实中', success: '成功', completed: '已完成', error: '失败', canceled: '已取消', unknown: '待确认',
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
const resultTabs = computed(() => [
  { id: 'result', name: '结果' },
  ...(queryResult.value && !queryResult.value.truncated && queryResult.value.rows?.length ? [{ id: 'chart', name: '图表' }] : []),
  { id: 'execution', name: '执行信息' },
])
watch(() => [state.value.requestId, state.value.status], () => { resultCategory.value = 'result' })

// 同类目标变化仅清理结果，保留SQL模型、选区和撤销历史。
watch(() => [props.source.id, props.database], () => { reset(); formatError.value = '' }, { flush: 'sync' })
const hasDraft = computed(() => !!sql.value.trim())
function openSettings() { if (!blockedReason.value && !props.locked) settingsOpen.value = true }
function openPolicies() { if (!blockedReason.value && !props.locked && database.value) policiesOpen.value = true }
defineExpose({ hasDraft, settings, settingsError, policySupported, openSettings, openPolicies })

async function runQuery() {
  if (!canRun.value || !connection.value) return
  const startedFromButton = document.activeElement === runButton.value
  const selected = editor.value?.getSelection().trim() ?? ''
  const query = selected || sql.value.trim()
  if (!query) return
  const context = {
    connectionId: connection.value.id,
    connectionName: connection.value.name,
    database: database.value,
    dialect: sqlDialect.value,
    sql: query,
    selected: !!selected,
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
  await run(context)
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
  if (!sql.value.trim() || busy.value || confirming.value || formatting.value || props.locked) return
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

watch(sql, () => { formatError.value = '' })

onMounted(() => {
  if (needsReview.value) void check()
  void advancedApi.settings().then(value=>{if(!disposed)settings.value=value}).catch(e=>{if(!disposed)settingsError.value=e instanceof Error?e.message:'读取查询限制失败'}).finally(()=>{if(!disposed)settingsLoading.value=false})
})

onBeforeUnmount(() => {
  legacyLookup?.abort()
  disposed = true
  if (confirming.value) ElMessageBox.close()
})
</script>

<template>
  <div class="sql-layout" :class="{ 'sql-layout--objects': source.capabilities?.metadata }" data-testid="sql-workbench">
    <aside v-if="source.capabilities?.metadata" class="glass-panel ui-card-padding min-w-0 space-y-3" aria-label="数据库对象">
      <button type="button" class="ghost-button ui-control-target w-full" aria-controls="sql-object-directory" :aria-expanded="showObjects" @click="showObjects = !showObjects">{{ showObjects ? '收起数据库对象' : '展开数据库对象' }}</button>
      <div id="sql-object-directory" :hidden="!showObjects">
        <SqlObjectDirectory v-if="showObjects" :source="source.id" :database="database" />
      </div>
    </aside>
    <div class="sql-workbench min-w-0" :class="{ 'sql-workbench--explain': !!explain, 'sql-workbench--editor': proportions === 'editor', 'sql-workbench--result': proportions === 'result' }">
    <section class="glass-panel flex min-h-0 flex-col overflow-hidden" aria-labelledby="sql-editor-title">
      <header class="flex flex-wrap shrink-0 items-center gap-2 border-b border-stroke px-3 py-2.5 sm:px-4">
        <FileCode2 class="h-4 w-4 shrink-0 text-accent" aria-hidden="true" />
        <h2 id="sql-editor-title" class="text-sm text-content">SQL 编辑器</h2>
        <span class="ml-2 hidden text-xs text-subtle lg:inline">选中执行 · Ctrl / ⌘ + Enter</span>
        <div class="flex-1" />
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
          ref="runButton"
          class="liquid-button flex items-center gap-1.5 !px-3 !py-2 text-xs ui-control-target"
          :disabled="!canRun && !busy && !needsReview"
          :aria-disabled="busy || needsReview || undefined"
          aria-keyshortcuts="Control+Enter Meta+Enter"
          @click="runQuery"
        >
          <LoaderCircle v-if="busy" class="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
          <Play v-else class="h-3.5 w-3.5" aria-hidden="true" />
          {{ busy ? '执行中' : '运行' }}
        </button>
        <button
          class="ghost-button flex items-center gap-1.5 !px-3 !py-2 text-xs disabled:cursor-not-allowed disabled:opacity-50 ui-control-target"
          :disabled="!connection?.capabilities?.format || !sql.trim() || busy || confirming || formatting || locked"
          aria-keyshortcuts="Control+Shift+F Meta+Shift+F"
          @click="formatCurrent"
        >
          <Wand2 class="h-3.5 w-3.5" aria-hidden="true" />{{ formatting ? '格式化中' : '格式化' }}
        </button>
      </header>
      <div class="min-h-0 flex-1">
        <SqlMonaco ref="editor" v-model="sql" @run="runQuery" @format="formatCurrent" />
      </div>
      <p
        v-if="formatError"
        class="shrink-0 border-t border-stroke px-4 py-2 text-xs text-warning"
        role="alert"
      >{{ formatError }}</p>
      <footer class="flex flex-wrap shrink-0 items-center justify-between gap-2 border-t border-stroke px-4 py-1.5 text-xs text-muted">
        <span>每次执行一条 SQL</span>
        <span>{{ selectedLength ? '已选 ' + selectedLength + ' 字符' : '未选中时执行全部内容' }}</span>
        <label class="flex flex-wrap items-center gap-1">显示比例
          <select v-model="proportions" class="glass-input ui-control-target">
            <option value="balanced">标准</option><option value="editor">编辑优先</option><option value="result">结果优先</option>
          </select>
        </label>
      </footer>
    </section>

    <section
      class="glass-panel flex min-h-0 flex-col overflow-hidden"
      aria-labelledby="sql-result-title"
      data-testid="query-result"
    >
      <header class="flex shrink-0 items-center gap-3 border-b border-stroke px-4 py-3">
        <Table2 class="h-4 w-4 shrink-0 text-accent" aria-hidden="true" />
        <h2 id="sql-result-title" ref="resultHeading" tabindex="-1" class="shrink-0 text-sm text-content ui-focus-target">结果</h2>
        <span v-if="executionTarget" class="min-w-0 truncate text-xs text-muted" :title="executionTarget">
          {{ executionTarget }}
        </span>
        <span class="flex-1" />
        <span class="shrink-0 text-xs" :class="statusClass" role="status">
          <span v-if="state.status === 'unknown'" class="sr-only">执行结果</span><span :aria-hidden="state.status === 'error' || undefined">{{ statusText }}</span>
        </span>
      </header>
      <div v-if="state.context" class="shrink-0 truncate border-b border-stroke bg-surface-muted px-4 py-1.5 font-mono text-xs text-muted" :title="state.context.sql">
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
          <ResultGrid v-if="resultCategory === 'result'" :columns="queryResult.columns ?? []" :rows="queryResult.rows ?? []" />
          <div v-else-if="resultCategory === 'chart'" class="min-h-0 flex-1 overflow-auto">
            <ResultChartPane :columns="queryResult.columns ?? []" :rows="queryResult.rows ?? []" :sql="state.context.sql" :connection-id="state.context.connectionId" :database="state.context.database" />
          </div>
          <dl v-else class="min-h-0 overflow-auto grid gap-2 p-4 text-sm sm:grid-cols-2">
            <div><dt class="text-muted">执行目标</dt><dd class="break-all">{{ executionTarget }}</dd></div>
            <div><dt class="text-muted">执行耗时</dt><dd>{{ queryResult.duration_ms }} ms</dd></div>
            <div><dt class="text-muted">执行编号</dt><dd class="break-all">{{ state.requestId }}</dd></div>
            <div><dt class="text-muted">返回行数</dt><dd>{{ queryResult.rows?.length ?? 0 }}<span v-if="queryResult.truncated">（结果已截断）</span></dd></div>
          </dl>
        </PageCategoryTabs>
      </div>
      <div v-else-if="state.status === 'error'" class="min-h-0 flex-1 overflow-auto p-4" role="alert">
        <p class="mb-2 flex items-center gap-2 text-sm text-danger"><CircleAlert class="h-4 w-4" aria-hidden="true" />执行失败</p>
        <pre class="whitespace-pre-wrap break-words font-mono text-xs leading-6 text-danger">{{ state.error }}</pre>
      </div>
      <div v-else class="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 overflow-auto px-5 py-6 text-center" :aria-busy="busy">
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
          <p class="text-sm text-muted">查询结果将在这里显示</p>
          <p class="text-xs text-muted">确认查询目标，输入 SQL 后运行。</p>
        </template>
      </div>

      <footer v-if="state.result" class="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-t border-stroke px-4 py-2 text-xs text-muted">
        <span>{{ state.result.kind === 'query' ? '返回' : '影响' }} {{ state.result.kind === 'query' ? (state.result.rows?.length ?? 0) : (state.result.affected_rows ?? 0) }} 行</span>
        <span>耗时 {{ state.result.duration_ms }} ms</span>
        <span v-if="state.result.truncated" class="text-warning">结果已截断，仅显示前 {{ state.result.rows?.length ?? 0 }} 行</span>
      </footer>
    </section>
    </div>
    <QuerySettingsDialog v-if="settingsOpen" @close="settingsOpen=false" @saved="settingsSaved" />
    <el-dialog v-if="policiesOpen" :model-value="true" title="行列访问策略" width="min(840px, 94vw)" :close-on-click-modal="false" @close="policiesOpen=false">
      <ColumnAuthPane :connection-id="source.id" :database="database" :editable="source.user_id===userStore.user?.id" />
    </el-dialog>
  </div>
</template>



<style scoped>
.sql-layout { display: grid; grid-template-columns: minmax(0, 1fr); align-items: start; gap: 12px; }
@media (min-width: 1024px) {
  .sql-layout--objects { grid-template-columns: 220px minmax(0, 1fr); }
}
.sql-workbench {
  display: grid;
  grid-template-rows: minmax(220px, 1.2fr) minmax(200px, 1fr);
  gap: 0;
  height: calc(100dvh - 13rem);
  min-height: 580px;
}
.sql-workbench--editor { grid-template-rows: minmax(240px, 2fr) minmax(200px, 1fr); }
.sql-workbench--result { grid-template-rows: minmax(220px, 1fr) minmax(240px, 2fr); }
.sql-result-tabs :deep([role='tabpanel']:not([hidden])) { display: flex; flex: 1; min-height: 0; flex-direction: column; }
@media (max-width: 639px) {
  .sql-workbench { height: calc(100dvh - 13rem); }
  .sql-workbench--explain {
    grid-template-rows: minmax(220px, 1fr) minmax(420px, 1fr);
    height: auto;
  }
}
@media (prefers-reduced-motion: reduce) {
  .animate-spin { animation: none; }
}
</style>
