<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-white/[0.02]">
      <div class="w-6 h-6 rounded-lg bg-orange-500/20 border border-orange-400/30 flex items-center justify-center">
        <Flame class="w-3.5 h-3.5 text-orange-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">性能火焰图</p>
        <p class="text-[10px] text-white/40">解析 · 规划 · 执行耗时分解</p>
      </div>
      <span v-if="duration" class="px-2 py-0.5 rounded-full bg-white/10 text-white/50 text-[11px]">{{ duration }}ms</span>
      <button class="ghost-button !py-1 !px-2.5 text-xs flex items-center gap-1" @click="generateMock">
        <RefreshCw class="w-3 h-3" /> 生成
      </button>
    </div>

    <div v-if="!data" class="flex-1 flex flex-col items-center justify-center gap-3 p-6 text-center">
      <div class="w-12 h-12 rounded-2xl bg-white/5 border border-white/10 flex items-center justify-center">
        <Flame class="w-6 h-6 text-white/20" />
      </div>
      <p class="text-xs text-white/50">暂无性能数据</p>
      <p class="text-[11px] text-white/30 leading-5">执行查询后可生成火焰图<br/>展示解析、重写、规划、执行各阶段耗时</p>
      <button class="liquid-button !py-1.5 !px-3 text-xs mt-2" @click="generateMock">生成示例火焰图</button>
    </div>

    <div v-else class="flex-1 overflow-auto p-4 space-y-4">
      <!-- 总览 -->
      <div class="grid grid-cols-4 gap-2">
        <div v-for="seg in data.segments" :key="seg.name" class="rounded-xl bg-white/5 border border-white/10 p-2.5 text-center">
          <p class="text-[10px] text-white/40">{{ seg.name }}</p>
          <p class="text-sm font-mono font-medium mt-1" :style="{ color: seg.color }">{{ seg.value }}ms</p>
          <p class="text-[10px] text-white/30 mt-0.5">{{ ((seg.value / data.total) * 100).toFixed(1) }}%</p>
          <div class="mt-1.5 h-1 rounded-full bg-white/10 overflow-hidden">
            <div class="h-full" :style="{ width: ((seg.value / data.total) * 100) + '%', background: seg.color }" />
          </div>
        </div>
      </div>

      <!-- 火焰图 -->
      <div class="rounded-xl bg-black/30 border border-white/10 overflow-hidden">
        <div class="px-3 py-2 border-b border-white/10 bg-white/5 flex items-center gap-2 text-[11px] text-white/40">
          <span>火焰图 · 宽度代表耗时占比</span>
          <div class="flex-1" />
          <span class="px-1.5 py-0.5 rounded bg-white/10">总计 {{ data.total }}ms</span>
        </div>
        <div class="p-3 space-y-1">
          <div v-for="(level, li) in data.levels" :key="li" class="flex gap-1 h-7">
            <div
              v-for="(block, bi) in level"
              :key="bi"
              class="rounded-md flex items-center px-2 text-[11px] font-mono truncate cursor-pointer hover:brightness-110 transition-all"
              :style="{ width: (block.width * 100) + '%', background: block.color + '30', border: `1px solid ${block.color}50`, color: block.color }"
              :title="`${block.name}: ${block.value}ms`"
              @click="selectedBlock = block"
            >
              <span class="truncate">{{ block.name }}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- 详情 -->
      <div v-if="selectedBlock" class="rounded-xl bg-white/5 border border-white/10 p-3">
        <p class="text-xs font-medium text-white/80 flex items-center gap-2">
          <span class="w-2 h-2 rounded-full" :style="{ background: selectedBlock.color }" />
          {{ selectedBlock.name }}
        </p>
        <p class="text-[11px] text-white/50 mt-1">{{ selectedBlock.desc }}</p>
        <div class="mt-2 flex gap-2 text-[11px]">
          <span class="px-2 py-0.5 rounded bg-white/10 text-white/50">耗时 {{ selectedBlock.value }}ms</span>
          <span class="px-2 py-0.5 rounded bg-white/10 text-white/50">占比 {{ (selectedBlock.width * 100).toFixed(1) }}%</span>
        </div>
      </div>

      <!-- 建议 -->
      <div class="rounded-xl bg-amber-500/10 border border-amber-400/20 p-3">
        <p class="text-[11px] font-medium text-amber-300 flex items-center gap-1"><Lightbulb class="w-3 h-3" /> 性能建议</p>
        <ul class="mt-1.5 space-y-1 text-[11px] text-amber-200/70 list-disc pl-4">
          <li v-for="tip in data.tips" :key="tip">{{ tip }}</li>
        </ul>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { Flame, Lightbulb, RefreshCw } from 'lucide-vue-next'

