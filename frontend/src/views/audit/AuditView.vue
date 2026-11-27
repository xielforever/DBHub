<script setup lang="ts">
import { nextTick, onMounted, onScopeDispose, reactive, ref, useId } from 'vue'
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { adminApi, type AuditLog, type AuditDelivery } from '../../api/admin'
import { pageTitles } from '../../navigation'
import request, { ApiRequestError } from '../../utils/request'
import { revealTableAction, scrollTableByKeyboard } from '../../utils/table-focus'

const id = useId()
const logs = ref<AuditLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(true)
const listError = ref('')
const denied = ref(false)
const isPlatform = ref(false)
const platformLog = ref(false)
const delivery = ref<AuditDelivery>()
const deliveryError = ref(false)
const selectedLog = ref<AuditLog>()
const searchButton = ref<HTMLButtonElement>()
const retryButton = ref<HTMLButtonElement>()
const previousPage = ref<HTMLButtonElement>()
const nextPage = ref<HTMLButtonElement>()
const resultsTable = ref<HTMLElement>()
const detailHeading = ref<HTMLHeadingElement>()
let detailTrigger: HTMLElement | undefined
let listRequest: AbortController | undefined
let deliveryRequest: AbortController | undefined
let deliveryTimer: ReturnType<typeof setInterval> | undefined
const platformRequest = new AbortController()
const denialLabels: Record<string,string> = {missing_token:'缺少认证令牌',invalid_token:'认证令牌无效或过期',user_unavailable:'用户不可用',invalid_session:'会话已失效',session_unavailable:'会话服务不可用',insufficient_role:'角色权限不足'}

const defaultFilters = {
  days: 7,
  resource_type: '',
  action: '',
  status: '',
  username: '',
  database_account: '',
  outcome: '',
}
const filters = reactive({ ...defaultFilters })
let appliedFilters = { ...defaultFilters }

const outcomes: Record<string, string> = {begun: '事务已开启', committed: '事务已提交', rolled_back: '事务已回滚', cancel_requested: '已请求取消', already_finished: '任务已结束', export_pending: '导出待审批', export_approved: '导出已批准', export_rejected: '导出已拒绝', download_response: '已返回下载响应', succeeded: '执行成功', failed: '执行失败', unknown: '结果待确认', rejected: '请求拒绝', duplicate: '重复请求', conflict: '请求冲突', completed: '操作完成', access_updated: '范围已更新', profile_updated: '资料已更新', revision_published: '修订已发布', revision_status_updated: '修订状态已更新'}
const actionLabels: Record<string, string> = {
  REFRESH: '刷新会话', LOGOUT: '退出登录', AUTH_DENIED: '访问拒绝',
  TRANSACTION_BEGIN: '开启事务', TRANSACTION_COMMIT: '提交事务', TRANSACTION_ROLLBACK: '回滚事务',
  REPORT_PREVIEW: '预览报表', REPORT_EXECUTE: '执行报表', REPORT_DRAFT_SAVE:'保存正式报表草稿',REPORT_SUBMIT:'提交报表审批',REPORT_REVIEW_VALIDATE:'验证审批账号',REPORT_REVIEW_DECISION:'审批报表',REPORT_RELEASE:'发布或下线报表',REPORT_TRANSFER:'移交报表',REPORT_ACCOUNT_MANAGE:'维护报表账号',REPORT_PUBLISHED_EXECUTE:'执行已发布报表',REPORT_VERSION_VIEW:'查看报表版本',
  'DASHBOARD_MANAGED-DASHBOARDS': '新建仪表盘',
  DASHBOARD_DRAFT: '保存仪表盘草稿',
  DASHBOARD_PREFLIGHT: '预检仪表盘',
  DASHBOARD_SUBMIT: '提交仪表盘审阅',
  DASHBOARD_VERSION_VIEW: '查看仪表盘版本',
  DASHBOARD_REFRESHES: '发起仪表盘刷新',
  DASHBOARD_EXECUTE: '执行仪表盘卡片',
  DASHBOARD_REVIEW_VIEW: '查看仪表盘审阅',
  DASHBOARD_REVIEW_VALIDATE: '验证仪表盘审阅',
  DASHBOARD_REVIEW_DECISION: '决定仪表盘审阅',
  DASHBOARD_REVIEW_REASSIGN: '改派仪表盘审阅',
  DASHBOARD_RELEASES: '发布或下线仪表盘',
  'DASHBOARD_ADOPT-CONTRACT': '采用仪表盘契约',
  'DASHBOARD_AUDIENCE-CHANGES': '调整仪表盘受众',
  'DASHBOARD_ACCESS-REQUESTS': '申请仪表盘受众访问',
  DASHBOARD_ARCHIVE: '归档仪表盘',
  DASHBOARD_RESTORE: '恢复仪表盘',
  DASHBOARD_TRANSFER: '移交仪表盘',
  DASHBOARD_DELETE: '删除仪表盘',
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
  REPORT: '报表', REPORT_ACCOUNT:'报表执行账号', DASHBOARD: '仪表盘', DASHBOARD_REVIEW: '仪表盘审阅', AUTH: '认证',
  CONNECTION: '数据源',
  SSH_TUNNEL: 'SSH 隧道',
  USER: '用户',
  SQL: 'SQL', 'DATASOURCE-DEFINITIONS': '数据源定义', 'USER-GROUPS': '用户组',
  QUERY_HISTORY: '查询历史',
}

