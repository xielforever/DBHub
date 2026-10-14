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
        <button class="ghost-button text-xs" @click="reload">刷新</button>
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
    <section class="glass-card p-0 overflow-hidden min-h-[540px] flex flex-col" v-loading="loading">
      <ChangeTicketPane :tickets="tickets as any" :prefill="prefill" @create="onCreate" @update-status="onStatus" />
    </section>

    <p class="text-[11px] text-white/25 text-center">
      <span v-if="usingMock">当前为本地演示存储（Mock 未接通），已自动回退至 localStorage</span>
      <span v-else>已接入服务端持久化，写操作进入审计</span>
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Ticket, Clock3, CheckCircle2, XCircle, PlayCircle, GitBranch } from 'lucide-vue-next'
import { ElMessage } from 'element-plus'
import ChangeTicketPane from '../query/components/ChangeTicketPane.vue'
import { useUserStore } from '../../stores/user'
import { ticketApi, type TicketItem } from '../../api/ticket'

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()

const LS_KEY = 'dbhub_tickets_v1'
const tickets = ref<TicketItem[]>([])
const loading = ref(false)
const usingMock = ref(false)

function loadMock(): TicketItem[] {
  try {
    const raw = localStorage.getItem(LS_KEY)
    if (raw) {
      const arr = JSON.parse(raw)
      if (Array.isArray(arr) && arr.length) return arr
    }
  } catch {}
  return [
    { id: 1001, title: 'orders 添加索引 idx_amount', type: 'index', table: 'orders', database_name: 'orders', connection_id: null, sql: 'CREATE INDEX idx_amount ON orders(total_amount)', risk: 'low', status: 'pending', requester_id: 1, requester: 'dev01', reason: '加速金额范围查询', created_at: new Date(Date.now()-3600000*2).toISOString(), updated_at: new Date().toISOString() },
    { id: 1002, title: 'customers 敏感列脱敏', type: 'ddl', table: 'customers', database_name: 'orders', connection_id: null, sql: 'ALTER TABLE customers ALTER COLUMN phone TYPE varchar(20)', risk: 'medium', status: 'approved', requester_id: 1, requester: 'admin', reason: '合规脱敏', created_at: new Date(Date.now()-3600000*24).toISOString(), updated_at: new Date().toISOString() },
    { id: 1003, title: 'order_items 清理过期数据', type: 'dml', table: 'order_items', database_name: 'orders', connection_id: null, sql: "DELETE FROM order_items WHERE created_at < now() - interval '90 days'", risk: 'high', status: 'executed', requester_id: 1, requester: 'dev02', reason: '归档清理', created_at: new Date(Date.now()-3600000*50).toISOString(), updated_at: new Date().toISOString() },
  ] as TicketItem[]
}
function persistMock() {
  try { localStorage.setItem(LS_KEY, JSON.stringify(tickets.value)) } catch {}
}
watch(tickets, () => { if (usingMock.value) persistMock() }, { deep: true })

async function reload() {
  loading.value = true
  try {
    const data = await ticketApi.list({ page: 1, page_size: 100 })
    tickets.value = data.items
    usingMock.value = false
  } catch (e) {
    // 回退至本地 Mock（无后端或 404 时）
    usingMock.value = true
    tickets.value = loadMock()
  } finally { loading.value = false }
}

const kpis = computed(() => [
  { label: '全部工单', value: tickets.value.length, icon: Ticket, color: '#a78bfa', tint: 'rgba(167,139,250,0.16)' },
  { label: '待审批', value: tickets.value.filter(t=>t.status==='pending').length, icon: Clock3, color: '#fbbf24', tint: 'rgba(251,191,36,0.15)' },
  { label: '已批准', value: tickets.value.filter(t=>t.status==='approved').length, icon: CheckCircle2, color: '#34d399', tint: 'rgba(52,211,153,0.15)' },
  { label: '已执行', value: tickets.value.filter(t=>t.status==='executed').length, icon: PlayCircle, color: '#60a5fa', tint: 'rgba(96,165,250,0.16)' },
  { label: '已拒绝', value: tickets.value.filter(t=>t.status==='rejected').length, icon: XCircle, color: '#f87171', tint: 'rgba(248,113,113,0.14)' },
])

const prefill = ref<{ title?:string; table?:string; sql?:string; risk?:string; reason?:string } | null>(null)
const incoming = ref<{ table:string; db:string; risk:string } | null>(null)

async function onCreate(payload: Omit<TicketItem,'id'|'status'|'requester'|'requester_id'|'created_at'|'updated_at'|'database_name'|'connection_id'> & { reason: string }) {
  // 兼容旧 ChangeTicketPane  payload：{ title, type, table, sql, risk, reason }
  const title = (payload as any).title as string
  const type = (payload as any).type as TicketItem['type']
  const table = (payload as any).table as string
  const sql = (payload as any).sql as string
  const risk = (payload as any).risk as TicketItem['risk']
  const reason = (payload as any).reason as string
  try {
    const created = await ticketApi.create({ title, type, table, database: incoming.value?.db || '', sql, risk, reason })
    tickets.value.unshift(created)
    ElMessage.success(`工单 #${created.id} 已创建`)
    usingMock.value = false
  } catch (e:any) {
    // Mock 回退
    const id = Date.now()
    const mock: TicketItem = {
      id, title, type, table, database_name: incoming.value?.db || '', connection_id: null,
      sql, risk, status: 'pending', requester_id: 0, requester: userStore.user?.username || 'user',
      reason, created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
    }
    tickets.value.unshift(mock)
    usingMock.value = true
    persistMock()
    ElMessage.success(`工单 #${id} 已创建（本地演示）`)
  }
  incoming.value = null
  if (route.query.table) router.replace({ query: { ...route.query, table: undefined, db: undefined, title: undefined, sql: undefined, reason: undefined } })
}

async function onStatus(id:number, status:string) {
  try {
    await ticketApi.updateStatus(id, status as TicketItem['status'])
    const t = tickets.value.find(x=>x.id===id)
    if (t) t.status = status as any
    ElMessage.success(`工单 #${id} 已 ${status==='approved'?'批准':status==='rejected'?'拒绝':status==='executed'?'执行':'更新'}`)
    usingMock.value = false
  } catch (e:any) {
    const t = tickets.value.find(x=>x.id===id)
    if (t) { t.status = status as any; persistMock(); ElMessage.success(`工单 #${id} 已 ${status==='approved'?'批准':status==='rejected'?'拒绝':status==='executed'?'执行':'更新'}（本地）`) }
    else ElMessage.error(e?.message || '更新失败')
  }
}

function loadDemo() {
  tickets.value = loadMock()
  usingMock.value = true
  persistMock()
  ElMessage.success('已加载示例（本地）')
}
function clearFinished() {
  const before = tickets.value.length
  tickets.value = tickets.value.filter(t=>t.status==='pending' || t.status==='approved')
  if (usingMock.value) persistMock()
  ElMessage.success(`已清理 ${before - tickets.value.length} 单（本地视图）`)
  if (!usingMock.value) ElMessage.info('后端数据未删除，仅本地过滤；请在服务端实现批量清理')
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
  setTimeout(()=> { incoming.value = null }, 300)
}

onMounted(async () => {
  await reload()
  const q = route.query as Record<string,string | undefined>
  if (q.table) {
    incoming.value = { table: q.table, db: q.db || q.database || '', risk: (q.risk as string) || 'medium' }
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
