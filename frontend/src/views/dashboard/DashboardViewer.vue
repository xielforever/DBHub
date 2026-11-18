<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import request, { ApiRequestError } from '../../utils/request'
import { newRequestId } from '../../utils/request-id'
import { useUserStore } from '../../stores/user'
import {
  managedDashboardApi as api,
  type DashboardView,
  type DashboardRefresh,
} from '../../api/managed-dashboard'
import type { ExecutionReceipt } from '../../api/workbench'
import ReportResult from '../report/ReportResult.vue'
import ReportParameterForm from '../report/ReportParameterForm.vue'
import { parameterDefaults, parameterQuery, parametersFromQuery, reportParameterError, type ParameterValues, type ReportParameter } from '../report/reportParameters'
import ResultGrid from '../query/components/ResultGrid.vue'
import { MoreHorizontal, ChevronDown } from 'lucide-vue-next'
import { displayUnit } from '../report/reportDisplay'
import {
  dashboardQuality,
  tileFreshness,
  usableResult,
  mustClearPrevious,
  retainOnFailure,
  RETAIN_MS,
  type TileState,
} from './dashboard-quality'
const props = withDefaults(
  defineProps<{
    id: number
    mode?: 'published' | 'draft' | 'review'
    reviewId?: number
  }>(),
  { mode: 'published', reviewId: 0 },
)
const emit = defineEmits<{ close: []; refreshed: [id: string]; 'review-evidence': [id: string] }>(),
  user = useUserStore()
const fullRefreshId = ref(''),
  partialRefresh = ref(false)
const route = useRoute(), router = useRouter()
const parameterDefinitions = ref<ReportParameter[]>([]), parameterValues = ref<ParameterValues>({})
const parameterForm = ref<InstanceType<typeof ReportParameterForm>>()
const applyingFilters = ref(false)
const view = ref<DashboardView>(),
  states = ref<TileState[]>([]),
  busy = ref(false),
  error = ref(''),
  fullCycleAt = ref(''),
  now = ref(0)
const definitionLoading = ref(false)
const definitionReady = ref(false)
const definitionError = ref('')
const cycleReady = ref(false)
const reviewEvidence = ref('')
const accessTarget = ref<string>()
const accessFeedback = ref(new Map<string, { kind: 'pending' | 'success' | 'error'; text: string }>())
const accessDrafts = new Map<string, { reason: string; requestId: string }>()
function setReviewEvidence(id: string) {
  if (props.mode !== 'review' || reviewEvidence.value === id) return
  reviewEvidence.value = id
  emit('review-evidence', id)
}
const definitionRead = new AbortController()
const contextLabel = computed(() => {
  if (props.mode === 'published') return '正式发布'
  if (props.mode === 'review') return '冻结版本审阅'
  return '已保存草稿预览'
})
let baseline = 0,
  started = performance.now(),
  generation = 0,
  alive = true,
  controller = new AbortController()
const defaultQuality = {
  allow_unknown_freshness: false,
  reason: '',
  max_watermark_skew_seconds: 300,
}
const quality = computed(() =>
  dashboardQuality(
    states.value,
    view.value?.quality || defaultQuality,
    now.value,
    fullRefreshId.value,
    partialRefresh.value,
  ),
)
const freshnessLabels: Record<string, string> = {
  fresh: '新鲜',
  stale: '已过期',
  unknown: '未知',
  missing: '尚无结果',
}
const receiptLabels: Record<ExecutionReceipt['status'], string> = {
  queued: '排队中',
  running: '执行中',
  succeeded: '成功',
  failed: '失败',
  rejected: '已拒绝',
  unknown: '结果待确认',
}
const formatTime = (value: string, timeZone?: string) =>
  new Date(value).toLocaleString('zh-CN', timeZone ? { timeZone } : undefined)
function tileCommand(command: string, state: TileState) {
  if (command === 'retry') void refresh(state.tile.key)
  else if (command === 'receipt') void checkReceipt(state)
}

async function retryCard(state: TileState, event: MouseEvent) {
  const trigger = event.currentTarget
  const card = trigger instanceof HTMLButtonElement && document.activeElement === trigger
    ? trigger.closest('article')
    : null
  const heading = card?.querySelector<HTMLElement>('h3')
  const pending = refresh(state.tile.key)
  if (!heading) return pending
  const retryGeneration = generation
  const ownsFocus = () => {
    const active = document.activeElement
    return alive && retryGeneration === generation && heading.isConnected &&
      (active === trigger || active === heading || active === document.body)
  }
  await nextTick()
  if (ownsFocus()) heading.focus()
  await pending
  await nextTick()
  if (!ownsFocus() || !states.value.some(tile => tile.tile.key === state.tile.key && tile.tile.status === 'ready')) return
  const target = card?.querySelector<HTMLButtonElement>('[data-dashboard-card-retry]') ?? heading
  target.focus()
}

