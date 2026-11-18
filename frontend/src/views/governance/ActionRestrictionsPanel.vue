<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import {onMounted,ref} from 'vue'
import {useUserStore} from '../../stores/user'
import {iamApi} from '../../api/iam'
import type {ActionRestriction,Delegation,Member,Proposal} from '../../types/iam'
import {actionLabel} from '../../utils/iam-labels'
import ChangeDialog from './ChangeDialog.vue'
const props=defineProps<{delegations:Delegation[];members:Member[];proposals:Proposal[]}>()
const emit=defineEmits<{submitted:[];review:[]}>(),user=useUserStore(),restrictions=ref<ActionRestriction[]>([]),selected=ref<ActionRestriction>(),loading=ref(false),error=ref('')
const pending=(item:ActionRestriction)=>props.proposals.some(p=>p.state==='pending'&&new Date(p.valid_until)>new Date()&&p.patch.kind==='action_release'&&p.patch.subject_id===item.subject_id&&p.patch.action_id===item.action_id&&p.patch.revocation_sequence===item.sequence)
const date=(value:string)=>new Date(value).toLocaleString()
async function load(){loading.value=true;error.value='';restrictions.value=[];try{restrictions.value=await iamApi.read<ActionRestriction[]>('restrictions')}catch(e){error.value=e instanceof Error?e.message:'读取禁止记录失败'}finally{loading.value=false}}
async function submitted(){selected.value=undefined;await load();emit('submitted')}
onMounted(()=>void load())
</script>

<template><section class="glass-card space-y-4 ui-card-padding" aria-labelledby="action-restrictions-title">
 <header class="flex flex-wrap justify-between gap-3"><h3 id="action-restrictions-title">禁止操作与解除审批</h3><button class="ghost-button text-xs ui-control-target" :disabled="loading" @click="load">刷新禁止记录</button></header>
 <p class="text-sm text-muted">只展示当前治理委派范围内的成员与操作。解除需要独立复核；重新分配角色、恢复角色或回退配置都不会清除禁止。</p>
 <p v-if="error" role="alert" class="text-danger text-sm">{{error}}</p><LoadingState v-if="loading" label="正在读取禁止记录…" />
 <article v-for="item in restrictions" :key="item.sequence" class="border-t border-stroke pt-4 space-y-2">
  <div class="flex flex-wrap justify-between gap-3"><h4>{{item.username}} · {{actionLabel(item.action_id)}}</h4><span class="text-sm">{{item.release_until?'已限期解除':pending(item)?'解除待复核，仍禁止':'禁止中'}}</span></div>
  <p class="text-xs text-muted">记录 #{{item.sequence}} · {{date(item.recorded_at)}} · {{item.reason}}</p>
  <p v-if="item.release_until" class="text-xs text-muted">解除截止 {{date(item.release_until)}}；届时自动恢复禁止。</p>
  <p v-if="item.release_history.length" class="text-xs text-muted">保留 {{item.release_history.length}} 次独立审批记录；历史批准不会覆盖后续新增的禁止。</p>
  <button v-if="pending(item)" class="ghost-button text-xs ui-control-target" @click="$emit('review')">查看解除申请</button>
  <button v-else-if="!item.release_until&&user.can('space.assign.manage')" class="ghost-button text-xs ui-control-target" @click="selected=item">申请解除此禁止</button>
 </article>
 <p v-if="!loading&&!restrictions.length&&!error" class="text-sm text-muted">当前委派范围内没有禁止操作记录。</p>
 <ChangeDialog v-if="selected" kind="action_release" :restriction="selected" :roles="[]" :members="members" :groups="[]" :delegations="delegations" @close="selected=undefined" @saved="submitted"/>
</section></template>
