<template>
  <el-dialog
    :model-value="true"
    title="仪表盘版本、审阅与发布"
    width="min(1200px,calc(100vw - 24px))"
    :close-on-click-modal="false"
    :show-close="!busy"
    :close-on-press-escape="!busy"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <p v-if="error" role="alert" class="text-rose-200 text-sm">{{ error }}</p>
      <button
        v-if="dashboard.active_release_id"
        class="ghost-button text-xs"
        :disabled="busy"
        @click="audienceOpen = true"
      >
        调整正式看板受众
      </button>
      <p class="text-xs text-white/60">
        审批固定布局、报表契约与受众。正式查看跟随兼容的报表发布；批准后仍需显式发布。归档后恢复必须重新审批。
      </p>
      <div class="flex flex-wrap gap-2 items-center">
        <el-select
          v-model="selectedId"
          aria-label="选择仪表盘版本"
          :disabled="busy"
          ><el-option
            v-for="v in reviews"
            :key="v.id"
            :value="v.id"
            :label="`v${v.number} · ${labels[v.status] || v.status}`" /></el-select
        ><button class="ghost-button text-xs" :disabled="busy" @click="load">
          刷新状态</button
        ><button
          v-if="user.role === 'admin' && dashboard.publication_no > 0"
          class="ghost-button text-xs"
          :disabled="busy"
          @click="release('unpublish', 0)"
        >
          下线当前发布
        </button>
      </div>
      <section v-if="selected" class="space-y-3">
        <div class="glass-panel p-4">
          <h3 class="font-semibold">
            {{ selected.definition.name }} · v{{ selected.number }}
          </h3>
          <p class="text-xs text-white/60 mt-2">
            提交人 {{ selected.submitter_name }} · 指定审阅人
            {{ reviewerName || '读取中…' }} · {{ labels[selected.status] }} ·
            审批到期
            {{ new Date(selected.expires_at).toLocaleString() }}
          </p>
          <p v-if="selected.comment" class="text-sm mt-2">
            审阅意见：{{ selected.comment }}
          </p>
        </div>
        <DashboardReviewSummary
          :key="`${selected.id}:${summaryGeneration}`"
          :review-id="selected.id"
          @loaded="summaryLoaded"
        />
        <details class="text-xs text-white/70">
          <summary class="cursor-pointer min-h-11 flex items-center">
            技术详情（冻结定义）
          </summary>
          <pre class="whitespace-pre-wrap overflow-auto max-h-72 p-3">{{
            JSON.stringify(selected.definition, null, 2)
          }}</pre>
        </details>
        <template v-if="selected.status === 'pending'">
          <div v-if="user.role === 'admin'" class="flex flex-wrap gap-2">
            <el-select
              v-model="reviewer"
              filterable
              aria-label="重分配审阅人"
              placeholder="选择其他管理员"
              ><el-option
                v-for="u in admins"
                :key="u.id"
                :value="u.id"
                :label="u.username" /></el-select
            ><button
              class="ghost-button text-xs"
              :disabled="busy || !reviewer"
              @click="action('reassign', { reviewer_id: reviewer })"
            >
              重分配审阅人
            </button>
          </div>
          <div
            v-if="
              selected.reviewer_id === user.user?.id && user.role === 'admin'
            "
            class="space-y-3"
          >
            <button
              class="ghost-button"
              :disabled="busy"
              @click="preview = !preview"
            >
              {{ preview ? '关闭真实预览' : '运行冻结版本真实预览' }}
            </button>
            <DashboardViewer
              v-if="preview"
              :key="selected.id"
              :id="dashboard.id"
              mode="review"
              :review-id="selected.id"
              @close="preview = false"
              @refreshed="refreshId = $event"
            />
            <button
              class="ghost-button text-xs"
              :disabled="busy || !refreshId"
              @click="validate"
            >
              登记本轮全部卡片验证
            </button>
            <p v-if="selected.validated_at" class="text-xs text-emerald-200">
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
            <div class="flex gap-2">
              <button
                class="liquid-button"
                :disabled="
                  busy || !summaryReady || !confirmed || !selected.validated_at
                "
                @click="action('decision', { decision: 'approved', comment })"
              >
                批准此版本</button
              ><button
                class="ghost-button"
                :disabled="busy || !comment.trim()"
                @click="action('decision', { decision: 'rejected', comment })"
              >
                退回修改
              </button>
            </div>
          </div>
          <button
            v-if="dashboard.owner_id === user.user?.id"
            class="ghost-button text-xs"
            :disabled="busy"
            @click="
              () => {
                action('decision', {
                  decision: 'withdrawn',
                  comment: '作者撤回',
                })
              }
            "
          >
            撤回待审版本
          </button>
        </template>
        <div
          v-if="selected.status === 'approved' && user.role === 'admin'"
          class="flex flex-wrap gap-2"
        >
          <button
            class="liquid-button"
            :disabled="
              busy ||
              !summaryReady ||
              selected.lifecycle_epoch !== dashboard.lifecycle_epoch
            "
            @click="release('publish', selected.id)"
          >
            发布此版本</button
          ><button
            v-if="selected.was_published"
            class="ghost-button"
            :disabled="
              busy ||
              !summaryReady ||
              selected.lifecycle_epoch !== dashboard.lifecycle_epoch
            "
            @click="release('rollback', selected.id)"
          >
            回滚到此版本
          </button>
        </div>
      </section>
      <p v-else class="text-sm text-white/60">尚未提交审批版本。</p>
    </div>
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
  </el-dialog>
