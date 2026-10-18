<template>
  <section
    class="space-y-3 min-w-0"
    aria-label="报表查询结果"
    :class="{ 'report-result-compact': compact }"
  >
    <p v-if="!compact" class="text-xs text-white/55" role="status">
      返回 {{ result.rows?.length ?? 0 }} 行 ·
      {{ result.duration_ms }} 毫秒<span v-if="!result.truncated">
        · 本次结果完整</span
      >
    </p>
    <p
      v-if="result.truncated"
      class="rounded-lg bg-amber-400/10 p-3 text-sm text-amber-200"
      role="alert"
    >
      结果已截断，仅展示已返回的部分数据。请在 SQL
      中完成聚合或缩小查询范围后重新运行。
    </p>
    <p v-if="chart.error" class="text-sm text-amber-200" role="alert">
      {{ chart.error }}
    </p>
    <div
      v-if="type === 'table' || chart.error"
      class="flex min-w-0 border border-white/10 rounded-lg overflow-hidden"
      :class="compact ? 'h-[260px]' : 'h-[360px]'"
    >
      <ResultGrid :columns="displayColumns" :rows="result.rows ?? []" />
    </div>
    <p
      v-else-if="!result.rows?.length"
      class="text-sm text-white/60 py-6 text-center"
      role="status"
    >
      查询成功，未返回数据
    </p>
    <div
      v-else-if="type === 'metric'"
      :class="compact ? 'py-1' : 'py-8 text-center space-y-2'"
    >
      <p v-if="!compact" class="text-sm text-white/60">{{ metricLabel }}</p>
      <p
        class="font-semibold tabular-nums break-all leading-tight"
        :class="compact ? 'text-3xl sm:text-4xl' : 'text-4xl'"
      >
        <span data-testid="report-metric">{{ metric }}</span
        ><span
          v-if="unit || currency"
          class="ml-2 text-sm font-normal text-white/75"
          >{{ unitLabel }}</span
        >
      </p>
      <p v-if="roundingApplied" class="text-xs text-white/50 mt-2">
        展示值已四舍五入至
        {{ config.display?.fraction_digits }} 位小数，明细保留原值
      </p>
    </div>
    <EChart
      v-else
      :option="option"
      :height="compact ? '240px' : '340px'"
      :aria-label="`${title}，${result.rows?.length ?? 0} 行数据；下方可展开查询结果`"
    />
    <details
      v-if="
        !hideDetails && type !== 'table' && !chart.error && result.rows?.length
      "
      class="text-xs text-white/60"
    >
      <summary
        class="cursor-pointer min-h-11 flex items-center focus-visible:outline focus-visible:outline-indigo-300"
      >
        查看查询结果
      </summary>
      <div class="h-[300px] flex min-w-0">
        <ResultGrid :columns="result.columns ?? []" :rows="result.rows ?? []" />
      </div>
    </details>
  </section>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import EChart from '../../components/base/EChart.vue'
import ResultGrid from '../query/components/ResultGrid.vue'
import { reportChart } from './reportChart'
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
import type { EChartsCoreOption } from 'echarts/core'
const props = withDefaults(
  defineProps<{
    type: ChartKind
    config: ChartConfig
    result: ExecResult
    title: string
    compact?: boolean
    hideDetails?: boolean
    unit?: string
    currency?: string
  }>(),
  { compact: false, hideDetails: false, unit: '', currency: '' },
)
const unitLabel = computed(() =>
  [displayUnit(props.unit), props.currency].filter(Boolean).join(' · '),
)
const chart = computed(() =>
  reportChart(props.type, props.config, props.result),
)
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
const option = computed<EChartsCoreOption>(() => {
  const data = chart.value
  const series = data.series.map((s) => ({
    ...s,
    name: displayLabel(s.name, props.config.display),
  }))
  const color = [
    '#a78bfa',
    '#60a5fa',
    '#34d399',
    '#fbbf24',
    '#f472b6',
    '#67e8f9',
  ]
  const valueFormatter = (value: unknown) =>
    formatReportNumber(value, props.config.display)
  if (props.type === 'pie')
    return {
      color,
      tooltip: { trigger: 'item', valueFormatter },
      legend: { type: 'scroll', bottom: 0, textStyle: { color: '#cbd5e1' } },
      series: [
        {
          type: 'pie',
          radius: ['35%', '65%'],
          data: data.categories.map((name, index) => ({
            name,
            value: series[0]?.values[index] ?? 0,
          })),
        },
      ],
    }
  return {
    color,
    tooltip: { trigger: 'axis', valueFormatter },
    legend: { type: 'scroll', top: 0, textStyle: { color: '#cbd5e1' } },
    grid: { left: 16, right: 16, top: 40, bottom: 20, containLabel: true },
    xAxis: {
      type: 'category',
      data: data.categories,
      axisLabel: { color: '#cbd5e1' },
    },
    yAxis: {
      type: 'value',
      name: unitLabel.value,
      nameTextStyle: { color: '#cbd5e1' },
      axisLabel: { color: '#cbd5e1', formatter: valueFormatter },
    },
    series: series.map((s) => ({
      name: s.name,
      type: props.type === 'line' ? 'line' : 'bar',
      data: s.values,
      connectNulls: false,
    })),
  }
})
</script>
