<script setup lang="ts">
import {featureAllowed} from '../../utils/permission'
import { computed, nextTick, onMounted, onScopeDispose, reactive, ref, watch } from 'vue'
import { useRoute, useRouter, type LocationQueryRaw } from 'vue-router'
import { ElMessage } from 'element-plus'
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import {
  assetApi,
  type BriefUser,
  type EnvKind,
  type MetaSnapshot,
  type OverviewCounts,
  type Sensitivity,
  type TableDetail,
  type TreeConnection,
  type MetaKey,
} from '../../api/asset'
import { workbenchApi } from '../../api/workbench'
import { useUserStore } from '../../stores/user'
import { revealTableAction, scrollTableByKeyboard } from '../../utils/table-focus'
import TreeView from './components/TreeView.vue'
import DataLineagePane from '../query/components/DataLineagePane.vue'
import { useReportRun } from '../report/useReportRun'

defineProps<{ embedded?: boolean }>()

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()
const canCollect=computed(()=>featureAllowed(userStore,'connection.manage',userStore.role!=='readonly')||featureAllowed(userStore,'source.definition.manage',userStore.hasCapability('datasource_manage')))
const canPreview=computed(()=>featureAllowed(userStore,'catalog.preview',true))
const canWrite = computed(() => featureAllowed(userStore,'catalog.classify',userStore.role !== 'readonly'))

const envMeta: Record<EnvKind, { label: string; cls: string }> = {
  dev: { label: '开发', cls: 'bg-accent-soft text-accent' },
  test: { label: '测试', cls: 'bg-warning-soft text-warning' },
  prod: { label: '生产', cls: 'bg-danger-soft text-danger' },
}
const sensMeta: Record<Sensitivity, { label: string; cls: string }> = {
  normal: { label: '普通', cls: 'bg-surface-muted text-muted' },
  sensitive: { label: '敏感', cls: 'bg-warning-soft text-warning' },
  confidential: { label: '机密', cls: 'bg-danger-soft text-danger' },
}

const overviewRun = useReportRun<OverviewCounts>()
const overview = computed(() => overviewRun.state.value.status === 'success' ? overviewRun.state.value.data : undefined)
const kpis = computed(() => overview.value ? [
  { label: '数据源', value: overview.value.connection_total },
  { label: '表', value: overview.value.table_total },
  { label: '视图', value: overview.value.view_total },
  { label: '已指派负责人', value: overview.value.owned_tables },
  { label: '敏感或机密字段', value: overview.value.sensitive_fields },
] : [])

interface Selection { connection_id: number; database: string; schema: string }
type AssetLocation = Parameters<typeof assetApi.table>[0]
const queryText = (value: unknown) => typeof value === 'string' ? value : ''
function positive(value: unknown) {
  const raw = queryText(value)
  const parsed = /^[1-9]\d*$/.test(raw) ? Number(raw) : 0
  return Number.isSafeInteger(parsed) ? parsed : 0
}
const legacyObjectLink = computed(() => !!queryText(route.query.table) && !route.query.asset_connection)
const selection = computed<Selection>(() => ({
  connection_id: positive(route.query.connection_id),
  database: queryText(route.query.database),
  schema: queryText(route.query.schema),
}))
const treeRun = useReportRun<TreeConnection[]>()
const tree = computed(() => treeRun.state.value.status === 'success' ? treeRun.state.value.data : [])
const treeDrawer = ref(false)
const list = useReportRun<Awaited<ReturnType<typeof assetApi.tables>>>()
const listLoading = computed(() => list.state.value.status === 'loading')
const listData = computed(() => list.state.value.status === 'success' ? list.state.value.data : undefined)
const rows = computed(() => listData.value?.items ?? [])
const page = computed(() => positive(route.query.page) || 1)
const pageSize = 20
const totalPages = computed(() => Math.max(1, Math.ceil((listData.value?.total ?? 0) / pageSize)))
const filters = reactive({ q: '', type: '', sensitivity: '', no_owner: false, starred: false })
const applied = computed(() => ({
  q: queryText(route.query.q), type: queryText(route.query.type), sensitivity: queryText(route.query.sensitivity),
  no_owner: route.query.no_owner === '1', starred: route.query.starred === '1',
}))
watch(() => JSON.stringify(applied.value), () => Object.assign(filters, applied.value), { immediate: true })
const listParams = computed(() => ({
  connection_id: selection.value.connection_id || undefined,
  database: selection.value.database || undefined,
  schema: selection.value.schema || undefined,
  q: applied.value.q || undefined,
  type: applied.value.type || undefined,
  sensitivity: applied.value.sensitivity || undefined,
  no_owner: applied.value.no_owner ? 1 as const : 0 as const,
  starred: applied.value.starred ? 1 as const : 0 as const,
  page: page.value, page_size: pageSize,
}))
const listKey = computed(() => JSON.stringify(listParams.value))
const directoryHeading = ref<HTMLElement>()
const detailHeading = ref<HTMLElement>()
let opener: HTMLElement | undefined
let active = true

