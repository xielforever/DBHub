<template>
  <div class="flex flex-col lg:flex-row gap-4 lg:h-[calc(100vh-7rem)]" :class="{ 'fixed inset-0 z-[2000] bg-[#0a0a14] p-4 gap-0': resultFullscreen }">
    <!-- 左侧：连接树（lg 及以上常驻） -->
    <ConnectionTreePanel
      v-if="!resultFullscreen"
      ref="treeRef"
      class="hidden lg:flex w-60 xl:w-64 shrink-0"
      @select-connection="onSelectConnection"
      @databases-loaded="onDatabasesLoaded"
      @tables-loaded="onTablesLoaded"
      @preview-table="onPreviewTable"
      @generate-select="onGenerateSelect"
      @redis-overview="onRedisOverview"
      @redis-keys="onRedisKeys"
    />

    <el-drawer v-model="treeDrawerOpen" title="数据源" direction="ltr" size="82%" lazy class="glass-drawer">
      <ConnectionTreePanel
        embedded
        @select-connection="onSelectConnection"
        @databases-loaded="onDatabasesLoaded"
        @tables-loaded="onTablesLoaded"
        @preview-table="(p) => { onPreviewTable(p); treeDrawerOpen = false }"
        @generate-select="(p) => { onGenerateSelect(p); treeDrawerOpen = false }"
        @redis-overview="(c) => { onRedisOverview(c); treeDrawerOpen = false }"
        @redis-keys="(c) => { onRedisKeys(c); treeDrawerOpen = false }"
      />
    </el-drawer>

    <section class="flex-1 min-w-0 flex flex-col gap-0 overflow-hidden">
      <!-- 编辑器卡片 -->
      <div
        v-if="!resultFullscreen"
        class="glass-panel flex flex-col overflow-hidden shrink-0"
        :style="{ height: editorHeight + '%' }"
      >
        <el-tabs
          v-model="activeTab"
          class="query-tabs flex-1 flex flex-col min-h-0"
          @tab-remove="closeTab"
        >
          <el-tab-pane
            v-for="tab in tabs"
            :key="tab.id"
            :name="tab.id"
            :closable="tabs.length > 1"
            class="flex flex-col min-h-0 flex-1"
          >
            <template #label>
              <span class="flex items-center gap-2 px-1">
                <FileCode2 class="w-3.5 h-3.5" />{{ tab.name }}
                <span v-if="tab.sql.trim().length" class="w-1.5 h-1.5 rounded-full bg-indigo-400/80 ml-1" />
              </span>
            </template>

            <div class="flex flex-wrap items-center gap-2 sm:gap-3 px-3 sm:px-4 py-2.5 border-b border-white/10 shrink-0">
              <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1.5 lg:hidden" @click="treeDrawerOpen = true">
                <FolderTree class="w-3.5 h-3.5" /> 连接
              </button>
              <button class="liquid-button !px-4 !py-2 text-sm flex items-center gap-2" :disabled="!currentConn || running" @click="runQuery">
                <Play class="w-4 h-4" />{{ running ? '执行中…' : '运行' }}
                <span class="hidden sm:inline text-[10px] opacity-60 ml-1">⌘↵</span>
              </button>
              <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1.5" @click="formatCurrent">
                <Wand2 class="w-3.5 h-3.5" /> 格式化
              </button>
              <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1.5" @click="addTab">
                <Plus class="w-3.5 h-3.5" /> 新查询
              </button>
              <div class="hidden sm:block h-5 w-px bg-white/10" />
              <span v-if="currentConn" class="flex items-center gap-1.5 text-xs text-white/65">
                <component :is="currentConn.type === 'redis' ? KeyRound : Database" class="w-3.5 h-3.5" :style="{ color: connColor(currentConn.type) }" />
                {{ currentConn.name }}
                <span v-if="currentConn.environment === 'prod'" class="px-1.5 py-0.5 rounded-full bg-rose-500/20 text-rose-300 text-[10px]">PROD</span>
                <span v-if="currentConn.proxy_name" class="px-1.5 py-0.5 rounded-full bg-violet-500/20 text-violet-300 text-[10px] flex items-center gap-1"><Waypoints class="w-3 h-3" />{{ currentConn.proxy_name }}</span>
              </span>
              <div v-if="currentConn && currentConn.type !== 'redis'" class="w-32 sm:w-40 shrink-0">
                <el-select
                  v-model="tab.database"
                  size="small"
                  aria-label="目标数据库"
                  class="w-full"
                  popper-class="glass-popper"
                >
                  <el-option v-for="d in databaseOptions" :key="d.name" :label="d.name" :value="d.name" />
                </el-select>
              </div>
              <div class="flex-1" />
              <span class="hidden lg:flex items-center gap-1 text-[10px] text-white/25"><Keyboard class="w-3 h-3" /> ⌘+Enter 运行 · ⇧⌘+F 格式化</span>
              <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1.5 xl:hidden" @click="aiDrawerOpen = true">
                <Sparkles class="w-3.5 h-3.5" /> AI
              </button>
            </div>

            <!-- Monaco 编辑器，失败回退 textarea -->
            <div class="flex-1 min-h-0 relative">
              <SqlMonaco
                v-if="monacoReady"
                :ref="(el: any) => { if (el) monacoRefs[tab.id] = el }"
                v-model="tab.sql"
                :tables="allTableNames"
                :columns="allColumnNames"
                :placeholder="currentConn && currentConn.type === 'redis'
                  ? '-- Redis 数据源不支持 SQL，请在左下方结果区浏览键空间'
                  : '-- 先在左侧选择数据源与表，然后在此编写 SQL，Ctrl/Cmd + Enter 运行'"
                @run="runQuery"
                @format="formatCurrent"
              />
              <textarea
                v-else
                v-model="tab.sql"
                spellcheck="false"
                role="textbox"
                aria-label="SQL 编辑器"
                class="flex-1 w-full h-full resize-none bg-transparent p-4 font-mono text-[13px] leading-6 text-indigo-100/90 outline-none focus:outline-none"
                :placeholder="currentConn && currentConn.type === 'redis'
                  ? '-- Redis 数据源不支持 SQL，请在左下方结果区浏览键空间'
                  : '-- 先在左侧选择数据源与表，然后在此编写 SQL，Ctrl/Cmd + Enter 运行'"
                @keydown.ctrl.enter.prevent="runQuery"
                @keydown.meta.enter.prevent="runQuery"
              />
            </div>
          </el-tab-pane>
        </el-tabs>
      </div>

      <!-- 分割线 -->
      <div
        v-if="!resultFullscreen"
        class="h-2 shrink-0 flex items-center justify-center cursor-row-resize group/splitter select-none"
        @mousedown="startSplitterDrag"
      >
        <div class="w-12 h-1 rounded-full bg-white/10 group-hover/splitter:bg-indigo-400/50 transition-colors" />
      </div>

      <!-- 结果面板 -->
      <div
        class="glass-panel flex flex-col overflow-hidden min-h-[180px]"
        :class="resultFullscreen ? 'flex-1' : ''"
        :style="resultFullscreen ? {} : { height: (100 - editorHeight) + '%' }"
      >
        <el-tabs v-model="resultTab" class="flex-1 flex flex-col min-h-0" @tab-change="onResultTabChange">
          <!-- 数据网格（SQL 结果 / 表预览） -->
          <el-tab-pane label="结果" name="result" class="flex flex-col min-h-0 flex-1">
            <!-- 表预览分页条 -->
            <div v-if="viewMode === 'preview'" class="flex flex-wrap items-center gap-3 px-4 py-2 border-b border-white/10 text-xs text-white/60 shrink-0">
              <Table2 class="w-3.5 h-3.5 text-emerald-300" />
              <span class="font-mono">{{ preview.schema || preview.database }}.{{ preview.table }}</span>
              <span class="text-white/35">共 {{ preview.total }} 行</span>
              <div class="flex-1" />
              <button class="ghost-button !py-1 !px-2.5" :disabled="preview.page <= 1" @click="previewPage(-1)">上一页</button>
              <span>第 {{ preview.page }} 页</span>
              <button class="ghost-button !py-1 !px-2.5" :disabled="!preview.hasMore" @click="previewPage(1)">下一页</button>
            </div>
            <!-- 写操作结果 -->
            <div v-if="writeResult" class="flex-1 flex flex-col items-center justify-center gap-2 text-sm">
              <CheckCircle2 class="w-8 h-8 text-emerald-400" />
              <p class="text-white/80">执行成功，影响 {{ writeResult.affected_rows ?? 0 }} 行 · 耗时 {{ writeResult.duration_ms }} ms</p>
              <button class="ghost-button !py-1 !px-3 text-xs mt-2" @click="resetGrid">清空结果</button>
            </div>
            <!-- 结果网格 -->
            <ResultGrid
              v-else
              :columns="grid.columns"
              :rows="grid.rows as any"
              :truncated="grid.truncated"
              :base-index="viewMode === 'preview' ? (preview.page - 1) * preview.pageSize : 0"
              :sql="currentTab.sql"
              :fullscreen="resultFullscreen"
              @toggle-fullscreen="resultFullscreen = !resultFullscreen"
            />
          </el-tab-pane>

          <!-- 图表（M3） -->
          <el-tab-pane label="图表" name="chart" class="flex flex-col min-h-0 flex-1">
            <ResultChartPane
              :columns="grid.columns"
              :rows="(grid.rows as unknown[][])"
              :sql="currentTab.sql"
              :connection-id="currentConn?.id ?? 0"
              :database="currentTab.database"
            />
          </el-tab-pane>

          <!-- 查询历史 -->
          <el-tab-pane label="历史" name="history" class="flex flex-col min-h-0 flex-1">
            <div class="flex flex-wrap items-center gap-2 px-4 py-2 border-b border-white/10">
              <div class="relative">
                <Search class="w-3.5 h-3.5 absolute left-2 top-1/2 -translate-y-1/2 text-white/30" />
                <input v-model.trim="historyKeyword" class="glass-input !py-1.5 !pl-7 text-xs w-40 sm:w-52" placeholder="搜索 SQL 关键词" @keydown.enter="loadHistory" />
              </div>
              <el-select v-model="historyFilter" size="small" class="w-24" popper-class="glass-popper" @change="loadHistory">
                <el-option label="全部" value="all" />
                <el-option label="成功" value="success" />
                <el-option label="失败" value="failed" />
              </el-select>
              <div class="flex-1" />
              <button class="ghost-button !py-1 !px-2.5 text-xs flex items-center gap-1" @click="loadHistory">
                <RefreshCw class="w-3 h-3" /> 刷新
              </button>
              <button class="ghost-button !py-1 !px-2.5 text-xs text-rose-300/80" @click="clearHistory">清空</button>
            </div>
            <div class="overflow-auto flex-1" v-loading="historyLoading">
              <div
                v-for="h in filteredHistory"
                :key="h.id"
                class="group px-4 py-2.5 border-b border-white/5 hover:bg-white/5 cursor-pointer"
                @click="reuseHistory(h)"
              >
                <div class="flex items-center gap-2 text-xs mb-1">
                  <span :class="h.status === 1 ? 'bg-emerald-400/15 text-emerald-300' : 'bg-rose-400/15 text-rose-300'" class="px-1.5 py-0.5 rounded-full">
                    {{ h.status === 1 ? '成功' : '失败' }}
                  </span>
                  <span class="text-white/45">{{ h.connection_name || `#${h.connection_id}` }}</span>
                  <span v-if="h.database_name" class="text-white/35">{{ h.database_name }}</span>
                  <span class="text-white/30">{{ h.execution_time_ms ?? 0 }} ms</span>
                  <span class="flex-1" />
                  <span class="text-white/30">{{ formatTime(h.created_at) }}</span>
                  <button
                    class="opacity-0 group-hover:opacity-100 text-white/40 hover:text-rose-300 transition-opacity"
                    aria-label="删除该历史"
                    @click.stop="removeHistory(h.id)"
                  >
                    <Trash2 class="w-3.5 h-3.5" />
                  </button>
                </div>
                <p class="font-mono text-xs truncate" :class="h.status === 1 ? 'text-indigo-200/80' : 'text-rose-200/80'">{{ h.sql_text }}</p>
                <p v-if="h.error_message" class="text-[11px] text-rose-300/70 truncate mt-0.5">{{ h.error_message }}</p>
              </div>
              <p v-if="!filteredHistory.length && !historyLoading" class="text-center text-xs text-white/35 py-10">暂无查询历史</p>
            </div>
          </el-tab-pane>

          <!-- Redis 浏览 -->
          <el-tab-pane :label="viewMode === 'redis' ? 'Redis' : 'Redis'" name="redis" class="flex flex-col min-h-0 flex-1">
            <div v-if="viewMode !== 'redis'" class="flex-1 flex items-center justify-center text-sm text-white/35">
              在左侧选择 Redis 数据源的「服务器概览」或「键空间浏览」
            </div>
            <div v-else class="overflow-auto flex-1" v-loading="redisLoading">
              <!-- 概览 -->
              <div v-if="redisView === 'overview' && redisOverview" class="p-4 grid grid-cols-2 sm:grid-cols-4 gap-3">
                <div v-for="card in redisStatCards" :key="card.label" class="rounded-xl bg-white/5 border border-white/10 p-3">
                  <p class="text-[11px] text-white/45">{{ card.label }}</p>
                  <p class="text-lg font-semibold mt-1">{{ card.value }}</p>
                </div>
              </div>
              <!-- 键列表 -->
              <div v-else-if="redisView === 'keys'" class="p-3">
                <div class="flex items-center gap-2 mb-2">
                  <input v-model.trim="redisPattern" class="glass-input !py-1.5 text-xs flex-1" placeholder="键匹配模式，如 user:*" @keydown.enter="loadRedisKeys" />
                  <button class="ghost-button !py-1.5 !px-3 text-xs" @click="loadRedisKeys">扫描</button>
                </div>
                <table class="w-full text-xs">
                  <thead class="text-white/45 sticky top-0 bg-white/10">
                    <tr>
                      <th class="text-left px-3 py-2 font-medium">键</th>
                      <th class="text-left px-3 py-2 font-medium w-20">类型</th>
                      <th class="text-left px-3 py-2 font-medium w-20">TTL(s)</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="k in redisKeys" :key="k.key" class="border-b border-white/5 hover:bg-white/5 cursor-pointer" @click="inspectRedisKey(k.key)">
                      <td class="px-3 py-2 font-mono break-all">{{ k.key }}</td>
                      <td class="px-3 py-2 text-violet-300">{{ k.type }}</td>
                      <td class="px-3 py-2 text-white/50">{{ k.ttl === -1 ? '永久' : k.ttl === -2 ? '已过期' : k.ttl }}</td>
                    </tr>
                  </tbody>
                </table>
                <p class="text-[11px] text-white/35 px-3 py-2">SCAN 最多返回前 500 个键；点击键查看内容预览</p>
              </div>
              <!-- 键值 -->
              <div v-else-if="redisView === 'value' && redisValue" class="p-4 space-y-2">
                <p class="text-xs text-white/55">
                  类型 <span class="text-violet-300">{{ redisValue.type }}</span> ·
                  元素数/长度 {{ redisValue.size }} ·
                  TTL {{ redisValue.ttl === -1 ? '永久' : redisValue.ttl + ' s' }}
                </p>
                <pre class="text-xs font-mono bg-black/30 rounded-xl p-3 overflow-auto max-h-48 whitespace-pre-wrap break-all">{{ redisValueText }}</pre>
                <div class="flex gap-2">
                  <button class="ghost-button !py-1 !px-3 text-xs" @click="redisView = 'keys'">← 返回键列表</button>
                  <button class="ghost-button !py-1 !px-3 text-xs flex items-center gap-1" @click="copyRedisValue"><Copy class="w-3 h-3" /> 复制</button>
                </div>
              </div>
            </div>
          </el-tab-pane>

          <!-- 消息 -->
          <el-tab-pane label="消息" name="message" class="flex flex-col min-h-0 flex-1">
            <div class="p-4 text-sm font-mono space-y-1 overflow-auto flex-1">
              <p v-if="!messages.length" class="text-white/35">编辑器就绪，等待执行…</p>
              <p v-for="(m, i) in messages" :key="i" :class="m.level === 'error' ? 'text-rose-300' : m.level === 'success' ? 'text-emerald-300' : 'text-white/50'">
                [{{ m.time }}] {{ m.text }}
              </p>
            </div>
          </el-tab-pane>
        </el-tabs>

        <footer class="flex items-center gap-4 sm:gap-5 px-4 py-2 border-t border-white/10 text-xs text-white/45 shrink-0">
          <span v-if="lastDuration !== null">执行耗时：<span class="text-emerald-300">{{ lastDuration }} ms</span></span>
          <span v-if="grid.columns.length">行数：<span class="text-indigo-200">{{ grid.rows.length }}</span></span>
          <span v-if="currentConn?.type === 'redis'" class="text-rose-300/80">Redis 模式</span>
          <span v-if="currentConn?.environment === 'prod'" class="text-rose-300/80 flex items-center gap-1"><ShieldAlert class="w-3 h-3" /> 生产环境</span>
          <span class="flex-1" />
          <span v-if="isReadonly" class="text-amber-300/80">只读角色：写操作将被拒绝</span>
          <span class="hidden sm:inline">UTF-8</span>
        </footer>
      </div>
    </section>

    <AiAssistantPanel v-if="aiVisible && !resultFullscreen" class="hidden xl:flex w-72 shrink-0" :current-connection="currentConn" :current-sql="currentTab.sql" :tables="allTableNames" @close="aiVisible = false" @insert-sql="onAiInsert" />
    <button
      v-if="!aiVisible && !resultFullscreen"
      class="hidden xl:flex w-10 shrink-0 glass-panel items-center justify-center text-white/50 hover:text-white transition-colors"
      aria-label="展开 AI 助手"
      @click="aiVisible = true"
    >
      <PanelRightOpen class="w-5 h-5" />
    </button>
    <el-drawer v-model="aiDrawerOpen" title="AI 助手" direction="rtl" size="85%" class="glass-drawer">
      <AiAssistantPanel embedded :closable="false" :current-connection="currentConn" :current-sql="currentTab.sql" :tables="allTableNames" @insert-sql="onAiInsert" />
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  CheckCircle2,
  Copy,
  Database,
  FileCode2,
  FolderTree,
  KeyRound,
  Keyboard,
  PanelRightOpen,
  Play,
  Plus,
  RefreshCw,
  Search,
  ShieldAlert,
  Sparkles,
  Table2,
  Trash2,
  Wand2,
  Waypoints,
} from 'lucide-vue-next'
import ConnectionTreePanel from './components/ConnectionTreePanel.vue'
import AiAssistantPanel from './components/AiAssistantPanel.vue'
import ResultChartPane from './components/ResultChartPane.vue'
import SqlMonaco from './components/SqlMonaco.vue'
import ResultGrid from './components/ResultGrid.vue'
import type { ConnectionItem, DbType } from '../../api/datasource'
import {
  workbenchApi,
  type ExecResult,
  type QueryHistoryItem,
  type RedisKey,
  type RedisOverview,
  type RedisValue,
  type TableInfo,
} from '../../api/workbench'
import { useUserStore } from '../../stores/user'

