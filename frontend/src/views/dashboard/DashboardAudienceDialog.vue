<template>
  <el-dialog
    :model-value="true"
    title="调整仪表盘受众"
    width="min(620px,calc(100vw - 24px))"
    :close-on-click-modal="false"
    :show-close="!busy"
    @close="emit('close')"
  >
    <form class="space-y-4" @submit.prevent="submit">
      <p class="text-sm text-white/65">
        复制当前正式发布 #{{
          base
        }}
        的布局、卡片与质量策略。受众变更将生成新的冻结版本，需要另一位合格管理员真实预览并重新审批发布。
      </p>
      <label class="block text-xs"
        >个人受众<el-select
          v-model="userIds"
          multiple
          filterable
          aria-label="看板受众变更个人授权"
          class="w-full"
          :disabled="busy"
          ><el-option
            v-for="u in users"
            :key="u.id"
            :value="u.id"
            :label="u.username" /></el-select
      ></label>
      <label class="block text-xs"
        >用户组受众<el-select
          v-model="groupIds"
          multiple
          filterable
          aria-label="看板受众变更组授权"
          class="w-full"
          :disabled="busy"
          ><el-option
            v-for="g in groups"
            :key="g.id"
            :value="g.id"
            :label="g.name" /></el-select
      ></label>
      <label class="block text-xs"
        >独立审阅人<el-select
          v-model="reviewer"
          filterable
          aria-label="看板受众变更审阅人"
          class="w-full"
          :disabled="busy"
          ><el-option
            v-for="u in admins"
            :key="u.id"
            :value="u.id"
            :label="u.username" /></el-select
      ></label>
      <p class="text-xs text-amber-200">
        每张必需报表须独立覆盖全部目标受众；此提案不会自动授予子报表权限。并发发布后必须基于最新版本重新提案。
      </p>
      <p v-if="error" role="alert" class="text-sm text-rose-200">{{ error }}</p>
      <button class="liquid-button" :disabled="busy || !base || !reviewer">
        提交新版本审阅
      </button>
    </form>
  </el-dialog>
</template>
<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { useUserStore } from '../../stores/user'
import request from '../../utils/request'
import { managedDashboardApi as api } from '../../api/managed-dashboard'
import { reportCommandKeys } from '../report/reportCommands'
const props = defineProps<{ id: number }>(),
  emit = defineEmits<{ close: []; submitted: [] }>(),
  user = useUserStore(),
  keys = reportCommandKeys()
const base = ref(0),
  userIds = ref<number[]>([]),
  groupIds = ref<number[]>([]),
  users = ref<{ id: number; username: string; role: string }[]>([]),
  groups = ref<{ id: number; name: string }[]>([]),
  reviewer = ref<number>(),
  busy = ref(false),
  error = ref('')
let alive = true
const admins = computed(() =>
    users.value.filter((u) => u.role === 'admin' && u.id !== user.user?.id),
  ),
  message = (e: unknown) =>
    e instanceof Error ? e.message : '操作失败，请核对状态'
onMounted(async () => {
  try {
    const [current, u, g] = await Promise.all([
      api.dashboardAudience(props.id),
      request.get<unknown, { items: typeof users.value }>('/users/brief'),
      request.get<unknown, { items: typeof groups.value }>('/audience-groups'),
    ])
    if (alive) {
      base.value = current.base_publication_no
      userIds.value = current.user_ids
      groupIds.value = current.group_ids
      users.value = u.items
      groups.value = g.items
    }
  } catch (e) {
    if (alive) error.value = message(e)
  }
})
onScopeDispose(() => {
  alive = false
})
async function submit() {
  busy.value = true
  const payload = {
    base_publication_no: base.value,
    user_ids: userIds.value,
    group_ids: groupIds.value,
    reviewer_id: reviewer.value,
  }
  try {
    await api.changeDashboardAudience(
      props.id,
      payload,
      keys.get('audience', payload),
    )
    if (alive) emit('submitted')
  } catch (e) {
    if (alive) error.value = message(e)
  } finally {
    if (alive) busy.value = false
  }
}
</script>
