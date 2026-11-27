<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref, useId, watch } from 'vue'
import { useRoute, useRouter, type RouteLocationRaw } from 'vue-router'
import { automationApi, type DashboardTodoKind, type NotificationItem, type TodoItem } from '../../api/automation'
import { managedDashboardApi, type DashboardReview } from '../../api/managed-dashboard'
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { handlingReturnState, useRestoredHandlingList, type HandlingOrigin } from '../../composables/useHandlingReturn'
import { useUserStore } from '../../stores/user'
import { deliveryInboxTarget, hasDeliveryTarget, inboxTarget, inboxTodoTarget, isDashboardTodoKind } from '../../utils/automation'
import { featureAllowed } from '../../utils/permission'
import { ApiRequestError } from '../../utils/request'

const props = defineProps<{ embedded?: boolean; notificationsOnly?: boolean; hideCategoryTabs?: boolean }>()
type Category = 'todos' | 'notifications' | 'unread' | 'all'
type Entry = { channel: 'todos'; item: TodoItem } | { channel: 'notifications'; item: NotificationItem }
type Page = { entries: Entry[]; total: number; unread?: number; throughId?: number }
type DashboardOrigin = Extract<HandlingOrigin, { kind: 'dashboard-review' }>
type DashboardReturn = { state: 'loading' | 'error' } | {
  state: 'ready'; review: DashboardReview; canEnterPublish: boolean
}
const pageSize = 20
const router = useRouter()
const route = useRoute()
const user = useUserStore()
const restoredList = useRestoredHandlingList()
const scope = computed(() => user.user?.id && user.workspaceId && user.permissionStatus === 'ready'
  ? user.user.id + ':' + user.permissionKey
  : null)
const categories = props.notificationsOnly
  ? [{ id: 'unread', name: '未读' }, { id: 'all', name: '全部' }]
  : [{ id: 'todos', name: '待我处理' }, { id: 'notifications', name: '通知' }]
const category = ref<Category>(props.notificationsOnly ? 'unread' : route.query.tab === 'notifications' ? 'notifications' : 'todos')
const isTodo = computed(() => category.value === 'todos')
const todoFilter = ref<'all' | DashboardTodoKind>('all')
const canFilterDashboard = computed(() => ['dashboard.review', 'dashboard.publish'].some(action =>
  featureAllowed(user, action, user.hasCapability('report_review')),
))
const onlyUnread = ref(false)
const unreadOnly = computed(() => category.value === 'unread' || (category.value === 'notifications' && onlyUnread.value))
const title = props.notificationsOnly ? '通知' : '通知与待办'
const page = ref(1)
const result = ref<Page>()
const loading = ref(false)
const updating = ref(false)
const error = ref('')
const forbidden = ref(false)
const readError = ref('')
const busy = ref(false)
const openingDelivery = ref(false)
const readUnavailable = computed(() => !scope.value || loading.value || updating.value || busy.value)
const selectedKey = ref<string>()
const inboxRegion = ref<HTMLElement>()
const pageSummary = ref<HTMLElement>()
const unreadSummary = ref<HTMLElement>()
const listRetry = ref<HTMLElement>()
const writeErrorNotice = ref<HTMLElement>()
const writeErrorId = useId()
const detailRegion = ref<HTMLElement>()
const detailHeading = ref<HTMLElement>()
const tableRegion = ref<HTMLElement>()
const dashboardReturn = ref<DashboardReturn>()
const dashboardReturnRegion = ref<HTMLElement>()
const selected = computed(() => result.value?.entries.find(entry => entryKey(entry) === selectedKey.value))
let detailTrigger: HTMLElement | undefined
let reader: AbortController | undefined
let writer: AbortController | undefined
let deliveryReader: AbortController | undefined
let dashboardReturnReader: AbortController | undefined
let dashboardReturnOrigin: DashboardOrigin | undefined
let timer: ReturnType<typeof setInterval> | undefined
let alive = true
let restoredOrigin = false

