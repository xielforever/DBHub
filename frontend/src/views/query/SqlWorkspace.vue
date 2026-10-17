<template>
  <div class="sql-workbench" data-testid="sql-workbench">
    <section class="glass-panel px-4 py-3" aria-label="数据库选择">
      <label class="block min-w-0 sm:w-64 text-xs text-white/60">数据库
        <el-select v-model="database" aria-label="选择数据库" placeholder="选择数据库" class="w-full mt-1" :loading="databasesLoading" :disabled="busy || confirming || databasesLoading" popper-class="glass-popper"><el-option v-for="name in databases" :key="name" :label="name" :value="name" /></el-select>
      </label>
      <p v-if="sourceError" class="mt-2 text-xs text-rose-300" role="alert">{{ sourceError }}</p>
    </section>

    <section class="glass-panel flex min-h-0 flex-col overflow-hidden" aria-labelledby="sql-editor-title">
      <header class="flex shrink-0 items-center gap-2 border-b border-white/10 px-3 py-2.5 sm:px-4">
        <FileCode2 class="h-4 w-4 shrink-0 text-indigo-300" aria-hidden="true" />
        <h2 id="sql-editor-title" class="text-sm text-white/85">SQL 编辑器</h2>
        <span class="ml-2 hidden text-xs text-white/40 lg:inline">选中执行 · Ctrl / ⌘ + Enter</span>
        <div class="flex-1" />
        <button
          v-if="busy && state.status !== 'checking'"
          class="ghost-button flex items-center gap-1.5 !px-3 !py-2 text-xs disabled:opacity-50"
          :disabled="state.status === 'canceling'"
          @click="cancel"
        >
          <Square class="h-3.5 w-3.5" aria-hidden="true" />{{ state.status === 'canceling' ? '取消中…' : '取消' }}
        </button>
        <button
          class="liquid-button flex items-center gap-1.5 !px-3 !py-2 text-xs"
          :disabled="!canRun"
          aria-keyshortcuts="Control+Enter Meta+Enter"
          @click="runQuery"
        >
          <LoaderCircle v-if="busy" class="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
          <Play v-else class="h-3.5 w-3.5" aria-hidden="true" />
          {{ busy ? '执行中' : '运行' }}
        </button>
        <button
          class="ghost-button flex items-center gap-1.5 !px-3 !py-2 text-xs disabled:cursor-not-allowed disabled:opacity-50"
          :disabled="!connection?.capabilities?.format || !sql.trim() || busy || confirming || formatting"
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
        class="shrink-0 border-t border-white/10 px-4 py-2 text-xs text-amber-200"
        role="alert"
      >{{ formatError }}</p>
      <footer class="flex shrink-0 items-center justify-between gap-2 border-t border-white/10 px-4 py-1.5 text-[11px] text-white/45">
        <span>每次执行一条 SQL</span>
        <span>{{ selectedLength ? '已选 ' + selectedLength + ' 字符' : '未选中时执行全部内容' }}</span>
      </footer>
    </section>

    <section
      class="glass-panel flex min-h-0 flex-col overflow-hidden"
      aria-labelledby="sql-result-title"
      :aria-busy="busy"
      data-testid="query-result"
    >
      <header class="flex shrink-0 items-center gap-3 border-b border-white/10 px-4 py-3">
        <Table2 class="h-4 w-4 shrink-0 text-indigo-300" aria-hidden="true" />
        <h2 id="sql-result-title" class="shrink-0 text-sm text-white/85">结果</h2>
        <span v-if="executionTarget" class="min-w-0 truncate text-xs text-white/50" :title="executionTarget">
          {{ executionTarget }}
        </span>
        <span class="flex-1" />
        <span class="shrink-0 text-xs" :class="statusClass" role="status">{{ statusText }}</span>
      </header>
      <div v-if="state.context" class="shrink-0 truncate border-b border-white/5 bg-white/[0.02] px-4 py-1.5 font-mono text-[11px] text-white/45" :title="state.context.sql">
        {{ state.context.selected ? '选中执行' : '执行 SQL' }}：{{ state.context.sql.replace(/\s+/g, ' ') }}
      </div>
      <p v-if="state.requestId && ['unknown', 'checking', 'completed'].includes(state.status)" class="shrink-0 break-all border-b border-white/5 px-4 py-1.5 text-[11px] text-white/50">
        执行编号：<span class="select-text font-mono">{{ state.requestId }}</span>
      </p>

      <ResultGrid
        v-if="state.status === 'success' && state.result?.kind === 'query'"
        :columns="state.result.columns ?? []"
        :rows="state.result.rows ?? []"
      />
      <div v-else-if="state.status === 'error'" class="min-h-0 flex-1 overflow-auto p-4" role="alert">
        <p class="mb-2 flex items-center gap-2 text-sm text-rose-300"><CircleAlert class="h-4 w-4" aria-hidden="true" />执行失败</p>
        <pre class="whitespace-pre-wrap break-words font-mono text-xs leading-6 text-rose-200/85">{{ state.error }}</pre>
      </div>
      <div v-else class="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 overflow-auto px-5 py-6 text-center">
        <template v-if="state.status === 'success' && state.result?.kind === 'write'">
          <CheckCircle2 class="h-7 w-7 text-emerald-300" aria-hidden="true" />
          <p class="text-sm text-white/80">执行成功，影响 {{ state.result.affected_rows ?? 0 }} 行</p>
        </template>
        <template v-else-if="busy">
          <LoaderCircle class="h-6 w-6 animate-spin text-indigo-300" aria-hidden="true" />
          <p class="text-sm text-white/65">{{ state.status === 'checking' ? '正在核实执行状态…' : state.status === 'canceling' ? '正在取消请求…' : '正在执行 SQL…' }}</p>
        </template>
        <template v-else-if="state.status === 'unknown'">
          <CircleAlert class="h-7 w-7 shrink-0 text-amber-200" aria-hidden="true" />
          <p class="text-sm text-amber-200" role="status">执行结果待确认</p>
          <p class="max-w-xl text-xs leading-5 text-white/65">{{ state.error }}</p>
          <div class="flex flex-wrap justify-center gap-2">
            <button class="ghost-button !px-3 !py-2 text-xs" @click="check">核实状态</button>
            <button class="ghost-button !px-3 !py-2 text-xs" @click="acknowledgeOutcome">已核实，继续</button>
          </div>
        </template>
        <template v-else-if="state.status === 'completed'">
          <CheckCircle2 class="h-7 w-7 text-emerald-300" aria-hidden="true" />
          <p class="text-sm text-white/80">服务端已确认执行完成</p>
          <p class="max-w-xl text-xs leading-5 text-white/55">{{ state.error }}</p>
        </template>
        <template v-else-if="state.status === 'canceled'">
          <Square class="h-6 w-6 text-amber-200/80" aria-hidden="true" />
          <p class="text-sm text-white/70">请求已取消</p>
          <p class="text-xs text-white/45">{{ state.error }}</p>
        </template>
        <template v-else>
          <Table2 class="h-7 w-7 text-white/25" aria-hidden="true" />
          <p class="text-sm text-white/65">查询结果将在这里显示</p>
          <p class="text-xs text-white/45">选择连接与数据库，输入 SQL 后运行。</p>
        </template>
      </div>

      <footer v-if="state.result" class="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-t border-white/10 px-4 py-2 text-xs text-white/55">
        <span>{{ state.result.kind === 'query' ? '返回' : '影响' }} {{ state.result.kind === 'query' ? (state.result.rows?.length ?? 0) : (state.result.affected_rows ?? 0) }} 行</span>
        <span>耗时 {{ state.result.duration_ms }} ms</span>
        <span v-if="state.result.truncated" class="text-amber-200">结果已截断，仅显示前 {{ state.result.rows?.length ?? 0 }} 行</span>
      </footer>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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

