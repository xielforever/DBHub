<template>
  <div class="space-y-5">
    <header class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold">数据资产</h1>
        <p class="text-sm text-white/45 mt-1">元数据字典、业务标注与查询热度；点击「同步字典」从数据源采集最新结构</p>
      </div>
      <div class="flex items-center gap-2 shrink-0">
        <button class="ghost-button flex items-center gap-2 lg:hidden" @click="treeDrawer = true">
          <FolderTree class="w-4 h-4" />目录
        </button>
        <button
          v-if="canWrite"
          class="liquid-button flex items-center justify-center gap-2"
          :disabled="syncing"
          @click="onSync"
        >
          <RefreshCw class="w-4 h-4" :class="syncing ? 'animate-spin' : ''" />
          {{ syncing ? '同步中…' : '同步字典' }}
        </button>
      </div>
    </header>

    <!-- KPI 概览 -->
    <section class="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-5 gap-3">
      <div
        v-for="kpi in kpis"
        :key="kpi.label"
        class="glass-card p-4 flex items-center gap-3"
      >
        <span class="w-9 h-9 rounded-xl flex items-center justify-center shrink-0" :style="{ background: kpi.tint }">
          <component :is="kpi.icon" class="w-4.5 h-4.5" :style="{ color: kpi.color }" />
        </span>
        <div class="min-w-0">
          <p class="text-lg font-semibold leading-tight">{{ kpi.value }}</p>
          <p class="text-[11px] text-white/45 truncate">{{ kpi.label }}</p>
        </div>
      </div>
    </section>

    <div class="flex gap-5 items-start">
      <!-- 左侧目录树（桌面常驻） -->
      <aside class="hidden lg:block w-72 shrink-0 sticky top-0">
        <div class="glass-card p-3">
          <div class="flex items-center justify-between px-1 pb-2">
            <p class="text-xs uppercase tracking-widest text-white/40">资产目录</p>
            <button class="text-white/40 hover:text-white transition-colors" title="清除选择" @click="clearSelection">
              <X class="w-4 h-4" />
            </button>
          </div>
          <div v-loading="treeLoading" class="max-h-[calc(100vh-280px)] overflow-y-auto pr-1">
            <TreeView :tree="tree" :selection="selection" @select="onTreeSelect" @open-table="onTreeOpenTable" />
            <p v-if="!treeLoading && !tree.length" class="text-xs text-white/35 px-2 py-6 text-center">
              暂无字典，请先同步数据源
            </p>
          </div>
        </div>
      </aside>

      <!-- 右侧列表 -->
      <section class="flex-1 min-w-0 space-y-4">
        <!-- 过滤栏 -->
        <div class="glass-card p-3.5">
          <div class="flex flex-wrap items-center gap-2.5">
            <div class="relative w-full sm:w-64">
              <Search class="w-4 h-4 text-white/40 absolute left-3 top-1/2 -translate-y-1/2" />
              <input
                v-model.trim="filters.q"
                class="glass-input pl-9 py-1.5 text-sm w-full"
                placeholder="搜索表名 / 注释 / 业务说明"
                type="search"
                @input="reload(1)"
              />
            </div>
            <el-select v-model="filters.type" class="w-32" popper-class="glass-popper" @change="reload(1)">
              <el-option label="全部类型" value="" />
              <el-option label="表" value="table" />
              <el-option label="视图" value="view" />
            </el-select>
            <el-select v-model="filters.sensitivity" class="w-36" popper-class="glass-popper" @change="reload(1)">
              <el-option label="全部分级" value="" />
              <el-option label="普通" value="normal" />
              <el-option label="敏感" value="sensitive" />
              <el-option label="机密" value="confidential" />
            </el-select>
            <button
              class="ghost-button text-xs py-1.5 px-3"
              :class="filters.no_owner ? '!bg-amber-400/15 !text-amber-300' : ''"
              @click="toggleNoOwner"
            >
              未指派 Owner
            </button>
            <button
              class="ghost-button text-xs py-1.5 px-3 flex items-center gap-1"
              :class="filters.starred ? '!bg-amber-400/15 !text-amber-300' : ''"
              @click="toggleStarred"
            >
              <Star class="w-3.5 h-3.5" />我的收藏
            </button>
          </div>
        </div>

        <!-- 表清单 -->
        <div v-loading="listLoading" class="glass-card overflow-hidden">
          <div class="overflow-x-auto">
            <table class="w-full text-sm min-w-[860px]">
              <thead>
                <tr class="text-left text-white/40 text-xs border-b border-white/10">
                  <th class="font-medium px-4 py-3">表 / 视图</th>
                  <th class="font-medium px-3 py-3">数据源</th>
                  <th class="font-medium px-3 py-3">Owner</th>
                  <th class="font-medium px-3 py-3">分级</th>
                  <th class="font-medium px-3 py-3 text-right">行数(估)</th>
                  <th class="font-medium px-3 py-3 text-right">30天查询</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="row in rows"
                  :key="row.snapshot.id"
                  class="border-b border-white/5 last:border-0 hover:bg-white/5 cursor-pointer transition-colors"
                  @click="openDetail(row.snapshot)"
                >
                  <td class="px-4 py-3">
                    <div class="flex items-center gap-2">
                      <component
                        :is="row.snapshot.table_type === 'view' ? Eye : Table2"
                        class="w-4 h-4 text-white/40 shrink-0"
                      />
                      <div class="min-w-0">
                        <p class="font-medium truncate flex items-center gap-1.5">
                          {{ row.snapshot.table_name }}
                          <Star v-if="row.starred" class="w-3 h-3 text-amber-300 fill-amber-300" />
                        </p>
                        <p class="text-[11px] text-white/40 truncate">
                          {{ row.snapshot.schema_name }}
                          <span v-if="row.business_desc"> · {{ row.business_desc }}</span>
                          <span v-else-if="row.snapshot.table_comment"> · {{ row.snapshot.table_comment }}</span>
                        </p>
                      </div>
                    </div>
                  </td>
                  <td class="px-3 py-3 text-white/60 text-xs whitespace-nowrap">
                    {{ row.connection_name }}
                  </td>
                  <td class="px-3 py-3 text-xs whitespace-nowrap">
                    <span v-if="row.owner_name" class="text-white/70">{{ row.owner_name }}</span>
                    <span v-else class="text-white/30">未指派</span>
                  </td>
                  <td class="px-3 py-3">
                    <span v-if="row.sensitivity !== 'normal'" class="px-2 py-0.5 rounded-full text-[11px]" :class="sensMeta[row.sensitivity].cls">
                      {{ sensMeta[row.sensitivity].label }}
                    </span>
                    <span v-else-if="row.sensitive_columns" class="px-2 py-0.5 rounded-full text-[11px] bg-amber-400/15 text-amber-300">
                      {{ row.sensitive_columns }} 敏感列
                    </span>
                    <span v-else class="text-white/25 text-xs">—</span>
                  </td>
                  <td class="px-3 py-3 text-right text-white/60 text-xs tabular-nums">{{ formatNum(row.snapshot.estimated_rows) }}</td>
                  <td class="px-3 py-3 text-right text-xs tabular-nums" :class="row.query_count_30d ? 'text-indigo-300' : 'text-white/25'">
                    {{ row.query_count_30d }}
                  </td>
                </tr>
                <tr v-if="!listLoading && !rows.length">
                  <td colspan="6" class="px-4 py-12 text-center text-white/40 text-sm">
                    没有符合条件的资产
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <!-- 分页 -->
          <div class="flex items-center justify-between px-4 py-3 border-t border-white/10 text-xs text-white/50">
            <span>共 {{ total }} 个对象</span>
            <div class="flex items-center gap-1">
              <button class="ghost-button px-2.5 py-1" :disabled="page <= 1" @click="reload(page - 1)">上一页</button>
              <span class="px-2">{{ page }} / {{ totalPages }}</span>
              <button class="ghost-button px-2.5 py-1" :disabled="page >= totalPages" @click="reload(page + 1)">下一页</button>
            </div>
          </div>
        </div>
      </section>
    </div>

    <!-- 移动端目录抽屉 -->
    <el-drawer v-model="treeDrawer" title="资产目录" direction="ltr" size="78%" class="glass-dialog">
      <TreeView
        :tree="tree"
        :selection="selection"
        @select="(s) => { onTreeSelect(s); treeDrawer = false }"
        @open-table="(loc) => { onTreeOpenTable(loc); treeDrawer = false }"
      />
    </el-drawer>

    <!-- 表详情抽屉（M1 只读，标注编辑在 M2） -->
    <el-drawer
      v-model="detailOpen"
      :title="detail?.snapshot.table_name ?? ''"
      size="640px"
      class="glass-dialog asset-detail"
    >
      <template v-if="detail">
        <div class="flex flex-wrap items-center gap-2 text-xs mb-4">
          <span class="px-2 py-0.5 rounded-full bg-white/8 text-white/60">{{ detail.connection_name }}</span>
          <span class="px-2 py-0.5 rounded-full" :class="envMeta[detail.environment].cls">{{ envMeta[detail.environment].label }}</span>
          <span class="px-2 py-0.5 rounded-full bg-white/8 text-white/60">
            {{ detail.snapshot.schema_name }} · {{ detail.snapshot.table_type === 'view' ? '视图' : '表' }}
          </span>
          <span class="px-2 py-0.5 rounded-full bg-indigo-400/15 text-indigo-300 tabular-nums">
            30天查询 {{ detail.query_count_30d }}
          </span>
        </div>

        <!-- 表级标注 -->
        <div class="glass-card p-4 mb-4 space-y-2">
          <div class="flex items-center justify-between">
            <p class="text-xs uppercase tracking-widest text-white/40">业务标注</p>
            <button
              class="flex items-center gap-1 text-xs transition-colors"
              :class="detail.starred ? 'text-amber-300' : 'text-white/40 hover:text-amber-300'"
              @click="toggleStar"
            >
              <Star class="w-3.5 h-3.5" :class="detail.starred ? 'fill-amber-300' : ''" />收藏
            </button>
          </div>
          <p v-if="detail.table_annotation?.business_desc" class="text-sm text-white/80">
            {{ detail.table_annotation.business_desc }}
          </p>
          <p v-else class="text-sm text-white/30">暂无业务说明（M2 开放编辑）</p>
          <div class="flex flex-wrap items-center gap-1.5">
            <span
              v-if="detail.table_annotation && detail.table_annotation.sensitivity !== 'normal'"
              class="px-2 py-0.5 rounded-full text-[11px]"
              :class="sensMeta[detail.table_annotation.sensitivity].cls"
            >
              {{ sensMeta[detail.table_annotation.sensitivity].label }}
            </span>
            <span v-for="t in detail.table_annotation?.tags ?? []" :key="t" class="px-2 py-0.5 rounded-full bg-white/8 text-white/55 text-[11px]">
              # {{ t }}
            </span>
            <span v-if="detail.table_annotation?.owner_user_id" class="text-[11px] text-white/45">
              Owner：{{ ownerName(detail.table_annotation?.owner_user_id) }}
            </span>
          </div>
        </div>

        <el-tabs model-value="columns" class="asset-tabs">
          <el-tab-pane label="字段" name="columns">
            <div class="space-y-1.5">
              <div
                v-for="col in detail.snapshot.raw_columns"
                :key="col.name"
                class="rounded-xl border border-white/8 bg-white/[0.03] px-3 py-2.5"
              >
                <div class="flex items-center gap-2 flex-wrap">
                  <span class="font-medium text-sm flex items-center gap-1.5">
                    <KeyRound v-if="col.is_primary" class="w-3.5 h-3.5 text-amber-300" />
                    {{ col.name }}
                  </span>
                  <span class="text-[11px] px-1.5 py-0.5 rounded bg-white/8 text-indigo-200/80">{{ col.data_type }}</span>
                  <span v-if="!col.is_nullable" class="text-[10px] text-white/35">NOT NULL</span>
                  <span
                    v-if="colAnno(col.name)?.sensitivity && colAnno(col.name)!.sensitivity !== 'normal'"
                    class="text-[10px] px-1.5 py-0.5 rounded-full"
                    :class="sensMeta[colAnno(col.name)!.sensitivity].cls"
                  >
                    {{ sensMeta[colAnno(col.name)!.sensitivity].label }}
                  </span>
                </div>
                <p v-if="col.comment || colAnno(col.name)?.business_desc" class="text-[11px] text-white/45 mt-1">
                  {{ colAnno(col.name)?.business_desc || col.comment }}
                </p>
              </div>
            </div>
          </el-tab-pane>
          <el-tab-pane label="键 / 索引" name="keys">
            <div class="space-y-3">
              <div>
                <p class="text-xs text-white/40 mb-1.5">约束键</p>
                <div v-for="k in detail.snapshot.raw_keys" :key="k.name" class="text-sm py-1.5 border-b border-white/5 last:border-0">
                  <span class="text-white/70">{{ keyKindLabel(k.kind) }}</span>
                  <span class="font-mono text-xs text-white/55 ml-2">{{ k.columns.join(', ') }}</span>
                  <span v-if="k.ref_table" class="text-[11px] text-white/40 ml-2">
                    → {{ k.ref_schema }}.{{ k.ref_table }}({{ (k.ref_columns ?? []).join(', ') }})
                  </span>
                </div>
                <p v-if="!detail.snapshot.raw_keys.length" class="text-xs text-white/30">无</p>
              </div>
              <div>
                <p class="text-xs text-white/40 mb-1.5">索引</p>
                <div v-for="ix in detail.snapshot.raw_indexes" :key="ix.name" class="text-sm py-1.5 border-b border-white/5 last:border-0">
                  <span class="text-white/70">{{ ix.name }}</span>
                  <span class="font-mono text-xs text-white/55 ml-2">({{ ix.columns.join(', ') }})</span>
                  <span v-if="ix.is_unique" class="text-[10px] text-sky-300 ml-2">UNIQUE</span>
                </div>
              </div>
            </div>
          </el-tab-pane>
          <el-tab-pane label="DDL" name="ddl">
            <pre class="text-[11px] leading-relaxed font-mono text-white/70 bg-black/30 rounded-xl p-3 overflow-x-auto whitespace-pre-wrap">{{ detail.snapshot.ddl_text || '—' }}</pre>
          </el-tab-pane>
        </el-tabs>
      </template>
    </el-drawer>

    <!-- 同步结果 -->
    <el-dialog v-model="syncResultOpen" title="字典同步结果" width="640px" class="glass-dialog">
      <div class="space-y-3">
        <p class="text-sm text-white/60">
          本次共采集 <b class="text-white">{{ syncSummary?.tables ?? 0 }}</b> 张表、
          <b class="text-white">{{ syncSummary?.views ?? 0 }}</b> 个视图
        </p>
        <div v-for="r in syncSummary?.connections ?? []" :key="r.connection_id" class="rounded-xl border border-white/8 bg-white/[0.03] p-3">
          <div class="flex items-center gap-2 flex-wrap text-sm">
            <span class="font-medium">{{ r.connection_name }}</span>
            <span class="text-[11px] px-1.5 py-0.5 rounded bg-white/8 text-white/55">{{ r.type }}</span>
            <span v-if="r.skipped" class="text-[11px] text-amber-300">类型不支持，已跳过</span>
            <span v-else class="text-[11px] text-white/45">{{ r.tables }} 表 / {{ r.views }} 视图</span>
          </div>
          <div v-for="e in r.errors ?? []" :key="e" class="text-[11px] text-rose-300/90 mt-1">⚠ {{ e }}</div>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  Boxes,
  Eye,
  FolderTree,
  KeyRound,
  RefreshCw,
  Search,
  Star,
  Table2,
  TableProperties,
  Tags,
  UserRound,
  X,
} from 'lucide-vue-next'
import {
  assetApi,
  type EnvKind,
  type MetaSnapshot,
  type OverviewCounts,
  type Sensitivity,
  type SyncSummary,
  type TableAssetRow,
  type TableDetail,
  type TreeConnection,
  type MetaKey,
} from '../../api/asset'
import { useUserStore } from '../../stores/user'
import TreeView from './components/TreeView.vue'

