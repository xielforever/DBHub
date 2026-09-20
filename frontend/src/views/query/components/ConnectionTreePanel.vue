<template>
  <aside class="glass-panel w-full flex flex-col overflow-hidden">
    <div class="px-3 py-2.5 border-b border-white/10 flex flex-col gap-2">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-medium flex items-center gap-2">
          <FolderTree class="w-4 h-4 text-indigo-300" /> 数据源
        </h2>
        <div class="flex items-center gap-1">
          <button class="text-white/40 hover:text-white p-1 rounded-lg hover:bg-white/10" title="全部折叠" @click="collapseAll">
            <ChevronsUpDown class="w-3.5 h-3.5" />
          </button>
          <button class="text-white/40 hover:text-white p-1 rounded-lg hover:bg-white/10" aria-label="刷新连接树" @click="reload(true)">
            <RefreshCw class="w-3.5 h-3.5" :class="{ 'animate-spin': loading }" />
          </button>
        </div>
      </div>
      <div class="relative">
        <Search class="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-white/30" />
        <input v-model.trim="keyword" class="glass-input !py-1.5 !pl-8 text-xs w-full" placeholder="搜索连接/库/表" />
      </div>
      <!-- 收藏与最近 -->
      <div v-if="favorites.length" class="mt-1 rounded-xl bg-amber-500/5 border border-amber-400/10 p-2">
        <div class="flex items-center justify-between mb-1">
          <span class="text-[11px] text-amber-300/70 flex items-center gap-1"><Star class="w-3 h-3" /> 收藏的表</span>
          <button class="text-[10px] text-white/30 hover:text-white/60" @click="clearFavorites">清空</button>
        </div>
        <div class="space-y-0.5">
          <button v-for="fav in favorites" :key="fav.key" class="tree-row !py-1 text-[11px]" @click="jumpFavorite(fav)" draggable="true" @dragstart="onDragTable($event, fav.name)">
            <Table2 class="w-3 h-3 text-amber-300" /><span class="truncate">{{ fav.name }}</span><span class="text-[10px] text-white/30 truncate">{{ fav.db }}</span>
          </button>
        </div>
      </div>
      <div v-if="recentTables.length" class="mt-1 rounded-xl bg-white/5 border border-white/10 p-2">
        <div class="flex items-center justify-between mb-1">
          <span class="text-[11px] text-white/40 flex items-center gap-1"><Clock3 class="w-3 h-3" /> 最近浏览</span>
          <button class="text-[10px] text-white/30 hover:text-white/60" @click="clearRecent">清空</button>
        </div>
        <div class="space-y-0.5">
          <button v-for="rt in recentTables.slice(0,5)" :key="rt.key" class="tree-row !py-1 text-[11px]" @click="jumpFavorite(rt)" draggable="true" @dragstart="onDragTable($event, rt.name)">
            <History class="w-3 h-3 text-white/30" /><span class="truncate">{{ rt.name }}</span><span class="text-[10px] text-white/30 truncate">{{ rt.db }}</span>
          </button>
        </div>
      </div>
    </div>

    <div class="flex-1 overflow-y-auto p-2" v-loading="loading">
      <div v-if="!filteredConnections.length && !loading" class="text-center text-xs text-white/40 px-4 py-10">
        {{ keyword ? '无匹配结果' : '暂无数据源' }}<br v-if="!keyword" /><span v-if="!keyword" class="text-[11px]">请先在「数据源管理」中创建连接</span>
      </div>

      <div v-for="conn in filteredConnections" :key="conn.id" class="mb-0.5">
        <!-- 连接根节点 -->
        <button
          class="tree-row w-full group"
          :class="{ 'bg-white/10': activeConnId === conn.id }"
          @click="toggleConnection(conn)"
        >
          <ChevronRight class="w-3.5 h-3.5 text-white/40 transition-transform shrink-0" :class="{ 'rotate-90': expandedConns.has(conn.id) }" />
          <component :is="typeIcon(conn.type)" class="w-4 h-4 shrink-0" :style="{ color: typeColor(conn.type) }" />
          <span class="truncate text-left flex-1">{{ conn.name }}</span>
          <span v-if="conn.environment === 'prod'" class="px-1 py-0.5 rounded text-[9px] bg-rose-500/20 text-rose-300 shrink-0">PROD</span>
          <span v-if="conn.proxy_name" class="px-1 py-0.5 rounded text-[9px] bg-violet-500/20 text-violet-300 flex items-center gap-0.5 shrink-0"><Waypoints class="w-2.5 h-2.5" />{{ shortProxy(conn.proxy_name) }}</span>
        </button>

        <div v-if="expandedConns.has(conn.id)" class="ml-4 border-l border-white/10 pl-1">
          <p v-if="errors[conn.id]" class="text-[11px] text-rose-300/80 px-2 py-1">{{ errors[conn.id] }}</p>

          <template v-else-if="conn.type === 'redis'">
            <button class="tree-row" @click="emitRedisOverview(conn)">
              <Activity class="w-3.5 h-3.5 text-rose-300" /><span>服务器概览</span>
            </button>
            <button class="tree-row" @click="emitRedisKeys(conn)">
              <KeyRound class="w-3.5 h-3.5 text-amber-300" /><span>键空间浏览</span>
            </button>
          </template>

          <template v-else>
            <div v-if="loadingConns[conn.id]" class="text-[11px] text-white/40 px-2 py-1">加载中…</div>
            <template v-else>
              <!-- PostgreSQL -->
              <template v-if="conn.type === 'postgres'">
                <template v-for="db in filteredDbs(conn.id)" :key="db.name">
                  <button class="tree-row" @click="toggleSchemaNode(conn, db.name, null)">
                    <ChevronRight class="w-3 h-3 text-white/35 transition-transform" :class="{ 'rotate-90': schemaOpen(conn, db.name, null) }" />
                    <Database class="w-3.5 h-3.5 text-sky-300" /><span class="truncate">{{ db.name }}</span>
                  </button>
                  <div v-if="schemaOpen(conn, db.name, null)" class="ml-4 border-l border-white/10 pl-1">
                    <template v-for="sch in schemas[`${conn.id}:${db.name}`] || []" :key="sch">
                      <button class="tree-row" @click="toggleTables(conn, db.name, sch)">
                        <ChevronRight class="w-3 h-3 text-white/35 transition-transform" :class="{ 'rotate-90': tablesOpen(conn, db.name, sch) }" />
                        <Folder class="w-3.5 h-3.5 text-violet-300" /><span class="truncate">{{ sch }}</span>
                      </button>
                      <div v-if="tablesOpen(conn, db.name, sch)" class="ml-4 border-l border-white/10 pl-1">
                        <div
                          v-for="t in filteredTables(conn.id, db.name, sch)"
                          :key="t.name"
                          class="group/table flex items-center"
                        >
                          <button
                            class="tree-row flex-1"
                            :class="{ 'bg-indigo-500/20 text-indigo-200': isActiveTable(conn.id, db.name, sch, t.name) }"
                            draggable="true"
                            @dragstart="onDragTable($event, t.name)"
                            @click="emitTable(conn, db.name, sch, t)"
                            @contextmenu.prevent="openContextMenu($event, conn, db.name, sch, t)"
                          >
                            <component :is="t.type === 'view' ? Eye : Table2" class="w-3.5 h-3.5" :class="t.type === 'view' ? 'text-white/35' : 'text-emerald-300'" />
                            <span class="truncate">{{ t.name }}</span>
                          </button>
                          <button class="opacity-0 group-hover/table:opacity-100 p-1 text-amber-300/60 hover:text-amber-300" :class="{ 'opacity-100': isFavorite(conn.id, db.name, sch, t.name) }" @click.stop="toggleFavorite(conn, db.name, sch, t)">
                            <Star class="w-3 h-3" :class="{ 'fill-amber-300': isFavorite(conn.id, db.name, sch, t.name) }" />
                          </button>
                          <button class="opacity-0 group-hover/table:opacity-100 p-1 text-white/30 hover:text-white" @click.stop="openContextMenu($event, conn, db.name, sch, t)">
                            <MoreHorizontal class="w-3 h-3" />
                          </button>
                        </div>
                      </div>
                    </template>
                  </div>
                </template>
              </template>

              <!-- MySQL -->
              <template v-else>
                <template v-for="db in filteredDbs(conn.id)" :key="db.name">
                  <button class="tree-row" @click="toggleTables(conn, db.name, '')">
                    <ChevronRight class="w-3 h-3 text-white/35 transition-transform" :class="{ 'rotate-90': tablesOpen(conn, db.name, '') }" />
                    <Database class="w-3.5 h-3.5 text-sky-300" /><span class="truncate">{{ db.name }}</span>
                  </button>
                  <div v-if="tablesOpen(conn, db.name, '')" class="ml-4 border-l border-white/10 pl-1">
                    <div v-for="t in filteredTables(conn.id, db.name, '')" :key="t.name" class="group/table flex items-center">
                      <button
                        class="tree-row flex-1"
                        :class="{ 'bg-indigo-500/20 text-indigo-200': isActiveTable(conn.id, db.name, '', t.name) }"
                        draggable="true"
                        @dragstart="onDragTable($event, t.name)"
                        @click="emitTable(conn, db.name, '', t)"
                        @contextmenu.prevent="openContextMenu($event, conn, db.name, '', t)"
                      >
                        <component :is="t.type === 'view' ? Eye : Table2" class="w-3.5 h-3.5" :class="t.type === 'view' ? 'text-white/35' : 'text-emerald-300'" />
                        <span class="truncate">{{ t.name }}</span>
                      </button>
                      <button class="opacity-0 group-hover/table:opacity-100 p-1 text-amber-300/60 hover:text-amber-300" :class="{ 'opacity-100': isFavorite(conn.id, db.name, '', t.name) }" @click.stop="toggleFavorite(conn, db.name, '', t)">
                        <Star class="w-3 h-3" :class="{ 'fill-amber-300': isFavorite(conn.id, db.name, '', t.name) }" />
                      </button>
                      <button class="opacity-0 group-hover/table:opacity-100 p-1 text-white/30 hover:text-white" @click.stop="openContextMenu($event, conn, db.name, '', t)">
                        <MoreHorizontal class="w-3 h-3" />
                      </button>
                    </div>
                  </div>
                </template>
              </template>
            </template>
          </template>
        </div>
      </div>
    </div>

    <!-- 右键菜单 -->
    <div
      v-if="contextMenu.visible"
      class="fixed z-[9999] glass-panel !p-1 min-w-[200px] shadow-2xl border border-white/15"
      :style="{ left: contextMenu.x + 'px', top: contextMenu.y + 'px' }"
      @mouseleave="contextMenu.visible = false"
    >
      <button class="w-full text-left px-3 py-1.5 text-xs hover:bg-white/10 rounded-lg flex items-center gap-2" @click="ctxPreview">
        <Eye class="w-3.5 h-3.5" /> 预览前 50 行
      </button>
      <button class="w-full text-left px-3 py-1.5 text-xs hover:bg-white/10 rounded-lg flex items-center gap-2" @click="ctxSelect">
        <Code2 class="w-3.5 h-3.5" /> 生成 SELECT
      </button>
      <button class="w-full text-left px-3 py-1.5 text-xs hover:bg-white/10 rounded-lg flex items-center gap-2" @click="ctxInsertName">
        <Plus class="w-3.5 h-3.5" /> 插入表名到编辑器
      </button>
      <button class="w-full text-left px-3 py-1.5 text-xs hover:bg-white/10 rounded-lg flex items-center gap-2" @click="ctxToggleFav">
        <Star class="w-3.5 h-3.5" /> {{ isFavorite(contextMenu.conn?.id || 0, contextMenu.database, contextMenu.schema, contextMenu.table?.name || '') ? '取消收藏' : '收藏表' }}
      </button>
      <div class="h-px bg-white/10 my-1" />
      <button class="w-full text-left px-3 py-1.5 text-xs hover:bg-white/10 rounded-lg flex items-center gap-2" @click="ctxCopyName">
        <Copy class="w-3.5 h-3.5" /> 复制表名
      </button>
      <button class="w-full text-left px-3 py-1.5 text-xs hover:bg-white/10 rounded-lg flex items-center gap-2" @click="ctxCopyQualified">
        <Copy class="w-3.5 h-3.5" /> 复制全限定名
      </button>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, reactive, ref, shallowReactive } from 'vue'