async function reload() {
  listRequest?.abort()
  const current = new AbortController()
  listRequest = current
  loading.value = true
  listError.value = ''
  denied.value = false
  logs.value = []
  total.value = 0
  selectedLog.value = undefined
  detailTrigger = undefined
  try {
    const result = await adminApi.auditLogs({
      ...appliedFilters,
      platform: isPlatform.value && platformLog.value,
      page: page.value,
      page_size: pageSize,
    }, { silentError: true, signal: current.signal })
    if (current.signal.aborted) return
    logs.value = result.items
    total.value = result.total
  } catch (error) {
    if (current.signal.aborted) return
    denied.value = error instanceof ApiRequestError && error.status === 403
    listError.value = denied.value ? '当前身份没有读取此类审计记录的权限。' : '暂时无法读取审计记录，请重试。'
  } finally {
    if (!current.signal.aborted) {
      listRequest = undefined
      loading.value = false
    }
  }
  return true
}

function search() {
  appliedFilters = { ...filters }
  page.value = 1
  void reload()
}

function reset() {
  Object.assign(filters, defaultFilters)
  search()
}

async function reloadFromControl(event: MouseEvent, preferred?: () => HTMLElement | undefined) {
  const trigger = event.currentTarget
  const restoreFocus = trigger instanceof HTMLElement && trigger === document.activeElement
  if (!await reload() || !restoreFocus) return
  await nextTick()
  const active = document.activeElement
  if (listRequest || platformRequest.signal.aborted || (active !== trigger && active !== document.body)) return
  const target = preferred?.() ?? retryButton.value ?? resultsTable.value ?? searchButton.value
  target?.focus()
}

function changePage(value: number, event?: MouseEvent) {
  const controls = value > page.value ? [nextPage, previousPage] : [previousPage, nextPage]
  page.value = value
  if (event) {
    void reloadFromControl(event, () => controls.map(control => control.value).find(button => button && !button.disabled))
  } else {
    void reload()
  }
}

async function readDelivery() {
  if (deliveryRequest) return
  const current = new AbortController()
  deliveryRequest = current
  try {
    const result = await adminApi.auditDelivery({ silentError: true, signal: current.signal })
    if (current.signal.aborted) return
    delivery.value = result
    deliveryError.value = false
  } catch {
    if (!current.signal.aborted) deliveryError.value = true
  } finally {
    if (!current.signal.aborted) deliveryRequest = undefined
  }
}

async function openDetail(log: AuditLog, event: MouseEvent) {
  detailTrigger = event.currentTarget instanceof HTMLElement ? event.currentTarget : undefined
  selectedLog.value = log
  await nextTick()
  detailHeading.value?.focus()
}

async function closeDetail() {
  selectedLog.value = undefined
  await nextTick()
  detailTrigger?.focus()
  detailTrigger = undefined
}

