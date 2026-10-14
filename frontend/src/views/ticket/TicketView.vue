<template>
  <div class="space-y-5">
    <header class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          <Ticket class="w-5 h-5 text-amber-300" />工单中心
        </h1>
        <p class="text-sm text-white/45 mt-1">变更工单的创建、审批与执行；支持血缘影响一键建单，完整审计留痕</p>
      </div>
      <div class="flex items-center gap-2 shrink-0">
        <button class="ghost-button text-xs" @click="loadDemo">加载示例</button>
        <button class="ghost-button text-xs" @click="clearFinished">清空已办</button>
      </div>
    </header>

    <!-- KPI -->
    <section class="grid grid-cols-2 md:grid-cols-5 gap-3">
      <div v-for="kpi in kpis" :key="kpi.label" class="glass-card p-4 flex items-center gap-3">
        <span class="w-9 h-9 rounded-xl flex items-center justify-center shrink-0" :style="{ background: kpi.tint }">
          <component :is="kpi.icon" class="w-4.5 h-4.5" :style="{ color: kpi.color }" />
        </span>
        <div class="min-w-0">
          <p class="text-lg font-semibold leading-tight">{{ kpi.value }}</p>
          <p class="text-[11px] text-white/45 truncate">{{ kpi.label }}</p>
        </div>
      </div>
    </section>

    <!-- 血缘来源提示 -->
    <div v-if="incoming" class="glass-card p-3 flex flex-col sm:flex-row sm:items-center gap-3 border-amber-400/20">
      <div class="flex items-center gap-2 text-amber-300 text-xs">
        <GitBranch class="w-3.5 h-3.5" />来自血缘影响分析
      </div>
      <p class="text-xs text-white/60 flex-1 truncate">
        目标表 <span class="font-mono text-white">{{ incoming.table }}</span>
        <span v-if="incoming.db" class="text-white/40">（{{ incoming.db }}）</span>
        <span class="hidden sm:inline text-white/30"> · 风险 {{ incoming.risk }}</span>
      </p>
      <div class="flex gap-2 shrink-0">
        <button class="ghost-button text-xs" @click="incoming=null">忽略</button>
        <button class="liquid-button text-xs" @click="openPrefill">一键建单</button>
      </div>
    </div>

    <!-- 主体：复用工作台工单能力 -->
    <section class="glass-card p-0 overflow-hidden min-h-[540px] flex flex-col">
      <ChangeTicketPane :tickets="tickets" :prefill="prefill" @create="onCreate" @update-status="onStatus" />
    </section>

    <p class="text-[11px] text-white/25 text-center">工单数据本地演示存储，接入后端后将持久化至服务端并进入审计</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Ticket, Clock3, CheckCircle2, XCircle, PlayCircle, GitBranch } from 'lucide-vue-next'
import { ElMessage } from 'element-plus'
import ChangeTicketPane from '../query/components/ChangeTicketPane.vue'
import { useUserStore } from '../../stores/user'

interface TicketItem { id: number; title: string; type: 'ddl'|'dml'|'index'; table: string; sql: string; risk: 'low'|'medium'|'high'; status: 'pending'|'approved'|'rejected'|'executed'; requester: string; reason: string; created_at: string }

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()

const LS_KEY = 'dbhub_tickets_v1'
const tickets = ref<TicketItem[]>(load())

function load(): TicketItem[] {
  try {
    const raw = localStorage.getItem(LS_KEY)
    if (raw) return JSON.parse(raw)
  } catch {}
  return [
    { id: 1001, title: 'orders 添加索引 idx_amount', type: 'index', table: 'orders', sql: 'CREATE INDEX idx_amount ON orders(total_amount)', risk: 'low', status: 'pending', requester: 'dev01', reason: '加速金额范围查询', created_at: new Date(Date.now()-3600000*2).toISOString() },
    { id: 1002, title: 'customers 敏感列脱敏', type: 'ddl', table: 'customers', sql: 'ALTER TABLE customers ALTER COLUMN phone TYPE varchar(20)', risk: 'medium', status: 'approved', requester: 'admin', reason: '合规脱敏', created_at: new Date(Date.now()-3600000*24).toISOString() },
    { id: 1003, title: 'order_items 清理过期数据', type: 'dml', table: 'order_items', sql: 'DELETE FROM order_items WHERE created_at < now() - interval \'90 days\'', risk: 'high', status: 'executed', requester: 'dev02', reason: '归档清理', created_at: new Date(Date.now()-3600000*50).toISOString() },
  ]
}
function persist() {
  try { localStorage.setItem(LS_KEY, JSON.stringify(tickets.value)) } catch {}
}
watch(tickets, persist, { deep: true })

