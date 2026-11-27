<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import ContentDevelopmentTabs from '../../components/base/ContentDevelopmentTabs.vue'
import { usePageCategory } from '../../composables/usePageCategory'
import { useHandlingReturn } from '../../composables/useHandlingReturn'
import { useDirectoryRefresh } from '../../composables/useDirectoryRefresh'
import { ElMessageBox } from 'element-plus'
import { useRoute, useRouter } from 'vue-router'
import { useUserStore } from '../../stores/user'
import {
  managedDashboardApi as api,
  type ManagedDashboard,
  type DashboardView,
} from '../../api/managed-dashboard'
import {featureAllowed} from '../../utils/permission'
import { reportCommandKeys } from '../report/reportCommands'
import DashboardViewer from './DashboardViewer.vue'
import DashboardEditor from './DashboardEditor.vue'
import DashboardReviewDialog from './DashboardReviewDialog.vue'
import AccessRequestsPanel from './AccessRequestsPanel.vue'
import DashboardSubmitDialog from './DashboardSubmitDialog.vue'
import DashboardDeliveryPanel from './DashboardDeliveryPanel.vue'
import DashboardVersionComparison from './DashboardVersionComparison.vue'
const user = useUserStore(),
  legacyCanWrite = computed(() => ['admin', 'developer'].includes(user.role)),
  canWrite = computed(() => featureAllowed(user, 'dashboard.design', legacyCanWrite.value)),
  canSubmit = computed(() => featureAllowed(user, 'dashboard.submit', legacyCanWrite.value)),
  canManage = computed(()=>canWrite.value||['dashboard.review','dashboard.publish','dashboard.offline'].some(a=>featureAllowed(user,a,user.hasCapability('report_review')))),
  keys = reportCommandKeys()
const canView = computed(() => featureAllowed(user, 'dashboard.view', true))
const categories = computed(() => [
  ...(canView.value ? [{ id: 'published', name: '已发布' }] : []),
  ...(canManage.value ? [{ id: 'managed', name: canWrite.value ? '内容开发' : '仪表盘复核' }] : []),
  ...(canView.value ? [{ id: 'delivery', name: '我的交付' }] : []),
  { id: 'access', name: '访问申请' },
])
const route = useRoute(), router = useRouter()
const { category } = usePageCategory(categories)
const tab = computed(() => category.value?.id)
const keyword = ref('')
const search = ref('')
const scope = ref(canWrite.value ? 'mine' : 'all')
const page = ref(1)
const pageSize = 12
const total = ref<number>()
const dashboardId = computed(() => {
  const id = typeof route.query.dashboard === 'string' ? Number(route.query.dashboard) : 0
  return Number.isSafeInteger(id) && id > 0 ? id : 0
})
const reviewsRoute = computed(() => route.query.review)
const reviewId = computed(() => {
  const raw = reviewsRoute.value
  const value = typeof raw === 'string' && /^\d+$/.test(raw) ? Number(raw) : 0
  return Number.isSafeInteger(value) && value > 0 ? value : undefined
})
const returnToHandling = useHandlingReturn('dashboard-review', reviewId)
const publishedId = computed(() => tab.value === 'published' ? dashboardId.value : 0)
const openDraft = ref<number>()
const viewing = computed<{ id: number; mode: 'published' | 'draft' } | undefined>(() => publishedId.value
  ? { id: publishedId.value, mode: 'published' }
  : openDraft.value ? { id: openDraft.value, mode: 'draft' } : undefined)
const directory = ref<HTMLElement>()
const directoryTable = ref<HTMLElement>()
const refreshButton = ref<HTMLButtonElement>()
const linkLoading = ref(false)
const linkError = ref('')
let listRead: AbortController | undefined
let linkRead: AbortController | undefined
let draftRead: AbortController | undefined
const readingDraft = ref<number>()
const published = ref<DashboardView[]>([]),
  managed = ref<ManagedDashboard[]>([]),
  creating = ref(false),
  editing = ref<ManagedDashboard>(),
  comparing = ref<ManagedDashboard>(),
  reviewing = ref<ManagedDashboard>(),
  loading = ref(false),
  listError = ref(''),
  actionError = ref(''),
  pending = ref<ManagedDashboard>()
