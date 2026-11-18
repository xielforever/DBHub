<script setup lang="ts">
import {featureAllowed} from '../../utils/permission'
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import { useUserStore } from '../../stores/user'
import request, { ApiRequestError } from '../../utils/request'
import {
  managedDashboardApi as api,
  type ManagedDashboard,
  type DashboardReview,
} from '../../api/managed-dashboard'
import { reportCommandKeys } from '../report/reportCommands'
import DashboardViewer from './DashboardViewer.vue'
import DashboardAudienceDialog from './DashboardAudienceDialog.vue'
import DashboardReviewSummary from './DashboardReviewSummary.vue'
const props = defineProps<{ dashboard: ManagedDashboard }>(),
  emit = defineEmits<{
    close: []
    changed: []
    reviewerEligibility: [dashboardId: number, reviewId: number, eligible: boolean]
  }>(),
  user = useUserStore(),
  keys = reportCommandKeys()
const audienceOpen = ref(false)
const summaryReady = ref(false),
  reviewerName = ref(''),
  summaryGeneration = ref(0)
function summaryLoaded(state: { ready: boolean; reviewerName: string }) {
  summaryReady.value = state.ready
  reviewerName.value = state.reviewerName
}
const dashboard = ref(props.dashboard),
  reviews = ref<DashboardReview[]>([]),
  selectedId = ref<number>(),
  selected = computed(() =>
    reviews.value.find((v) => v.id === selectedId.value),
  ),
  admins = ref<{ id: number; username: string }[]>([]),
  reviewer = ref<number>(),
  preview = ref(false),
  refreshId = ref(''),
  confirmed = ref(false),
  comment = ref(''),
  busy = ref(false),
  error = ref('')
const readPending = ref(true)
const readError = ref('')
const reviewerError = ref('')
const reviewerLoading = ref(false)
const dialogOpen = ref(true)
const versionContent = ref<HTMLElement>()
const versionSelect = ref<HTMLSelectElement>()
const versionDescription = ref<HTMLElement>()
const validateButton = ref<HTMLButtonElement>()
const validationReceipt = ref<HTMLElement>()
const retryVersionRead = ref<HTMLButtonElement>()
const operationError = ref<HTMLElement>()
const releaseReceipt = ref<HTMLElement>()
const releaseMessage = ref('')
const reviewActionReceipt = ref<HTMLElement>()
const reviewActionResult = ref<{ reviewId: number; message: string }>()
const staleApprovalNotice = ref<HTMLElement>()
const staleApproval = computed(() => selected.value?.status === 'approved' && selected.value.reviewer_eligible === false)
const canReview = computed(() =>
  selected.value?.reviewer_id === user.user?.id && featureAllowed(user, 'dashboard.review', user.hasCapability('report_review')),
)
const canWithdraw = computed(() =>
  selected.value?.submitted_by != null && selected.value.submitted_by === user.user?.id &&
  featureAllowed(user, 'dashboard.design', ['admin', 'developer'].includes(user.role)),
)
const showReviewActionReceipt = computed(() => !!reviewActionResult.value && reviewActionResult.value.reviewId === selectedId.value)
const reviewerCandidates = computed(() => admins.value.filter(candidate =>
  candidate.id !== selected.value?.reviewer_id && candidate.id !== selected.value?.submitted_by,
))
const validating = ref(false)
let alive = true
let versionRead: AbortController | undefined
let validationReadFailedFor: number | undefined
const reviewerRead = new AbortController()
const labels: Record<string, string> = {
  pending: '待审阅',
  approved: '已批准',
  rejected: '已退回',
  withdrawn: '已撤回',
  expired: '已过期',
}
const parameterTypes = { text: '文本', integer: '整数', decimal: '小数', date: '日期', datetime: '日期时间', boolean: '是／否', enum: '选项' }
function closePreview() {
  preview.value = false
  refreshId.value = ''
}
function togglePreview() {
  refreshId.value = ''
  preview.value = !preview.value
}
watch(selectedId, () => {
  summaryReady.value = false
  reviewerName.value = ''
  closePreview()
  reviewer.value = undefined
  confirmed.value = false
  comment.value = ''
}, { flush: 'sync' })