const route = useRoute()
const userStore = useUserStore()
const isReadonly = computed(() => userStore.role === 'readonly')
const treeRef = ref<InstanceType<typeof ConnectionTreePanel> | null>(null)

interface EditorTab {
  id: number
  name: string
  database: string
  sql: string
}
let tabSeq = 1
function newTab(sql = ''): EditorTab {
  tabSeq += 1
  return { id: tabSeq, name: `SQL Editor ${tabSeq}`, database: '', sql }
}

const STORAGE_TABS = 'dbhub_query_tabs_v2'
const STORAGE_SPLIT = 'dbhub_query_split'

function loadTabsFromStorage(): { tabs: EditorTab[]; active: number } | null {
  try {
    const raw = localStorage.getItem(STORAGE_TABS)
    if (!raw) return null
    const parsed = JSON.parse(raw)
    if (Array.isArray(parsed.tabs) && parsed.tabs.length) {
      const maxId = Math.max(...parsed.tabs.map((t: any) => t.id), 0)
      tabSeq = Math.max(tabSeq, maxId)
      return parsed
    }
  } catch {}
  return null
}

const stored = loadTabsFromStorage()
const tabs = ref<EditorTab[]>(stored?.tabs ?? [{ id: 1, name: 'SQL Editor 1', database: '', sql: '' }])
const activeTab = ref(stored?.active ?? 1)
const currentTab = computed<EditorTab>(
  () => tabs.value.find((t) => t.id === activeTab.value) ?? tabs.value[0]!,
)

