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
  type Sensitivity,
  type TableDetail,
  type MetaKey,
  type TechnicalElement,
} from '../../api/asset'
import { workbenchApi } from '../../api/workbench'
import { useUserStore } from '../../stores/user'
import { revealTableAction, scrollTableByKeyboard } from '../../utils/table-focus'
import TechnicalAssetInventory from './TechnicalAssetInventory.vue'
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

type AssetLocation = Parameters<typeof assetApi.table>[0]
const queryText = (value: unknown) => typeof value === 'string' ? value : ''
function positive(value: unknown) {
  const raw = queryText(value)
  const parsed = /^[1-9]\d*$/.test(raw) ? Number(raw) : 0
  return Number.isSafeInteger(parsed) ? parsed : 0
}
const legacyObjectLink = computed(() => !!queryText(route.query.table) && !route.query.asset_connection)
const inventoryRevision = ref(0)
const inventoryHeading = ref<HTMLElement>()
const detailHeading = ref<HTMLElement>()
let opener: HTMLElement | undefined
let active = true

function changeLocation(changes: LocationQueryRaw) {
  const query = { ...route.query, workspace_id: userStore.workspaceId ? String(userStore.workspaceId) : undefined, ...changes }
  return router.push({ query, hash: route.hash })
}
const clearObject = { asset_connection: undefined, asset_database: undefined, asset_schema: undefined, table: undefined }
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
const detailKey = computed(() => selectedLocation.value ? JSON.stringify([userStore.workspaceId, selectedLocation.value]) : '')

function openLocation(loc: AssetLocation) {
  return changeLocation({ asset_connection: String(loc.connection_id), asset_database: loc.database, asset_schema: loc.schema,
    table: loc.table,
  })
}
function openDetail(element: TechnicalElement, trigger?: HTMLElement) {
  opener = trigger
  void openLocation({ connection_id: element.connection_id, database: element.database, schema: element.schema, table: element.table })
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
    const target = opener?.isConnected ? opener : inventoryHeading.value
    target?.focus()
    opener = undefined
  }
})



function colAnno(name: string) {
  return detail.value?.column_annotations?.find((c) => c.column_name === name)
}
function ownerName(id?: number | null): string {
  return id ? usersMap.value[id] || `成员 #${id}` : '—'
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
    inventoryRevision.value++
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
    inventoryRevision.value++
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
  const connectionID = positive(route.query.connection_id)
  void router.push({ path: '/source-tasks', query: { workspace_id: String(userStore.workspaceId), kind: 'collect', connection_id: connectionID ? String(connectionID) : undefined } })
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
  void loadUsers()
  void readDetail()
})
onScopeDispose(() => { active = false })


</script>

<template>
  <div class="ui-page-stack">
    <PageHeader v-if="!embedded" title="数据目录" />
    <section ref="inventoryHeading" tabindex="-1" aria-label="技术资产">
      <TechnicalAssetInventory :revision="inventoryRevision" @open-object="openDetail">
        <template #actions>
          <button v-if="canCollect" type="button" class="ghost-button ui-control-target" @click="onSync">采集任务</button>
        </template>
      </TechnicalAssetInventory>
    </section>
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