async function loadReviewers() {
  if (reviewerLoading.value) return
  reviewerLoading.value = true
  reviewerError.value = ''
  try {
    const response = await request.get<
      unknown,
      { items: { id: number; username: string; role: string;can_review_dashboards?:boolean }[] }
    >('/users/brief', { silentError: true, signal: reviewerRead.signal })
    if (alive) admins.value = response.items.filter((u) => u.can_review_dashboards ?? (u.role === 'admin'))
  } catch (e) {
    if (alive) {
      admins.value = []
      reviewer.value = undefined
      reviewerError.value = message(e)
    }
  } finally {
    if (alive) reviewerLoading.value = false
  }
}
onMounted(() => {
  void load()
  if (featureAllowed(user, 'dashboard.review', user.hasCapability('report_review'))) void loadReviewers()
})
onScopeDispose(() => {
  alive = false
  versionRead?.abort()
  reviewerRead.abort()
})
const message = (e: unknown) =>
  e instanceof Error ? e.message : '操作失败，请核对当前状态'
async function load() {
  versionRead?.abort()
  const read = new AbortController()
  versionRead = read
  readPending.value = true
  summaryReady.value = false
  reviewerName.value = ''
  confirmed.value = false
  closePreview()
  audienceOpen.value = false
  try {
    const [d, v] = await Promise.all([
      api.get(props.dashboard.id, read.signal),
      api.reviews(props.dashboard.id, read.signal),
    ])
    if (!read.signal.aborted) {
      readError.value = ''
      dashboard.value = d
      reviews.value = v.items
      summaryGeneration.value++
      if (!v.items.some((x) => x.id === selectedId.value))
        selectedId.value = v.items[0]?.id
      const latest = v.items.find((review) => review.id === d.review_id)
      if (latest?.reviewer_eligible !== undefined)
        emit('reviewerEligibility', d.id, latest.id, latest.reviewer_eligible)
    }
  } catch (e) {
    if (!read.signal.aborted) {
      reviews.value = []
      reviewer.value = undefined
      comment.value = ''
      readError.value = message(e)
    }
  } finally {
    if (!read.signal.aborted) readPending.value = false
  }
}
async function retryRead() {
  if (readPending.value) return
  const retry = retryVersionRead.value
  const restoreFocus = retry && document.activeElement === retry
  const reviewId = validationReadFailedFor
  await load()
  await nextTick()
  if (!alive) return
  if (!readError.value) validationReadFailedFor = undefined
  if (!restoreFocus || (document.activeElement !== document.body && document.activeElement !== retry)) return
  if (readError.value) retryVersionRead.value?.focus()
  else if (reviewId === selectedId.value && validationReceipt.value) validationReceipt.value.focus()
  else if (showReviewActionReceipt.value && reviewActionReceipt.value) reviewActionReceipt.value.focus()
  else if (versionSelect.value && !versionSelect.value.disabled) versionSelect.value.focus()
  else versionDescription.value?.focus()
}