const timer = setInterval(() => {
  now.value = baseline + performance.now() - started
  for (const state of states.value) {
    if (
      state.result &&
      now.value - Date.parse(state.result.quality.query_completed_at) >=
        RETAIN_MS
    ) {
      state.result = undefined
      state.message = '结果保留期已满，请重新查询'
      state.retained = false
      setReviewEvidence('')
    }
  }
}, 1000)
function pauseUnavailablePage() {
  if (
    !busy.value ||
    (navigator.onLine && document.visibilityState === 'visible')
  )
    return
  generation++
  controller.abort()
  busy.value = false
  setReviewEvidence('')
  error.value = '页面已暂停查询；返回页面或恢复网络后，请手动刷新。'
  for (const state of states.value) {
    if (state.status === 'loading' || state.status === 'idle') {
      state.status = 'error'
      state.retained = !!state.result
      state.message = '查询已暂停，请手动刷新'
    }
  }
}
async function initialize() {
  if (definitionLoading.value || !alive) return
  definitionLoading.value = true
  definitionError.value = ''
  const signal = definitionRead.signal
  try {
    if (props.mode === 'published') {
      const published = await api.publishedGet(props.id, signal)
      if (signal.aborted) return
      view.value = published
      parameterDefinitions.value = published.parameters ?? []
    } else if (props.mode === 'draft') {
      const draft = await api.get(props.id, signal)
      if (signal.aborted) return
      parameterDefinitions.value = draft.draft.parameters ?? []
    } else {
      const reviews = await api.reviews(props.id, signal)
      if (signal.aborted) return
      const review = reviews.items.find(item => item.id === props.reviewId)
      if (!review) throw new Error('当前审阅版本不可用，请核对版本状态。')
      parameterDefinitions.value = review.definition.parameters ?? []
    }
    parameterValues.value = { ...parameterDefaults(parameterDefinitions.value), ...parametersFromQuery(parameterDefinitions.value, route.query) }
    definitionReady.value = true
  } catch (failure) {
    if (!signal.aborted) definitionError.value = failure instanceof Error ? failure.message : '无法读取仪表盘定义。'
  } finally {
    if (!signal.aborted) definitionLoading.value = false
  }
  if (definitionReady.value && !signal.aborted) await refresh()
}

