<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-white/[0.02]">
      <div class="w-6 h-6 rounded-lg bg-emerald-500/20 border border-emerald-400/30 flex items-center justify-center">
        <GitBranch class="w-3.5 h-3.5 text-emerald-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">数据血缘</p>
        <p class="text-[10px] text-white/40">上游 · 当前 · 下游 · 影响面</p>
      </div>
      <el-select v-model="depth" size="small" class="w-20" popper-class="glass-popper">
        <el-option label="1层" :value="1" />
        <el-option label="2层" :value="2" />
        <el-option label="3层" :value="3" />
      </el-select>
      <button class="ghost-button !py-1 !px-2 text-[11px]" @click="refresh"><RefreshCw class="w-3 h-3" :class="{ 'animate-spin': loading }" /></button>
    </div>

    <div class="px-3 py-2 border-b border-white/10 flex flex-wrap gap-2 items-center bg-black/20 shrink-0">
      <div class="relative flex-1 min-w-[140px]">
        <Search class="w-3 h-3 absolute left-2 top-1/2 -translate-y-1/2 text-white/30" />
        <input v-model.trim="filter" class="glass-input !py-1 !pl-6 text-xs w-full" placeholder="过滤表/视图/报表" />
      </div>
      <div class="flex gap-1">
        <button class="ghost-button !py-1 !px-2 text-[11px]" :class="direction==='both' ? 'bg-white/10' : ''" @click="direction='both'">全部</button>
        <button class="ghost-button !py-1 !px-2 text-[11px]" :class="direction==='upstream' ? 'bg-white/10' : ''" @click="direction='upstream'">上游</button>
        <button class="ghost-button !py-1 !px-2 text-[11px]" :class="direction==='downstream' ? 'bg-white/10' : ''" @click="direction='downstream'">下游</button>
      </div>
      <span class="text-[11px] px-1.5 py-0.5 rounded-full bg-emerald-500/15 text-emerald-300">{{ nodes.length }} 节点 · {{ edges }} 边</span>
    </div>

    <div class="flex-1 overflow-auto p-4" v-loading="loading">
      <div v-if="!currentTable" class="text-center text-xs text-white/35 py-10">
        未选择表，血缘以当前查询表 <span class="font-mono text-white/60">{{ fallbackTable }}</span> 为中心
      </div>

      <!-- 图谱 -->
      <div class="flex items-stretch gap-3 min-h-[220px]">
        <!-- 上游 -->
        <div v-if="direction!=='downstream'" class="flex-1 space-y-2">
          <p class="text-[11px] text-white/40 flex items-center gap-1"><ArrowLeft class="w-3 h-3" /> 上游 {{ upstreamNodes.length }}</p>
          <div v-for="n in upstreamNodes" :key="n.id" class="rounded-xl border p-2.5 cursor-pointer hover:brightness-110 transition" :class="n.type==='table' ? 'bg-sky-500/10 border-sky-400/20' : 'bg-white/5 border-white/10'" @click="emit('select', n)">
            <div class="flex items-center gap-2">
              <Table2 v-if="n.type==='table'" class="w-3.5 h-3.5 text-sky-300" />
              <Eye v-else class="w-3.5 h-3.5 text-white/40" />
              <span class="text-xs font-mono truncate flex-1" :class="n.type==='table' ? 'text-sky-200' : 'text-white/60'">{{ n.name }}</span>
              <span class="text-[10px] px-1 py-0.5 rounded bg-white/10 text-white/40">{{ n.db }}</span>
            </div>
            <p class="text-[10px] text-white/30 mt-1 truncate">{{ n.desc }}</p>
          </div>
          <p v-if="!upstreamNodes.length" class="text-[11px] text-white/25 py-4 text-center">无上游</p>
        </div>

        <!-- 连接线 -->
        <div v-if="direction==='both'" class="w-px bg-white/10 self-stretch my-2 hidden sm:block" />

        <!-- 当前 -->
        <div class="flex-1 flex flex-col items-center justify-center gap-2">
          <div class="w-full rounded-xl bg-violet-500/15 border border-violet-400/30 p-3 text-center shadow-lg shadow-violet-500/10">
            <p class="text-xs font-mono font-medium text-violet-200">{{ currentTable || fallbackTable }}</p>
            <p class="text-[10px] text-white/40 mt-1">{{ currentDb || 'orders' }} · 当前表</p>
            <span class="inline-flex mt-2 px-2 py-0.5 rounded-full bg-violet-500/20 text-violet-300 text-[10px]">中心节点</span>
          </div>
          <div class="flex items-center gap-1 text-[10px] text-white/25">
            <span class="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
            影响面 {{ downstreamNodes.length + upstreamNodes.length }} 对象
          </div>
        </div>

        <div v-if="direction==='both'" class="w-px bg-white/10 self-stretch my-2 hidden sm:block" />

        <!-- 下游 -->
        <div v-if="direction!=='upstream'" class="flex-1 space-y-2">
          <p class="text-[11px] text-white/40 flex items-center gap-1">下游 {{ downstreamNodes.length }} <ArrowRight class="w-3 h-3" /></p>
          <div v-for="n in downstreamNodes" :key="n.id" class="rounded-xl border p-2.5 cursor-pointer hover:brightness-110 transition" :class="n.type==='report' ? 'bg-amber-500/10 border-amber-400/20' : n.type==='view' ? 'bg-emerald-500/10 border-emerald-400/20' : 'bg-white/5 border-white/10'" @click="emit('select', n)">
            <div class="flex items-center gap-2">
              <BarChart3 v-if="n.type==='report'" class="w-3.5 h-3.5 text-amber-300" />
              <Eye v-else-if="n.type==='view'" class="w-3.5 h-3.5 text-emerald-300" />
              <Table2 v-else class="w-3.5 h-3.5 text-white/40" />
              <span class="text-xs font-mono truncate flex-1" :class="n.type==='report' ? 'text-amber-200' : n.type==='view' ? 'text-emerald-200' : 'text-white/60'">{{ n.name }}</span>
              <span class="text-[10px] px-1 py-0.5 rounded bg-white/10 text-white/40">{{ n.db }}</span>
            </div>
            <p class="text-[10px] text-white/30 mt-1 truncate">{{ n.desc }}</p>
          </div>
          <p v-if="!downstreamNodes.length" class="text-[11px] text-white/25 py-4 text-center">无下游</p>
        </div>
      </div>

      <!-- 影响分析 -->
      <div class="mt-4 rounded-xl bg-amber-500/10 border border-amber-400/20 p-3">
        <p class="text-xs font-medium text-amber-200 flex items-center gap-1.5"><AlertTriangle class="w-3.5 h-3.5" /> 变更影响分析</p>
        <p class="text-[11px] text-white/50 mt-1">若修改 <span class="font-mono text-white/70">{{ currentTable || fallbackTable }}</span> 的列 <span class="font-mono text-amber-200">total_amount</span>，将影响下游 {{ downstreamNodes.length }} 对象，建议先创建变更工单并通知 Owner。</p>
        <div class="flex gap-1.5 mt-2">
          <span v-for="n in downstreamNodes.slice(0,3)" :key="n.id" class="px-2 py-1 rounded-full bg-white/10 text-white/60 text-[11px]">{{ n.name }}</span>
          <span v-if="downstreamNodes.length>3" class="text-[11px] text-white/30">+{{ downstreamNodes.length-3 }}</span>
        </div>
        <button class="ghost-button !py-1 !px-3 text-xs mt-3" @click="emit('createTicket')">创建变更工单</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { AlertTriangle, ArrowLeft, ArrowRight, BarChart3, Eye, GitBranch, RefreshCw, Search, Table2 } from 'lucide-vue-next'

