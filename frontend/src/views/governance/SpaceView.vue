<script setup lang="ts">
import {computed,nextTick,onScopeDispose,reactive,ref,watch} from 'vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import LoadingState from '../../components/base/LoadingState.vue'
import {usePageCategory} from '../../composables/usePageCategory'
import {useRouter} from 'vue-router'
import {ElMessage} from 'element-plus'
import {useUserStore} from '../../stores/user'
import {iamApi} from '../../api/iam'
import type {Role,Member,Assignment,Delegation,Group,Proposal,Change,MenuConfiguration,MenuItem,Effective,Baseline} from '../../types/iam'
import {newRequestId} from '../../utils/request-id'
import {actionLabel,changeActions,changeLabels,stateLabels,governanceLabels} from '../../utils/iam-labels'
import { workspaceGovernanceActions } from '../../utils/permission'
import { configuredEditorialGroup, editorialGroupNames, editorialMenuGroup } from '../../utils/editorial-navigation'
import ChangeDialog from './ChangeDialog.vue'
import LegacyUsersView from '../admin/UsersView.vue'
import MigrationPanel from './MigrationReviewPanel.vue'
import ActionRestrictionsPanel from './ActionRestrictionsPanel.vue'
const user=useUserStore(),router=useRouter(),loading=ref(true),busy=ref(false),error=ref(''),message=ref(''),readError=ref('')
const canGovern = computed(() => workspaceGovernanceActions.some(user.can))
const migrationAccess = computed(() => user.effective?.isolated_migration)
const canReviewMigration = computed(() => migrationAccess.value?.review === true)
const canOpenGovernance = computed(() => canGovern.value || canReviewMigration.value)
const canOpenMigration = computed(() => user.can('space.assign.view') || !!migrationAccess.value)
const categories = [
  { id: 'members', name: '成员', actions: ['space.members.view'] },
  { id: 'menus', name: '菜单配置', actions: ['space.menus.view'] },
  { id: 'roles', name: '角色管理', actions: ['space.roles.view'] },
  { id: 'assignments', name: '角色分配', actions: ['space.assign.view'] },
  { id: 'resources', name: '资源授权', actions: ['space.resources.view'] },
  { id: 'policy', name: '审批发布策略', actions: ['space.policy.view'] },
  { id: 'governance', name: '治理审批', actions: workspaceGovernanceActions },
  { id: 'migration', name: '迁移与回退', actions: ['space.assign.view'] },
]
const tabs = computed(() => categories.filter(item => {
  if (item.id === 'governance') return canOpenGovernance.value
  if (item.id === 'migration') return canOpenMigration.value
  return item.actions.some(user.can)
}))
const { category, selectCategory } = usePageCategory(tabs)
const tab = computed(() => category.value?.id)
const roles=ref<Role[]>([]),members=ref<Member[]>([]),assignments=ref<Assignment[]>([]),delegations=ref<Delegation[]>([]),groups=ref<Group[]>([]),proposals=ref<Proposal[]>([]),menuConfig=ref<MenuConfiguration>({version:0,items:[],preferred_menu:''}),custody=ref<{user_id:number;freeze_sequence:number;assets:{reports:number;dashboards:number}}[]>([])
const resources = ref<{ reason: string; sources: number; reports: number; dashboards: number; sensitive_reveal_enabled: boolean; team_delivery_enabled: boolean }>()
const policy = ref<{ reason: string; version: string; editable: boolean; allow_unknown_freshness: boolean; new_export_expiry_enabled: boolean; dashboard_export_enabled: boolean; team_subscription_enabled: boolean }>()
const resourceLinks = [
  { field: 'sources', name: '数据源', path: '/sources', action: 'source.page' },
  { field: 'reports', name: '报表', path: '/reports', action: 'report.page' },
  { field: 'dashboards', name: '仪表盘', path: '/dashboards', action: 'dashboard.page' },
] as const
const policyFields = computed(() => policy.value ? [
  { name: '当前策略版本', value: policy.value.version },
  { name: '编辑状态', value: policy.value.editable ? '接口已标记可编辑' : '只读' },
  { name: '允许未知数据时效', value: policy.value.allow_unknown_freshness ? '允许' : '不允许' },
  { name: '新导出有效期', value: policy.value.new_export_expiry_enabled ? '已启用' : '未启用' },
  { name: '仪表盘导出', value: policy.value.dashboard_export_enabled ? '已启用' : '未启用' },
  { name: '团队订阅', value: policy.value.team_subscription_enabled ? '已启用' : '未启用' },
] : [])
const date=(value:string)=>new Date(value).toLocaleString(),memberName=(id:number)=>members.value.find(m=>m.id===id)?.username||`成员 #${id}`,groupName=(id:number)=>groups.value.find(g=>g.id===id)?.name||`用户组 #${id}`,menuName=(id:string)=>user.effective?.catalog.menus.find(m=>m.id===id)?.label||id
const memberSearch = ref(''), memberFilter = ref(''), roleSearch = ref(''), selectedMemberId = ref<number>(), selectedRoleId = ref<string>()
const memberHeading = ref<HTMLElement>(), roleHeading = ref<HTMLElement>(), memberTable = ref<HTMLElement>(), roleTable = ref<HTMLElement>()
const memberStatusFilter = ref<HTMLSelectElement>(), memberDetail = ref<HTMLElement>()
const refreshButton = ref<HTMLButtonElement>()
const selectedMember = computed(() => members.value.find(item => item.id === selectedMemberId.value))
const selectedRole = computed(() => roles.value.find(item => item.id === selectedRoleId.value))
const now = ref(Date.now())
const roleLabels: Record<string, string> = { admin: '管理员', developer: '开发者', readonly: '只读成员' }
const memberStatus = (item: Member) => item.frozen ? '已冻结' : !item.active ? '已停用' : item.expires_at && Date.parse(item.expires_at) <= now.value ? '已到期' : '有效'
const assignmentStatus = (item: Assignment) => item.revoked_at ? '已撤销' : Date.parse(item.valid_until) <= now.value ? '已到期' : '有效'
const filteredMembers = computed(() => members.value.filter(item => (!memberFilter.value || memberStatus(item) === memberFilter.value) && item.username.toLowerCase().includes(memberSearch.value.trim().toLowerCase())))
const filteredRoles = computed(() => roles.value.filter(item => item.name.toLowerCase().includes(roleSearch.value.trim().toLowerCase())))
const assignmentType = ref<'user'|'group'>('user')
const filteredAssignments = computed(() => assignments.value.filter(item => item.subject_type === assignmentType.value))
async function viewMember(id: number) { selectedMemberId.value = id; await nextTick(); memberHeading.value?.focus() }
async function viewRole(id: string) { selectedRoleId.value = id; await nextTick(); roleHeading.value?.focus() }
async function closeMember() { const id = selectedMemberId.value; selectedMemberId.value = undefined; await nextTick(); memberTable.value?.querySelector<HTMLElement>(`[data-member-id="${id}"]`)?.focus() }
async function closeRole() {
  const id = selectedRoleId.value
  selectedRoleId.value = undefined
  await nextTick()
  Array.from(roleTable.value?.querySelectorAll<HTMLElement>('[data-role-id]') ?? []).find(el => el.dataset.roleId === id)?.focus()
}
watch(filteredMembers, items => {
  const active = document.activeElement
  const focusedRow = active?.closest<HTMLElement>('[data-member-id]')
  const selectionRemoved = selectedMemberId.value !== undefined &&
    !items.some(item => item.id === selectedMemberId.value)
  const focusedRowRemoved = focusedRow &&
    !items.some(item => item.id === Number(focusedRow.dataset.memberId))
  const focusInRemovedDetail = selectionRemoved && memberDetail.value?.contains(active)
  const emptyTableFocused = active === memberTable.value && items.length === 0
  if (selectionRemoved) selectedMemberId.value = undefined
  if (focusedRowRemoved || focusInRemovedDetail || emptyTableFocused) {
    void nextTick(() => {
      if (tab.value === 'members' && document.activeElement === document.body) memberStatusFilter.value?.focus()
    })
  }
})
watch(filteredRoles, items => { if (!items.some(item => item.id === selectedRoleId.value)) selectedRoleId.value = undefined })
const baseline=()=>{if(!user.effective)throw Error('当前空间权限不可用');return{config_version:user.effective.snapshot.config_version,authorization_epoch:user.effective.snapshot.authorization_epoch}}
const menuBaseline = ref<Baseline>()
const loadedMenu = ref('')
const menuDirty = computed(() => loadedMenu.value !== '' && loadedMenu.value !== JSON.stringify(menuConfig.value))
const menuItems = computed(() => [...menuConfig.value.items].sort((a, b) => a.id.localeCompare(b.id, undefined, { numeric: true })))
function navigationGroup(item: MenuItem) {
  const entry = user.effective?.catalog.menus.find(menu => menu.id === item.id)
  return editorialMenuGroup(entry?.path ?? '', item.group, entry?.group)
}
function changeNavigationGroup(item: MenuItem, group: string) {
  const entry = user.effective?.catalog.menus.find(menu => menu.id === item.id)
  if (entry) item.group = configuredEditorialGroup(group, entry.path, entry.group)
}
const menuValidation = computed(() => {
  const items = menuConfig.value.items
  if (items.length !== user.effective?.catalog.menus.length) return '请重新核对完整菜单目录。'
  if (items.some(item => !item.label.trim() || !item.group.trim())) return '菜单名称和分组均不能为空。'
  if (items.some(item => !Number.isSafeInteger(item.order) || item.order < 0)) return '顺序必须是非负整数。'
  if (new Set(items.map(item => item.order)).size !== items.length) return '菜单顺序不能重复。'
  return ''
})
let controller: AbortController | undefined
let active = true
function setMenus(effective: Effective, force = false) {
  const versionChanged = !menuBaseline.value || effective.menus.version !== menuConfig.value.version ||
    effective.snapshot.config_version !== menuBaseline.value.config_version || effective.snapshot.authorization_epoch !== menuBaseline.value.authorization_epoch
  if (!force && !versionChanged && menuDirty.value) return
  if (!force && versionChanged && menuDirty.value) message.value = '菜单配置版本已变化，已载入最新配置。'
  menuConfig.value = { ...effective.menus, items: effective.menus.items.map(item => ({ ...item })) }
  loadedMenu.value = JSON.stringify(menuConfig.value)
  menuBaseline.value = { config_version: effective.snapshot.config_version, authorization_epoch: effective.snapshot.authorization_epoch }
}
async function load() {
  controller?.abort()
  const selected = tab.value
  readError.value = ''
  if (!selected) { loading.value = false; return }
  const current = new AbortController()
  controller = current
  loading.value = true
  const canReadAssignments = user.can('space.assign.view')
  const canManageAssignments = user.can('space.assign.manage')
  const canReadDelegations = (canReadAssignments && canGovern.value) || (selected === 'governance' && canReviewMigration.value)
  now.value = Date.now()
  delegations.value = []
  proposals.value = []
  const jobs: Promise<void>[] = []
  function read<T>(name: string, apply: (value: T) => void) {
    jobs.push(iamApi.read<T>(name, current.signal).then(value => {
      if (active && controller === current && !current.signal.aborted) apply(value)
    }))
  }
  try {
    if (selected === 'members') {
      read<Member[]>('members', value => { members.value = value })
      read<typeof custody.value>('custody', value => { custody.value = value })
      if (canReadDelegations && user.can('space.members.exit')) read<Delegation[]>('delegations', value => { delegations.value = value })
    } else if (selected === 'menus') {
      read<Effective>('menus', value => setMenus(value))
    } else if (selected === 'roles') {
      read<Role[]>('roles', value => { roles.value = value })
      if (canReadDelegations && user.can('space.roles.manage')) read<Delegation[]>('delegations', value => { delegations.value = value })
    } else if (selected === 'assignments' || selected === 'governance') {
      if (user.can('space.members.view') || canManageAssignments) read<Member[]>(user.can('space.members.view') ? 'members' : 'assignment-targets', value => { members.value = value })
      if (canReadDelegations) read<Delegation[]>('delegations', value => { delegations.value = value })
      if (canOpenGovernance.value) read<Proposal[]>('proposals', value => {
        proposals.value = canGovern.value ? value : value.filter(item => item.patch.kind === 'bundle_activate')
      })
      if (selected === 'assignments') {
        read<Assignment[]>('assignments', value => { assignments.value = value })
        read<Group[]>('groups', value => { groups.value = value })
        if (user.can('space.roles.view') || canManageAssignments) read<Role[]>(user.can('space.roles.view') ? 'roles' : 'assignable-roles', value => { roles.value = value })
      }
    } else if (selected === 'resources') read<typeof resources.value>('resources', value => { resources.value = value })
    else if (selected === 'policy') read<typeof policy.value>('policy', value => { policy.value = value })
    await Promise.all(jobs)
  } catch (e) {
    if (active && controller === current) { readError.value = e instanceof Error ? e.message : '读取空间配置失败。'; current.abort() }
  } finally { if (active && controller === current) loading.value = false }
}
const changeKind=ref<Change['kind']>(),editingRole=ref<Role>()
function openChange(kind:Change['kind'],role?:Role){changeKind.value=kind;editingRole.value=role}
async function submitted(){changeKind.value=undefined;message.value='已提交独立复核，审批前当前有效权限不变';await load()}
const menuRequests=new Map<string,string>()
async function saveMenus() {
  if (busy.value || loading.value || readError.value || menuValidation.value || !menuDirty.value || !menuBaseline.value || !user.can('space.menus.manage')) return
  const input = { baseline: menuBaseline.value, configuration: JSON.parse(JSON.stringify(menuConfig.value)) as MenuConfiguration }
  const key = JSON.stringify(input)
  if (!menuRequests.has(key)) menuRequests.set(key, newRequestId())
  busy.value = true
  const permissionKey = user.permissionKey
  error.value = ''
  message.value = ''
  try {
    await iamApi.menus({ ...input, request_id: menuRequests.get(key) })
    message.value = '菜单显示配置已保存。'
    loadedMenu.value = JSON.stringify(input.configuration)
    menuRequests.clear()
    await load()
    try {
      await user.loadPermissions(true)
      if (permissionKey !== user.permissionKey) ElMessage.success('菜单显示配置已保存。')
    }
    catch { ElMessage.warning('菜单已保存，权限刷新待确认。') }
  } catch (e) { error.value = e instanceof Error ? e.message : '保存结果待确认，请核对菜单配置。' }
  finally { busy.value = false }
}
function changeVisibility(id: string, visible: boolean) {
  if (!visible && menuConfig.value.preferred_menu === id) menuConfig.value.preferred_menu = ''
}
const restrictionOpen=ref(false),restriction=reactive({request_id:'',kind:'',id:'',version:0,target_id:0,delegation_id:'',reason:''})
const restrictionRole = computed(() => restriction.kind === 'role.disable'
  ? roles.value.find(role => role.id === restriction.id)
  : undefined)
