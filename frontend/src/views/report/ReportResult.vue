<script setup lang="ts">
import { computed, onScopeDispose, ref, useId, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { managedReportApi } from '../../api/managed-report'
import { reportPivot } from './reportPivot'
import { drillParameters } from './reportDrill'
import { parameterQuery } from './reportParameters'
import EChart from '../../components/base/EChart.vue'
import ResultGrid from '../query/components/ResultGrid.vue'
import { reportChart } from './reportChart'
import { reportChartOptions } from './reportChartOptions'
import {
  displayLabel,
  displayUnit,
  formatReportNumber,
  reportNumberRounded,
} from './reportDisplay'
import type {
  ChartConfig,
  ChartKind,
} from '../../components/business/chart/ChartCard.vue'
import type { ExecResult } from '../../api/workbench'
const props = withDefaults(
  defineProps<{
    type: ChartKind
    config: ChartConfig
    result: ExecResult
    title: string
    compact?: boolean
    hideDetails?: boolean
    fill?: boolean
    unit?: string
    currency?: string
  }>(),
  { compact: false, hideDetails: false, unit: '', currency: '' },
)
const unitLabel = computed(() =>
  [displayUnit(props.unit, props.currency), props.unit === 'currency' ? '' : props.currency].filter(Boolean).join(' · '),
)
const chart = computed(() =>
  reportChart(props.type, props.config, props.result),
)
const pivot = computed(() => props.type === 'pivot' ? reportPivot(props.config, props.result) : { columns: [], rows: [], error: undefined })
const renderError = computed(() => chart.value.error || pivot.value.error)
const zeroPieComposition = computed(() => {
  const values = chart.value.series[0]?.values
  return props.type === 'pie' && !!values?.length && values.every(value => value === 0)
})
const route = useRoute()
const router = useRouter()
const drillIndex = ref(0)
const drillError = ref('')
const drillBusy = ref(false)
const drillErrorId = `${useId()}-drill-error`
let drillRequest: AbortController | undefined

function resetDrill() {
  drillRequest?.abort()
  drillRequest = undefined
  drillError.value = ''
  drillBusy.value = false
}

watch(drillIndex, resetDrill, { flush: 'sync' })
watch(() => props.result, () => {
  drillIndex.value = 0
  resetDrill()
})
onScopeDispose(() => drillRequest?.abort())

function drillLabel(row: unknown[]) {
  const index = (props.result.columns ?? []).indexOf(props.config.dimension ?? '')
  return String(row[Math.max(0, index)] ?? 'NULL')
}

async function navigateDrill(index: number) {
  const config = props.config.drillthrough
  if (!config || drillBusy.value || props.result.truncated) return
  const workspaceId = route.query.workspace_id
  const request = new AbortController()
  drillRequest = request
  drillBusy.value = true
  drillError.value = ''
  try {
    const target = await managedReportApi.publishedGet(config.report_id, request.signal)
    if (request.signal.aborted || workspaceId !== route.query.workspace_id) return
    const values = drillParameters(config, props.result, index, target.parameters ?? [])
    await router.push({
      path: '/reports',
      query: { workspace_id: workspaceId, view: 'published', report: String(target.id), ...parameterQuery(values) },
    })
  } catch (error) {
    if (!request.signal.aborted) {
      drillError.value = error instanceof Error ? error.message : '下钻失败，请核对目标报表授权'
    }
  } finally {
    if (drillRequest === request) {
      drillRequest = undefined
      drillBusy.value = false
    }
  }
}
const displayColumns = computed(() =>
  (props.result.columns || []).map((column) =>
    displayLabel(column, props.config.display),
  ),
)
const metricLabel = computed(() => {
  const field = props.config.metrics?.[0] || ''
  const label = displayLabel(field, props.config.display)
  return label === field ? props.title || field : label
})
const roundingApplied = computed(() => {
  const index = (props.result.columns ?? []).indexOf(
    props.config.metrics?.[0] ?? '',
  )
  return reportNumberRounded(
    props.result.rows?.[0]?.[index],
    props.config.display,
  )
})
const metric = computed(() => {
  const index = (props.result.columns ?? []).indexOf(
    props.config.metrics?.[0] ?? '',
  )
  return formatReportNumber(
    props.result.rows?.[0]?.[index],
    props.config.display,
  )
})
const option = computed(() => reportChartOptions(props.type, props.config, chart.value, unitLabel.value))
</script>

<template>
  <section
    class="space-y-3 min-w-0"
    aria-label="报表查询结果"
    :data-chart-kind="type"
    :data-chart-style="type === 'pie' ? config.pie_style ?? 'donut' : undefined"
    :class="{ 'report-result-compact': compact, 'flex min-h-[180px] flex-1 flex-col': fill }"
  >
    <p v-if="!compact" class="text-xs text-muted" role="status">
      返回 {{ result.rows?.length ?? 0 }} 行 ·
      {{ result.duration_ms }} 毫秒<span v-if="!result.truncated">
        · 本次结果完整</span
      >
    </p>
    <p
      v-if="result.truncated"
      class="rounded-lg bg-warning-soft p-3 text-sm text-warning"
      role="alert"
    >
      结果已截断，仅展示已返回的部分数据。请在 SQL
      中完成聚合或缩小查询范围后重新运行。
    </p>
    <p v-if="renderError" class="text-sm text-warning" role="alert">
      {{ renderError }}
    </p>
    <p v-if="type==='dual_axis' && config.dual_axis" class="text-xs text-muted">左轴（柱形）：{{config.dual_axis.left_unit}} · 右轴（折线）：{{config.dual_axis.right_unit}}</p>
    <div
      v-if="type === 'table' || renderError"
      class="ui-inset-card flex min-w-0 border border-stroke rounded-lg overflow-hidden"
      :class="fill ? 'min-h-[124px] flex-1' : compact ? 'h-[260px]' : 'h-[360px]'"
    >
      <ResultGrid :columns="displayColumns" :rows="result.rows ?? []" />
    </div>
    <template v-else-if="type === 'pivot'"><p class="text-xs text-muted">单元格：{{displayLabel(config.metrics?.[0] || '',config.display)}}{{unitLabel?' · '+unitLabel:''}}。透视仅重排 SQL 结果，不在页面汇总；— 表示该组合没有记录，NULL 保留原始空值。</p><div class="h-[360px] flex min-w-0 overflow-hidden"><ResultGrid :columns="pivot.columns" :rows="pivot.rows" /></div></template>
    <p
      v-else-if="!result.rows?.length"
      class="text-sm text-muted py-6 text-center"
      role="status"
    >
      查询成功，未返回数据
    </p>
    <p v-else-if="zeroPieComposition" class="text-sm text-muted py-6 text-center" role="status">
      各类别数值均为 0，暂无可展示的占比
    </p>
    <div
      v-else-if="type === 'metric'"
      :class="compact ? 'py-1' : 'py-8 text-center space-y-2'"
    >
      <p v-if="!compact" class="text-sm text-muted">{{ metricLabel }}</p>
      <p
        class="font-semibold tabular-nums break-all leading-tight"
        :class="compact ? 'text-3xl sm:text-4xl' : 'text-4xl'"
      >
        <span data-testid="report-metric">{{ metric }}</span
        ><span
          v-if="unit || currency"
          class="ml-2 text-sm font-normal text-content"
          >{{ unitLabel }}</span
        >
      </p>
      <p v-if="roundingApplied" class="text-xs text-muted mt-2">
        展示值已四舍五入至
        {{ config.display?.fraction_digits }} 位小数，明细保留原值
      </p>
    </div>
    <EChart
      v-else
      :option="option"
      :height="fill ? 'auto' : compact ? '240px' : '340px'"
      :class="{ 'min-h-[180px] flex-1': fill }"
      :aria-label="`${title}，${result.rows?.length ?? 0} 行数据${hideDetails ? '' : '；下方可展开查询结果'}`"
      @select="navigateDrill"
    />
    <div v-if="config.drillthrough && !result.truncated && result.rows?.length" class="flex flex-wrap items-center gap-2 text-xs">
      <label>明细项
        <select v-model.number="drillIndex" aria-label="选择下钻明细项" class="glass-input ml-2 max-w-full">
          <option v-for="(row, index) in result.rows" :key="index" :value="index">{{ index + 1 }} · {{ drillLabel(row) }}</option>
        </select>
      </label>
      <button type="button" class="ghost-button ui-control-target" :class="{ 'opacity-60': drillBusy }"
        :aria-disabled="drillBusy || undefined" :aria-busy="drillBusy || undefined"
        :aria-describedby="drillError ? drillErrorId : undefined" @click="navigateDrill(drillIndex)">
        {{ drillBusy ? '核对目标权限…' : '查看此项明细' }}
      </button>
      <p v-if="!zeroPieComposition" class="text-muted">可点击图表数据项下钻，目标报表会重新检查当前授权。</p>
    </div>
    <p v-if="drillError" :id="drillErrorId" role="alert" class="text-xs text-danger">{{ drillError }}</p>
    <details
      v-if="
        !hideDetails && type !== 'table' && !renderError && result.rows?.length
      "
      class="text-xs text-muted"
    >
      <summary
        class="cursor-pointer flex items-center focus-visible:outline focus-visible:outline-focus ui-control-target"
      >
        查看查询结果
      </summary>
      <div class="h-[300px] flex min-w-0">
        <ResultGrid :columns="displayColumns" :rows="result.rows ?? []" />
      </div>
    </details>
  </section>
</template>
