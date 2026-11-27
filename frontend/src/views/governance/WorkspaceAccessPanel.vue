<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { canAssignWorkspaceBusinessRoles, directRoleApi, type DirectRoleDirectory, type DirectRoleMember, type WorkspaceMembershipOperation } from '../../api/direct-roles'
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageState from '../../components/base/PageState.vue'
import { useUserStore } from '../../stores/user'
import DirectRoleDialog from './DirectRoleDialog.vue'
import WorkspaceMembershipDialog from './WorkspaceMembershipDialog.vue'

const props = defineProps<{ workspaceId: number; adminsOnly?: boolean; membersOnly?: boolean; initialMemberId?: number }>()
const user = useUserStore()
const directory = shallowRef<DirectRoleDirectory>()
const loading = ref(false)
const error = ref('')
const receipt = ref('')
const tab = ref('members')
const memberId = ref(props.initialMemberId ?? 0)
const keyword = ref('')
const memberHeading = ref<HTMLElement>()
const adminHeading = ref<HTMLElement>()
const memberSearch = ref<HTMLInputElement>()
const roleTarget = ref<DirectRoleMember>()
const roleSaved = ref(false)
const operation = ref<'join' | WorkspaceMembershipOperation>()
const target = ref<DirectRoleMember>()
let read: AbortController | undefined
let active = true

const members = computed(() => (directory.value?.members ?? []).filter(member => member.active !== false))
const admins = computed(() => members.value.filter(member => member.member_role === 'admin'))
const ordinaryMembers = computed(() => members.value.filter(member => member.member_role !== 'admin'))
const filteredMembers = computed(() => members.value.filter(member => member.username.toLowerCase().includes(keyword.value.trim().toLowerCase())))
const selected = computed(() => members.value.find(member => member.user_id === memberId.value))
const canAssignSelected = computed(() => canAssignWorkspaceBusinessRoles(directory.value, user.user?.id, selected.value))
const categories = computed(() => {
  const items: { id: string; name: string }[] = []
  if (!props.adminsOnly) items.push({ id: 'members', name: '成员' })
  if (!props.membersOnly && directory.value?.can_manage_admins) items.push({ id: 'admins', name: '空间管理员' })
  return items
})
const activeTab = computed(() => props.adminsOnly ? 'admins' : tab.value)
const operator = computed(() => user.user?.username ?? '当前账号')
const roleNames = computed(() => (directory.value?.roles ?? []).filter(role => selected.value?.role_codes?.includes(role.code)).map(role => role.name))

async function load() {
  read?.abort()
  const attempt = new AbortController()
  read = attempt
  const actor = user.user?.id
  loading.value = true
  error.value = ''
  directory.value = undefined
  try {
    const result = await directRoleApi.list(props.workspaceId, attempt.signal)
    if (attempt.signal.aborted || user.user?.id !== actor) return
    if (result.workspace_id !== props.workspaceId) throw new Error('读回的工作空间与指定目标不一致。')
    directory.value = result
    if (!members.value.some(member => member.user_id === memberId.value)) memberId.value = 0
    if (!categories.value.some(item => item.id === tab.value)) tab.value = categories.value[0]?.id ?? 'members'
  } catch (failure) {
    if (!attempt.signal.aborted) error.value = failure instanceof Error ? failure.message : '无法读取目标工作空间的成员资格。'
  } finally {
    if (!attempt.signal.aborted && user.user?.id === actor) loading.value = false
  }
}

function openMembership(kind: 'join' | WorkspaceMembershipOperation, member?: DirectRoleMember) {
  if (loading.value) return
  receipt.value = ''
  target.value = member
  operation.value = kind
}

function closeMembership() {
  operation.value = undefined
  target.value = undefined
}

async function membershipSaved(member: DirectRoleMember) {
  const kind = operation.value
  closeMembership()
  if (kind === 'join') {
    keyword.value = ''
    memberId.value = member.user_id
  }
  const labels = { join: '已添加成员', remove: '已移出成员', appoint_admin: '已任命空间管理员', revoke_admin: '已撤销空间管理员资格' }
  if (kind) receipt.value = `${labels[kind]} · ${member.username} · 操作者 ${operator.value}`
  await load()
  await nextTick()
  if (!active) return
  if (kind === 'join') memberHeading.value?.focus({ preventScroll: true })
  else if (kind === 'remove') memberSearch.value?.focus({ preventScroll: true })
  else adminHeading.value?.focus({ preventScroll: true })
}

function openRoles(member: DirectRoleMember) {
  if (!canAssignWorkspaceBusinessRoles(directory.value, user.user?.id, member)) return
  receipt.value = ''
  roleTarget.value = member
  roleSaved.value = false
}

function closeRoles() {
  if (roleSaved.value && roleTarget.value) {
    receipt.value = `角色已保存 · ${roleTarget.value.username} · 操作者 ${operator.value}`
    if (active) void load()
  }
  roleTarget.value = undefined
  roleSaved.value = false
}

watch(() => props.initialMemberId, id => { memberId.value = id ?? 0 })
onMounted(() => void load())
onScopeDispose(() => { active = false; read?.abort() })
</script>