const restrictionDescription = computed(() => {
  switch (restriction.kind) {
    case 'member.freeze':
      return '将立即冻结当前空间访问并登记待交接资产。最后一位治理人也可以冻结，恢复需独立双人核验。'
    case 'role.disable':
      return '将立即停用该角色，并撤销该角色的全部原分配。重新启用须独立复核，恢复成员的该项权限须重新申请角色分配。'
    default:
      return '仅撤销选定的授权来源；存在其他合法来源时，权限继续由剩余来源提供。解除限制需重新审批。'
  }
})
const restrictionDelegations=computed(()=>delegations.value.filter(d=>d.delegate_id===user.user?.id&&!d.revoked_at&&new Date(d.valid_until)>new Date()&&d.governance_actions.includes(restriction.kind==='member.freeze'?'member.freeze':'restriction.revoke')))
function restrict(kind:string,id:string,version:number,target_id=0){Object.assign(restriction,{request_id:crypto.randomUUID(),kind,id,version,target_id,delegation_id:'',reason:''});restriction.delegation_id=restrictionDelegations.value[0]?.id||'';restrictionOpen.value=true;error.value=''}
async function confirmRestriction(){
 if (busy.value || loading.value || readError.value || !restriction.reason.trim()) return
 const delegation=restrictionDelegations.value.find(d=>d.id===restriction.delegation_id);if(!delegation)return
 busy.value=true;error.value=''
 try{
  const receipt=await iamApi.restrict({...restriction,delegation_version:delegation.version,baseline:baseline()})
  restrictionOpen.value=false
  message.value=receipt.execution_fence==='confirmed'?'访问限制已保存，执行边界已确认。历史授权与审批证据保留。':'访问限制已保存；在途执行仍待核实，请到个人设置查看本人操作回执。'
  ElMessage.success(message.value)
  // A committed self-freeze can invalidate this page. Its success receipt must
  // remain distinct from a subsequent directory/permission refresh failure.
  try{await user.loadWorkspaces(true);if(!user.workspaceId){await router.replace('/profile');return}await user.loadPermissions(true);await load()}
  catch{await router.replace('/profile')}
 }catch(e){error.value=e instanceof Error?e.message:'提交状态待核实'}finally{busy.value=false}
}