import {
  Activity,
  ChevronRight,
  ChevronsUpDown,
  Clock3,
  Code2,
  Copy,
  Database,
  Eye,
  Folder,
  FolderTree,
  History,
  KeyRound,
  MoreHorizontal,
  Plus,
  RefreshCw,
  Search,
  Star,
  Table2,
  Waypoints,
} from 'lucide-vue-next'
import { connectionApi, type ConnectionItem, type DbType } from '../../../api/datasource'
import { workbenchApi, type TableInfo } from '../../../api/workbench'

const emit = defineEmits<{
  (e: 'select-connection', conn: ConnectionItem): void
  (e: 'databases-loaded', payload: { conn: ConnectionItem; items: { name: string }[] }): void
  (e: 'tables-loaded', payload: { conn: ConnectionItem; database: string; schema: string; tables: TableInfo[] }): void
  (e: 'preview-table', payload: { conn: ConnectionItem; database: string; schema: string; table: TableInfo }): void
  (e: 'redis-overview', conn: ConnectionItem): void
  (e: 'redis-keys', conn: ConnectionItem): void
  (e: 'generate-select', payload: { conn: ConnectionItem; database: string; schema: string; table: TableInfo }): void
  (e: 'insert-table-name', name: string): void
}>()

const connections = ref<ConnectionItem[]>([])
const loading = ref(false)
const loadingConns = reactive<Record<number, boolean>>({})
const errors = reactive<Record<number, string>>({})
const expandedConns = ref<Set<number>>(new Set())
const activeConnId = ref<number | null>(null)
const activeTableKey = ref('')
const keyword = ref('')

