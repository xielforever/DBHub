<template>
  <div class="explain-plan flex flex-col h-full min-h-0">
    <div class="flex items-center gap-2 px-4 py-2 border-b border-white/10 bg-white/[0.02] shrink-0">
      <span class="text-xs text-white/60">执行计划可视化</span>
      <span class="text-[11px] text-white/30">{{ columns.length }} 列 · {{ rows.length }} 行</span>
      <div class="flex-1" />
      <el-select v-model="viewMode" size="small" class="w-24" popper-class="glass-popper">
        <el-option label="可视化" value="visual" />
        <el-option label="表格" value="table" />
        <el-option label="原始" value="raw" />
      </el-select>
    </div>

    <!-- 可视化模式 -->
    <div v-if="viewMode === 'visual'" class="flex-1 overflow-auto p-4 space-y-3">
      <!-- PG 风格 -->
      <template v-if="isPgPlan">
        <div v-for="(node, idx) in pgNodes" :key="idx" class="relative">
          <div class="flex items-center gap-2">
            <div class="w-6 h-6 rounded-full bg-indigo-500/20 border border-indigo-400/30 flex items-center justify-center text-[10px] text-indigo-300">{{ idx + 1 }}</div>
            <div class="flex-1 rounded-xl bg-white/5 border border-white/10 p-3">
              <div class="flex items-center gap-2">
                <span class="px-2 py-0.5 rounded-full text-[10px]" :class="nodeTypeBadge(node.type)">{{ node.type }}</span>
                <span class="text-xs font-mono text-white/80">{{ node.relation || node.detail }}</span>
                <span v-if="node.cost" class="text-[11px] text-white/40">cost {{ node.cost }}</span>
              </div>
              <div class="mt-1.5 flex flex-wrap gap-2 text-[11px]">
                <span v-if="node.rows" class="px-1.5 py-0.5 rounded bg-white/10 text-white/50">rows {{ node.rows }}</span>
                <span v-if="node.width" class="px-1.5 py-0.5 rounded bg-white/10 text-white/50">width {{ node.width }}</span>
                <span v-if="node.filter" class="px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-300">filter: {{ node.filter }}</span>
              </div>
              <div v-if="node.raw" class="mt-2 text-[11px] font-mono text-white/35 whitespace-pre-wrap">{{ node.raw }}</div>
            </div>
          </div>
          <div v-if="idx < pgNodes.length - 1" class="ml-3 w-px h-3 bg-white/10" />
        </div>
        <div class="mt-4 p-3 rounded-xl bg-emerald-500/10 border border-emerald-400/20 text-[11px] text-emerald-200/70">
          <p class="font-medium">💡 优化建议</p>
          <ul class="list-disc pl-4 mt-1 space-y-1">
            <li v-for="tip in pgTips" :key="tip">{{ tip }}</li>
          </ul>
        </div>
      </template>

      <!-- MySQL 风格 -->
      <template v-else-if="isMySQLPlan">
        <div class="grid gap-2">
          <div v-for="(row, idx) in mysqlRows" :key="idx" class="rounded-xl bg-white/5 border border-white/10 p-3 flex flex-col gap-2">
            <div class="flex items-center gap-2">
              <span class="text-[11px] text-white/40">#{{ row.id }}</span>
              <span class="px-2 py-0.5 rounded-full text-[10px]" :class="mysqlTypeBadge(row.type)">{{ row.type || 'ALL' }}</span>
              <span class="text-xs font-mono text-white/80">{{ row.table }}</span>
              <span class="text-[11px] text-white/40">{{ row.select_type }}</span>
            </div>
            <div class="flex flex-wrap gap-1.5 text-[11px]">
              <span v-if="row.key" class="px-1.5 py-0.5 rounded bg-indigo-500/15 text-indigo-300">key: {{ row.key }}</span>
              <span v-if="row.possible_keys" class="px-1.5 py-0.5 rounded bg-white/10 text-white/50">possible: {{ row.possible_keys }}</span>
              <span v-if="row.rows" class="px-1.5 py-0.5 rounded bg-white/10 text-white/50">rows {{ row.rows }}</span>
              <span v-if="row.Extra" class="px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-300">{{ row.Extra }}</span>
            </div>
          </div>
        </div>
        <div class="mt-4 p-3 rounded-xl bg-emerald-500/10 border border-emerald-400/20 text-[11px] text-emerald-200/70">
          <p class="font-medium">💡 优化建议</p>
          <ul class="list-disc pl-4 mt-1 space-y-1">
            <li v-for="tip in mysqlTips" :key="tip">{{ tip }}</li>
          </ul>
        </div>
      </template>

      <template v-else>
        <div class="text-center text-xs text-white/35 py-10">无法识别的执行计划格式，已回退到表格视图</div>
      </template>
    </div>

    <!-- 表格模式 -->
    <div v-else-if="viewMode === 'table'" class="flex-1 overflow-auto">
      <table class="w-full text-xs">
        <thead class="sticky top-0 bg-[#141428]/90 backdrop-blur text-white/45">
          <tr>
            <th v-for="c in columns" :key="c" class="text-left px-3 py-2 font-medium whitespace-nowrap">{{ c }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(r, i) in rows" :key="i" class="border-b border-white/5 hover:bg-white/5">
            <td v-for="(cell, j) in (r as unknown[])" :key="j" class="px-3 py-2 font-mono text-white/70 max-w-[400px] truncate" :title="String(cell ?? '')">
              <span v-if="cell === null" class="text-white/25 italic">NULL</span>
              <template v-else>{{ cell }}</template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 原始模式 -->
    <div v-else class="flex-1 overflow-auto p-3">
      <pre class="text-xs font-mono whitespace-pre-wrap break-all bg-black/30 rounded-xl p-3 border border-white/10">{{ rawText }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

const props = defineProps<{
  columns: string[]
  rows: unknown[][]
}>()

const viewMode = ref<'visual' | 'table' | 'raw'>('visual')

const isPgPlan = computed(() => {
  return props.columns.length === 1 && props.columns[0]?.toUpperCase().includes('QUERY PLAN')
})
const isMySQLPlan = computed(() => {
  const cols = props.columns.map(c => c.toLowerCase())
  return cols.includes('select_type') || cols.includes('type') || cols.includes('table')
})

interface PgNode {
  type: string
  relation: string
  detail: string
  cost: string
  rows: string
  width: string
  filter: string
  raw: string
}
const pgNodes = computed<PgNode[]>(() => {
  if (!isPgPlan.value) return []
  const nodes: PgNode[] = []
  for (const r of props.rows) {
    const line = String((r as any)[0] || '')
    if (!line.trim()) continue
    if (line.includes('Planning Time') || line.includes('Execution Time')) continue
    // 简单解析：Seq Scan on orders  (cost=0.00..120.30 rows=100 width=64)
    const typeMatch = line.match(/^\s*([A-Za-z ]+?)\s+on\s+(\w+)/) || line.match(/^\s*([A-Za-z]+)/)
    const costMatch = line.match(/cost=([^\s]+)/)
    const rowsMatch = line.match(/rows=([^\s]+)/)
    const widthMatch = line.match(/width=([^\s\)]+)/)
    const filterMatch = line.match(/Filter:\s*(.+)/)
    nodes.push({
      type: typeMatch ? (typeMatch[1] || 'Unknown').trim() : 'Unknown',
      relation: line.match(/on\s+(\w+)/)?.[1] || '',
      detail: line.trim(),
      cost: costMatch?.[1] || '',
      rows: rowsMatch?.[1] || '',
      width: widthMatch?.[1] || '',
      filter: filterMatch?.[1] || '',
      raw: line,
    })
  }
  return nodes
})
const pgTips = computed(() => {
  const tips: string[] = []
  const text = props.rows.map(r => String((r as any)[0])).join(' ').toLowerCase()
  if (text.includes('seq scan')) tips.push('检测到全表扫描，建议为过滤列添加索引')
  if (text.includes('sort')) tips.push('存在 Sort 操作，考虑添加 ORDER BY 列的索引或减少排序数据量')
  if (text.includes('filter')) tips.push('Filter 在扫描后过滤，尝试将条件推入索引扫描')
  if (text.includes('nested loop')) tips.push('Nested Loop 适合小表，关注内外表行数预估是否准确')
  if (!tips.length) tips.push('执行计划看起来正常，关注实际 Execution Time 是否符合预期')
  return tips
})

interface MySQLRow {
  id: any
  select_type: any
  table: any
  type: any
  possible_keys: any
  key: any
  rows: any
  Extra: any
}
const mysqlRows = computed<MySQLRow[]>(() => {
  if (!isMySQLPlan.value) return []
  const colMap: Record<string, number> = {}
  props.columns.forEach((c, i) => { colMap[c.toLowerCase()] = i })
  return props.rows.map(r => {
    const arr = r as any[]
    const get = (k: string) => {
      const idx = colMap[k]
      return idx !== undefined ? arr[idx] : ''
    }
    return {
      id: get('id') ?? '',
      select_type: get('select_type') ?? '',
      table: get('table') ?? '',
      type: get('type') ?? '',
      possible_keys: get('possible_keys') ?? '',
      key: get('key') ?? '',
      rows: get('rows') ?? '',
      Extra: get('extra') ?? '',
    }
  })
})
const mysqlTips = computed(() => {
  const tips: string[] = []
  for (const r of mysqlRows.value) {
    const t = String(r.type || '').toUpperCase()
    if (t === 'ALL') tips.push(`表 ${r.table} 全表扫描，考虑添加索引`)
    if (String(r.Extra || '').includes('Using filesort')) tips.push(`表 ${r.table} 使用文件排序，优化 ORDER BY`)
    if (String(r.Extra || '').includes('Using temporary')) tips.push(`表 ${r.table} 使用临时表，优化 GROUP BY/DISTINCT`)
    if (!r.key && r.possible_keys) tips.push(`表 ${r.table} 有可用索引 ${r.possible_keys} 但未使用，检查查询条件`)
  }
  if (!tips.length) tips.push('执行计划无明显问题，关注 rows 预估与实际是否一致')
  return [...new Set(tips)]
})

const rawText = computed(() => {
  return props.rows.map(r => (r as any[]).join(' | ')).join('\n')
})

function nodeTypeBadge(t: string) {
  const lower = t.toLowerCase()
  if (lower.includes('seq scan')) return 'bg-rose-500/15 text-rose-300'
  if (lower.includes('index scan') || lower.includes('index only')) return 'bg-emerald-500/15 text-emerald-300'
  if (lower.includes('bitmap')) return 'bg-indigo-500/15 text-indigo-300'
  if (lower.includes('sort')) return 'bg-amber-500/15 text-amber-300'
  if (lower.includes('hash') || lower.includes('merge') || lower.includes('nested')) return 'bg-violet-500/15 text-violet-300'
  return 'bg-white/10 text-white/50'
}
function mysqlTypeBadge(t: string) {
  const up = (t || '').toUpperCase()
  if (up === 'ALL') return 'bg-rose-500/15 text-rose-300'
  if (up === 'INDEX') return 'bg-amber-500/15 text-amber-300'
  if (up === 'RANGE' || up === 'REF') return 'bg-emerald-500/15 text-emerald-300'
  if (up === 'CONST' || up === 'EQ_REF') return 'bg-indigo-500/15 text-indigo-300'
  return 'bg-white/10 text-white/50'
}
</script>
