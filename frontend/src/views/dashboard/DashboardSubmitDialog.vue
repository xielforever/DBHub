<template>
  <el-dialog
    :model-value="true"
    title="仪表盘送审检查"
    width="min(780px,calc(100vw - 24px))"
    :close-on-click-modal="false"
    :close-on-press-escape="!submitting"
    :show-close="!submitting"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 class="font-semibold">{{ dashboard.draft.name }}</h3>
          <p class="text-xs text-white/60 mt-1">
            检查已保存草稿
            {{ dashboard.draft_version }}；提交时重新核对当前权限和口径。
          </p>
        </div>
        <button
          class="ghost-button"
          :disabled="checking || submitting"
          @click="load"
        >
          {{ checking ? '检查中…' : '重新检查' }}
        </button>
      </div>
      <p v-if="error" role="alert" class="text-sm text-rose-200">{{ error }}</p>
      <p v-if="checking" role="status" class="text-sm text-white/70">
        正在检查卡片、受众覆盖和独立审阅人…
      </p>
      <template v-if="check">
        <div
          class="rounded-xl border p-3"
          :class="
            check.can_submit
              ? 'border-emerald-400/30 bg-emerald-400/5'
              : 'border-amber-400/30 bg-amber-400/5'
          "
          role="status"
        >
          <strong>{{
            check.can_submit
              ? '送审条件已满足'
              : `有 ${blockers.length} 项问题需要处理`
          }}</strong
          ><span class="text-xs text-white/65 ml-2"
            >合格审阅人 {{ check.eligible_reviewers.length }} 位<span
              v-if="warnings.length"
            >
              · {{ warnings.length }} 项提示</span
            ></span
          >
        </div>
        <div
          v-if="check.issues.length"
          class="space-y-3 max-h-[48vh] overflow-y-auto pr-1"
          aria-label="送审检查结果"
        >
          <article
            v-for="(issue, index) in check.issues"
            :key="index"
            class="rounded-xl border border-white/10 p-3 space-y-2"
          >
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <h4 class="font-medium text-sm">
                  {{
                    issue.report_name ||
                    (issue.tile_key ? tileName(issue.tile_key) : '看板检查')
                  }}
                  <span
                    class="text-xs"
                    :class="
                      issue.severity === 'error'
                        ? 'text-rose-200'
                        : 'text-amber-200'
                    "
                    >{{ issue.severity === 'error' ? '需处理' : '提示' }}</span
                  >
                </h4>
                <p class="text-sm text-white/70 mt-1">{{ issue.message }}</p>
              </div>
              <button
                v-if="issue.action === 'request_access' && issue.tile_key"
                class="ghost-button text-xs shrink-0"
                :disabled="submitting"
                @click="accessIssue = issue"
              >
                申请补权</button
              ><button
                v-else-if="
                  ['edit', 'adopt_contract'].includes(issue.action || '') &&
                  dashboard.owner_id === user.user?.id
                "
                class="ghost-button text-xs shrink-0"
                :disabled="submitting"
                @click="emit('edit', dashboard)"
              >
                {{
                  issue.action === 'adopt_contract' ? '核对口径' : '编辑草稿'
                }}
              </button>
            </div>
            <p
              v-if="issue.users?.length"
              class="text-xs text-white/65 break-words"
            >
              个人：{{ issue.users.map((u) => u.name).join('、') }}
            </p>
            <p
              v-if="issue.groups?.length"
              class="text-xs text-white/65 break-words"
            >
              用户组：{{ issue.groups.map((g) => g.name).join('、') }}
            </p>
          </article>
        </div>
        <label class="block text-sm"
          >独立审阅人<el-select
            v-model="reviewer"
            aria-label="仪表盘提交审阅人"
            placeholder="选择具备全部报表查看权的管理员"
            class="w-full mt-2"
            :disabled="
              checking || submitting || !check.eligible_reviewers.length
            "
            ><el-option
              v-for="candidate in check.eligible_reviewers"
              :key="candidate.id"
              :value="candidate.id"
              :label="candidate.name" /></el-select
        ></label>
        <p
          v-if="!check.eligible_reviewers.length"
          class="text-xs text-white/65"
        >
          请联系管理员配置另一位有效管理员，并通过报表审批发布授予其全部引用报表的查看权。
        </p>
      </template>
      <div class="flex justify-end gap-2 pt-3 border-t border-white/10">
        <button
          class="ghost-button"
          :disabled="submitting"
          @click="emit('close')"
        >
          返回</button
        ><button
          class="liquid-button"
          :disabled="checking || submitting || !check?.can_submit || !reviewer"
          @click="submit"
        >
          {{ submitting ? '提交中…' : '提交审阅' }}
        </button>
      </div>
    </div>
    <DashboardAccessDialog
      v-if="accessIssue?.tile_key"
      :dashboard="dashboard"
      :tile="accessIssue.tile_key"
      :user-ids="accessIssue.users?.map((u) => u.id) || []"
      :group-ids="accessIssue.groups?.map((g) => g.id) || []"
      @close="accessIssue = undefined"
      @submitted="accessSubmitted"
    />
  </el-dialog>