const dbs = shallowReactive<Record<number, { name: string }[]>>({})
const schemas = shallowReactive<Record<string, string[]>>({})
const tables = shallowReactive<Record<string, TableInfo[]>>({})
const openSchemaNodes = ref<Set<string>>(new Set())
const openTableNodes = ref<Set<string>>(new Set())

const contextMenu = reactive({
  visible: false,
  x: 0,
  y: 0,
  conn: null as ConnectionItem | null,
  database: '',
  schema: '',
  table: null as TableInfo | null,
})

// 收藏与最近
interface FavItem { key: string; name: string; db: string; schema: string; connId: number; conn: ConnectionItem; table: TableInfo }
const STORAGE_FAV = 'dbhub_table_fav'
const STORAGE_RECENT = 'dbhub_table_recent'
const favorites = ref<FavItem[]>([])
const recentTables = ref<FavItem[]>([])
try {
  const rawFav = localStorage.getItem(STORAGE_FAV)
  if (rawFav) favorites.value = JSON.parse(rawFav)
  const rawRecent = localStorage.getItem(STORAGE_RECENT)
  if (rawRecent) recentTables.value = JSON.parse(rawRecent)
} catch {}
function persistFav() {
  try { localStorage.setItem(STORAGE_FAV, JSON.stringify(favorites.value.slice(0, 30))) } catch {}
}
function persistRecent() {
  try { localStorage.setItem(STORAGE_RECENT, JSON.stringify(recentTables.value.slice(0, 20))) } catch {}
}
function isFavorite(connId: number, db: string, schema: string, tableName: string) {
  const key = `${connId}:${db}:${schema}:${tableName}`
  return favorites.value.some((f) => f.key === key)
}
function toggleFavorite(conn: ConnectionItem, db: string, schema: string, table: TableInfo) {
  const key = `${conn.id}:${db}:${schema}:${table.name}`
  const idx = favorites.value.findIndex((f) => f.key === key)
  if (idx >= 0) favorites.value.splice(idx, 1)
  else favorites.value.unshift({ key, name: table.name, db, schema, connId: conn.id, conn, table })
  persistFav()
}
function clearFavorites() { favorites.value = []; persistFav() }
function clearRecent() { recentTables.value = []; persistRecent() }
function addRecent(conn: ConnectionItem, db: string, schema: string, table: TableInfo) {
  const key = `${conn.id}:${db}:${schema}:${table.name}`
  recentTables.value = recentTables.value.filter((r) => r.key !== key)
  recentTables.value.unshift({ key, name: table.name, db, schema, connId: conn.id, conn, table })
  recentTables.value = recentTables.value.slice(0, 20)
  persistRecent()
}
function jumpFavorite(fav: FavItem) {
  // 尝试找到连接对象
  const conn = connections.value.find((c) => c.id === fav.connId) || fav.conn
  if (conn) emit('preview-table', { conn, database: fav.db, schema: fav.schema, table: fav.table })
}
function onDragTable(e: DragEvent, name: string) {
  e.dataTransfer?.setData('text/plain', name)
}
function collapseAll() {
  expandedConns.value = new Set()
  openSchemaNodes.value = new Set()
  openTableNodes.value = new Set()
}

