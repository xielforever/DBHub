<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute, useRouter, type LocationQuery } from 'vue-router'
import { managedReportApi, type PublishedReport, type ReportPage } from '../../api/managed-report'
import type { ExecResult } from '../../api/workbench'
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { useDirectoryRefresh } from '../../composables/useDirectoryRefresh'
import { useUserStore } from '../../stores/user'
import { featureAllowed } from '../../utils/permission'
import { useReportRun } from './useReportRun'
import ReportResult from './ReportResult.vue'
import ReportParameterForm from './ReportParameterForm.vue'
import ReportExportDialog from './ReportExportDialog.vue'
import { parameterDefaults, parameterQuery, parametersFromQuery, type ParameterValues } from './reportParameters'
import { recordReportVisit } from './recentReportVisits'
const props = defineProps<{ focusReportId?: number }>()
const route = useRoute()
const router = useRouter()
const user = useUserStore()
const list = useReportRun<ReportPage<PublishedReport>>()
const detail = useReportRun<ExecResult>()
const metadata = useReportRun<PublishedReport>()
const querying = computed(() => detail.state.value.status === 'loading')
const current = computed(() => metadata.state.value.status === 'success' && metadata.state.value.data.id === props.focusReportId
  ? metadata.state.value.data : undefined)
const canExport = computed(() => current.value?.export_policy?.enabled && featureAllowed(user, 'report.export', true))
const definitions = computed(() => current.value?.parameters ?? [])
const parameterValues = ref<ParameterValues>({})
const parameterForm = ref<InstanceType<typeof ReportParameterForm>>()
const exportOpen = ref(false)
const readerHeading = ref<HTMLElement>()
const directory = ref<HTMLElement>()
const refreshButton = ref<HTMLButtonElement>()
const pageSize = 12
const page = ref(1)
const keyword = ref('')
const search = ref('')
const listLoading = computed(() => list.state.value.status === 'loading')
const listed = computed(() => list.state.value.status === 'success' ? list.state.value.data : undefined)
const totalPages = computed(() => Math.max(1, Math.ceil((listed.value?.total ?? 0) / pageSize)))
const { refresh, retry } = useDirectoryRefresh(refreshButton, load, () => listLoading.value)

async function load(next = page.value) {
  page.value = next
  const q = search.value
  const loaded = await list.run(`${next}:${q}`, signal => managedReportApi.published({ q: q || undefined, page: next, page_size: pageSize }, signal))
  if (loaded && listed.value && !listed.value.items.length && next > totalPages.value) await load(totalPages.value)
}

function applySearch() {
  search.value = keyword.value.trim()
  void load(1)
}

function directoryQuery() {
  return Object.fromEntries(Object.entries(route.query).filter(([key]) => key !== 'report' && !key.startsWith('p_')))
}

function openReport(id: number) {
  return router.push({ query: { ...directoryQuery(), view: 'published', report: String(id) }, hash: route.hash })
}

function returnToDirectory() {
  return router.replace({ query: { ...directoryQuery(), view: 'published' }, hash: route.hash })
}

function filtersIn(query: LocationQuery) {
  return Object.fromEntries(Object.entries(query).filter(([key]) => key.startsWith('p_')).sort(([a], [b]) => a.localeCompare(b)))
}

function readFiltersAndRun() {
  parameterValues.value = { ...parameterDefaults(definitions.value), ...parametersFromQuery(definitions.value, route.query) }
  void run()
}

async function loadDefinition(id = props.focusReportId) {
  if (!id) return
  const loaded = await metadata.run(String(id), signal => managedReportApi.publishedGet(id, signal))
  if (loaded && current.value) {
    if (user.user?.id && user.workspaceId) recordReportVisit(user.user.id, user.workspaceId, current.value.id)
    readFiltersAndRun()
  }
}

