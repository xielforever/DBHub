<script setup lang="ts">
import { computed, useId } from 'vue'
import LoadingState from '../../components/base/LoadingState.vue'
import type { Assignment, Group, Member, Proposal, Role } from '../../types/iam'
import { changeLabels, stateLabels } from '../../utils/iam-labels'

const props = defineProps<{
  member: Member
  assignments: Assignment[]
  groups: Group[]
  proposals: Proposal[]
  roles: Role[]
  now: number
  canViewAssignments: boolean
  canViewHistory: boolean
  canAdjust: boolean
  directRoles?: boolean
  currentDirectRoles?: string[]
  directRolesLoading?: boolean
  directRolesError?: string
  canFreeze: boolean
  canEditMembership: boolean
  canRemoveMembership?: boolean
  workspaceAdmin?: boolean
  busy: boolean
  assignmentsLoading: boolean
  assignmentsError: string
  historyLoading: boolean
  historyError: string
  returnProposalId?: string
}>()

const emit = defineEmits<{
  close: []
  freeze: []
  adjust: []
  editMembership: []
  removeMembership: []
  retryAssignments: []
  retryDirectRoles: []
  retryHistory: []
  openProposal: [id: string]
}>()

const headingId = useId()
const localTime = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric', month: '2-digit', day: '2-digit',
  hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZoneName: 'short',
})
const baseRoles: Record<string, string> = { admin: '管理员', developer: '开发者', readonly: '只读成员' }
const rolesById = computed(() => new Map(props.roles.map(role => [role.id, role])))
const groupsById = computed(() => new Map(props.groups.map(group => [group.id, group.name])))

function displayTime(value: string) {
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) ? localTime.format(timestamp) : '时间待核对'
}

const memberStatus = computed(() => {
  if (props.member.frozen) return '已冻结'
  if (!props.member.active) return '已停用'
  if (!props.member.expires_at) return '有效'
  const expiry = Date.parse(props.member.expires_at)
  if (!Number.isFinite(expiry)) return '期限待核对'
  return expiry <= props.now ? '已到期' : '有效'
})

function assignmentStatus(assignment: Assignment) {
  if (assignment.revoked_at) return '已撤销'
  const expiry = Date.parse(assignment.valid_until)
  if (!Number.isFinite(expiry)) return '期限待核对'
  if (expiry <= props.now) return '已到期'
  if (memberStatus.value !== '有效') return '成员不可用'
  if (rolesById.value.get(assignment.role_id)?.enabled === false) return '角色已停用'
  return '有效'
}

function proposalStatus(proposal: Proposal) {
  if (proposal.state === 'pending' && Date.parse(proposal.valid_until) <= props.now) return '已过期'
  if (proposal.state === 'applied') return '已批准'
  return stateLabels[proposal.state] ?? '状态待核对'
}

function proposalTitle(proposal: Proposal) {
  const assignment = props.assignments.find(item => item.proposal_id === proposal.id)
  const role = proposal.patch.role_id ? rolesById.value.get(proposal.patch.role_id) : undefined
  return assignment?.role_name ?? role?.name ?? changeLabels[proposal.patch.kind] ?? '角色调整'
}

const assignmentsReady = computed(() => props.canViewAssignments && !props.assignmentsLoading && !props.assignmentsError)
const returnedAssignment = computed(() => {
  if (!props.returnProposalId || !assignmentsReady.value) return undefined
  return props.assignments.find(assignment =>
    assignment.proposal_id === props.returnProposalId && assignmentStatus(assignment) === '有效')
})

const returnMessage = computed(() => {
  if (!props.returnProposalId) return ''
  if (returnedAssignment.value) return '已批准，已读回有效分配。'
  if (!props.canViewHistory || props.historyLoading || props.historyError) return ''
  const proposal = props.proposals.find(item => item.id === props.returnProposalId)
  if (!proposal) return ''
  const status = proposalStatus(proposal)
  if (status !== '已批准') return `此申请${status}。`
  if (!props.canViewAssignments) return '此申请已批准，当前无权查看角色分配。'
  if (props.assignmentsLoading) return '此申请已批准，正在核对角色分配。'
  if (props.assignmentsError) return '此申请已批准，角色分配读回失败。'
  return '此申请已批准，未读回当前有效分配。'
})
</script>

