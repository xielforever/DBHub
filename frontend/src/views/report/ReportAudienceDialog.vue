<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import { onMounted, onScopeDispose, ref } from 'vue'
import request from '../../utils/request'
import { managedDashboardApi as api } from '../../api/managed-dashboard'
import { reportCommandKeys } from './reportCommands'
const props = defineProps<{ reportId: number }>(),
  emit = defineEmits<{ close: []; submitted: [id: number] }>(),
  keys = reportCommandKeys()
const base = ref(0),
  accountId = ref(0),
  userIds = ref<number[]>([]),
  groupIds = ref<number[]>([]),
  users = ref<{ id: number; username: string }[]>([]),
  groups = ref<{ id: number; name: string }[]>([]),
  busy = ref(false),
  error = ref(''),
  readError = ref(''),
  readPending = ref(false)
let alive = true
const message = (e: unknown) =>
  e instanceof Error ? e.message : '受众提案提交失败，请核对状态'
async function load() {
  if (readPending.value || busy.value) return
  readPending.value = true
  readError.value = ''
  base.value = 0
  try {
    const [current, u, g] = await Promise.all([
      api.currentAudience(props.reportId),
      request.get<unknown, { items: { id: number; username: string }[] }>(
        '/users/brief', { silentError: true },
      ),
      request.get<unknown, { items: { id: number; name: string }[] }>(
        '/audience-groups', { silentError: true },
      ),
    ])
    if (alive) {
      base.value = current.base_publication_no
      accountId.value = current.account_id
      userIds.value = current.user_ids
      groupIds.value = current.group_ids
      users.value = u.items
      groups.value = g.items
    }
  } catch (e) {
    if (alive) readError.value = e instanceof Error ? e.message : '无法核对当前正式发布与受众。'
  } finally {
    if (alive) readPending.value = false
  }
}
onMounted(load)
onScopeDispose(() => {
  alive = false
})
async function submit() {
  if (busy.value || readPending.value || readError.value || !base.value || (!userIds.value.length && !groupIds.value.length)) return
  busy.value = true
  error.value = ''
  const payload = {
    base_publication_no: base.value,
    user_ids: [...userIds.value],
    group_ids: [...groupIds.value],
    request_ids: [],
  }
  try {
    const out = await api.audienceChange(
      props.reportId,
      payload,
      keys.get('audience', payload),
    )
    if (alive) emit('submitted', out.id)
  } catch (e) {
    if (alive) error.value = message(e)
  } finally {
    if (alive) busy.value = false
  }
}
</script>

<template>
  <el-dialog
    :model-value="true"
    title="仅调整正式报表受众"
    width="min(620px,calc(100vw - 24px))"
    :close-on-click-modal="false"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    @close="emit('close')"
  >
    <LoadingState v-if="readPending" label="正在核对当前正式发布和受众…" />
    <PageState v-else-if="readError" title="读取正式报表受众失败" :description="readError" tone="danger">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="load">重试读取受众</button></template>
    </PageState>
    <form v-else-if="base" class="space-y-4 min-w-0" @submit.prevent="submit">
      <p class="text-sm text-muted">
        以当前正式发布 #{{ base }} 为基准，复制其
        SQL、图表、契约和执行账号。仅修改受众，无需作者个人连接。提交后需另一位合格管理员真实验证、批准并正式发布。
      </p>
      <dl class="grid grid-cols-1 gap-2 border-y border-stroke py-3 text-sm sm:grid-cols-2" aria-label="正式执行基准">
        <div><dt class="text-muted">基准发布</dt><dd>#{{ base }}</dd></div>
        <div v-if="accountId"><dt class="text-muted">执行账号编号</dt><dd>#{{ accountId }}</dd></div>
      </dl>
      <label class="block text-xs"
        >个人受众<el-select
          v-model="userIds"
          multiple
          filterable
          aria-label="报表受众变更个人授权"
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
          aria-label="报表受众变更组授权"
          class="w-full"
          :disabled="busy"
          ><el-option
            v-for="g in groups"
            :key="g.id"
            :value="g.id"
            :label="g.name" /></el-select
      ></label>
      <p class="text-xs text-warning">
        报表权限独立生效。移除看板卡片不会撤销报表权限；并发发布后需要重新生成提案，系统不会暗中合并受众。
      </p>
      <p v-if="error" class="text-sm text-danger" role="alert">{{ error }}</p>
      <div class="flex justify-end gap-2">
        <button
          type="button"
          class="ghost-button ui-control-target"
          :disabled="busy"
          @click="emit('close')"
        >
          取消</button
        ><button
          class="liquid-button ui-control-target"
          :disabled="busy || !base || (!userIds.length && !groupIds.length)"
        >
          提交受众审批提案
        </button>
      </div>
    </form>
  </el-dialog>
</template>