function changeLocation(changes: LocationQueryRaw, replace = false) {
  const query = { ...route.query, workspace_id: userStore.workspaceId ? String(userStore.workspaceId) : undefined, ...changes }
  return replace ? router.replace({ query, hash: route.hash }) : router.push({ query, hash: route.hash })
}
const clearObject = { asset_connection: undefined, asset_database: undefined, asset_schema: undefined, table: undefined }
function onTreeSelect(value: Selection) {
  treeDrawer.value = false
  void changeLocation({ ...clearObject, connection_id: value.connection_id ? String(value.connection_id) : undefined, database: value.database || undefined, schema: value.schema || undefined, page: undefined })
}
function onTreeOpenTable(loc: AssetLocation) {
  treeDrawer.value = false
  void openLocation(loc)
}
function clearSelection() {
  onTreeSelect({ connection_id: 0, database: '', schema: '' })
}
function applyFilters() {
  const { q, type, sensitivity, no_owner, starred } = filters
  const changes = { ...clearObject, q: q.trim() || undefined, type: type || undefined, sensitivity: sensitivity || undefined,
    no_owner: no_owner ? '1' : undefined, starred: starred ? '1' : undefined, page: undefined }
  const alreadyApplied = !selectedLocation.value && page.value === 1 && q.trim() === applied.value.q &&
    type === applied.value.type && sensitivity === applied.value.sensitivity && no_owner === applied.value.no_owner && starred === applied.value.starred
  if (alreadyApplied) void reload()
  else void changeLocation(changes)
}
function changePage(value: number) {
  if (!listData.value || value < 1 || value > totalPages.value || value === page.value) return
  void changeLocation({ ...clearObject, page: value > 1 ? String(value) : undefined })
}
async function reload() {
  const params = listParams.value
  const loaded = await list.run(listKey.value, signal => assetApi.tables(params, { signal, silentError: true }))
  if (loaded && listData.value && !rows.value.length && page.value > totalPages.value) {
    await changeLocation({ page: totalPages.value > 1 ? String(totalPages.value) : undefined }, true)
  }
}
watch(listKey, () => { void reload() }, { immediate: true })

function loadTree() {
  return treeRun.run('tree', async signal => (await assetApi.tree(undefined, { signal, silentError: true })).items ?? [])
}
function loadOverview() {
  return overviewRun.run('overview', signal => assetApi.overview({ signal, silentError: true }))
}



