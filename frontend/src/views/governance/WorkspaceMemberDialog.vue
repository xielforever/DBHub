<script setup lang="ts">
import { computed, onScopeDispose, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import { useUserStore } from '../../stores/user'
import type { Member } from '../../types/iam'
import request from '../../utils/request'

const props = defineProps<{ workspaceId: number; workspaceName: string; member?: Member; dutiesLoading?: boolean }>()
const emit = defineEmits<{ close: []; saved: []; duties: [member: Member] }>()
const user = useUserStore()
const username = ref('')
const role = ref(props.member?.role ?? 'readonly')
const accessChange = ref<'keep' | 'enable' | 'disable'>('keep')
const saving = ref(false)
const failure = ref('')
const actor = user.user?.id
let active = true
const roles = [
  { id: 'readonly', name: '只读成员' },
  { id: 'developer', name: '开发者' },
  { id: 'admin', name: '管理员' },
]

const canSave = computed(() => roles.some(item => item.id === role.value) && (
  props.member ? role.value !== props.member.role || accessChange.value !== 'keep' : !!username.value.trim()
))
const formBusy = computed(() => saving.value || props.dutiesLoading)
function stillAllowed() {
  return active && user.user?.id === actor && user.workspaceId === props.workspaceId && user.role === 'admin' &&
    user.can('space.members.manage') && !user.effective?.snapshot.governance_only
}

async function save() {
  if (!canSave.value || formBusy.value || !stillAllowed()) return
  saving.value = true
  failure.value = ''
  try {
    const member = props.member
    const access = accessChange.value
    if (member && access === 'disable') {
      try {
        await ElMessageBox.confirm(`停用「${member.username}」的成员资格？此操作会移出该成员在本空间的用户组，并取消办理中的访问申请。名下仍有受管报表或生效仪表盘时，须先移交。`, '停用成员资格', {
          type: 'warning', confirmButtonText: '确认停用', cancelButtonText: '取消',
        })
      } catch { return }
    }
    if (!stillAllowed()) return
    const base = `/workspaces/${props.workspaceId}/members`
    if (member) {
      await request.patch(`${base}/${member.id}`, {
        expected_version: member.version,
        ...(role.value !== member.role ? { role: role.value } : {}),
        ...(access !== 'keep' ? { active: access === 'enable' } : {}),
      }, { silentError: true })
    } else {
      await request.post(base, { username: username.value.trim(), role: role.value }, { silentError: true })
    }
    if (active && user.user?.id === actor && user.workspaceId === props.workspaceId) emit('saved')
  } catch (error) {
    if (active) failure.value = error instanceof Error ? error.message : '成员资格暂未保存，请重试。'
  } finally {
    saving.value = false
  }
}

onScopeDispose(() => { active = false })
</script>

<template>
  <el-dialog :model-value="true" :title="member ? '编辑成员资格' : '添加成员'" append-to-body align-center destroy-on-close
    class="glass-dialog" width="min(560px, calc(100vw - 24px))" :close-on-click-modal="false"
    :style="{ maxHeight: 'calc(100dvh - 24px)', overflowY: 'auto' }"
    :close-on-press-escape="!saving" :show-close="!saving" @close="emit('close')">
    <form class="space-y-4" @submit.prevent="save">
      <dl class="grid gap-3 text-sm sm:grid-cols-2">
        <div><dt class="text-muted">工作空间</dt><dd class="break-words">{{ workspaceName }}</dd></div>
        <div v-if="member"><dt class="text-muted">平台账号</dt><dd class="break-words">{{ member.username }}</dd></div>
      </dl>
      <label v-if="!member" class="block text-sm">已有平台账号
        <input v-model.trim="username" class="glass-input mt-2 block w-full" required :disabled="saving" />
      </label>
      <label class="block text-sm">基础角色
        <select v-model="role" class="glass-input mt-2 block w-full" :disabled="formBusy">
          <option v-for="item in roles" :key="item.id" :value="item.id">{{ item.name }}</option>
        </select>
      </label>
      <label v-if="member" class="block text-sm">成员资格状态
        <select v-model="accessChange" class="glass-input mt-2 block w-full" :disabled="formBusy"
          :aria-describedby="accessChange === 'enable' ? 'member-enable-description' : undefined">
          <option value="keep">保持现有状态</option>
          <option value="disable">停用成员资格</option>
          <option value="enable">启用成员资格</option>
        </select>
        <span v-if="accessChange === 'enable'" id="member-enable-description" class="text-muted mt-2 block">空间访问仍受平台账号状态、有效期和冻结记录约束。</span>
      </label>
      <p v-if="failure" role="alert" class="text-sm text-danger break-words">{{ failure }}</p>
      <div class="flex flex-wrap justify-end gap-3">
        <button v-if="member" type="button" class="ghost-button ui-control-target" :disabled="formBusy"
          :aria-busy="dutiesLoading || undefined" @click="emit('duties', member)">{{ dutiesLoading ? '正在读取空间职责…' : '编辑空间职责' }}</button>
        <button type="button" class="ghost-button ui-control-target" :disabled="saving" @click="emit('close')">取消</button>
        <button type="submit" class="liquid-button ui-control-target" :disabled="formBusy || !canSave">
          {{ saving ? '正在保存…' : member ? '保存成员资格' : '添加到工作空间' }}
        </button>
      </div>
    </form>
  </el-dialog>
</template>