async function action(action: string, payload: Record<string, unknown>, reassignment?: { trigger: HTMLElement; username: string }) {
  if (busy.value || readPending.value || readError.value || !selected.value) return
  const { id: reviewId, number } = selected.value
  const trigger = reassignment?.trigger ?? (action === 'validate' ? validateButton.value : undefined)
  const initiator = trigger === document.activeElement ? trigger : undefined
  validating.value = action === 'validate'
  busy.value = true
  error.value = ''
  if (reassignment) reviewActionResult.value = undefined
  try {
    await api.reviewCommand(
      reviewId,
      action,
      payload,
      keys.get(`${action}:${reviewId}`, payload),
    )
    keys.clear()
    if (alive) {
      const restoreFocus = initiator && document.activeElement === initiator
      const focusInsideVersion = validating.value && versionContent.value?.contains(document.activeElement)
      if (reassignment) {
        reviewer.value = undefined
        reviewActionResult.value = { reviewId, message: `v${number} · 已改派至 ${reassignment.username}` }
      }
      preview.value = false
      await load()
      await nextTick()
      if (!alive) return
      if (validating.value && readError.value) validationReadFailedFor = reviewId
      if (document.activeElement === document.body || document.activeElement === initiator) {
        if (readError.value && (restoreFocus || focusInsideVersion)) retryVersionRead.value?.focus()
        else if (restoreFocus && selectedId.value === reviewId) {
          const receipt = reassignment ? reviewActionReceipt.value : validationReceipt.value
          receipt?.focus()
        }
        else if (focusInsideVersion) versionDescription.value?.focus()
      }
      emit('changed')
    }
  } catch (e) {
    if (alive) {
      error.value = message(e)
      if (reassignment && initiator) {
        await nextTick()
        if (alive && document.activeElement === initiator) operationError.value?.focus()
      }
    }
  } finally {
    if (alive) {
      validating.value = false
      busy.value = false
    }
  }
}
async function validate() {
  if (!preview.value || !refreshId.value) return
  await action('validate', { refresh_id: refreshId.value })
}

async function reassign(event: MouseEvent) {
  const trigger = event.currentTarget
  const candidate = reviewerCandidates.value.find(user => user.id === reviewer.value)
  if (selected.value?.status !== 'pending' || reviewerLoading.value || reviewerError.value ||
    !candidate || !(trigger instanceof HTMLElement)) return
  await action('reassign', { reviewer_id: candidate.id }, { trigger, username: candidate.username })
}

async function decide(decision: 'rejected' | 'withdrawn', event: MouseEvent) {
  const version = selected.value
  const trigger = event.currentTarget
  const reason = decision === 'rejected' ? comment.value.trim() : '作者撤回'
  const permitted = () => decision === 'rejected' ? canReview.value : canWithdraw.value
  if (busy.value || readPending.value || readError.value || version?.status !== 'pending' ||
    !(trigger instanceof HTMLElement) || !reason || !permitted()) return

  const modalClass = 'dashboard-decision-confirmation'
  const canRestore = () => {
    const active = document.activeElement
    return active === trigger || active === document.body || !!active?.closest(`.${modalClass}`)
  }
  let restoreFocus = document.activeElement === trigger
  const label = decision === 'rejected' ? '退回' : '撤回'
  busy.value = true
  error.value = ''
  try {
    const confirmed = await ElMessageBox.confirm(`确认${label} v${version.number}？`, '仪表盘审批决定', {
      confirmButtonText: '确认', cancelButtonText: '取消', type: 'warning', modalClass,
    }).then(() => true, () => false)
    if (!confirmed || !alive || selected.value !== version || !permitted()) return
    reviewActionResult.value = undefined
    const payload = { decision, comment: reason }
    await api.reviewCommand(version.id, 'decision', payload, keys.get(`decision:${version.id}`, payload))
    keys.clear()
    if (!alive) return
    restoreFocus = restoreFocus && canRestore()
    reviewActionResult.value = { reviewId: version.id, message: `v${version.number} · ${labels[decision]}` }
    await load()
    if (alive) emit('changed')
  } catch (e) {
    if (alive) error.value = message(e)
  } finally {
    if (alive) {
      busy.value = false
      await nextTick()
      if (alive && restoreFocus && canRestore()) {
        const target = operationError.value ?? retryVersionRead.value ?? reviewActionReceipt.value ?? trigger
        target.focus()
      }
    }
  }
}

