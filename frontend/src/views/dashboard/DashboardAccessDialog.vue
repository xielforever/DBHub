<template>
  <el-dialog
    :model-value="true"
    title="为拟受众申请子报表权限"
    width="min(620px,calc(100vw - 24px))"
    :close-on-click-modal="false"
    :show-close="!busy"
    @close="emit('close')"
  >
    <form class="space-y-4" @submit.prevent="submit">
      <p class="text-sm text-white/65">
        申请仅限已保存草稿的这张卡片和拟受众，每次最多50个目标。它不会授予权限；合格管理员需创建报表受众提案，交另一位管理员验证、批准并发布。已具有权限的目标会记录为已满足。
      </p>
      <label class="block text-xs"
        >拟个人受众<el-select
          v-model="users"
          multiple
          aria-label="看板补权个人受众"
          class="w-full"
          :disabled="busy"
          ><el-option
            v-for="id in dashboard.draft.user_ids"
            :key="id"
            :value="id"
            :label="`用户 #${id}`" /></el-select
      ></label>
      <label class="block text-xs"
        >拟用户组受众<el-select
          v-model="groups"
          multiple
          aria-label="看板补权用户组受众"
          class="w-full"
          :disabled="busy"
          ><el-option
            v-for="id in dashboard.draft.group_ids"
            :key="id"
            :value="id"
            :label="`用户组 #${id}`" /></el-select
      ></label>
      <label class="block text-xs"
        >业务用途<el-input
          v-model="reason"
          aria-label="拟受众报表补权理由"
          maxlength="1000"
          :disabled="busy"
      /></label>
      <p class="text-xs text-amber-200">
        生效的是独立报表查看权；看板移除卡片或下线不会自动撤销报表授权。
      </p>
      <p v-if="error" role="alert" class="text-sm text-rose-200">{{ error }}</p>
      <button
        class="liquid-button"
        :disabled="busy || !reason.trim() || (!users.length && !groups.length)"
      >
        提交站内补权申请
      </button>
    </form>
  </el-dialog>
</template>
<script setup lang="ts">
import { onScopeDispose, ref } from 'vue'
import {
  managedDashboardApi as api,
  type ManagedDashboard,
} from '../../api/managed-dashboard'
import { reportCommandKeys } from '../report/reportCommands'
const props = defineProps<{ dashboard: ManagedDashboard; tile: string; userIds?: number[]; groupIds?: number[] }>(),
  emit = defineEmits<{ close: []; submitted: [] }>(),
  keys = reportCommandKeys()
const users = ref([...(props.userIds ?? props.dashboard.draft.user_ids)]),
  groups = ref([...(props.groupIds ?? props.dashboard.draft.group_ids)]),
  reason = ref(''),
  busy = ref(false),
  error = ref('')
let alive = true
onScopeDispose(() => {
  alive = false
})
async function submit() {
  busy.value = true
  error.value = ''
  const payload = {
    version: props.dashboard.draft_version,
    tile_key: props.tile,
    user_ids: users.value,
    group_ids: groups.value,
    reason: reason.value,
  }
  try {
    await api.requestAudienceAccess(
      props.dashboard.id,
      payload,
      keys.get('audience-access', payload),
    )
    if (alive) emit('submitted')
  } catch (e) {
    if (alive)
      error.value =
        e instanceof Error ? e.message : '申请失败，请核对当前草稿和受众'
  } finally {
    if (alive) busy.value = false
  }
}
</script>