watch(
  [tabs, activeTab],
  () => {
    try {
      localStorage.setItem(STORAGE_TABS, JSON.stringify({ tabs: tabs.value, active: activeTab.value }))
    } catch {}
  },
  { deep: true },
)

const currentConn = ref<ConnectionItem | null>(null)
const databaseOptions = ref<{ name: string }[]>([])
const allTableNames = ref<string[]>([])
const allColumnNames = ref<string[]>([])
const running = ref(false)

const resultTab = ref('result')
const viewMode = ref<'sql' | 'preview' | 'redis'>('sql')
const messages = ref<{ time: string; text: string; level: 'info' | 'success' | 'error' }[]>([])
const lastDuration = ref<number | null>(null)

const grid = reactive<{ columns: string[]; rows: unknown[][]; truncated: boolean }>({
  columns: [],
  rows: [],
  truncated: false,
})
const writeResult = ref<ExecResult | null>(null)
const preview = reactive({
  schema: '',
  database: '',
  table: '',
  page: 1,
  pageSize: 50,
  total: 0,
  hasMore: false,
})

const aiVisible = ref(true)
const treeDrawerOpen = ref(false)
const aiDrawerOpen = ref(false)
const resultFullscreen = ref(false)

// 分割线
const editorHeight = ref<number>(55)
try {
  const v = Number(localStorage.getItem(STORAGE_SPLIT))
  if (v >= 20 && v <= 80) editorHeight.value = v
} catch {}