onMounted(() => {
  document.addEventListener('visibilitychange', pauseUnavailablePage)
  window.addEventListener('offline', pauseUnavailablePage)
  void initialize()
})
watch(parameterValues, () => {
  generation++; controller.abort(); busy.value = false; states.value = []; fullCycleAt.value = ''; fullRefreshId.value = ''; cycleReady.value = false
  setReviewEvidence('')
}, { deep: true, flush: 'sync' })
async function submitFilters(updateAddress = false) {
  if (!alive || !definitionReady.value || busy.value || applyingFilters.value) return
  const selection = generation
  applyingFilters.value = true
  try {
    await nextTick()
    if (!alive || selection !== generation) return
    if (parameterForm.value && !(await parameterForm.value.validate())) return
    if (!alive || selection !== generation) return
    if (updateAddress && props.mode === 'published') {
      const query = Object.fromEntries(Object.entries(route.query).filter(([key]) => !key.startsWith('p_')))
      await router.replace({ query: { ...query, tab: 'published', dashboard: String(props.id), ...parameterQuery(parameterValues.value) } })
    }
  } finally {
    applyingFilters.value = false
  }
  if (selection === generation) await refresh()
}
function resetFilters() {
  if (applyingFilters.value) return
  parameterValues.value = parameterDefaults(parameterDefinitions.value)
  void submitFilters(true)
}
onScopeDispose(() => {
  alive = false
  generation++
  definitionRead.abort()
  controller.abort()
  clearInterval(timer)
  document.removeEventListener('visibilitychange', pauseUnavailablePage)
  window.removeEventListener('offline', pauseUnavailablePage)
  states.value = []
})
async function refresh(only?: string) {
  if (busy.value || !definitionReady.value || !alive) return
  setReviewEvidence('')
  if (!navigator.onLine || document.visibilityState !== 'visible') {
    error.value = '页面已暂停查询；返回页面或恢复网络后，请手动刷新。'
    return
  }
  busy.value = true
  error.value = ''
  controller.abort()
  controller = new AbortController()
  const current = ++generation,
    signal = controller.signal
  partialRefresh.value = !!only
  if (!only) fullRefreshId.value = `pending:${current}`
  const isCurrent = () => alive && current === generation && !signal.aborted
  const params = { ...parameterValues.value }
  try {
    const context: DashboardRefresh = await api.refresh(
      props.id,
      props.mode,
      props.reviewId,
      signal,
      params,
    )
    if (!isCurrent()) return
    if (!only) fullRefreshId.value = context.refresh_id
    baseline = Date.parse(context.server_time)
    started = performance.now()
    now.value = baseline
    const changed =
      !!view.value &&
      (view.value.publication_no !== context.publication_no ||
        view.value.lifecycle_epoch !== context.lifecycle_epoch)
    if (changed) {
      fullCycleAt.value = ''
      if (only) error.value = '仪表盘布局已更新，请刷新全部卡片以完成本轮查询'
    }
    const previous = changed ? [] : states.value
    states.value = context.tiles.map((tile) => {
      const prior = previous.find((s) => s.tile.key === tile.key)
      const state: TileState = {
        tile,
        status: tile.status === 'ready' ? 'idle' : tile.status,
      }
      if (
        prior &&
        !mustClearPrevious(prior, tile) &&
        usableResult(prior, now.value)
      ) {
        state.result = prior.result
        state.retained = only && tile.key !== only ? prior.retained : true
        state.cycle = prior.cycle
        if (only && tile.key !== only) {
          state.status = prior.status
          state.message = prior.message
          state.receiptId = prior.receiptId
        }
      }
      return state
    })
    view.value = context
    cycleReady.value = true
    const targets = states.value.filter(
      (s) => s.tile.status === 'ready' && (!only || s.tile.key === only),
    )
    let position = 0
    async function worker() {
      while (position < targets.length && isCurrent()) {
        const state = targets[position++]!
        state.status = 'loading'
        state.receiptId = state.tile.execution_id
        try {
          const result = await api.execute(
            context.refresh_id,
            state.tile.key,
            signal,
          )
          if (!isCurrent()) return
          if (
            result.refresh_id !== context.refresh_id ||
            result.tile_key !== state.tile.key ||
            result.dashboard_publication_no !== context.publication_no ||
            result.lifecycle_epoch !== context.lifecycle_epoch ||
            result.report_publication_no !==
              state.tile.report?.publication_no ||
            result.quality.contract_hash !== state.tile.contract_hash
          )
            throw new ApiRequestError('执行结果版本不一致，请重新刷新', 409)
          state.result = result
          state.status = 'success'
          state.retained = false
          state.cycle = context.refresh_id
          state.message = undefined
        } catch (e) {
          if (!isCurrent()) return
          const status = e instanceof ApiRequestError ? e.status : undefined
          state.status = 'error'
          state.message =
            e instanceof Error ? e.message : '查询失败，请核对执行回执'
          if (!retainOnFailure(status)) {
            state.result = undefined
            state.retained = false
          } else {
            state.retained = !!state.result
            if (state.result) state.message += '；暂显示上次成功结果'
          }
        }
      }
    }
    await Promise.all([worker(), worker()])
    if (!isCurrent()) return
    const required = states.value.filter((s) => s.tile.required)
    if (
      !only &&
      required.length &&
      required.every(
        (s) => s.status === 'success' && s.cycle === context.refresh_id,
      )
    ) {
      fullCycleAt.value = new Date(
        Math.max(
          ...required.map((s) =>
            Date.parse(s.result!.quality.query_completed_at),
          ),
        ),
      ).toISOString()
    }
    emit('refreshed', context.refresh_id)
    if (!only && states.value.length > 0 && states.value.every(state => state.status === 'success' && state.cycle === context.refresh_id)) {
      setReviewEvidence(context.refresh_id)
    }
  } catch (e) {
    if (!isCurrent()) return
    error.value = e instanceof Error ? e.message : '看板加载失败'
    if (e instanceof ApiRequestError && e.status === 400 &&
        parameterDefinitions.value.some(parameter => reportParameterError(parameter, params[parameter.name]))) {
      fullRefreshId.value = ''
      return
    }
    if (e instanceof ApiRequestError && !retainOnFailure(e.status)) {
      states.value = []
      view.value = undefined
      fullCycleAt.value = ''
      definitionReady.value = false
      cycleReady.value = false
      parameterDefinitions.value = []
      definitionError.value = error.value
    } else {
      for (const state of states.value) {
        if (state.result) {
          state.retained = true
          state.status = 'error'
        }
      }
    }
  } finally {
    if (isCurrent()) busy.value = false
  }
}
async function requestAccess(tile: string, event: MouseEvent) {
  if (accessTarget.value !== undefined) return
  const trigger = event.currentTarget
  const card = trigger instanceof HTMLButtonElement ? trigger.closest('article') : null
  const restoreFocus = document.activeElement === trigger
  const dashboardId = props.id
  const workspaceId = user.workspaceId
  const isCurrent = () => alive && props.id === dashboardId && user.workspaceId === workspaceId
  const previous = accessDrafts.get(tile)
  let submitted = false
  accessTarget.value = tile
  try {
    const input = await ElMessageBox.prompt(
      '申请将授予独立的报表查看权，获批并正式发布后生效。请说明用途。',
      '申请本人权限',
      {
        inputPlaceholder: '申请理由',
        inputValue: previous?.reason,
        inputValidator: (v) => !!v?.trim() || '请填写理由',
        modalClass: 'dashboard-access-confirmation',
      },
    )
    if (!isCurrent() || !states.value.some(state => state.tile.key === tile && state.tile.status === 'restricted')) return
    const reason = input.value.trim()
    const requestId = previous?.reason === reason ? previous.requestId : newRequestId()
    accessDrafts.set(tile, { reason, requestId })
    accessFeedback.value.set(tile, { kind: 'pending', text: '正在提交访问申请…' })
    submitted = true
    const result = await api.requestAccess(dashboardId, tile, reason, requestId)
    if (isCurrent()) {
      accessDrafts.delete(tile)
      accessFeedback.value.set(tile, { kind: 'success', text: `访问申请已提交 · 申请 #${result.id}，可在访问申请队列查看进度。` })
    }
  } catch (e) {
    if (submitted && isCurrent()) {
      const status = e instanceof ApiRequestError ? e.status : undefined
      const text = e instanceof ApiRequestError && status !== undefined && status >= 400 && status < 500
        ? e.message
        : '暂未确认访问申请结果，可使用原理由重试。'
      accessFeedback.value.set(tile, { kind: 'error', text })
    }
  } finally {
    accessTarget.value = undefined
    if (submitted && restoreFocus && isCurrent()) {
      await nextTick()
      const active = document.activeElement
      if (isCurrent() && (active === trigger || active === document.body || active?.closest('.dashboard-access-confirmation')))
        card?.querySelector<HTMLElement>('[data-dashboard-access-feedback]')?.focus()
    }
  }
}
async function checkReceipt(state: TileState) {
  const id = state.receiptId
  if (!id) return
  const signal = controller.signal
  const isCurrent = () => alive && !signal.aborted && states.value.includes(state)
  try {
    const receipt = await request.get<unknown, ExecutionReceipt>(
      `/query/executions/${id}`,
      { signal, silentError: true },
    )
    if (isCurrent())
      state.message = `执行回执：${receiptLabels[receipt.status]} · 执行编号 ${receipt.request_id}。回执不保存结果行，重新查询将创建新的执行编号。`
  } catch (e) {
    if (isCurrent()) {
      state.message = e instanceof Error ? e.message : '回执暂不可用'
      if (e instanceof ApiRequestError && !retainOnFailure(e.status)) {
        setReviewEvidence('')
        state.result = undefined
        state.retained = false
      }
    }
  }
}
</script>

