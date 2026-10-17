<template>
  <div class="space-y-5">
    <header>
      <h1 class="text-xl font-semibold">操作审计</h1>
      <p class="text-sm text-white/45 mt-1">关联平台用户、配置的数据库账号与数据源修订；查看执行回执区分失败和结果待确认。</p>
    </header>

    <div v-if="delivery || deliveryError" role="status" class="glass-panel px-4 py-3 text-sm" :class="deliveryError || !delivery?.ready || delivery?.last_error ? 'text-amber-200' : 'text-white/60'">
      <p v-if="deliveryError">暂时无法读取审计写入状态，请稍后刷新。</p>
      <template v-else-if="delivery">
      <p>{{ !delivery.ready ? '审计存储不可用或队列已满，新的审计操作已暂停。' : delivery.pending ? '审计记录已保存，正在等待落库。' : '审计写入正常' }}</p>
      <p class="text-xs mt-1">待写入 {{ delivery.pending }} / {{ delivery.capacity }}<span v-if="delivery.oldest"> · 最早待写入 {{ fmtTime(delivery.oldest) }}</span><span v-if="delivery.last_delivered"> · 最近落库 {{ fmtTime(delivery.last_delivered) }}</span> · 本次启动写入失败 {{ delivery.delivery_failures }} 次 · 存储失败 {{ delivery.storage_failures }} 次</p>
      </template>
    </div>

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
      <input v-model.trim="filters.database_account" class="glass-input !w-full sm:!w-48" aria-label="按数据库账号搜索" placeholder="配置的数据库账号" @keyup.enter="page = 1; reload()" />
      <el-select v-model="filters.outcome" class="!w-full sm:!w-44" aria-label="按执行状态筛选" @change="page = 1; reload()"><el-option label="全部执行状态" value="" /><el-option v-for="(label, value) in outcomes" :key="value" :label="label" :value="value" /></el-select>
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
              <th class="font-medium px-5 py-3">数据库身份与修订</th>
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
              <td class="px-5 py-3 text-xs text-white/65 max-w-64 break-all"><p>{{ a.context?.database_account || '—' }}</p><p v-if="a.context?.definition_revision_id" class="text-white/45">定义 #{{ a.context.definition_revision_id }} · 修订 {{ a.context.revision || '—' }}</p><p v-if="a.context?.driver" class="text-white/45">{{ a.context.driver }}</p><details v-if="a.context?.source_family_id || a.context?.execution_request_id" class="mt-1"><summary class="cursor-pointer">关联编号</summary><p>{{ a.context.source_family_id }}</p><p>{{ a.context.execution_request_id }}</p></details></td>
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
                  {{ outcomes[a.context?.outcome || ''] || (a.status === 1 ? '成功' : '失败') }}{{ a.context?.response_failed && a.context?.outcome !== 'unknown' ? '（响应未确认）' : '' }}
                </span>
                <p v-if="a.error_message" class="text-[11px] text-rose-300/70 mt-1 max-w-[200px] truncate" :title="a.error_message">
                  {{ a.error_message }}
                </p>
                <details class="text-xs text-white/45 mt-1"><summary class="cursor-pointer">请求详情</summary><p>{{ a.context?.route }}</p><p v-if="a.context?.http_status">HTTP {{ a.context.http_status }}</p><p v-if="a.context?.auth_failure">{{ denialLabels[a.context.auth_failure] || a.context.auth_failure }}</p><p v-if="a.context?.transaction_id" class="break-all">事务 {{ a.context.transaction_id }}</p><p v-if="a.context?.job_id" class="break-all">任务 {{ a.context.job_id }}</p><p v-if="a.context?.export_id">导出 #{{ a.context.export_id }}</p><p class="break-all">事件 {{ a.event_id || '历史记录' }}</p></details>
              </td>
              <td class="px-5 py-3.5 text-right text-white/45 text-xs whitespace-nowrap">{{ a.duration_ms ?? '—' }} ms</td>
            </tr>
            <tr v-if="!logs.length && !loading">
              <td colspan="8" class="px-5 py-12 text-center text-white/40 text-sm">所选条件下暂无审计记录</td>
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
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { RefreshCw, Search } from 'lucide-vue-next'
import { adminApi, type AuditLog, type AuditDelivery } from '../../api/admin'