function startSplitterDrag(e: MouseEvent) {
  const startY = e.clientY
  const startH = editorHeight.value
  const container = (e.currentTarget as HTMLElement).parentElement
  const containerHeight = container?.clientHeight || window.innerHeight * 0.8
  const onMove = (ev: MouseEvent) => {
    const delta = ev.clientY - startY
    const deltaPct = (delta / containerHeight) * 100
    let newH = startH + deltaPct
    newH = Math.max(20, Math.min(80, newH))
    editorHeight.value = newH
  }
  const onUp = () => {
    window.removeEventListener('mousemove', onMove)
    window.removeEventListener('mouseup', onUp)
    try {
      localStorage.setItem(STORAGE_SPLIT, String(editorHeight.value))
    } catch {}
  }
  window.addEventListener('mousemove', onMove)
  window.addEventListener('mouseup', onUp)
}

// Monaco
const monacoReady = ref(false)
const monacoRefs = reactive<Record<number, any>>({})

onMounted(() => {
  // 懒加载检测
  import('monaco-editor')
    .then(() => {
      monacoReady.value = true
    })
    .catch(() => {
      monacoReady.value = false
    })
})

function connColor(t: DbType) {
  return { mysql: '#60a5fa', postgres: '#a78bfa', redis: '#f472b6' }[t] ?? '#94a3b8'
}