onMounted(async () => {
  void reload()
  try {
    const platform = await request.get<unknown, { is_platform_admin: boolean }>('/platform/status', {
      silentError: true,
      signal: platformRequest.signal,
    })
    if (platformRequest.signal.aborted) return
    isPlatform.value = platform.is_platform_admin
    if (isPlatform.value) {
      void readDelivery()
      deliveryTimer = setInterval(() => void readDelivery(), 10000)
    }
  } catch {
    // 空间审计仍由其原有权限独立判断。
  }
})

onScopeDispose(() => {
  platformRequest.abort()
  listRequest?.abort()
  deliveryRequest?.abort()
  if (deliveryTimer) clearInterval(deliveryTimer)
})

function fmtTime(s: string) {
  return new Date(s).toLocaleString('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  })
}
function cleanIP(ip: string) {
  return (ip || '').replace('/32', '')
}

function outcomeLabel(log: AuditLog) {
  const outcome = log.context?.outcome
  const label = outcomes[outcome || ''] || (log.status === 1 ? '成功' : '失败')
  return log.context?.response_failed && outcome !== 'unknown' ? `${label}（响应未确认）` : label
}
</script>

<template>
  <section class="ui-page-stack min-w-0">
    <PageHeader :title="pageTitles.audit"
      description="按时间、操作用户、资源和结果查看审计记录，并核对数据库身份与执行回执。" />

    <div v-if="delivery || deliveryError" role="status" class="glass-panel px-4 py-3 text-sm"
      :class="deliveryError || !delivery?.ready || delivery?.last_error ? 'text-warning' : 'text-muted'">
      <p v-if="deliveryError">暂时无法读取审计写入状态，将自动重试。</p>
      <template v-else-if="delivery">
        <p>{{ !delivery.ready ? '审计存储不可用或队列已满，新的审计操作已暂停。' : delivery.pending ? '审计记录已保存，正在等待落库。' : '审计写入正常' }}</p>
        <p class="text-xs mt-1">
          待写入 {{ delivery.pending }} / {{ delivery.capacity }}
          <span v-if="delivery.oldest"> · 最早待写入 {{ fmtTime(delivery.oldest) }}</span>
          <span v-if="delivery.last_delivered"> · 最近落库 {{ fmtTime(delivery.last_delivered) }}</span>
          · 本次启动写入失败 {{ delivery.delivery_failures }} 次 · 存储失败 {{ delivery.storage_failures }} 次
        </p>
      </template>
    </div>

    <label v-if="isPlatform" class="flex items-center gap-2 text-sm">
      <input v-model="platformLog" type="checkbox" @change="changePage(1)" />
      查看平台身份审计（独立于工作空间）
    </label>

    <form class="space-y-3" aria-label="审计筛选" @submit.prevent="search">
      <div class="ui-filter-bar">
        <div class="ui-filter-field">
          <label :for="id + '-days'" class="text-sm text-muted">时间范围</label>
          <el-select :id="id + '-days'" v-model="filters.days" class="w-full">
            <el-option label="近 1 天" :value="1" />
            <el-option label="近 7 天" :value="7" />
            <el-option label="近 30 天" :value="30" />
            <el-option label="近 90 天" :value="90" />
          </el-select>
        </div>
        <div class="ui-filter-field ui-filter-search">
          <label :for="id + '-username'" class="text-sm text-muted">操作用户</label>
          <input :id="id + '-username'" v-model.trim="filters.username" class="glass-input w-full" placeholder="搜索用户名" />
        </div>
        <div class="ui-filter-field">
          <label :for="id + '-action'" class="text-sm text-muted">操作</label>
          <el-select :id="id + '-action'" v-model="filters.action" placeholder="全部操作" class="w-full">
            <el-option label="全部操作" value="" />
            <el-option v-for="(label, key) in actionLabels" :key="key" :label="label" :value="key" />
          </el-select>
        </div>
        <div class="ui-filter-field">
          <label :for="id + '-resource'" class="text-sm text-muted">资源类型</label>
          <el-select :id="id + '-resource'" v-model="filters.resource_type" placeholder="全部资源" class="w-full">
            <el-option label="全部资源" value="" />
            <el-option v-for="(label, key) in resourceLabels" :key="key" :label="label" :value="key" />
          </el-select>
        </div>
      </div>
      <div class="ui-filter-bar">
        <div class="ui-filter-field">
          <label :for="id + '-status'" class="text-sm text-muted">结果</label>
          <el-select :id="id + '-status'" v-model="filters.status" placeholder="全部结果" class="w-full">
            <el-option label="全部结果" value="" />
            <el-option label="成功" value="success" />
            <el-option label="失败" value="failed" />
          </el-select>
        </div>
        <div class="ui-filter-field">
          <label :for="id + '-database-account'" class="text-sm text-muted">数据库账号</label>
          <input :id="id + '-database-account'" v-model.trim="filters.database_account" class="glass-input w-full" placeholder="配置的数据库账号" />
        </div>
        <div class="ui-filter-field">
          <label :for="id + '-outcome'" class="text-sm text-muted">执行状态</label>
          <el-select :id="id + '-outcome'" v-model="filters.outcome" placeholder="全部执行状态" class="w-full">
            <el-option label="全部执行状态" value="" />
            <el-option v-for="(label, value) in outcomes" :key="value" :label="label" :value="value" />
          </el-select>
        </div>
      </div>
      <div class="flex flex-wrap gap-2">
        <button ref="searchButton" type="submit" class="liquid-button ui-control-target">查询</button>
        <button type="button" class="ghost-button ui-control-target" @click="reset">重置</button>
      </div>
    </form>

    <LoadingState v-if="loading" label="正在读取审计记录…" />
    <PageState v-else-if="listError" :title="denied ? '无权查看审计记录' : '审计记录读取失败'"
      :description="listError" :tone="denied ? 'warning' : 'danger'">
      <template v-if="!denied" #actions>
        <button ref="retryButton" type="button" class="ghost-button ui-control-target" @click="reloadFromControl($event)">重试</button>
      </template>
    </PageState>
    <PageState v-else-if="!logs.length && page === 1" title="暂无审计记录" description="所选条件下没有审计记录。" />
    <section v-else class="min-w-0 space-y-3" aria-label="审计结果">
      <div ref="resultsTable" class="editorial-table-wrap ui-focus-target" role="region" tabindex="0" aria-label="审计记录表格"
        @focusin="revealTableAction" @keydown.self="scrollTableByKeyboard">
        <table class="editorial-table min-w-[760px]" aria-label="审计记录">
          <thead>
            <tr>
              <th scope="col">时间</th>
              <th scope="col">操作用户</th>
              <th scope="col">操作</th>
              <th scope="col">资源</th>
              <th scope="col">结果</th>
              <th scope="col">请求标识</th>
              <th scope="col">详情</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="log in logs" :key="log.id" :class="{ 'bg-interactive': selectedLog?.id === log.id }">
              <td class="whitespace-nowrap text-muted">{{ fmtTime(log.created_at) }}</td>
              <td class="max-w-40 [overflow-wrap:anywhere]">{{ log.username || '匿名' }}</td>
              <td class="whitespace-nowrap">{{ actionLabels[log.action] || log.action }}</td>
              <td class="max-w-56 [overflow-wrap:anywhere]">
                {{ resourceLabels[log.resource_type] || log.resource_type }}
                <p v-if="log.resource_name" class="editorial-table__meta">{{ log.resource_name }}</p>
              </td>
              <td>
                <span class="inline-block rounded-sm px-2 py-0.5 text-xs"
                  :class="log.context?.outcome === 'unknown' || log.context?.response_failed ? 'bg-warning-soft text-warning' : log.status === 1 ? 'bg-success-soft text-success' : 'bg-danger-soft text-danger'">
                  {{ outcomeLabel(log) }}
                </span>
              </td>
              <td class="max-w-48 font-mono text-xs [overflow-wrap:anywhere]">{{ log.trace_id || '—' }}</td>
              <td>
                <button type="button" class="editorial-text-action ui-control-target whitespace-nowrap"
                  :aria-label="'查看详情 ' + (log.trace_id || '#' + log.id)"
                  :aria-expanded="selectedLog?.id === log.id"
                  :aria-controls="selectedLog?.id === log.id ? id + '-detail' : undefined"
                  @click="openDetail(log, $event)">查看详情</button>
              </td>
            </tr>
            <tr v-if="!logs.length">
              <td colspan="7" class="text-center text-muted">本页暂无记录，可返回上一页。</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="flex flex-wrap items-center justify-between gap-3 text-sm text-muted">
        <span aria-live="polite">共 {{ total }} 条记录</span>
        <nav class="flex flex-wrap items-center gap-2" aria-label="审计分页">
          <button ref="previousPage" type="button" class="ghost-button ui-control-target" :disabled="page <= 1" @click="changePage(page - 1, $event)">上一页</button>
          <span>第 {{ page }} 页</span>
          <button ref="nextPage" type="button" class="ghost-button ui-control-target" :disabled="page * pageSize >= total" @click="changePage(page + 1, $event)">下一页</button>
        </nav>
      </div>
    </section>

    <section v-if="selectedLog" :id="id + '-detail'" :aria-labelledby="id + '-detail-title'"
      class="data-surface ui-card-padding space-y-4 min-w-0 [overflow-wrap:anywhere]">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h2 :id="id + '-detail-title'" ref="detailHeading" tabindex="-1" class="text-lg font-semibold ui-focus-target">操作详情</h2>
        <button type="button" class="ghost-button ui-control-target" @click="closeDetail">关闭详情</button>
      </div>
      <div class="grid gap-5 text-sm sm:grid-cols-2">
        <section class="space-y-2 min-w-0">
          <h3 class="font-semibold">操作与结果</h3>
          <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-2">
            <dt class="text-muted">时间</dt><dd>{{ fmtTime(selectedLog.created_at) }}</dd>
            <dt class="text-muted">操作用户</dt><dd>{{ selectedLog.username || '匿名' }}</dd>
            <dt class="text-muted">操作</dt><dd>{{ actionLabels[selectedLog.action] || selectedLog.action }}</dd>
            <dt class="text-muted">资源</dt><dd>{{ resourceLabels[selectedLog.resource_type] || selectedLog.resource_type }} {{ selectedLog.resource_name }}</dd>
            <dt class="text-muted">结果</dt><dd>{{ outcomeLabel(selectedLog) }}</dd>
            <dt class="text-muted">耗时</dt><dd>{{ selectedLog.duration_ms == null ? '—' : selectedLog.duration_ms + ' ms' }}</dd>
            <dt v-if="selectedLog.error_message" class="text-muted">错误</dt>
            <dd v-if="selectedLog.error_message" class="text-danger">{{ selectedLog.error_message }}</dd>
          </dl>
        </section>
        <section class="space-y-2 min-w-0">
          <h3 class="font-semibold">来源与数据库身份</h3>
          <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-2">
            <dt class="text-muted">请求标识</dt><dd class="font-mono">{{ selectedLog.trace_id || '—' }}</dd>
            <dt class="text-muted">来源 IP</dt><dd class="font-mono">{{ cleanIP(selectedLog.ip_address) || '—' }}</dd>
            <dt class="text-muted">数据库账号</dt><dd>{{ selectedLog.context?.database_account || '—' }}</dd>
            <dt v-if="selectedLog.context?.definition_revision_id" class="text-muted">数据源修订</dt>
            <dd v-if="selectedLog.context?.definition_revision_id">定义 #{{ selectedLog.context.definition_revision_id }} · 修订 {{ selectedLog.context.revision || '—' }}</dd>
            <dt v-if="selectedLog.context?.driver" class="text-muted">驱动</dt>
            <dd v-if="selectedLog.context?.driver">{{ selectedLog.context.driver }}</dd>
            <dt v-if="selectedLog.context?.route" class="text-muted">路由</dt>
            <dd v-if="selectedLog.context?.route">{{ selectedLog.context.route }}</dd>
            <dt v-if="selectedLog.context?.http_status" class="text-muted">响应</dt>
            <dd v-if="selectedLog.context?.http_status">HTTP {{ selectedLog.context.http_status }}</dd>
            <dt v-if="selectedLog.context?.auth_failure" class="text-muted">拒绝原因</dt>
            <dd v-if="selectedLog.context?.auth_failure">{{ denialLabels[selectedLog.context.auth_failure] || selectedLog.context.auth_failure }}</dd>
          </dl>
        </section>
        <section class="space-y-2 min-w-0 sm:col-span-2">
          <h3 class="font-semibold">关联编号</h3>
          <ul class="flex flex-col gap-1 text-muted">
            <li>事件 {{ selectedLog.event_id || '历史记录' }}</li>
            <li v-if="selectedLog.context?.source_family_id">来源 {{ selectedLog.context.source_family_id }}</li>
            <li v-if="selectedLog.context?.execution_request_id">执行请求 {{ selectedLog.context.execution_request_id }}</li>
            <li v-if="selectedLog.context?.transaction_id">事务 {{ selectedLog.context.transaction_id }}</li>
            <li v-if="selectedLog.context?.job_id">任务 {{ selectedLog.context.job_id }}</li>
            <li v-if="selectedLog.context?.export_id">导出 #{{ selectedLog.context.export_id }}</li>
            <li v-if="selectedLog.context?.report_id">报表 #{{ selectedLog.context.report_id }} · 版本 {{ selectedLog.context.report_version }}</li>
            <li v-if="selectedLog.context?.report_publication_no">发布序号 {{ selectedLog.context.report_publication_no }} · 审批 #{{ selectedLog.context.report_review_id }} · 执行账号 #{{ selectedLog.context.report_account_id }}</li>
            <li v-if="selectedLog.context?.report_author">作者 {{ selectedLog.context.report_author }} · 审批 {{ selectedLog.context.report_approver }} · 发布 {{ selectedLog.context.report_publisher }}</li>
            <li v-if="selectedLog.context?.report_decision">审批决定 {{ selectedLog.context.report_decision }}</li>
            <li v-if="selectedLog.context?.report_release_kind">发布操作 {{ selectedLog.context.report_release_kind }}</li>
            <li v-if="selectedLog.context?.report_manifest_hash">批准摘要 {{ selectedLog.context.report_manifest_hash }}</li>
            <li v-if="selectedLog.context?.access_request_id">访问申请 #{{ selectedLog.context.access_request_id }} · {{ selectedLog.context.access_decision }}</li>
            <li v-if="selectedLog.context?.access_request_ids?.length">关联申请 {{ selectedLog.context.access_request_ids.join(', ') }} · 目标用户 {{ selectedLog.context.access_target_user_ids?.join(', ') }} · 目标组 {{ selectedLog.context.access_target_group_ids?.join(', ') }}</li>
            <li v-if="selectedLog.context?.workspace_id">空间 #{{ selectedLog.context.workspace_id }}</li>
            <li v-if="selectedLog.context?.dashboard_id">
              仪表盘 #{{ selectedLog.context.dashboard_id }}
              <span v-if="selectedLog.context.dashboard_publication_no"> · 发布 {{ selectedLog.context.dashboard_publication_no }}</span>
              <span v-if="selectedLog.context.tile_id"> · 卡片 {{ selectedLog.context.tile_id }}</span>
            </li>
            <li v-if="selectedLog.context?.dashboard_draft_version">仪表盘草稿版本 {{ selectedLog.context.dashboard_draft_version }}</li>
            <li v-if="selectedLog.context?.dashboard_version">仪表盘版本 {{ selectedLog.context.dashboard_version }}</li>
            <li v-if="selectedLog.context?.dashboard_review_id">仪表盘审阅 #{{ selectedLog.context.dashboard_review_id }}</li>
            <li v-if="selectedLog.context?.refresh_id">刷新 {{ selectedLog.context.refresh_id }}</li>
            <li v-if="selectedLog.context?.contract_hash">契约 {{ selectedLog.context.contract_hash }}</li>
          </ul>
        </section>
      </div>
    </section>
  </section>
</template>
