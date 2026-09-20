<template>
  <div class="column-stats flex flex-col h-full min-h-0">
    <div class="flex items-center gap-2 px-4 py-2 border-b border-white/10 bg-white/[0.02] shrink-0">
      <span class="text-xs text-white/60">列统计与分布</span>
      <span class="text-[11px] text-white/30">{{ columns.length }} 列 · {{ rows.length }} 行</span>
      <div class="flex-1" />
      <button class="ghost-button !py-1 !px-2 text-[11px]" @click="refresh">刷新</button>
    </div>

    <div v-if="!columns.length" class="flex-1 flex items-center justify-center text-sm text-white/35">
      执行 SQL 后可在此查看列统计
    </div>

    <div v-else class="flex-1 overflow-auto p-3 space-y-3">
      <!-- 概览 -->
      <div class="grid grid-cols-2 sm:grid-cols-4 gap-2">
        <div class="rounded-xl bg-white/5 border border-white/10 p-2.5">
          <p class="text-[10px] text-white/40">总行数</p>
          <p class="text-sm font-semibold mt-1">{{ rows.length }}</p>
        </div>
        <div class="rounded-xl bg-white/5 border border-white/10 p-2.5">
          <p class="text-[10px] text-white/40">NULL 总数</p>
          <p class="text-sm font-semibold mt-1">{{ totalNulls }}</p>
        </div>
        <div class="rounded-xl bg-white/5 border border-white/10 p-2.5">
          <p class="text-[10px] text-white/40">数值列</p>
          <p class="text-sm font-semibold mt-1">{{ numericColumns.length }}</p>
        </div>
        <div class="rounded-xl bg-white/5 border border-white/10 p-2.5">
          <p class="text-[10px] text-white/40">文本列</p>
          <p class="text-sm font-semibold mt-1">{{ columns.length - numericColumns.length }}</p>
        </div>
      </div>

      <!-- 每列卡片 -->
      <div v-for="stat in columnStats" :key="stat.name" class="rounded-xl bg-black/30 border border-white/10 overflow-hidden">
        <div class="flex items-center justify-between px-3 py-2 border-b border-white/10 bg-white/5">
          <div class="flex items-center gap-2">
            <span class="text-xs font-mono font-medium text-white/80">{{ stat.name }}</span>
            <span class="px-1.5 py-0.5 rounded-full text-[10px]" :class="stat.isNumeric ? 'bg-emerald-400/15 text-emerald-300' : 'bg-indigo-400/15 text-indigo-300'">{{ stat.isNumeric ? '数值' : '文本' }}</span>
            <span v-if="stat.nullCount" class="px-1.5 py-0.5 rounded-full text-[10px] bg-amber-400/15 text-amber-300">{{ stat.nullCount }} NULL</span>
          </div>
          <span class="text-[11px] text-white/30">去重 {{ stat.distinctCount }}</span>
        </div>

        <div class="p-3 grid grid-cols-2 sm:grid-cols-4 gap-3 text-[11px]">
          <template v-if="stat.isNumeric">
            <div><span class="text-white/40">最小</span><p class="font-mono text-white/70 mt-0.5">{{ stat.min }}</p></div>
            <div><span class="text-white/40">最大</span><p class="font-mono text-white/70 mt-0.5">{{ stat.max }}</p></div>
            <div><span class="text-white/40">均值</span><p class="font-mono text-white/70 mt-0.5">{{ stat.avg?.toFixed(2) }}</p></div>
            <div><span class="text-white/40">求和</span><p class="font-mono text-white/70 mt-0.5">{{ stat.sum?.toLocaleString() }}</p></div>
            <div><span class="text-white/40">中位数</span><p class="font-mono text-white/70 mt-0.5">{{ stat.median }}</p></div>
            <div><span class="text-white/40">标准差</span><p class="font-mono text-white/70 mt-0.5">{{ stat.stddev?.toFixed(2) }}</p></div>
          </template>
          <template v-else>
            <div><span class="text-white/40">最短长度</span><p class="font-mono text-white/70 mt-0.5">{{ stat.minLen }}</p></div>
            <div><span class="text-white/40">最长长度</span><p class="font-mono text-white/70 mt-0.5">{{ stat.maxLen }}</p></div>
            <div><span class="text-white/40">平均长度</span><p class="font-mono text-white/70 mt-0.5">{{ stat.avgLen?.toFixed(1) }}</p></div>
            <div><span class="text-white/40">空字符串</span><p class="font-mono text-white/70 mt-0.5">{{ stat.emptyCount }}</p></div>
          </template>
        </div>

        <!-- 直方图 -->
        <div v-if="stat.histogram.length" class="px-3 pb-3">
          <p class="text-[10px] text-white/40 mb-1.5">分布直方图 · Top {{ stat.histogram.length }}</p>
          <div class="space-y-1">
            <div v-for="bin in stat.histogram" :key="String(bin.value)" class="flex items-center gap-2">
              <span class="w-20 truncate text-[11px] font-mono text-white/50 text-right" :title="String(bin.value)">{{ bin.label }}</span>
              <div class="flex-1 h-2 rounded-full bg-white/10 overflow-hidden">
                <div class="h-full rounded-full transition-all" :class="stat.isNumeric ? 'bg-emerald-400/70' : 'bg-indigo-400/70'" :style="{ width: (bin.count / (stat.maxBin || 1) * 100) + '%' }" />
              </div>
              <span class="w-10 text-[10px] text-white/40 text-right">{{ bin.count }}</span>
              <span class="w-8 text-[10px] text-white/25 text-right">{{ ((bin.count / rows.length) * 100).toFixed(0) }}%</span>
            </div>
          </div>
        </div>

        <!-- Top 值 -->
        <div v-if="stat.topValues.length" class="px-3 pb-3">
          <p class="text-[10px] text-white/40 mb-1.5">高频值</p>
          <div class="flex flex-wrap gap-1">
            <span v-for="tv in stat.topValues" :key="String(tv.value)" class="px-2 py-0.5 rounded-full bg-white/10 text-[11px] font-mono text-white/60 flex items-center gap-1">
              <span class="truncate max-w-[120px]">{{ tv.value === null ? 'NULL' : String(tv.value).slice(0,30) }}</span>
              <span class="text-[10px] text-white/30">{{ tv.count }}</span>
            </span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

