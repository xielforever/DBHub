<template>
  <div class="chart-card">
    <!-- 表格模式 -->
    <div v-if="chartType === 'table'" class="overflow-auto max-h-[400px]">
      <table class="w-full text-sm">
        <thead class="sticky top-0 bg-white/5 backdrop-blur">
          <tr class="text-left text-white/45 text-xs">
            <th v-for="c in columns" :key="c" class="px-3 py-2 font-medium whitespace-nowrap">{{ c }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, i) in displayRows" :key="i" class="border-b border-white/5 last:border-0 hover:bg-white/5">
            <td v-for="(cell, j) in row" :key="j" class="px-3 py-2 text-white/70 max-w-[240px] truncate" :title="String(cell ?? '')">
              <span v-if="cell === null || cell === undefined" class="text-white/25 italic">NULL</span>
              <template v-else>{{ cell }}</template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 指标卡 -->
    <div v-else-if="chartType === 'metric'" class="flex flex-col items-center justify-center py-8 gap-2">
      <p class="text-xs text-white/40">{{ metricLabel }}</p>
      <p class="text-4xl font-bold tabular-nums bg-gradient-to-r from-indigo-300 to-violet-300 bg-clip-text text-transparent">
        {{ metricValue }}
      </p>
      <p v-if="metricSub" class="text-[11px] text-white/30">{{ metricSub }}</p>
    </div>

    <!-- ECharts -->
    <EChart v-else :option="echartOption" :height="height" :aria-label="`${chartType} 图表`" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import EChart from '../../base/EChart.vue'
import type { EChartsCoreOption } from 'echarts/core'

export type ChartKind = 'table' | 'bar' | 'line' | 'pie' | 'metric'

export interface ChartConfig {
  dimension?: string // 维度列名
  metrics?: string[] // 指标列名
  aggregation?: 'sum' | 'count' | 'avg' | 'min' | 'max'
  sort?: 'asc' | 'desc'
  topN?: number
}

const props = withDefaults(defineProps<{
  chartType: ChartKind
  columns: string[]
  rows: unknown[][]
  config?: ChartConfig
  height?: string
}>(), {
  config: () => ({}),
  height: '340px',
})

function toNumber(v: unknown): number {
  if (typeof v === 'number') return v
  if (typeof v === 'string') {
    const n = Number(v)
    return isNaN(n) ? 0 : n
  }
  return 0
}

function agg(values: number[], method: ChartConfig['aggregation'] = 'sum'): number {
  if (!values.length) return 0
  switch (method) {
    case 'count': return values.length
    case 'avg': return values.reduce((a,b)=>a+b,0)/values.length
    case 'min': return Math.min(...values)
    case 'max': return Math.max(...values)
    case 'sum':
    default: return values.reduce((a,b)=>a+b,0)
  }
}

const colIndexMap = computed(() => {
  const m: Record<string, number> = {}
  props.columns.forEach((c, i) => m[c] = i)
  return m
})

interface Grouped {
  key: string
  values: Record<string, number[]> // metric -> values
  count: number
}

const groupedData = computed<Grouped[]>(() => {
  const dim = props.config?.dimension
  const metrics = props.config?.metrics ?? []
  if (!dim || !metrics.length) return []
  const dimIdx = colIndexMap.value[dim]
  if (dimIdx === undefined) return []
  const metricIdxs = metrics.map(m => colIndexMap.value[m]).filter(i => i !== undefined) as number[]
  if (!metricIdxs.length) return []

  const map = new Map<string, Grouped>()
  for (const row of props.rows) {
    const key = String(row[dimIdx] ?? '未知')
    if (!map.has(key)) map.set(key, { key, values: {}, count: 0 })
    const g = map.get(key)!
    g.count++
    metrics.forEach((mName) => {
      const idx = colIndexMap.value[mName]
      if (idx === undefined) return
      if (!g.values[mName]) g.values[mName] = []
      g.values[mName].push(toNumber(row[idx]))
    })
  }
  let arr = Array.from(map.values())
  // 排序按第一个指标聚合值
  const firstMetric = metrics[0]!
  const aggMethod = props.config?.aggregation ?? 'sum'
  arr.sort((a,b) => {
    const av = agg(a.values[firstMetric] ?? [], aggMethod)
    const bv = agg(b.values[firstMetric] ?? [], aggMethod)
    return props.config?.sort === 'asc' ? av - bv : bv - av
  })
  if (props.config?.topN && props.config.topN > 0) {
    arr = arr.slice(0, props.config.topN)
  }
  return arr
})

const displayRows = computed(() => {
  // 表格模式也支持 topN / sort 简单处理
  if (props.chartType !== 'table') return props.rows
  if (!props.config?.topN) return props.rows.slice(0, 100)
  return props.rows.slice(0, props.config.topN)
})

