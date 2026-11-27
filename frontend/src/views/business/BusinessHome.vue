<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useId } from 'vue'
import { useRouter } from 'vue-router'
import { automationApi, type TodoItem } from '../../api/automation'
import { managedDashboardApi } from '../../api/managed-dashboard'
import { managedReportApi } from '../../api/managed-report'
import { reportApi, type ReportItem } from '../../api/report'
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { useUserStore } from '../../stores/user'
import { inboxTarget } from '../../utils/automation'
import { ApiRequestError } from '../../utils/request'
import { parameterQuery } from '../report/reportParameters'
import { recentReportVisits } from '../report/recentReportVisits'
import { useReportRun } from '../report/useReportRun'

const user = useUserStore()
const router = useRouter()
const reports = useReportRun<Awaited<ReturnType<typeof managedReportApi.published>>>()
const dashboards = useReportRun<Awaited<ReturnType<typeof managedDashboardApi.published>>>()
const todos = useReportRun<Awaited<ReturnType<typeof automationApi.todos>>>()
const favorites = useReportRun<Awaited<ReturnType<typeof reportApi.list>>>()
const recent = useReportRun<{ report: Awaited<ReturnType<typeof managedReportApi.publishedGet>>; openedAt: string }[]>()
const removal = useReportRun<{ starred: boolean }>()
const canCreate = computed(() => ['admin', 'developer'].includes(user.role))
const published = computed(() => reports.state.value.status === 'success' ? reports.state.value.data.items : [])
const searchable = computed(() => published.value.filter(report => report.parameters?.some(parameter => parameter.name === 'q' && parameter.type === 'text')))
const metrics = computed(() => published.value.filter(report => report.chart_type === 'metric' && report.data_contract))
const searchText = ref('')
const searchReport = ref<number>()
const removalError = ref('')
const removalErrorId = useId()
const removalTarget = ref<number>()
const removalNotice = ref('')
const favoritesHeading = ref<HTMLElement>()
const favoritesTable = ref<HTMLElement>()
const favoritesRetry = ref<HTMLButtonElement>()
const time = (value: string) => new Date(value).toLocaleString('zh-CN')
const rowNumber = (index: number) => String(index + 1).padStart(2, '0')
const todoPreview = computed(() => todos.state.value.status === 'success'
  ? todos.state.value.data.items.slice(0, 5).map(item => ({ ...item, location: todoLocation(item) }))
  : [])

function location(path: string, query: Record<string, string> = {}) {
  return { path, query: { ...query, workspace_id: String(user.workspaceId) } }
}
function todoLocation(item: TodoItem) {
  const target = inboxTarget(item.kind, item.resource_id)
  if (!target) return undefined
  const resolved = router.resolve(target)
  return { path: resolved.path, query: { ...resolved.query, workspace_id: String(user.workspaceId) } }
}
async function loadReports() {
  const loaded = await reports.run('published', signal => managedReportApi.published({ page: 1, page_size: 100 }, signal))
  if (loaded && !searchable.value.some(item => item.id === searchReport.value)) searchReport.value = searchable.value[0]?.id
}
function loadDashboards() {
  return dashboards.run('dashboards', signal => managedDashboardApi.published({ page: 1, page_size: 24 }, signal))
}
function loadTodos() {
  return todos.run('todos', signal => automationApi.todos(1, signal))
}
function loadFavorites() {
  return favorites.run('favorites', signal => reportApi.list({ scope: 'starred', page: 1, page_size: 5 }, signal, true))
}
async function reloadFavoritesFrom(focusOrigin: HTMLElement | undefined) {
  const restoreFocus = focusOrigin?.contains(document.activeElement)
  await loadFavorites()
  await nextTick()
  if (restoreFocus && document.activeElement === document.body) {
    (favoritesRetry.value ?? favoritesHeading.value)?.focus()
  }
}
function loadRecent() {
  const visits = recentReportVisits(user.user?.id ?? 0, user.workspaceId)
  return recent.run('recent', async signal => {
    const settled = await Promise.allSettled(visits.map(async visit => {
      const report = await managedReportApi.publishedGet(visit.id, signal)
      return { report, openedAt: visit.openedAt }
    }))
    const available: { report: Awaited<ReturnType<typeof managedReportApi.publishedGet>>; openedAt: string }[] = []
    for (const item of settled) {
      if (item.status === 'fulfilled') available.push(item.value)
      else if (!(item.reason instanceof ApiRequestError && (item.reason.status === 403 || item.reason.status === 404))) throw item.reason
    }
    return available
  })
}
async function unstar(report: ReportItem) {
  if (removal.state.value.status === 'loading' || favorites.state.value.status !== 'success') return
  removalTarget.value = report.id
  removalError.value = ''
  removalNotice.value = ''
  const saved = await removal.run(String(report.id), signal => reportApi.star(report.id, false, signal, true))
  if (saved && removal.state.value.status === 'success' && removal.state.value.data.starred === false) {
    removalNotice.value = '已取消收藏。'
    await reloadFavoritesFrom(favoritesTable.value)
  } else if (removal.state.value.status === 'error') {
    removalError.value = removal.state.value.message || '取消收藏结果待确认，请刷新收藏。'
  } else if (saved) {
    removalError.value = '服务端未确认取消收藏，请刷新收藏。'
  }
}
function search() {
  if (!searchable.value.some(item => item.id === searchReport.value)) return
  void router.push(location('/reports', { view: 'published', report: String(searchReport.value), ...parameterQuery({ q: searchText.value || null }) }))
}
onMounted(() => {
  void loadRecent()
  void loadTodos()
  void loadFavorites()
  void loadReports()
  void loadDashboards()
})
</script>