const todoTypes: Record<string, string> = {
  business_operation: '业务操作审批',
  report_review: '报表审批',
  dashboard_review: '仪表盘待复核',
  dashboard_publish: '仪表盘待发布',
  access_request: '访问申请',
  report_export: '导出审批',
  metric_incident: '指标待跟踪',
}
const noticeTypes: Record<string, string> = {
  ...todoTypes,
  operations: '运行通知',
  source_tasks: '连接任务',
  published_report: '报表',
  published_dashboard: '仪表盘',
  report_subscription: '报表订阅',
  report_delivery: '报表投递',
  formal_export_delivery: '正式导出',
  team_report_delivery: '团队订阅',
  dashboard_delivery: '仪表盘交付',
  report_access: '报表访问申请',
  dashboard_access: '仪表盘访问申请',
}

function entryKey(entry: Entry) {
  return [entry.channel, entry.item.kind, entry.item.id].join(':')
}

function formatTime(value: string) {
  return new Date(value).toLocaleString('zh-CN')
}

function originalLocation(entry: Entry): RouteLocationRaw | undefined {
  const kind = entry.channel === 'todos' ? entry.item.kind : entry.item.resource_type
  const target = entry.channel === 'todos' ? inboxTodoTarget(entry.item) : inboxTarget(kind, entry.item.resource_id)
  if (!target || !scope.value) return undefined
  const resolved = router.resolve(target)
  const workspaceId = user.workspaceId
  const query = { ...resolved.query, workspace_id: String(workspaceId) }
  const userId = user.user?.id
  if (entry.channel === 'todos' && isDashboardTodoKind(kind) && route.name === 'handling-center' && userId) {
    const state = handlingReturnState({
      kind: 'dashboard-review', workspaceId, userId, dashboardId: entry.item.resource_id, reviewId: entry.item.id,
      page: page.value, todoKind: kind, todoFilter: todoFilter.value, summaryOpen: selectedKey.value === entryKey(entry),
    })
    return { path: resolved.path, query, state }
  }
  if (kind !== 'business_operation' || route.name !== 'handling-center' || !userId) {
    return { path: resolved.path, query }
  }
  const state = handlingReturnState({
    kind: 'business', workspaceId, userId, requestId: entry.item.resource_id,
    entryId: entry.item.id, page: page.value, category: entry.channel, onlyUnread: onlyUnread.value,
    summaryOpen: selectedKey.value === entryKey(entry),
  })
  if (user.visibleMenus.some(menu => menu.path === '/business-operations')) {
    return { path: '/handling', query: { ...query, section: 'business' }, state }
  }
  return { path: resolved.path, query, state }
}

const directLocations = computed(() => {
  const locations = new Map<string, RouteLocationRaw>()
  if (!props.embedded || route.name !== 'handling-center' || !isTodo.value) return locations
  for (const entry of result.value?.entries ?? []) {
    if (entry.channel !== 'todos' || (entry.item.kind !== 'business_operation' && !isDashboardTodoKind(entry.item.kind))) continue
    const target = originalLocation(entry)
    if (target) locations.set(entryKey(entry), target)
  }
  return locations
})

async function readPage(signal: AbortSignal): Promise<Page> {
  if (isTodo.value) {
    const response = await automationApi.todos(page.value, signal, todoFilter.value === 'all' ? undefined : todoFilter.value)
    return { total: response.total, entries: response.items.map(item => ({ channel: 'todos', item })) }
  }
  const response = await automationApi.inbox({
    state: unreadOnly.value ? 'unread' : undefined, page: page.value, page_size: pageSize,
  }, signal)
  return {
    entries: response.items.map(item => ({ channel: 'notifications', item })),
    total: response.total,
    unread: response.unread,
    throughId: response.through_id,
  }
}

