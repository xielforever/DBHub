<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, watch } from 'vue'
import { canAssignWorkspaceBusinessRoles, directRoleApi, type DirectRoleDirectory } from '../../api/direct-roles'
import { useUserStore } from '../../stores/user'
import { reportCommandKeys } from '../report/reportCommands'

const props = defineProps<{ workspaceId: number; workspaceName: string; memberId?: number }>()
const emit = defineEmits<{ close: []; saved: [memberId: number] }>()
const user = useUserStore()
const descriptions: Record<string, string> = {
  business_it: '开发 SQL、维护报表草稿并提交审批',
  business_supervisor: '审核本人负责的正式报表',
  viewer: '查看有权访问的正式报表',
}
const keys = reportCommandKeys()
const directory = ref<DirectRoleDirectory>()
const chosen = ref(props.memberId ?? 0)
const eligibleMembers = computed(() => (directory.value?.members ?? [])
  .filter(member => canAssignWorkspaceBusinessRoles(directory.value, user.user?.id, member)))
const selected = computed(() => eligibleMembers.value.find(member => member.user_id === chosen.value))
const codes = ref<string[]>([])
const loading = ref(false)
const saving = ref(false)
const failure = ref('')
const receipt = ref('')
const changed = computed(() => selected.value && [...(selected.value.role_codes ?? [])].sort().join(',') !== [...codes.value].sort().join(','))
let current: AbortController | undefined
let active = true

function readMember() {
  codes.value = [...(selected.value?.role_codes ?? [])]
  receipt.value = ''
  failure.value = ''
}

async function load() {
  current?.abort()
  const attempt = new AbortController()
  current = attempt
  loading.value = true
  failure.value = ''
  try {
    const result = await directRoleApi.list(props.workspaceId, attempt.signal)
    if (attempt.signal.aborted) return
    directory.value = { ...result, roles: result.roles ?? [], members: result.members ?? [] }
    readMember()
  } catch (error) {
    if (!attempt.signal.aborted) failure.value = error instanceof Error ? error.message : '无法读取工作空间角色。'
  } finally {
    if (current === attempt) loading.value = false
  }
}

async function save() {
  const member = selected.value
  if (!member || !changed.value || saving.value || !directory.value?.enabled) return
  const payload = { expected_version: member.version, role_codes: [...codes.value].sort() }
  saving.value = true
  failure.value = ''
  receipt.value = ''
  try {
    const saved = await directRoleApi.save(props.workspaceId, member.user_id, { ...payload, request_id: keys.get('direct-roles', payload) })
    if (!active || !directory.value) return
    directory.value = { ...directory.value, members: directory.value.members.map(item => item.user_id === saved.user_id ? saved : item) }
    readMember()
    receipt.value = '角色已保存并读回。'
    keys.clear()
    emit('saved', saved.user_id)
  } catch (error) {
    if (active) failure.value = error instanceof Error ? error.message : '角色未保存，请刷新后核对。'
  } finally {
    if (active) saving.value = false
  }
}

watch(chosen, readMember)
onMounted(() => void load())
onScopeDispose(() => { active = false; current?.abort(); keys.clear() })
</script>

<template>
  <el-dialog :model-value="true" title="分配角色" append-to-body align-center class="glass-dialog"
    width="min(560px, calc(100vw - 24px))" :close-on-click-modal="false" :close-on-press-escape="!saving"
    :show-close="!saving" @close="emit('close')">
    <form class="space-y-4" @submit.prevent="save">
      <dl class="space-y-2 text-sm">
        <div><dt class="text-muted">工作空间</dt><dd class="break-words font-medium">{{ directory?.workspace_name || workspaceName }}</dd></div>
        <div><dt class="text-muted">操作者</dt><dd class="break-words font-medium">{{ user.username }}</dd></div>
      </dl>
      <p v-if="loading" role="status" class="text-sm text-muted">正在读取成员与角色…</p>
      <p v-if="failure" role="alert" class="break-words text-sm text-danger">{{ failure }}</p>
      <button v-if="failure && !loading" type="button" class="ghost-button ui-control-target" :disabled="saving" @click="load">重新读取</button>
      <p v-if="directory && !directory.enabled" class="text-sm text-muted">此空间使用原有角色管理方式。</p>
      <template v-else-if="directory && !loading">
        <label class="block text-sm">成员
          <select v-model.number="chosen" class="glass-input mt-1 block w-full" :disabled="saving || !eligibleMembers.length">
            <option :value="0">请选择成员</option>
            <option v-for="member in eligibleMembers" :key="member.user_id" :value="member.user_id">{{ member.username }}</option>
          </select>
        </label>
        <p v-if="!eligibleMembers.length" class="text-sm text-muted">此空间暂无可分配的有效成员。</p>
        <fieldset v-if="selected" class="space-y-2" :disabled="saving">
          <legend class="text-sm font-medium">角色</legend>
          <label v-for="role in directory.roles" :key="role.code" class="flex min-h-11 items-center gap-3 border border-stroke px-3 py-2">
            <input v-model="codes" type="checkbox" :value="role.code" class="h-4 w-4 shrink-0" />
            <span class="min-w-0 text-sm"><span class="block font-medium">{{ role.name }}</span><span class="block text-xs text-muted">{{ descriptions[role.code] }}</span></span>
          </label>
        </fieldset>
      </template>
      <p v-if="receipt" role="status" class="text-sm text-success">{{ receipt }}</p>
      <div class="flex flex-wrap justify-end gap-2 border-t border-stroke pt-3">
        <button type="button" class="ghost-button ui-control-target" :disabled="saving" @click="emit('close')">关闭</button>
        <button v-if="directory?.enabled" type="submit" class="liquid-button ui-control-target" :disabled="!changed || saving || loading">{{ saving ? '保存中…' : '保存角色' }}</button>
      </div>
    </form>
  </el-dialog>
</template>