const proposalTabs = [{ id: 'pending', name: '待处理' }, { id: 'processed', name: '已处理' }, { id: 'mine', name: '我的申请' }]
const proposalTab = ref('pending'), selectedProposalId = ref<string>(), reviewDelegation = ref(''), reviewComment = ref('')
const proposalHeading = ref<HTMLElement>(), proposalTable = ref<HTMLElement>()
const reviewOperations: Record<Change['kind'], string> = { role_upsert: 'role.review', assignment_add: 'assignment.review', action_release: 'assignment.review', group_members_add: 'group.review', delegation_add: 'delegation.review', bundle_activate: 'bundle.activate' }
const proposalState = (item: Proposal) => item.state === 'pending' && Date.parse(item.valid_until) <= now.value ? 'expired' : item.state
const visibleProposals = computed(() => proposals.value.filter(item => proposalTab.value === 'mine' ? item.requester_id === user.user?.id : (proposalState(item) === 'pending') === (proposalTab.value === 'pending')))
const reviewing = computed(() => proposals.value.find(item => item.id === selectedProposalId.value))
const proposalChangeFields = computed(() => {
  const patch = reviewing.value?.patch
  if (!patch) return []
  const fields: { label: string; value: string | number }[] = []
  const add = (label: string, value: string | number | undefined) => {
    fields.push({ label, value: value ?? '—' })
  }
  const memberNames = (ids: number[] = []) => ids.map(memberName).join('、') || '—'
  add('变更类型', changeLabels[patch.kind] || patch.kind)
  switch (patch.kind) {
    case 'role_upsert':
      add('角色名称', patch.name)
      add('变更方式', patch.role_version ? '调整角色' : '创建角色')
      add('角色编号', patch.role_id)
      if (patch.role_version) add('原角色版本', patch.role_version)
      break
    case 'assignment_add':
      add('角色编号', patch.role_id)
      add('角色修订', patch.role_revision)
      add('角色版本', patch.role_version)
      if (patch.subject_type === 'group') {
        add('分配用户组', patch.subject_id ? '#' + patch.subject_id : undefined)
        add('组版本', patch.group_version)
        add('申请时成员名单', memberNames(patch.member_ids))
      } else {
        add('分配成员', patch.subject_id ? memberName(patch.subject_id) : undefined)
      }
      break
    case 'group_members_add':
      add('目标用户组', patch.group_id ? '#' + patch.group_id : undefined)
      add('组版本', patch.group_version)
      add('拟新增成员', memberNames(patch.member_ids))
      break
    case 'delegation_add':
      add('受委派人', patch.delegate_id ? memberName(patch.delegate_id) : undefined)
      add('适用成员', memberNames(patch.target_ids))
      add('可执行的治理动作', patch.governance_actions?.map(action => governanceLabels[action] || action).join('、') || '—')
      add('来源编号', patch.source_ids?.join('、') || '—')
      break
    case 'action_release':
      add('申请解除的成员', patch.subject_id ? memberName(patch.subject_id) : undefined)
      add('禁止记录编号', patch.revocation_sequence ? '#' + patch.revocation_sequence : undefined)
      add('申请原因', patch.reason)
      break
    case 'bundle_activate':
      add('配置包编号', patch.bundle_id)
      break
  }
  const actions = changeActions(patch)
  if (actions.length || patch.kind === 'delegation_add') {
    add(patch.kind === 'delegation_add' ? '可授予的功能许可' : '功能许可', actions.map(actionLabel).join('、') || '—')
  }
  if (patch.valid_until) add('变更有效期', date(patch.valid_until))
  return fields
})
const reviewChoices = computed(() => {
  const item = reviewing.value
  if (!item || item.requester_id === user.user?.id || item.beneficiaries.includes(user.user?.id ?? 0)) return []
  const canReview = user.can(item.patch.kind === 'role_upsert' ? 'space.roles.manage' : 'space.assign.manage') ||
    (item.patch.kind === 'bundle_activate' && canReviewMigration.value)
  if (!canReview) return []
  return delegations.value.filter(d => d.delegate_id === user.user?.id && !d.revoked_at && Date.parse(d.valid_until) > now.value &&
    Date.parse(d.valid_until) >= Date.parse(item.patch.valid_until) && d.governance_actions.includes(reviewOperations[item.patch.kind]) &&
    item.beneficiaries.every(id => d.target_ids.includes(id)) && changeActions(item.patch).every(action => d.grantable_actions.includes(action)) &&
    (item.patch.source_ids ?? []).every(id => d.source_ids.includes(id)))
})
const currentBaseline = computed(() => user.effective?.snapshot)
const staleProposal = computed(() => !!reviewing.value && (reviewing.value.baseline.config_version !== currentBaseline.value?.config_version || reviewing.value.baseline.authorization_epoch !== currentBaseline.value?.authorization_epoch))
async function selectReview(proposal: Proposal) {
  selectedProposalId.value = proposal.id
  reviewDelegation.value = reviewChoices.value[0]?.id ?? ''
  reviewComment.value = ''
  await nextTick()
  proposalHeading.value?.focus()
}
async function closeProposal() {
  const id = selectedProposalId.value
  selectedProposalId.value = undefined
  await nextTick()
  Array.from(proposalTable.value?.querySelectorAll<HTMLElement>('[data-proposal-id]') ?? []).find(el => el.dataset.proposalId === id)?.focus()
}
function selectProposalTab(id: string) {
  if (busy.value) return
  proposalTab.value = id
  selectedProposalId.value = undefined
}
const reviewRequests=new Map<string,string>()
async function decide(proposal: Proposal, decision: 'approve'|'reject'|'withdraw') {
  if (busy.value || loading.value || readError.value || proposal.state !== 'pending' || Date.parse(proposal.valid_until) <= Date.now()) return
  const d = reviewChoices.value.find(item => item.id === reviewDelegation.value && Date.parse(item.valid_until) > Date.now())
  if (decision === 'withdraw' ? proposal.requester_id !== user.user?.id : !d || (decision === 'approve' && staleProposal.value)) return
  if (decision === 'reject' && !reviewComment.value.trim()) return
  const input = { proposal_id: proposal.id, version: proposal.version, decision, delegation_id: d?.id ?? '', delegation_version: d?.version ?? 0, comment: reviewComment.value }
  const key = JSON.stringify(input)
  if (!reviewRequests.has(key)) reviewRequests.set(key, newRequestId())
  busy.value = true
  error.value = ''
  message.value = ''
  try {
    const result = await iamApi.review({ ...input, request_id: reviewRequests.get(key) })
    reviewRequests.delete(key)
    message.value = result.state === 'applied' ? '审批已应用。' : result.state === 'conflict' ? '配置已变化，本次未应用，请重新提交。' : `提案${stateLabels[result.state] || result.state}。`
    if (proposalTab.value === 'pending') proposalTab.value = 'processed'
    if (result.state === 'applied') ElMessage.success(message.value)
    await load()
    try { await user.loadPermissions(true) }
    catch { ElMessage.warning('复核回执已收到，当前权限待读取确认。') }
  } catch (e) { error.value = e instanceof Error ? e.message : '复核状态待核实。' }
  finally { busy.value = false }
}
function changeCategory(id: string) { if (!busy.value) void selectCategory(id) }
function refreshCategory(event: MouseEvent) {
  if (loading.value || busy.value) return
  if (event.currentTarget === document.activeElement) refreshButton.value?.focus()
  void load()
}
watch(tab, () => {
  error.value = ''
  changeKind.value = undefined
  restrictionOpen.value = false
  selectedProposalId.value = undefined
  void load()
}, { immediate: true })
const expiryClock = setInterval(() => { now.value = Date.now() }, 15000)
onScopeDispose(() => { active = false; controller?.abort(); clearInterval(expiryClock) })
</script>