<template>
  <div class="ui-page-stack min-w-0">
    <PageHeader title="业务工作台" description="查看当前工作空间的报表、待办和收藏。">
      <template v-if="canCreate" #actions>
        <RouterLink class="liquid-button ui-control-target" :to="location('/reports', { view: 'personal', create: '1' })">新建报表</RouterLink>
      </template>
    </PageHeader>

    <section class="space-y-3 min-w-0" aria-labelledby="recent-reports">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h2 id="recent-reports" class="editorial-object-title">最近访问报表</h2>
        <RouterLink class="editorial-text-action" :to="location('/reports', { view: 'published' })">查看已发布报表</RouterLink>
      </div>
      <LoadingState v-if="recent.state.value.status === 'loading'" label="正在核对最近访问…" />
      <PageState v-else-if="recent.state.value.status === 'error'" title="读取最近访问失败" :description="recent.state.value.message || '请重新核对当前可见报表。'" tone="danger" heading="h3">
        <template #actions><button class="ghost-button ui-control-target" @click="loadRecent">重试最近访问</button></template>
      </PageState>
      <template v-else-if="recent.state.value.status === 'success'">
        <p v-if="!recent.state.value.data.length" class="text-sm text-muted">当前标签页暂无可见的报表访问记录。</p>
        <div v-else class="editorial-table-wrap" role="region" tabindex="0" aria-label="最近访问报表表格">
          <table class="editorial-table min-w-[540px]" aria-label="最近访问报表">
            <thead><tr><th scope="col">序号</th><th scope="col">报表名称</th><th scope="col">当前发布号</th><th scope="col">最近访问时间</th><th scope="col">操作</th></tr></thead>
            <tbody><tr v-for="({ report, openedAt }, index) in recent.state.value.data" :key="report.id">
              <td class="editorial-table__number">{{ rowNumber(index) }}</td>
              <th scope="row" class="editorial-table__name">{{ report.name }}</th>
              <td>#{{ report.publication_no }}</td>
              <td class="whitespace-nowrap"><time :datetime="openedAt">{{ time(openedAt) }}</time></td>
              <td><RouterLink class="editorial-text-action whitespace-nowrap" :to="location('/reports', { view: 'published', report: String(report.id) })">打开</RouterLink></td>
            </tr></tbody>
          </table>
        </div>
      </template>
    </section>

    <section class="space-y-3 min-w-0" aria-labelledby="home-todos">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h2 id="home-todos" class="editorial-object-title">我的待办 <span v-if="todos.state.value.status === 'success'" class="text-xs text-muted font-normal">共 {{ todos.state.value.data.total }} 项</span></h2>
        <RouterLink class="editorial-text-action" :to="location('/inbox', { tab: 'business' })">查看全部待办</RouterLink>
      </div>
      <LoadingState v-if="todos.state.value.status === 'loading'" label="正在读取待办…" />
      <PageState v-else-if="todos.state.value.status === 'error'" title="读取待办失败" :description="todos.state.value.message || '请重试读取待办。'" tone="danger" heading="h3">
        <template #actions><button class="ghost-button ui-control-target" @click="loadTodos">重试待办</button></template>
      </PageState>
      <template v-else-if="todos.state.value.status === 'success'">
        <p v-if="!todoPreview.length" class="text-sm text-muted">当前没有待办。</p>
        <div v-else class="editorial-table-wrap" role="region" tabindex="0" aria-label="我的待办表格">
          <table class="editorial-table min-w-[560px]" aria-label="我的待办">
            <thead><tr><th scope="col">序号</th><th scope="col">事项</th><th scope="col">创建时间</th><th scope="col">截止时间</th><th scope="col">操作</th></tr></thead>
            <tbody><tr v-for="(item, index) in todoPreview" :key="item.kind + ':' + item.id">
              <td class="editorial-table__number">{{ rowNumber(index) }}</td>
              <th scope="row" class="editorial-table__name">{{ item.title }}</th>
              <td class="whitespace-nowrap"><time :datetime="item.created_at">{{ time(item.created_at) }}</time></td>
              <td class="whitespace-nowrap"><time v-if="item.expires_at" :datetime="item.expires_at">{{ time(item.expires_at) }}</time><span v-else>未设期限</span></td>
              <td><RouterLink v-if="item.location" class="editorial-text-action whitespace-nowrap" :to="item.location">查看事项</RouterLink><span v-else>—</span></td>
            </tr></tbody>
          </table>
        </div>
      </template>
    </section>

    <section class="space-y-3 min-w-0" aria-labelledby="home-favorites">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h2 id="home-favorites" ref="favoritesHeading" tabindex="-1" class="editorial-object-title">我的收藏 <span v-if="favorites.state.value.status === 'success'" class="text-xs text-muted font-normal">共 {{ favorites.state.value.data.total }} 项</span></h2>
        <div class="flex flex-wrap items-center gap-3">
          <RouterLink class="editorial-text-action" :to="location('/reports', { view: 'personal', scope: 'starred' })">查看全部收藏</RouterLink>
          <button class="ghost-button ui-control-target" :disabled="favorites.state.value.status === 'loading' || removal.state.value.status === 'loading'" @click="loadFavorites">刷新收藏</button>
        </div>
      </div>
      <p v-if="removalNotice" class="text-sm text-success" role="status">{{ removalNotice }}</p>
      <p v-if="removalError" :id="removalErrorId" class="text-sm text-danger" role="alert">{{ removalError }}</p>
      <LoadingState v-if="favorites.state.value.status === 'loading'" label="正在读取收藏…" />
      <PageState v-else-if="favorites.state.value.status === 'error'" title="读取收藏失败" :description="favorites.state.value.message || '请重试读取收藏。'" tone="danger" heading="h3">
        <template #actions><button ref="favoritesRetry" class="ghost-button ui-control-target" @click="reloadFavoritesFrom(favoritesRetry)">重试收藏</button></template>
      </PageState>
      <template v-else-if="favorites.state.value.status === 'success'">
        <p v-if="!favorites.state.value.data.items.length" class="text-sm text-muted">当前没有可见的收藏报表。</p>
        <div v-else ref="favoritesTable" class="editorial-table-wrap" role="region" tabindex="0" aria-label="我的收藏表格">
          <table class="editorial-table min-w-[490px]" aria-label="我的收藏">
            <thead><tr><th scope="col">序号</th><th scope="col">报表名称</th><th scope="col">来源</th><th scope="col">操作</th></tr></thead>
            <tbody><tr v-for="(report, index) in favorites.state.value.data.items" :key="report.id">
              <td class="editorial-table__number">{{ rowNumber(index) }}</td>
              <th scope="row" class="editorial-table__name">{{ report.name }}</th>
              <td>{{ report.source_name }}</td>
              <td><div class="editorial-table__actions whitespace-nowrap">
                <RouterLink class="editorial-text-action" :to="location('/reports', { view: 'personal', scope: 'starred', report: String(report.id) })">打开</RouterLink>
                <button
                  class="editorial-text-action ui-control-target aria-disabled:opacity-50"
                  :aria-disabled="removal.state.value.status === 'loading' || undefined"
                  :aria-describedby="removalError && removalTarget === report.id ? removalErrorId : undefined"
                  :aria-label="'取消收藏 ' + report.name"
                  @click="unstar(report)"
                >取消收藏</button>
              </div></td>
            </tr></tbody>
          </table>
        </div>
      </template>
    </section>

    <section class="space-y-3 min-w-0" aria-labelledby="business-reports">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h2 id="business-reports" class="editorial-object-title">业务报表</h2>
        <RouterLink class="editorial-text-action" :to="location('/reports', { view: 'published' })">查看全部业务报表</RouterLink>
      </div>
      <LoadingState v-if="reports.state.value.status === 'loading'" label="正在读取业务报表…" />
      <PageState v-else-if="reports.state.value.status === 'error'" title="读取业务报表失败" :description="reports.state.value.message || '请重试读取业务报表。'" tone="danger" heading="h3">
        <template #actions><button class="ghost-button ui-control-target" @click="loadReports">重试业务报表</button></template>
      </PageState>
      <template v-else-if="reports.state.value.status === 'success'">
        <form v-if="searchable.length" class="ui-filter-bar" aria-label="按业务编号查询" @submit.prevent="search">
          <label class="flex-1 basis-48 min-w-0 text-sm">订单、会员或运单编号<input v-model.trim="searchText" class="glass-input block w-full mt-1" maxlength="128" /></label>
          <label class="flex-1 basis-40 min-w-0 text-sm">查询报表<select v-model="searchReport" class="glass-input block w-full mt-1"><option v-for="report in searchable" :key="report.id" :value="report.id">{{ report.name }}</option></select></label>
          <button class="liquid-button self-end ui-control-target" :disabled="!searchReport">查询</button>
        </form>
        <p v-if="!published.length" class="text-sm text-muted">暂无已授权的业务报表。</p>
        <div v-else class="editorial-table-wrap" role="region" tabindex="0" aria-label="业务报表表格">
          <table class="editorial-table min-w-[420px]" aria-label="业务报表">
            <thead><tr><th scope="col">序号</th><th scope="col">报表名称</th><th scope="col">发布号</th><th scope="col">操作</th></tr></thead>
            <tbody><tr v-for="(report, index) in published" :key="report.id">
              <td class="editorial-table__number">{{ rowNumber(index) }}</td>
              <th scope="row" class="editorial-table__name">{{ report.name }}<p v-if="report.description" class="editorial-table__meta">{{ report.description }}</p></th>
              <td>#{{ report.publication_no }}</td>
              <td><RouterLink class="editorial-text-action whitespace-nowrap" :to="location('/reports', { view: 'published', report: String(report.id) })">打开</RouterLink></td>
            </tr></tbody>
          </table>
        </div>
      </template>
    </section>

    <section class="space-y-3 min-w-0" aria-labelledby="department-dashboards">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h2 id="department-dashboards" class="editorial-object-title">部门仪表盘</h2>
        <RouterLink class="editorial-text-action" :to="location('/dashboards', { tab: 'published' })">查看全部仪表盘</RouterLink>
      </div>
      <LoadingState v-if="dashboards.state.value.status === 'loading'" label="正在读取仪表盘…" />
      <PageState v-else-if="dashboards.state.value.status === 'error'" title="读取仪表盘失败" :description="dashboards.state.value.message || '请重试读取仪表盘。'" tone="danger" heading="h3">
        <template #actions><button class="ghost-button ui-control-target" @click="loadDashboards">重试仪表盘</button></template>
      </PageState>
      <template v-else-if="dashboards.state.value.status === 'success'">
        <p v-if="!dashboards.state.value.data.items.length" class="text-sm text-muted">当前暂无可见的已发布仪表盘。</p>
        <div v-else class="editorial-table-wrap" role="region" tabindex="0" aria-label="部门仪表盘表格">
          <table class="editorial-table min-w-[420px]" aria-label="部门仪表盘">
            <thead><tr><th scope="col">序号</th><th scope="col">仪表盘名称</th><th scope="col">发布号</th><th scope="col">操作</th></tr></thead>
            <tbody><tr v-for="(dashboard, index) in dashboards.state.value.data.items" :key="dashboard.id">
              <td class="editorial-table__number">{{ rowNumber(index) }}</td>
              <th scope="row" class="editorial-table__name">{{ dashboard.name }}<p v-if="dashboard.description" class="editorial-table__meta">{{ dashboard.description }}</p></th>
              <td>#{{ dashboard.publication_no }}</td>
              <td><RouterLink class="editorial-text-action whitespace-nowrap" :to="location('/dashboards', { tab: 'published', dashboard: String(dashboard.id) })">打开</RouterLink></td>
            </tr></tbody>
          </table>
        </div>
      </template>
    </section>

    <section class="space-y-3 min-w-0" aria-labelledby="business-metrics">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h2 id="business-metrics" class="editorial-object-title">指标口径</h2>
        <RouterLink class="editorial-text-action" :to="location('/metrics')">查看指标目录</RouterLink>
      </div>
      <template v-if="reports.state.value.status === 'success'">
        <p v-if="!metrics.length" class="text-sm text-muted">当前业务报表未提供指标口径。</p>
        <div v-else class="editorial-table-wrap" role="region" tabindex="0" aria-label="指标口径表格">
          <table class="editorial-table min-w-[580px]" aria-label="指标口径">
            <thead><tr><th scope="col">指标</th><th scope="col">业务口径</th><th scope="col">单位</th><th scope="col">时区</th><th scope="col">操作</th></tr></thead>
            <tbody><tr v-for="report in metrics" :key="report.id">
              <th scope="row" class="editorial-table__name">{{ report.name }}</th>
              <td>{{ report.data_contract?.meaning || '—' }}<p v-if="report.data_contract?.scope_description" class="editorial-table__meta">{{ report.data_contract.scope_description }}</p></td>
              <td>{{ report.data_contract?.currency || report.data_contract?.unit || '—' }}</td>
              <td>{{ report.data_contract?.timezone || '—' }}</td>
              <td><RouterLink class="editorial-text-action whitespace-nowrap" :to="location('/reports', { view: 'published', report: String(report.id) })">查看明细</RouterLink></td>
            </tr></tbody>
          </table>
        </div>
      </template>
    </section>
  </div>
</template>

