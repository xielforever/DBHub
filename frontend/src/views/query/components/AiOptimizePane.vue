<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-white/[0.02]">
      <div class="w-6 h-6 rounded-lg bg-emerald-500/20 border border-emerald-400/30 flex items-center justify-center">
        <Sparkles class="w-3.5 h-3.5 text-emerald-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">AI 优化建议</p>
        <p class="text-[10px] text-white/40">索引推荐 · 重写 · 成本分析</p>
      </div>
      <button class="ghost-button !py-1 !px-2.5 text-xs flex items-center gap-1" :disabled="!sql || loading" @click="analyze">
        <RefreshCw class="w-3 h-3" :class="{ 'animate-spin': loading }" /> {{ loading ? '分析中' : 'AI 分析' }}
      </button>
    </div>

    <div v-if="!sql" class="flex-1 flex flex-col items-center justify-center gap-3 p-6 text-center">
      <div class="w-12 h-12 rounded-2xl bg-white/5 border border-white/10 flex items-center justify-center">
        <FileSearch class="w-6 h-6 text-white/20" />
      </div>
      <p class="text-xs text-white/50">暂无 SQL</p>
      <p class="text-[11px] text-white/30">在编辑器中编写 SQL 后可进行 AI 优化分析</p>
    </div>

    <div v-else-if="!result" class="flex-1 flex flex-col items-center justify-center gap-3 p-6 text-center">
      <div class="w-12 h-12 rounded-2xl bg-emerald-500/10 border border-emerald-400/20 flex items-center justify-center">
        <Sparkles class="w-6 h-6 text-emerald-300/50" />
      </div>
      <p class="text-xs text-white/60">点击「AI 分析」获取优化建议</p>
      <p class="text-[11px] text-white/30 leading-5">基于 EXPLAIN 计划、表结构、历史慢查询<br/>给出索引、重写、配置建议</p>
      <div class="mt-2 w-full max-w-[320px] space-y-2">
        <div class="p-2.5 rounded-xl bg-white/5 border border-white/10 text-left">
          <p class="text-[11px] text-white/40 mb-1">可检测问题</p>
          <div class="flex flex-wrap gap-1">
            <span class="px-2 py-0.5 rounded-full bg-rose-500/15 text-rose-300 text-[10px]">全表扫描</span>
            <span class="px-2 py-0.5 rounded-full bg-amber-500/15 text-amber-300 text-[10px]">文件排序</span>
            <span class="px-2 py-0.5 rounded-full bg-violet-500/15 text-violet-300 text-[10px]">SELECT *</span>
            <span class="px-2 py-0.5 rounded-full bg-indigo-500/15 text-indigo-300 text-[10px]">无 LIMIT</span>
          </div>
        </div>
      </div>
    </div>

    <div v-else class="flex-1 overflow-auto p-3 space-y-3">
      <!-- 评分 -->
      <div class="rounded-xl bg-gradient-to-br from-emerald-500/10 to-indigo-500/10 border border-emerald-400/20 p-3 flex items-center gap-3">
        <div class="w-12 h-12 rounded-full border-2 flex items-center justify-center text-sm font-bold" :class="scoreColor">
          {{ result.score }}
        </div>
        <div class="flex-1 min-w-0">
          <p class="text-xs font-medium text-white/80">查询健康度评分</p>
          <p class="text-[11px] text-white/50 mt-0.5">{{ result.summary }}</p>
          <div class="mt-1.5 flex gap-1.5">
            <span v-for="tag in result.tags" :key="tag" class="px-1.5 py-0.5 rounded-full text-[10px]" :class="tagBadge(tag)">{{ tag }}</span>
          </div>
        </div>
      </div>

      <!-- 问题 -->
      <div v-if="result.issues.length" class="space-y-2">
        <p class="text-[11px] font-medium text-white/60 flex items-center gap-1"><AlertTriangle class="w-3 h-3 text-amber-400" /> 检测到 {{ result.issues.length }} 个问题</p>
        <div v-for="(issue, i) in result.issues" :key="i" class="rounded-xl bg-white/5 border border-white/10 p-2.5">
          <div class="flex items-start gap-2">
            <span class="px-1.5 py-0.5 rounded text-[10px] mt-0.5" :class="severityBadge(issue.severity)">{{ issue.severity }}</span>
            <div class="flex-1 min-w-0">
              <p class="text-xs text-white/80">{{ issue.title }}</p>
              <p class="text-[11px] text-white/45 mt-1 leading-5">{{ issue.description }}</p>
              <p v-if="issue.location" class="text-[10px] font-mono text-white/30 mt-1">{{ issue.location }}</p>
            </div>
          </div>
        </div>
      </div>

      <!-- 索引推荐 -->
      <div v-if="result.indexes.length" class="space-y-2">
        <p class="text-[11px] font-medium text-white/60 flex items-center gap-1"><Database class="w-3 h-3 text-emerald-400" /> 索引推荐 · {{ result.indexes.length }}</p>
        <div v-for="(idx, i) in result.indexes" :key="i" class="rounded-xl bg-emerald-500/5 border border-emerald-400/20 p-2.5">
          <div class="flex items-center gap-2">
            <span class="text-xs font-mono text-emerald-300">{{ idx.table }}</span>
            <span class="text-[10px] px-1.5 py-0.5 rounded bg-emerald-500/20 text-emerald-300">{{ idx.type }}</span>
            <span class="text-[10px] text-white/30">预估提升 {{ idx.benefit }}</span>
          </div>
          <pre class="mt-2 text-[11px] font-mono text-white/70 bg-black/30 rounded-lg p-2 overflow-auto">{{ idx.ddl }}</pre>
          <div class="mt-2 flex gap-1">
            <button class="ghost-button !py-1 !px-2 text-[10px]" @click="copyText(idx.ddl)">复制 DDL</button>
            <button class="ghost-button !py-1 !px-2 text-[10px] text-emerald-300" @click="emit('apply-index', idx)">应用建议</button>
          </div>
        </div>
      </div>

      <!-- 重写建议 -->
      <div v-if="result.rewrites.length" class="space-y-2">
        <p class="text-[11px] font-medium text-white/60 flex items-center gap-1"><Wand2 class="w-3 h-3 text-violet-400" /> 查询重写 · {{ result.rewrites.length }}</p>
        <div v-for="(rw, i) in result.rewrites" :key="i" class="rounded-xl bg-violet-500/5 border border-violet-400/20 p-2.5">
          <p class="text-xs text-violet-200">{{ rw.title }}</p>
          <p class="text-[11px] text-white/45 mt-1">{{ rw.reason }}</p>
          <div class="mt-2 grid grid-cols-2 gap-2">
            <div>
              <p class="text-[10px] text-white/30 mb-1">重写前</p>
              <pre class="text-[11px] font-mono text-white/50 bg-black/30 rounded-lg p-2 overflow-auto max-h-24">{{ rw.before }}</pre>
            </div>
            <div>
              <p class="text-[10px] text-emerald-300/70 mb-1">重写后</p>
              <pre class="text-[11px] font-mono text-emerald-200/80 bg-emerald-500/10 rounded-lg p-2 overflow-auto max-h-24">{{ rw.after }}</pre>
            </div>
          </div>
          <div class="mt-2 flex gap-1">
            <button class="liquid-button !py-1 !px-2.5 text-[11px]" @click="emit('apply-rewrite', rw.after)">应用重写</button>
            <button class="ghost-button !py-1 !px-2 text-[10px]" @click="copyText(rw.after)">复制</button>
          </div>
        </div>
      </div>

      <!-- 成本分析 -->
      <div v-if="result.cost" class="rounded-xl bg-white/5 border border-white/10 p-2.5">
        <p class="text-[11px] font-medium text-white/60 flex items-center gap-1"><BarChart3 class="w-3 h-3" /> 成本分析</p>
        <div class="mt-2 grid grid-cols-3 gap-2 text-[11px]">
          <div class="rounded-lg bg-white/5 p-2 text-center">
            <p class="text-white/40">预估行数</p>
            <p class="text-white/80 font-mono mt-1">{{ result.cost.estimated_rows }}</p>
          </div>
          <div class="rounded-lg bg-white/5 p-2 text-center">
            <p class="text-white/40">预估成本</p>
            <p class="text-white/80 font-mono mt-1">{{ result.cost.estimated_cost }}</p>
          </div>
          <div class="rounded-lg bg-white/5 p-2 text-center">
            <p class="text-white/40">实际耗时</p>
            <p class="text-emerald-300 font-mono mt-1">{{ result.cost.actual_time }}ms</p>
          </div>
        </div>
      </div>

      <div class="flex gap-2">
        <button class="ghost-button !py-1.5 !px-3 text-xs flex-1" @click="result = null">重新分析</button>
        <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1" @click="exportResult"><Download class="w-3 h-3" /> 导出报告</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { ElMessage } from 'element-plus'
