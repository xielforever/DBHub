<template>
  <div class="space-y-5">
    <header class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          <Ticket class="w-5 h-5 text-amber-300" />变更工单
        </h1>
        <p class="text-sm text-white/45 mt-1">变更工单的创建、审批与执行；支持血缘影响一键建单，完整审计留痕</p>
      </div>
      <div class="flex items-center gap-2 shrink-0">
        <button class="ghost-button text-xs" @click="reload">刷新</button>
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
      <ChangeTicketPane :tickets="tickets as any" :prefill="prefill" :create="onCreate" @update-status="onStatus" />
    </section>

    <p class="text-[11px] text-white/25 text-center">工单保存到服务端，审批和执行均需管理员权限，失败结果会保留。</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Ticket, Clock3, CheckCircle2, XCircle, PlayCircle, GitBranch } from 'lucide-vue-next'
import { ElMessage, ElMessageBox } from 'element-plus'
import ChangeTicketPane from '../query/components/ChangeTicketPane.vue'
import { ticketApi, type TicketItem, type TicketPayload } from '../../api/ticket'

const route = useRoute()
const router = useRouter()

const tickets = ref<TicketItem[]>([])
const loading = ref(false)
async function reload() {loading.value=true;try {tickets.value=(await ticketApi.list({page:1,page_size:100})).items} catch(e:unknown) {ElMessage.error(e instanceof Error?e.message:'工单加载失败')} finally {loading.value=false}}

const kpis = computed(() => [
  { label: '全部工单', value: tickets.value.length, icon: Ticket, color: '#a78bfa', tint: 'rgba(167,139,250,0.16)' },
  { label: '待审批', value: tickets.value.filter(t=>t.status==='pending').length, icon: Clock3, color: '#fbbf24', tint: 'rgba(251,191,36,0.15)' },
  { label: '已批准', value: tickets.value.filter(t=>t.status==='approved').length, icon: CheckCircle2, color: '#34d399', tint: 'rgba(52,211,153,0.15)' },
  { label: '已执行', value: tickets.value.filter(t=>t.status==='executed').length, icon: PlayCircle, color: '#60a5fa', tint: 'rgba(96,165,250,0.16)' },
  { label: '已拒绝', value: tickets.value.filter(t=>t.status==='rejected').length, icon: XCircle, color: '#f87171', tint: 'rgba(248,113,113,0.14)' },
])

const prefill = ref<{ title?:string; table?:string; sql?:string; risk?:string; reason?:string } | null>(null)
const incoming = ref<{ table:string; db:string; risk:string } | null>(null)

async function onCreate(payload: TicketPayload):Promise<boolean> {
 try {const created=await ticketApi.create({...payload,database:payload.database||incoming.value?.db||''});tickets.value.unshift(created);ElMessage.success(`工单 #${created.id} 已创建`);incoming.value=null;if(route.query.table)router.replace({query:{}});return true} catch(e:unknown) {ElMessage.error(e instanceof Error?e.message:'创建失败');return false}
}
async function onStatus(id:number,status:string) {
 try {
  if(status==='executed') {await ElMessageBox.confirm('执行将修改目标数据库，确认执行已批准的 SQL？','执行工单',{type:'warning',confirmButtonText:'执行',cancelButtonText:'取消'});await ticketApi.execute(id)}
  else await ticketApi.updateStatus(id,status as TicketItem['status'])
  ElMessage.success(status==='executed'?'SQL 已实际执行':'审批已保存')
 } catch(e:unknown) {if(e instanceof Error)ElMessage.error(e.message)} finally {await reload()}
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
        title: q.title ? q.title : `${q.table} 变更`,
        table: q.table,
        sql: q.sql ? q.sql : '',
        risk: (q.risk as string) || 'medium',
        reason: q.reason ? q.reason : '',
      }
    }
  }
})
</script>