async function load({ background = false, writeTrigger, retry = false }: {
  background?: boolean
  writeTrigger?: HTMLElement
  retry?: boolean
} = {}) {
  if (!scope.value) return false
  reader?.abort()
  const read = new AbortController()
  reader = read
  loading.value = !retry && (!background || !result.value)
  updating.value = retry || (background && !!result.value)
  if (!retry) {
    error.value = ''
    forbidden.value = false
  }
  try {
    let current = await readPage(read.signal)
    while (!read.signal.aborted && page.value > Math.max(1, Math.ceil(current.total / pageSize))) {
      page.value = Math.max(1, Math.ceil(current.total / pageSize))
      current = await readPage(read.signal)
    }
    if (read.signal.aborted) return false
    error.value = ''
    forbidden.value = false
    result.value = current
    if (selectedKey.value && !selected.value) {
      const active = document.activeElement
      const detailFocused = detailRegion.value?.contains(active)
      void closeDetail(detailFocused === true && active !== writeTrigger)
    }
  } catch (failure) {
    if (read.signal.aborted) return false
    result.value = undefined
    selectedKey.value = undefined
    detailTrigger = undefined
    forbidden.value = failure instanceof ApiRequestError && failure.status === 403
    error.value = failure instanceof Error ? failure.message : '读取失败，请重试'
  } finally {
    if (reader === read) {
      reader = undefined
      loading.value = false
      updating.value = false
    }
  }
  return true
}

function actionFocus(event?: MouseEvent) {
  const trigger = event?.currentTarget
  return trigger instanceof HTMLElement && document.activeElement === trigger ? trigger : undefined
}

function restoreActionFocus(trigger: HTMLElement | undefined, target: HTMLElement | undefined) {
  const active = document.activeElement
  if (trigger && alive && (active === trigger || active === document.body)) target?.focus()
}

async function reload(event: MouseEvent, retry = false) {
  if (readUnavailable.value) return
  const trigger = actionFocus(event)
  const originalScope = scope.value
  if (!await load({ background: true, retry }) || !trigger) return
  await nextTick()
  if (scope.value !== originalScope) return
  const recoveredFocus = isTodo.value ? pageSummary.value : unreadSummary.value
  const target = listRetry.value ?? (retry ? recoveredFocus ?? inboxRegion.value : trigger)
  restoreActionFocus(trigger, target)
}

async function changePage(value: number, event?: MouseEvent) {
  if (busy.value) return
  const trigger = actionFocus(event)
  const originalScope = scope.value
  selectedKey.value = undefined
  detailTrigger = undefined
  result.value = undefined
  page.value = value
  readError.value = ''
  if (!await load() || !trigger) return
  await nextTick()
  if (scope.value === originalScope)
    restoreActionFocus(trigger, listRetry.value ?? pageSummary.value ?? unreadSummary.value ?? inboxRegion.value)
}

function selectCategory(value: string) {
  if (busy.value || value === category.value || !categories.some(item => item.id === value)) return
  clearDashboardReturn()
  category.value = value as Category
  changePage(1)
}

function clearDashboardReturn() {
  dashboardReturnReader?.abort()
  dashboardReturnReader = undefined
  dashboardReturnOrigin = undefined
  dashboardReturn.value = undefined
}

function selectTodoFilter(value: unknown, event?: MouseEvent) {
  if (busy.value || value === todoFilter.value || (value !== 'all' && !isDashboardTodoKind(value))) return
  clearDashboardReturn()
  todoFilter.value = value
  void changePage(1, event)
}

function dashboardReturnLabel(review: DashboardReview) {
  if (review.was_published) return '此版本已有发布记录。'
  if (review.status === 'approved') {
    if (review.requires_re_review ?? review.reviewer_eligible === false) return '批准记录保留，当前需要重新送审。'
    return review.can_publish === true ? '已通过复核，待手动发布。' : '已通过复核。'
  }
  if (review.status === 'pending' && review.approval_stage === 'data_review') return '业务复核已签署，等待独立数据复核。'
  const labels: Record<string,string> = {
    pending: '仍待复核。请核对当前列表。',
    rejected: '已退回修改。',
    withdrawn: '已撤回。',
    expired: '审批已到期。',
  }
  return labels[review.status] ?? '已读取当前审批状态。'
}

async function loadDashboardReturn(origin: DashboardOrigin, originalScope: string, active = document.activeElement) {
  dashboardReturnReader?.abort()
  const attempt = new AbortController()
  dashboardReturnReader = attempt
  dashboardReturnOrigin = origin
  dashboardReturn.value = { state: 'loading' }
  try {
    const review = await managedDashboardApi.review(origin.dashboardId, origin.reviewId, attempt.signal)
    if (alive && scope.value === originalScope && !attempt.signal.aborted) dashboardReturn.value = {
      state: 'ready', review, canEnterPublish: review.can_publish === true && !review.was_published && todoFilter.value !== 'dashboard_publish',
    }
  } catch {
    if (alive && scope.value === originalScope && !attempt.signal.aborted) dashboardReturn.value = { state: 'error' }
  } finally {
    if (dashboardReturnReader === attempt) dashboardReturnReader = undefined
  }
  await nextTick()
  if (alive && scope.value === originalScope && !attempt.signal.aborted &&
    (document.activeElement === active || document.activeElement === document.body)) dashboardReturnRegion.value?.focus()
}