const props=defineProps<{source:ConnectionItem}>()
const emit=defineEmits<{busy:[value:boolean]}>()
const route = useRoute()
const userStore = useUserStore()
const editor = ref<InstanceType<typeof SqlMonaco> | null>(null)
const sql = ref(typeof route.query.sql === 'string' ? route.query.sql : '')
const selectedLength = computed(() => editor.value?.selectedLength ?? 0)
const connections = computed(() => [props.source])
const connectionId = ref<number>()
const database = ref('')
const databases = ref<string[]>([])
const connectionsLoading = ref(false)
const databasesLoading = ref(false)
const sourceError = ref('')
const formatting = ref(false)
const confirming = ref(false)
const formatError = ref('')
const connection = computed(() => connections.value.find(item => item.id === connectionId.value))
let disposed = false
let metadataRequest: AbortController | null = null

const recoveryKey = `dbhub:pending-execution:${userStore.user?.id ?? 'anonymous'}`
const { state, busy, needsReview, run, cancel, check, acknowledge } = useSqlExecution((context, signal) =>
  workbenchApi.execute(context.connectionId, context.sql, context.database, signal, undefined, {
    request_id: context.requestId, timeout_ms: 30000, max_rows: 1000,
  }),
  (id, signal) => workbenchApi.execution(id, signal),
  {
    read: () => sessionStorage.getItem(recoveryKey),
    write: id => sessionStorage.setItem(recoveryKey, id),
    clear: () => sessionStorage.removeItem(recoveryKey),
  },
)
watch([busy,needsReview],()=>emit('busy',busy.value || needsReview.value))
const canRun = computed(() => !!connection.value && !!database.value && !!sql.value.trim()
  && !busy.value && !needsReview.value && !confirming.value && !connectionsLoading.value && !databasesLoading.value && !formatting.value && !sourceError.value)

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
  idle: 'text-white/45', running: 'text-indigo-200', canceling: 'text-amber-200',
  checking: 'text-amber-200', success: 'text-emerald-300', completed: 'text-emerald-300', error: 'text-rose-300', canceled: 'text-amber-200', unknown: 'text-amber-200',
})[state.value.status])

