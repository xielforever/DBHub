<template>
  <div class="space-y-5">
    <header>
      <h1 class="text-xl font-semibold">操作审计</h1>
      <p class="text-sm text-white/45 mt-1">所有登录、连接管理与 SQL 执行均自动留痕，不可篡改</p>
    </header>

    <!-- 筛选栏 -->
    <div class="glass-panel p-4 flex flex-wrap items-center gap-3">
      <div class="w-full sm:w-36 shrink-0">
        <el-select v-model="filters.days" aria-label="时间范围" class="w-full" popper-class="glass-popper" @change="reload">
          <el-option label="近 1 天" :value="1" />
          <el-option label="近 7 天" :value="7" />
          <el-option label="近 30 天" :value="30" />
          <el-option label="近 90 天" :value="90" />
        </el-select>
      </div>
      <div class="w-full sm:w-36 shrink-0">
        <el-select v-model="filters.resource_type" placeholder="全部资源" aria-label="按资源类型筛选" class="w-full" popper-class="glass-popper" @change="reload">
          <el-option label="全部资源" value="" />
          <el-option v-for="(label, key) in resourceLabels" :key="key" :label="label" :value="key" />
        </el-select>
      </div>
      <div class="w-full sm:w-32 shrink-0">
        <el-select v-model="filters.action" placeholder="全部操作" aria-label="按操作类型筛选" class="w-full" popper-class="glass-popper" @change="reload">
          <el-option label="全部操作" value="" />
          <el-option v-for="(label, key) in actionLabels" :key="key" :label="label" :value="key" />
        </el-select>
      </div>
      <div class="w-full sm:w-28 shrink-0">
        <el-select v-model="filters.status" placeholder="全部结果" aria-label="按结果筛选" class="w-full" popper-class="glass-popper" @change="reload">
          <el-option label="全部结果" value="" />
          <el-option label="成功" value="success" />
          <el-option label="失败" value="failed" />
        </el-select>
      </div>
      <div class="relative w-full sm:w-56 shrink-0">
        <Search class="w-4 h-4 text-white/40 absolute left-3.5 top-1/2 -translate-y-1/2" />
        <input
          v-model.trim="filters.username"
          aria-label="按用户名搜索"
          class="glass-input pl-10 text-sm"
          placeholder="搜索操作用户"
          @keyup.enter="reload"
        />
      </div>
      <button class="ghost-button flex items-center gap-2 text-sm w-full sm:w-auto sm:ml-auto shrink-0" @click="reload">
        <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" /> 刷新
      </button>
    </div>

    <!-- 审计表 -->
    <div class="glass-panel overflow-hidden" v-loading="loading">
      <div class="overflow-x-auto">
        <table class="w-full text-sm min-w-[820px]">
          <thead>
            <tr class="text-left text-white/40 text-xs border-b border-white/10">
              <th class="font-medium px-5 py-3 whitespace-nowrap">时间</th>
              <th class="font-medium px-5 py-3">用户</th>
              <th class="font-medium px-5 py-3">操作</th>
              <th class="font-medium px-5 py-3">资源</th>
              <th class="font-medium px-5 py-3 hidden md:table-cell">来源 IP</th>
              <th class="font-medium px-5 py-3">结果</th>
              <th class="font-medium px-5 py-3 text-right whitespace-nowrap">耗时</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in logs" :key="a.id" class="border-b border-white/5 last:border-0 hover:bg-white/5 align-top">
              <td class="px-5 py-3 text-white/60 whitespace-nowrap text-xs">{{ fmtTime(a.created_at) }}</td>
              <td class="px-5 py-3">
                <span class="flex items-center gap-2">
                  <span class="w-6 h-6 rounded-full bg-indigo-400/20 text-indigo-200 text-[10px] flex items-center justify-center shrink-0">
                    {{ (a.username || '?').slice(0, 1).toUpperCase() }}
                  </span>
                  {{ a.username || '匿名' }}
                </span>
              </td>
              <td class="px-5 py-3">
                <span class="px-2 py-0.5 rounded-full text-xs bg-white/8 text-white/75 whitespace-nowrap">
                  {{ actionLabels[a.action] || a.action }}
                </span>
              </td>
              <td class="px-5 py-3 text-white/65">
                <span class="whitespace-nowrap">{{ resourceLabels[a.resource_type] || a.resource_type }}</span>
                <span v-if="a.resource_name" class="text-white/40 ml-1 font-mono text-xs">#{{ a.resource_name }}</span>
              </td>
              <td class="px-5 py-3 text-white/50 text-xs hidden md:table-cell font-mono">{{ cleanIP(a.ip_address) }}</td>
              <td class="px-5 py-3">
                <span
                  class="px-2 py-0.5 rounded-full text-xs whitespace-nowrap"
                  :class="a.status === 1 ? 'bg-emerald-400/15 text-emerald-300' : 'bg-rose-400/15 text-rose-300'"
                >
                  {{ a.status === 1 ? '成功' : '失败' }}
                </span>
                <p v-if="a.error_message" class="text-[11px] text-rose-300/70 mt-1 max-w-[200px] truncate" :title="a.error_message">
                  {{ a.error_message }}
                </p>
              </td>
              <td class="px-5 py-3.5 text-right text-white/45 text-xs whitespace-nowrap">{{ a.duration_ms ?? '—' }} ms</td>
            </tr>
            <tr v-if="!logs.length && !loading">
              <td colspan="7" class="px-5 py-12 text-center text-white/40 text-sm">所选条件下暂无审计记录</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="flex items-center justify-between px-5 py-3 border-t border-white/10 text-xs text-white/50">
        <span>共 {{ total }} 条记录</span>
        <div class="flex items-center gap-2">
          <button class="ghost-button !py-1 !px-3" :disabled="page <= 1" @click="page--; reload()">上一页</button>
          <span>第 {{ page }} 页</span>
          <button class="ghost-button !py-1 !px-3" :disabled="page * pageSize >= total" @click="page++; reload()">下一页</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { RefreshCw, Search } from 'lucide-vue-next'
import { adminApi, type AuditLog } from '../../api/admin'

const logs = ref<AuditLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)

const filters = reactive({
  days: 7,
  resource_type: '',
  action: '',
  status: '',
  username: '',
})

const actionLabels: Record<string, string> = {
  LOGIN: '登录',
  CHANGE_PASSWORD: '修改密码',
  CREATE: '新建',
  UPDATE: '更新',
  DELETE: '删除',
  TEST: '测试连接',
  QUERY: '执行 SQL',
}
const resourceLabels: Record<string, string> = {
  AUTH: '认证',
  CONNECTION: '数据源',
  SSH_TUNNEL: 'SSH 隧道',
  USER: '用户',
  SQL: 'SQL',
  QUERY_HISTORY: '查询历史',
}

async function reload() {
  loading.value = true
  try {
    const res = await adminApi.auditLogs({
      days: filters.days,
      resource_type: filters.resource_type,
      action: filters.action,
      status: filters.status,
      username: filters.username,
      page: page.value,
      page_size: pageSize,
    })
    logs.value = res.items
    total.value = res.total
  } finally {
    loading.value = false
  }
}
onMounted(reload)

function fmtTime(s: string) {
  return new Date(s).toLocaleString('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  })
}
function cleanIP(ip: string) {
  return (ip || '').replace('/32', '')
}
</script>