function retryDashboardReturn() {
  if (scope.value && dashboardReturnOrigin && !dashboardReturnReader) void loadDashboardReturn(dashboardReturnOrigin, scope.value)
}

async function selectEntry(entry: Entry, event: MouseEvent) {
  detailTrigger = event.currentTarget as HTMLElement
  selectedKey.value = entryKey(entry)
  readError.value = ''
  await nextTick()
  detailHeading.value?.focus()
}

async function closeDetail(restoreFocus = true) {
  const trigger = detailTrigger
  selectedKey.value = undefined
  detailTrigger = undefined
  if (!restoreFocus) return
  await nextTick()
  if (trigger?.isConnected) trigger.focus()
  else tableRegion.value?.focus()
}

async function markRead(body: { ids?: number[]; through_id?: number }, event?: MouseEvent) {
  if (busy.value || !scope.value) return false
  const trigger = actionFocus(event)
  const originalScope = scope.value
  const write = new AbortController()
  writer = write
  busy.value = true
  readError.value = ''
  const isCurrent = () => alive && !write.signal.aborted && scope.value === originalScope
  let saved = false
  let settled = false
  try {
    await automationApi.read(body, write.signal)
    if (!isCurrent()) return false
    saved = true
    window.dispatchEvent(new Event('dbhub-inbox-updated'))
    settled = await load({ background: true, writeTrigger: trigger })
  } catch (failure) {
    if (isCurrent()) {
      readError.value = failure instanceof Error ? failure.message : '标记已读失败'
      settled = true
    }
  } finally {
    if (writer === write) {
      writer = undefined
      busy.value = false
    }
  }
  if (settled && trigger) {
    await nextTick()
    if (isCurrent()) {
      const target = readError.value
        ? (trigger.isConnected ? trigger : writeErrorNotice.value)
        : listRetry.value ?? unreadSummary.value ?? inboxRegion.value
      restoreActionFocus(trigger, target)
    }
  }
  return saved && isCurrent()
}

function readAll(event: MouseEvent) {
  const cutoff = result.value?.throughId
  if (cutoff && result.value?.unread) void markRead({ through_id: cutoff }, event)
}

async function openNotice(entry: Extract<Entry, { channel: 'notifications' }>, event: MouseEvent) {
  const originalScope = scope.value
  if (!originalScope || busy.value || openingDelivery.value) return
  let target = originalLocation(entry)
  if (hasDeliveryTarget(entry.item)) {
    const key = entryKey(entry)
    const attempt = new AbortController()
    deliveryReader = attempt
    openingDelivery.value = true
    readError.value = ''
    const current = () => alive && !attempt.signal.aborted && scope.value === originalScope && selectedKey.value === key
    try {
      const delivery = await automationApi.notificationDelivery(entry.item.id, attempt.signal)
      if (!current()) return
      const path = delivery.resource_type === entry.item.resource_type && deliveryInboxTarget(delivery)
      if (!path) {
        readError.value = '此通知无法打开对应的交付，请从交付列表核对。'
        return
      }
      const resolved = router.resolve(path)
      target = { path: resolved.path, query: { ...resolved.query, workspace_id: String(user.workspaceId) } }
    } catch (failure) {
      if (current()) {
        readError.value = failure instanceof ApiRequestError && [403, 404, 409, 410].includes(failure.status ?? 0)
          ? '交付链接已过期或当前不可访问，请从交付列表核对。'
          : '读取交付链接失败，请重试。'
      }
      return
    } finally {
      if (deliveryReader === attempt) {
        deliveryReader = undefined
        openingDelivery.value = false
      }
    }
  }
  if (!target) return
  if (!entry.item.read_at && !await markRead({ ids: [entry.item.id] }, event)) return
  if (alive && scope.value && scope.value === originalScope) await router.push(target)
}