async function release(kind: string, review_id: number, event: MouseEvent) {
  if (busy.value || readPending.value || readError.value || (kind !== 'unpublish' && staleApproval.value)) return
  const trigger = event.currentTarget
  const restoreFocus = trigger instanceof HTMLElement && document.activeElement === trigger
  const canRestore = () => {
    const active = document.activeElement
    return active === trigger || active === document.body || !!active?.closest('.dashboard-release-confirmation')
  }
  busy.value = true
  error.value = ''
  releaseMessage.value = ''
  try {
    await ElMessageBox.confirm(
      kind === 'unpublish'
        ? '下线后所有人都无法继续刷新此看板。'
        : kind === 'rollback'
          ? '恢复所选历史布局和受众？卡片仍跟随兼容报表的当前发布。'
          : '按冻结布局、契约和受众发布？发布前会重新检查报表与授权。',
      '确认发布操作',
      { modalClass: 'dashboard-release-confirmation' },
    )
    if (!alive) return
    const payload = {
      expected_publication_no: dashboard.value.publication_no,
      review_id,
      kind,
    }
    const result = await api.command(
      dashboard.value.id,
      'releases',
      payload,
      keys.get('release', payload),
    )
    keys.clear()
    if (alive) {
      const retainedFocus = restoreFocus && canRestore()
      const actionLabel = kind === 'rollback' ? '回滚' : kind === 'unpublish' ? '下线' : '发布'
      releaseMessage.value = `${actionLabel}已确认 · 发布记录 #${result.version}`
      await load()
      if (!alive) return
      emit('changed')
      await nextTick()
      if (alive && retainedFocus && canRestore()) {
        const target = retryVersionRead.value ?? releaseReceipt.value
        target?.focus()
      }
    }
  } catch (e) {
    if (!(e instanceof Error) || !alive) return
    const retainedFocus = restoreFocus && canRestore()
    error.value = message(e)
    if (e instanceof ApiRequestError && e.status === 409 && kind !== 'unpublish') {
      await load()
      if (!alive) return
      if (staleApproval.value) error.value = ''
      if (!readError.value) emit('changed')
    }
    await nextTick()
    if (alive && retainedFocus && canRestore()) {
      const target = staleApprovalNotice.value ?? retryVersionRead.value ?? operationError.value
      target?.focus()
    }
  } finally {
    if (alive) busy.value = false
  }
}
</script>

