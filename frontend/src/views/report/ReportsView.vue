<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import {iamUiEnabled} from '../../utils/iam-mode'
import PageHeader from '../../components/base/PageHeader.vue'
import ContentDevelopmentTabs from '../../components/base/ContentDevelopmentTabs.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageState from '../../components/base/PageState.vue'
import CardActionMenu from '../../components/base/CardActionMenu.vue'
import { Star } from 'lucide-vue-next'
import {featureAllowed} from '../../utils/permission'
import { computed, nextTick, onMounted, onScopeDispose, reactive, ref, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import {useRoute,useRouter} from 'vue-router'
import { reportApi, type ReportItem } from '../../api/report'
import type { ExecResult } from '../../api/workbench'
import { useUserStore } from '../../stores/user'
import ReportEditor from './ReportEditor.vue'
import PublishedReportsPanel from './PublishedReportsPanel.vue'
import ManagedReportsPanel from './ManagedReportsPanel.vue'
import ReportAccountsPanel from './ReportAccountsPanel.vue'
import ReportExportsPanel from './ReportExportsPanel.vue'
import ReportSubscriptionsPanel from './ReportSubscriptionsPanel.vue'
import ReportResult from './ReportResult.vue'
import { useReportRun } from './useReportRun'
import { reportChartLabels } from './reportChart'
import ReportParameterForm from './ReportParameterForm.vue'
import { parameterDefaults, type ParameterValues } from './reportParameters'
const route=useRoute()
const router=useRouter()
const user = useUserStore(), canWrite = computed(() => featureAllowed(user,'report.design',['admin','developer'].includes(user.role)))
const canRead=computed(()=>featureAllowed(user,'report.view',true)),canManaged=computed(()=>canWrite.value||['report.review','report.publish','report.offline'].some(a=>featureAllowed(user,a,user.hasCapability('report_review'))))
const categories = computed(() => [
  ...(!user.actionControlled || (canRead.value && user.can('connection.use')) ? [{ id: 'personal', name: '个人报表' }] : []),
  ...(canRead.value ? [{ id: 'published', name: '已发布' }] : []),
  ...(!iamUiEnabled || user.can('report.export') || user.can('report.exportReview') ? [{ id: 'exports', name: '报表导出' }] : []),
  ...(!iamUiEnabled || canRead.value ? [{ id: 'subscriptions', name: '订阅与告警' }] : []),
  ...(canManaged.value ? [{ id: 'managed', name: canWrite.value
    ? (featureAllowed(user, 'report.review', user.hasCapability('report_review')) ? '开发与审批' : '我的开发')
    : '审批与发布' }] : []),
  ...(!user.actionControlled && user.hasCapability('datasource_manage') ? [{ id: 'accounts', name: '执行账号' }] : []),
])
const view = computed(() => {
  const value = route.query.view
  if (value !== undefined) return categories.value.find(item => item.id === value)?.id
  const preferred = !user.actionControlled ? 'personal' : canRead.value ? 'published' : canManaged.value ? 'managed' : 'exports'
  return categories.value.find(item => item.id === preferred)?.id ?? categories.value[0]?.id
})
const reportId = computed(() => {
  const value = route.query.report
  const id = typeof value === 'string' && /^\d+$/.test(value) ? Number(value) : 0
  return Number.isSafeInteger(id) && id > 0 ? id : undefined
})
const reading = computed(() => view.value === 'published' && reportId.value !== undefined)
const designing = ref(false)
async function selectView(value: string) {
  if (view.value === value || !categories.value.some(item => item.id === value)) return
  const query = Object.fromEntries(Object.entries(route.query).filter(([key]) => !key.startsWith('p_') && !['report', 'export', 'subscription', 'delivery', 'team_delivery', 'incident', 'scope', 'create'].includes(key)))
  await router.push({ query: { ...query, view: value }, hash: route.hash })
}
const items = ref<ReportItem[]>([]), total = ref(0), page = ref(1), loading = ref(true), listError = ref(''), keyword = ref(''), scope = ref<'all'|'mine'|'shared'|'starred'>(route.query.scope === 'starred' ? 'starred' : 'all')
const search = ref('')
const totalPages = computed(() => Math.max(1,Math.ceil(total.value/12)))
const pending = reactive(new Set<number>())
let listGeneration = 0, listAbort: AbortController | undefined
async function reload(p = page.value) {
  if (view.value !== 'personal') return
  page.value = p
  const run = ++listGeneration
  listAbort?.abort()
  const controller = new AbortController(); listAbort = controller
  loading.value = true; listError.value = ''
  try { const response = await reportApi.list({ scope:scope.value,q:search.value || undefined,page:p,page_size:12 },controller.signal); if (run !== listGeneration) return; items.value=response.items;total.value=response.total; if (p>1 && !response.items.length && response.total<=12*(p-1)) await reload(Math.max(1,Math.ceil(response.total/12))) }
  catch (error) { if (run===listGeneration) { items.value=[];total.value=0;listError.value=error instanceof Error?error.message:'报表加载失败' } }
  finally { if (run===listGeneration) loading.value=false }
}
function applySearch() { search.value = keyword.value.trim(); void reload(1) }
const canManage = (rep: ReportItem) => canWrite.value && rep.owner_user_id===user.user?.id
const cardActions = (rep: ReportItem) => [
  { id: 'edit', label: '编辑' },
  { id: 'draft', label: '创建正式草稿' },
  { id: 'remove', label: '删除', danger: true, disabled: pending.has(rep.id) },
]
function selectCardAction(id: string, rep: ReportItem) {
  if (!canManage(rep)) return
  if (id === 'edit') void edit(rep)
  else if (id === 'draft') void edit(rep, true)
  else if (id === 'remove') void remove(rep)
}
const formatTime = (value: string) => new Date(value).toLocaleString()
const editorOpen = ref(false), formalEditor=ref(false), editing = ref<ReportItem>()
function create() { formalEditor.value=false;editing.value=undefined;editorOpen.value=true }
let editGeneration = 0
async function edit(rep: ReportItem, formal=false) { const generation=++editGeneration;try { const data=await reportApi.get(rep.id);if(generation===editGeneration && view.value==='personal'){formalEditor.value=formal;editing.value=data;editorOpen.value=true} } catch { /* API shows the failure. */ } }
function clearPersonalIntent(key: 'report' | 'create') {
  if (view.value !== 'personal' || route.query[key] === undefined) return
  const query = { ...route.query }
  delete query[key]
  void router.replace({ query, hash: route.hash })
}
function closeEditor() { editorOpen.value = false; clearPersonalIntent('create') }
function saved() { closeEditor(); void reload() }
function managedSaved(){editorOpen.value=false;void selectView('managed')}
async function toggleStar(rep: ReportItem) {
  if (pending.has(rep.id)) return
  pending.add(rep.id)
  try { const result=await reportApi.star(rep.id,!rep.starred);rep.starred=result.starred;if(scope.value==='starred'&&!result.starred)void reload() } catch { /* API shows the failure. */ } finally { pending.delete(rep.id) }
}
async function remove(rep: ReportItem) {
  if (pending.has(rep.id)) return
  try { await ElMessageBox.confirm(`确认删除报表「${rep.name}」？`, '删除报表', { type:'warning',confirmButtonText:'删除',cancelButtonText:'取消' }) } catch { return }
  pending.add(rep.id)
  try { await reportApi.remove(rep.id);await reload() } catch { /* API shows the failure. */ } finally { pending.delete(rep.id) }
}
const detailOpen=ref(false), current=ref<ReportItem>()
const personalTable = ref<HTMLElement>()
const personalSearch = ref<HTMLInputElement>()
let closingReportId: number | undefined
const detail=useReportRun<{ report:ReportItem;result:ExecResult }>()
const personalLink = useReportRun<ReportItem>()
const parameterValues = ref<ParameterValues>({})
watch(parameterValues, () => detail.reset(), { deep: true, flush: 'sync' })
function openReport(rep: ReportItem, verified = false) {
  closingReportId = undefined
  detail.reset()
  current.value = rep
  parameterValues.value = parameterDefaults(rep.parameters || [])
  detailOpen.value = true
  void runDetail(verified ? rep : undefined)
}
async function runDetail(verified?: ReportItem) {
  const id=current.value?.id
  if (!id) return
  const params = { ...parameterValues.value }
  const success=await detail.run(`${id}:${JSON.stringify(params)}`,async signal=>{const report=verified ?? await reportApi.get(id,signal);const result=await reportApi.execute(id,report.version,signal,params);return {report,result}})
  if (success&&detail.state.value.status==='success') current.value=detail.state.value.data.report
}
function closeDetail(done: () => void) {
  closingReportId = current.value?.id
  detailOpen.value = false
  done()
  clearPersonalIntent('report')
}
async function restorePersonalReportFocus() {
  const id = closingReportId
  closingReportId = undefined
  await nextTick()
  if (id === undefined || detailOpen.value || view.value !== 'personal' || reportId.value !== undefined) return
  const opener = personalTable.value?.querySelector<HTMLButtonElement>(`button[data-report-id="${id}"]`)
  const focusTarget = opener ?? personalSearch.value
  focusTarget?.focus()
}
async function openPersonalLink(id = reportId.value) {
  if (view.value !== 'personal' || id === undefined) return
  const loaded = await personalLink.run(String(id), signal => reportApi.get(id, signal, true))
  if (loaded && view.value === 'personal' && reportId.value === id && personalLink.state.value.status === 'success') {
    openReport(personalLink.state.value.data, true)
  }
}
watch(detailOpen,open=>{if(!open)detail.reset()},{flush:'sync'})
watch(view,value=>{editGeneration++;designing.value=false;detailOpen.value=false;editorOpen.value=false;if(value==='personal')void reload();else{listGeneration++;listAbort?.abort();loading.value=false}})
watch(() => route.query.scope, (next, previous) => {
  if (view.value !== 'personal' || (next !== 'starred' && previous !== 'starred')) return
  scope.value = next === 'starred' ? 'starred' : 'all'
  void reload(1)
})
watch([view, reportId], ([category, id], previous) => {
  personalLink.reset()
  if (previous?.[1] !== undefined) detailOpen.value = false
  if (category === 'personal' && id !== undefined) void openPersonalLink(id)
}, { immediate: true })
watch([view, () => route.query.create, canWrite], ([category, intent, allowed], previous) => {
  if (previous?.[1] === '1' && (intent !== '1' || !allowed)) editorOpen.value = false
  if (category === 'personal' && intent === '1' && allowed && reportId.value === undefined) create()
}, { immediate: true })
onMounted(()=>{if(view.value==='personal')void reload(1)})
onScopeDispose(()=>{editGeneration++;listGeneration++;listAbort?.abort()})
</script>

<template>
  <div class="ui-page-stack">
    <PageHeader v-if="!reading && view !== 'managed'" :title="view === 'exports' ? '报表导出' : view === 'subscriptions' ? '订阅与指标告警' : '报表目录'"
      :description="view === 'personal' ? '保存只读查询与图表，使用自己的数据库账号查看最新数据' : view === 'exports' ? '跟踪完整快照的独立审批、有效期和当前领取资格。' : view === 'subscriptions' ? '查看本人交付结果，按权限管理个人订阅计划和指标告警。' : '查看、开发和管理当前工作空间中获准访问的报表。'">
      <template #actions>
        <RouterLink v-if="!user.actionControlled && user.workspaceId" class="ghost-button ui-control-target" :to="{ path: '/home', query: { view: 'business', workspace_id: String(user.workspaceId) } }">业务工作台</RouterLink>
        <button v-if="canWrite && view==='personal'" class="liquid-button text-sm ui-control-target" @click="create">新建报表</button>
      </template>
    </PageHeader>
    <PageCategoryTabs label="报表功能" :categories="categories" :active-id="view" :hide-tabs="reading || designing" @select="selectView">
    <template v-if="view==='personal'">
    <LoadingState v-if="personalLink.state.value.status === 'loading'" variant="inline" label="正在读取个人报表…" />
    <PageState v-else-if="personalLink.state.value.status === 'error'" title="无法打开个人报表" :description="personalLink.state.value.message || '请重新核对报表。'" tone="danger">
      <template #actions>
        <button type="button" class="ghost-button ui-control-target" @click="openPersonalLink()">重试打开报表</button>
        <button type="button" class="ghost-button ui-control-target" @click="clearPersonalIntent('report')">关闭提示</button>
      </template>
    </PageState>
    <form class="ui-filter-bar" @submit.prevent="applySearch">
      <el-select v-model="scope" aria-label="报表范围" class="w-32" @change="reload(1)"><el-option label="全部" value="all" /><el-option label="我的" value="mine" /><el-option label="登录后共享" value="shared" /><el-option label="收藏" value="starred" /></el-select>
      <input ref="personalSearch" v-model.trim="keyword" aria-label="搜索报表" class="glass-input ui-filter-search text-sm sm:max-w-sm" placeholder="搜索名称、描述或 SQL" />
      <button type="submit" class="ghost-button text-xs ui-control-target">搜索</button>
      <button type="button" class="ghost-button text-xs ui-control-target" :disabled="loading" @click="reload()">刷新</button>
    </form>
    <LoadingState v-if="loading" label="正在读取报表目录…" />
    <PageState v-else-if="listError" title="读取报表目录失败" :description="listError" tone="danger">
      <template #actions><button class="ghost-button ui-control-target" @click="reload()">重试读取列表</button></template>
    </PageState>
    <div v-else ref="personalTable" class="editorial-table-wrap" role="region" tabindex="0" aria-label="个人报表目录表格">
      <table class="editorial-table" aria-label="报表列表">
        <thead><tr><th scope="col">序号</th><th scope="col">报表名称</th><th scope="col">可见范围</th><th scope="col">负责人</th><th scope="col">最近更新</th><th scope="col">操作</th></tr></thead>
        <tbody>
          <tr v-for="(rep, index) in items" :key="rep.id" :aria-label="`报表：${rep.name}`">
            <td class="editorial-table__number">{{ String((page - 1) * 12 + index + 1).padStart(2, '0') }}</td>
            <td class="editorial-table__name"><h2 class="editorial-object-title">{{ rep.name }}</h2><p class="editorial-table__meta">{{ rep.description || '无描述' }}</p><p class="editorial-table__meta">{{ reportChartLabels[rep.chart_type] }} · {{ rep.source_name }} · 修订 {{ rep.source_revision }}</p><p v-if="!rep.connection_id" class="text-xs text-warning mt-1">查看前需配置此修订的个人连接</p></td>
            <td><span class="status-badge">{{ rep.visibility === 'shared' ? '登录后共享' : '仅自己' }}</span></td>
            <td class="whitespace-nowrap">{{ rep.owner_name }}</td>
            <td class="text-xs text-muted whitespace-nowrap"><time :datetime="rep.updated_at">{{ formatTime(rep.updated_at) }}</time></td>
            <td><div class="editorial-table__actions">
              <button type="button" class="editorial-text-action" :data-report-id="rep.id" @click="openReport(rep)">打开</button>
              <button type="button" class="ghost-button ui-icon-button" :class="rep.starred ? 'text-warning' : ''" :disabled="pending.has(rep.id)" :aria-pressed="rep.starred" :aria-label="`${rep.starred ? '取消收藏' : '收藏'} ${rep.name}`" :title="rep.starred ? '取消收藏' : '收藏'" @click="toggleStar(rep)"><Star class="w-4 h-4" :class="rep.starred ? 'fill-current' : ''" aria-hidden="true" /></button>
              <CardActionMenu v-if="canManage(rep)" :label="`报表「${rep.name}」的更多操作`" :items="cardActions(rep)" @select="selectCardAction($event, rep)" />
            </div></td>
          </tr>
        </tbody>
      </table>
      <div v-if="!items.length" class="p-8 text-center space-y-2"><p class="text-sm text-muted">{{ search ? '暂无匹配报表' : '暂无报表' }}</p><p class="text-xs text-subtle">{{ search ? '调整搜索条件后再次查看。' : canWrite ? '点击“新建报表”，选择个人连接并保存只读 SQL。' : '共享报表将在获得对应数据源权限后显示。' }}</p></div>
    </div>
    <div v-if="!loading && !listError" class="flex flex-wrap items-center justify-between gap-2 text-xs text-muted"><span>共 {{total}} 个报表</span><div class="flex items-center gap-2"><button class="ghost-button px-2 py-1 ui-control-target" :disabled="page <= 1" @click="reload(page-1)">上一页</button><span>{{ page }} / {{ totalPages }}</span><button class="ghost-button px-2 py-1 ui-control-target" :disabled="page >= totalPages" @click="reload(page+1)">下一页</button></div></div>
    <ReportEditor v-if="editorOpen" :report="editing" :formal="formalEditor" @close="closeEditor" @saved="saved" @managed-saved="managedSaved" />
    <el-dialog :model-value="detailOpen" :title="current?.name || '报表详情'" width="min(960px, calc(100vw - 24px))" class="glass-dialog" :before-close="closeDetail" @closed="restorePersonalReportFocus">
      <div v-if="current" class="space-y-4 min-w-0">
        <p class="text-xs text-muted">{{ current.source_name }} · 修订 {{ current.source_revision }} · {{ current.database_name || '连接默认数据库' }} · 使用本人数据库账号执行</p>
        <pre class="text-xs font-mono bg-surface-inset rounded-lg p-3 max-h-48 overflow-auto whitespace-pre-wrap break-words">{{ current.sql_text }}</pre>
        <ReportParameterForm v-model="parameterValues" :definitions="current.parameters || []" @apply="runDetail()" @reset="parameterValues = parameterDefaults(current.parameters || [])" />
        <div class="flex flex-wrap items-center gap-3"><button class="ghost-button text-xs ui-control-target" :disabled="detail.state.value.status === 'loading'" @click="runDetail()">{{ detail.state.value.status === 'loading' ? '查询中…' : '刷新数据' }}</button><button v-if="detail.state.value.status === 'loading'" class="ghost-button text-xs ui-control-target" @click="detail.reset">取消查询</button><RouterLink v-if="!current.connection_id" to="/connections" class="text-xs text-accent underline">配置个人连接（修订 {{ current.source_revision }}）</RouterLink></div>
        <LoadingState v-if="detail.state.value.status === 'loading'" variant="detail" label="正在执行只读查询…" />
        <p v-else-if="detail.state.value.status === 'error'" role="alert" class="text-sm text-danger">{{ detail.state.value.message }}</p>
        <ReportResult v-else-if="detail.state.value.status === 'success'" :key="detail.state.value.data.report.id" :type="detail.state.value.data.report.chart_type" :config="detail.state.value.data.report.chart_config" :result="detail.state.value.data.result" :title="detail.state.value.data.report.name" />
        <p v-else class="text-sm text-muted">查询已停止，可刷新数据重新执行只读查询。</p>
      </div>
    </el-dialog>
    </template>
    <PublishedReportsPanel v-else-if="view==='published'" :focus-report-id="reportId" />
    <ReportExportsPanel v-else-if="view==='exports'" :can-request="canRead && featureAllowed(user, 'report.export', true)" />
    <template v-else-if="view==='managed'">
      <ContentDevelopmentTabs v-if="canWrite && !designing" active="report" />
      <ManagedReportsPanel :focus-report-id="reportId" @editing="designing = $event" />
    </template>
    <ReportAccountsPanel v-else-if="view==='accounts' && user.hasCapability('datasource_manage')" />
    <ReportSubscriptionsPanel v-else-if="view==='subscriptions'" :key="`${user.user?.id}:${user.permissionKey}`" />
    <template #unavailable><PageState title="此报表分类不可用" description="请选择当前有权访问的报表分类。" /></template>
    </PageCategoryTabs>
  </div>
</template>