function formatNum(n: number): string {
  return n >= 10000 ? `${(n / 10000).toFixed(1)}w` : String(n)
}
function formatTime(s: string): string {
  if (!s) return '—'
  try {
    const d = new Date(s)
    return `${d.getMonth()+1}/${d.getDate()} ${String(d.getHours()).padStart(2,'0')}:${String(d.getMinutes()).padStart(2,'0')}`
  } catch { return s.slice(0,16) }
}
function keyKindLabel(kind: MetaKey['kind']): string {
  return { primary_key: '主键', foreign_key: '外键', unique: '唯一键' }[kind]
}
function formatCell(v: unknown): string {
  if (v === null || v === undefined) return 'NULL'
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

const detailRun = useReportRun<TableDetail>()
const detail = computed(() => detailRun.state.value.status === 'success' ? detailRun.state.value.data : undefined)
const activeTab = ref('details')
const detailTabs = computed(() => [
  { id: 'details', name: '详情' }, { id: 'columns', name: '字段' }, { id: 'keys', name: '键与索引' },
  { id: 'ddl', name: 'DDL' }, { id: 'lineage', name: '血缘' },
  ...(canPreview.value ? [{ id: 'preview', name: '数据预览' }] : []),
])
const usersMap = ref<Record<number, string>>({})
const briefUsers = ref<BriefUser[]>([])
const selectedLocation = computed<AssetLocation | undefined>(() => {
  const table = queryText(route.query.table)
  const id = positive(legacyObjectLink.value ? route.query.connection_id : route.query.asset_connection)
  if (!table || !id) return undefined
  return { connection_id: id, database: queryText(legacyObjectLink.value ? route.query.database : route.query.asset_database),
    schema: queryText(legacyObjectLink.value ? route.query.schema : route.query.asset_schema), table }
})
const detailKey = computed(() => selectedLocation.value ? JSON.stringify(selectedLocation.value) : '')

function openLocation(loc: AssetLocation) {
  return changeLocation({ asset_connection: String(loc.connection_id), asset_database: loc.database, asset_schema: loc.schema,
    table: loc.table,
  })
}
async function openDetail(snapshot: MetaSnapshot, event: MouseEvent) {
  opener = event.currentTarget instanceof HTMLElement ? event.currentTarget : undefined
  await openLocation({ connection_id: snapshot.connection_id, database: snapshot.database_name, schema: snapshot.schema_name, table: snapshot.table_name })
}
async function closeDetail() {
  await changeLocation(clearObject)
}
function readDetail() {
  const location = selectedLocation.value
  if (!location) return
  return detailRun.run(detailKey.value, signal => assetApi.table(location, { signal, silentError: true }))
}
watch(detailKey, async (value, previous) => {
  detailRun.reset()
  resetPreview()
  activeTab.value = 'details'
  tableEditOpen.value = false
  columnEditOpen.value = false
  if (value) void readDetail()
  await nextTick()
  if (!active || detailKey.value !== value) return
  if (value) detailHeading.value?.focus()
  else if (previous) {
    const target = opener?.isConnected ? opener : directoryHeading.value
    target?.focus()
    opener = undefined
  }
})



function colAnno(name: string) {
  return detail.value?.column_annotations?.find((c) => c.column_name === name)
}
function ownerName(id?: number | null): string {
  return id ? usersMap.value[id] || rows.value.find(row => row.owner_user_id === id && row.owner_name)?.owner_name || `成员 #${id}` : '—'
}
const starring = ref(false)
const writeNotice = ref('')
const writeNoticeTarget = ref<HTMLParagraphElement>()
watch(detailKey, () => { writeNotice.value = '' })
async function toggleStar() {
  const target = selectedLocation.value
  if (!detail.value || !target || starring.value) return
  const key = detailKey.value
  const starred = !detail.value.starred
  starring.value = true
  try {
    const res = await assetApi.star(target, starred)
    if (!active) return
    if (detailKey.value === key) {
      writeNotice.value = res.starred ? '已收藏。' : '已取消收藏。'
      void readDetail()
    }
    void reload()
  } catch {
    /* 拦截器提示 */
  } finally {
    starring.value = false
  }
}

// ---------- M2 标注编辑 ----------
const tableEditOpen = ref(false)
const columnEditOpen = ref(false)
const editingColumn = ref<string>('')
const savingAnno = ref(false)
let annotationReceiptKey: string | undefined

function showAnnotationReceipt(key: string, message: string) {
  annotationReceiptKey = document.activeElement?.closest('.asset-annotation-editor') ? key : undefined
  writeNotice.value = message
  void readDetail()
}

async function focusAnnotationReceipt() {
  const key = annotationReceiptKey
  annotationReceiptKey = undefined
  if (key !== detailKey.value) return
  await nextTick()
  if (!active || key !== detailKey.value || tableEditOpen.value || columnEditOpen.value) return
  const focused = document.activeElement
  if (focused === document.body || focused?.closest('.asset-annotation-editor')) writeNoticeTarget.value?.focus()
}
const tableForm = reactive<{
  owner_user_id: number | null
  business_desc: string
  tags: string[]
  sensitivity: Sensitivity
}>({
  owner_user_id: null,
  business_desc: '',
  tags: [],
  sensitivity: 'normal',
})
const columnForm = reactive<{
  owner_user_id: number | null
  business_desc: string
  tags: string[]
  sensitivity: Sensitivity
}>({
  owner_user_id: null,
  business_desc: '',
  tags: [],
  sensitivity: 'normal',
})

function openTableEdit() {
  if (!detail.value) return
  const a = detail.value.table_annotation
  tableForm.owner_user_id = a?.owner_user_id ?? null
  tableForm.business_desc = a?.business_desc ?? ''
  tableForm.tags = [...(a?.tags ?? [])]
  tableForm.sensitivity = (a?.sensitivity as Sensitivity) ?? 'normal'
  tableEditOpen.value = true
}
function openColumnEdit(colName: string) {
  if (!detail.value) return
  editingColumn.value = colName
  const a = colAnno(colName)
  columnForm.owner_user_id = a?.owner_user_id ?? null
  columnForm.business_desc = a?.business_desc ?? ''
  columnForm.tags = [...(a?.tags ?? [])]
  columnForm.sensitivity = (a?.sensitivity as Sensitivity) ?? 'normal'
  columnEditOpen.value = true
}
async function saveTableAnno() {
  if (!detail.value || !canWrite.value || savingAnno.value) return
  const s = detail.value.snapshot
  const key = detailKey.value
  if (tableForm.tags.length > 8) {
    ElMessage.warning('标签最多 8 个')
    return
  }
  savingAnno.value = true
  try {
    await assetApi.putAnnotation({
      connection_id: s.connection_id,
      database: s.database_name,
      schema: s.schema_name,
      table: s.table_name,
      owner_user_id: tableForm.owner_user_id,
      business_desc: tableForm.business_desc,
      tags: tableForm.tags,
      sensitivity: tableForm.sensitivity,
    })
    if (!active) return
    if (detailKey.value === key) {
      showAnnotationReceipt(key, '表标注已保存。')
      tableEditOpen.value = false
    }
    void reload()
    void loadOverview()
  } catch {
    /* 拦截器提示 */
  } finally {
    savingAnno.value = false
  }
}
async function saveColumnAnno() {
  if (!detail.value || !editingColumn.value || !canWrite.value || savingAnno.value) return
  const s = detail.value.snapshot
  const key = detailKey.value
  if (columnForm.tags.length > 8) {
    ElMessage.warning('标签最多 8 个')
    return
  }
  savingAnno.value = true
  try {
    await assetApi.putAnnotation({
      connection_id: s.connection_id,
      database: s.database_name,
      schema: s.schema_name,
      table: s.table_name,
      column: editingColumn.value,
      owner_user_id: columnForm.owner_user_id,
      business_desc: columnForm.business_desc,
      tags: columnForm.tags,
      sensitivity: columnForm.sensitivity,
    })
    if (!active) return
    if (detailKey.value === key) {
      showAnnotationReceipt(key, '列标注已保存。')
      columnEditOpen.value = false
    }
    void loadOverview()
  } catch {
    /* 拦截器提示 */
  } finally {
    savingAnno.value = false
  }
}

const preview = useReportRun<Awaited<ReturnType<typeof workbenchApi.preview>>>()
const previewData = computed(() => preview.state.value.status === 'success' ? preview.state.value.data : undefined)
const previewPage = ref(1)
function resetPreview() { preview.reset(); previewPage.value = 1 }
function loadPreview(p = 1) {
  if (!detail.value || !canPreview.value || activeTab.value !== 'preview') return
  previewPage.value = p
  const s = detail.value.snapshot
  return preview.run(detailKey.value + ':' + p, signal => workbenchApi.preview(s.connection_id, {
    database: s.database_name, schema: s.schema_name, table: s.table_name, page: p, page_size: 20,
  }, { signal, silentError: true }))
}
watch(activeTab, tab => {
  if (tab === 'preview') void loadPreview()
  else resetPreview()
})
watch(canPreview, allowed => { if (!allowed && activeTab.value === 'preview') activeTab.value = 'details' })



function onLineageCreateTicket() {
  if (!detail.value) return
  const s = detail.value.snapshot
  router.push({ path: '/tickets', query: { workspace_id: String(userStore.workspaceId), table: s.table_name, db: s.database_name, risk: 'medium', title: `${s.table_name} 变更（血缘影响）` } })
  ElMessage.success('已跳转变更工单，可基于血缘影响创建变更单')
}

// ---------- 后台采集入口 ----------
function onSync() {
  void router.push({ path: '/source-tasks', query: { workspace_id: String(userStore.workspaceId), kind: 'collect', connection_id: selection.value.connection_id ? String(selection.value.connection_id) : undefined } })
}

async function loadUsers() {
  if (!canWrite.value) return
  try {
    const res = await assetApi.briefUsers()
    if (!active) return
    briefUsers.value = res.items
    usersMap.value = Object.fromEntries(res.items.map(user => [user.id, user.username]))
  } catch { /* 成员查询失败时仍可查看资产。 */ }
}

onMounted(() => {
  void loadOverview()
  void loadTree()
  void loadUsers()
  void readDetail()
})
onScopeDispose(() => { active = false })


</script>

<template>
  <div class="ui-page-stack">
    <PageHeader :title="embedded ? '元数据' : '数据目录'" :heading="embedded ? 'h2' : 'h1'" description="浏览已采集的结构、业务标注和数据血缘。">
      <template #actions>
        <button type="button" class="ghost-button ui-control-target lg:hidden" @click="treeDrawer = true">资产树</button>
        <button type="button" class="ghost-button ui-control-target" :disabled="list.state.value.status === 'loading'" @click="reload()">刷新目录</button>
        <button v-if="canCollect" type="button" class="liquid-button ui-control-target" @click="onSync">采集任务</button>
      </template>
    </PageHeader>
    <LoadingState v-if="overviewRun.state.value.status === 'loading'" variant="inline" label="正在读取目录概览…" />
    <div v-else-if="overviewRun.state.value.status === 'error'" class="flex flex-wrap items-center gap-2">
      <p class="text-sm text-danger" role="alert">{{ overviewRun.state.value.message || '目录概览不可用。' }}</p>
      <button type="button" class="ghost-button ui-control-target" @click="loadOverview">重试概览</button>
    </div>
    <dl v-else-if="overview" class="flex flex-wrap gap-x-6 gap-y-2 border-y border-stroke py-3 text-sm" aria-label="目录概览">
      <div v-for="kpi in kpis" :key="kpi.label" class="flex gap-2"><dt class="text-muted">{{ kpi.label }}</dt><dd>{{ kpi.value }}</dd></div>
    </dl>
    <div class="grid min-w-0 gap-4 lg:grid-cols-[220px_minmax(0,1fr)]">
      <aside class="hidden lg:block min-w-0 glass-panel ui-card-padding self-start space-y-3" aria-label="资产树">
        <header class="flex flex-wrap items-center justify-between gap-2"><h3 class="editorial-object-title">资产树</h3><button v-if="selection.connection_id" class="ghost-button ui-control-target" @click="clearSelection">清除范围</button></header>
        <LoadingState v-if="treeRun.state.value.status === 'loading'" variant="inline" label="正在读取资产树…" />
        <div v-else-if="treeRun.state.value.status === 'error'" class="space-y-2"><p class="text-sm text-danger" role="alert">{{ treeRun.state.value.message || '资产树不可用。' }}</p><button class="ghost-button ui-control-target" @click="loadTree">重试资产树</button></div>
        <div v-else-if="tree.length" class="max-h-[65vh] overflow-auto"><TreeView :tree="tree" :selection="selection" @select="onTreeSelect" @open-table="onTreeOpenTable" /></div>
        <p v-else class="text-sm text-muted">暂无可见的结构快照。</p>
      </aside>
      <section class="min-w-0 space-y-4" aria-labelledby="asset-directory-heading">
        <h3 id="asset-directory-heading" ref="directoryHeading" tabindex="-1" class="editorial-object-title">资产目录</h3>
        <div v-if="selection.connection_id" class="flex flex-wrap items-center gap-2 text-sm">
          <span class="break-all">当前范围 {{ tree.find(item => item.id === selection.connection_id)?.name || ('来源 #' + selection.connection_id) }}<span v-if="selection.database"> / {{ selection.database }}</span><span v-if="selection.schema"> / {{ selection.schema }}</span></span>
          <button class="ghost-button ui-control-target lg:hidden" @click="clearSelection">清除范围</button>
        </div>
        <form class="flex min-w-0 flex-wrap items-end gap-2" role="search" aria-label="数据目录筛选" @submit.prevent="applyFilters">
          <label class="flex-1 basis-48 min-w-0 text-sm">搜索数据资产<input v-model="filters.q" type="search" class="glass-input block w-full mt-1" placeholder="表名、注释或业务说明" /></label>
          <label class="text-sm min-w-0">资产类型<select v-model="filters.type" class="glass-input block w-full mt-1"><option value="">全部类型</option><option value="table">表</option><option value="view">视图</option></select></label>
          <label class="text-sm min-w-0">资产分级<select v-model="filters.sensitivity" class="glass-input block w-full mt-1"><option value="">全部分级</option><option value="normal">普通</option><option value="sensitive">敏感</option><option value="confidential">机密</option></select></label>
          <label class="flex min-h-9 items-center gap-1 text-sm"><input v-model="filters.no_owner" type="checkbox" />未指派负责人</label>
          <label class="flex min-h-9 items-center gap-1 text-sm"><input v-model="filters.starred" type="checkbox" />我的收藏</label>
          <button type="submit" class="liquid-button ui-control-target">应用筛选</button>
        </form>
        <PageState v-if="list.state.value.status === 'error'" heading="h3" title="读取资产目录失败" :description="list.state.value.message || '请重新读取当前筛选条件。'" tone="danger">
          <template #actions><button class="ghost-button ui-control-target" @click="reload()">重试资产目录</button></template>
        </PageState>
        <div v-if="listLoading || rows.length" class="editorial-table-wrap" tabindex="0" role="region" aria-label="资产目录表格"
          @focusin="revealTableAction" @keydown.self="scrollTableByKeyboard">
          <table class="editorial-table min-w-[840px]" aria-label="资产目录" :aria-busy="listLoading">
            <thead><tr><th scope="col">序号</th><th scope="col">对象</th><th scope="col">类型</th><th scope="col">数据源</th><th scope="col">负责人</th><th scope="col">分级</th><th scope="col">估算行数</th><th scope="col">30 天查询</th><th scope="col">采集时间</th><th scope="col">收藏</th></tr></thead>
            <tbody><tr v-for="(row, index) in rows" :key="row.snapshot.id">
              <td class="editorial-table__number">{{ String((page - 1) * pageSize + index + 1).padStart(2, '0') }}</td>
              <th scope="row" class="editorial-table__name">
                <button type="button" class="editorial-text-action text-left whitespace-normal break-all" :aria-expanded="selectedLocation?.connection_id === row.snapshot.connection_id && selectedLocation?.table === row.snapshot.table_name && selectedLocation?.database === row.snapshot.database_name && selectedLocation?.schema === row.snapshot.schema_name" @click="openDetail(row.snapshot, $event)">{{ row.snapshot.table_name }}</button>
                <p class="editorial-table__meta">{{ row.snapshot.schema_name }}<span v-if="row.business_desc || row.snapshot.table_comment"> · {{ row.business_desc || row.snapshot.table_comment }}</span></p>
                <p v-if="row.tags?.length" class="editorial-table__meta">{{ row.tags.join('、') }}</p>
              </th>
              <td class="whitespace-nowrap">{{ row.snapshot.table_type === 'view' ? '视图' : '表' }}</td>
              <td class="min-w-32"><p class="max-w-64 [overflow-wrap:anywhere]">{{ row.connection_name }}</p></td>
              <td :class="row.owner_name ? '[overflow-wrap:anywhere]' : 'whitespace-nowrap'">{{ row.owner_name || '未指派' }}</td>
              <td><span class="status-badge whitespace-nowrap" :class="sensMeta[row.sensitivity]?.cls">{{ sensMeta[row.sensitivity]?.label || '—' }}</span><p v-if="row.sensitive_columns" class="whitespace-nowrap text-xs text-muted">{{ row.sensitive_columns }} 个敏感列</p></td>
              <td class="whitespace-nowrap">{{ row.snapshot.metadata_status?.statistics === 'unsupported' ? '未提供' : formatNum(row.snapshot.estimated_rows) }}</td>
              <td>{{ row.query_count_30d }}</td><td class="whitespace-nowrap">{{ formatTime(row.snapshot.synced_at) }}</td>
              <td class="whitespace-nowrap"><span v-if="row.starred" class="text-warning">已收藏</span><span v-else>—</span></td>
            </tr></tbody>
          </table>
        </div>
        <LoadingState v-if="listLoading" label="正在读取资产目录…" />
        <PageState v-else-if="listData && !rows.length" heading="h3" title="没有符合条件的资产" description="可以调整筛选，或核对当前范围的采集记录。" />
        <nav class="flex flex-wrap items-center justify-between gap-2 text-sm text-muted" aria-label="资产目录分页">
          <span v-if="listData">共 {{ listData.total }} 个对象</span>
          <span v-else>{{ listLoading ? '总数正在确认' : '总数暂不可用' }}</span>
          <div class="flex flex-wrap items-center gap-2">
            <button type="button" class="ghost-button ui-control-target aria-disabled:opacity-50" :aria-disabled="(!listData || page <= 1) || undefined" @click="changePage(page - 1)">上一页</button>
            <span v-if="listData">{{ page }} / {{ totalPages }}</span>
            <span v-else>第 {{ page }} 页</span>
            <button type="button" class="ghost-button ui-control-target aria-disabled:opacity-50" :aria-disabled="(!listData || page >= totalPages) || undefined" @click="changePage(page + 1)">下一页</button>
          </div>
        </nav>
      </section>
    </div>
    <el-drawer v-model="treeDrawer" title="资产树" direction="ltr" size="min(360px, calc(100vw - 24px))" class="glass-dialog">
      <button v-if="selection.connection_id" class="ghost-button ui-control-target mb-3" @click="clearSelection">清除范围</button>
      <LoadingState v-if="treeRun.state.value.status === 'loading'" variant="inline" label="正在读取资产树…" />
      <div v-else-if="treeRun.state.value.status === 'error'" class="space-y-2"><p role="alert" class="text-sm text-danger">{{ treeRun.state.value.message || '资产树不可用。' }}</p><button class="ghost-button ui-control-target" @click="loadTree">重试资产树</button></div>
      <TreeView v-else-if="tree.length" :tree="tree" :selection="selection" @select="onTreeSelect" @open-table="onTreeOpenTable" />
      <p v-else class="text-sm text-muted">暂无可见的结构快照。</p>
    </el-drawer>

    <section v-if="selectedLocation" class="glass-panel ui-card-padding space-y-4 min-w-0" aria-labelledby="asset-detail-heading">
      <header class="flex flex-wrap items-start justify-between gap-2">
        <h3 id="asset-detail-heading" ref="detailHeading" tabindex="-1" class="editorial-object-title break-all">{{ selectedLocation.table }} · 资产详情</h3>
        <button class="ghost-button ui-control-target" @click="closeDetail">关闭资产详情</button>
      </header>
      <p v-if="writeNotice" ref="writeNoticeTarget" tabindex="-1" class="text-sm text-success ui-focus-target" role="status">{{ writeNotice }}</p>
      <LoadingState v-if="detailRun.state.value.status === 'loading'" label="正在读取资产详情…" />
      <PageState v-else-if="detailRun.state.value.status === 'error'" heading="h3" title="读取资产详情失败" :description="detailRun.state.value.message || '请重新核对所选对象。'" tone="danger">
        <template #actions><button class="ghost-button ui-control-target" @click="readDetail">重试资产详情</button></template>
      </PageState>
      <template v-else-if="detail">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p class="text-sm text-muted break-all">{{ detail.connection_name }} · {{ detail.snapshot.database_name }} · {{ detail.snapshot.schema_name || '默认范围' }} · {{ detail.snapshot.table_type === 'view' ? '视图' : '表' }}</p>
          <div class="flex flex-wrap gap-2">
            <button v-if="canWrite" class="ghost-button ui-control-target" @click="openTableEdit">编辑表标注</button>
            <button class="ghost-button ui-control-target" :aria-pressed="detail.starred" :disabled="starring" @click="toggleStar">{{ detail.starred ? '取消收藏' : '收藏对象' }}</button>
          </div>
        </div>
        <PageCategoryTabs label="资产详情分类" :categories="detailTabs" :active-id="activeTab" @select="activeTab = $event">
          <div v-if="activeTab === 'details'" class="grid gap-3 py-3 text-sm sm:grid-cols-2">
            <dl class="grid gap-3">
              <div><dt class="text-muted">负责人</dt><dd>{{ detail.table_annotation?.owner_user_id ? ownerName(detail.table_annotation.owner_user_id) : '未指派' }}</dd></div>
              <div><dt class="text-muted">分级</dt><dd>{{ sensMeta[detail.table_annotation?.sensitivity || 'normal'].label }}</dd></div>
              <div><dt class="text-muted">业务说明</dt><dd class="break-words">{{ detail.table_annotation?.business_desc || detail.snapshot.table_comment || '—' }}</dd></div>
              <div><dt class="text-muted">标签</dt><dd class="break-words">{{ detail.table_annotation?.tags?.join('、') || '—' }}</dd></div>
            </dl>
            <dl class="grid gap-3">
              <div><dt class="text-muted">环境</dt><dd>{{ envMeta[detail.environment]?.label || '未标注' }}</dd></div>
              <div><dt class="text-muted">最近采集</dt><dd>{{ formatTime(detail.snapshot.synced_at) }}</dd></div>
              <div><dt class="text-muted">最近 30 天查询</dt><dd>{{ detail.query_count_30d }}</dd></div>
              <div><dt class="text-muted">数据预览</dt><dd>{{ canPreview ? '可申请读取，结果按当前对象权限返回' : '当前缺少数据预览许可' }}</dd></div>
            </dl>
          </div>
          <template v-else-if="activeTab === 'columns'">
            <div v-if="detail.snapshot.raw_columns?.length" class="editorial-table-wrap mt-3" tabindex="0" role="region" aria-label="资产字段表格"
              @focusin="revealTableAction" @keydown.self="scrollTableByKeyboard">
              <table class="editorial-table min-w-[640px]" aria-label="资产字段">
                <thead><tr><th scope="col">序号</th><th scope="col">字段</th><th scope="col">类型</th><th scope="col">主键</th><th scope="col">可空</th><th scope="col">默认值</th><th scope="col">说明</th><th v-if="canWrite" scope="col">操作</th></tr></thead>
                <tbody><tr v-for="(col, index) in detail.snapshot.raw_columns" :key="col.name">
                  <td class="editorial-table__number">{{ col.ordinal || index + 1 }}</td><th scope="row" class="font-mono">{{ col.name }}</th>
                  <td class="font-mono">{{ col.data_type }}</td><td>{{ col.is_primary ? '是' : '否' }}</td><td>{{ col.is_nullable ? '是' : '否' }}</td><td>{{ col.default ?? '—' }}</td>
                  <td><p>{{ colAnno(col.name)?.business_desc || col.comment || '—' }}</p><p v-if="colAnno(col.name)" class="editorial-table__meta">{{ sensMeta[colAnno(col.name)!.sensitivity]?.label }}<span v-if="colAnno(col.name)?.owner_user_id"> · {{ ownerName(colAnno(col.name)?.owner_user_id) }}</span><span v-if="colAnno(col.name)?.tags?.length"> · {{ colAnno(col.name)?.tags?.join('、') }}</span></p></td>
                  <td v-if="canWrite"><button class="editorial-text-action whitespace-nowrap" :aria-label="'编辑列标注 ' + col.name" @click="openColumnEdit(col.name)">编辑标注</button></td>
                </tr></tbody>
              </table>
            </div>
            <p v-else class="py-3 text-sm text-muted">快照未返回字段。</p>
          </template>
          <div v-else-if="activeTab === 'keys'" class="py-3 space-y-3 text-sm">
            <h4 class="editorial-object-title">约束键</h4>
            <p v-for="key in detail.snapshot.raw_keys" :key="key.name" class="break-words border-b border-stroke pb-2">{{ keyKindLabel(key.kind) }} · {{ key.name }} · {{ key.columns.join('、') }}<span v-if="key.ref_table"> → {{ key.ref_schema }}.{{ key.ref_table }} ({{ key.ref_columns?.join('、') }})</span></p>
            <p v-if="!detail.snapshot.raw_keys?.length" class="text-muted">快照未返回约束键。</p>
            <h4 class="editorial-object-title">索引</h4>
            <p v-for="index in detail.snapshot.raw_indexes" :key="index.name" class="break-words border-b border-stroke pb-2">{{ index.name }} · {{ index.columns.join('、') }}<span v-if="index.is_unique"> · 唯一</span></p>
            <p v-if="!detail.snapshot.raw_indexes?.length" class="text-muted">快照未返回索引。</p>
          </div>
          <div v-else-if="activeTab === 'ddl'" class="py-3">
            <pre v-if="detail.snapshot.ddl_text" class="max-h-96 overflow-auto whitespace-pre-wrap break-all bg-surface-muted p-3 text-xs">{{ detail.snapshot.ddl_text }}</pre>
            <p v-else class="text-sm text-muted">{{ detail.snapshot.metadata_status?.ddl === 'unsupported' ? '当前驱动尚未提供 DDL。' : '当前快照未返回 DDL。' }}</p>
          </div>
          <DataLineagePane v-else-if="activeTab === 'lineage'" :current-table="detail.snapshot.table_name" :current-db="detail.snapshot.database_name" :fallback-table="detail.snapshot.table_name" :connection-id="detail.snapshot.connection_id" :schema="detail.snapshot.schema_name" @create-ticket="onLineageCreateTicket" />
          <section v-else-if="activeTab === 'preview' && canPreview" class="py-3 space-y-3" aria-label="资产数据预览">
            <button class="ghost-button ui-control-target" :disabled="preview.state.value.status === 'loading'" @click="loadPreview()">刷新预览</button>
            <LoadingState v-if="preview.state.value.status === 'loading'" label="正在读取资产预览…" />
            <PageState v-else-if="preview.state.value.status === 'error'" heading="h3" title="读取资产预览失败" :description="preview.state.value.message || '请重新核对当前预览。'" tone="danger">
              <template #actions><button class="ghost-button ui-control-target" @click="loadPreview(previewPage)">重试资产预览</button></template>
            </PageState>
            <template v-else-if="previewData">
              <div v-if="previewData.rows?.length" class="editorial-table-wrap max-h-[420px]" tabindex="0" role="region" aria-label="资产预览表格">
                <table class="editorial-table min-w-[520px]" aria-label="资产预览">
                  <thead><tr><th v-for="(column, index) in previewData.columns" :key="index" scope="col">{{ column }}</th></tr></thead>
                  <tbody><tr v-for="(row, i) in previewData.rows" :key="i"><td v-for="(cell, j) in row" :key="j" class="max-w-[260px] break-all">{{ formatCell(cell) }}</td></tr></tbody>
                </table>
              </div>
              <p v-else class="text-sm text-muted">本页未返回数据。</p>
              <div class="flex flex-wrap items-center gap-2 text-sm text-muted">
                <span>共 {{ previewData.total }} 行（估）</span>
                <button class="ghost-button ui-control-target" :disabled="previewPage <= 1" @click="loadPreview(previewPage - 1)">上一页预览</button>
                <span>第 {{ previewPage }} 页</span><button class="ghost-button ui-control-target" :disabled="!previewData.has_more" @click="loadPreview(previewPage + 1)">下一页预览</button>
              </div>
            </template>
          </section>
        </PageCategoryTabs>
      </template>
    </section>


    <!-- 表级标注编辑 M2 -->
    <el-dialog v-model="tableEditOpen" title="编辑表标注" width="min(560px, calc(100vw - 24px))" class="glass-dialog asset-annotation-editor"
      :close-on-click-modal="false" @close-auto-focus="focusAnnotationReceipt">
      <div class="space-y-4">
        <div>
          <p class="text-xs text-muted mb-1">Owner</p>
          <el-select v-model="tableForm.owner_user_id" class="w-full" popper-class="glass-popper" clearable placeholder="选择 Owner">
            <el-option v-for="u in briefUsers" :key="u.id" :label="`${u.username} (${u.role})`" :value="u.id" />
          </el-select>
        </div>
        <div>
          <p class="text-xs text-muted mb-1">业务说明</p>
          <el-input v-model="tableForm.business_desc" type="textarea" :rows="3" placeholder="表用途、口径、注意事项" maxlength="1000" show-word-limit />
        </div>
        <div>
          <p class="text-xs text-muted mb-1">标签（最多8个，回车新增）</p>
          <el-select v-model="tableForm.tags" multiple filterable allow-create default-first-option class="w-full" popper-class="glass-popper" placeholder="输入标签后回车">
            <el-option v-for="t in tableForm.tags" :key="t" :label="t" :value="t" />
          </el-select>
        </div>
        <div>
          <p class="text-xs text-muted mb-1">敏感分级</p>
          <el-select v-model="tableForm.sensitivity" class="w-full" popper-class="glass-popper">
            <el-option label="普通" value="normal" />
            <el-option label="敏感" value="sensitive" />
            <el-option label="机密" value="confidential" />
          </el-select>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="ghost-button ui-control-target" @click="tableEditOpen=false">取消</button>
          <button class="liquid-button ui-control-target aria-disabled:opacity-60 aria-disabled:cursor-not-allowed" :aria-disabled="savingAnno || undefined"
            @click="saveTableAnno">{{ savingAnno ? '保存中…' : '保存' }}</button>
        </div>
      </template>
    </el-dialog>

    <!-- 列级标注编辑 M2 -->
    <el-dialog v-model="columnEditOpen" :title="`编辑列标注 · ${editingColumn || ''}`" width="min(560px, calc(100vw - 24px))" class="glass-dialog asset-annotation-editor"
      :close-on-click-modal="false" @close-auto-focus="focusAnnotationReceipt">
      <div class="space-y-4">
        <div>
          <p class="text-xs text-muted mb-1">Owner</p>
          <el-select v-model="columnForm.owner_user_id" class="w-full" popper-class="glass-popper" clearable placeholder="选择 Owner">
            <el-option v-for="u in briefUsers" :key="u.id" :label="`${u.username} (${u.role})`" :value="u.id" />
          </el-select>
        </div>
        <div>
          <p class="text-xs text-muted mb-1">业务说明</p>
          <el-input v-model="columnForm.business_desc" type="textarea" :rows="2" placeholder="列含义、口径" maxlength="1000" show-word-limit />
        </div>
        <div>
          <p class="text-xs text-muted mb-1">标签</p>
          <el-select v-model="columnForm.tags" multiple filterable allow-create default-first-option class="w-full" popper-class="glass-popper" placeholder="输入标签后回车">
            <el-option v-for="t in columnForm.tags" :key="t" :label="t" :value="t" />
          </el-select>
        </div>
        <div>
          <p class="text-xs text-muted mb-1">敏感分级</p>
          <el-select v-model="columnForm.sensitivity" class="w-full" popper-class="glass-popper">
            <el-option label="普通" value="normal" />
            <el-option label="敏感" value="sensitive" />
            <el-option label="机密" value="confidential" />
          </el-select>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="ghost-button ui-control-target" @click="columnEditOpen=false">取消</button>
          <button class="liquid-button ui-control-target aria-disabled:opacity-60 aria-disabled:cursor-not-allowed" :aria-disabled="savingAnno || undefined"
            @click="saveColumnAnno">{{ savingAnno ? '保存中…' : '保存' }}</button>
        </div>
      </template>
    </el-dialog>


  </div>
</template>