function log(text: string, level: 'info' | 'success' | 'error' = 'info') {
  const time = new Date().toLocaleTimeString('zh-CN', { hour12: false })
  messages.value.unshift({ time, text, level })
}

function resetGrid() {
  grid.columns = []
  grid.rows = []
  grid.truncated = false
  writeResult.value = null
}

function onSelectConnection(conn: ConnectionItem) {
  currentConn.value = conn
  viewMode.value = conn.type === 'redis' ? 'redis' : 'sql'
  if (conn.type === 'redis') {
    resultTab.value = 'redis'
    redisView.value = 'overview'
    loadRedisOverview(conn)
    return
  }
  resultTab.value = 'result'
  databaseOptions.value = []
  allTableNames.value = []
}

function onDatabasesLoaded({ conn, items }: { conn: ConnectionItem; items: { name: string }[] }) {
  if (currentConn.value?.id !== conn.id) return
  databaseOptions.value = items
  const preferred = items.find((d) => d.name === conn.database) ?? items[0]
  if (preferred && !currentTab.value.database) {
    currentTab.value.database = preferred.name
  }
}

function onTablesLoaded({ tables }: { tables: TableInfo[] }) {
  allTableNames.value = tables.map((t) => t.name)
}

async function formatCurrent() {
  const tab = currentTab.value
  if (!tab.sql.trim()) return
  try {
    const { format } = await import('sql-formatter')
    const formatted = format(tab.sql, { language: 'postgresql', tabWidth: 2, keywordCase: 'upper' })
    tab.sql = formatted
    ElMessage.success({ message: '已格式化', duration: 1000 })
  } catch {
    ElMessage.warning('格式化失败')
  }
}

