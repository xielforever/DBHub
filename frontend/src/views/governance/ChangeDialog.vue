<script setup lang="ts">
import {computed,nextTick,onScopeDispose,ref} from 'vue'
import {useUserStore} from '../../stores/user'
import {iamApi} from '../../api/iam'
import type {Role,Member,Delegation,Group,Change,ProposalCommand,ActionRestriction} from '../../types/iam'
import {actionLabel,changeLabels,governanceLabels} from '../../utils/iam-labels'
const props=defineProps<{kind:Change['kind'];role?:Role;restriction?:ActionRestriction;roles:Role[];members:Member[];groups:Group[];delegations:Delegation[]}>(),emit=defineEmits<{close:[];saved:[]}>(),user=useUserStore()
const releaseReason=ref('')
const operation:Record<Change['kind'],string>={role_upsert:'role.propose',assignment_add:'assignment.propose',action_release:'assignment.propose',group_members_add:'group.propose',delegation_add:'delegation.propose',bundle_activate:'bundle.activate'}
const choices=computed(()=>props.delegations.filter(d=>d.delegate_id===user.user?.id&&!d.revoked_at&&new Date(d.valid_until)>new Date()&&d.governance_actions.includes(operation[props.kind])&&(props.kind!=='action_release'||!!props.restriction&&d.target_ids.includes(props.restriction.subject_id)&&d.grantable_actions.includes(props.restriction.action_id))))
const delegationID=ref(choices.value[0]?.id||''),delegation=computed(()=>choices.value.find(d=>d.id===delegationID.value))
const unavailableOriginal=(props.role?.actions||[]).filter(id=>!user.effective?.runtime_enabled.includes(id))
const name=ref(props.role?.name||''),actions=ref((props.role?.actions||[]).filter(id=>user.effective?.runtime_enabled.includes(id))),roleID=ref(props.role?.id||''),subjectType=ref<'user'|'group'>('user'),subjectID=ref<number>(),groupID=ref<number>(),delegateID=ref<number>(),targetIDs=ref<number[]>([]),memberIDs=ref<number[]>([]),governanceActions=ref<string[]>([])
const localDate=(date:Date)=>new Date(date.getTime()-date.getTimezoneOffset()*60000).toISOString().slice(0,16),until=ref(localDate(new Date(Date.now()+3600000))),error=ref(''),busy=ref(false),impact=ref<{beneficiaries:number[];actions:string[];request_hash:string}>(),command=ref<ProposalCommand>()
const selectedGroup=computed(()=>props.groups.find(g=>g.id===groupID.value)),eligibleMembers=computed(()=>props.members.filter(m=>m.active&&!m.frozen&&delegation.value?.target_ids.includes(m.id))),capabilities=computed(()=>user.effective?.catalog.capabilities.filter(c=>c.scope==='workspace'&&c.state!=='compatibility')||[])
const date=(value:string)=>new Date(value).toLocaleString(),memberName=(id:number)=>props.members.find(m=>m.id===id)?.username||`成员 #${id}`
const permitted=(id:string)=>!!delegation.value?.grantable_actions.includes(id)&&!!user.effective?.runtime_enabled.includes(id)
let previewSequence=0
let expiryTimer: ReturnType<typeof setTimeout> | undefined
const previewButton = ref<HTMLButtonElement>()
const submitButton = ref<HTMLButtonElement>()
const expiredPreviewMessage = '预览有效期已过，请更新有效期后重新查看影响。'
function resetPreview() {
  clearTimeout(expiryTimer)
  previewSequence++
  impact.value = undefined
  command.value = undefined
  error.value = ''
}
function expirePreview(message = expiredPreviewMessage) {
  const restoreFocus = document.activeElement === submitButton.value
  resetPreview()
  error.value = message
  if (restoreFocus) {
    void nextTick(() => {
      if (document.activeElement === document.body) previewButton.value?.focus()
    })
  }
}
function schedulePreviewExpiry(input: ProposalCommand) {
  const remaining = Date.parse(input.valid_until) - Date.now()
  if (remaining <= 0) {
    expirePreview()
    return
  }
  expiryTimer = setTimeout(() => schedulePreviewExpiry(input), Math.min(remaining, 2_147_483_647))
}
onScopeDispose(() => {
  clearTimeout(expiryTimer)
  previewSequence++
})
function selectAction(id:string){resetPreview();if(actions.value.includes(id)){const visit=(value:string,seen=new Set<string>())=>{if(seen.has(value))return;seen.add(value);const cap=capabilities.value.find(c=>c.id===value);for(const dep of cap?.dependencies||[]){if(!permitted(dep)){error.value='依赖许可超出委派上限：'+actionLabel(dep);continue};if(!actions.value.includes(dep))actions.value.push(dep);visit(dep,seen)}};visit(id)}}
function build():ProposalCommand{if(!delegation.value||!user.effective)throw Error('当前委派或空间权限不可用');const expiry=new Date(until.value);if(!Number.isFinite(expiry.getTime()))throw Error('请选择有效期');const patch:Change={kind:props.kind,valid_until:expiry.toISOString()};switch(props.kind){case 'action_release':if(!props.restriction)throw Error('请选择禁止记录');if(!releaseReason.value.trim())throw Error('请填写申请解除的原因');Object.assign(patch,{subject_id:props.restriction.subject_id,action_id:props.restriction.action_id,revocation_sequence:props.restriction.sequence,reason:releaseReason.value.trim()});break;case 'role_upsert':Object.assign(patch,{role_id:props.role?.id||crypto.randomUUID(),role_version:props.role?.version||0,name:name.value.trim(),actions:[...actions.value].sort(),enabled:true});break;case 'assignment_add':{const role=props.roles.find(r=>r.id===roleID.value);if(!role)throw Error('请选择角色');Object.assign(patch,{role_id:role.id,role_version:role.version,role_revision:role.revision,subject_type:subjectType.value,subject_id:subjectType.value==='user'?subjectID.value:selectedGroup.value?.id,...(subjectType.value==='group'?{group_version:selectedGroup.value?.version,member_ids:selectedGroup.value?.member_ids}:{} )});break};case 'group_members_add':Object.assign(patch,{group_id:groupID.value,group_version:selectedGroup.value?.version,member_ids:[...memberIDs.value].sort((a,b)=>a-b)});break;case 'delegation_add':Object.assign(patch,{delegate_id:delegateID.value,actions:[...actions.value].sort(),governance_actions:governanceActions.value,target_ids:targetIDs.value,source_ids:delegation.value.source_ids});break};return{request_id:crypto.randomUUID(),baseline:{config_version:user.effective.snapshot.config_version,authorization_epoch:user.effective.snapshot.authorization_epoch},delegation_id:delegation.value.id,delegation_version:delegation.value.version,valid_until:expiry.toISOString(),patch}}
async function preview() {
  if (busy.value) return
  resetPreview()
  const sequence = previewSequence
  busy.value = true
  try {
    const input = build()
    const result = await iamApi.preview(input)
    if (sequence !== previewSequence) return
    impact.value = result
    command.value = input
    schedulePreviewExpiry(input)
  } catch (err) {
    if (sequence === previewSequence) {
      error.value = err instanceof Error ? err.message : '预览失败'
    }
  } finally {
    busy.value = false
  }
}
async function submit() {
  const input = command.value
  if (busy.value || !input || !impact.value) return
  if (Date.parse(input.valid_until) <= Date.now()) {
    expirePreview()
    return
  }
  const sequence = previewSequence
  clearTimeout(expiryTimer)
  busy.value = true
  error.value = ''
  try {
    await iamApi.propose(input)
    emit('saved')
  } catch (err) {
    if (sequence !== previewSequence) return
    const message = err instanceof Error ? err.message : '提交状态待核实，请保留本次请求编号'
    if (Date.parse(input.valid_until) <= Date.now()) {
      expirePreview(message)
    } else {
      error.value = message
      schedulePreviewExpiry(input)
    }
  } finally {
    busy.value = false
  }
}
</script>

