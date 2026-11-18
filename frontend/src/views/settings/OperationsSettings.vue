<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { useRoute } from 'vue-router'
import { automationApi, type OperationsSnapshot } from '../../api/automation'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'

const route = useRoute()
const snapshot = ref<OperationsSnapshot>()
const error = ref('')
const loading = ref(false)
const checkedAt = ref('')
const refreshButton = ref<HTMLButtonElement>()
const controller = new AbortController()
let timer: ReturnType<typeof setInterval> | undefined

const alertLabels: Record<string, string> = {
  audit_delivery: '审计待写队列异常', retention_failed: '历史留存维护失败',
  backup_failed: '元数据库自动备份失败', backup_stale: '元数据库备份超期',
}
const operationLabels: Record<string, string> = {
  backup: '元数据库自动备份', retention: '历史留存维护', source_worker: '数据源后台任务',
}
const statusLabels: Record<string, string> = { succeeded: '成功', running: '执行中', failed: '失败' }
const retentionFields = [
  ['audit', '审计'], ['query_history', '查询历史'], ['source_checks', '检查历史'],
  ['source_tasks', '任务历史'], ['read_notifications', '已读通知'],
] as const
const activeAlerts = computed(() => snapshot.value?.alerts.filter(item => item.active) ?? [])

function formatTime(value: string | null) {
  return value ? new Date(value).toLocaleString('zh-CN') : '未记录'
}

function retention(days?: number) {
  if (days === undefined) return '未记录'
  return days === 0 ? '自动清理关闭' : '保留 ' + days + ' 天'
}

function formatBytes(value: unknown) {
  return typeof value === 'number' ? (value / 1048576).toFixed(2) + ' MiB' : '大小未记录'
}

function count(value: unknown) {
  return typeof value === 'number' ? value + ' 条' : '未记录'
}

async function load() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const result = await automationApi.operations(controller.signal)
    if (controller.signal.aborted) return
    snapshot.value = result
    checkedAt.value = new Date().toLocaleString('zh-CN')
  } catch (failure) {
    if (controller.signal.aborted) return
    snapshot.value = undefined
    checkedAt.value = ''
    error.value = failure instanceof Error ? failure.message : '读取运行与备份状态失败。'
  } finally {
    if (!controller.signal.aborted) loading.value = false
  }
}

function retry(event: MouseEvent) {
  if (document.activeElement === event.currentTarget) refreshButton.value?.focus()
  void load()
}

onMounted(() => {
  void load()
  if (route.query.section === 'operations') document.getElementById('operations')?.scrollIntoView()
  timer = setInterval(() => {
    if (!error.value && !document.hidden && navigator.onLine) void load()
  }, 30000)
})
onScopeDispose(() => {
  controller.abort()
  clearInterval(timer)
})
</script>