let loadingPromise: Promise<void> | null = null
async function reload(force = false) {
  if (loadingPromise && !force) return loadingPromise
  loadingPromise = (async () => {
    loading.value = true
    try {
      const res = await connectionApi.list()
      connections.value = res.items
    } finally {
      loading.value = false
    }
  })()
  await loadingPromise
  loadingPromise = null
}
reload()

const filteredConnections = computed(() => {
  const kw = keyword.value.toLowerCase()
  if (!kw) return connections.value
  return connections.value.filter((c) => {
    if (c.name.toLowerCase().includes(kw)) return true
    const dbList = dbs[c.id] || []
    if (dbList.some((d) => d.name.toLowerCase().includes(kw))) return true
    const prefix = `${c.id}:`
    return Object.keys(tables).some((k) => k.startsWith(prefix) && tables[k]?.some((t) => t.name.toLowerCase().includes(kw)))
  })
})

function filteredDbs(connId: number) {
  const kw = keyword.value.toLowerCase()
  const list = dbs[connId] || []
  if (!kw) return list
  return list.filter((d) => d.name.toLowerCase().includes(kw) || tables[`${connId}:${d.name}:`]?.some((t) => t.name.toLowerCase().includes(kw)) || Object.keys(tables).some((k) => k.startsWith(`${connId}:${d.name}:`) && tables[k]?.some((t) => t.name.toLowerCase().includes(kw))))
}

