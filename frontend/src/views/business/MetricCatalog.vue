<script setup lang="ts">
import {computed,onScopeDispose,ref,useId,watch} from 'vue'
import {useRoute,useRouter} from 'vue-router'
import { RefreshCw, Search } from 'lucide-vue-next'
import {managedReportApi,type PublishedReport} from '../../api/managed-report'
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import {displayLabel,displayUnit} from '../report/reportDisplay'
import {useUserStore} from '../../stores/user'
import {featureAllowed} from '../../utils/permission'
import { revealTableAction, scrollTableByKeyboard } from '../../utils/table-focus'

defineProps<{ embedded?: boolean }>()

const route=useRoute(),router=useRouter(),user=useUserStore(),items=ref<PublishedReport[]>([]),keyword=ref(''),page=ref(1),total=ref(0),loading=ref(true),error=ref('')
const id = useId()
const selected = ref<PublishedReport>()
const detailOpen = ref(false)
const refreshButton = ref<HTMLButtonElement>()
const canViewReport = computed(() =>
  featureAllowed(user, 'report.page', true) && featureAllowed(user, 'report.view', true))
const canDesignDashboard = computed(() =>
  featureAllowed(user, 'dashboard.page', true) &&
  featureAllowed(user, 'dashboard.design', ['admin', 'developer'].includes(user.role)))
let detailTrigger: HTMLElement | undefined
const pageSize = 24
let alive=true,generation=0,controller:AbortController|undefined
const windowName=(report:PublishedReport)=>{const window=report.data_contract?.time_window;return String(window?.definition || ({current_snapshot:'当前快照',calendar_day:'自然日',fixed_period:'固定期间',rolling_window:'滚动窗口',parameterized_period:'按所选日期期间',period_comparison:'本期、上一等长期间与去年同期'} as Record<string,string>)[String(window?.kind)] || '查看报表时间说明')}
const typeName=(type:string)=>({string:'文本',integer:'整数',lossless_numeric:'精确数值',number:'数值',boolean:'是／否',date:'日期',timestamp_with_timezone:'带时区时间'} as Record<string,string>)[type]||type
async function load() {
  const run = ++generation
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  error.value = ''
  items.value = []
  total.value = 0
  detailOpen.value = false
  try {
    const q = typeof route.query.q === 'string' ? route.query.q : ''
    const result = await managedReportApi.metricCatalog({ page: page.value, page_size: pageSize, q }, controller.signal)
    if (alive && run === generation) {
      items.value = result.items
      total.value = result.total
    }
  } catch (cause) {
    if (alive && run === generation) error.value = cause instanceof Error ? cause.message : '业务资产加载失败'
  } finally {
    if (alive && run === generation) loading.value = false
  }
}
function search() {
  if (String(route.query.q || '') === keyword.value && page.value === 1) {
    if (!loading.value) void load()
    return
  }
  void router.replace({ query: { ...route.query, q: keyword.value || undefined, page: '1' } })
}
function refresh(event: MouseEvent) {
  if (loading.value) return
  if (event.currentTarget === document.activeElement) refreshButton.value?.focus()
  void load()
}
function move(direction:number){void router.replace({query:{...route.query,page:String(page.value+direction)}})}
function showDetails(report: PublishedReport, event: MouseEvent) {
  detailTrigger = event.currentTarget instanceof HTMLElement ? event.currentTarget : undefined
  selected.value = report
  detailOpen.value = true
}
function finishClose() {
  selected.value = undefined
  detailTrigger?.focus({ preventScroll: true })
  detailTrigger = undefined
}
watch(()=>[route.query.q,route.query.page],()=>{keyword.value=typeof route.query.q==='string'?route.query.q:'';const next=Number(route.query.page);page.value=Number.isSafeInteger(next)&&next>0&&next<=1000000?next:1;void load()},{immediate:true})
onScopeDispose(()=>{alive=false;generation++;controller?.abort()})
</script>