<template><div class="ui-page-stack">
 <PageHeader :title="category ? `空间管理 · ${category.name}` : '空间管理'" eyebrow="协作与治理" :description="`成员、菜单、角色与发布治理在${user.workspace?.name || '当前工作空间'}内生效。`">
   <template v-if="category && tab !== 'migration'" #actions>
     <button ref="refreshButton" type="button" class="ghost-button ui-control-target aria-disabled:opacity-50"
       :aria-disabled="loading || busy || undefined" :aria-busy="loading" @click="refreshCategory">刷新</button>
   </template>
 </PageHeader>
 <PageCategoryTabs label="空间管理分类" :categories="tabs" :active-id="tab" @select="changeCategory">
 <p v-if="message" role="status" class="text-success text-sm">{{ message }}</p>
 <p v-if="error" role="alert" class="text-danger text-sm">{{ error }}</p>
 <LoadingState v-if="loading" label="正在读取空间配置…" />
 <PageState v-else-if="readError" title="读取当前空间分类失败" :description="readError" tone="danger">
   <template #actions><button type="button" class="ghost-button ui-control-target" @click="refreshCategory">重试读取分类</button></template>
 </PageState>
 <div v-else class="space-y-4 min-w-0">
   <section v-if="tab === 'members'" class="space-y-4 min-w-0" aria-label="空间成员">
     <section v-if="!user.effective?.snapshot.target && user.can('space.members.manage')" class="glass-panel p-4 space-y-3">
       <p class="text-sm text-muted">基础角色与原有职责通过兼容入口维护。</p>
       <LegacyUsersView embedded />
     </section>
     <div class="ui-filter-bar">
       <label class="text-sm min-w-0 sm:max-w-sm">成员用户名<input v-model="memberSearch" class="glass-input block w-full mt-1" aria-label="搜索空间成员" /></label>
       <label class="text-sm">访问状态<select ref="memberStatusFilter" v-model="memberFilter" class="glass-input block w-full mt-1" aria-label="成员访问状态"><option value="">全部状态</option><option v-for="status in ['有效', '已冻结', '已停用', '已到期']" :key="status">{{ status }}</option></select></label>
     </div>
     <div v-if="filteredMembers.length" ref="memberTable" class="editorial-table-wrap" role="region" tabindex="0" aria-label="空间成员表格">
       <table class="editorial-table" aria-label="空间成员">
         <thead><tr><th scope="col">序号</th><th scope="col">成员</th><th scope="col">基础角色</th><th scope="col">访问状态</th><th scope="col">权限方案</th><th scope="col">成员期限</th><th scope="col">操作</th></tr></thead>
         <tbody><tr v-for="(member, index) in filteredMembers" :key="member.id" :aria-label="member.username">
           <td class="editorial-table__number">{{ String(index + 1).padStart(2, '0') }}</td>
           <td class="editorial-table__name"><p class="editorial-object-title">{{ member.username }}</p></td>
           <td>{{ roleLabels[member.role] || member.role || '—' }}</td>
           <td><span class="status-badge" :class="{ 'status-badge--success': memberStatus(member) === '有效' }">{{ memberStatus(member) }}</span></td>
           <td class="whitespace-nowrap">{{ member.principal_mode === 'TARGET' ? '角色权限' : '旧权限兼容' }}</td>
           <td class="whitespace-nowrap">{{ member.expires_at ? date(member.expires_at) : '未设期限' }}</td>
           <td><button class="editorial-text-action whitespace-nowrap" :data-member-id="member.id" :aria-expanded="selectedMemberId === member.id" @click="viewMember(member.id)">查看成员</button></td>
         </tr></tbody>
       </table>
     </div>
     <PageState v-else title="暂无匹配成员" description="可调整成员用户名或访问状态筛选。" />
     <section v-if="selectedMember" ref="memberDetail" class="glass-panel p-4 space-y-3" aria-label="成员详情">
       <header class="flex flex-wrap items-center justify-between gap-2"><h2 ref="memberHeading" tabindex="-1" class="editorial-object-title scroll-mt-32">{{ selectedMember.username }} · 成员详情</h2><button class="ghost-button ui-control-target" @click="closeMember">关闭成员详情</button></header>
       <p class="text-sm text-muted">成员 #{{ selectedMember.id }} · {{ memberStatus(selectedMember) }} · 成员版本 {{ selectedMember.version }}</p>
       <p v-if="selectedMember.frozen" class="text-sm text-muted">本空间访问已冻结。托管资产见下方记录。</p>
       <button v-else-if="user.can('space.members.exit')" class="ghost-button ui-control-target" :disabled="busy" @click="restrict('member.freeze', String(selectedMember.id), selectedMember.version, selectedMember.id)">冻结并托管资产</button>
     </section>
     <section v-if="custody.length" class="glass-panel p-4"><h2 class="editorial-object-title">待交接资产</h2><ul class="text-sm text-muted mt-3 space-y-2"><li v-for="item in custody" :key="item.freeze_sequence">{{ memberName(item.user_id) }} · 报表 {{ item.assets.reports }} · 仪表盘 {{ item.assets.dashboards }} · 待交接</li></ul></section>
   </section>
   <section v-else-if="tab === 'menus'" class="space-y-4 min-w-0" aria-label="菜单显示配置">
     <div class="flex flex-wrap items-center justify-between gap-2">
       <p class="text-sm text-muted">菜单显示设置仅控制导航。业务入口仍按当前功能许可访问。</p>
       <button v-if="user.can('space.menus.manage')" class="liquid-button ui-control-target" :disabled="busy || !menuDirty || !!menuValidation" @click="saveMenus">保存菜单显示配置</button>
     </div>
     <p v-if="menuDirty && menuValidation" role="alert" class="text-sm text-danger">{{ menuValidation }}</p>
     <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="菜单配置表格">
       <table class="editorial-table min-w-[690px]" aria-label="菜单配置">
         <thead><tr><th scope="col">编号</th><th scope="col">入口</th><th scope="col">显示名称</th><th scope="col">分组</th><th scope="col">顺序</th><th scope="col">显示</th><th scope="col">推荐首页</th></tr></thead>
         <tbody><tr v-for="item in menuItems" :key="item.id">
           <td class="editorial-table__number">{{ item.id }}</td><td class="whitespace-nowrap">{{ menuName(item.id) }}</td>
           <td><input v-model="item.label" class="glass-input" :aria-label="`${menuName(item.id)}显示名称`" :disabled="busy || !user.can('space.menus.manage')" maxlength="40" /></td>
           <td>
             <select :value="navigationGroup(item)" class="glass-input" :aria-label="`${menuName(item.id)}分类`"
               :disabled="busy || !user.can('space.menus.manage')" @change="changeNavigationGroup(item, ($event.target as HTMLSelectElement).value)">
               <option v-for="group in editorialGroupNames" :key="group" :value="group">{{ group }}</option>
             </select>
           </td>
           <td><input v-model.number="item.order" type="number" min="0" step="1" class="glass-input !w-20" :aria-label="`${menuName(item.id)}顺序`" :disabled="busy || !user.can('space.menus.manage')" /></td>
           <td><input v-model="item.visible" type="checkbox" :aria-label="`显示${menuName(item.id)}`" :disabled="busy || !user.can('space.menus.manage')" @change="changeVisibility(item.id, item.visible)" /></td>
           <td><input v-model="menuConfig.preferred_menu" name="workspace-preferred-menu" type="radio" :value="item.id" :aria-label="`推荐${menuName(item.id)}为首页`" :disabled="busy || !item.visible || !user.can('space.menus.manage')" /></td>
         </tr></tbody>
       </table>
     </div>
     <div class="flex flex-wrap items-center gap-3"><label class="flex items-center gap-2 text-sm"><input v-model="menuConfig.preferred_menu" name="workspace-preferred-menu" type="radio" value="" :disabled="busy || !user.can('space.menus.manage')" />不设置推荐首页</label><span class="text-xs text-muted">配置版本 {{ menuConfig.version }}</span></div>
     <p class="text-xs text-muted">仍有效的个人首页优先，其次是获准的空间推荐首页和首个可见入口。</p>
   </section>
   <section v-else-if="tab === 'roles'" class="space-y-4 min-w-0" aria-label="角色定义">
     <div class="ui-filter-bar"><label class="text-sm min-w-0 sm:max-w-sm">角色名称<input v-model="roleSearch" aria-label="搜索角色" class="glass-input block w-full mt-1" /></label><button v-if="user.can('space.roles.manage')" class="liquid-button ui-control-target" @click="openChange('role_upsert')">创建角色</button></div>
     <div v-if="filteredRoles.length" ref="roleTable" class="editorial-table-wrap" role="region" tabindex="0" aria-label="角色表格">
       <table class="editorial-table" aria-label="角色目录">
         <thead><tr><th scope="col">序号</th><th scope="col">角色名称</th><th scope="col">修订</th><th scope="col">状态</th><th scope="col">分配数</th><th scope="col">操作</th></tr></thead>
         <tbody><tr v-for="(role, index) in filteredRoles" :key="role.id" :aria-label="role.name"><td class="editorial-table__number">{{ String(index + 1).padStart(2, '0') }}</td><td class="editorial-table__name"><p class="editorial-object-title">{{ role.name }}</p></td><td>{{ role.revision }}</td><td><span class="status-badge" :class="{ 'status-badge--success': role.enabled }">{{ role.enabled ? '启用' : '停用' }}</span></td><td>{{ role.assigned_count }}</td><td><button class="editorial-text-action whitespace-nowrap" :data-role-id="role.id" :aria-expanded="selectedRoleId === role.id" @click="viewRole(role.id)">查看角色</button></td></tr></tbody>
       </table>
     </div>
     <PageState v-else title="暂无匹配角色" description="可调整名称筛选。创建角色仍需独立复核。" />
     <section v-if="selectedRole" class="glass-panel p-4 space-y-3" aria-label="角色详情">
       <header class="flex flex-wrap justify-between gap-2"><h2 ref="roleHeading" tabindex="-1" class="editorial-object-title scroll-mt-32">{{ selectedRole.name }} · 角色详情</h2><button class="ghost-button ui-control-target" @click="closeRole">关闭角色详情</button></header>
       <div v-if="selectedRole.actions.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="角色许可表格">
         <table class="editorial-table" aria-label="角色功能许可"><thead><tr><th scope="col">已配置许可</th><th scope="col">运行阶段</th></tr></thead><tbody><tr v-for="id in selectedRole.actions" :key="id"><td>{{ actionLabel(id) }}</td><td>{{ user.effective?.runtime_enabled.includes(id) ? '本阶段启用' : '本阶段未启用' }}</td></tr></tbody></table>
       </div>
       <p v-else class="text-sm text-muted">该修订未配置功能许可。</p>
       <div v-if="user.can('space.roles.manage')" class="flex flex-wrap gap-2"><button class="ghost-button ui-control-target" @click="openChange('role_upsert', selectedRole)">{{ selectedRole.enabled ? '调整角色' : '重新申请启用' }}</button><button v-if="selectedRole.enabled" class="ghost-button text-danger ui-control-target" @click="restrict('role.disable', selectedRole.id, selectedRole.version)">停用并撤销原分配</button></div>
     </section>
   </section>
   <section v-else-if="tab === 'assignments'" class="space-y-4 min-w-0" aria-label="角色分配">
     <div v-if="user.can('space.assign.manage')" class="flex flex-wrap gap-2"><button class="liquid-button ui-control-target" @click="openChange('assignment_add')">分配角色</button><button class="ghost-button ui-control-target" @click="openChange('group_members_add')">增加组成员</button><button class="ghost-button ui-control-target" @click="openChange('delegation_add')">创建治理委派</button></div>
     <label class="block text-sm max-w-xs">授权主体<select v-model="assignmentType" class="glass-input block w-full mt-1" aria-label="授权主体"><option value="user">成员授权</option><option value="group">用户组授权</option></select></label>
     <div v-if="filteredAssignments.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="角色分配表格">
       <table class="editorial-table" aria-label="角色分配"><thead><tr><th scope="col">序号</th><th scope="col">授权主体</th><th scope="col">角色与修订</th><th scope="col">有效期</th><th scope="col">状态</th><th scope="col">操作</th></tr></thead>
         <tbody><tr v-for="(item, index) in filteredAssignments" :key="item.id"><td class="editorial-table__number">{{ String(index + 1).padStart(2, '0') }}</td><td class="editorial-table__name">{{ item.subject_type === 'group' ? groupName(item.subject_id) : memberName(item.subject_id) }}<p v-if="item.subject_type === 'group'" class="editorial-table__meta">获批成员：{{ item.approved_members.map(memberName).join('、') || '—' }}</p></td><td class="whitespace-nowrap">{{ item.role_name }} · 修订 {{ item.role_revision }}</td><td class="whitespace-nowrap">{{ date(item.valid_until) }}</td><td><span class="status-badge" :class="{ 'status-badge--success': assignmentStatus(item) === '有效' }">{{ assignmentStatus(item) }}</span></td><td><button v-if="!item.revoked_at && user.can('space.assign.manage')" class="editorial-text-action whitespace-nowrap" @click="restrict('assignment.revoke', item.id, item.version)">撤销此来源</button></td></tr></tbody>
       </table>
     </div>
     <PageState v-else title="暂无此类角色分配" description="可切换成员或用户组授权查看。" />
     <ActionRestrictionsPanel :delegations="delegations" :members="members" :proposals="proposals" @submitted="submitted" @review="selectCategory('governance')" />
     <section v-if="delegations.length" class="glass-panel ui-card-padding"><h2 class="editorial-object-title">治理委派</h2><article v-for="item in delegations" :key="item.id" class="py-4 border-b border-stroke space-y-2"><div class="flex flex-wrap justify-between gap-3"><span>{{ memberName(item.issuer_id) }} → {{ memberName(item.delegate_id) }}</span><button v-if="!item.revoked_at && user.can('space.assign.manage')" class="ghost-button text-xs ui-control-target" @click="restrict('delegation.revoke', item.id, item.version)">撤销委派</button></div><p class="text-xs text-muted">{{ item.governance_actions.map(op => governanceLabels[op] || op).join('、') }} · {{ item.revoked_at ? '已撤销' : '截止 ' + date(item.valid_until) }}</p><p class="text-xs text-muted">可授予：{{ item.grantable_actions.map(actionLabel).join('、') }}。目标成员：{{ item.target_ids.map(memberName).join('、') }}</p></article></section>
   </section>
   <section v-else-if="tab === 'resources' && resources" class="space-y-4 min-w-0" aria-label="空间资源授权">
     <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="空间资源汇总表格">
       <table class="editorial-table" aria-label="空间资源汇总">
         <thead><tr><th scope="col">资源</th><th scope="col">空间数量</th><th scope="col">入口</th></tr></thead>
         <tbody><tr v-for="item in resourceLinks" :key="item.field"><th scope="row">{{ item.name }}</th><td>{{ resources[item.field] }}</td><td><RouterLink v-if="user.can(item.action)" class="editorial-text-action whitespace-nowrap" :to="{ path: item.path, query: { workspace_id: String(user.workspaceId) } }">查看{{ item.name }}</RouterLink><span v-else class="text-muted text-xs">当前无入口许可</span></td></tr></tbody>
       </table>
     </div>
     <div class="glass-panel p-4 space-y-3 text-sm text-muted">
       <p>资源数量按当前工作空间汇总。具体访问范围由原对象授权和执行契约决定。</p>
       <p>{{ resources.reason }}</p>
       <dl class="grid gap-2 sm:grid-cols-2"><div><dt>敏感明文</dt><dd>{{ resources.sensitive_reveal_enabled ? '已启用' : '未启用' }}</dd></div><div><dt>团队交付</dt><dd>{{ resources.team_delivery_enabled ? '已启用' : '未启用' }}</dd></div></dl>
     </div>
   </section>
   <section v-else-if="tab === 'policy' && policy" class="space-y-4 min-w-0" aria-label="审批发布策略">
     <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="当前策略表格">
       <table class="editorial-table" aria-label="当前审批发布策略"><thead><tr><th scope="col">配置项</th><th scope="col">当前值</th></tr></thead><tbody><tr v-for="item in policyFields" :key="item.name"><th scope="row">{{ item.name }}</th><td>{{ item.value }}</td></tr></tbody></table>
     </div>
     <div class="glass-panel p-4 space-y-3">
       <h2 class="editorial-object-title">正式内容审批流程</h2>
       <p class="text-sm text-muted">{{ policy.reason }}</p>
       <ol class="list-decimal pl-5 space-y-2 text-sm text-muted"><li>开发者提交固定版本。</li><li>申请人及受益人回避。独立复核人验证执行账号、契约和受众。</li><li>具备资格的发布人执行显式发布，正式查询继续检查对象授权。</li></ol>
     </div>
     <button v-if="canGovern" class="ghost-button ui-control-target" @click="changeCategory('governance')">查看治理申请</button>
   </section>
   <section v-else-if="tab === 'governance' && canOpenGovernance" class="space-y-4 min-w-0" aria-label="治理审批记录">
     <div class="grid min-w-0 grid-cols-1 items-start gap-4" :class="{ 'xl:grid-cols-[minmax(0,3fr)_minmax(320px,2fr)]': reviewing }">
       <div class="min-w-0">
       <PageCategoryTabs label="治理申请分类" :categories="proposalTabs" :active-id="proposalTab" @select="selectProposalTab">
         <div v-if="visibleProposals.length" ref="proposalTable" class="editorial-table-wrap" role="region" tabindex="0" aria-label="治理申请表格">
           <table class="editorial-table" aria-label="治理申请">
             <thead><tr><th scope="col">序号</th><th scope="col">申请类型</th><th scope="col">申请人</th><th scope="col">状态</th><th scope="col">截止时间</th><th scope="col">操作</th></tr></thead>
             <tbody><tr v-for="(item, index) in visibleProposals" :key="item.id">
               <td class="editorial-table__number">{{ String(index + 1).padStart(2, '0') }}</td>
               <td class="editorial-table__name"><p class="editorial-object-title">{{ changeLabels[item.patch.kind] || item.patch.kind }}</p><p v-if="item.patch.name" class="editorial-table__meta">{{ item.patch.name }}</p></td>
               <td class="whitespace-nowrap">{{ memberName(item.requester_id) }}</td>
               <td><span class="status-badge whitespace-nowrap">{{ stateLabels[proposalState(item)] || proposalState(item) }}</span></td>
               <td class="whitespace-nowrap">{{ date(item.valid_until) }}</td>
               <td><button class="editorial-text-action whitespace-nowrap" :data-proposal-id="item.id" :aria-expanded="selectedProposalId === item.id" :disabled="busy" @click="selectReview(item)">查看申请</button></td>
             </tr></tbody>
           </table>
         </div>
         <PageState v-else title="当前分类没有治理申请" description="可切换分类查看其他获准记录。" />
       </PageCategoryTabs>
       </div>
       <section v-if="reviewing" class="glass-panel p-4 space-y-4 min-w-0" aria-label="治理申请详情">
         <header class="flex flex-wrap items-center justify-between gap-2"><h2 ref="proposalHeading" tabindex="-1" class="editorial-object-title scroll-mt-32">治理申请详情</h2><button class="ghost-button ui-control-target" :disabled="busy" @click="closeProposal">关闭申请详情</button></header>
         <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
           <div class="min-w-0"><dt class="text-muted">申请编号</dt><dd class="break-all">{{ reviewing.id }}</dd></div>
           <div><dt class="text-muted">状态</dt><dd>{{ stateLabels[proposalState(reviewing)] || proposalState(reviewing) }}</dd></div>
           <div><dt class="text-muted">申请人</dt><dd>{{ memberName(reviewing.requester_id) }}</dd></div>
           <div><dt class="text-muted">受益人</dt><dd class="break-words">{{ reviewing.beneficiaries.map(memberName).join('、') || '无已分配成员' }}</dd></div>
           <div><dt class="text-muted">申请基线</dt><dd>配置 {{ reviewing.baseline.config_version }} · 授权代次 {{ reviewing.baseline.authorization_epoch }}</dd></div>
           <div v-if="currentBaseline"><dt class="text-muted">当前基线</dt><dd>配置 {{ currentBaseline.config_version }} · 授权代次 {{ currentBaseline.authorization_epoch }}</dd></div>
           <div v-if="reviewing.created_at"><dt class="text-muted">申请时间</dt><dd>{{ date(reviewing.created_at) }}</dd></div>
           <div><dt class="text-muted">截止时间</dt><dd>{{ date(reviewing.valid_until) }}</dd></div>
           <div v-if="reviewing.reviewer_id"><dt class="text-muted">复核人</dt><dd>{{ memberName(reviewing.reviewer_id) }}</dd></div>
         </dl>
         <section class="space-y-3 border-t border-stroke pt-4" aria-labelledby="proposal-change-heading">
           <h3 id="proposal-change-heading" class="editorial-object-title">申请变更内容</h3>
           <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
             <div v-for="field in proposalChangeFields" :key="field.label" class="min-w-0">
               <dt class="text-muted">{{ field.label }}</dt>
               <dd class="break-words whitespace-pre-wrap">{{ field.value }}</dd>
             </div>
           </dl>
         </section>
         <form v-if="proposalState(reviewing) === 'pending'" class="space-y-3 border-t border-stroke pt-4" @submit.prevent="decide(reviewing, 'approve')">
           <button v-if="reviewing.requester_id === user.user?.id" type="button" class="ghost-button ui-control-target" :disabled="busy" @click="decide(reviewing, 'withdraw')">撤回申请</button>
           <p v-else-if="reviewing.beneficiaries.includes(user.user?.id || 0)" class="text-sm text-warning">你是受益人，须由其他合格人员独立复核。</p>
           <p v-else-if="!reviewChoices.length" class="text-sm text-muted">当前没有适用的复核委派。</p>
           <template v-else>
             <p v-if="staleProposal" class="text-sm text-warning">申请基线已变化，请重新提交申请。</p>
             <label class="block text-sm max-w-sm">复核使用的委派<el-select v-model="reviewDelegation" class="w-full mt-1" aria-label="复核使用的委派" :disabled="busy"><el-option v-for="d in reviewChoices" :key="d.id" :value="d.id" :label="'委派 ' + d.id.slice(0,8) + ' · ' + date(d.valid_until)" /></el-select></label>
             <label class="block text-sm">复核意见<el-input v-model="reviewComment" type="textarea" maxlength="1000" aria-label="复核意见" class="mt-1" :disabled="busy" /></label>
             <div class="flex flex-wrap gap-2"><button type="button" class="ghost-button ui-control-target" :disabled="busy || !reviewDelegation || !reviewComment.trim()" @click="decide(reviewing, 'reject')">退回</button><button class="liquid-button ui-control-target" :disabled="busy || !reviewDelegation || staleProposal">批准当前变更</button></div>
           </template>
         </form>
       </section>
     </div>
   </section> <MigrationPanel v-else-if="tab==='migration'&&canOpenMigration"/>
 </div>
 <template #unavailable>
  <PageState title="此空间管理分类不可用"
    :description="tabs.length ? '请选择当前可用的分类。' : '尚未获得空间管理分类的许可。'" />
 </template>
 </PageCategoryTabs>
 <ChangeDialog v-if="changeKind" :kind="changeKind" :role="editingRole" :roles="roles" :members="members" :groups="groups" :delegations="delegations" @close="changeKind=undefined" @saved="submitted"/>
 <el-dialog v-model="restrictionOpen" title="收紧访问或授权" width="min(560px, calc(100vw - 24px))" destroy-on-close>
   <form class="space-y-4" @submit.prevent="confirmRestriction">
     <dl v-if="restrictionRole" class="text-sm">
       <dt class="text-muted">目标角色</dt>
       <dd class="break-words">{{ restrictionRole.name }}</dd>
     </dl>
     <p class="text-sm text-muted">{{ restrictionDescription }}</p>
     <label class="block text-sm">明确治理委派<el-select v-model="restriction.delegation_id" class="w-full mt-2" aria-label="收紧使用的委派"><el-option v-for="d in restrictionDelegations" :key="d.id" :value="d.id" :label="`委派 ${d.id.slice(0,8)} · ${date(d.valid_until)}`"/></el-select></label>
     <label class="block text-sm">原因<el-input v-model="restriction.reason" type="textarea" maxlength="1000" aria-label="收紧原因" class="mt-2"/></label>
     <p v-if="error" role="alert" class="text-danger text-sm">{{error}}</p>
     <button class="liquid-button ui-control-target" :disabled="busy||!restriction.delegation_id||!restriction.reason.trim()">提交收紧</button>
   </form>
 </el-dialog>
 </div>
</template>