function filteredTables(connId: number, db: string, sch: string) {
  const key = `${connId}:${db}:${sch}`
  const list = tables[key] || []
  const kw = keyword.value.toLowerCase()
  if (!kw) return list
  return list.filter((t) => t.name.toLowerCase().includes(kw))
}

function shortProxy(name: string) {
  return name.length > 6 ? name.slice(0, 6) : name
}

function typeIcon(t: DbType) {
  return t === 'redis' ? KeyRound : Database
}
function typeColor(t: DbType) {
  return { mysql: '#60a5fa', postgres: '#a78bfa', redis: '#f472b6' }[t] ?? '#94a3b8'
}

async function toggleConnection(conn: ConnectionItem) {
  activeConnId.value = conn.id
  emit('select-connection', conn)
  if (expandedConns.value.has(conn.id)) {
    expandedConns.value.delete(conn.id)
    return
  }
  expandedConns.value.add(conn.id)
  if (conn.type === 'redis') return

  const cached = dbs[conn.id]
  if (cached) {
    emit('databases-loaded', { conn, items: cached })
    return
  }

  loadingConns[conn.id] = true
  try {
    const res = await workbenchApi.databases(conn.id)
    dbs[conn.id] = res.items
    delete errors[conn.id]
    emit('databases-loaded', { conn, items: res.items })
  } catch (err) {
    errors[conn.id] = err instanceof Error ? err.message : '无法连接该数据源，请检查配置或网络'
  } finally {
    loadingConns[conn.id] = false
  }
}

function schemaOpen(conn: ConnectionItem, db: string, _sch: null) {
  return openSchemaNodes.value.has(`${conn.id}:${db}`)
}
async function toggleSchemaNode(conn: ConnectionItem, db: string, _sch: null) {
  const key = `${conn.id}:${db}`
  if (openSchemaNodes.value.has(key)) {
    openSchemaNodes.value.delete(key)
    return
  }
  openSchemaNodes.value.add(key)
  if (!schemas[key]) {
    try {
      const res = await workbenchApi.schemas(conn.id, db)
      schemas[key] = res.items
    } catch {
      schemas[key] = []
    }
  }
}