<template>
  <section class="space-y-3 min-w-0" aria-label="业务资产">
    <PageHeader v-if="!embedded" title="业务资产" />
    <form class="flex flex-wrap items-center gap-2" role="search" aria-label="搜索业务资产" @submit.prevent="search">
      <div class="relative min-w-0 flex-1 basis-56">
        <Search class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted" aria-hidden="true" />
        <label :for="id + '-keyword'" class="sr-only">搜索报表名称或口径</label>
        <input :id="id + '-keyword'" v-model.trim="keyword" type="search" class="glass-input w-full !pl-9"
          placeholder="搜索报表名称或口径" />
      </div>
      <button type="submit" class="ghost-button ui-control-target">搜索</button>
      <button ref="refreshButton" type="button" class="ghost-button ui-control-target ui-icon-button"
        title="刷新业务资产" aria-label="刷新业务资产" :aria-disabled="loading || undefined" @click="refresh">
        <RefreshCw class="size-4" aria-hidden="true" />
      </button>
    </form>

    <div class="flex flex-wrap items-baseline justify-between gap-2">
      <h2 class="editorial-object-title">已发布报表</h2>
      <span v-if="!loading && !error" aria-live="polite" class="text-sm text-muted">共 {{ total }} 个报表</span>
    </div>
    <div class="min-w-0 space-y-3">
      <LoadingState v-if="loading" label="正在读取已批准的指标定义…" />
      <PageState v-else-if="error" heading="h3" title="无法读取业务资产" :description="error" tone="danger">
        <template #actions><button type="button" class="ghost-button ui-control-target" @click="refresh">重试</button></template>
      </PageState>
      <PageState v-else-if="!items.length" heading="h3" title="暂无符合条件的已发布报表"
        description="这里会显示你有权浏览且已发布数据契约的报表。" />
      <div v-else class="editorial-table-wrap relative" role="region" tabindex="0" aria-label="业务资产表格"
        @focusin="revealTableAction" @keydown.self="scrollTableByKeyboard">
        <table class="editorial-table min-w-[480px]" aria-label="已发布报表清单">
          <thead><tr>
            <th scope="col">报表名称</th><th scope="col">统计范围</th>
            <th scope="col">时间口径</th><th scope="col"><span class="sr-only">操作</span></th>
          </tr></thead>
          <tbody>
            <tr v-for="report in items" :key="report.id" :class="{ 'bg-selected': detailOpen && selected?.id === report.id }">
              <th scope="row" class="max-w-72 !whitespace-normal [overflow-wrap:anywhere]">
                <span class="text-sm font-semibold text-content">{{ report.name }}</span>
                <p v-if="report.data_contract?.meaning" class="editorial-table__meta">{{ report.data_contract.meaning }}</p>
              </th>
              <td class="max-w-56 text-sm [overflow-wrap:anywhere]">{{ report.data_contract?.scope_description || '—' }}</td>
              <td class="max-w-48 text-sm [overflow-wrap:anywhere]">{{ windowName(report) }}</td>
              <td><button type="button" class="editorial-text-action ui-control-target"
                :aria-label="'查看口径 ' + report.name" aria-haspopup="dialog"
                @click="showDetails(report, $event)">查看口径</button></td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="!loading && !error && (total > pageSize || page > 1)" class="flex flex-wrap justify-end gap-3 text-sm text-muted">
        <nav class="flex flex-wrap gap-2 items-center" aria-label="业务资产分页">
          <button type="button" class="ghost-button ui-control-target" :disabled="page <= 1" @click="move(-1)">上一页</button>
          <span>第 {{ page }} 页</span>
          <button type="button" class="ghost-button ui-control-target" :disabled="page * pageSize >= total" @click="move(1)">下一页</button>
        </nav>
      </div>
    </div>
    <el-dialog v-model="detailOpen" :title="selected?.name || '报表口径'" class="catalog-detail-dialog"
      modal-class="catalog-detail-overlay" align-center append-to-body destroy-on-close :lock-scroll="false"
      @closed="finishClose">
      <div v-if="selected" class="space-y-4">
        <p v-if="selected.data_contract?.meaning" class="text-sm">{{ selected.data_contract.meaning }}</p>
        <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm">
          <dt class="text-muted">业务标识</dt><dd class="font-mono">{{ selected.data_contract?.semantic_key || '—' }}</dd>
          <dt class="text-muted">版本</dt><dd>{{ selected.version }}</dd>
          <dt class="text-muted">单位</dt><dd>{{ displayUnit(selected.data_contract?.unit, selected.data_contract?.currency) || '—' }}</dd>
          <dt class="text-muted">范围</dt><dd>{{ selected.data_contract?.scope_description || '—' }}</dd>
          <dt class="text-muted">时区</dt><dd>{{ selected.data_contract?.timezone || '—' }}</dd>
          <dt class="text-muted">时间口径</dt><dd>{{ windowName(selected) }}</dd>
          <dt class="text-muted">指标</dt>
          <dd>{{ selected.data_contract?.metrics.map(field => displayLabel(field, selected?.chart_config.display)).join('、') || '明细数据集' }}</dd>
          <dt class="text-muted">维度</dt>
          <dd>{{ selected.data_contract?.dimensions.map(field => displayLabel(field, selected?.chart_config.display)).join('、') || '不分组' }}</dd>
        </dl>
        <details class="text-sm text-muted">
          <summary class="cursor-pointer py-1">字段与时效规则</summary>
          <p class="mt-2">实际新鲜度由执行结果中的业务水位判断。</p>
          <p>{{ selected.data_contract?.freshness.mode === 'watermark_column' ? '按业务水位验证' : '尚未验证业务时效' }}</p>
          <ul class="mt-2 space-y-1">
            <li v-for="column in selected.data_contract?.result_schema" :key="column.name">
              {{ displayLabel(column.name, selected.chart_config.display) }} · {{ typeName(column.logical_type) }} · {{ column.nullable ? '允许空值' : '不能为空' }}
            </li>
          </ul>
        </details>
        <div v-if="canViewReport || canDesignDashboard" class="flex flex-wrap gap-2">
          <RouterLink v-if="canViewReport" :to="{ path: '/reports', query: { workspace_id: user.workspaceId, view: 'published', report: String(selected.id) } }"
            class="liquid-button text-sm ui-control-target inline-flex items-center">查看报表</RouterLink>
          <RouterLink v-if="canDesignDashboard" to="/dashboards"
            class="ghost-button text-sm ui-control-target inline-flex items-center">打开仪表盘开发</RouterLink>
        </div>
      </div>
    </el-dialog>
  </section>
</template>