interface Block {
  name: string
  value: number
  width: number
  color: string
  desc: string
}
interface FlameData {
  total: number
  segments: { name: string; value: number; color: string }[]
  levels: Block[][]
  tips: string[]
}

const props = defineProps<{
  duration: number | null
  sql: string
}>()

const data = ref<FlameData | null>(null)
const selectedBlock = ref<Block | null>(null)

function generateMock() {
  const total = props.duration || 120 + Math.floor(Math.random() * 80)
  const parse = Math.floor(total * 0.05)
  const rewrite = Math.floor(total * 0.08)
  const plan = Math.floor(total * 0.15)
  const exec = total - parse - rewrite - plan

  const segments = [
    { name: '解析', value: parse, color: '#A78BFA' },
    { name: '重写', value: rewrite, color: '#60A5FA' },
    { name: '规划', value: plan, color: '#FBBF24' },
    { name: '执行', value: exec, color: '#34D399' },
  ]

  const levels: Block[][] = [
    [
      { name: '总计', value: total, width: 1, color: '#A78BFA', desc: '查询总耗时' },
    ],
    [
      { name: '解析', value: parse, width: parse / total, color: '#A78BFA', desc: 'SQL 解析与语法检查' },
      { name: '重写', value: rewrite, width: rewrite / total, color: '#60A5FA', desc: '查询重写与优化' },
      { name: '规划', value: plan, width: plan / total, color: '#FBBF24', desc: '生成执行计划' },
      { name: '执行', value: exec, width: exec / total, color: '#34D399', desc: '实际数据扫描与计算' },
    ],
    [
      { name: '词法', value: Math.floor(parse * 0.4), width: (parse * 0.4) / total, color: '#A78BFA', desc: '词法分析' },
      { name: '语法', value: Math.floor(parse * 0.6), width: (parse * 0.6) / total, color: '#C4B5FD', desc: '语法分析' },
      { name: '常量折叠', value: Math.floor(rewrite * 0.5), width: (rewrite * 0.5) / total, color: '#60A5FA', desc: '常量表达式优化' },
      { name: '谓词下推', value: Math.floor(rewrite * 0.5), width: (rewrite * 0.5) / total, color: '#93C5FD', desc: 'WHERE 条件下推' },
      { name: '索引选择', value: Math.floor(plan * 0.6), width: (plan * 0.6) / total, color: '#FBBF24', desc: '选择最优索引' },
      { name: 'JOIN 排序', value: Math.floor(plan * 0.4), width: (plan * 0.4) / total, color: '#FCD34D', desc: 'JOIN 顺序优化' },
      { name: 'Seq Scan', value: Math.floor(exec * 0.6), width: (exec * 0.6) / total, color: '#34D399', desc: '全表扫描或索引扫描' },
      { name: 'Sort', value: Math.floor(exec * 0.25), width: (exec * 0.25) / total, color: '#6EE7B7', desc: '排序操作' },
      { name: 'Aggregate', value: Math.floor(exec * 0.15), width: (exec * 0.15) / total, color: '#A7F3D0', desc: '聚合计算' },
    ],
  ]

  const tips: string[] = []
  if (exec / total > 0.7) tips.push('执行阶段占比过高，关注是否全表扫描，考虑添加索引')
  if (plan / total > 0.2) tips.push('规划阶段耗时较长，表统计信息可能过期，执行 ANALYZE 更新')
  if (parse > 10) tips.push('解析耗时偏高，SQL 可能过于复杂，尝试拆分或简化')
  if (!tips.length) tips.push('各阶段耗时分布正常，查询性能良好')

  data.value = { total, segments, levels, tips }
}

// auto generate if duration exists
if (props.duration) generateMock()
</script>
