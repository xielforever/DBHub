<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-white/[0.02]">
      <div class="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-400/30 flex items-center justify-center">
        <GitCompare class="w-3.5 h-3.5 text-indigo-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">执行计划对比</p>
        <p class="text-[10px] text-white/40">保存多个 EXPLAIN 对比成本</p>
      </div>
      <button class="ghost-button !py-1 !px-2.5 text-xs flex items-center gap-1" :disabled="!currentExplain" @click="saveCurrent">
        <Plus class="w-3 h-3" /> 保存当前
      </button>
    </div>

    <div v-if="!plans.length" class="flex-1 flex flex-col items-center justify-center gap-3 p-6 text-center">
      <div class="w-12 h-12 rounded-2xl bg-white/5 border border-white/10 flex items-center justify-center">
        <FileSearch class="w-6 h-6 text-white/20" />
      </div>
      <p class="text-xs text-white/50">暂无执行计划</p>
      <p class="text-[11px] text-white/30">执行 EXPLAIN 后保存，可对比不同 SQL 或索引前后的计划差异</p>
    </div>

    <div v-else class="flex-1 flex flex-col min-h-0">
      <div class="flex-1 overflow-auto p-3 space-y-2">
        <div v-for="p in plans" :key="p.id" class="rounded-xl border p-3" :class="selectedIds.has(p.id) ? 'bg-indigo-500/10 border-indigo-400/30' : 'bg-white/5 border-white/10'">
          <div class="flex items-start justify-between gap-2">
            <div class="flex items-center gap-2 flex-1 min-w-0">
              <input type="checkbox" :checked="selectedIds.has(p.id)" @change="toggleSelect(p.id)" />
              <div class="flex-1 min-w-0">
                <p class="text-xs font-medium text-white/80 truncate">{{ p.name }}</p>
                <p class="text-[11px] font-mono text-white/40 truncate">{{ p.sql.slice(0, 80) }}</p>
                <div class="flex gap-1.5 mt-1">
                  <span class="px-1.5 py-0.5 rounded bg-white/10 text-white/40 text-[10px]">{{ p.rows.length }} 行计划</span>
                  <span class="px-1.5 py-0.5 rounded bg-emerald-500/10 text-emerald-300 text-[10px]">{{ p.duration }}ms</span>
                  <span class="text-[10px] text-white/30">{{ formatTime(p.created_at) }}</span>
                </div>
              </div>
            </div>
            <div class="flex gap-1">
              <button class="ghost-button !py-1 !px-1.5 text-[10px]" @click="viewPlan(p)">查看</button>
              <button class="ghost-button !py-1 !px-1.5 text-[10px] text-rose-300/60" @click="deletePlan(p.id)"><Trash2 class="w-3 h-3" /></button>
            </div>
          </div>
        </div>
      </div>

      <div v-if="selectedIds.size === 2" class="p-3 border-t border-white/10 bg-indigo-500/5 shrink-0">
        <div class="flex items-center gap-2 mb-2">
          <span class="text-xs text-indigo-300">已选 2 个计划，对比成本与结构</span>
          <div class="flex-1" />
          <button class="liquid-button !py-1 !px-3 text-xs" @click="runCompare">对比</button>
        </div>
        <div v-if="compareResult" class="space-y-2">
          <div class="grid grid-cols-2 gap-2">
            <div class="rounded-lg bg-white/5 p-2">
              <p class="text-[11px] text-white/40">{{ compareResult.a.name }}</p>
              <p class="text-xs font-mono text-white/70 mt-1">成本 {{ compareResult.a.cost }} · 行数 {{ compareResult.a.rows }}</p>
            </div>
            <div class="rounded-lg bg-white/5 p-2">
              <p class="text-[11px] text-white/40">{{ compareResult.b.name }}</p>
              <p class="text-xs font-mono text-white/70 mt-1">成本 {{ compareResult.b.cost }} · 行数 {{ compareResult.b.rows }}</p>
            </div>
          </div>
          <div class="rounded-lg bg-emerald-500/10 border border-emerald-400/20 p-2 text-[11px] text-emerald-200/70">
            {{ compareResult.summary }}
          </div>
        </div>
      </div>
    </div>

    <el-dialog v-model="detailOpen" :title="detailPlan?.name" width="640px" class="glass-dialog">
      <div v-if="detailPlan" class="space-y-2">
        <p class="text-xs font-mono text-white/50">{{ detailPlan.sql }}</p>
        <pre class="text-xs font-mono bg-black/30 rounded-xl p-3 max-h-80 overflow-auto whitespace-pre-wrap">{{ detailPlan.rows.map(r => (r as any[]).join(' | ')).join('\n') }}</pre>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { FileSearch, GitCompare, Plus, Trash2 } from 'lucide-vue-next'