const props = defineProps<{
  columns: string[]
  rows: unknown[][]
}>()

const refreshKey = ref(0)
function refresh() { refreshKey.value++ }

const numericColumns = computed(() => {
  if (!props.rows.length) return []
  const sample = props.rows.slice(0, 30)
  return props.columns.filter((_, idx) => {
    let numCount = 0
    let total = 0
    for (const r of sample) {
      const v = (r as any[])[idx]
      if (v === null || v === undefined || v === '') continue
      total++
      if (!isNaN(Number(v))) numCount++
    }
    return total > 0 && numCount / total > 0.8
  })
})

const totalNulls = computed(() => {
  let c = 0
  for (const r of props.rows) {
    for (const v of r as any[]) if (v === null || v === undefined) c++
  }
  return c
})

interface Bin { value: unknown; label: string; count: number }
interface ColStat {
  name: string
  isNumeric: boolean
  nullCount: number
  distinctCount: number
  min: any
  max: any
  sum?: number
  avg?: number
  median?: any
  stddev?: number
  minLen?: number
  maxLen?: number
  avgLen?: number
  emptyCount?: number
  histogram: Bin[]
  maxBin: number
  topValues: { value: unknown; count: number }[]
}

const columnStats = computed<ColStat[]>(() => {
  void refreshKey.value
  const stats: ColStat[] = []
  const rowCount = props.rows.length
  if (!rowCount) return stats

  props.columns.forEach((col, colIdx) => {
    const values = props.rows.map(r => (r as any[])[colIdx])
    const nonNull = values.filter(v => v !== null && v !== undefined)
    const nullCount = rowCount - nonNull.length
    const distinct = new Set(nonNull.map(v => String(v))).size
    const isNumeric = numericColumns.value.includes(col)

    const freqMap = new Map<string, { value: unknown; count: number }>()
    for (const v of nonNull) {
      const k = String(v)
      if (!freqMap.has(k)) freqMap.set(k, { value: v, count: 0 })
      freqMap.get(k)!.count++
    }
    const topValues = Array.from(freqMap.values()).sort((a, b) => b.count - a.count).slice(0, 8)

    if (isNumeric) {
      const nums = nonNull.map(v => Number(v)).filter(n => !isNaN(n)).sort((a, b) => a - b)
      if (!nums.length) {
        stats.push({ name: col, isNumeric: true, nullCount, distinctCount: distinct, min: '-', max: '-', histogram: [], maxBin: 0, topValues })
        return
      }
      const min = nums[0] as number
      const max = nums[nums.length - 1] as number
      const sum = nums.reduce((a, b) => a + b, 0)
      const avg = sum / nums.length
      const median = nums[Math.floor(nums.length / 2)] as number
      const variance = nums.reduce((a, b) => a + Math.pow(b - avg, 2), 0) / nums.length
      const stddev = Math.sqrt(variance)

      // 直方图分桶 10 桶
      const binCount = Math.min(10, nums.length)
      const binSize = (max - min) / binCount || 1
      const bins: Bin[] = []
      for (let i = 0; i < binCount; i++) {
        const start = min + i * binSize
        const end = start + binSize
        const count = nums.filter(n => n >= start && (i === binCount - 1 ? n <= end : n < end)).length
        if (count > 0) bins.push({ value: `${start.toFixed(1)}-${end.toFixed(1)}`, label: `${start.toFixed(0)}~${end.toFixed(0)}`, count })
      }
      // 如果数值离散度高，用 Top 值作为直方图
      const histogram = bins.length >= 3 ? bins : topValues.map(tv => ({ value: tv.value, label: String(tv.value).slice(0, 12), count: tv.count }))
      const maxBin = Math.max(...histogram.map(b => b.count), 1)

      stats.push({ name: col, isNumeric: true, nullCount, distinctCount: distinct, min, max, sum, avg, median, stddev, histogram, maxBin, topValues })
    } else {
      const strs = nonNull.map(v => String(v))
      const lens = strs.map(s => s.length)
      const minLen = lens.length ? Math.min(...lens) : 0
      const maxLen = lens.length ? Math.max(...lens) : 0
      const avgLen = lens.length ? lens.reduce((a, b) => a + b, 0) / lens.length : 0
      const emptyCount = strs.filter(s => s === '').length

      const histogram = topValues.map(tv => ({ value: tv.value, label: String(tv.value).slice(0, 12) || '(空)', count: tv.count }))
      const maxBin = Math.max(...histogram.map(b => b.count), 1)

      stats.push({ name: col, isNumeric: false, nullCount, distinctCount: distinct, min: minLen, max: maxLen, minLen, maxLen, avgLen, emptyCount, histogram, maxBin, topValues })
    }
  })

  return stats
})
</script>
