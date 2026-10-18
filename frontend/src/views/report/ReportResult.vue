<template>
  <section class="space-y-3 min-w-0" aria-label="报表查询结果">
    <p class="text-xs text-white/55" role="status">返回 {{ result.rows?.length ?? 0 }} 行 · {{ result.duration_ms }} 毫秒<span v-if="!result.truncated"> · 本次结果完整</span></p>
    <p v-if="result.truncated" class="rounded-lg bg-amber-400/10 p-3 text-sm text-amber-200" role="alert">结果已截断，仅展示已返回的部分数据。请在 SQL 中完成聚合或缩小查询范围后重新运行。</p>
    <p v-if="chart.error" class="text-sm text-amber-200" role="alert">{{ chart.error }}</p>
    <div v-if="type === 'table' || chart.error" class="h-[360px] flex min-w-0 border border-white/10 rounded-lg overflow-hidden">
      <ResultGrid :columns="result.columns ?? []" :rows="result.rows ?? []" />
    </div>
    <p v-else-if="!result.rows?.length" class="text-sm text-white/60 py-12 text-center" role="status">查询成功，未返回数据</p>
    <div v-else-if="type === 'metric'" class="py-10 text-center space-y-2">
      <p class="text-sm text-white/55">{{ config.metrics?.[0] }}</p>
      <p class="text-4xl font-semibold break-all" data-testid="report-metric">{{ metric }}</p>
    </div>
    <EChart v-else :option="option" height="340px" :aria-label="`${title}，${result.rows?.length ?? 0} 行数据；下方可展开查询结果`" />
    <details v-if="type !== 'table' && !chart.error && result.rows?.length" class="text-xs text-white/60">
      <summary class="cursor-pointer py-2 focus-visible:outline focus-visible:outline-indigo-300">查看查询结果</summary>
      <div class="h-[300px] flex min-w-0"><ResultGrid :columns="result.columns ?? []" :rows="result.rows ?? []" /></div>
    </details>
  </section>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import EChart from '../../components/base/EChart.vue'
import ResultGrid from '../query/components/ResultGrid.vue'
import { reportChart } from './reportChart'
import type { ChartConfig, ChartKind } from '../../components/business/chart/ChartCard.vue'
import type { ExecResult } from '../../api/workbench'
import type { EChartsCoreOption } from 'echarts/core'
const props = defineProps<{ type: ChartKind; config: ChartConfig; result: ExecResult; title: string }>()
const chart = computed(() => reportChart(props.type, props.config, props.result))
const metric = computed(() => {
  const index = (props.result.columns ?? []).indexOf(props.config.metrics?.[0] ?? '')
  const value = props.result.rows?.[0]?.[index]
  return value === null || value === undefined ? 'NULL' : String(value)
})
const option = computed<EChartsCoreOption>(() => {
  const data = chart.value
  const color = ['#a78bfa', '#60a5fa', '#34d399', '#fbbf24', '#f472b6', '#67e8f9']
  if (props.type === 'pie') return { color, tooltip: { trigger: 'item' }, legend: { bottom: 0, textStyle: { color: '#cbd5e1' } }, series: [{ type: 'pie', radius: ['35%', '65%'], data: data.categories.map((name, index) => ({ name, value: data.series[0]?.values[index] ?? 0 })) }] }
  return { color, tooltip: { trigger: 'axis' }, legend: { top: 0, textStyle: { color: '#cbd5e1' } }, grid: { left: 20, right: 16, top: 38, bottom: 20, containLabel: true }, xAxis: { type: 'category', data: data.categories, axisLabel: { color: '#cbd5e1' } }, yAxis: { type: 'value', axisLabel: { color: '#cbd5e1' } }, series: data.series.map(series => ({ name: series.name, type: props.type === 'line' ? 'line' : 'bar', data: series.values, connectNulls: false })) }
})
</script>