const { refresh, retry } = useDirectoryRefresh(refreshButton, load, () => loading.value)
let alive = true
let reviewOpener: 'primary' | 'more' = 'primary'
let pendingReviewFocus: { id: number; opener: 'primary' | 'more' } | undefined
const message = (e: unknown) =>
  e instanceof Error ? e.message : '操作失败，请刷新后核对'

function directoryQuery() {
  return Object.fromEntries(Object.entries(route.query).filter(([key]) => !['dashboard', 'delivery', 'review'].includes(key) && !key.startsWith('p_')))
}

async function selectCategory(id: string) {
  if (id === tab.value || !categories.value.some(item => item.id === id)) return
  await router.push({ query: { ...directoryQuery(), tab: id }, hash: route.hash })
}

async function openPublished(id: number) {
  await router.push({ query: { ...directoryQuery(), tab: 'published', dashboard: String(id) } })
}

async function closeViewer() {
  openDraft.value = undefined
  if (publishedId.value) await router.replace({ query: directoryQuery() })
  await nextTick()
  directory.value?.focus()
}

function changePage(change: number) {
  page.value += change
  void load()
}

function applySearch() {
  search.value = keyword.value.trim()
  page.value = 1
  void load()
}

function changeScope() {
  page.value = 1
  void load()
}

function clearList() {
  published.value = []
  managed.value = []
  total.value = undefined
  listError.value = ''
}

async function load() {
  listRead?.abort()
  const selected = tab.value
  clearList()
  loading.value = false
  if (selected !== 'published' && selected !== 'managed' || viewing.value) return
  const read = new AbortController()
  listRead = read
  loading.value = true
  try {
    const params = { q: search.value, page: page.value, page_size: pageSize }
    if (selected === 'published') {
      const result = await api.published(params, read.signal)
      if (read.signal.aborted) return
      published.value = result.items
      total.value = result.total
    } else {
      const result = await api.list({ ...params, scope: scope.value }, read.signal)
      if (read.signal.aborted) return
      managed.value = result.items
      total.value = result.total
    }
    if (page.value > 1 && page.value > Math.ceil((total.value ?? 0) / pageSize)) {
      page.value = Math.max(1, Math.ceil((total.value ?? 0) / pageSize))
      await load()
    }
  } catch (failure) {
    if (!read.signal.aborted) listError.value = message(failure)
  } finally {
    if (!read.signal.aborted) loading.value = false
  }
}

async function openReviewLink() {
  linkRead?.abort()
  const id = dashboardId.value
  linkLoading.value = false
  linkError.value = ''
  if (tab.value !== 'managed' || !id) return
  if (reviewsRoute.value !== undefined && reviewId.value === undefined) {
    linkError.value = '审批编号无效，请返回原入口。'
    return
  }
  const read = new AbortController()
  linkRead = read
  linkLoading.value = true
  try {
    const item = await api.get(id, read.signal)
    if (!read.signal.aborted) reviewing.value = item
  } catch (failure) {
    if (!read.signal.aborted) linkError.value = message(failure)
  } finally {
    if (!read.signal.aborted) linkLoading.value = false
  }
}

