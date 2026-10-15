<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-white/[0.02]">
      <div class="w-6 h-6 rounded-lg bg-amber-500/20 border border-amber-400/30 flex items-center justify-center">
        <Ticket class="w-3.5 h-3.5 text-amber-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">变更工单</p>
        <p class="text-[10px] text-white/40">{{ tickets.length }} 单 · {{ pendingCount }} 待审批</p>
      </div>
      <button class="liquid-button !py-1 !px-3 text-xs flex items-center gap-1" @click="showCreate = true"><Plus class="w-3 h-3" /> 新建</button>
    </div>

    <div class="px-3 py-2 border-b border-white/10 flex gap-2 items-center bg-black/20 shrink-0">
      <el-select v-model="statusFilter" size="small" class="w-24" popper-class="glass-popper">
        <el-option label="全部" value="" />
        <el-option label="待审批" value="pending" />
        <el-option label="已批准" value="approved" />
        <el-option label="已执行" value="executed" />
        <el-option label="已拒绝" value="rejected" />
        <el-option label="执行失败" value="failed" />
        <el-option label="执行中" value="executing" />
      </el-select>
      <div class="relative flex-1">
        <Search class="w-3 h-3 absolute left-2 top-1/2 -translate-y-1/2 text-white/30" />
        <input v-model.trim="keyword" class="glass-input !py-1 !pl-6 text-xs w-full" placeholder="搜索标题/表名" />
      </div>
      <span class="text-[11px] px-1.5 py-0.5 rounded-full bg-white/10 text-white/40 hidden sm:inline">{{ filtered.length }} 单</span>
    </div>

    <div class="flex-1 overflow-auto divide-y divide-white/5" v-loading="loading">
      <div v-for="t in filtered" :key="t.id" class="px-3 py-3 hover:bg-white/5">
        <div class="flex items-center gap-2">
          <span class="px-1.5 py-0.5 rounded text-[10px] font-medium" :class="statusBadge(t.status)">{{ statusLabel(t.status) }}</span>
          <span class="text-xs font-medium text-white/80 truncate flex-1">{{ t.title }}</span>
          <span class="text-[10px] px-1 py-0.5 rounded bg-white/10 text-white/40">{{ typeLabel(t.type) }}</span>
          <span class="text-[10px] text-white/30">{{ formatTime(t.created_at) }}</span>
        </div>
        <p class="text-[11px] font-mono text-white/45 truncate mt-1">{{ t.sql.slice(0,120) }}</p>
        <p class="text-[11px] text-white/30 mt-1 flex items-center gap-2">
          <span>表 {{ t.table }}</span><span>·</span><span>申请人 {{ t.requester }}</span><span class="hidden sm:inline">· 风险 {{ riskBadge(t.risk) }}</span>
        </p>
        <div class="flex gap-1 mt-2">
          <button v-if="t.status==='pending' && isAdmin" class="ghost-button !py-0.5 !px-2 text-[11px] text-emerald-300" @click="updateStatus(t.id,'approved')">批准</button>
          <button v-if="t.status==='pending' && isAdmin" class="ghost-button !py-0.5 !px-2 text-[11px] text-rose-300" @click="updateStatus(t.id,'rejected')">拒绝</button>
          <button v-if="t.status==='approved' && isAdmin" class="ghost-button !py-0.5 !px-2 text-[11px] text-indigo-300" @click="updateStatus(t.id,'executed')">执行 SQL</button>
          <button class="ghost-button !py-0.5 !px-2 text-[11px]" @click="copySql(t.sql)">复制 SQL</button>
          <div class="flex-1" />
          <span class="text-[10px] text-white/20">#{{ t.id }}</span>
        </div>
      </div>
      <p v-if="!filtered.length && !loading" class="text-center text-xs text-white/30 py-10">暂无工单</p>
    </div>

    <!-- 新建弹窗 -->
    <el-dialog v-model="showCreate" title="新建变更工单" width="520px" class="glass-dialog" :close-on-click-modal="false">
      <div class="space-y-3">
        <div><p class="text-xs text-white/50 mb-1">目标数据源</p><el-select v-model="form.connection_id" class="w-full" placeholder="选择执行目标"><el-option v-for="c in connections" :key="c.id" :label="c.name" :value="c.id" /></el-select></div>
        <div><p class="text-xs text-white/50 mb-1">数据库（留空使用连接默认值）</p><el-input v-model="form.database" /></div>
        <div>
          <p class="text-xs text-white/50 mb-1">标题</p>
          <el-input v-model="form.title" placeholder="例如：orders 添加索引 idx_amount" />
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <p class="text-xs text-white/50 mb-1">类型</p>
            <el-select v-model="form.type" class="w-full" popper-class="glass-popper">
              <el-option label="DDL" value="ddl" />
              <el-option label="DML" value="dml" />
              <el-option label="索引" value="index" />
            </el-select>
          </div>
          <div>
            <p class="text-xs text-white/50 mb-1">风险</p>
            <el-select v-model="form.risk" class="w-full" popper-class="glass-popper">
              <el-option label="低" value="low" />
              <el-option label="中" value="medium" />
              <el-option label="高" value="high" />
            </el-select>
          </div>
        </div>
        <div>
          <p class="text-xs text-white/50 mb-1">目标表</p>
          <el-input v-model="form.table" placeholder="例如：orders" />
        </div>
        <div>
          <p class="text-xs text-white/50 mb-1">变更 SQL</p>
          <el-input v-model="form.sql" type="textarea" :rows="4" placeholder="CREATE INDEX ... 或 ALTER TABLE ..." />
        </div>
        <div>
          <p class="text-xs text-white/50 mb-1">原因</p>
          <el-input v-model="form.reason" type="textarea" :rows="2" placeholder="说明变更背景与影响" />
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="ghost-button" @click="showCreate=false">取消</button>
          <button class="liquid-button" :disabled="!form.title.trim() || !form.sql.trim()" @click="createTicket">创建</button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, reactive, watch, onMounted } from 'vue'