<template>
  <section class="ui-page-stack" aria-label="仪表盘查看">
    <PageHeader :title="view?.name || '仪表盘'" :description="view?.description" :heading="mode === 'review' ? 'h2' : 'h1'"
      :eyebrow="[user.workspace?.name, contextLabel, view?.version ? 'v' + view.version : ''].filter(Boolean).join(' · ')">
      <template #actions>
        <button class="ghost-button ui-control-target" @click="emit('close')">返回列表</button>
        <button v-if="definitionReady" type="button" class="liquid-button ui-control-target"
          :class="{ 'opacity-60': busy || applyingFilters }" :aria-disabled="busy || applyingFilters || undefined"
          :aria-busy="busy || undefined" @click="submitFilters()">
          {{ busy ? '查询中…' : '刷新数据' }}
        </button>
      </template>
    </PageHeader>
    <LoadingState v-if="definitionLoading" label="正在读取仪表盘定义…" variant="detail" />
    <PageState v-else-if="definitionError" :heading="mode === 'review' ? 'h3' : 'h2'" title="无法读取仪表盘" :description="definitionError" tone="danger">
      <template #actions><button class="ghost-button ui-control-target" @click="initialize">重试读取仪表盘</button></template>
    </PageState>
    <template v-if="definitionReady">
    <ReportParameterForm ref="parameterForm" v-model="parameterValues" :definitions="parameterDefinitions" @apply="submitFilters(true)" @reset="resetFilters" />
    <LoadingState v-if="busy && !cycleReady" label="正在建立仪表盘查询…" variant="detail" />
    <div
      v-if="cycleReady && states.length"
      class="glass-panel px-4 py-3 flex flex-wrap gap-x-5 gap-y-1 text-sm"
      role="status"
      aria-live="polite"
    >
      <span
        >{{
          partialRefresh ? '部分刷新' : quality.complete ? '完整' : '部分可用'
        }}
        {{ quality.available }}/{{ quality.total }} 张必需卡片</span
      >
      <span v-if="quality.optionalTotal"
        >可选卡片 {{ quality.optionalAvailable }}/{{
          quality.optionalTotal
        }}</span
      ><span>时效：{{ freshnessLabels[quality.freshness] }}</span
      ><span v-if="quality.misaligned" class="text-warning"
        >比较组水位未对齐，暂不宜比较</span
      ><span class="text-muted"
        >最近完整查询：{{
          fullCycleAt ? formatTime(fullCycleAt) : '尚未完成'
        }}</span
      >
    </div>
    <p v-if="error" role="alert" class="text-danger">{{ error }}</p>
    <div class="dashboard-grid">
      <article
        v-for="state in states"
        :key="state.tile.key"
        class="glass-panel p-4 min-w-0 space-y-2"
        :class="[
          `tile-${state.tile.width}`,
          { 'metric-tile': state.tile.report?.chart_type === 'metric' },
        ]"
        :aria-label="state.tile.report?.name || '受限卡片'"
      >
        <header class="flex items-start justify-between gap-2">
          <h3 class="ui-focus-target font-semibold pt-2" tabindex="-1">
            {{ state.tile.report?.name || '受限卡片' }}
          </h3>
          <el-dropdown
            v-if="state.tile.status === 'ready'"
            trigger="click"
            @command="tileCommand($event, state)"
            ><button
              class="flex items-center justify-center rounded-lg text-muted hover:bg-interactive focus-visible:outline focus-visible:outline-focus ui-control-target ui-icon-button"
              :aria-label="`${state.tile.report?.name || '卡片'}更多操作`"
              :disabled="busy"
            >
              <MoreHorizontal class="w-5 h-5" /></button
            ><template #dropdown
              ><el-dropdown-menu
                ><el-dropdown-item command="retry"
                  >重新查询此卡片</el-dropdown-item
                ><el-dropdown-item
                  command="receipt"
                  :disabled="!state.receiptId"
                  >查看本次执行回执</el-dropdown-item
                ></el-dropdown-menu
              ></template
            ></el-dropdown
          >
        </header>
        <LoadingState v-if="state.status === 'loading'" variant="chart" label="正在查询图表数据…" />
        <div
          v-if="state.tile.status === 'restricted'"
          class="py-6 text-center space-y-3"
        >
          <p class="text-sm text-muted">无权访问或报表暂不可用</p>
          <button
            v-if="mode === 'published'"
            class="ghost-button text-xs ui-control-target"
            :aria-disabled="accessTarget !== undefined || undefined"
            @click="requestAccess(state.tile.key, $event)"
          >
            申请本人报表访问权
          </button>
          <p v-if="accessFeedback.has(state.tile.key)"
            data-dashboard-access-feedback
            :role="accessFeedback.get(state.tile.key)?.kind === 'error' ? 'alert' : 'status'"
            :tabindex="accessFeedback.get(state.tile.key)?.kind === 'pending' ? undefined : -1"
            class="text-sm ui-focus-target"
            :class="{
              'text-danger': accessFeedback.get(state.tile.key)?.kind === 'error',
              'text-success': accessFeedback.get(state.tile.key)?.kind === 'success',
              'text-muted': accessFeedback.get(state.tile.key)?.kind === 'pending',
            }"
          >{{ accessFeedback.get(state.tile.key)?.text }}</p>
        </div>
        <p
          v-if="state.tile.status === 'contract_changed'"
          role="alert"
          class="text-warning text-sm py-6"
        >
          报表口径已变化，需要看板负责人明确采纳新契约并重新审批。
        </p>
        <p v-if="state.message" class="text-sm text-warning [overflow-wrap:anywhere]" role="alert">
          {{ state.message }}
        </p>
        <template
          v-if="state.result && usableResult(state, now) && state.tile.report"
        >
          <p
            class="text-xs"
            :class="
              tileFreshness(state, now) === 'fresh'
                ? 'text-success'
                : 'text-warning'
            "
          >
            {{ state.retained ? '上次成功结果 · ' : ''
            }}{{ freshnessLabels[tileFreshness(state, now)] }} · 数据截止
            {{
              state.result.quality.data_as_of
                ? formatTime(
                    state.result.quality.data_as_of,
                    state.tile.report.data_contract.declaration.timezone,
                  )
                : '未知'
            }}
          </p>
          <ReportResult
            :type="state.tile.report.chart_type"
            :config="state.tile.report.chart_config"
            :result="state.result.result"
            :title="state.tile.report.name"
            :unit="state.tile.report.data_contract.declaration.unit"
            :currency="state.tile.report.data_contract.declaration.currency"
            compact
            hide-details
          />
          <details class="group text-xs text-muted border-t border-stroke">
            <summary
              class="cursor-pointer flex items-center focus-visible:outline focus-visible:outline-focus ui-control-target"
              :aria-label="`${state.tile.report.name}查看详情`"
            >
              查看详情
              <ChevronDown class="w-4 h-4 ml-1 group-open:rotate-180" />
            </summary>
            <div class="space-y-2 pb-2">
              <p>
                {{ state.tile.required ? '必需卡片' : '可选卡片' }} · 单位
                {{
                  displayUnit(state.tile.report.data_contract.declaration.unit, state.tile.report.data_contract.declaration.currency)
                }}<span
                  v-if="state.tile.report.data_contract.declaration.currency && state.tile.report.data_contract.declaration.unit !== 'currency'"
                >
                  · 币种
                  {{
                    state.tile.report.data_contract.declaration.currency
                  }}</span
                >
              </p>
              <p>
                指标含义：{{
                  state.tile.report.data_contract.declaration.meaning
                }}
              </p>
              <p>
                统计范围：{{
                  state.tile.report.data_contract.declaration.scope_description
                }}
              </p>
              <p>
                业务时区：{{
                  state.tile.report.data_contract.declaration.timezone
                }}
                · 报表发布 #{{ state.tile.report.publication_no }}
              </p>
              <p>
                查询完成：{{
                  formatTime(state.result.quality.query_completed_at)
                }}
                · 返回 {{ state.result.result.rows?.length || 0 }} 行 ·
                {{ state.result.result.duration_ms }} 毫秒
              </p>
              <p>原始查询结果</p>
              <div class="h-[240px] flex min-w-0">
                <ResultGrid
                  :columns="state.result.result.columns || []"
                  :rows="state.result.result.rows || []"
                />
              </div>
            </div>
          </details>
        </template>
        <button
          v-if="state.status === 'error' && state.tile.status === 'ready'"
          class="ghost-button text-xs ui-control-target"
          data-dashboard-card-retry
          :disabled="busy"
          @click="retryCard(state, $event)"
        >
          重试此卡片
        </button>
      </article>
    </div>
    <PageState v-if="cycleReady && !states.length && !busy && !error" :heading="mode === 'review' ? 'h3' : 'h2'" title="尚未添加卡片" description="当前仪表盘没有可呈现的卡片。" />
    </template>
  </section>
</template>

<style scoped>
button {
  min-height: var(--db-control-height-sm);
}
.dashboard-grid {
  display: grid;
  grid-template-columns: repeat(12, minmax(0, 1fr));
  gap: 1rem;
}
.tile-full {
  grid-column: span 12;
}
.tile-half {
  grid-column: span 6;
}
.tile-wide {
  grid-column: span 8;
}
.tile-narrow {
  grid-column: span 4;
}
@media (max-width: 767px) {
  .dashboard-grid > article {
    grid-column: span 12;
  }
}
</style>
