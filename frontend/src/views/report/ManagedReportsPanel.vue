<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import {featureAllowed} from '../../utils/permission'
import { revealTableAction, scrollTableByKeyboard } from '../../utils/table-focus'
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import { useRoute, useRouter } from 'vue-router'
import { managedReportApi, type ManagedReport, type ReportPage } from '../../api/managed-report'
import { useUserStore } from '../../stores/user'
import { useReportRun } from './useReportRun'
import { reportCommandKeys } from './reportCommands'
import ReportEditor from './ReportEditor.vue'
import ReportReviewDialog from './ReportReviewDialog.vue'
const props=defineProps<{focusReportId?:number}>()
const emit = defineEmits<{ editing: [open: boolean] }>()
const user = useUserStore()
const route = useRoute(), router = useRouter()
const isAdmin = computed(() => ['report.review', 'report.publish', 'report.offline'].some(a => featureAllowed(user, a, user.hasCapability('report_review'))))
const canDesign = computed(() => featureAllowed(user, 'report.design', ['admin', 'developer'].includes(user.role)))
const canSubmit = computed(() => featureAllowed(user, 'report.submit', ['admin', 'developer'].includes(user.role)))
const scope = ref<'all' | 'mine' | 'pending'>(isAdmin.value ? 'all' : 'mine')
const keyword = ref(''), search = ref(''), page = ref(1)
const busy = ref(false), error = ref(''), editorOpen = ref(false), editing = ref<ManagedReport>()
const editorRevision = ref(0), editorFocusOnMount = ref(true)
const savedDraft = ref<number>()
const directory = ref<HTMLElement>(), reviewRegion = ref<HTMLElement>()
const pageSize = 12
const list=useReportRun<ReportPage<ManagedReport>>(),keys=reportCommandKeys();let alive=true
const listed = computed(() => list.state.value.status === 'success' ? list.state.value.data : undefined)
const totalPages = computed(() => Math.max(1, Math.ceil((listed.value?.total ?? 0) / pageSize)))
const errorText=(e:unknown)=>e instanceof Error?e.message:'操作失败，请刷新状态后核对'
async function load(next = page.value) {
  page.value = next
  const range = isAdmin.value ? scope.value : 'mine'
  const q = search.value
  const loaded = await list.run(`${range}:${q}:${next}`, signal => managedReportApi.list({ scope: range, q: q || undefined, page: next, page_size: pageSize }, signal))
  if (loaded && listed.value && !listed.value.items.length && next > totalPages.value) await load(totalPages.value)
}
function applySearch() { search.value = keyword.value.trim(); void load(1) }
function formatTime(value: string) { return new Date(value).toLocaleString() }
function create(){editing.value=undefined;error.value='';savedDraft.value=undefined;editorFocusOnMount.value=true;editorOpen.value=true}
async function edit(report: ManagedReport) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  savedDraft.value = undefined
  try {
    const data = await managedReportApi.get(report.id)
    if (alive && canDesign.value && data.owner_id === user.user?.id) {
      editing.value = data
      editorFocusOnMount.value = true
      editorOpen.value = true
    }
  } catch (e) { if (alive) error.value = errorText(e) }
  finally { if (alive) busy.value = false }
}
function reloadEditor(draft: ManagedReport, restoreFocus: boolean) {
  editing.value = draft
  editorFocusOnMount.value = restoreFocus
  editorRevision.value++
}
async function closeEditor() {
  const id = editing.value?.id
  editorOpen.value = false
  await nextTick()
  const opener = directory.value?.querySelector<HTMLElement>(id ? `[data-edit-id="${id}"]` : '[data-create-report]')
  const focusTarget = opener ?? directory.value
  focusTarget?.focus()
}
function saved(id: number) {
  savedDraft.value = id
  void closeEditor()
  void load()
}
async function openReview(id: number) {
  if (props.focusReportId === id) { focusReview(); return }
  const query = { ...route.query, view: 'managed', report: String(id) }
  await router.push({ query, hash: route.hash })
}
function closeReview() {
  const query = { ...route.query }
  delete query.report
  return router.replace({ query, hash: route.hash })
}
function focusReview() {
  reviewRegion.value?.querySelector<HTMLElement>('h2[tabindex="-1"]')?.focus()
}
async function openPromoted(id:number){await closeReview();error.value='';try{const report=await managedReportApi.get(id);if(alive){editing.value=report;editorFocusOnMount.value=true;editorOpen.value=true;void load(1)}}catch(e){if(alive)error.value=errorText(e)}}
async function submit(report: ManagedReport) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    try {
      await ElMessageBox.confirm(`提交「${report.draft.name}」的当前草稿？提交内容将冻结为新版本，已发布版本继续服务。`, '提交审批', { confirmButtonText: '提交审批', cancelButtonText: '取消' })
    } catch { return }
    if (!alive || !canSubmit.value) return
    await managedReportApi.submit(report.id, report.draft_version, keys.get(`submit:${report.id}`, { version: report.draft_version }))
    keys.clear()
    if (alive) { void load(); await openReview(report.id) }
  } catch (e) { if (alive) error.value = errorText(e) }
  finally { if (alive) busy.value = false }
}
watch(editorOpen, value => emit('editing', value))
watch(isAdmin, value => { scope.value = value ? 'all' : 'mine'; void load(1) })
watch(() => props.focusReportId, async (id, previous) => {
  await nextTick()
  if (id !== undefined) { focusReview(); return }
  if (previous === undefined || editorOpen.value) return
  const opener = directory.value?.querySelector<HTMLButtonElement>(`[data-review-id="${previous}"]`)
  const focusTarget = opener ?? directory.value
  focusTarget?.focus()
}, { immediate: true })
onMounted(()=>void load());onScopeDispose(()=>{alive=false;keys.clear()})
</script>