watch(tab, () => {
  page.value = 1
  actionError.value = ''
  openDraft.value = undefined
})
watch([tab, viewing, creating, reviewing, pending], ([category, viewer, create, review, submit]) => {
  if (category !== 'managed' || viewer || create || review || submit) cancelDraftRead()
})
watch([tab, viewing], () => void load(), { immediate: true })
watch([tab, dashboardId, reviewsRoute], () => {
  reviewing.value = undefined
  reviewOpener = 'primary'
  pendingReviewFocus = undefined
  void openReviewLink()
}, { immediate: true })
onScopeDispose(() => {
  alive = false
  listRead?.abort()
  linkRead?.abort()
  cancelDraftRead()
})
function canEditDraft(d: ManagedDashboard) {
  return canWrite.value && d.lifecycle === 'active' && d.owner_id === user.user?.id
}
function canSubmitDraft(d: ManagedDashboard) {
  return canEditDraft(d) && canSubmit.value && d.review_status !== 'pending'
}
function canChangeLifecycle(d: ManagedDashboard) {
  if (d.dashboard_matrix_enabled && d.active_release_id && !(d.can_archive_active ?? (user.role === 'admin'))) return false
  return canWrite.value && (d.owner_id === user.user?.id || user.role === 'admin')
}
function submit(d: ManagedDashboard) {
  if (canSubmitDraft(d)) pending.value = d
}
async function submitted() {
  pending.value = undefined
  if (tab.value === 'managed') await load()
  else await selectCategory('managed')
}
function editFromCheck(d: ManagedDashboard) {
  pending.value = undefined
  void openEditor(d.id)
}
function cancelDraftRead() {
  draftRead?.abort()
  draftRead = undefined
  readingDraft.value = undefined
}
async function openEditor(id: number) {
  if (readingDraft.value === id || !canWrite.value) return
  cancelDraftRead()
  const read = new AbortController()
  draftRead = read
  readingDraft.value = id
  actionError.value = ''
  try {
    const item = await api.get(id, read.signal)
    if (read.signal.aborted || tab.value !== 'managed' || viewing.value || creating.value || reviewing.value || pending.value) return
    if (!canEditDraft(item)) {
      actionError.value = '此草稿当前不可编辑，请刷新目录核对状态。'
      return
    }
    editing.value = item
  } catch (error) {
    if (!read.signal.aborted) actionError.value = message(error)
  } finally {
    if (draftRead === read) {
      draftRead = undefined
      readingDraft.value = undefined
    }
  }
}
function closeEditor() {
  creating.value = false
  editing.value = undefined
}
async function saved() {
  closeEditor()
  if (tab.value === 'managed') await load()
  else await selectCategory('managed')
}
function reviewTrigger(id: number, opener: 'primary' | 'more') {
  return directoryTable.value?.querySelector<HTMLButtonElement>(
    `button[data-dashboard="${id}"][data-review-opener="${opener}"]`,
  )
}

watch(loading, (isLoading) => {
  if (isLoading || !pendingReviewFocus) return
  const { id, opener } = pendingReviewFocus
  pendingReviewFocus = undefined
  if (document.activeElement === directory.value && !reviewing.value && tab.value === 'managed') {
    reviewTrigger(id, opener)?.focus()
  }
}, { flush: 'post' })

async function closeReview() {
  const id = reviewing.value?.id
  const opener = reviewOpener
  reviewing.value = undefined
  if (returnToHandling.value) {
    await router.push(returnToHandling.value)
    return
  }
  if (tab.value === 'managed' && dashboardId.value) await router.replace({ query: directoryQuery() })
  await nextTick()
  if (id === undefined || !alive || reviewing.value || tab.value !== 'managed') return
  const trigger = reviewTrigger(id, opener)
  const focusTarget = trigger ?? directory.value
  focusTarget?.focus()
  if (!trigger && loading.value) pendingReviewFocus = { id, opener }
}
async function openComparedReview(id: number) {
  const dashboard = comparing.value?.id
  comparing.value = undefined
  if (dashboard) await router.push({ query: { ...directoryQuery(), tab: 'managed', dashboard: String(dashboard), review: String(id) } })
}
function openComparedDraft(id: number) {
  comparing.value = undefined
  openDraft.value = id
}
function openComparedPublished(id: number) {
  comparing.value = undefined
  void openPublished(id)
}
function updateReviewerEligibility(dashboardId: number, reviewId: number, eligible: boolean, requiresReReview?: boolean) {
  const row = managed.value.find((item) => item.id === dashboardId && item.review_id === reviewId)
  if (row) {
    row.reviewer_eligible = eligible
    row.review_requires_re_review = requiresReReview
  }
}

function needsReReview(d: ManagedDashboard) {
  return d.review_requires_re_review ?? (d.reviewer_eligible === false)
}