<template>
  <section class="glass-panel ui-card-padding min-w-0 space-y-4" :aria-labelledby="headingId">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <h2 :id="headingId" data-member-heading tabindex="-1" class="editorial-object-title scroll-mt-32 [overflow-wrap:anywhere]">
          {{ member.username }} · 成员详情
        </h2>
        <p class="mt-1 text-sm text-muted">成员 #{{ member.id }}</p>
      </div>
      <div class="flex flex-wrap gap-2">
        <button v-if="canAdjust && memberStatus === '有效'" type="button" class="liquid-button ui-control-target" data-member-adjust
          :disabled="busy" @click="emit('adjust')">
          {{ directRoles ? '分配角色' : '调整角色' }}
        </button>
        <button v-if="canEditMembership" type="button" class="ghost-button ui-control-target" data-member-edit
          :disabled="busy" @click="emit('editMembership')">编辑成员资格</button>
        <button v-if="canRemoveMembership && memberStatus === '有效'" type="button" class="ghost-button ui-control-target" data-member-remove
          :disabled="busy" @click="emit('removeMembership')">移出空间</button>
        <button type="button" class="ghost-button ui-control-target" @click="emit('close')">
          关闭成员详情
        </button>
      </div>
    </header>

    <dl class="grid grid-cols-[repeat(auto-fit,minmax(min(100%,8rem),1fr))] gap-3 text-sm">
      <div>
        <dt class="text-muted">访问状态</dt>
        <dd class="mt-1"><span class="status-badge" :class="{ 'status-badge--success': memberStatus === '有效' }">{{ memberStatus }}</span></dd>
      </div>
      <div>
        <dt class="text-muted">{{ workspaceAdmin === undefined ? '基础角色' : '成员职责' }}</dt>
        <dd class="mt-1 [overflow-wrap:anywhere]">{{ workspaceAdmin === undefined ? (baseRoles[member.role] ?? member.role) || '未显示' : workspaceAdmin ? '空间管理员' : '普通成员' }}</dd>
      </div>
      <div>
        <dt class="text-muted">权限方案</dt>
        <dd class="mt-1">{{ member.principal_mode === 'TARGET' || member.direct_role_managed ? '角色权限' : '旧权限兼容' }}</dd>
      </div>
      <div>
        <dt class="text-muted">成员期限</dt>
        <dd class="mt-1">
          <time v-if="member.expires_at" :datetime="member.expires_at">{{ displayTime(member.expires_at) }}</time>
          <span v-else>未设期限</span>
        </dd>
      </div>
    </dl>
    <p v-if="member.frozen" class="text-sm text-muted">本空间访问已冻结。托管资产见待交接资产。</p>
    <button v-else-if="canFreeze" type="button" class="ghost-button ui-control-target" :disabled="busy" @click="emit('freeze')">
      冻结并托管资产
    </button>

    <p v-if="returnMessage" role="status" class="text-sm" :class="returnedAssignment ? 'text-success' : 'text-muted'">
      {{ returnMessage }}
    </p>

    <section v-if="directRoles" :aria-labelledby="`${headingId}-current-roles`" class="min-w-0 space-y-2 border-t border-stroke pt-3">
      <h3 :id="`${headingId}-current-roles`" class="editorial-object-title">当前角色</h3>
      <LoadingState v-if="directRolesLoading" variant="detail" label="正在读取当前角色…" />
      <div v-else-if="directRolesError" class="space-y-2">
        <p role="alert" class="text-sm text-danger break-words">{{ directRolesError }}</p>
        <button type="button" class="ghost-button ui-control-target" @click="emit('retryDirectRoles')">重试读取当前角色</button>
      </div>
      <ul v-else-if="currentDirectRoles?.length" class="flex flex-wrap gap-2 text-sm">
        <li v-for="role in currentDirectRoles" :key="role" class="status-badge status-badge--success">{{ role }}</li>
      </ul>
      <p v-else class="text-sm text-muted">暂无直接分配的角色。</p>
    </section>

    <section v-if="canViewAssignments" class="min-w-0 space-y-2 border-t border-stroke pt-3"
      :aria-labelledby="`${headingId}-assignments`" :aria-busy="assignmentsLoading">
      <h3 :id="`${headingId}-assignments`" class="editorial-object-title">{{ directRoles ? '原有分配记录' : '角色分配' }}</h3>
      <LoadingState v-if="assignmentsLoading" variant="detail" label="正在读取角色分配…" />
      <div v-else-if="assignmentsError" class="space-y-2">
        <p role="alert" class="text-sm text-danger [overflow-wrap:anywhere]">角色分配读取失败。{{ assignmentsError }}</p>
        <button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="emit('retryAssignments')">
          重试读取角色分配
        </button>
      </div>
      <p v-else-if="!assignments.length" class="text-sm text-muted">{{ directRoles ? '暂无原有分配记录。' : '暂无该成员的角色分配记录。' }}</p>
      <template v-else>
        <p class="text-sm text-muted">实际操作许可以成员本人会话的权限结果为准。</p>
        <div role="region" tabindex="0" aria-label="成员角色分配表格" class="editorial-table-wrap">
          <table class="editorial-table min-w-[28rem]" aria-label="成员角色分配">
            <thead><tr><th scope="col">角色与修订</th><th scope="col">分配来源</th><th scope="col">授权截止</th><th scope="col">状态</th></tr></thead>
            <tbody>
              <tr v-for="assignment in assignments" :key="assignment.id"
                :class="{ 'bg-[var(--db-selected)]': returnedAssignment?.id === assignment.id }">
                <th scope="row">
                  <span class="block [overflow-wrap:anywhere]">{{ assignment.role_name }}</span>
                  <span class="editorial-table__meta">修订 {{ assignment.role_revision }}</span>
                  <span v-if="returnedAssignment?.id === assignment.id" class="block text-xs text-success">已读回此申请的分配</span>
                </th>
                <td>
                  <span v-if="assignment.subject_type === 'user'">直接分配</span>
                  <template v-else>
                    <span class="block [overflow-wrap:anywhere]">{{ groupsById.get(assignment.subject_id) ?? `用户组 #${assignment.subject_id}` }}</span>
                    <span class="editorial-table__meta">用户组分配 · 当前组员且已获批</span>
                  </template>
                </td>
                <td class="whitespace-nowrap"><time :datetime="assignment.valid_until">{{ displayTime(assignment.valid_until) }}</time></td>
                <td class="whitespace-nowrap">
                  <span class="status-badge" :class="{ 'status-badge--success': assignmentStatus(assignment) === '有效' }">
                    {{ assignmentStatus(assignment) }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </section>

    <section v-if="canViewHistory" class="min-w-0 space-y-2 border-t border-stroke pt-3"
      :aria-labelledby="`${headingId}-history`" :aria-busy="historyLoading">
      <h3 :id="`${headingId}-history`" class="editorial-object-title">角色调整记录</h3>
      <LoadingState v-if="historyLoading" variant="detail" label="正在读取角色调整记录…" />
      <div v-else-if="historyError" class="space-y-2">
        <p role="alert" class="text-sm text-danger [overflow-wrap:anywhere]">角色调整记录读取失败。{{ historyError }}</p>
        <button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="emit('retryHistory')">
          重试读取角色调整记录
        </button>
      </div>
      <p v-else-if="!proposals.length" class="text-sm text-muted">暂无该成员的角色调整记录。</p>
      <ul v-else class="divide-y divide-stroke">
        <li v-for="proposal in proposals" :key="proposal.id" class="flex flex-wrap items-start justify-between gap-2 py-3">
          <div class="min-w-0">
            <p class="flex flex-wrap items-center gap-2 text-sm">
              <span class="[overflow-wrap:anywhere]">{{ proposalTitle(proposal) }}</span>
              <span class="status-badge" :class="{ 'status-badge--warning': proposalStatus(proposal) === '待复核' }">{{ proposalStatus(proposal) }}</span>
            </p>
            <p class="mt-1 text-xs text-muted [overflow-wrap:anywhere]">申请 {{ proposal.id }}</p>
            <p v-if="proposal.created_at" class="mt-1 text-xs text-muted">
              提交于 <time :datetime="proposal.created_at">{{ displayTime(proposal.created_at) }}</time>
            </p>
          </div>
          <button type="button" class="ghost-button ui-control-target" :disabled="busy"
            :aria-label="`查看申请 ${proposal.id}`" @click="emit('openProposal', proposal.id)">
            查看申请
          </button>
        </li>
      </ul>
    </section>
  </section>
</template>