const logs = ref<AuditLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const delivery = ref<AuditDelivery>()
const deliveryError = ref(false)
const denialLabels: Record<string,string> = {missing_token:'缺少认证令牌',invalid_token:'认证令牌无效或过期',user_unavailable:'用户不可用',invalid_session:'会话已失效',session_unavailable:'会话服务不可用',insufficient_role:'角色权限不足'}

const filters = reactive({
  days: 7,
  resource_type: '',
  action: '',
  status: '',
  username: '', database_account: '', outcome: '',
})

const outcomes: Record<string, string> = {begun: '事务已开启', committed: '事务已提交', rolled_back: '事务已回滚', cancel_requested: '已请求取消', already_finished: '任务已结束', export_pending: '导出待审批', export_approved: '导出已批准', export_rejected: '导出已拒绝', download_response: '已返回下载响应', succeeded: '执行成功', failed: '执行失败', unknown: '结果待确认', rejected: '请求拒绝', duplicate: '重复请求', conflict: '请求冲突', completed: '操作完成', access_updated: '范围已更新', profile_updated: '资料已更新', revision_published: '修订已发布', revision_status_updated: '修订状态已更新'}
const actionLabels: Record<string, string> = {
  REFRESH: '刷新会话', LOGOUT: '退出登录', AUTH_DENIED: '访问拒绝',
  TRANSACTION_BEGIN: '开启事务', TRANSACTION_COMMIT: '提交事务', TRANSACTION_ROLLBACK: '回滚事务',
  QUERY_CANCEL: '取消查询', EXPORT_CREATE: '申请导出', EXPORT_REVIEW: '审批导出', EXPORT_DOWNLOAD: '下载导出',
  LOGIN: '登录',
  CHANGE_PASSWORD: '修改密码',
  CREATE: '新建',
  UPDATE: '更新',
  DELETE: '删除',
  TEST: '测试连接',
  QUERY: '执行 SQL',
}
const resourceLabels: Record<string, string> = {
  TRANSACTION: '事务', QUERY_JOB: '查询任务', EXPORT: '数据导出',
  AUTH: '认证',
  CONNECTION: '数据源',
  SSH_TUNNEL: 'SSH 隧道',
  USER: '用户',
  SQL: 'SQL', 'DATASOURCE-DEFINITIONS': '数据源定义', 'USER-GROUPS': '用户组',
  QUERY_HISTORY: '查询历史',
}

async function reload() {
  loading.value = true
  try {
    const [records,state] = await Promise.allSettled([adminApi.auditLogs({
      days: filters.days,
      resource_type: filters.resource_type,
      action: filters.action,
      status: filters.status,
      username: filters.username, database_account: filters.database_account, outcome: filters.outcome,
      page: page.value,
      page_size: pageSize,
    }), adminApi.auditDelivery()])
    deliveryError.value = state.status === 'rejected'
    if(state.status === 'fulfilled') delivery.value = state.value
    if(records.status === 'fulfilled') {
      logs.value = records.value.items
      total.value = records.value.total
    }
  } finally {
    loading.value = false
  }
}
let deliveryTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void reload()
  deliveryTimer = setInterval(() => { void adminApi.auditDelivery().then(value => {delivery.value=value; deliveryError.value=false}).catch(() => {deliveryError.value=true}) },10000)
})
onUnmounted(() => { if(deliveryTimer) clearInterval(deliveryTimer) })

function fmtTime(s: string) {
  return new Date(s).toLocaleString('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  })
}
function cleanIP(ip: string) {
  return (ip || '').replace('/32', '')
}
</script>