<template>
  <ReportEditor v-if="editorOpen" :key="editorRevision" formal inline :managed="editing" :focus-on-mount="editorFocusOnMount"
    @close="closeEditor" @managed-saved="saved" @managed-reloaded="reloadEditor" />
  <section v-else ref="directory" tabindex="-1" class="ui-page-stack" aria-label="正式报表管理">
    <PageHeader title="正式报表管理" description="开发报表草稿，审阅冻结版本并管理正式发布。">
      <template #actions><button v-if="canDesign" type="button" data-create-report class="liquid-button ui-control-target" :disabled="busy" @click="create">新建正式报表</button></template>
    </PageHeader>
    <form class="ui-filter-bar" @submit.prevent="applySearch">
      <el-select v-if="isAdmin" v-model="scope" aria-label="正式报表管理范围" class="!w-36 max-w-full" :disabled="busy" @change="load(1)">
        <el-option label="全部开发报表" value="all" /><el-option label="我的开发报表" value="mine" /><el-option label="待审批" value="pending" />
      </el-select>
      <input v-model.trim="keyword" aria-label="搜索开发报表" class="glass-input ui-filter-search text-sm sm:max-w-sm" placeholder="搜索名称或描述" />
      <button type="submit" class="ghost-button text-xs ui-control-target" :disabled="busy">搜索</button>
      <button type="button" class="ghost-button text-xs ui-control-target" :disabled="busy || list.state.value.status === 'loading'" @click="load()">刷新</button>
    </form>
    <p v-if="savedDraft !== undefined" role="status" class="text-sm text-success">报表 #{{ savedDraft }} 草稿已保存。</p>
    <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
    <PageState v-if="list.state.value.status==='error'" title="读取正式报表目录失败" :description="list.state.value.message || '请重试读取目录。'" tone="danger">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="load()">重试读取列表</button></template>
    </PageState>
    <LoadingState v-else-if="list.state.value.status==='loading'" label="正在加载开发报表…" />
    <template v-else-if="listed">
      <div v-if="listed.items.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="正式报表目录表格" @focusin="revealTableAction" @keydown.self="scrollTableByKeyboard">
        <table class="editorial-table" aria-label="正式报表目录">
          <thead><tr><th scope="col">序号</th><th scope="col">报表名称</th><th scope="col">版本</th><th scope="col">状态</th><th scope="col">负责人</th><th scope="col">更新时间</th><th scope="col">操作</th></tr></thead>
          <tbody>
            <tr v-for="(report, index) in listed.items" :key="report.id" :aria-label="`开发报表：${report.draft.name}`">
              <td class="editorial-table__number">{{ String((page - 1) * pageSize + index + 1).padStart(2, '0') }}</td>
              <td class="editorial-table__name"><h2 class="editorial-object-title">{{ report.draft.name }}</h2><p v-if="report.draft.description" class="editorial-table__meta">{{ report.draft.description }}</p></td>
              <td class="whitespace-nowrap"><p v-if="report.active_version">正式 v{{ report.active_version }}</p><p v-if="report.latest_version" class="text-xs text-muted">最新提交 v{{ report.latest_version }}</p><span v-else class="text-xs text-muted">尚未提交</span></td>
              <td class="whitespace-nowrap"><span class="status-badge" :class="{ 'status-badge--success': report.active_release_id }">{{ report.active_release_id ? '已发布' : '未发布' }}</span><p v-if="report.pending_count" class="mt-1 text-xs text-warning">{{ report.pending_count }} 个待审批</p></td>
              <td class="whitespace-nowrap">{{ report.owner_name }}</td>
              <td class="whitespace-nowrap text-xs text-muted"><time :datetime="report.updated_at">{{ formatTime(report.updated_at) }}</time></td>
              <td><div class="editorial-table__actions">
                <button type="button" class="editorial-text-action" :data-review-id="report.id" :aria-expanded="focusReportId === report.id" :disabled="busy" @click="openReview(report.id)">版本与发布</button>
                <template v-if="report.owner_id === user.user?.id && canDesign">
                  <button type="button" class="ghost-button text-xs ui-control-target" :data-edit-id="report.id" :disabled="busy" @click="edit(report)">编辑草稿</button>
                  <button v-if="canSubmit" type="button" class="ghost-button text-xs ui-control-target" :disabled="busy" @click="submit(report)">提交审批</button>
                </template>
              </div></td>
            </tr>
          </tbody>
        </table>
      </div>
      <PageState v-else title="暂无符合条件的开发报表" description="可更换范围或调整搜索条件。" />
      <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-muted"><span>共 {{ listed.total }} 个报表</span><div class="flex items-center gap-2"><button type="button" class="ghost-button ui-control-target" :disabled="page <= 1 || busy" @click="load(page - 1)">上一页</button><span>{{ page }} / {{ totalPages }}</span><button type="button" class="ghost-button ui-control-target" :disabled="page >= totalPages || busy" @click="load(page + 1)">下一页</button></div></div>
    </template>
    <div v-if="focusReportId" ref="reviewRegion">
      <ReportReviewDialog :key="focusReportId" inline :report-id="focusReportId" @close="closeReview" @changed="load()" @promoted="openPromoted" />
    </div>
  </section>
</template>