import { AlertTriangle, BarChart3, Database, Download, FileSearch, RefreshCw, Sparkles, Wand2 } from 'lucide-vue-next'

const props = defineProps<{
  sql: string
  explainColumns: string[]
  explainRows: unknown[][]
  duration: number | null
  tableColumns?: string[]
}>()

const emit = defineEmits<{
  (e: 'apply-rewrite', sql: string): void
  (e: 'apply-index', idx: any): void
}>()

interface Issue {
  severity: 'high' | 'medium' | 'low'
  title: string
  description: string
  location?: string
}
interface IndexRec {
  table: string
  type: string
  ddl: string
  benefit: string
}
interface Rewrite {
  title: string
  reason: string
  before: string
  after: string
}
interface OptimizeResult {
  score: number
  summary: string
  tags: string[]
  issues: Issue[]
  indexes: IndexRec[]
  rewrites: Rewrite[]
  cost: { estimated_rows: string; estimated_cost: string; actual_time: number } | null
}

const loading = ref(false)
const result = ref<OptimizeResult | null>(null)

const scoreColor = computed(() => {
  if (!result.value) return 'border-white/20 text-white/40'
  if (result.value.score >= 80) return 'border-emerald-400/50 text-emerald-300 bg-emerald-500/10'
  if (result.value.score >= 60) return 'border-amber-400/50 text-amber-300 bg-amber-500/10'
  return 'border-rose-400/50 text-rose-300 bg-rose-500/10'
})