const route = useRoute()
const userStore = useUserStore()
const canWrite = computed(() => userStore.role !== 'readonly')

const envMeta: Record<EnvKind, { label: string; cls: string }> = {
  dev: { label: '开发', cls: 'bg-sky-400/15 text-sky-300' },
  test: { label: '测试', cls: 'bg-amber-400/15 text-amber-300' },
  prod: { label: '生产', cls: 'bg-rose-400/15 text-rose-300' },
}
const sensMeta: Record<Sensitivity, { label: string; cls: string }> = {
  normal: { label: '普通', cls: 'bg-white/8 text-white/50' },
  sensitive: { label: '敏感', cls: 'bg-amber-400/15 text-amber-300' },
  confidential: { label: '机密', cls: 'bg-rose-400/15 text-rose-300' },
}

// ---------- KPI ----------
const overview = ref<OverviewCounts | null>(null)
const kpis = computed(() => [
  { label: '数据源', value: overview.value?.connection_total ?? 0, icon: Boxes, color: '#a78bfa', tint: 'rgba(167,139,250,0.16)' },
  { label: '表', value: overview.value?.table_total ?? 0, icon: TableProperties, color: '#60a5fa', tint: 'rgba(96,165,250,0.16)' },
  { label: '视图', value: overview.value?.view_total ?? 0, icon: Eye, color: '#67e8f9', tint: 'rgba(103,232,249,0.14)' },
  { label: '已指派 Owner', value: overview.value?.owned_tables ?? 0, icon: UserRound, color: '#34d399', tint: 'rgba(52,211,153,0.15)' },
  { label: '敏感 / 机密字段', value: overview.value?.sensitive_fields ?? 0, icon: Tags, color: '#fbbf24', tint: 'rgba(251,191,36,0.15)' },
])