const props = defineProps<{
  currentTable: string
  currentDb: string
  fallbackTable: string
}>()

const emit = defineEmits<{
  (e: 'select', node: any): void
  (e: 'createTicket'): void
}>()

const depth = ref(2)
const direction = ref<'both'|'upstream'|'downstream'>('both')
const filter = ref('')
const loading = ref(false)

interface Node { id: string; name: string; type: 'table'|'view'|'report'; db: string; desc: string }
const upstream = ref<Node[]>([])
const downstream = ref<Node[]>([])

function generate() {
  loading.value = true
  setTimeout(() => {
    upstream.value = [
      { id: 'u1', name: 'users', type: 'table' as const, db: 'users', desc: '用户主表 · 外键 users.id' },
      { id: 'u2', name: 'products', type: 'table' as const, db: 'shop', desc: '商品表 · JOIN products.id' },
      { id: 'u3', name: 'orders_staging', type: 'table' as const, db: 'orders', desc: '临时表 · ETL 来源' },
    ].slice(0, depth.value) as Node[]
    downstream.value = [
      { id: 'd1', name: 'v_monthly_sales', type: 'view' as const, db: 'orders', desc: '视图 · 依赖 orders' },
      { id: 'd2', name: 'v_user_orders', type: 'view' as const, db: 'orders', desc: '视图 · 关联 users' },
      { id: 'd3', name: '月度销售趋势', type: 'report' as const, db: 'orders', desc: '报表 #1 · 订阅 12 人' },
      { id: 'd4', name: '今日订单指标', type: 'report' as const, db: 'orders', desc: '报表 #3 · 大盘' },
    ].slice(0, depth.value + 1) as Node[]
    if (filter.value) {
      const kw = filter.value.toLowerCase()
      upstream.value = upstream.value.filter(n => n.name.toLowerCase().includes(kw))
      downstream.value = downstream.value.filter(n => n.name.toLowerCase().includes(kw))
    }
    loading.value = false
  }, 400)
}

const upstreamNodes = computed(() => upstream.value)
const downstreamNodes = computed(() => downstream.value)
const nodes = computed(() => [...upstreamNodes.value, ...downstreamNodes.value, { id: 'center', name: props.currentTable || props.fallbackTable, type: 'table', db: props.currentDb || 'orders', desc: '当前' } as Node])
const edges = computed(() => upstreamNodes.value.length + downstreamNodes.value.length)

function refresh() { generate() }
onMounted(generate)
watch([depth, () => props.currentTable, filter], generate)
</script>