/** 后来的数据库请求可覆盖前一个；过时响应不改变当前连接的数据库。 */
watch(connectionId, async () => {
  metadataRequest?.abort()
  metadataRequest = null
  database.value = ''
  databases.value = []
  sourceError.value = ''
  const selected = connection.value
  if (!selected) { databasesLoading.value = false; return }
  const request = new AbortController()
  metadataRequest = request
  databasesLoading.value = true
  try {
    const result = await workbenchApi.databases(selected.id, request.signal)
    if (disposed || metadataRequest !== request || connectionId.value !== selected.id) return
    databases.value = [...new Set(result.items.map(item => item.name))]
    database.value = databases.value.includes(selected.database) ? selected.database : (databases.value[0] ?? '')
    if (!database.value) sourceError.value = '该连接没有可用的数据库，请检查数据源配置。'
  } catch (error: unknown) {
    if (!disposed && metadataRequest === request && !request.signal.aborted) {
      sourceError.value = error instanceof Error ? error.message : '数据库列表加载失败，请重新选择连接。'
    }
  } finally {
    if (!disposed && metadataRequest === request) databasesLoading.value = false
  }
})

async function runQuery() {
  if (!canRun.value || !connection.value) return
  const selected = editor.value?.getSelection().trim() ?? ''
  const query = selected || sql.value.trim()
  if (!query) return
  const context = {
    connectionId: connection.value.id,
    connectionName: connection.value.name,
    database: database.value,
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
  if (!disposed) void run(context)
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
  if (!sql.value.trim() || busy.value || confirming.value || formatting.value) return
  const original = sql.value
  const targetId = connectionId.value
  const dialect = connection.value?.template?.dialect || (connection.value?.type === 'mysql' ? 'mysql' : 'postgres')
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
  connectionId.value=props.source.id
})

onBeforeUnmount(() => {
  disposed = true
  metadataRequest?.abort()
  if (confirming.value) ElMessageBox.close()
})
</script>

<style scoped>
.sql-workbench {
  display: grid;
  grid-template-rows: auto minmax(220px, 1fr) minmax(220px, 1fr);
  gap: 12px;
  height: calc(100dvh - 13rem);
  min-height: 580px;
}
@media (max-width: 639px) {
  .sql-workbench { height: calc(100dvh - 13rem); }
}
@media (prefers-reduced-motion: reduce) {
  .animate-spin { animation: none; }
}
</style>