// ---------- 目录树 ----------
interface Selection {
  connection_id: number
  database: string
  schema: string
}
const tree = ref<TreeConnection[]>([])
const treeLoading = ref(false)
const treeDrawer = ref(false)
const selection = reactive<Selection>({ connection_id: 0, database: '', schema: '' })

async function loadTree() {
  treeLoading.value = true
  try {
    const res = await assetApi.tree()
    tree.value = res.items
  } catch {
    /* 拦截器提示 */
  } finally {
    treeLoading.value = false
  }
}
function onTreeSelect(s: Selection) {
  Object.assign(selection, s)
  reload(1)
}
/** 目录树直接点表名：先收敛列表过滤，再打开详情抽屉 */
async function onTreeOpenTable(loc: { connection_id: number; database: string; schema: string; table: string }) {
  Object.assign(selection, { connection_id: loc.connection_id, database: loc.database, schema: loc.schema })
  openDetail({
    connection_id: loc.connection_id,
    database_name: loc.database,
    schema_name: loc.schema,
    table_name: loc.table,
  } as MetaSnapshot)
}
function clearSelection() {
  Object.assign(selection, { connection_id: 0, database: '', schema: '' })
  reload(1)
}

// ---------- 列表 ----------
const filters = reactive({ q: '', type: '', sensitivity: '', no_owner: false, starred: false })
const rows = ref<TableAssetRow[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const listLoading = ref(false)
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))