<template>
  <el-dialog
    v-model="dialogOpen"
    title="仪表盘版本、审阅与发布"
    width="min(1200px,calc(100vw - 24px))"
    :close-on-click-modal="false"
    :show-close="!busy"
    :close-on-press-escape="!busy"
    @closed="emit('close')"
  >
    <p v-if="error" ref="operationError" role="alert" tabindex="-1" class="text-danger text-sm ui-focus-target">{{ error }}</p>
    <p v-if="releaseMessage" ref="releaseReceipt" role="status" tabindex="-1" class="text-success text-sm ui-focus-target">{{ releaseMessage }}</p>
    <p v-if="reviewActionResult && showReviewActionReceipt" ref="reviewActionReceipt" role="status" tabindex="-1" class="text-success text-sm ui-focus-target">{{ reviewActionResult.message }}</p>
    <LoadingState v-if="readPending && !readError" label="正在读取仪表盘版本…" variant="detail" />
    <PageState v-if="readError" heading="h3" :title="readPending ? '正在重试读取仪表盘版本…' : '读取仪表盘版本失败'"
      :description="readPending ? '' : readError" :loading="readPending" tone="danger">
      <template #actions><button ref="retryVersionRead" class="ghost-button ui-control-target"
        :aria-disabled="readPending || undefined" @click="retryRead">重试读取版本</button></template>
    </PageState>
    <div v-else-if="!readPending || (selected && (validating || (busy && showReviewActionReceipt)))" ref="versionContent" class="space-y-4">
      <button
        v-if="dashboard.active_release_id&&featureAllowed(user,'dashboard.design',true)"
        class="ghost-button text-xs ui-control-target"
        :disabled="busy"
        @click="audienceOpen = true"
      >
        调整正式看板受众
      </button>
      <p ref="versionDescription" tabindex="-1" class="text-xs text-muted ui-focus-target">
        审批固定布局、报表契约与受众。正式查看跟随兼容的报表发布；批准后仍需显式发布。归档后恢复必须重新审批。
      </p>
      <p v-if="staleApproval" ref="staleApprovalNotice" role="status" tabindex="-1" class="text-warning text-sm ui-focus-target">
        原审阅人已失去当前审阅资格，请由作者重新送审。
      </p>
      <div class="flex flex-wrap gap-2 items-center">
        <select ref="versionSelect" v-model="selectedId" aria-label="选择仪表盘版本" class="glass-input max-w-full" :disabled="busy || !reviews.length">
          <option v-for="version in reviews" :key="version.id" :value="version.id">v{{ version.number }} · {{ labels[version.status] || version.status }}</option>
        </select>
        <button class="ghost-button text-xs ui-control-target" :disabled="busy" @click="load">
          刷新状态</button
        ><button
          v-if="featureAllowed(user,'dashboard.offline',user.hasCapability('report_review')) && dashboard.publication_no > 0"
          class="ghost-button text-xs ui-control-target"
          :disabled="busy"
          @click="release('unpublish', 0, $event)"
        >
          下线当前发布
        </button>
      </div>
      <section v-if="selected" class="space-y-4">
        <div class="grid gap-4 lg:grid-cols-2">
        <div class="glass-panel p-4 space-y-2">
          <h3 class="font-semibold">
            {{ selected.definition.name }} · v{{ selected.number }}
          </h3>
          <p class="text-xs text-muted mt-2">
            提交人 {{ selected.submitter_name }} · 指定审阅人
            {{ reviewerName || '读取中…' }} · {{ labels[selected.status] }} ·
            审批到期
            {{ new Date(selected.expires_at).toLocaleString() }}
          </p>
          <p v-if="selected.comment" class="text-sm mt-2">
            审阅意见：{{ selected.comment }}
          </p>
        </div>
        <section class="glass-panel p-4 space-y-2" aria-label="冻结参数">
          <h3 class="font-semibold">参数配置</h3>
          <div v-if="selected.definition.parameters?.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="冻结参数表格">
            <table class="editorial-table min-w-[360px]" aria-label="仪表盘冻结参数">
              <thead><tr><th scope="col">参数</th><th scope="col">类型</th><th scope="col">默认值</th><th scope="col">必填</th></tr></thead>
              <tbody><tr v-for="parameter in selected.definition.parameters" :key="parameter.name">
                <td>{{ parameter.label }}</td><td>{{ parameterTypes[parameter.type] }}</td><td class="break-all">{{ parameter.default_relative_days === undefined ? parameter.default ?? '—' : '相对日 ' + parameter.default_relative_days }}</td><td>{{ parameter.required ? '是' : '否' }}</td>
              </tr></tbody>
            </table>
          </div>
          <p v-else class="text-sm text-muted">此冻结版本未定义全局参数。</p>
        </section>
        </div>
        <DashboardReviewSummary
          :key="`${selected.id}:${summaryGeneration}`"
          :review-id="selected.id"
          @loaded="summaryLoaded"
        />
        <details class="text-xs text-muted">
          <summary class="cursor-pointer flex items-center ui-control-target">
            技术详情（冻结定义）
          </summary>
          <pre class="whitespace-pre-wrap overflow-auto max-h-72 p-3">{{
            JSON.stringify(selected.definition, null, 2)
          }}</pre>
        </details>
        <template v-if="selected.status === 'pending'">
          <div v-if="featureAllowed(user,'dashboard.review',user.hasCapability('report_review'))" class="flex flex-wrap gap-2">
            <el-select
              v-model="reviewer"
              filterable
              aria-label="重分配审阅人"
              placeholder="选择其他审阅人"
              :disabled="busy || reviewerLoading || !!reviewerError"
              ><el-option
                v-for="u in reviewerCandidates"
                :key="u.id"
                :value="u.id"
                :label="u.username" /></el-select
            ><button
              class="ghost-button text-xs ui-control-target"
              :disabled="!reviewer || reviewerLoading || !!reviewerError"
              :aria-disabled="busy || undefined"
              @click="reassign"
            >
              重分配审阅人
            </button>
          </div>
          <div v-if="reviewerError" class="flex flex-wrap items-center gap-2">
            <p role="alert" class="text-danger text-sm">{{ reviewerError }}</p>
            <button class="ghost-button ui-control-target" :disabled="reviewerLoading || busy" @click="loadReviewers">重试读取审阅人</button>
          </div>
          <div
            v-if="canReview"
            class="space-y-3"
          >
            <button
              class="ghost-button ui-control-target"
              :disabled="busy"
              @click="togglePreview"
            >
              {{ preview ? '关闭真实预览' : '运行冻结版本真实预览' }}
            </button>
            <DashboardViewer
              v-if="preview"
              :key="selected.id"
              :id="dashboard.id"
              mode="review"
              :review-id="selected.id"
              @close="closePreview"
              @review-evidence="refreshId = $event"
            />
            <button
              ref="validateButton"
              class="ghost-button text-xs ui-control-target"
              :disabled="!validating && (busy || !preview || !refreshId)"
              :aria-disabled="validating || undefined"
              @click="validate"
            >
              登记本轮全部卡片验证
            </button>
            <p v-if="!readPending && selected.validated_at" ref="validationReceipt" role="status" tabindex="-1" class="text-xs text-success ui-focus-target">
              验证时间
              {{
                new Date(selected.validated_at).toLocaleString()
              }}，15分钟内可批准；报表发布、账号代次或授权变化后需要重新验证。
            </p>
            <label class="text-xs flex gap-2"
              ><input
                v-model="confirmed"
                type="checkbox"
                :disabled="busy || !summaryReady"
              />已核对卡片样本、口径、比较组、时效策略和全部受众覆盖</label
            >
            <label class="block text-xs"
              >审阅意见（退回必填）<el-input
                v-model="comment"
                aria-label="仪表盘审阅意见"
                maxlength="1000"
                :disabled="busy"
            /></label>
            <div class="flex flex-wrap gap-2">
              <button
                class="liquid-button ui-control-target"
                :disabled="
                  busy || !summaryReady || !confirmed || !selected.validated_at
                "
                @click="action('decision', { decision: 'approved', comment })"
              >
                批准此版本</button
              ><button
                class="ghost-button ui-control-target"
                :disabled="!comment.trim()"
                :aria-disabled="busy || undefined"
                @click="decide('rejected', $event)"
              >
                退回修改
              </button>
            </div>
          </div>
          <button
            v-if="canWithdraw"
            class="ghost-button text-xs ui-control-target"
            :aria-disabled="busy || undefined"
            @click="decide('withdrawn', $event)"
          >
            撤回待审版本
          </button>
        </template>
        <div
          v-if="selected.status === 'approved' && !staleApproval && featureAllowed(user,'dashboard.publish',user.hasCapability('report_review'))"
          class="flex flex-wrap gap-2"
        >
          <button
            class="liquid-button ui-control-target"
            :disabled="
              busy ||
              !summaryReady ||
              selected.lifecycle_epoch !== dashboard.lifecycle_epoch
            "
            @click="release('publish', selected.id, $event)"
          >
            发布此版本</button
          ><button
            v-if="selected.was_published"
            class="ghost-button ui-control-target"
            :disabled="
              busy ||
              !summaryReady ||
              selected.lifecycle_epoch !== dashboard.lifecycle_epoch
            "
            @click="release('rollback', selected.id, $event)"
          >
            回滚到此版本
          </button>
        </div>
      </section>
      <p v-else class="text-sm text-muted">尚未提交审批版本。</p>
    <DashboardAudienceDialog
      v-if="audienceOpen"
      :id="dashboard.id"
      @close="audienceOpen = false"
      @submitted="
        () => {
          audienceOpen = false
          selectedId = undefined
          load()
          emit('changed')
        }
      "
    />

</div>
</el-dialog>
</template>