function tagBadge(tag: string) {
  if (tag.includes('全表')) return 'bg-rose-500/15 text-rose-300'
  if (tag.includes('索引')) return 'bg-emerald-500/15 text-emerald-300'
  if (tag.includes('排序')) return 'bg-amber-500/15 text-amber-300'
  return 'bg-white/10 text-white/50'
}
function severityBadge(s: string) {
  if (s === 'high') return 'bg-rose-500/20 text-rose-300'
  if (s === 'medium') return 'bg-amber-500/20 text-amber-300'
  return 'bg-white/10 text-white/40'
}

function copyText(t: string) {
  navigator.clipboard.writeText(t).then(() => ElMessage.success('已复制'))
}

function exportResult() {
  if (!result.value) return
  const blob = new Blob([JSON.stringify(result.value, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `optimize_${Date.now()}.json`
  a.click()
  URL.revokeObjectURL(url)
}

async function analyze() {
  if (!props.sql) return
  loading.value = true
  // 模拟 AI 分析延迟
  await new Promise(r => setTimeout(r, 800 + Math.random() * 700))

  const sqlLower = props.sql.toLowerCase()
  const issues: Issue[] = []
  const indexes: IndexRec[] = []
  const rewrites: Rewrite[] = []
  let score = 90
  const tags: string[] = []

  // 检测 SELECT *
  if (sqlLower.includes('select *')) {
    issues.push({ severity: 'medium', title: '使用 SELECT *', description: 'SELECT * 会查询所有列，增加 IO 和网络开销，建议显式指定所需列', location: 'SELECT 子句' })
    rewrites.push({ title: '显式列名', reason: '减少不必要列的传输，提升性能', before: 'SELECT * FROM orders', after: 'SELECT id, order_no, total_amount, status, created_at FROM orders' })
    score -= 10
    tags.push('SELECT *')
  }
  // 检测无 WHERE 的全表
  if (sqlLower.includes('from') && !sqlLower.includes('where') && sqlLower.includes('select')) {
    issues.push({ severity: 'high', title: '无 WHERE 条件全表扫描', description: '查询未包含 WHERE 过滤条件，将扫描全表，数据量大时性能极差', location: 'WHERE 缺失' })
    score -= 25
    tags.push('全表扫描')
  }
  // 检测 EXPLAIN 中的问题
  const explainText = props.explainRows.map(r => String((r as any)[0] || '')).join(' ').toLowerCase()
  if (explainText.includes('seq scan')) {
    issues.push({ severity: 'high', title: '检测到 Seq Scan 全表扫描', description: 'PostgreSQL 执行计划中出现 Seq Scan，建议为过滤条件添加索引', location: 'EXPLAIN: Seq Scan' })
    // 尝试从 SQL 提取表和列
    const tableMatch = props.sql.match(/FROM\s+(\w+)/i)
    const whereMatch = props.sql.match(/WHERE\s+(\w+)\s*[=<>]/i)
    const table = tableMatch?.[1] || 'orders'
    const col = whereMatch?.[1] || 'status'
    indexes.push({ table, type: 'B-Tree', ddl: `CREATE INDEX idx_${table}_${col} ON ${table} (${col});`, benefit: '90% 行过滤' })
    score -= 20
    tags.push('全表扫描')
  }
  if (explainText.includes('filesort') || explainText.includes('sort')) {
    issues.push({ severity: 'medium', title: '文件排序', description: 'EXPLAIN 显示 Using filesort 或 Sort 操作，ORDER BY 未命中索引', location: 'EXPLAIN: Sort' })
    const tableMatch = props.sql.match(/FROM\s+(\w+)/i)
    const orderMatch = props.sql.match(/ORDER BY\s+(\w+)/i)
    if (tableMatch && orderMatch) {
      indexes.push({ table: tableMatch[1]!, type: 'B-Tree', ddl: `CREATE INDEX idx_${tableMatch[1]}_${orderMatch[1]} ON ${tableMatch[1]} (${orderMatch[1]});`, benefit: '避免排序' })
    }
    score -= 10
    tags.push('文件排序')
  }
  if (explainText.includes('temporary')) {
    issues.push({ severity: 'medium', title: '使用临时表', description: 'GROUP BY 或 DISTINCT 导致 Using temporary，考虑优化分组逻辑或添加覆盖索引', location: 'EXPLAIN: temporary' })
    score -= 10
    tags.push('临时表')
  }
  if (!sqlLower.includes('limit') && sqlLower.startsWith('select')) {
    issues.push({ severity: 'low', title: '无 LIMIT 限制', description: 'SELECT 查询未包含 LIMIT，返回行数可能过多，建议添加 LIMIT 1000 以内', location: 'LIMIT 缺失' })
    rewrites.push({ title: '添加 LIMIT', reason: '防止大结果集导致内存和网络压力', before: props.sql.slice(0, 80), after: props.sql.replace(/;?\s*$/, '') + ' LIMIT 100' })
    score -= 5
    tags.push('无 LIMIT')
  }
  // JOIN 优化
  if (sqlLower.includes('join') && !sqlLower.includes('on')) {
    issues.push({ severity: 'high', title: 'JOIN 缺少 ON 条件', description: 'JOIN 未指定连接条件，可能产生笛卡尔积', location: 'JOIN' })
    score -= 20
  }
  if (sqlLower.includes('join')) {
    const joinTables = [...props.sql.matchAll(/JOIN\s+(\w+)/gi)].map(m => m[1])
    if (joinTables.length >= 1) {
      tags.push('多表 JOIN')
    }
  }

  if (!issues.length) {
    tags.push('健康')
  }

  // 重写建议：子查询改 JOIN
  if (sqlLower.includes('in (select')) {
    rewrites.push({ title: 'IN 子查询改 JOIN', reason: 'IN + 子查询在某些版本中性能较差，JOIN 通常更高效', before: 'WHERE id IN (SELECT ...)', after: 'JOIN (SELECT ...) AS sub ON ...' })
  }

  // 成本
  const cost = props.duration ? {
    estimated_rows: props.explainRows.length ? String(props.explainRows.length * 100) : '1000',
    estimated_cost: (Math.random() * 200 + 50).toFixed(2),
    actual_time: props.duration,
  } : null

  result.value = {
    score: Math.max(0, score),
    summary: score >= 80 ? '查询较为健康，少量优化空间' : score >= 60 ? '存在中等性能问题，建议优化' : '存在严重性能问题，需立即优化',
    tags: [...new Set(tags)],
    issues,
    indexes,
    rewrites,
    cost,
  }
  loading.value = false
}
</script>