function isDangerousSQL(sql: string) {
  const up = sql.toUpperCase()
  return /\b(DELETE|UPDATE|DROP|TRUNCATE|ALTER)\b/.test(up)
}

async function runQuery() {
  if (!currentConn.value) {
    ElMessage.warning('请先在左侧选择数据源')
    return
  }
  if (currentConn.value.type === 'redis') {
    ElMessage.info('Redis 不支持 SQL，请使用 Redis 浏览页签')
    return
  }
  const sql = currentTab.value.sql.trim()
  if (!sql) {
    ElMessage.warning('SQL 内容不能为空')
    return
  }
  if (currentConn.value.environment === 'prod' && isDangerousSQL(sql)) {
    try {
      await ElMessageBox.confirm(
        `当前连接为生产环境（${currentConn.value.name}），即将执行写操作：\n${sql.slice(0, 200)}\n\n确认继续？`,
        '生产环境二次确认',
        { type: 'warning', confirmButtonText: '确认执行', cancelButtonText: '取消' },
      )
    } catch {
      return
    }
  }
  running.value = true
  resetGrid()
  viewMode.value = 'sql'
  resultTab.value = 'result'
  log(`开始执行：${sql.replace(/\s+/g, ' ').slice(0, 80)}`)
  try {
    const res = await workbenchApi.execute(currentConn.value.id, sql, currentTab.value.database)
    lastDuration.value = res.duration_ms
    if (res.kind === 'query') {
      grid.columns = res.columns ?? []
      grid.rows = res.rows ?? []
      grid.truncated = Boolean(res.truncated)
      log(`查询成功，返回 ${grid.rows.length} 行，耗时 ${res.duration_ms} ms`, 'success')
    } else {
      writeResult.value = res
      log(`执行成功，影响 ${res.affected_rows ?? 0} 行，耗时 ${res.duration_ms} ms`, 'success')
    }
  } catch (err) {
    log(err instanceof Error ? err.message : '执行失败', 'error')
    resultTab.value = 'message'
  } finally {
    running.value = false
    if (resultTab.value === 'history') loadHistory()
  }
}