async function run() {
  const report = current.value
  if (!report) return
  const params = { ...parameterValues.value }
  await detail.run(`${report.id}:${report.publication_no}:${JSON.stringify(params)}`,
    signal => managedReportApi.execute(report.id, report.publication_no, signal, params))
}

async function applyFilters() {
  const report = current.value
  if (!report) return
  if (parameterForm.value && !await parameterForm.value.validate()) return
  if (current.value !== report) return
  const query = { ...directoryQuery(), view: 'published', report: String(report.id), ...parameterQuery(parameterValues.value) }
  if (JSON.stringify(filtersIn(query)) === JSON.stringify(filtersIn(route.query))) await run()
  else await router.push({ query, hash: route.hash })
}

function refreshReport() {
  if (!querying.value) void applyFilters()
}

async function resetFilters() {
  parameterValues.value = parameterDefaults(definitions.value)
  await nextTick()
  void applyFilters()
}

watch(parameterValues, () => {
  detail.reset()
  exportOpen.value = false
}, { deep: true, flush: 'sync' })

watch(() => route.query, (query, previous) => {
  if (current.value && typeof query.report === 'string' && Number(query.report) === current.value.id && query.view === 'published'
    && JSON.stringify(filtersIn(query)) !== JSON.stringify(filtersIn(previous))) readFiltersAndRun()
})

watch(() => props.focusReportId, async (id, previous) => {
  metadata.reset()
  parameterValues.value = {}
  if (id !== undefined) {
    if (list.state.value.status === 'loading') list.reset()
    void loadDefinition(id)
    await nextTick()
    if (props.focusReportId === id) readerHeading.value?.focus()
    return
  }
  const pendingList = list.state.value.status === 'idle' ? load() : undefined
  if (previous === undefined) return
  await nextTick()
  const container = directory.value
  if (!container || props.focusReportId !== undefined) return
  const findOpener = () => container.querySelector<HTMLButtonElement>(`button[data-report-id="${previous}"]`)
  const opener = findOpener()
  if (opener) {
    opener.focus()
    return
  }
  container.focus()
  if (!pendingList) return
  await pendingList
  await nextTick()
  if (directory.value === container && props.focusReportId === undefined && document.activeElement === container) {
    findOpener()?.focus()
  }
}, { immediate: true })
</script>

