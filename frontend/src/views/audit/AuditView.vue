<template>
  <div class="space-y-5">
    <header>
      <h1 class="text-xl font-semibold">操作审计</h1>
      <p class="text-sm text-white/45 mt-1">所有连接、查询与管理操作均不可篡改地留痕</p>
    </header>

    <!-- 筛选栏 -->
    <div class="glass-panel p-4 flex flex-wrap items-center gap-3">
      <el-date-picker
        v-model="dateRange"
        type="daterange"
        range-separator="至"
        start-placeholder="开始日期"
        end-placeholder="结束日期"
        class="!w-full sm:!w-72"
      />
      <select v-model="actionFilter" class="glass-input w-full sm:w-36 appearance-none text-sm">
        <option value="">全部操作</option>
        <option value="LOGIN">登录</option>
        <option value="QUERY">查询</option>
        <option value="CONNECT">连接</option>
        <option value="DELETE">删除</option>
      </select>
      <div class="relative w-full sm:w-64 sm:flex-1 sm:max-w-64">
        <Search class="w-4 h-4 text-white/35 absolute left-3.5 top-1/2 -translate-y-1/2" />
        <input v-model.trim="keyword" class="glass-input pl-10 text-sm" placeholder="搜索用户 / 资源名称" />
      </div>
      <button class="ghost-button flex items-center justify-center gap-2 text-sm w-full sm:w-auto sm:ml-auto" @click="todo">
        <Download class="w-4 h-4" /> 导出
      </button>
    </div>

    <!-- 审计表格 -->
    <div class="glass-panel overflow-hidden">
      <el-table :data="pagedLogs" class="audit-table" style="width: 100%">
        <el-table-column prop="createdAt" label="时间" width="180" />
        <el-table-column prop="username" label="用户" width="120" />
        <el-table-column label="操作" width="110">
          <template #default="{ row }">
            <el-tag :type="actionTagType[row.action as string]" effect="dark" round size="small">
              {{ actionLabels[row.action as string] ?? row.action }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="resourceType" label="资源类型" width="120" />
        <el-table-column prop="resourceName" label="资源名称" min-width="200" />
        <el-table-column prop="ip" label="IP 地址" width="140" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <span class="inline-flex items-center gap-1.5 text-xs">
              <span
                class="w-1.5 h-1.5 rounded-full"
                :class="row.status === 1 ? 'bg-emerald-400' : 'bg-rose-400'"
              />
              {{ row.status === 1 ? '成功' : '失败' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column prop="duration" label="耗时" width="100" />
      </el-table>
      <div class="flex flex-wrap items-center justify-between gap-2 px-4 py-3 border-t border-white/10">
        <span class="text-xs text-white/40">共 {{ filteredLogs.length }} 条记录（示例数据）</span>
        <el-pagination
          v-model:current-page="page"
          :page-size="pageSize"
          :total="filteredLogs.length"
          layout="prev, pager, next"
          background
          small
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Download, Search } from 'lucide-vue-next'

interface AuditLog {
  createdAt: string
  username: string
  action: string
  resourceType: string
  resourceName: string
  ip: string
  status: 0 | 1
  duration: string
}

const actionLabels: Record<string, string> = {
  LOGIN: '登录',
  QUERY: '查询',
  CONNECT: '连接',
  DELETE: '删除',
}
const actionTagType: Record<string, 'success' | 'warning' | 'info' | 'danger' | 'primary'> = {
  LOGIN: 'primary',
  QUERY: 'success',
  CONNECT: 'info',
  DELETE: 'danger',
}

const logs = ref<AuditLog[]>([
  { createdAt: '2026-09-14 10:32:11', username: 'admin', action: 'QUERY', resourceType: 'TABLE', resourceName: 'sales-prod-mysql / orders', ip: '10.10.2.31', status: 1, duration: '45ms' },
  { createdAt: '2026-09-14 10:18:02', username: 'alice', action: 'CONNECT', resourceType: 'CONNECTION', resourceName: 'user-center-pg', ip: '10.10.2.45', status: 1, duration: '320ms' },
  { createdAt: '2026-09-14 09:58:47', username: 'bob', action: 'LOGIN', resourceType: 'SESSION', resourceName: 'Web 控制台登录', ip: '10.10.2.52', status: 1, duration: '120ms' },
  { createdAt: '2026-09-14 09:41:19', username: 'bob', action: 'QUERY', resourceType: 'TABLE', resourceName: 'analytics-mysql / events', ip: '10.10.2.52', status: 0, duration: '1.8s' },
  { createdAt: '2026-09-14 09:20:33', username: 'admin', action: 'DELETE', resourceType: 'CONNECTION', resourceName: 'old-redis-test', ip: '10.10.2.31', status: 1, duration: '88ms' },
  { createdAt: '2026-09-14 08:55:07', username: 'alice', action: 'QUERY', resourceType: 'TABLE', resourceName: 'billing-postgres / invoices', ip: '10.10.2.45', status: 1, duration: '210ms' },
  { createdAt: '2026-09-13 22:14:50', username: 'system', action: 'CONNECT', resourceType: 'SSH_TUNNEL', resourceName: 'bastion-hk-01', ip: '10.10.1.2', status: 1, duration: '940ms' },
  { createdAt: '2026-09-13 21:02:11', username: 'admin', action: 'LOGIN', resourceType: 'SESSION', resourceName: 'Web 控制台登录', ip: '10.10.2.31', status: 1, duration: '96ms' },
  { createdAt: '2026-09-13 18:40:36', username: 'charlie', action: 'LOGIN', resourceType: 'SESSION', resourceName: 'Web 控制台登录', ip: '203.0.113.8', status: 0, duration: '15ms' },
  { createdAt: '2026-09-13 17:12:04', username: 'alice', action: 'QUERY', resourceType: 'TABLE', resourceName: 'user-center-pg / users', ip: '10.10.2.45', status: 1, duration: '63ms' },
])

const dateRange = ref<null | [Date, Date]>(null)
const actionFilter = ref('')
const keyword = ref('')
const page = ref(1)
const pageSize = 8

const filteredLogs = computed(() =>
  logs.value.filter((row) => {
    const matchAction = !actionFilter.value || row.action === actionFilter.value
    const kw = keyword.value.toLowerCase()
    const matchKw = !kw || row.username.includes(kw) || row.resourceName.toLowerCase().includes(kw)
    return matchAction && matchKw
  }),
)
const pagedLogs = computed(() =>
  filteredLogs.value.slice((page.value - 1) * pageSize, page.value * pageSize),
)

function todo() {
  ElMessage.info('审计导出功能开发中')
}
</script>

<style scoped>
.audit-table {
  background: transparent;
  --el-table-bg-color: transparent;
  --el-table-tr-bg-color: transparent;
  --el-table-header-bg-color: rgba(255, 255, 255, 0.04);
  --el-table-row-hover-bg-color: rgba(255, 255, 255, 0.05);
  --el-table-border-color: rgba(255, 255, 255, 0.08);
  --el-table-text-color: rgba(255, 255, 255, 0.75);
  --el-table-header-text-color: rgba(255, 255, 255, 0.55);
}
</style>