<template>
  <div class="min-w-0 space-y-4">
    <p v-if="receipt" role="status" class="break-words text-sm text-success">{{ receipt }}</p>
    <LoadingState v-if="loading" label="正在读取目标空间成员…" />
    <PageState v-else-if="error" title="读取目标空间成员失败" :description="error" tone="danger">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="load">重试读取</button></template>
    </PageState>
    <PageState v-else-if="directory && !directory.enabled" title="此工作空间未启用权限矩阵" description="请选择已启用权限矩阵的工作空间。" />
    <PageState v-else-if="!categories.length" title="当前无权管理空间管理员" description="请核对目标工作空间的管理员任免资格。" />
    <template v-else-if="directory">
      <div v-if="!adminsOnly && !membersOnly" class="glass-panel ui-card-padding min-w-0">
        <dl class="flex flex-wrap gap-x-8 gap-y-2 text-sm">
          <div><dt class="text-muted">目标工作空间</dt><dd class="break-words font-medium">{{ directory.workspace_name }}</dd></div>
          <div><dt class="text-muted">操作者</dt><dd class="break-words font-medium">{{ operator }}</dd></div>
        </dl>
      </div>
      <PageCategoryTabs label="目标工作空间授权管理" :categories="categories" :active-id="activeTab" :hide-tabs="adminsOnly || membersOnly"
        @select="tab = $event; receipt = ''">
        <section v-if="activeTab === 'admins'" class="glass-panel ui-card-padding min-w-0 space-y-4" aria-label="空间管理员名单">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h2 ref="adminHeading" tabindex="-1" class="editorial-object-title">空间管理员</h2>
            <div class="flex flex-wrap gap-2">
              <button type="button" class="ghost-button ui-control-target" @click="load">刷新管理员</button>
              <button type="button" class="liquid-button ui-control-target" :disabled="!ordinaryMembers.length"
                @click="openMembership('appoint_admin')">任命管理员</button>
            </div>
          </div>
          <div v-if="admins.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="空间管理员表格">
            <table class="editorial-table min-w-[24rem]">
              <thead><tr><th scope="col">成员账号</th><th scope="col">职责</th><th scope="col">操作</th></tr></thead>
              <tbody><tr v-for="member in admins" :key="member.user_id">
                <th scope="row" class="break-all">{{ member.username }}</th>
                <td>空间管理员</td>
                <td><button type="button" class="ghost-button ui-control-target whitespace-nowrap" :disabled="admins.length <= 1"
                  :aria-label="`撤销${member.username}的管理员资格`" @click="openMembership('revoke_admin', member)">撤销管理员资格</button></td>
              </tr></tbody>
            </table>
          </div>
          <p v-else class="text-sm text-muted">暂无有效空间管理员。</p>
          <p v-if="admins.length === 1" class="text-sm text-muted">此空间需要保留至少一名有效管理员。</p>
        </section>
        <section v-else class="glass-panel ui-card-padding min-w-0 space-y-4" aria-label="目标空间成员">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h2 class="editorial-object-title">成员</h2>
            <button v-if="directory.can_manage_members" type="button" class="liquid-button ui-control-target"
              @click="openMembership('join')">添加成员</button>
          </div>
          <label class="block text-sm sm:max-w-sm">搜索空间成员
            <input ref="memberSearch" v-model="keyword" type="search" maxlength="100" class="glass-input ui-control-target mt-1 block w-full"
              placeholder="成员用户名" />
          </label>
          <div v-if="filteredMembers.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="目标空间成员表格">
            <table class="editorial-table min-w-[26rem]">
              <thead><tr><th scope="col">成员账号</th><th scope="col">成员职责</th><th scope="col">操作</th></tr></thead>
              <tbody><tr v-for="member in filteredMembers" :key="member.user_id">
                <th scope="row" class="break-all">{{ member.username }}</th>
                <td>{{ member.member_role === 'admin' ? '空间管理员' : '普通成员' }}</td>
                <td><button type="button" class="editorial-text-action ui-control-target whitespace-nowrap"
                  :aria-label="`查看成员 ${member.username}`" :aria-pressed="memberId === member.user_id"
                  @click="memberId = member.user_id">查看成员</button></td>
              </tr></tbody>
            </table>
          </div>
          <p v-else class="text-sm text-muted">暂无匹配成员。</p>
          <div v-if="selected" class="min-w-0 space-y-2 border-t border-stroke pt-3" aria-label="成员详情">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <h3 ref="memberHeading" tabindex="-1" class="editorial-object-title break-all">{{ selected.username }} · 成员详情</h3>
              <div v-if="directory.can_manage_members" class="flex flex-wrap gap-2">
                <button v-if="selected.member_role !== 'admin' && selected.user_id !== user.user?.id" type="button"
                  class="ghost-button ui-control-target" @click="openMembership('remove', selected)">移出空间</button>
                <button v-if="canAssignSelected" type="button" class="liquid-button ui-control-target" @click="openRoles(selected)">分配角色</button>
              </div>
            </div>
            <dl class="text-sm"><dt class="text-muted">当前角色</dt><dd class="break-words">{{ roleNames.join('、') || '暂无直接分配的角色' }}</dd></dl>
          </div>
        </section>
      </PageCategoryTabs>
      <WorkspaceMembershipDialog v-if="operation" :workspace-id="workspaceId" :workspace-name="directory.workspace_name"
        :operation="operation" :member="target" :eligible-members="ordinaryMembers" @close="closeMembership" @saved="membershipSaved" />
      <DirectRoleDialog v-if="roleTarget" :workspace-id="workspaceId" :workspace-name="directory.workspace_name"
        :member-id="roleTarget.user_id" @close="closeRoles" @saved="roleSaved = true" />
    </template>
  </div>
</template>