</template>
<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import { useUserStore } from '../../stores/user'
import request from '../../utils/request'
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
  emit = defineEmits<{ close: []; changed: [] }>(),
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
let alive = true,
  loadGeneration = 0
const labels: Record<string, string> = {
  pending: '待审阅',
  approved: '已批准',
  rejected: '已退回',
  withdrawn: '已撤回',
  expired: '已过期',
}
watch(selectedId, () => {
  summaryReady.value = false
  preview.value = false
  refreshId.value = ''
  confirmed.value = false
  comment.value = ''
})
onMounted(async () => {
  await load()
  try {
    const response = await request.get<
      unknown,
      { items: { id: number; username: string; role: string }[] }
    >('/users/brief')
    if (alive) admins.value = response.items.filter((u) => u.role === 'admin')
  } catch (e) {
    if (alive) error.value = message(e)
  }
})
onScopeDispose(() => {
  alive = false
  loadGeneration++
})
const message = (e: unknown) =>
  e instanceof Error ? e.message : '操作失败，请核对当前状态'
async function load() {
  const generation = ++loadGeneration
  summaryReady.value = false
  try {
    const [d, v] = await Promise.all([
      api.get(props.dashboard.id),
      api.reviews(props.dashboard.id),
    ])
    if (alive && generation === loadGeneration) {
      dashboard.value = d
      reviews.value = v.items
      summaryGeneration.value++
      if (!v.items.some((x) => x.id === selectedId.value))
        selectedId.value = v.items[0]?.id
    }
  } catch (e) {
    if (alive && generation === loadGeneration) error.value = message(e)
  }
}
async function action(action: string, payload: Record<string, unknown>) {
  if (!selected.value) return
  busy.value = true
  error.value = ''
  try {
    await api.reviewCommand(
      selected.value.id,
      action,
      payload,
      keys.get(`${action}:${selected.value.id}`, payload),
    )
    keys.clear()
    if (alive) {
      preview.value = false
      await load()
      emit('changed')
    }
  } catch (e) {
    if (alive) error.value = message(e)
  } finally {
    if (alive) busy.value = false
  }
}
async function validate() {
  await action('validate', { refresh_id: refreshId.value })
}
async function release(kind: string, review_id: number) {
  try {
    await ElMessageBox.confirm(
      kind === 'unpublish'
        ? '下线后所有人都无法继续刷新此看板。'
        : '按冻结布局、契约和受众发布？发布前会重新检查报表与授权。',
      '确认发布操作',
    )
    busy.value = true
    error.value = ''
    const payload = {
      expected_publication_no: dashboard.value.publication_no,
      review_id,
      kind,
    }
    await api.command(
      dashboard.value.id,
      'releases',
      payload,
      keys.get('release', payload),
    )
    keys.clear()
    if (alive) {
      await load()
      emit('changed')
    }
  } catch (e) {
    if (e instanceof Error && alive) error.value = message(e)
  } finally {
    if (alive) busy.value = false
  }
}
</script>