async function restoreEntryFocus(origin: Extract<HandlingOrigin, { kind: 'business' }>, originalScope: string) {
  await load()
  if (!alive || scope.value !== originalScope || category.value !== origin.category) return
  await nextTick()
  if (!alive || scope.value !== originalScope || category.value !== origin.category) return
  const entry = result.value?.entries.find(item => item.channel === origin.category &&
    (item.channel === 'todos' ? item.item.kind : item.item.resource_type) === 'business_operation' &&
    item.item.id === origin.entryId && item.item.resource_id === origin.requestId)
  const rowButtons = inboxRegion.value?.querySelectorAll<HTMLButtonElement>('[data-inbox-entry-key]')
  const trigger = entry && rowButtons && Array.from(rowButtons).find(button => button.dataset.inboxEntryKey === entryKey(entry))
  const applicationAction = entry && inboxRegion.value?.querySelector<HTMLAnchorElement>(`[data-inbox-application-id="${origin.entryId}"]`)
  if (origin.summaryOpen && entry && trigger) {
    selectedKey.value = entryKey(entry)
    detailTrigger = trigger
  }
  const target = applicationAction || trigger || (listRetry.value ?? tableRegion.value ?? inboxRegion.value)
  target?.focus()
}

async function restoreDashboardFocus(origin: DashboardOrigin, originalScope: string) {
  const active = document.activeElement
  await load()
  await nextTick()
  if (!alive || scope.value !== originalScope || !isTodo.value || todoFilter.value !== origin.todoFilter) return
  const key = ['todos', origin.todoKind, origin.reviewId].join(':')
  const entry = result.value?.entries.find(item => entryKey(item) === key && item.item.resource_id === origin.dashboardId)
  if (entry) {
    const trigger = inboxRegion.value?.querySelector<HTMLButtonElement>(`[data-inbox-entry-key="${key}"]`)
    if (origin.summaryOpen && trigger) {
      selectedKey.value = key
      detailTrigger = trigger
    }
    const destination = inboxRegion.value?.querySelector<HTMLAnchorElement>(`[data-inbox-direct-key="${key}"]`)
    if (document.activeElement === active || document.activeElement === document.body) (destination ?? trigger ?? inboxRegion.value)?.focus()
  } else if (result.value) {
    await loadDashboardReturn(origin, originalScope, active)
  } else if (document.activeElement === active || document.activeElement === document.body) {
    (listRetry.value ?? inboxRegion.value)?.focus()
  }
}

watch(scope, value => {
  reader?.abort()
  writer?.abort()
  deliveryReader?.abort()
  clearDashboardReturn()
  reader = undefined
  writer = undefined
  deliveryReader = undefined
  selectedKey.value = undefined
  detailTrigger = undefined
  result.value = undefined
  loading.value = false
  updating.value = false
  busy.value = false
  openingDelivery.value = false
  error.value = ''
  readError.value = ''
  page.value = 1
  todoFilter.value = 'all'
  if (!value) return
  const origin = props.embedded && restoredList.value
  if (!restoredOrigin && origin && origin.kind === 'business') {
    restoredOrigin = true
    category.value = origin.category
    onlyUnread.value = origin.onlyUnread
    page.value = origin.page
    void restoreEntryFocus(origin, value)
  } else if (!restoredOrigin && origin && origin.kind === 'dashboard-review') {
    restoredOrigin = true
    category.value = 'todos'
    todoFilter.value = origin.todoFilter
    page.value = origin.page
    void restoreDashboardFocus(origin, value)
  } else void load()
}, { immediate: true, flush: 'sync' })

onMounted(() => {
  timer = setInterval(() => {
    if (scope.value && !reader && !busy.value && !error.value && !document.hidden && navigator.onLine) void load({ background: true })
  }, 30000)
})
onScopeDispose(() => {
  alive = false
  reader?.abort()
  writer?.abort()
  deliveryReader?.abort()
  dashboardReturnReader?.abort()
  clearInterval(timer)
})
</script>