async function onPreviewTable(payload: {
  conn: ConnectionItem
  database: string
  schema: string
  table: TableInfo
}) {
  currentConn.value = payload.conn
  viewMode.value = 'preview'
  resultTab.value = 'result'
  Object.assign(preview, {
    schema: payload.schema || payload.database,
    database: payload.database,
    table: payload.table.name,
    page: 1,
  })
  currentTab.value.database = payload.database
  if (!databaseOptions.value.find((d) => d.name === payload.database)) {
    databaseOptions.value = [{ name: payload.database }, ...databaseOptions.value]
  }
  await loadPreview()
}

async function loadPreview() {
  if (!currentConn.value) return
  resetGrid()
  const isPg = currentConn.value.type === 'postgres'
  try {
    const res = await workbenchApi.preview(currentConn.value.id, {
      database: isPg ? preview.database : preview.database,
      schema: isPg ? preview.schema || undefined : undefined,
      table: preview.table,
      page: preview.page,
      page_size: preview.pageSize,
    })
    grid.columns = res.columns
    grid.rows = res.rows
    preview.total = res.total
    preview.hasMore = res.has_more
    // 尝试加载列名用于补全
    try {
      const cols = await workbenchApi.columns(currentConn.value.id, {
        database: preview.database,
        schema: preview.schema || undefined,
        table: preview.table,
      })
      allColumnNames.value = cols.items.map((c) => c.name)
    } catch {}
  } catch {
    /* 拦截器已提示 */
  }
}

function previewPage(delta: number) {
  preview.page = Math.max(1, preview.page + delta)
  loadPreview()
}

// ---------- 历史 ----------
const history = ref<QueryHistoryItem[]>([])
const historyLoading = ref(false)
const historyFilter = ref('all')
const historyKeyword = ref('')

const filteredHistory = computed(() => {
  const kw = historyKeyword.value.toLowerCase()
  if (!kw) return history.value
  return history.value.filter((h) => h.sql_text.toLowerCase().includes(kw) || (h.database_name || '').toLowerCase().includes(kw))
})

function onResultTabChange(name: string | number) {
  if (name === 'history') loadHistory()
}

