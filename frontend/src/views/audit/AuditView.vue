<template>
  <div class="space-y-5">
    <header>
      <h1 class="text-xl font-semibold">操作审计</h1>
      <p class="text-sm text-white/45 mt-1">所有连接、查询与管理操作均不可篡改地留痕</p>
    </header>

    <!-- 筛选栏（每项 shrink-0，空间不足时整体换行，绝不互相压缩） -->
    <div class="glass-panel p-4 flex flex-wrap items-center gap-3">
      <el-date-picker
        v-model="dateRange"
        type="daterange"
        range-separator="至"
        start-placeholder="开始日期"
        end-placeholder="结束日期"
        aria-label="日期范围筛选"
        class="!w-full sm:!w-72 shrink-0"
      />
      <div class="w-full sm:w-36 shrink-0">
        <el-select
          v-model="actionFilter"
          aria-label="按操作类型筛选"
          class="w-full"
          popper-class="glass-popper"
        >
          <el-option label="全部操作" value="all" />
          <el-option label="登录" value="LOGIN" />
          <el-option label="查询" value="QUERY" />
          <el-option label="连接" value="CONNECT" />
          <el-option label="删除" value="DELETE" />
        </el-select>
      </div>
      <div class="relative w-full sm:w-60 md:w-64 shrink-0">
        <Search class="w-4 h-4 text-white/40 absolute left-3.5 top-1/2 -translate-y-1/2" />
        <input
          v-model.trim="keyword"
          aria-label="按用户或资源名称搜索"
          class="glass-input pl-10 text-sm"
          placeholder="搜索用户 / 资源名称"
        />
      </div>
      <button
        class="ghost-button flex items-center justify-center gap-2 text-sm w-full sm:w-auto sm:ml-auto shrink-0"
        @click="todo"
      >
        <Download class="w-4 h-4" /> 导出
      </button>
    </div>

    <!-- 审计表格（窄屏自动隐藏次要列，保证状态等关键信息无需横滑即可见） -->
    <div class="glass-panel overflow-hidden">
      <el-table :data="pagedLogs" class="audit-table" style="width: 100%">
        <el-table-column label="时间" :min-width="vp.sm ? 170 : 96">
          <template #default="{ row }">
            {{ vp.sm ? row.createdAt : shortTime(row.createdAt) }}
          </template>
        </el-table-column>
        <el-table-column v-if="vp.sm" prop="username" label="用户" :min-width="vp.lg ? 110 : 80" />
        <el-table-column label="操作" :width="vp.sm ? 100 : 68">
          <template #default="{ row }">
            <el-tag :type="actionTagType[row.action as string]" effect="dark" round size="small">
              {{ actionLabels[row.action as string] ?? row.action }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          v-if="vp.sm"
          prop="resourceType"
          label="资源类型"
          min-width="120"
          show-overflow-tooltip
        />
        <el-table-column
          prop="resourceName"
          label="资源名称"
          :min-width="vp.sm ? 150 : 84"
          show-overflow-tooltip
        />
        <el-table-column v-if="vp.lg" prop="ip" label="IP 地址" min-width="130" />
        <el-table-column label="状态" :width="vp.sm ? 96 : 68">
          <template #default="{ row }">
            <span class="inline-flex items-center gap-1.5 text-xs whitespace-nowrap">
              <span
                class="w-1.5 h-1.5 rounded-full shrink-0"
                :class="row.status === 1 ? 'bg-emerald-400' : 'bg-rose-400'"
              />
              {{ row.status === 1 ? '成功' : '失败' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column v-if="vp.xl" prop="duration" label="耗时" width="90" />
      </el-table>
      <div class="flex flex-wrap items-center justify-between gap-2 px-4 py-3 border-t border-white/10">
        <span class="text-xs text-white/40">共 {{ filteredLogs.length }} 条记录（示例数据）</span>
        <el-pagination
          v-model:current-page="page"
          :page-size="pageSize"
          :total="filteredLogs.length"
          :layout="vp.sm ? 'prev, pager, next' : 'prev, next'"
          background
          small
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Download, Search } from 'lucide-vue-next'

/** 响应式断点（与 Tailwind 保持一致） */
const vp = reactive({ sm: false, md: false, lg: false, xl: false })
function syncViewport() {
  const w = window.innerWidth
  vp.sm = w >= 640
  vp.md = w >= 768
  vp.lg = w >= 1024
  vp.xl = w >= 1280
}
onMounted(() => {
  syncViewport()
  window.addEventListener('resize', syncViewport)
})
onBeforeUnmount(() => window.removeEventListener('resize', syncViewport))

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
const actionFilter = ref('all')
const keyword = ref('')
const page = ref(1)
const pageSize = 8

const filteredLogs = computed(() =>
  logs.value.filter((row) => {
    const matchAction = actionFilter.value === 'all' || row.action === actionFilter.value
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

/** 窄屏时间格式：2026-09-14 10:32:11 -> 09-14 10:32 */
function shortTime(full: string): string {
  const m = full.match(/^\d{4}-(\d{2}-\d{2}) (\d{2}:\d{2})/)
  return m ? `${m[1]} ${m[2]}` : full
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
