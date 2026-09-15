<template>
  <aside class="glass-panel w-full flex flex-col overflow-hidden">
    <div class="px-4 py-3 border-b border-white/10 flex items-center justify-between">
      <h2 class="text-sm font-medium flex items-center gap-2">
        <FolderTree class="w-4 h-4 text-indigo-300" /> 数据库
      </h2>
      <button class="text-white/40 hover:text-white" aria-label="刷新连接树" @click="reload">
        <RefreshCw class="w-3.5 h-3.5" :class="{ 'animate-spin': loading }" />
      </button>
    </div>

    <div class="flex-1 overflow-y-auto p-2" v-loading="loading">
      <div v-if="!connections.length && !loading" class="text-center text-xs text-white/40 px-4 py-10">
        暂无数据源<br />请先在「数据源管理」中创建连接
      </div>

      <div v-for="conn in connections" :key="conn.id" class="mb-0.5">
        <!-- 连接根节点 -->
        <button
          class="tree-row w-full"
          :class="{ 'bg-white/10': activeConnId === conn.id }"
          @click="toggleConnection(conn)"
        >
          <ChevronRight class="w-3.5 h-3.5 text-white/40 transition-transform" :class="{ 'rotate-90': expandedConns.has(conn.id) }" />
          <component :is="typeIcon(conn.type)" class="w-4 h-4 shrink-0" :style="{ color: typeColor(conn.type) }" />
          <span class="truncate text-left">{{ conn.name }}</span>
        </button>

        <div v-if="expandedConns.has(conn.id)" class="ml-4 border-l border-white/10 pl-1">
          <!-- 加载失败 -->
          <p v-if="errors[conn.id]" class="text-[11px] text-rose-300/80 px-2 py-1">{{ errors[conn.id] }}</p>

          <!-- Redis 节点 -->
          <template v-else-if="conn.type === 'redis'">
            <button class="tree-row" @click="emitRedisOverview(conn)">
              <Activity class="w-3.5 h-3.5 text-rose-300" /><span>服务器概览</span>
            </button>
            <button class="tree-row" @click="emitRedisKeys(conn)">
              <KeyRound class="w-3.5 h-3.5 text-amber-300" /><span>键空间浏览</span>
            </button>
          </template>

          <!-- 关系型：数据库/模式/表 -->
          <template v-else>
            <div v-if="loadingConns[conn.id]" class="text-[11px] text-white/40 px-2 py-1">加载中…</div>
            <template v-else>
              <!-- PostgreSQL: database → schema → tables -->
              <template v-if="conn.type === 'postgres'">
                <template v-for="db in dbs[conn.id] || []" :key="db.name">
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
                        <button
                          v-for="t in tables[`${conn.id}:${db.name}:${sch}`] || []"
                          :key="t.name"
                          class="tree-row"
                          :class="{ 'bg-indigo-500/20 text-indigo-200': isActiveTable(conn.id, db.name, sch, t.name) }"
                          @click="emitTable(conn, db.name, sch, t)"
                        >
                          <component :is="t.type === 'view' ? Eye : Table2" class="w-3.5 h-3.5" :class="t.type === 'view' ? 'text-white/35' : 'text-emerald-300'" />
                          <span class="truncate">{{ t.name }}</span>
                        </button>
                      </div>
                    </template>
                  </div>
                </template>
              </template>

              <!-- MySQL: database(schema) → tables -->
              <template v-else>
                <template v-for="db in dbs[conn.id] || []" :key="db.name">
                  <button class="tree-row" @click="toggleTables(conn, db.name, '')">
                    <ChevronRight class="w-3 h-3 text-white/35 transition-transform" :class="{ 'rotate-90': tablesOpen(conn, db.name, '') }" />
                    <Database class="w-3.5 h-3.5 text-sky-300" /><span class="truncate">{{ db.name }}</span>
                  </button>
                  <div v-if="tablesOpen(conn, db.name, '')" class="ml-4 border-l border-white/10 pl-1">
                    <button
                      v-for="t in tables[`${conn.id}:${db.name}:`] || []"
                      :key="t.name"
                      class="tree-row"
                      :class="{ 'bg-indigo-500/20 text-indigo-200': isActiveTable(conn.id, db.name, '', t.name) }"
                      @click="emitTable(conn, db.name, '', t)"
                    >
                      <component :is="t.type === 'view' ? Eye : Table2" class="w-3.5 h-3.5" :class="t.type === 'view' ? 'text-white/35' : 'text-emerald-300'" />
                      <span class="truncate">{{ t.name }}</span>
                    </button>
                  </div>
                </template>
              </template>
            </template>
          </template>
        </div>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import {
  Activity,
  ChevronRight,
  Database,
  Eye,
  Folder,
  FolderTree,
  KeyRound,
  RefreshCw,
  Table2,
} from 'lucide-vue-next'
import { connectionApi, type ConnectionItem, type DbType } from '../../../api/datasource'
import { workbenchApi, type TableInfo } from '../../../api/workbench'

const emit = defineEmits<{
  (e: 'select-connection', conn: ConnectionItem): void
  (e: 'preview-table', payload: { conn: ConnectionItem; database: string; schema: string; table: TableInfo }): void
  (e: 'redis-overview', conn: ConnectionItem): void
  (e: 'redis-keys', conn: ConnectionItem): void
}>()

const connections = ref<ConnectionItem[]>([])
const loading = ref(false)
const loadingConns = reactive<Record<number, boolean>>({})
const errors = reactive<Record<number, string>>({})
const expandedConns = ref<Set<number>>(new Set())
const activeConnId = ref<number | null>(null)
const activeTableKey = ref('')

// 懒加载缓存
const dbs = reactive<Record<number, { name: string }[]>>({})
const schemas = reactive<Record<string, string[]>>({})
const tables = reactive<Record<string, TableInfo[]>>({})
const openSchemaNodes = ref<Set<string>>(new Set())
const openTableNodes = ref<Set<string>>(new Set())

async function reload() {
  loading.value = true
  try {
    const res = await connectionApi.list()
    connections.value = res.items
  } finally {
    loading.value = false
  }
}
reload()

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
  if (conn.type === 'redis' || dbs[conn.id]) return

  loadingConns[conn.id] = true
  try {
    const res = await workbenchApi.databases(conn.id)
    dbs[conn.id] = res.items
    delete errors[conn.id]
  } catch (err) {
    errors[conn.id] = err instanceof Error ? err.message : '加载失败'
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
    } catch {
      tables[key] = []
    }
  }
}

function emitTable(conn: ConnectionItem, db: string, sch: string, table: TableInfo) {
  activeTableKey.value = `${conn.id}:${db}:${sch}:${table.name}`
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

// 供外部（路由 query）预选连接
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
