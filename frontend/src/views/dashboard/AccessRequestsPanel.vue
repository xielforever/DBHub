<script setup lang="ts">
import { useReadActivity } from '../../composables/useReadActivity'
import { nextTick, onMounted, onScopeDispose, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import { useUserStore } from '../../stores/user'
import request from '../../utils/request'
import {
  managedDashboardApi as api,
  type AccessRequest,
} from '../../api/managed-dashboard'
import { reportCommandKeys } from '../report/reportCommands'
import ReportReviewDialog from '../report/ReportReviewDialog.vue'
const { busy: readPending, run: trackRead } = useReadActivity()
const user = useUserStore(),
  keys = reportCommandKeys(),
  items = ref<AccessRequest[]>([]),
  error = ref(''),
  busy = ref(false),
  reviewing = ref<{ reportId: number; requestId: number; reviewId: number }>(),
  proposal = ref<{
    request: AccessRequest
    base: number
    users: number[]
    groups: number[]
  }>(),
  users = ref<{ id: number; username: string }[]>([]),
  groups = ref<{ id: number; name: string }[]>([])
const listRegion = ref<HTMLElement>()
const refreshButton = ref<HTMLButtonElement>()
let alive = true
const labels: Record<string, string> = {
  pending: '待受理',
  processing: '审批处理中',
  approved_pending_release: '已批准，待正式发布',
  fulfilled: '权限已生效',
  rejected: '已拒绝',
  cancelled: '已撤回',
  expired: '已到期',
}
const active = (item: AccessRequest) =>
  ['pending', 'processing', 'approved_pending_release'].includes(item.status)
const message = (e: unknown) =>
  e instanceof Error ? e.message : '操作失败，请核对状态'
onMounted(load)
onScopeDispose(() => {
  alive = false
})
async function load() {
  if (readPending.value) return false
  error.value = ''
  return trackRead(async () => {
    try {
      const out = await api.accessRequests()
      if (!alive) return false
      items.value = out.items
      return true
    } catch (e) {
      if (alive) error.value = message(e)
      return false
    }
  })
}
async function act(
  item: AccessRequest,
  action: string,
  extra: Record<string, unknown>,
  trigger?: EventTarget | null,
) {
  if (busy.value || readPending.value) return
  busy.value = true
  error.value = ''
  const payload = { version: item.version, ...extra }
  let restoreFocus = false
  let refreshed = false
  try {
    await api.accessAction(
      item.id,
      action,
      payload,
      keys.get(`${action}:${item.id}`, payload),
    )
    keys.clear()
    if (!alive) return
    restoreFocus = !!trigger && document.activeElement === trigger
    refreshed = await load()
    if (alive && action === 'cancel' && !refreshed) {
      items.value = items.value.map(current =>
        current.id === item.id ? { ...current, status: 'cancelled' } : current,
      )
      error.value = '申请已撤回，读取申请列表失败。请刷新申请。'
    }
  } catch (e) {
    if (alive) error.value = message(e)
  } finally {
    if (alive) busy.value = false
  }
  if (!restoreFocus || !alive) return
  await nextTick()
  const focused = document.activeElement
  if (!alive || (focused !== trigger && focused !== listRegion.value && focused !== document.body)) return
  const status = refreshed
    ? listRegion.value?.querySelector<HTMLElement>(`[data-access-status="${item.id}"]`)
    : undefined
  const target = status ?? refreshButton.value
  target?.focus()
}
async function reject(item: AccessRequest) {
  try {
    const input = await ElMessageBox.prompt(
      '请填写拒绝理由。关联的未发布授权提案将被阻断。',
      '拒绝申请',
      { inputValidator: (v) => !!v?.trim() || '请填写理由' },
    )
    await act(item, 'reject', { reason: input.value })
  } catch {}
}
async function assign(item: AccessRequest) {
  try {
    const input = await ElMessageBox.prompt(
      '输入有效管理员用户编号。分配处理人不会授予数据访问权。',
      '分配处理人',
      { inputPattern: /^[1-9]\d*$/ },
    )
    await act(item, 'assign', { assigned_to: Number(input.value) })
  } catch {}
}
async function prepare(item: AccessRequest) {
  if (!item.report_id) return
  busy.value = true
  error.value = ''
  try {
    const [current, u, g] = await Promise.all([
      api.currentAudience(item.report_id),
      request.get<unknown, { items: { id: number; username: string }[] }>(
        '/users/brief',
      ),
      request.get<unknown, { items: { id: number; name: string }[] }>(
        '/audience-groups',
      ),
    ])
    if (alive) {
      users.value = u.items
      groups.value = g.items
      proposal.value = {
        request: item,
        base: current.base_publication_no,
        users: [
          ...new Set([
            ...current.user_ids,
            ...(item.target_user_id ? [item.target_user_id] : []),
          ]),
        ],
        groups: [
          ...new Set([
            ...current.group_ids,
            ...(item.target_group_id ? [item.target_group_id] : []),
          ]),
        ],
      }
    }
  } catch (e) {
    if (alive) error.value = message(e)
  } finally {
    if (alive) busy.value = false
  }
}
function openReview(item: AccessRequest, reviewId = item.review_id) {
  if (item.report_id && reviewId) {
    reviewing.value = { reportId: item.report_id, requestId: item.id, reviewId }
  }
}
async function closeReview() {
  const requestId = reviewing.value?.requestId
  reviewing.value = undefined
  await nextTick()
  if (!alive || reviewing.value || requestId === undefined) return
  const trigger = listRegion.value?.querySelector<HTMLButtonElement>(`[data-access-review="${requestId}"]`)
  const status = listRegion.value?.querySelector<HTMLElement>(`[data-access-status="${requestId}"]`)
  const target = trigger ?? status ?? refreshButton.value
  target?.focus()
}
async function createProposal() {
  if (!proposal.value?.request.report_id) return
  const p = proposal.value,
    payload = {
      base_publication_no: p.base,
      user_ids: p.users,
      group_ids: p.groups,
      request_ids: [p.request.id],
    }
  busy.value = true
  error.value = ''
  try {
    const result = await api.audienceChange(
      p.request.report_id!,
      payload,
      keys.get(`proposal:${p.request.id}`, payload),
    )
    keys.clear()
    if (alive) {
      openReview(p.request, result.id)
      proposal.value = undefined
      await load()
    }
  } catch (e) {
    if (alive) error.value = message(e)
  } finally {
    if (alive) busy.value = false
  }
}
</script>

<template>
  <section class="space-y-4" aria-label="站内访问申请">
    <div class="flex flex-wrap justify-between gap-3">
      <p class="text-sm text-muted">
        报表访问权独立于看板。批准后仍需正式发布并校验目标权限，申请才会完成。
      </p>
      <button ref="refreshButton" class="ghost-button text-xs ui-control-target" :disabled="busy" @click="load">
        刷新申请
      </button>
    </div>
    <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
    <div ref="listRegion" v-data-loading="readPending" data-loading-label="正在加载访问申请…" class="space-y-4">
<article
      v-for="item in items"
      :key="item.id"
      class="glass-panel p-4 space-y-3"
    >
      <div class="flex flex-wrap justify-between gap-3">
        <h3 class="font-semibold">申请 #{{ item.id }}</h3>
        <span class="text-sm text-accent ui-focus-target" role="status" tabindex="-1"
          :data-access-status="item.id" :aria-label="`申请 #${item.id} 状态`">{{
          labels[item.status] || item.status
        }}</span>
      </div>
      <p class="text-xs text-muted">
        申请人 #{{ item.requester_id }} · 到期
        {{ new Date(item.expires_at).toLocaleString()
        }}<span v-if="item.actual_publication_no">
          · 生效发布 #{{ item.actual_publication_no }}</span
        >
      </p>
      <p class="text-sm text-content">{{ item.reason }}</p>
      <p v-if="item.decision_reason" class="text-xs text-danger">
        处理理由：{{ item.decision_reason }}
      </p>
      <p class="text-xs text-muted">
        目标：{{
          item.target_group_id
            ? `用户组 #${item.target_group_id}`
            : `用户 #${item.target_user_id}`
        }}
      </p>
      <div v-if="active(item)" class="flex flex-wrap gap-2">
        <template v-if="user.hasCapability('report_review')"
          ><button
            class="ghost-button text-xs ui-control-target"
            :disabled="busy"
            @click="prepare(item)"
          >
            创建受众变更提案</button
          ><button
            class="ghost-button text-xs ui-control-target"
            :disabled="busy"
            @click="assign(item)"
          >
            分配处理人</button
          ><button
            class="ghost-button text-xs ui-control-target"
            :disabled="busy"
            @click="reject(item)"
          >
            拒绝申请</button
          ><button
            v-if="item.review_id && item.report_id"
            :data-access-review="item.id"
            class="ghost-button text-xs ui-control-target"
            @click="openReview(item)"
          >
            查看关联报表审批
          </button></template
        ><button
          v-if="item.requester_id === user.user?.id"
          class="ghost-button text-xs ui-control-target"
          :aria-disabled="busy || readPending || undefined"
          @click="act(item, 'cancel', {}, $event.currentTarget)"
        >
          撤回申请
        </button>
      </div>
    </article>
    <p v-if="!items.length" class="glass-panel p-8 text-muted">
      暂无访问申请。受限卡片可申请本人报表访问权。
    </p>
    <el-dialog
      :model-value="!!proposal"
      title="仅调整受众"
      width="min(700px,calc(100vw - 24px))"
      :close-on-click-modal="false"
      @close="proposal = undefined"
      ><div v-if="proposal" class="space-y-4">
        <p class="text-sm text-muted">
          服务端复制当前正式发布 #{{ proposal.base }} 的
          SQL、图表、契约和执行账号。此入口仅修改受众；需要另一位合格管理员验证、批准并发布。并发发布后必须基于最新受众重新提案。
        </p>
        <label class="block text-xs"
          >个人受众<el-select
            v-model="proposal.users"
            multiple
            filterable
            aria-label="受众变更个人授权"
            class="w-full"
            ><el-option
              v-for="u in users"
              :key="u.id"
              :value="u.id"
              :label="u.username" /></el-select></label
        ><label class="block text-xs"
          >用户组受众<el-select
            v-model="proposal.groups"
            multiple
            filterable
            aria-label="受众变更用户组授权"
            class="w-full"
            ><el-option
              v-for="g in groups"
              :key="g.id"
              :value="g.id"
              :label="g.name" /></el-select
        ></label>
        <p class="text-xs text-warning">
          权限生效后可以独立查看该报表；移除看板卡片不会撤回这项权限。
        </p>
        <p v-if="error" role="alert" class="text-sm text-danger">
          {{ error }}
        </p>
        <button class="liquid-button ui-control-target" :disabled="busy" @click="createProposal">
          提交独立审批提案
        </button>
      </div></el-dialog
    >

</div>
<ReportReviewDialog
      v-if="reviewing"
      :report-id="reviewing.reportId"
      :review-id="reviewing.reviewId"
      @close="closeReview"
      @changed="load"
    />
  </section>
</template>