function stateLabel(d: ManagedDashboard) {
  if (d.lifecycle === 'archived') return '已归档'
  if (d.review_status === 'pending') return '待审阅'
  if (d.review_status === 'approved' && !d.review_was_published)
    return needsReReview(d)
      ? d.dashboard_matrix_enabled ? '业务签署失效 · 待重新送审' : '审阅资格失效 · 待重新送审'
      : '已批准 · 待发布'
  if (d.active_release_id) return '已发布'
  if (d.review_status === 'approved' && d.review_was_published && d.publication_no > 0) return '已下线'
  return (
    (
      {
        rejected: '已退回',
        withdrawn: '已撤回',
        expired: '审阅已过期',
      } as Record<string, string>
    )[d.review_status] || '草稿'
  )
}
function primaryLabel(d: ManagedDashboard) {
  if (d.lifecycle === 'archived') return '查看历史版本'
  if (d.review_status === 'pending')
    return d.reviewer_eligible !== false && featureAllowed(user,'dashboard.review',user.hasCapability('report_review')) && d.reviewer_id === user.user?.id
      ? '审阅'
      : '查看审阅'
  if (d.review_status === 'approved' && !d.review_was_published) {
    if (needsReReview(d)) return '查看审阅'
    if (d.dashboard_matrix_enabled)
      return d.reviewer_id === user.user?.id && featureAllowed(user, 'dashboard.publish', user.hasCapability('report_review'))
        ? '审阅与发布' : '查看发布进度'
    return featureAllowed(user,'dashboard.review',user.hasCapability('report_review')) ? '审阅与发布' : '查看发布进度'
  }
  return canEditDraft(d) ? '编辑草稿' : '查看版本'
}
function primaryAction(d: ManagedDashboard) {
  if (primaryLabel(d) === '编辑草稿') void openEditor(d.id)
  else {
    reviewOpener = 'primary'
    reviewing.value = d
  }
}
function moreAction(action: string, d: ManagedDashboard) {
  if (action === 'history') {
    reviewOpener = 'more'
    reviewing.value = d
  }
  else if (action === 'preview' && canEditDraft(d)) openDraft.value = d.id
  else if (action === 'edit') void openEditor(d.id)
  else if (action === 'transfer') void transfer(d)
  else if (action === 'archive')
    void lifecycle(d, d.latest_version ? 'archive' : 'delete')
  else if (action === 'restore') void lifecycle(d, 'restore')
}
async function lifecycle(d: ManagedDashboard, action: string) {
  if (!canChangeLifecycle(d)) return
  try {
    await ElMessageBox.confirm(
      action === 'archive'
        ? '归档会立即下线、取消待审和访问申请，并使所有刷新上下文失效。'
        : action === 'restore'
          ? '恢复后为草稿，旧批准不能重新发布，需重新送审。'
          : '删除这个从未送审的草稿？',
      '确认操作',
    )
    const payload = { version: d.draft_version }
    await api.command(
      d.id,
      action,
      payload,
      keys.get(`${action}:${d.id}`, payload),
    )
    if (alive) await load()
  } catch (e) {
    if (e instanceof Error && alive) actionError.value = message(e)
  }
}
async function transfer(d: ManagedDashboard) {
  try {
    const input = await ElMessageBox.prompt(
      '输入本工作空间有效开发者或管理员的用户编号。归属移交不改变当前正式发布。',
      '移交看板责任人',
      { inputPattern: /^[1-9]\d*$/, inputErrorMessage: '请输入用户编号' },
    )
    const payload = { version: d.draft_version, owner_id: Number(input.value) }
    await api.command(
      d.id,
      'transfer',
      payload,
      keys.get(`transfer:${d.id}`, payload),
    )
    if (alive) await load()
  } catch (e) {
    if (e instanceof Error && alive) actionError.value = message(e)
  }
}
</script>