async function reload(p?: number) {
  if (p) page.value = p
  listLoading.value = true
  try {
    const res = await assetApi.tables({
      connection_id: selection.connection_id || undefined,
      database: selection.database || undefined,
      schema: selection.schema || undefined,
      q: filters.q || undefined,
      type: filters.type || undefined,
      sensitivity: filters.sensitivity || undefined,
      no_owner: filters.no_owner ? 1 : 0,
      starred: filters.starred ? 1 : 0,
      page: page.value,
      page_size: pageSize,
    })
    rows.value = res.items
    total.value = res.total
  } catch {
    /* 拦截器提示 */
  } finally {
    listLoading.value = false
  }
}
function toggleNoOwner() {
  filters.no_owner = !filters.no_owner
  reload(1)
}
function toggleStarred() {
  filters.starred = !filters.starred
  reload(1)
}
function formatNum(n: number): string {
  return n >= 10000 ? `${(n / 10000).toFixed(1)}w` : String(n)
}
function keyKindLabel(kind: MetaKey['kind']): string {
  return { primary_key: '主键', foreign_key: '外键', unique: '唯一键' }[kind]
}

// ---------- 详情 ----------
const detailOpen = ref(false)
const detail = ref<TableDetail | null>(null)
const usersMap = ref<Record<number, string>>({})