import { connectionApi, type ConnectionItem } from '../../../api/datasource'
import type { TicketItem, TicketPayload } from '../../../api/ticket'
import { useUserStore } from '../../../stores/user'
import { ElMessage } from 'element-plus'
import { Plus, Search, Ticket } from 'lucide-vue-next'


const props = defineProps<{
  tickets: TicketItem[]
  create: (payload: TicketPayload) => Promise<boolean>
  prefill?: { title?: string; table?: string; sql?: string; risk?: string; reason?: string; type?: string } | null
}>()
const emit = defineEmits<{
  (e: 'create', payload: Omit<TicketItem,'id'|'status'|'requester'|'created_at'>): void
  (e: 'updateStatus', id: number, status: string): void
}>()

const loading = ref(false)
const connections=ref<ConnectionItem[]>([])
const isAdmin=computed(()=>useUserStore().role==='admin')
onMounted(async()=>{try{connections.value=(await connectionApi.list()).items.filter(c=>c.type!=='redis')}catch{}})
const statusFilter = ref('')
const keyword = ref('')
const showCreate = ref(false)
const form = reactive({ title: '', type: 'ddl', table: '', sql: '', risk: 'low', reason: '', connection_id: undefined as number|undefined, database: '' })
watch(() => props.prefill, (v) => {
  if (!v) return
  if (v.title) form.title = v.title
  if (v.table) form.table = v.table
  if (v.sql) form.sql = v.sql
  if (v.risk) form.risk = v.risk as any
  if ((v as any).type) form.type = (v as any).type as any
  if (v.reason) form.reason = v.reason
  showCreate.value = true
}, { immediate: true, deep: true })

const filtered = computed(() => {
  let out = [...props.tickets]
  if (statusFilter.value) out = out.filter(t => t.status === statusFilter.value)
  if (keyword.value) {
    const kw = keyword.value.toLowerCase()
    out = out.filter(t => t.title.toLowerCase().includes(kw) || t.table.toLowerCase().includes(kw) || t.sql.toLowerCase().includes(kw))
  }
  return out
})
const pendingCount = computed(() => props.tickets.filter(t=>t.status==='pending').length)

function statusBadge(s: string) {
  return s==='pending' ? 'bg-amber-500/15 text-amber-300' : s==='approved' ? 'bg-emerald-500/15 text-emerald-300' : s==='executed' ? 'bg-indigo-500/15 text-indigo-300' : 'bg-rose-500/15 text-rose-300'
}
function statusLabel(s: string) {
  return s==='pending' ? '待审批' : s==='approved' ? '已批准' : s==='executed' ? '已执行' : s==='failed' ? '执行失败' : s==='executing' ? '执行中' : '已拒绝'
}
function typeLabel(t: string) {
  return t==='ddl' ? 'DDL' : t==='dml' ? 'DML' : '索引'
}
function riskBadge(r: string) {
  return r==='high' ? '高' : r==='medium' ? '中' : '低'
}
function formatTime(s: string) {
  return new Date(s).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
function updateStatus(id:number, status:string) {
  emit('updateStatus', id, status)
}
async function createTicket() {
 if(!form.title.trim()||!form.sql.trim()||!form.connection_id||!form.table.trim()){ElMessage.warning('标题、SQL、表名和数据源必填');return}
 const ok=await props.create({...form,type:form.type as TicketPayload['type'],risk:form.risk as TicketPayload['risk']})
 if(ok){showCreate.value=false;form.title='';form.sql='';form.reason=''}
}

function copySql(sql: string) {
  navigator.clipboard.writeText(sql).then(()=> ElMessage.success('已复制'))
}
</script>
