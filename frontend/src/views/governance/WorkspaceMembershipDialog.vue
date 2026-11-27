<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { directRoleApi, type DirectRoleMember, type WorkspaceMembershipOperation, type WorkspaceMemberCandidate } from '../../api/direct-roles'
import LoadingState from '../../components/base/LoadingState.vue'
import { useUserStore } from '../../stores/user'
import { newRequestId } from '../../utils/request-id'

const props = defineProps<{
  workspaceId: number
  workspaceName: string
  operation: 'join' | WorkspaceMembershipOperation
  member?: DirectRoleMember
  eligibleMembers?: DirectRoleMember[]
}>()
const emit = defineEmits<{ close: []; saved: [member: DirectRoleMember] }>()
const user = useUserStore()
const labels = {
  join: { title: '添加成员', submit: '确认添加', pending: '正在添加…' },
  remove: { title: '移出空间', submit: '确认移出', pending: '正在移出…' },
  appoint_admin: { title: '任命管理员', submit: '确认任命', pending: '正在任命…' },
  revoke_admin: { title: '撤销管理员资格', submit: '确认撤销', pending: '正在撤销…' },
} as const
const label = computed(() => labels[props.operation])
const keyword = ref('')
const candidates = ref<WorkspaceMemberCandidate[]>([])
const chosen = ref(0)
const loading = ref(false)
const saving = ref(false)
const readError = ref('')
const commandError = ref('')
let read: AbortController | undefined
let active = true
let request: { userId: number; version: number; id: string } | undefined

const member = computed(() => props.operation === 'appoint_admin'
  ? props.eligibleMembers?.find(item => item.user_id === chosen.value) : props.member)
const valid = computed(() => props.operation === 'join'
  ? candidates.value.some(item => item.user_id === chosen.value) : !!member.value)

async function searchAccounts() {
  if (props.operation !== 'join' || saving.value) return
  read?.abort()
  const attempt = new AbortController()
  read = attempt
  loading.value = true
  chosen.value = 0
  candidates.value = []
  readError.value = ''
  commandError.value = ''
  try {
    const result = await directRoleApi.candidates(props.workspaceId, keyword.value.trim(), attempt.signal)
    if (!attempt.signal.aborted) candidates.value = result ?? []
  } catch (failure) {
    if (!attempt.signal.aborted) readError.value = failure instanceof Error ? failure.message : '无法读取可添加的平台账号。'
  } finally {
    if (!attempt.signal.aborted) loading.value = false
  }
}

async function save() {
  if (!valid.value || loading.value || saving.value) return
  const operation = props.operation
  const target = member.value
  const userId = operation === 'join' ? chosen.value : target?.user_id ?? 0
  const version = target?.version ?? 0
  if (!request || request.userId !== userId || request.version !== version) request = { userId, version, id: newRequestId() }
  const id = request.id
  saving.value = true
  commandError.value = ''
  try {
    const result = operation === 'join'
      ? await directRoleApi.join(props.workspaceId, { user_id: userId, request_id: id })
      : await directRoleApi.changeMembership(props.workspaceId, userId, { expected_version: version, operation, request_id: id })
    if (active) emit('saved', result)
  } catch (failure) {
    if (active) commandError.value = failure instanceof Error ? failure.message : '成员操作未确认，请核对后重试。'
  } finally {
    if (active) saving.value = false
  }
}

onMounted(() => void searchAccounts())
onScopeDispose(() => { active = false; read?.abort() })
</script>

<template>
  <el-dialog :model-value="true" :title="label.title" append-to-body align-center class="glass-dialog"
    width="min(560px, calc(100vw - 24px))" :close-on-click-modal="false" :close-on-press-escape="!saving"
    :show-close="!saving" @close="emit('close')">
    <form class="space-y-4" :aria-busy="saving" @submit.prevent="save">
      <dl class="space-y-2 text-sm">
        <div><dt class="text-muted">工作空间</dt><dd class="break-words font-medium">{{ workspaceName }}</dd></div>
        <div v-if="member && operation !== 'appoint_admin'"><dt class="text-muted">成员</dt><dd class="break-words font-medium">{{ member.username }}</dd></div>
        <div><dt class="text-muted">操作者</dt><dd class="break-words font-medium">{{ user.username }}</dd></div>
      </dl>
      <template v-if="operation === 'join'">
        <div class="flex flex-wrap items-end gap-2">
          <label class="min-w-0 flex-1 text-sm">搜索平台账号
            <input v-model="keyword" type="search" class="glass-input mt-1 block w-full" autocomplete="off" maxlength="100"
              :disabled="saving" @keydown.enter.prevent="searchAccounts" />
          </label>
          <button type="button" class="ghost-button ui-control-target" :disabled="loading || saving" @click="searchAccounts">搜索</button>
        </div>
        <LoadingState v-if="loading" variant="detail" label="正在读取可添加的平台账号…" />
        <p v-else-if="readError" role="alert" class="break-words text-sm text-danger">{{ readError }}</p>
        <p v-else-if="!candidates.length" class="text-sm text-muted">暂无可添加的平台账号。</p>
        <label v-else class="block text-sm">平台账号
          <select v-model.number="chosen" class="glass-input mt-1 block w-full" :disabled="saving">
            <option :value="0">请选择平台账号</option>
            <option v-for="account in candidates" :key="account.user_id" :value="account.user_id">{{ account.username }}</option>
          </select>
        </label>
        <p class="text-sm text-muted">账号以普通成员身份加入，可在成员详情分配业务角色。</p>
      </template>
      <template v-else-if="operation === 'appoint_admin'">
        <label class="block text-sm">已有成员
          <select v-model.number="chosen" class="glass-input mt-1 block w-full" :disabled="saving || !eligibleMembers?.length">
            <option :value="0">请选择普通成员</option>
            <option v-for="item in eligibleMembers" :key="item.user_id" :value="item.user_id">{{ item.username }}</option>
          </select>
        </label>
        <p v-if="!eligibleMembers?.length" class="text-sm text-muted">暂无可任命的有效普通成员。</p>
      </template>
      <p v-else-if="operation === 'remove'" class="text-sm text-muted">移出后，该账号失去此工作空间的访问资格。</p>
      <p v-else class="text-sm text-muted">撤销后，该账号以普通成员身份保留在此工作空间。</p>
      <p v-if="commandError" class="editorial-field-error break-words" role="alert">{{ commandError }}</p>
      <div class="flex flex-wrap justify-end gap-2 border-t border-stroke pt-3">
        <button type="button" class="ghost-button ui-control-target" :disabled="saving" @click="emit('close')">取消</button>
        <button type="submit" class="liquid-button ui-control-target" :disabled="!valid || loading || saving">{{ saving ? label.pending : label.submit }}</button>
      </div>
    </form>
  </el-dialog>
</template>