async function openDetail(s: MetaSnapshot) {
  try {
    detail.value = await assetApi.table({
      connection_id: s.connection_id,
      database: s.database_name,
      schema: s.schema_name,
      table: s.table_name,
    })
    detailOpen.value = true
  } catch {
    /* 拦截器提示 */
  }
}
function colAnno(name: string) {
  return detail.value?.column_annotations.find((c) => c.column_name === name)
}
function ownerName(id?: number | null): string {
  return (id && usersMap.value[id]) || '—'
}
async function toggleStar() {
  if (!detail.value) return
  const s = detail.value.snapshot
  try {
    const res = await assetApi.star(
      { connection_id: s.connection_id, database: s.database_name, schema: s.schema_name, table: s.table_name },
      !detail.value.starred,
    )
    detail.value.starred = res.starred
    // 同步更新列表行，避免抽屉关闭后仍显示旧收藏状态
    const idx = rows.value.findIndex(
      (r) =>
        r.snapshot.connection_id === s.connection_id &&
        r.snapshot.database_name === s.database_name &&
        r.snapshot.schema_name === s.schema_name &&
        r.snapshot.table_name === s.table_name,
    )
    if (idx >= 0) {
      const hit = rows.value[idx]
      if (hit) hit.starred = res.starred
    }
    ElMessage.success(res.starred ? '已收藏' : '已取消收藏')
  } catch {
    /* 拦截器提示 */
  }
}

