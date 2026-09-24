<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-white/[0.02]">
      <div class="w-6 h-6 rounded-lg bg-violet-500/20 border border-violet-400/30 flex items-center justify-center">
        <ScrollText class="w-3.5 h-3.5 text-violet-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">查询审计</p>
        <p class="text-[10px] text-white/40">留痕 · 溯源 · 合规</p>
      </div>
      <span class="text-[11px] px-1.5 py-0.5 rounded-full bg-white/10 text-white/50">{{ total }} 条</span>
      <button class="ghost-button !py-1 !px-2.5 text-xs flex items-center gap-1" @click="reload">
        <RefreshCw class="w-3 h-3" :class="{ 'animate-spin': loading }" /> 刷新
      </button>
      <button class="ghost-button !py-1 !px-2 text-xs" @click="exportCsv">导出</button>
    </div>

    <div class="px-3 py-2 border-b border-white/10 flex flex-wrap gap-2 items-center bg-black/20 shrink-0">
      <el-select v-model="filters.action" size="small" class="w-28" popper-class="glass-popper" @change="reload">
        <el-option label="全部动作" value="" />
        <el-option label="执行 SQL" value="QUERY" />
        <el-option label="登录" value="LOGIN" />
        <el-option label="测试连接" value="TEST" />
      </el-select>
      <el-select v-model="filters.status" size="small" class="w-24" popper-class="glass-popper" @change="reload">
        <el-option label="全部" value="" />
        <el-option label="成功" value="success" />
        <el-option label="失败" value="failed" />
      </el-select>
      <div class="relative flex-1 min-w-[120px]">
        <Search class="w-3 h-3 absolute left-2 top-1/2 -translate-y-1/2 text-white/30" />
        <input v-model.trim="filters.username" class="glass-input !py-1 !pl-6 text-xs w-full" placeholder="用户名" @keydown.enter="reload" />
      </div>
      <span class="hidden sm:inline text-[10px] text-white/25 ml-auto">审计面向全站，含查询与连接操作</span>
    </div>

    <div class="flex-1 overflow-auto" v-loading="loading">
      <div v-for="a in logs" :key="a.id" class="px-3 py-2.5 border-b border-white/5 hover:bg-white/5 flex items-start gap-3">
        <span class="w-6 h-6 rounded-full bg-indigo-400/15 text-indigo-200 text-[10px] flex items-center justify-center shrink-0 mt-0.5">{{ (a.username || '?').slice(0,1).toUpperCase() }}</span>
        <div class="flex-1 min-w-0">
          <div class="flex flex-wrap items-center gap-1.5 text-xs">
            <span class="text-white/70">{{ a.username || '匿名' }}</span>
            <span class="px-1.5 py-0.5 rounded-full text-[10px] bg-white/10 text-white/60">{{ a.action }}</span>
            <span class="text-white/40 text-[11px]">{{ a.resource_type }}</span>
            <span v-if="a.resource_name" class="font-mono text-[11px] text-white/30">#{{ a.resource_name }}</span>
            <span :class="a.status===1 ? 'bg-emerald-400/15 text-emerald-300' : 'bg-rose-400/15 text-rose-300'" class="px-1.5 py-0.5 rounded-full text-[10px] ml-1">{{ a.status===1 ? '成功' : '失败' }}</span>
            <span class="text-white/30 text-[11px] ml-auto">{{ fmtTime(a.created_at) }}</span>
          </div>
          <p v-if="a.params && (a.params as any).sql" class="font-mono text-[11px] text-white/45 truncate mt-1">{{ (a.params as any).sql }}</p>
          <p v-else-if="a.error_message" class="text-[11px] text-rose-300/60 truncate mt-1">{{ a.error_message }}</p>
          <p class="text-[11px] text-white/25 mt-0.5 flex gap-2"><span class="font-mono">{{ (a.ip_address||'').replace('/32','') }}</span><span class="hidden sm:inline">{{ a.duration_ms ?? '—' }}ms</span><span class="hidden md:inline font-mono text-[10px]">{{ a.trace_id?.slice(0,8) }}</span></p>
        </div>
      </div>
      <p v-if="!logs.length && !loading" class="text-center text-xs text-white/35 py-10">暂无审计记录</p>
    </div>

    <div class="px-3 py-2 border-t border-white/10 flex items-center justify-between text-xs text-white/40 shrink-0">
      <span>共 {{ total }} 条</span>
      <div class="flex items-center gap-2">
        <button class="ghost-button !py-1 !px-2 text-[11px]" :disabled="page<=1" @click="page--; reload()">上一页</button>
        <span>第 {{ page }} 页</span>
        <button class="ghost-button !py-1 !px-2 text-[11px]" :disabled="page*pageSize>=total" @click="page++; reload()">下一页</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, watch } from 'vue'
import { RefreshCw, ScrollText, Search } from 'lucide-vue-next'
import { adminApi, type AuditLog } from '../../../api/admin'

const logs = ref<AuditLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 15
const loading = ref(false)

const filters = reactive({
  action: 'QUERY',
  status: '',
  username: '',
})

async function reload() {
  loading.value = true
  try {
    const res = await adminApi.auditLogs({
      action: filters.action || undefined,
      status: filters.status || undefined,
      username: filters.username || undefined,
      days: 30,
      page: page.value,
      page_size: pageSize,
    })
    logs.value = res.items
    total.value = res.total
  } catch {
    logs.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function fmtTime(s: string) {
  return new Date(s).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
}

function exportCsv() {
  if (!logs.value.length) return
  const rows = [['time','user','action','resource','status','ip','duration'] , ...logs.value.map(a => [a.created_at, a.username, a.action, a.resource_type, a.status===1?'success':'failed', a.ip_address, String(a.duration_ms ?? '')])]
  const csv = rows.map(r => r.map(v => `"${String(v).replace(/"/g,'""')}"`).join(',')).join('\n')
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `audit_${new Date().toISOString().slice(0,10)}.csv`
  a.click()
  URL.revokeObjectURL(url)
}

watch(() => filters.action, () => { page.value=1; reload() })
onMounted(reload)
</script>
