<template>
  <div class="sql-workbench" data-testid="sql-workbench">
    <section class="glass-panel px-4 py-3" aria-label="执行目标">
      <div class="grid grid-cols-2 gap-3 sm:flex sm:items-end sm:gap-4">
        <label class="min-w-0 sm:w-64">
          <span class="mb-1.5 flex items-center justify-between text-xs text-white/55">
            <span>连接</span><span v-if="connection?.environment === 'prod'" class="text-rose-300">生产环境</span>
          </span>
          <el-select
            v-model="connectionId"
            aria-label="选择连接"
            placeholder="选择数据库连接"
            class="w-full"
            :loading="connectionsLoading"
            :disabled="busy || confirming || connectionsLoading"
            popper-class="glass-popper"
          >
            <el-option v-for="item in connections" :key="item.id" :label="item.name" :value="item.id">
              <span>{{ item.name }}</span>
              <span class="ml-3 text-xs text-white/40">{{ item.type === 'postgres' ? 'PostgreSQL' : 'MySQL' }}</span>
            </el-option>
          </el-select>
        </label>
        <label class="min-w-0 sm:w-56">
          <span class="mb-1.5 flex items-center justify-between text-xs text-white/55">
            <span>数据库</span><span v-if="userStore.role === 'readonly'" class="text-amber-200">只读</span>
          </span>
          <el-select
            v-model="database"
            aria-label="选择数据库"
            placeholder="选择数据库"
            class="w-full"
            :loading="databasesLoading"
            :disabled="busy || confirming || databasesLoading || !connection"
            popper-class="glass-popper"
          >
            <el-option v-for="name in databases" :key="name" :label="name" :value="name" />
          </el-select>
        </label>
        <div class="hidden min-w-0 flex-1 items-center justify-end gap-3 pb-2 text-xs text-white/50 sm:flex">
          <span v-if="connection" class="truncate">{{ connection.type === 'postgres' ? 'PostgreSQL' : 'MySQL' }}</span>
        </div>
      </div>
      <p v-if="sourceError" class="mt-2 text-xs text-rose-300" role="alert">{{ sourceError }}</p>
      <p v-else-if="!connectionsLoading && !connections.length" class="mt-2 text-xs text-white/55">
        暂无 PostgreSQL 或 MySQL 连接，请先在
        <RouterLink to="/connections" class="text-indigo-300 underline underline-offset-2">数据源管理</RouterLink>
        中添加。
      </p>
    </section>

    <section class="glass-panel flex min-h-0 flex-col overflow-hidden" aria-labelledby="sql-editor-title">
      <header class="flex shrink-0 items-center gap-2 border-b border-white/10 px-3 py-2.5 sm:px-4">
        <FileCode2 class="h-4 w-4 shrink-0 text-indigo-300" aria-hidden="true" />
        <h2 id="sql-editor-title" class="text-sm text-white/85">SQL 编辑器</h2>
        <span class="ml-2 hidden text-xs text-white/40 lg:inline">选中执行 · Ctrl / ⌘ + Enter</span>
        <div class="flex-1" />
        <button
          v-if="busy"
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
          :disabled="!sql.trim() || busy || confirming || formatting"
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
        <span v-if="state.context" class="min-w-0 truncate text-xs text-white/50" :title="state.context.connectionName + ' / ' + state.context.database">
          {{ state.context.connectionName }} / {{ state.context.database }}
        </span>
        <span class="flex-1" />
        <span class="shrink-0 text-xs" :class="statusClass" role="status">{{ statusText }}</span>
      </header>
      <div v-if="state.context" class="shrink-0 truncate border-b border-white/5 bg-white/[0.02] px-4 py-1.5 font-mono text-[11px] text-white/45" :title="state.context.sql">
        {{ state.context.selected ? '选中执行' : '执行 SQL' }}：{{ state.context.sql.replace(/\s+/g, ' ') }}
      </div>

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
          <p class="text-sm text-white/65">{{ state.status === 'canceling' ? '正在取消请求…' : '正在执行 SQL…' }}</p>
        </template>
        <template v-else-if="state.status === 'canceled'">
          <Square class="h-6 w-6 text-amber-200/80" aria-hidden="true" />
          <p class="text-sm text-white/70">请求已取消</p>
          <p class="text-xs text-white/45">本次请求未接收执行结果。</p>
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
import { connectionApi, type ConnectionItem } from '../../api/datasource'
import { workbenchApi } from '../../api/workbench'
import { useUserStore } from '../../stores/user'
import SqlMonaco from './components/SqlMonaco.vue'
import ResultGrid from './components/ResultGrid.vue'
import { formatSql } from './formatSql'
import { useSqlExecution } from './useSqlExecution'

const route = useRoute()
const userStore = useUserStore()
const editor = ref<InstanceType<typeof SqlMonaco> | null>(null)
const sql = ref(typeof route.query.sql === 'string' ? route.query.sql : '')
const selectedLength = computed(() => editor.value?.selectedLength ?? 0)
const connections = ref<ConnectionItem[]>([])
const connectionId = ref<number>()
const database = ref('')
const databases = ref<string[]>([])
const connectionsLoading = ref(true)
const databasesLoading = ref(false)
const sourceError = ref('')
const formatting = ref(false)
const confirming = ref(false)
const formatError = ref('')
const connection = computed(() => connections.value.find(item => item.id === connectionId.value))
let disposed = false
let metadataRequest: AbortController | null = null

const { state, busy, run, cancel } = useSqlExecution((context, signal) =>
  workbenchApi.execute(context.connectionId, context.sql, context.database, signal, undefined, {
    timeout_ms: 30000, max_rows: 1000,
  }),
)
const canRun = computed(() => !!connection.value && !!database.value && !!sql.value.trim()
  && !busy.value && !confirming.value && !connectionsLoading.value && !databasesLoading.value && !formatting.value && !sourceError.value)

const statusText = computed(() => ({
  idle: '就绪', running: '执行中', canceling: '取消中', success: '成功', error: '失败', canceled: '已取消',
})[state.value.status])
const statusClass = computed(() => ({
  idle: 'text-white/45', running: 'text-indigo-200', canceling: 'text-amber-200',
  success: 'text-emerald-300', error: 'text-rose-300', canceled: 'text-amber-200',
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

async function formatCurrent() {
  if (!sql.value.trim() || busy.value || confirming.value || formatting.value) return
  const original = sql.value
  const targetId = connectionId.value
  const dialect = connection.value?.type === 'mysql' ? 'mysql' : 'postgres'
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

onMounted(async () => {
  try {
    const result = await connectionApi.list()
    if (disposed) return
    connections.value = result.items.filter(item => item.type === 'postgres' || item.type === 'mysql')
    const requested = Number(route.query.connection)
    const initial = requested ? connections.value.find(item => item.id === requested) : connections.value[0]
    connectionId.value = initial?.id
    if (requested && !initial) sourceError.value = '指定连接不可用，请选择 PostgreSQL 或 MySQL 连接。'
  } catch (error: unknown) {
    if (!disposed) sourceError.value = error instanceof Error ? error.message : '数据源加载失败，请刷新页面重试。'
  } finally {
    if (!disposed) connectionsLoading.value = false
  }
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
  height: calc(100dvh - 7rem);
  min-height: 660px;
}
@media (max-width: 639px) {
  .sql-workbench { height: calc(100dvh - 6rem); }
}
@media (prefers-reduced-motion: reduce) {
  .animate-spin { animation: none; }
}
</style>