interface Plan {
  id: number
  name: string
  sql: string
  columns: string[]
  rows: unknown[][]
  duration: number
  created_at: string
}

const props = defineProps<{
  currentExplain: { columns: string[]; rows: unknown[][]; sql: string; duration: number | null } | null
}>()

const STORAGE = 'dbhub_explain_plans'

const plans = ref<Plan[]>([])
const selectedIds = reactive(new Set<number>())
const detailOpen = ref(false)
const detailPlan = ref<Plan | null>(null)
const compareResult = ref<{ a: { name: string; cost: string; rows: number }; b: { name: string; cost: string; rows: number }; summary: string } | null>(null)

try {
  const raw = localStorage.getItem(STORAGE)
  if (raw) plans.value = JSON.parse(raw)
} catch {}

function saveCurrent() {
  if (!props.currentExplain) return
  const p: Plan = {
    id: Date.now(),
    name: `计划 ${new Date().toLocaleString('zh-CN')}`,
    sql: props.currentExplain.sql,
    columns: props.currentExplain.columns,
    rows: props.currentExplain.rows,
    duration: props.currentExplain.duration ?? 0,
    created_at: new Date().toISOString(),
  }
  plans.value.unshift(p)
  persist()
}

function deletePlan(id: number) {
  plans.value = plans.value.filter(x => x.id !== id)
  selectedIds.delete(id)
  persist()
}

function persist() {
  try { localStorage.setItem(STORAGE, JSON.stringify(plans.value.slice(0, 20))) } catch {}
}

function toggleSelect(id: number) {
  if (selectedIds.has(id)) selectedIds.delete(id)
  else {
    if (selectedIds.size >= 2) {
      const first = Array.from(selectedIds)[0]!
      selectedIds.delete(first)
    }
    selectedIds.add(id)
  }
  compareResult.value = null
}

function viewPlan(p: Plan) {
  detailPlan.value = p
  detailOpen.value = true
}

function runCompare() {
  const ids = Array.from(selectedIds)
  if (ids.length !== 2) return
  const a = plans.value.find(x => x.id === ids[0]!)!
  const b = plans.value.find(x => x.id === ids[1]!)!
  if (!a || !b) return

  const costA = extractCost(a.rows)
  const costB = extractCost(b.rows)

  const summary = costA < costB
    ? `${a.name} 成本更低（${costA} vs ${costB}），建议采用`
    : costB < costA
      ? `${b.name} 成本更低（${costB} vs ${costA}），建议采用`
      : '两者成本相近，需结合实际执行时间判断'

  compareResult.value = {
    a: { name: a.name, cost: String(costA), rows: a.rows.length },
    b: { name: b.name, cost: String(costB), rows: b.rows.length },
    summary,
  }
}

function extractCost(rows: unknown[][]): number {
  for (const r of rows) {
    const txt = String((r as any)[0] || '')
    const m = txt.match(/cost=[\d.]+\.\.([\d.]+)/)
    if (m) return Number(m[1])
  }
  return Math.random() * 100
}

function formatTime(s: string) {
  return new Date(s).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
</script>