<template><el-dialog append-to-body align-center :style="{maxHeight: 'calc(100dvh - 24px)', overflowY: 'auto'}" :model-value="true" :title="changeLabels[kind]||'治理变更'" width="min(820px, calc(100vw - 24px))" destroy-on-close :close-on-click-modal="false" @close="$emit('close')"><form class="space-y-5" @submit.prevent="preview">
 <p class="text-sm text-muted">在「{{user.workspace?.name}}」提交变更。通过独立复核后生效，数据范围与数据库账号权限保持原约束。</p>
 <p v-if="unavailableOriginal.length" role="status" class="text-sm text-warning">原定义包含尚未启用的功能：{{unavailableOriginal.map(actionLabel).join('、')}}。这些功能当前不生效；本次变更只提交可用许可，请在预览中核对。</p>
 <label class="block text-sm">使用治理委派<el-select v-model="delegationID" class="w-full mt-2" aria-label="使用治理委派" @change="resetPreview"><el-option v-for="d in choices" :key="d.id" :value="d.id" :label="`委派 ${d.id.slice(0,8)} · 截止 ${date(d.valid_until)}`"/></el-select></label>
 <p v-if="!choices.length" role="alert" class="text-warning text-sm">没有当前有效且包含此治理动作的委派。具备操作权限并不意味着可以给他人授权。</p>
 <template v-if="kind==='role_upsert'"><label class="block text-sm">角色名称<el-input v-model="name" maxlength="100" aria-label="角色名称" class="mt-2" @input="resetPreview"/></label><p class="text-xs text-muted">名称可以自定义。停用的历史分配不会因重新启用角色自动恢复。</p></template>
 <section v-if="kind==='action_release'&&restriction" class="ui-inset-card ui-inset-card--warning rounded-xl border border-warning/20 p-4 space-y-3" aria-label="解除禁止的精确范围"><p>{{restriction.username}} · {{actionLabel(restriction.action_id)}}</p><p class="text-xs text-muted">禁止记录 #{{restriction.sequence}} · {{date(restriction.recorded_at)}}。原原因：{{restriction.reason}}</p><p class="text-sm text-muted">仅在获批期限内解除这条禁止。已有角色和原数据授权仍须有效；到期后禁止自动恢复，新的禁止记录立即优先生效。</p><label class="block text-sm">申请解除的原因<el-input v-model="releaseReason" type="textarea" maxlength="1000" aria-label="申请解除的原因" class="mt-2" @input="resetPreview"/></label></section>
 <template v-if="kind==='assignment_add'"><label class="block text-sm">角色<el-select v-model="roleID" class="w-full mt-2" aria-label="分配角色" @change="resetPreview"><el-option v-for="item in roles.filter(r=>r.enabled)" :key="item.id" :value="item.id" :label="`${item.name} · 版本 ${item.revision}`"/></el-select></label><label class="block text-sm">分配给<el-radio-group v-model="subjectType" class="ml-3" @change="resetPreview"><el-radio value="user">成员</el-radio><el-radio value="group">用户组</el-radio></el-radio-group></label><el-select v-if="subjectType==='user'" v-model="subjectID" filterable aria-label="分配成员" class="w-full" @change="resetPreview"><el-option v-for="member in eligibleMembers" :key="member.id" :value="member.id" :label="member.username"/></el-select><el-select v-else v-model="groupID" aria-label="分配用户组" class="w-full" @change="resetPreview"><el-option v-for="group in groups" :key="group.id" :value="group.id" :label="`${group.name} · ${group.member_ids.length} 人`"/></el-select><p class="text-xs text-muted">组分配固定本次获批的成员名单。以后加入组的成员不会自动获得该角色。</p></template>
 <template v-if="kind==='group_members_add'"><label class="block text-sm">用户组<el-select v-model="groupID" class="w-full mt-2" aria-label="增加成员的组" @change="resetPreview"><el-option v-for="group in groups" :key="group.id" :value="group.id" :label="group.name"/></el-select></label><label class="block text-sm">新增成员<el-select v-model="memberIDs" multiple filterable class="w-full mt-2" aria-label="新增组成员" @change="resetPreview"><el-option v-for="member in eligibleMembers.filter(m=>!selectedGroup?.member_ids.includes(m.id))" :key="member.id" :value="member.id" :label="member.username"/></el-select></label><p class="text-xs text-muted">关联数据资源的组需要资源审批，当前入口会阻止数据受众扩张。</p></template>
 <template v-if="kind==='delegation_add'"><label class="block text-sm">受委派人<el-select v-model="delegateID" filterable class="w-full mt-2" aria-label="受委派人" @change="resetPreview"><el-option v-for="member in eligibleMembers.filter(m=>m.id!==user.user?.id)" :key="member.id" :value="member.id" :label="member.username"/></el-select></label><fieldset class="space-y-2"><legend class="text-sm mb-2">可执行的治理动作</legend><label v-for="op in delegation?.governance_actions||[]" :key="op" class="inline-flex items-center gap-2 mr-4 mb-2 text-xs"><input v-model="governanceActions" type="checkbox" :value="op" @change="resetPreview"/>{{governanceLabels[op]||op}}</label></fieldset><label class="block text-sm">可影响的成员<el-select v-model="targetIDs" multiple filterable class="w-full mt-2" aria-label="委派目标成员" @change="resetPreview"><el-option v-for="member in eligibleMembers" :key="member.id" :value="member.id" :label="member.username"/></el-select></label></template>
 <fieldset v-if="kind==='role_upsert'||kind==='delegation_add'" class="space-y-3"><legend class="text-sm mb-2">{{kind==='delegation_add'?'可授予的功能许可':'角色功能许可'}}</legend><div class="ui-inset-card max-h-64 overflow-auto rounded-xl border border-stroke p-3 grid sm:grid-cols-2 gap-3"><label v-for="cap in capabilities" :key="cap.id" class="flex items-start gap-2 text-xs" :class="!permitted(cap.id)?'text-subtle':''"><input v-model="actions" type="checkbox" :value="cap.id" :disabled="!permitted(cap.id)&&!actions.includes(cap.id)" @change="selectAction(cap.id)"/><span :title="cap.id">{{actionLabel(cap.id)}}<span v-if="!user.effective?.runtime_enabled.includes(cap.id)" class="block mt-1">本阶段未开放</span><span v-else-if="!delegation?.grantable_actions.includes(cap.id)" class="block mt-1">超出委派上限</span></span></label></div></fieldset>
 <label class="block text-sm">授权有效期至<input v-model="until" type="datetime-local" class="glass-input mt-2" required @input="resetPreview"/></label>
 <p v-if="error" role="alert" class="text-danger text-sm">{{error}}</p>
 <section v-if="impact" class="ui-inset-card ui-inset-card--accent rounded-xl border border-accent/20 bg-accent-soft p-4 space-y-3" aria-label="变更影响预览"><h3 class="font-medium">待复核的变更</h3><p class="text-sm">受益成员：{{impact.beneficiaries.length?impact.beneficiaries.map(memberName).join('、'):'暂未分配成员；角色定义仍须复核'}}</p><p class="text-sm text-muted">功能许可：{{impact.actions.map(actionLabel).join('、')||'不直接增加功能许可'}}</p><p class="text-xs text-muted">申请人和全部受益人不能批准此变更。截止 {{date(command?.patch.valid_until||'')}}。</p></section>
 <div class="flex flex-wrap justify-end gap-3"><button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="$emit('close')">取消</button><button ref="previewButton" class="ghost-button ui-control-target" :disabled="busy||!delegation">查看影响</button><button v-if="impact" ref="submitButton" type="button" class="liquid-button ui-control-target" :disabled="busy" @click="submit">提交独立复核</button></div>
 </form></el-dialog></template>
