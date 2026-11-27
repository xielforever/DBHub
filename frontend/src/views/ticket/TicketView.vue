<script setup lang="ts">
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus } from 'lucide-vue-next'
import PageHeader from '../../components/base/PageHeader.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageState from '../../components/base/PageState.vue'
import LoadingState from '../../components/base/LoadingState.vue'
import ChangeTicketPane from '../query/components/ChangeTicketPane.vue'
import { ticketApi, type TicketItem, type TicketPayload } from '../../api/ticket'
import { useUserStore } from '../../stores/user'
import { featureAllowed } from '../../utils/permission'
import { ApiRequestError } from '../../utils/request'

defineProps<{ embedded?: boolean }>()

const route = useRoute()
const router = useRouter()
const user = useUserStore()
const canApply = computed(() => featureAllowed(user, 'change.apply', user.role === 'admin' || user.role === 'developer'))
const canReview = computed(() => featureAllowed(user, 'change.review', user.role === 'admin'))
const canExecute = computed(() => featureAllowed(user, 'change.execute', user.role === 'admin'))

const statusInfo = {
  pending: { label: '待审批', tone: 'bg-warning-soft text-warning' },
  approved: { label: '已批准', tone: 'bg-success-soft text-success' },
  failed: { label: '执行失败', tone: 'bg-danger-soft text-danger' },
  executed: { label: '已执行', tone: 'bg-success-soft text-success' },
  executing: { label: '执行中', tone: 'bg-warning-soft text-warning' },
  rejected: { label: '已拒绝', tone: 'bg-danger-soft text-danger' },
}
const categories = [
  { id: 'all', name: '全部' },
  ...Object.entries(statusInfo).map(([id, value]) => ({ id, name: value.label })),
]
const types = { ddl: 'DDL', dml: 'DML', index: '索引' }
const risks = { low: '低', medium: '中', high: '高' }
const pageSize = 20
const status = ref('all')
const draft = ref({ keyword: '', type: '' })
const filters = ref({ ...draft.value })
const page = ref(1)
const total = ref(0)
const tickets = ref<TicketItem[]>([])
const loading = ref(true)
const listError = ref('')
const forbidden = ref(false)
const busy = ref(false)
const showCreate = ref(false)
const selectedId = ref<number>()
const selected = computed(() => tickets.value.find(ticket => ticket.id === selectedId.value))
const ticketPage = ref<HTMLElement>()
const detailHeading = ref<HTMLElement>()
const detailStatus = ref<HTMLElement>()
const listRetry = ref<HTMLButtonElement>()
let listRequest: AbortController | undefined
let alive = true