<template>
  <section id="operations" class="glass-panel space-y-4 ui-card-padding" aria-labelledby="platform-operations-heading">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 id="platform-operations-heading" class="text-lg font-semibold">运行与备份</h2>
        <p class="mt-2 text-sm text-muted">查看后台任务、备份监测和部署中的留存设置。</p>
      </div>
      <button ref="refreshButton" type="button" class="ghost-button ui-control-target aria-disabled:opacity-50" :aria-disabled="loading || undefined" @click="load">刷新状态</button>
    </div>
    <LoadingState v-if="loading && !snapshot" label="正在读取运行与备份状态…" variant="detail" />
    <PageState v-else-if="error" heading="h3" title="读取运行状态失败" :description="error" tone="danger">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="retry">重试读取状态</button></template>
    </PageState>
    <template v-else-if="snapshot">
      <p v-if="loading" role="status" class="text-sm text-muted">正在更新运行状态，以下为上次结果。上次读取时间 {{ checkedAt }}</p>
      <section class="border-t border-divider pt-3 space-y-3" aria-labelledby="platform-operations-summary" :aria-busy="loading">
        <h3 id="platform-operations-summary" class="font-semibold">运行概况</h3>
        <dl class="grid gap-3 sm:grid-cols-2 text-sm">
          <div class="border border-divider p-3 space-y-1">
            <dt class="text-muted">审计待写</dt>
            <dd v-if="snapshot.audit">{{ snapshot.audit.pending }} / {{ snapshot.audit.capacity }} · {{ snapshot.audit.ready ? '队列可接收' : '队列不可接收' }}</dd>
            <dd v-else>队列状态未记录</dd>
          </div>
          <div class="border border-divider p-3 space-y-1">
            <dt class="text-muted">备份监测</dt>
            <dd>{{ snapshot.config.backup_expected_hours === 0 ? '未启用' : snapshot.config.backup_expected_hours == null ? '未记录' : '预期间隔 ' + snapshot.config.backup_expected_hours + ' 小时' }}</dd>
          </div>
        </dl>
        <div v-if="activeAlerts.length" role="alert" class="border border-warning/40 p-3 space-y-1">
          <h4 class="font-medium text-warning">活动告警</h4>
          <p v-for="alert in activeAlerts" :key="alert.code" class="text-sm break-words">{{ alertLabels[alert.code] || alert.code }} · {{ formatTime(alert.changed_at) }}</p>
        </div>
        <p v-else class="text-sm text-muted">当前快照无活动告警。</p>
      </section>
      <section class="border-t border-divider pt-3 space-y-3" aria-labelledby="platform-operations-receipts" :aria-busy="loading">
        <h3 id="platform-operations-receipts" class="font-semibold">后台任务回执</h3>
        <div v-if="snapshot.states.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="后台任务回执表格">
          <table class="editorial-table min-w-[610px]" aria-label="后台任务回执">
            <thead><tr><th scope="col">任务</th><th scope="col">状态</th><th scope="col">最近成功</th><th scope="col">回执详情</th></tr></thead>
            <tbody>
              <tr v-for="state in snapshot.states" :key="state.name">
                <td>{{ operationLabels[state.name] || state.name }}</td>
                <td class="whitespace-nowrap" :class="state.status === 'failed' ? 'text-warning' : 'text-muted'">{{ statusLabels[state.status] || '未知' }}</td>
                <td>{{ formatTime(state.last_success_at) }}</td>
                <td>
                  <template v-if="state.name === 'backup'">
                    <template v-if="state.status === 'succeeded' && typeof state.details.file === 'string' && state.details.file.trim()">
                      <span class="break-all">{{ state.details.file }}</span> · {{ formatBytes(state.details.bytes) }}
                    </template>
                    <span v-else-if="state.status === 'running'">正在生成归档</span>
                    <span v-else-if="state.status === 'failed'">本次未确认归档</span>
                    <span v-else>归档未记录</span>
                  </template>
                  <details v-else-if="state.name === 'retention'">
                    <summary class="cursor-pointer ui-control-target aria-disabled:opacity-50" :aria-disabled="loading || undefined" @click="loading && $event.preventDefault()">查看清理数量</summary>
                    <p v-for="[key, label] in retentionFields" :key="key" class="text-sm">{{ label }} {{ count(state.details[key]) }}</p>
                  </details>
                  <span v-else>—</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-else class="text-sm text-muted">暂无后台任务回执。</p>
      </section>
      <section class="border-t border-divider pt-3 space-y-3" aria-labelledby="platform-operations-retention" :aria-busy="loading">
        <h3 id="platform-operations-retention" class="font-semibold">部署留存设置</h3>
        <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
          <dt class="text-muted">审计</dt><dd>{{ retention(snapshot.config.audit_retention_days) }}</dd>
          <dt class="text-muted">查询历史</dt><dd>{{ retention(snapshot.config.history_retention_days) }}</dd>
          <dt class="text-muted">已读通知</dt><dd>{{ retention(snapshot.config.read_notification_retention_days) }}</dd>
        </dl>
        <p class="text-sm text-muted">留存周期和备份服务由部署配置管理。每小时每类最多清理 500 条，未知执行结果及版本审批历史保留。</p>
      </section>
      <p v-if="!loading" class="text-sm text-muted">读取时间 {{ checkedAt }}</p>
    </template>
  </section>
</template>