<template>
  <section ref="inboxRegion" class="ui-page-stack ui-focus-target" tabindex="-1" :aria-label="title">
    <header v-if="embedded" class="flex flex-wrap items-start justify-between gap-3" :class="{ '!justify-end': notificationsOnly }">
      <h2 v-if="!notificationsOnly" class="text-lg font-semibold">{{ title }}</h2>
      <button class="ghost-button ui-control-target aria-disabled:opacity-60" :aria-disabled="readUnavailable" @click="reload($event)">
        {{ isTodo ? '刷新待办' : '刷新通知' }}
      </button>
    </header>
    <PageHeader v-else :title="title" description="当前工作空间的通知和待处理事项。">
      <template #actions>
        <button class="ghost-button ui-control-target aria-disabled:opacity-60" :aria-disabled="readUnavailable" @click="reload($event)">
          {{ isTodo ? '刷新待办' : '刷新通知' }}
        </button>
      </template>
    </PageHeader>

    <PageState v-if="!scope" title="待办与通知暂不可用" description="请确认当前工作空间和访问资格后重试。" />
    <PageCategoryTabs v-else :label="notificationsOnly ? '通知范围' : '通知与待办分类'"
      :categories="categories" :active-id="category" :hide-tabs="hideCategoryTabs" @select="selectCategory">
      <div class="ui-page-stack">
        <label v-if="isTodo && canFilterDashboard" class="ui-filter-field max-w-xs">
          <span>待办类型</span>
          <select aria-label="待办类型" class="glass-input ui-control-target" :value="todoFilter" :disabled="busy"
            @change="selectTodoFilter(($event.target as HTMLSelectElement).value)">
            <option value="all">全部事项</option>
            <option value="dashboard_review">仪表盘 · 待我复核</option>
            <option value="dashboard_publish">仪表盘 · 待我发布</option>
          </select>
        </label>
        <div v-if="!isTodo" class="flex flex-wrap items-center gap-3">
          <label v-if="!notificationsOnly" class="inline-flex items-center gap-2 text-sm">
            <input v-model="onlyUnread" type="checkbox" :disabled="busy" @change="changePage(1)" />
            仅未读
          </label>
          <span v-if="result" ref="unreadSummary" class="ui-focus-target text-sm text-muted" tabindex="-1">{{ result.unread }} 条未读</span>
          <button class="ghost-button ui-control-target" :disabled="busy || loading || updating || !result?.unread || !result?.throughId" @click="readAll">
            当前通知全部已读
          </button>
        </div>
        <p v-if="updating" role="status" class="text-sm text-muted">{{ isTodo ? '正在更新待办…' : '正在更新通知…' }}</p>
        <p v-if="readError" :id="writeErrorId" ref="writeErrorNotice" role="alert" class="ui-focus-target text-sm text-danger" tabindex="-1">{{ readError }}</p>
        <section v-if="dashboardReturn" ref="dashboardReturnRegion" tabindex="-1"
          class="glass-panel ui-card-padding space-y-2 ui-focus-target" aria-label="本次办理结果">
          <h3 class="font-semibold">本次办理结果</h3>
          <p v-if="dashboardReturn.state === 'loading'" role="status" class="text-sm text-muted">正在核对原审批…</p>
          <template v-else-if="dashboardReturn.state === 'ready'">
            <p class="text-sm break-words" role="status">
              {{ dashboardReturn.review.definition.name }} · v{{ dashboardReturn.review.number }} · 审批 #{{ dashboardReturn.review.id }} ·
              {{ dashboardReturnLabel(dashboardReturn.review) }}
            </p>
            <button v-if="dashboardReturn.canEnterPublish" class="ghost-button ui-control-target"
              @click="selectTodoFilter('dashboard_publish', $event)">前往待我发布</button>
          </template>
          <template v-else>
            <p class="text-sm text-danger" role="alert">无法读取原审批，当前状态尚未核实。</p>
            <button class="ghost-button ui-control-target" @click="retryDashboardReturn">重试读取审批</button>
          </template>
        </section>
        <LoadingState v-if="loading" :label="isTodo ? '正在读取待办…' : '正在读取通知…'" />
        <PageState v-else-if="error" :heading="embedded && !notificationsOnly ? 'h3' : 'h2'"
          :title="forbidden ? '无法访问当前记录' : isTodo ? '读取待办失败' : '读取通知失败'" :description="error" tone="danger">
          <template #actions>
            <button ref="listRetry" class="ghost-button ui-control-target aria-disabled:opacity-60" :aria-disabled="readUnavailable"
              @click="reload($event, true)">重试读取</button>
          </template>
        </PageState>
        <PageState v-else-if="result && !result.total" :heading="embedded && !notificationsOnly ? 'h3' : 'h2'"
          :title="isTodo ? '暂无待处理事项' : unreadOnly ? '暂无未读通知' : '暂无通知'"
          :description="isTodo ? '当前工作空间没有分配给你的待处理事项。' : '当前范围没有可见通知。'" />
        <div v-else-if="result" class="grid gap-4" :class="selected ? 'xl:grid-cols-[minmax(0,2fr)_minmax(17rem,1fr)]' : ''">
          <div class="min-w-0 space-y-3">
            <div ref="tableRegion" class="editorial-table-wrap" role="region" tabindex="0" :aria-label="isTodo ? '待办表格' : '通知表格'">
              <table class="editorial-table min-w-[540px]" :aria-label="isTodo ? '待我处理事项' : '通知列表'">
                <thead>
                  <tr>
                    <th scope="col">标题</th>
                    <th scope="col">类型</th>
                    <th scope="col">{{ isTodo ? '对象编号' : '状态' }}</th>
                    <th scope="col">{{ isTodo ? '申请时间' : '时间' }}</th>
                    <th v-if="directLocations.size" scope="col">操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="entry in result.entries" :key="entryKey(entry)" :class="{ 'bg-accent-soft': selectedKey === entryKey(entry) }">
                    <td class="editorial-table__name">
                      <RouterLink v-if="entry.channel === 'todos' && isDashboardTodoKind(entry.item.kind) && directLocations.has(entryKey(entry))"
                        :to="directLocations.get(entryKey(entry))!" class="editorial-text-action ui-control-target max-w-full break-words"
                        :data-inbox-direct-key="entryKey(entry)">
                        {{ entry.item.content_name || entry.item.title }}
                        <span class="sr-only"> · {{ entry.item.kind === 'dashboard_publish' ? '查看待发布版本' : '查看冻结版本' }}</span>
                      </RouterLink>
                      <button v-else class="ghost-button ui-control-target max-w-full text-left break-all" :disabled="busy"
                        :data-inbox-entry-id="entry.item.id" :data-inbox-entry-key="entryKey(entry)"
                        :aria-expanded="selectedKey === entryKey(entry)" @click="selectEntry(entry, $event)">
                        {{ entry.channel === 'todos' ? entry.item.content_name || entry.item.title : entry.item.title }}
                      </button>
                      <p v-if="entry.channel === 'todos' && isDashboardTodoKind(entry.item.kind)" class="text-xs text-muted">
                        <template v-if="entry.item.content_version">v{{ entry.item.content_version }} · </template>审批 #{{ entry.item.id }}
                      </p>
                    </td>
                    <td>{{ entry.channel === 'todos' ? todoTypes[entry.item.kind] || '业务待办' : noticeTypes[entry.item.resource_type] || '通知' }}</td>
                    <td v-if="entry.channel === 'todos'">#{{ entry.item.resource_id }}</td>
                    <td v-else><span :class="entry.item.read_at ? 'text-muted' : 'text-accent font-semibold'">{{ entry.item.read_at ? '已读' : '未读' }}</span></td>
                    <td><time :datetime="entry.item.created_at" class="whitespace-nowrap">{{ formatTime(entry.item.created_at) }}</time></td>
                    <td v-if="directLocations.size">
                      <RouterLink v-if="directLocations.has(entryKey(entry))" :to="directLocations.get(entryKey(entry))!"
                        class="editorial-text-action ui-control-target whitespace-nowrap"
                        :aria-label="isDashboardTodoKind(entry.item.kind) ? `${entry.item.kind === 'dashboard_publish' ? '查看待发布版本' : '查看冻结版本'}，审批 #${entry.item.id}` : `查看申请 #${entry.item.resource_id}`"
                        :data-inbox-application-id="entry.item.kind === 'business_operation' ? entry.item.id : undefined">
                        {{ entry.item.kind === 'dashboard_publish' ? '查看待发布版本' : entry.item.kind === 'dashboard_review' ? '查看冻结版本' : '查看申请' }}
                      </RouterLink>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="flex flex-wrap items-center justify-between gap-2 text-sm text-muted">
              <span ref="pageSummary" class="ui-focus-target" tabindex="-1" aria-live="polite">共 {{ result.total }} 项 · 第 {{ page }} 页</span>
              <div class="flex gap-2">
                <button class="ghost-button ui-control-target" :disabled="page <= 1 || busy" @click="changePage(page - 1, $event)">上一页</button>
                <button class="ghost-button ui-control-target" :disabled="page * pageSize >= result.total || busy" @click="changePage(page + 1, $event)">下一页</button>
              </div>
            </div>
          </div>
          <section v-if="selected" ref="detailRegion" class="glass-panel ui-card-padding min-w-0 self-start space-y-4" aria-label="记录详情">
            <div class="flex flex-wrap items-start justify-between gap-2">
              <component :is="embedded && !notificationsOnly ? 'h3' : 'h2'" ref="detailHeading" tabindex="-1" class="ui-focus-target min-w-0 break-all text-lg font-semibold">
                {{ selected.channel === 'todos' ? selected.item.content_name || selected.item.title : selected.item.title }}
              </component>
              <button class="ghost-button ui-control-target" :disabled="busy" @click="closeDetail()">关闭详情</button>
            </div>
            <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm">
              <dt class="text-muted">时间</dt><dd><time :datetime="selected.item.created_at">{{ formatTime(selected.item.created_at) }}</time></dd>
              <dt class="text-muted">类型</dt><dd>{{ selected.channel === 'todos' ? todoTypes[selected.item.kind] || '业务待办' : noticeTypes[selected.item.resource_type] || '通知' }}</dd>
              <template v-if="selected.channel === 'todos'">
                <dt class="text-muted">{{ isDashboardTodoKind(selected.item.kind) ? '审批编号' : '待办编号' }}</dt><dd>#{{ selected.item.id }}</dd>
                <dt class="text-muted">对象编号</dt><dd>#{{ selected.item.resource_id }}</dd>
                <template v-if="selected.item.content_version">
                  <dt class="text-muted">内容版本</dt><dd>v{{ selected.item.content_version }}</dd>
                </template>
                <template v-if="selected.item.expires_at">
                  <dt class="text-muted">截止时间</dt><dd><time :datetime="selected.item.expires_at">{{ formatTime(selected.item.expires_at) }}</time></dd>
                </template>
              </template>
              <template v-else>
                <dt class="text-muted">状态</dt><dd>{{ selected.item.read_at ? '已读' : '未读' }}</dd>
                <template v-if="selected.item.resource_id > 0">
                  <dt class="text-muted">对象编号</dt><dd>#{{ selected.item.resource_id }}</dd>
                </template>
              </template>
            </dl>
            <div v-if="selected.channel === 'notifications' && selected.item.message" class="border-t border-divider pt-3">
              <p class="font-medium">通知内容</p>
              <p class="mt-2 whitespace-pre-wrap break-words text-sm">{{ selected.item.message }}</p>
            </div>
            <div class="flex flex-wrap gap-2">
              <template v-if="selected.channel === 'todos'">
                <RouterLink v-if="originalLocation(selected)" :to="originalLocation(selected)!" class="liquid-button ui-control-target">去处理</RouterLink>
              </template>
              <template v-else>
                <button v-if="!selected.item.read_at" class="ghost-button ui-control-target" :aria-disabled="busy || undefined"
                  :aria-describedby="readError ? writeErrorId : undefined" @click="markRead({ ids: [selected.item.id] }, $event)">标记已读</button>
                <button v-if="originalLocation(selected) || hasDeliveryTarget(selected.item)" class="liquid-button ui-control-target"
                  :aria-disabled="busy || openingDelivery || undefined" :aria-describedby="readError ? writeErrorId : undefined"
                  @click="openNotice(selected, $event)">{{ openingDelivery ? '核对交付链接中…' : '查看原对象' }}</button>
              </template>
            </div>
          </section>
        </div>
      </div>
    </PageCategoryTabs>
  </section>
</template>