async function loadHistory() {
  historyLoading.value = true
  try {
    const res = await workbenchApi.history({
      status: historyFilter.value === 'all' ? '' : historyFilter.value,
      page: 1,
      page_size: 100,
    })
    history.value = res.items
  } finally {
    historyLoading.value = false
  }
}
function reuseHistory(h: QueryHistoryItem) {
  const tab = tabs.value.find((t) => t.id === activeTab.value) ?? tabs.value[0]!
  tab.sql = h.sql_text
  if (h.database_name) tab.database = h.database_name
  resultTab.value = 'result'
  ElMessage.success('SQL 已回填到编辑器')
}
async function removeHistory(id: number) {
  await workbenchApi.deleteHistory(id)
  loadHistory()
}
async function clearHistory() {
  try {
    await ElMessageBox.confirm('确认清空你的全部查询历史？', '清空确认', {
      type: 'warning',
      confirmButtonText: '清空',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  await workbenchApi.clearHistory()
  ElMessage.success('已清空')
  loadHistory()
}

// ---------- Redis ----------
const redisLoading = ref(false)
const redisView = ref<'overview' | 'keys' | 'value'>('overview')
const redisOverview = ref<RedisOverview | null>(null)
const redisKeys = ref<RedisKey[]>([])
const redisValue = ref<RedisValue | null>(null)
const redisPattern = ref('*')

const redisStatCards = computed(() => {
  const o = redisOverview.value
  if (!o) return []
  return [
    { label: '版本', value: o.version || '-' },
    { label: '运行模式', value: o.mode || '-' },
    { label: '运行天数', value: o.uptime_days },
    { label: '连接客户端', value: o.connected_clients },
    { label: '内存占用 (MB)', value: o.used_memory_mb },
    { label: '累计命令数', value: o.total_commands },
    ...o.keyspaces.map((k) => ({ label: `${k.db} 键数量`, value: k.keys })),
  ]
})

async function onRedisOverview(conn: ConnectionItem) {
  currentConn.value = conn
  viewMode.value = 'redis'
  redisView.value = 'overview'
  resultTab.value = 'redis'
  await loadRedisOverview(conn)
}
async function loadRedisOverview(conn: ConnectionItem) {
  redisLoading.value = true
  try {
    redisOverview.value = await workbenchApi.redisOverview(conn.id)
  } finally {
    redisLoading.value = false
  }
}
async function onRedisKeys(conn: ConnectionItem) {
  currentConn.value = conn
  viewMode.value = 'redis'
  redisView.value = 'keys'
  resultTab.value = 'redis'
  await loadRedisKeys()
}
async function loadRedisKeys() {
  if (!currentConn.value) return
  redisLoading.value = true
  try {
    const res = await workbenchApi.redisKeys(currentConn.value.id, redisPattern.value || '*')
    redisKeys.value = res.items
  } finally {
    redisLoading.value = false
  }
}
async function inspectRedisKey(key: string) {
  if (!currentConn.value) return
  redisLoading.value = true
  try {
    redisValue.value = await workbenchApi.redisValue(currentConn.value.id, key)
    redisView.value = 'value'
  } finally {
    redisLoading.value = false
  }
}
const redisValueText = computed(() => {
  const v = redisValue.value?.value
  if (v === null || v === undefined) return '(nil)'
  if (typeof v === 'string') return v
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
})
async function copyRedisValue() {
  try {
    await navigator.clipboard.writeText(redisValueText.value)
    ElMessage.success('已复制')
  } catch {
    ElMessage.warning('复制失败')
  }
}

// ---------- Tab 管理 ----------
function addTab() {
  const t = newTab()
  tabs.value.push(t)
  activeTab.value = t.id
}
function closeTab(id: number) {
  const idx = tabs.value.findIndex((t) => t.id === id)
  if (idx === -1) return
  tabs.value.splice(idx, 1)
  if (tabs.value.length === 0) {
    tabs.value.push(newTab())
  }
  if (activeTab.value === id) {
    const neighbor = tabs.value[Math.max(0, idx - 1)] ?? tabs.value[0]!
    activeTab.value = neighbor.id
  }
}

function onGenerateSelect(payload: { conn: ConnectionItem; database: string; schema: string; table: TableInfo }) {
  const sch = payload.schema ? `${payload.schema}.` : ''
  const qualified = payload.conn.type === 'postgres' ? `${payload.database}.${sch}${payload.table.name}` : `${payload.database}.${payload.table.name}`
  const sql = `SELECT * FROM ${qualified} LIMIT 100;`
  const tab = currentTab.value
  const monaco = monacoRefs[tab.id]
  if (monaco) {
    monaco.insertText(sql)
  } else {
    tab.sql = tab.sql ? `${tab.sql}\n${sql}` : sql
  }
  currentTab.value.database = payload.database
  ElMessage.success(`已生成 SELECT：${payload.table.name}`)
}

function onAiInsert(sql: string) {
  const tab = currentTab.value
  const monaco = monacoRefs[tab.id]
  if (monaco) {
    monaco.insertText(sql)
  } else {
    tab.sql += (tab.sql ? '\n' : '') + sql
  }
  ElMessage.success('已插入到编辑器')
}

function formatTime(s: string): string {
  const d = new Date(s)
  const today = new Date()
  const sameDay = d.toDateString() === today.toDateString()
  return d.toLocaleTimeString('zh-CN', { hour12: false, ...(sameDay ? {} : { month: '2-digit', day: '2-digit' }) })
}

onMounted(async () => {
  const preselect = Number(route.query.connection)
  await treeRef.value?.whenLoaded()
  if (preselect > 0) {
    const conn = treeRef.value?.findConnection(preselect)
    if (conn) onSelectConnection(conn)
  }
})
</script>

<style scoped>
.query-tabs {
  padding: 0 0.5rem;
}
.query-tabs :deep(.el-tabs__content) {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.query-tabs :deep(.el-tab-pane) {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
</style>