async function reload() {
  listRequest?.abort()
  const request = new AbortController()
  listRequest = request
  loading.value = true
  listError.value = ''
  forbidden.value = false
  try {
    const result = await ticketApi.list({
      status: status.value === 'all' ? undefined : status.value,
      q: filters.value.keyword || undefined,
      type: filters.value.type || undefined,
      page: page.value,
      page_size: pageSize,
    }, request.signal)
    if (request.signal.aborted) return
    tickets.value = result.items
    total.value = result.total
    if (!selected.value) selectedId.value = undefined
  } catch (error) {
    if (request.signal.aborted) return
    tickets.value = []
    selectedId.value = undefined
    total.value = 0
    forbidden.value = error instanceof ApiRequestError && error.status === 403
    listError.value = error instanceof Error ? error.message : '读取工单失败'
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}

function changePage(value: number) {
  selectedId.value = undefined
  page.value = value
  void reload()
}

function changeStatus(value: string) {
  if (busy.value || status.value === value) return
  status.value = value
  changePage(1)
}

function applyFilters() {
  filters.value = { ...draft.value }
  changePage(1)
}

function resetFilters() {
  draft.value = { keyword: '', type: '' }
  applyFilters()
}

async function selectTicket(id: number) {
  selectedId.value = id
  await nextTick()
  detailHeading.value?.focus()
}

function focusList(id: number) {
  const row = ticketPage.value?.querySelector<HTMLButtonElement>(`[data-ticket-id="${id}"]`)
  const tab = ticketPage.value?.querySelector<HTMLButtonElement>('[role="tab"][aria-selected="true"]')
  const target = row ?? listRetry.value ?? tab
  target?.focus()
}

async function closeDetail() {
  const id = selectedId.value
  selectedId.value = undefined
  await nextTick()
  if (id !== undefined) focusList(id)
}

function time(value?: string | null) {
  return value ? new Date(value).toLocaleString('zh-CN') : '—'
}

function queryText(key: string) {
  const value = route.query[key]
  return typeof value === 'string' ? value : ''
}

const incoming = computed(() => {
  const table = queryText('table')
  if (!table) return null
  const requestedRisk = queryText('risk')
  const risk: TicketPayload['risk'] = requestedRisk === 'high' || requestedRisk === 'low' ? requestedRisk : 'medium'
  return {
    table,
    database: queryText('db') || queryText('database'),
    risk,
    title: queryText('title') || `${table} 变更（血缘影响）`,
    sql: queryText('sql'),
    reason: queryText('reason'),
  }
})
const prefill = ref<Partial<TicketPayload> | null>(null)

function openCreate(fromLineage = false) {
  prefill.value = fromLineage && incoming.value ? { ...incoming.value } : null
  showCreate.value = true
}

async function clearLineage() {
  const query = { ...route.query }
  for (const key of ['table', 'db', 'database', 'title', 'sql', 'risk', 'reason']) delete query[key]
  await router.replace({ query })
}

watch(incoming, (value) => {
  if (value && canApply.value && (queryText('title') || queryText('sql'))) openCreate(true)
}, { immediate: true })

async function createTicket(payload: TicketPayload) {
  try {
    const created = await ticketApi.create(payload)
    if (!alive) return true
    ElMessage.success(`工单 #${created.id} 已创建`)
  } catch (error) {
    if (alive) ElMessage.error(error instanceof Error ? error.message : '创建失败')
    return false
  }
  if (prefill.value && incoming.value) void clearLineage()
  void reload()
  return true
}

async function updateStatus(ticket: TicketItem, next: 'approved' | 'rejected' | 'executed', event: MouseEvent) {
  if (busy.value) return
  const trigger = event.currentTarget
  const restoreFocus = trigger instanceof HTMLElement && trigger === document.activeElement
  busy.value = true
  let sent = false
  let failure: string | undefined
  try {
    if (next === 'executed') {
      await ElMessageBox.confirm('执行将修改目标数据库，确认执行已批准的 SQL？', '执行工单', {
        type: 'warning', confirmButtonText: '执行', cancelButtonText: '取消', modalClass: 'ticket-confirmation',
      })
    } else if (next === 'approved') {
      await ElMessageBox.confirm('确认已核对完整 SQL、目标数据源修订和影响范围，并批准此变更？', '批准工单', {
        type: 'warning', confirmButtonText: '批准', cancelButtonText: '取消', modalClass: 'ticket-confirmation',
      })
    }
    if (!alive) return
    sent = true
    if (next === 'executed') await ticketApi.execute(ticket.id)
    else await ticketApi.updateStatus(ticket.id, next)
  } catch (error) {
    if (sent) failure = error instanceof Error ? error.message : '操作未完成，请核对工单状态'
  }
  if (!alive) return
  const canRestore = () => {
    const active = document.activeElement
    return active === trigger || active === document.body || !!active?.closest('.ticket-confirmation')
  }
  const focusWasRetained = restoreFocus && canRestore()
  if (sent) await reload()
  if (!alive) return
  busy.value = false

  if (sent) {
    const failedReceiptVisible = next === 'executed' && selected.value?.id === ticket.id &&
      selected.value.status === 'failed' && !!selected.value.execution_error
    if (!failure) ElMessage.success(next === 'executed' ? 'SQL 已实际执行' : '审批已保存')
    else if (!failedReceiptVisible) ElMessage.error(failure)
  }

  await nextTick()
  if (!focusWasRetained || !canRestore()) return
  if (!sent && trigger instanceof HTMLElement && trigger.isConnected) trigger.focus()
  else if (selected.value?.id === ticket.id && detailStatus.value) detailStatus.value.focus()
  else focusList(ticket.id)
}

async function copySql(sql: string) {
  try {
    await navigator.clipboard.writeText(sql)
    ElMessage.success('已复制 SQL')
  } catch {
    ElMessage.error('无法访问剪贴板，可在下方选中 SQL 后复制')
  }
}

void reload()
onScopeDispose(() => {
  alive = false
  listRequest?.abort()
})
</script>

<template>
  <section ref="ticketPage" class="ui-page-stack">
    <PageHeader :heading="embedded ? 'h2' : 'h1'" title="变更工单" description="数据库变更的申请、审批与执行跟踪">
      <template #actions>
        <button class="ghost-button ui-control-target" :disabled="loading || busy" @click="reload">刷新工单</button>
        <button v-if="canApply" class="liquid-button ui-control-target inline-flex items-center gap-1" :disabled="busy" @click="openCreate()">
          <Plus class="w-4 h-4" aria-hidden="true" />新建工单
        </button>
      </template>
    </PageHeader>

    <section v-if="incoming && canApply" class="glass-panel ui-card-padding flex flex-wrap items-center gap-3">
      <p class="min-w-0 break-all text-sm">来自血缘影响分析 · {{ incoming.database ? incoming.database + '.' : '' }}{{ incoming.table }}</p>
      <button class="ghost-button ui-control-target" @click="clearLineage">忽略预填</button>
      <button class="liquid-button ui-control-target" @click="openCreate(true)">从血缘新建</button>
    </section>

    <PageCategoryTabs label="工单状态" :categories="categories" :active-id="status" @select="changeStatus">
      <div class="ui-page-stack">
        <form class="glass-panel ui-card-padding flex flex-wrap items-end gap-3" aria-label="筛选工单" @submit.prevent="applyFilters">
          <label class="block flex-1 basis-48 min-w-0 text-sm">
            搜索工单
            <input v-model.trim="draft.keyword" class="glass-input mt-1 w-full" placeholder="标题、表名或 SQL" :disabled="busy" />
          </label>
          <label class="block w-full sm:w-40 text-sm">
            工单类型
            <select v-model="draft.type" class="glass-input mt-1 w-full" :disabled="busy">
              <option value="">全部类型</option>
              <option v-for="(label, key) in types" :key="key" :value="key">{{ label }}</option>
            </select>
          </label>
          <button type="submit" class="liquid-button ui-control-target" :disabled="busy">查询</button>
          <button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="resetFilters">重置</button>
        </form>

        <LoadingState v-if="loading" label="正在读取工单…" />
        <PageState v-else-if="listError" :title="forbidden ? '无法访问变更工单' : '读取工单失败'" :description="listError" tone="danger">
          <template #actions><button ref="listRetry" class="ghost-button ui-control-target" @click="reload">重试读取</button></template>
        </PageState>
        <PageState v-else-if="!total" title="暂无工单" description="当前分类和筛选条件下没有可见工单。" />
        <template v-else>
          <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="工单表格">
            <table class="editorial-table min-w-[640px]" aria-label="变更工单记录">
              <thead><tr><th scope="col">工单编号</th><th scope="col">标题</th><th scope="col">类型</th><th scope="col">申请人</th><th scope="col">申请时间</th><th scope="col">状态</th></tr></thead>
              <tbody>
                <tr v-for="ticket in tickets" :key="ticket.id" :class="{ 'bg-accent-soft': selectedId === ticket.id }">
                  <td><button class="ghost-button ui-control-target font-mono" :aria-label="`查看工单 #${ticket.id}`"
                    :data-ticket-id="ticket.id" :aria-expanded="selectedId === ticket.id" :disabled="busy" @click="selectTicket(ticket.id)">#{{ ticket.id }}</button></td>
                  <td class="break-words">{{ ticket.title }}</td>
                  <td>{{ types[ticket.type] }}</td>
                  <td>{{ ticket.requester || '—' }}</td>
                  <td class="whitespace-nowrap">{{ time(ticket.created_at) }}</td>
                  <td><span class="inline-block whitespace-nowrap px-2 py-1 text-xs" :class="statusInfo[ticket.status]?.tone">{{ statusInfo[ticket.status]?.label ?? ticket.status }}</span></td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-if="!tickets.length" class="text-sm text-muted">此页没有工单，可返回上一页。</p>
          <div class="flex flex-wrap items-center justify-between gap-2 text-sm text-muted">
            <span aria-live="polite">共 {{ total }} 项 · 第 {{ page }} 页</span>
            <div class="flex gap-2">
              <button class="ghost-button ui-control-target" :disabled="busy || page <= 1" @click="changePage(page - 1)">上一页</button>
              <button class="ghost-button ui-control-target" :disabled="busy || page * pageSize >= total" @click="changePage(page + 1)">下一页</button>
            </div>
          </div>

          <section v-if="selected" class="glass-panel ui-card-padding space-y-4" aria-labelledby="ticket-detail-title">
            <div class="flex flex-wrap justify-between items-center gap-2">
              <h2 id="ticket-detail-title" ref="detailHeading" tabindex="-1" class="text-lg font-semibold ui-focus-target">工单详情 #{{ selected.id }}</h2>
              <button class="ghost-button ui-control-target" :disabled="busy" @click="closeDetail">关闭工单详情</button>
            </div>
            <div class="grid gap-4 lg:grid-cols-[minmax(13rem,1fr)_minmax(0,2fr)]">
              <dl class="grid grid-cols-[auto_minmax(0,1fr)] content-start gap-x-3 gap-y-2 text-sm">
                <dt class="text-muted">标题</dt><dd class="break-words">{{ selected.title }}</dd>
                <dt class="text-muted">类型</dt><dd>{{ types[selected.type] }}</dd>
                <dt class="text-muted">状态</dt>
                <dd><span ref="detailStatus" role="status" tabindex="-1" class="ui-focus-target">{{ statusInfo[selected.status]?.label ?? selected.status }}</span></dd>
                <dt class="text-muted">申请人</dt><dd class="break-words">{{ selected.requester || '—' }}</dd>
                <dt class="text-muted">申请时间</dt><dd>{{ time(selected.created_at) }}</dd>
                <dt class="text-muted">目标表</dt><dd class="break-all">{{ selected.table }}</dd>
                <dt class="text-muted">数据库</dt><dd class="break-all">{{ selected.database_name || '默认' }}</dd>
                <dt class="text-muted">连接</dt><dd>{{ selected.connection_id ? '#' + selected.connection_id : '已删除' }}</dd>
                <dt class="text-muted">数据源修订</dt><dd>{{ selected.definition_revision_id ? '#' + selected.definition_revision_id : '需重新提交' }}</dd>
                <dt class="text-muted">风险</dt><dd>{{ risks[selected.risk] }}</dd>
              </dl>
              <div class="min-w-0 space-y-3">
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <h3 class="font-medium">变更 SQL</h3>
                  <button class="ghost-button ui-control-target" @click="copySql(selected.sql)">复制 SQL</button>
                </div>
                <pre tabindex="0" aria-label="完整变更 SQL" class="border border-stroke bg-surface-muted p-3 max-h-72 overflow-auto text-sm">{{ selected.sql }}</pre>
                <div v-if="selected.reason"><h3 class="font-medium">变更原因</h3><p class="mt-1 whitespace-pre-wrap break-words text-sm">{{ selected.reason }}</p></div>
                <div v-if="selected.executed_at || selected.execution_result || selected.execution_error" class="border-t border-divider pt-3 space-y-1 text-sm">
                  <h3 class="font-medium">执行回执</h3>
                  <p v-if="selected.executed_at">执行时间 {{ time(selected.executed_at) }}</p>
                  <p v-if="selected.executed_by">执行人 #{{ selected.executed_by }}</p>
                  <p v-if="selected.execution_result?.affected_rows != null">影响 {{ selected.execution_result.affected_rows }} 行</p>
                  <p v-if="selected.execution_error" class="text-danger break-words">{{ selected.execution_error }}</p>
                </div>
              </div>
            </div>
            <div class="flex flex-wrap gap-2">
              <template v-if="selected.status === 'pending' && canReview">
                <p v-if="selected.requester_id === user.user?.id" class="text-sm text-muted">等待其他有权复核人审批</p>
                <template v-else>
                  <button class="liquid-button ui-control-target" :disabled="busy" @click="updateStatus(selected, 'approved', $event)">批准</button>
                  <button class="ghost-button ui-control-target" :disabled="busy" @click="updateStatus(selected, 'rejected', $event)">拒绝</button>
                </template>
              </template>
              <button v-if="selected.status === 'approved' && canExecute" class="liquid-button ui-control-target" :disabled="busy"
                @click="updateStatus(selected, 'executed', $event)">执行 SQL</button>
            </div>
          </section>
        </template>
      </div>
    </PageCategoryTabs>

    <ChangeTicketPane v-if="canApply" v-model="showCreate" :prefill="prefill" :create="createTicket" />
  </section>
</template>