<template>
  <section v-if="focusReportId === undefined" ref="directory" tabindex="-1" class="space-y-4" aria-label="已发布报表">
    <form class="ui-filter-bar" @submit.prevent="applySearch">
      <input v-model.trim="keyword" aria-label="搜索已发布报表" class="glass-input ui-filter-search text-sm sm:max-w-sm" placeholder="搜索报表名称或描述" />
      <button type="submit" class="ghost-button text-xs ui-control-target">搜索</button>
      <button ref="refreshButton" type="button" class="ghost-button text-xs ui-control-target" :aria-disabled="listLoading || undefined" @click="refresh">刷新</button>
    </form>
    <div v-if="listLoading || listed?.items.length">
      <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="已发布报表目录表格">
        <table class="editorial-table table-fixed min-w-[34rem]" aria-label="已发布报表目录" :aria-busy="listLoading">
          <colgroup>
            <col class="w-14" />
            <col />
            <col class="w-20" />
            <col class="w-28" />
            <col class="w-24" />
          </colgroup>
          <thead><tr><th scope="col">序号</th><th scope="col">报表名称</th><th scope="col">发布号</th><th scope="col">已批准版本</th><th scope="col">操作</th></tr></thead>
          <tbody>
            <tr v-for="(report, index) in listed?.items" :key="report.id" :aria-label="`正式报表：${report.name}`">
              <td class="editorial-table__number">{{ String((page - 1) * pageSize + index + 1).padStart(2, '0') }}</td>
              <td class="editorial-table__name">
                <h2 class="editorial-object-title">{{ report.name }}</h2>
                <p v-if="report.description" class="editorial-table__meta">{{ report.description }}</p>
              </td>
              <td>{{ report.publication_no }}</td>
              <td><span class="status-badge status-badge--success">v{{ report.version }}</span></td>
              <td><button type="button" class="editorial-text-action" :data-report-id="report.id" @click="openReport(report.id)">打开报表</button></td>
            </tr>
          </tbody>
        </table>
      </div>
      <LoadingState v-if="listLoading" label="正在读取已发布报表…" />
    </div>
    <PageState v-else-if="list.state.value.status === 'error'" title="读取已发布报表失败" :description="list.state.value.message || '请重试读取目录。'" tone="danger">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="retry">重试读取列表</button></template>
    </PageState>
    <PageState v-else-if="listed" :title="search ? '暂无匹配报表' : '暂无可访问的已发布报表'"
      :description="search ? '调整搜索条件后再次查看。' : '通过审批且已授权给你的正式报表将在这里显示。'" />
    <template v-if="listed">
      <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-muted">
        <span>共 {{ listed.total }} 个报表</span>
        <div class="flex items-center gap-2">
          <button type="button" class="ghost-button ui-control-target" :disabled="page <= 1" @click="load(page - 1)">上一页</button>
          <span>{{ page }} / {{ totalPages }}</span>
          <button type="button" class="ghost-button ui-control-target" :disabled="page >= totalPages" @click="load(page + 1)">下一页</button>
        </div>
      </div>
    </template>
  </section>
  <section v-else class="ui-page-stack min-w-0" aria-label="正式报表阅读">
    <div ref="readerHeading" tabindex="-1">
      <PageHeader :title="current?.name || '正式报表'" :description="current?.description">
        <template #actions><button type="button" class="ghost-button ui-control-target" @click="returnToDirectory">返回目录</button></template>
      </PageHeader>
    </div>
    <PageState v-if="metadata.state.value.status === 'error'" title="读取报表定义失败" :description="metadata.state.value.message || '请重试读取报表定义。'" tone="danger">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="loadDefinition()">重试读取定义</button></template>
    </PageState>
    <LoadingState v-else-if="!current" variant="detail" label="正在读取报表定义…" />
    <template v-else>
      <p class="text-sm text-muted">发布号 {{ current.publication_no }} · 已批准版本 v{{ current.version }} · 使用受管报表执行账号查询</p>
      <ReportParameterForm ref="parameterForm" v-model="parameterValues" :definitions="definitions" @apply="applyFilters" @reset="resetFilters" />
      <div class="flex flex-wrap gap-2">
        <button type="button" class="ghost-button text-xs ui-control-target" :class="{ 'opacity-60': querying }"
          :aria-disabled="querying || undefined" :aria-busy="querying || undefined" @click="refreshReport">刷新数据</button>
        <button v-if="querying" type="button" class="ghost-button text-xs ui-control-target" @click="detail.reset">取消查询</button>
        <button v-if="canExport" type="button" class="ghost-button text-xs ui-control-target" @click="exportOpen = true">申请完整导出</button>
      </div>
      <LoadingState v-if="querying" variant="detail" label="正在查询…" />
      <PageState v-else-if="detail.state.value.status === 'error'" title="报表查询失败" :description="detail.state.value.message || '请重试查询。'" tone="danger">
        <template #actions><button type="button" class="ghost-button ui-control-target" @click="run">重试查询</button></template>
      </PageState>
      <ReportResult v-else-if="detail.state.value.status === 'success'" :type="current.chart_type" :config="current.chart_config"
        :result="detail.state.value.data" :title="current.name" :unit="current.data_contract?.unit" :currency="current.data_contract?.currency" />
      <p v-else class="text-sm text-muted">可应用筛选或刷新数据重新查询。</p>
      <ReportExportDialog v-if="exportOpen && canExport" :report="current" :params="parameterValues" @close="exportOpen = false" />
    </template>
  </section>
</template>