// ---------- 同步 ----------
const syncing = ref(false)
const syncResultOpen = ref(false)
const syncSummary = ref<SyncSummary | null>(null)
async function onSync() {
  syncing.value = true
  try {
    syncSummary.value = await assetApi.sync(selection.connection_id || undefined)
    syncResultOpen.value = true
    await Promise.all([loadTree(), reload(), loadOverview()])
  } catch {
    /* 拦截器提示 */
  } finally {
    syncing.value = false
  }
}

async function loadOverview() {
  try {
    overview.value = await assetApi.overview()
  } catch {
    /* 拦截器提示 */
  }
}
async function loadUsers() {
  try {
    const res = await assetApi.briefUsers()
    usersMap.value = Object.fromEntries(res.items.map((u) => [u.id, u.username]))
  } catch {
    /* 非关键 */
  }
}

onMounted(async () => {
  if (typeof route.query.q === 'string') filters.q = route.query.q
  if (route.query.connection_id) selection.connection_id = Number(route.query.connection_id)
  if (typeof route.query.database === 'string') selection.database = route.query.database
  if (typeof route.query.schema === 'string') selection.schema = route.query.schema
  await Promise.all([loadOverview(), loadTree(), loadUsers()])
  await reload(1)
  if (route.query.connection_id && typeof route.query.table === 'string') {
    const loc = {
      connection_id: Number(route.query.connection_id),
      database_name: selection.database,
      schema_name: selection.schema,
      table_name: route.query.table,
    } as MetaSnapshot
    openDetail(loc)
  }
})
</script>