const kpis = computed(() => [
  { label: '全部工单', value: tickets.value.length, icon: Ticket, color: '#a78bfa', tint: 'rgba(167,139,250,0.16)' },
  { label: '待审批', value: tickets.value.filter(t=>t.status==='pending').length, icon: Clock3, color: '#fbbf24', tint: 'rgba(251,191,36,0.15)' },
  { label: '已批准', value: tickets.value.filter(t=>t.status==='approved').length, icon: CheckCircle2, color: '#34d399', tint: 'rgba(52,211,153,0.15)' },
  { label: '已执行', value: tickets.value.filter(t=>t.status==='executed').length, icon: PlayCircle, color: '#60a5fa', tint: 'rgba(96,165,250,0.16)' },
  { label: '已拒绝', value: tickets.value.filter(t=>t.status==='rejected').length, icon: XCircle, color: '#f87171', tint: 'rgba(248,113,113,0.14)' },
])

const prefill = ref<{ title?:string; table?:string; sql?:string; risk?:string; reason?:string } | null>(null)
const incoming = ref<{ table:string; db:string; risk:string } | null>(null)

function onCreate(payload: Omit<TicketItem,'id'|'status'|'requester'|'created_at'>) {
  const id = Date.now()
  tickets.value.unshift({ id, ...payload, status:'pending', requester: userStore.user?.username || 'user', created_at: new Date().toISOString() })
  ElMessage.success(`工单 #${id} 已创建`)
  incoming.value = null
  // 清除 query，避免刷新重复
  if (route.query.table) router.replace({ query: { ...route.query, table: undefined, db: undefined, title: undefined } })
}

function onStatus(id:number, status:string) {
  const t = tickets.value.find(x=>x.id===id)
  if (t) { t.status = status as any; ElMessage.success(`工单 #${id} 已 ${status==='approved'?'批准':status==='rejected'?'拒绝':status==='executed'?'执行':'更新'}`) }
}

function loadDemo() {
  tickets.value = load()
  ElMessage.success('已加载示例')
}
function clearFinished() {
  const before = tickets.value.length
  tickets.value = tickets.value.filter(t=>t.status==='pending' || t.status==='approved')
  ElMessage.success(`已清理 ${before - tickets.value.length} 单`)
}

function openPrefill() {
  if (!incoming.value) return
  prefill.value = {
    title: `${incoming.value.table} 变更（血缘影响）`,
    table: incoming.value.table,
    sql: `ALTER TABLE ${incoming.value.table} -- 请补充变更语句`,
    risk: incoming.value.risk || 'medium',
    reason: `来源于 ${incoming.value.db || ''}.${incoming.value.table} 的血缘影响分析，建议评审下游 ${incoming.value.table} 依赖`,
  }
  // 触发子组件打开弹窗：通过改变 prefill 触发 watch
  // 给子组件一点时间响应后清空 incoming 标识
  setTimeout(()=> { incoming.value = null }, 300)
}

onMounted(() => {
  const q = route.query as Record<string,string | undefined>
  if (q.table) {
    incoming.value = { table: q.table, db: q.db || q.database || '', risk: (q.risk as string) || 'medium' }
    // 若带 sql，则直接预填
    if (q.sql || q.title) {
      prefill.value = {
        title: q.title ? decodeURIComponent(q.title) : `${q.table} 变更`,
        table: q.table,
        sql: q.sql ? decodeURIComponent(q.sql) : '',
        risk: (q.risk as string) || 'medium',
        reason: q.reason ? decodeURIComponent(q.reason) : '',
      }
    }
  }
})
</script>