const metricLabel = computed(() => {
  const m = props.config?.metrics?.[0] ?? props.columns[1] ?? props.columns[0] ?? '指标'
  return `${m} (${props.config?.aggregation ?? 'sum'})`
})
const metricValue = computed(() => {
  const mName = props.config?.metrics?.[0] ?? props.columns[1] ?? props.columns[0]
  if (!mName) return '—'
  const idx = colIndexMap.value[mName]
  if (idx === undefined) return '—'
  const nums = props.rows.map(r => toNumber(r[idx]))
  const v = agg(nums, props.config?.aggregation)
  return Number.isInteger(v) ? v.toLocaleString() : v.toFixed(2)
})
const metricSub = computed(() => {
  return `${props.rows.length} 行 · ${props.columns.length} 列`
})

const echartOption = computed<EChartsCoreOption>(() => {
  const groups = groupedData.value
  const metrics = props.config?.metrics ?? []
  const aggMethod = props.config?.aggregation ?? 'sum'

  if (props.chartType === 'bar') {
    return {
      tooltip: { trigger: 'axis' },
      legend: { data: metrics, textStyle: { color: 'rgba(255,255,255,0.6)' }, top: 0 },
      grid: { left: 48, right: 16, top: 32, bottom: 24, containLabel: true },
      xAxis: {
        type: 'category',
        data: groups.map(g => g.key),
        axisLabel: { color: 'rgba(255,255,255,0.5)', interval: 0, rotate: groups.length > 12 ? 30 : 0 },
        axisLine: { lineStyle: { color: 'rgba(255,255,255,0.1)' } },
      },
      yAxis: {
        type: 'value',
        axisLabel: { color: 'rgba(255,255,255,0.5)' },
        splitLine: { lineStyle: { color: 'rgba(255,255,255,0.06)' } },
      },
      series: metrics.map((m, idx) => ({
        name: m,
        type: 'bar',
        data: groups.map(g => agg(g.values[m] ?? [], aggMethod)),
        itemStyle: {
          color: ['#a78bfa','#60a5fa','#34d399','#fbbf24','#f472b6','#67e8f9'][idx % 6],
          borderRadius: [6,6,0,0],
        },
        emphasis: { focus: 'series' as const },
        animationDelay: (i: number) => i * 20,
      })),
      animationEasing: 'cubicOut',
    }
  }

  if (props.chartType === 'line') {
    return {
      tooltip: { trigger: 'axis' },
      legend: { data: metrics, textStyle: { color: 'rgba(255,255,255,0.6)' }, top: 0 },
      grid: { left: 48, right: 16, top: 32, bottom: 24, containLabel: true },
      xAxis: {
        type: 'category',
        data: groups.map(g => g.key),
        axisLabel: { color: 'rgba(255,255,255,0.5)' },
        axisLine: { lineStyle: { color: 'rgba(255,255,255,0.1)' } },
      },
      yAxis: {
        type: 'value',
        axisLabel: { color: 'rgba(255,255,255,0.5)' },
        splitLine: { lineStyle: { color: 'rgba(255,255,255,0.06)' } },
      },
      series: metrics.map((m, idx) => ({
        name: m,
        type: 'line',
        smooth: true,
        data: groups.map(g => agg(g.values[m] ?? [], aggMethod)),
        lineStyle: { width: 2, color: ['#a78bfa','#60a5fa','#34d399','#fbbf24','#f472b6'][idx % 5] },
        itemStyle: { color: ['#a78bfa','#60a5fa','#34d399','#fbbf24','#f472b6'][idx % 5] },
        areaStyle: { opacity: 0.12, color: ['#a78bfa','#60a5fa','#34d399','#fbbf24','#f472b6'][idx % 5] },
        animationDelay: (i: number) => i * 20,
      })),
    }
  }

  if (props.chartType === 'pie') {
    const m = metrics[0]
    const data = groups.map(g => ({
      name: g.key,
      value: m ? agg(g.values[m] ?? [], aggMethod) : g.count,
    }))
    return {
      tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
      legend: {
        orient: 'vertical',
        right: 12,
        top: 'center',
        textStyle: { color: 'rgba(255,255,255,0.6)', fontSize: 11 },
      },
      series: [{
        type: 'pie',
        radius: ['42%','72%'],
        center: ['38%','50%'],
        itemStyle: { borderRadius: 6, borderColor: 'rgba(0,0,0,0.3)', borderWidth: 2 },
        label: { color: 'rgba(255,255,255,0.6)' },
        data,
        emphasis: { itemStyle: { shadowBlur: 12, shadowColor: 'rgba(0,0,0,0.4)' } },
      }],
    }
  }

  return {}
})
</script>

<style scoped>
.chart-card {
  border-radius: 0.75rem;
  overflow: hidden;
}
</style>