<template>
  <div class="ui-page-stack">
    <DashboardViewer v-if="viewing" :key="viewing.mode + ':' + viewing.id" :id="viewing.id" :mode="viewing.mode" @close="closeViewer" />
    <template v-else>
      <div ref="directory" tabindex="-1" class="ui-focus-target">
        <PageHeader :title="tab === 'managed' ? (canWrite ? '内容开发' : '仪表盘复核') : '仪表盘目录'"
          :description="tab === 'managed' ? '分别查看当前发布与本次开发版本。' : '查看你有权访问的已发布仪表盘。'">
          <template #actions>
            <button v-if="tab === 'published' || tab === 'managed'" ref="refreshButton" type="button" class="ghost-button ui-control-target" :aria-disabled="loading || undefined" @click="refresh">刷新列表</button>
            <button v-if="canWrite && tab === 'managed'" class="liquid-button ui-control-target" @click="creating = true">新建仪表盘</button>
          </template>
        </PageHeader>
      </div>
      <ContentDevelopmentTabs v-if="tab === 'managed' && canWrite && !reviewId" active="dashboard" />

      <PageCategoryTabs label="仪表盘分类" :categories="categories" :active-id="tab" @select="selectCategory">
        <AccessRequestsPanel v-if="tab === 'access'" />
        <DashboardDeliveryPanel v-else-if="tab === 'delivery'" />
        <div v-else class="ui-page-stack">
          <form class="glass-panel ui-card-padding flex flex-wrap items-end gap-3" aria-label="筛选仪表盘" @submit.prevent="applySearch">
            <label class="text-sm flex-1 min-w-0 basis-48">
              搜索仪表盘
              <input v-model="keyword" class="glass-input block w-full mt-1" placeholder="搜索名称或说明" maxlength="128" />
            </label>
            <div v-if="tab === 'managed' && featureAllowed(user, 'dashboard.review', user.hasCapability('report_review'))" class="w-full sm:w-44">
              <label for="dashboard-scope" class="block text-sm">开发范围</label>
              <select id="dashboard-scope" v-model="scope" class="glass-input block w-full mt-1" @change="changeScope">
                <option value="all">全部开发看板</option>
                <option value="mine">我的开发</option>
                <option value="pending">待审阅</option>
              </select>
            </div>
            <button class="liquid-button ui-control-target">搜索</button>
          </form>
          <p v-if="actionError" role="alert" class="text-danger text-sm">{{ actionError }}</p>
          <LoadingState v-if="linkLoading" label="正在读取审阅对象…" variant="inline" />
          <PageState v-else-if="linkError" title="无法打开仪表盘审阅" :description="linkError" tone="danger">
            <template #actions>
              <button class="ghost-button ui-control-target" @click="openReviewLink">重试打开审阅</button>
              <button v-if="returnToHandling" class="ghost-button ui-control-target" @click="closeReview">返回办理中心</button>
            </template>
          </PageState>
          <LoadingState v-if="loading" label="正在读取仪表盘目录…" />
          <PageState v-else-if="listError" title="读取仪表盘目录失败" :description="listError" tone="danger">
            <template #actions><button type="button" class="ghost-button ui-control-target" @click="retry">重试读取列表</button></template>
          </PageState>
          <PageState v-else-if="total === 0" :title="tab === 'published' ? '暂无可见的正式仪表盘' : '暂无匹配的开发仪表盘'"
            description="可调整当前搜索条件或查看其他获准分类。" />
          <template v-else-if="total !== undefined">
            <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="仪表盘目录表格">
              <table v-if="tab === 'published'" class="editorial-table min-w-[530px]" aria-label="已发布仪表盘">
                <thead><tr><th scope="col">序号</th><th scope="col">仪表盘名称</th><th scope="col">状态</th><th scope="col">内容版本</th><th scope="col">操作</th></tr></thead>
                <tbody>
                  <tr v-for="(item, index) in published" :key="item.id">
                    <td class="editorial-table__number">{{ String((page - 1) * pageSize + index + 1).padStart(2, '0') }}</td>
                    <td class="editorial-table__name">
                      <h2 class="editorial-object-title break-words">{{ item.name }}</h2>
                      <p v-if="item.description" class="editorial-table__meta">{{ item.description }}</p>
                    </td>
                    <td><span class="bg-success-soft text-success px-2 py-1 whitespace-nowrap">已发布 #{{ item.publication_no }}</span></td>
                    <td>v{{ item.version }}</td>
                    <td><button class="ghost-button ui-control-target whitespace-nowrap" :aria-label="'打开仪表盘 · ' + item.name"
                      @click="openPublished(item.id)">打开仪表盘</button></td>
                  </tr>
                </tbody>
              </table>
              <table v-else ref="directoryTable" class="editorial-table min-w-[760px]" aria-label="仪表盘开发与版本">
                <thead><tr><th scope="col">序号</th><th scope="col">仪表盘名称</th><th scope="col">当前发布</th><th scope="col">本次开发</th><th scope="col">责任人</th><th scope="col">最近更新</th><th scope="col">操作</th></tr></thead>
                <tbody>
                  <tr v-for="(item, index) in managed" :key="item.id">
                    <td class="editorial-table__number">{{ String((page - 1) * pageSize + index + 1).padStart(2, '0') }}</td>
                    <td class="editorial-table__name">
                      <h2 class="editorial-object-title break-words">{{ item.draft.name }}</h2>
                      <p v-if="item.draft.description" class="editorial-table__meta">{{ item.draft.description }}</p>
                      <p class="editorial-table__meta">仪表盘 #{{ item.id }}</p>
                    </td>
                    <td>
                      <template v-if="item.active_release_id">
                        <span v-if="item.active_version">v{{ item.active_version }} · </span>发布 #{{ item.publication_no }}
                      </template>
                      <span v-else class="text-muted">未生效</span>
                    </td>
                    <td>
                      <p v-if="item.review_version">v{{ item.review_version }} · {{ stateLabel(item) }}</p>
                      <p v-else>{{ stateLabel(item) }}</p>
                      <p v-if="canEditDraft(item)" class="editorial-table__meta">草稿修订 {{ item.draft_version }}</p>
                    </td>
                    <td class="break-words">{{ item.owner_name || '—' }}</td>
                    <td><time v-if="item.updated_at" :datetime="item.updated_at">{{ new Date(item.updated_at).toLocaleString('zh-CN') }}</time><span v-else>—</span></td>
                    <td>
                      <div class="flex flex-wrap items-center gap-2">
                        <button class="ghost-button ui-control-target whitespace-nowrap" :data-dashboard="item.id" :aria-disabled="readingDraft === item.id"
                          data-review-opener="primary" @click="primaryAction(item)">{{ primaryLabel(item) }}</button>
                        <button v-if="canSubmitDraft(item)"
                          class="ghost-button ui-control-target whitespace-nowrap" @click="submit(item)">送审检查</button>
                        <button v-if="canEditDraft(item)" type="button" class="ghost-button ui-control-target whitespace-nowrap"
                          @click="comparing = item">版本对照</button>
                        <el-dropdown trigger="click" @command="moreAction($event, item)">
                          <button class="ghost-button ui-control-target" :aria-label="item.draft.name + '更多操作'"
                            :data-dashboard="item.id" data-review-opener="more">更多</button>
                          <template #dropdown>
                            <el-dropdown-menu>
                              <el-dropdown-item command="history">版本、审阅与发布</el-dropdown-item>
                              <el-dropdown-item v-if="canEditDraft(item)" command="preview">预览已保存草稿</el-dropdown-item>
                              <el-dropdown-item v-if="canEditDraft(item) && primaryLabel(item) !== '编辑草稿'" command="edit">编辑草稿</el-dropdown-item>
                              <el-dropdown-item v-if="item.lifecycle === 'active' && user.role === 'admin' && canWrite" command="transfer">移交责任人</el-dropdown-item>
                              <template v-if="canChangeLifecycle(item)">
                                <el-dropdown-item v-if="item.lifecycle === 'active'" command="archive" divided>{{ item.latest_version ? '归档' : '删除草稿' }}</el-dropdown-item>
                                <el-dropdown-item v-else command="restore" divided>恢复为草稿</el-dropdown-item>
                              </template>
                            </el-dropdown-menu>
                          </template>
                        </el-dropdown>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="flex flex-wrap justify-between items-center gap-3 text-sm text-muted">
              <span aria-live="polite">共 {{ total }} 个仪表盘</span>
              <div class="flex gap-2 items-center">
                <button class="ghost-button ui-control-target" :disabled="page <= 1" @click="changePage(-1)">上一页</button>
                <span>第 {{ page }} 页</span>
                <button class="ghost-button ui-control-target" :disabled="page * pageSize >= total" @click="changePage(1)">下一页</button>
              </div>
            </div>
          </template>
        </div>
        <template #unavailable>
          <PageState title="此仪表盘分类不可用" description="请选择当前有权访问的分类。" />
        </template>
      </PageCategoryTabs>
    </template>
    <DashboardEditor v-if="creating || editing" :dashboard="editing" @close="closeEditor" @saved="saved" />
    <DashboardVersionComparison v-if="comparing" :dashboard="comparing" @close="comparing = undefined"
      @review="openComparedReview" @draft="openComparedDraft" @published="openComparedPublished" />
    <DashboardReviewDialog v-if="reviewing" :dashboard="reviewing" :review-id="reviewId" :handling-origin="!!returnToHandling"
      @close="closeReview" @changed="load"
      @reviewer-eligibility="updateReviewerEligibility" />
    <DashboardSubmitDialog v-if="pending" :dashboard="pending" @close="pending = undefined" @submitted="submitted" @edit="editFromCheck" />
  </div>
</template>