function tablesOpen(conn: ConnectionItem, db: string, sch: string) {
  return openTableNodes.value.has(`${conn.id}:${db}:${sch}`)
}
async function toggleTables(conn: ConnectionItem, db: string, sch: string) {
  const key = `${conn.id}:${db}:${sch}`
  if (openTableNodes.value.has(key)) {
    openTableNodes.value.delete(key)
    return
  }
  openTableNodes.value.add(key)
  if (!tables[key]) {
    try {
      const res = await workbenchApi.tables(conn.id, {
        database: db,
        schema: sch || undefined,
      })
      tables[key] = res.items
      emit('tables-loaded', { conn, database: db, schema: sch, tables: res.items })
    } catch {
      tables[key] = []
    }
  } else {
    emit('tables-loaded', { conn, database: db, schema: sch, tables: tables[key] || [] })
  }
}

function emitTable(conn: ConnectionItem, db: string, sch: string, table: TableInfo) {
  activeTableKey.value = `${conn.id}:${db}:${sch}:${table.name}`
  addRecent(conn, db, sch, table)
  emit('preview-table', { conn, database: db, schema: sch, table })
}
function isActiveTable(connId: number, db: string, sch: string, name: string) {
  return activeTableKey.value === `${connId}:${db}:${sch}:${name}`
}
function emitRedisOverview(conn: ConnectionItem) {
  activeConnId.value = conn.id
  emit('redis-overview', conn)
}
function emitRedisKeys(conn: ConnectionItem) {
  activeConnId.value = conn.id
  emit('redis-keys', conn)
}

function openContextMenu(e: MouseEvent, conn: ConnectionItem, db: string, sch: string, table: TableInfo) {
  contextMenu.conn = conn
  contextMenu.database = db
  contextMenu.schema = sch
  contextMenu.table = table
  contextMenu.x = e.clientX
  contextMenu.y = e.clientY
  contextMenu.visible = true
}

function ctxPreview() {
  if (contextMenu.conn && contextMenu.table) {
    emitTable(contextMenu.conn, contextMenu.database, contextMenu.schema, contextMenu.table)
  }
  contextMenu.visible = false
}
function ctxSelect() {
  if (contextMenu.conn && contextMenu.table) {
    emit('generate-select', { conn: contextMenu.conn, database: contextMenu.database, schema: contextMenu.schema, table: contextMenu.table })
  }
  contextMenu.visible = false
}
function ctxInsertName() {
  if (contextMenu.table) {
    emit('insert-table-name', contextMenu.table.name)
  }
  contextMenu.visible = false
}
function ctxToggleFav() {
  if (contextMenu.conn && contextMenu.table) {
    toggleFavorite(contextMenu.conn, contextMenu.database, contextMenu.schema, contextMenu.table)
  }
  contextMenu.visible = false
}
function ctxCopyName() {
  if (contextMenu.table) {
    navigator.clipboard.writeText(contextMenu.table.name)
  }
  contextMenu.visible = false
}
function ctxCopyQualified() {
  if (contextMenu.table) {
    const sch = contextMenu.schema ? `${contextMenu.schema}.` : ''
    const qualified = `${contextMenu.database}.${sch}${contextMenu.table.name}`
    navigator.clipboard.writeText(qualified)
  }
  contextMenu.visible = false
}

defineExpose({
  findConnection: (id: number) => connections.value.find((c) => c.id === id),
  connectionsRef: connections,
  whenLoaded: async () => {
    if (connections.value.length) return
    await reload()
  },
})
</script>

<style scoped>
.tree-row {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  width: 100%;
  padding: 0.35rem 0.5rem;
  border-radius: 0.55rem;
  font-size: 0.78rem;
  color: rgba(255, 255, 255, 0.72);
  transition: background 0.15s, color 0.15s;
}
.tree-row:hover {
  background: rgba(255, 255, 255, 0.07);
  color: #fff;
}
.tree-row span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