</template>
<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import {
  managedDashboardApi as api,
  type ManagedDashboard,
  type DashboardPreflight,
  type DashboardIssue,
} from '../../api/managed-dashboard'
import { useUserStore } from '../../stores/user'
import { reportCommandKeys } from '../report/reportCommands'
import DashboardAccessDialog from './DashboardAccessDialog.vue'
const props = defineProps<{ dashboard: ManagedDashboard }>()
const emit = defineEmits<{
  close: []
  submitted: []
  edit: [dashboard: ManagedDashboard]
}>()
const user = useUserStore(),
  keys = reportCommandKeys(),
  dashboard = ref(props.dashboard),
  check = ref<DashboardPreflight>(),
  reviewer = ref<number>(),
  checking = ref(false),
  submitting = ref(false),
  error = ref(''),
  accessIssue = ref<DashboardIssue>()
const blockers = computed(
    () => check.value?.issues.filter((i) => i.severity === 'error') || [],
  ),
  warnings = computed(
    () => check.value?.issues.filter((i) => i.severity === 'warning') || [],
  )
let alive = true,
  generation = 0,
  controller = new AbortController()
onMounted(load)
onScopeDispose(() => {
  alive = false
  generation++
  controller.abort()
})
function tileName(key: string) {
  return `卡片 ${dashboard.value.draft.tiles.findIndex((t) => t.key === key) + 1}`
}
async function load() {
  if (submitting.value) return
  const current = ++generation
  controller.abort()
  controller = new AbortController()
  checking.value = true
  check.value = undefined
  error.value = ''
  try {
    const d = await api.get(props.dashboard.id)
    if (!alive || current !== generation) return
    const result = await api.preflight(d.id, d.draft_version, controller.signal)
    if (!alive || current !== generation) return
    dashboard.value = d
    check.value = result
    if (!result.eligible_reviewers.some((r) => r.id === reviewer.value))
      reviewer.value =
        result.eligible_reviewers.length === 1
          ? result.eligible_reviewers[0]!.id
          : undefined
  } catch (e) {
    if (alive && current === generation)
      error.value = e instanceof Error ? e.message : '检查失败，请重试'
  } finally {
    if (alive && current === generation) checking.value = false
  }
}
async function accessSubmitted() {
  accessIssue.value = undefined
  await load()
}
async function submit() {
  if (
    checking.value ||
    submitting.value ||
    !check.value?.can_submit ||
    !reviewer.value
  )
    return
  submitting.value = true
  error.value = ''
  const payload = {
    version: check.value.draft_version,
    reviewer_id: reviewer.value,
  }
  try {
    await api.command(
      dashboard.value.id,
      'submit',
      payload,
      keys.get('submit', payload),
    )
    if (alive) emit('submitted')
  } catch (e) {
    if (alive) {
      error.value =
        e instanceof Error ? e.message : '提交失败，请核对状态后重新检查'
      check.value = undefined
    }
  } finally {
    if (alive) submitting.value = false
  }
}
</script>
<style scoped>
button {
  min-height: 44px;
}
</style>
