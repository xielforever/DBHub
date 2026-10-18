<template>
  <el-dialog :model-value="true" title="报表版本与发布" width="min(1020px, calc(100vw - 24px))" class="glass-dialog" :close-on-click-modal="false" :close-on-press-escape="!busy" :show-close="!busy" @close="close">
    <div class="space-y-4">
      <p v-if="detail.state.value.status==='loading'" role="status" class="text-sm text-white/50">正在读取版本与发布记录…</p>
      <p v-else-if="detail.state.value.status==='error'" role="alert" class="text-sm text-rose-300">{{ detail.state.value.message }}</p>
      <button class="ghost-button text-xs" :disabled="busy" @click="load">刷新状态</button>
      <p v-if="error" role="alert" class="text-sm text-rose-300">{{ error }}</p>
      <template v-if="data">
        <div class="glass-card p-3 space-y-2"><h2 class="font-medium break-words">{{ data.report.draft.name }}</h2><p class="text-xs text-white/55">责任人 {{ data.report.owner_name }} · {{ data.report.active_release_id?'已发布':'未发布' }} · 发布序号 {{ data.report.publication_no }}</p><p class="text-xs text-white/50">修改草稿不影响已发布版本，正式运行始终使用获批版本与报表执行账号。</p></div>
        <div class="flex flex-wrap items-center gap-2"><el-select v-model="selectedReview" aria-label="选择报表版本" class="!w-72 max-w-full" :disabled="busy"><el-option v-for="version in data.versions.items" :key="version.id" :value="version.review_id" :label="`v${version.number} · ${statusLabels[version.status]} · ${version.definition.name}`" /></el-select><button class="ghost-button text-xs" :disabled="versionPage<=1 || busy" @click="changeVersionPage(-1)">较新版本</button><span class="text-xs text-white/50">{{ versionPage }}</span><button class="ghost-button text-xs" :disabled="versionPage*10>=data.versions.total || busy" @click="changeVersionPage(1)">较早版本</button></div>
        <p v-if="!data.versions.total" class="text-sm text-white/50">尚未提交版本，请先从开发列表提交审批。</p>
        <section v-if="selected" class="space-y-3" aria-label="报表版本内容">
          <p class="text-xs text-white/55">v{{ selected.number }} · {{ statusLabels[selected.status] }} · 提交人 {{ selected.submitter_name }} · {{ formatTime(selected.created_at) }}</p>
          <p class="text-xs text-white/50">数据源修订 #{{ selected.definition.definition_revision_id }} · {{ selected.definition.database_name || '默认数据库' }} · {{ selected.definition.chart_type }}</p>
          <pre class="bg-black/25 rounded-lg p-3 text-xs overflow-auto max-h-56 whitespace-pre-wrap break-words">{{ selected.definition.sql_text }}</pre>
          <details v-if="previous" class="text-xs text-white/55 space-y-2"><summary class="cursor-pointer py-1">与 v{{ previous.number }} 对比：{{ differences.length?differences.join('、'):'内容相同' }}</summary><div class="grid grid-cols-1 sm:grid-cols-2 gap-2"><pre class="bg-black/25 p-3 rounded-lg overflow-auto max-h-56 whitespace-pre-wrap break-words">v{{ previous.number }}：{{ JSON.stringify(previous.definition,null,2) }}</pre><pre class="bg-black/25 p-3 rounded-lg overflow-auto max-h-56 whitespace-pre-wrap break-words">v{{ selected.number }}：{{ JSON.stringify(selected.definition,null,2) }}</pre></div></details>
          <p v-if="selected.comment" class="text-xs text-white/60 break-words">审批意见：{{ selected.comment }}（{{ selected.decider_name }}）</p>
          <p v-if="selected.account_name" class="text-xs text-white/55">批准执行账号：{{ selected.account_name }}；受众：{{ selected.manifest?.user_ids.length ?? 0 }} 位用户、{{ selected.manifest?.group_ids.length ?? 0 }} 个用户组</p>
          <p v-if="isAdmin && selected.status==='pending' && selected.submitted_by===user.user?.id" class="text-sm text-amber-200">不能审批自己提交的版本，请由其他管理员审阅。</p>
          <div v-if="canReview" class="border border-white/10 rounded-lg p-3 space-y-3">
            <p class="text-xs text-white/55">选择执行账号及受众，用该账号验证冻结的查询，确认数据范围后批准。</p>
            <label class="block text-xs text-white/60 space-y-1">报表执行账号<el-select v-model="accountId" aria-label="审批执行账号" class="w-full" :disabled="busy"><el-option v-for="account in eligibleAccounts" :key="account.id" :value="account.id" :label="`${account.name} · ${account.username}`" /></el-select></label>
            <p v-if="!eligibleAccounts.length" class="text-xs text-amber-200">此修订没有可用执行账号，请在“执行账号”中添加后刷新状态。</p>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <label class="text-xs text-white/60 space-y-1">共享用户<el-select v-model="userIds" aria-label="审批共享用户" multiple filterable remote :remote-method="searchUsers" class="w-full" :disabled="busy"><el-option v-for="member in users" :key="member.id" :value="member.id" :label="member.username" :disabled="!member.is_active" /></el-select></label>
              <label class="text-xs text-white/60 space-y-1">共享用户组<el-select v-model="groupIds" aria-label="审批共享用户组" multiple filterable remote :remote-method="searchGroups" class="w-full" :disabled="busy"><el-option v-for="group in groups" :key="group.id" :value="group.id" :label="group.name" /></el-select></label>
            </div>
            <p class="text-xs text-amber-100/75">受众会获得此查询返回的数据。部门数据隔离请使用受限数据库视图或不同执行账号。</p>
            <button class="ghost-button text-xs" :disabled="busy || !accountId || (!userIds.length && !groupIds.length) || validation.state.value.status==='loading'" @click="validate">{{ validation.state.value.status==='loading'?'正在验证…':'使用执行账号验证' }}</button>
            <button v-if="validation.state.value.status==='loading'" class="ghost-button text-xs ml-2" @click="validation.reset">取消验证</button>
            <p v-if="validation.state.value.status==='error'" role="alert" class="text-sm text-rose-300">{{ validation.state.value.message }}</p>
            <template v-else-if="validation.state.value.status==='success'"><p class="text-xs text-emerald-200">执行账号验证通过，15 分钟内可批准；更改账号或受众后需要重新验证。</p><ReportResult :type="selected.definition.chart_type" :config="selected.definition.chart_config" :result="validation.state.value.data.result" :title="selected.definition.name" /></template>
            <label class="block text-xs text-white/60 space-y-1">审批意见<el-input v-model="comment" aria-label="审批意见" type="textarea" maxlength="1000" :disabled="busy" placeholder="退回必须填写原因" /></label>
            <div class="flex flex-wrap gap-2"><button class="liquid-button text-xs" :disabled="busy || validation.state.value.status!=='success'" @click="decide('approved')">批准此版本</button><button class="ghost-button text-xs" :disabled="busy || !comment.trim() || validation.state.value.status==='loading'" @click="decide('rejected')">退回修改</button></div>
          </div>
          <div class="flex flex-wrap gap-2">
            <button v-if="selected.status==='pending' && data.report.owner_id===user.user?.id" class="ghost-button text-xs" :disabled="busy" @click="decide('withdrawn')">撤回申请</button>
            <button v-if="isAdmin && selected.status==='approved' && (!data.report.active_version || selected.number>=data.report.active_version)" class="liquid-button text-xs" :disabled="busy || activeReview===selected.review_id" @click="release('publish',selected.review_id)">发布此版本</button>
            <button v-if="isAdmin && selected.status==='approved' && wasPublished && activeReview!==selected.review_id" class="ghost-button text-xs" :disabled="busy" @click="release('rollback',selected.review_id)">回滚到此版本</button>
            <button v-if="isAdmin && data.report.active_release_id" class="ghost-button text-xs text-amber-200" :disabled="busy" @click="release('unpublish',0)">下线报表</button>
          </div>
        </section>
        <section class="space-y-2 border-t border-white/10 pt-4" aria-label="报表发布历史"><h3 class="text-sm font-medium">发布历史</h3><p v-if="!data.releases.total" class="text-xs text-white/50">尚无发布记录</p><p v-for="event in data.releases.items" :key="event.id" class="text-xs text-white/55 break-words">#{{ event.number }} · {{ releaseLabels[event.kind] }}<span v-if="event.version"> v{{ event.version }}</span> · {{ event.performer_name }} · {{ formatTime(event.created_at) }}</p><div v-if="data.releases.total>10" class="flex gap-2"><button class="ghost-button text-xs" :disabled="releasePage<=1 || busy" @click="changeReleasePage(-1)">较新记录</button><button class="ghost-button text-xs" :disabled="releasePage*10>=data.releases.total || busy" @click="changeReleasePage(1)">较早记录</button></div></section>
        <section v-if="isAdmin" class="space-y-2 border-t border-white/10 pt-4"><h3 class="text-sm font-medium">移交责任人</h3><div class="flex flex-wrap gap-2"><el-select v-model="newOwner" aria-label="报表新责任人" filterable remote :remote-method="searchUsers" class="!w-64 max-w-full" :disabled="busy"><el-option v-for="member in writers" :key="member.id" :value="member.id" :label="member.username" /></el-select><button class="ghost-button text-xs" :disabled="busy || !newOwner || newOwner===data.report.owner_id" @click="transfer">移交</button></div></section>
      </template>
    </div>
  </el-dialog>
</template>
<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import { managedReportApi, type ManagedReport, type ReportVersion, type ReportRelease, type ReportAccount, type ReportPage, type ReportValidation } from '../../api/managed-report'
import { adminApi, type UserItem, type UserGroup } from '../../api/admin'
import { useUserStore } from '../../stores/user'
import { useReportRun } from './useReportRun'
import { reportCommandKeys, reportDefinitionDiff } from './reportCommands'
import ReportResult from './ReportResult.vue'
const props=defineProps<{reportId:number}>(),emit=defineEmits<{close:[];changed:[]}>(),user=useUserStore(),isAdmin=computed(()=>user.role==='admin')
const detail=useReportRun<{report:ManagedReport;versions:ReportPage<ReportVersion>;releases:ReportPage<ReportRelease>}>(),validation=useReportRun<ReportValidation>(),keys=reportCommandKeys()
const data=computed(()=>detail.state.value.status==='success'?detail.state.value.data:undefined),busy=ref(false),error=ref(''),versionPage=ref(1),releasePage=ref(1),selectedReview=ref<number>(),accountId=ref<number>(),userIds=ref<number[]>([]),groupIds=ref<number[]>([]),comment=ref(''),newOwner=ref<number>()
const accounts=ref<ReportAccount[]>([]),users=ref<UserItem[]>([]),groups=ref<UserGroup[]>([])
const selected=computed(()=>data.value?.versions.items.find(v=>v.review_id===selectedReview.value)),previous=computed(()=>{const versions=data.value?.versions.items??[],index=versions.findIndex(v=>v.review_id===selectedReview.value);return index>=0?versions[index+1]:undefined})
const differences=computed(()=>selected.value&&previous.value?reportDefinitionDiff(previous.value.definition,selected.value.definition):[]),eligibleAccounts=computed(()=>accounts.value.filter(a=>a.enabled&&a.definition_revision_id===selected.value?.definition.definition_revision_id)),writers=computed(()=>users.value.filter(u=>u.is_active&&['admin','developer'].includes(u.role)))
const canReview=computed(()=>isAdmin.value&&selected.value?.status==='pending'&&selected.value.submitted_by!==user.user?.id),activeReview=computed(()=>data.value?.report.active_review_id),wasPublished=computed(()=>selected.value?.was_published)
const statusLabels={pending:'待审批',approved:'已批准',rejected:'已退回',withdrawn:'已撤回'},releaseLabels={publish:'发布',rollback:'回滚',unpublish:'下线'},formatTime=(v:string)=>new Date(v).toLocaleString(),errorText=(e:unknown)=>e instanceof Error?e.message:'操作失败，请刷新状态后核对'
let alive=true,userGeneration=0,groupGeneration=0
async function load(){validation.reset();await detail.run(`${props.reportId}:${versionPage.value}:${releasePage.value}`,async signal=>{const [report,versions,releases]=await Promise.all([managedReportApi.get(props.reportId,signal),managedReportApi.versions(props.reportId,versionPage.value,signal),managedReportApi.releases(props.reportId,releasePage.value,signal)]);return {report,versions,releases}});if(data.value&&!data.value.versions.items.some(v=>v.review_id===selectedReview.value))selectedReview.value=data.value.versions.items[0]?.review_id;if(isAdmin.value){try{const result=await managedReportApi.accounts();if(alive)accounts.value=result.items}catch(e){if(alive)error.value=errorText(e)}}}
async function searchUsers(keyword=''){const run=++userGeneration;try{const result=await adminApi.users({keyword,page:1,page_size:100});if(alive&&run===userGeneration)users.value=result.items}catch(e){if(alive&&run===userGeneration)error.value=errorText(e)}}
async function searchGroups(keyword=''){const run=++groupGeneration;try{const result=await adminApi.groups({keyword,page:1,page_size:100});if(alive&&run===groupGeneration)groups.value=result.items}catch(e){if(alive&&run===groupGeneration)error.value=errorText(e)}}
function close(){if(!busy.value){validation.reset();detail.reset();emit('close')}}
function changeVersionPage(step:number){versionPage.value+=step;selectedReview.value=undefined;void load()}
function changeReleasePage(step:number){releasePage.value+=step;void load()}
watch(selected,version=>{validation.reset();accountId.value=undefined;userIds.value=version?.manifest?.user_ids?[...version.manifest.user_ids]:[];groupIds.value=version?.manifest?.group_ids?[...version.manifest.group_ids]:[];comment.value='';error.value=''},{flush:'sync'})
// Select controls may emit a new array containing the same members on blur.
// Only an actual account/audience change invalidates an in-flight validation.
watch(()=>JSON.stringify([accountId.value,[...userIds.value].sort((a,b)=>a-b),[...groupIds.value].sort((a,b)=>a-b)]),()=>validation.reset(),{flush:'sync'})
async function validate(){if(!selected.value||!accountId.value)return;const id=selected.value.review_id,payload={account_id:accountId.value,user_ids:[...userIds.value],group_ids:[...groupIds.value]};await validation.run(JSON.stringify([id,payload]),signal=>managedReportApi.validate(id,payload,signal))}
async function confirmed(text:string,title:string){try{await ElMessageBox.confirm(text,title,{confirmButtonText:'确认',cancelButtonText:'取消',type:'warning'});return true}catch{return false}}
async function decide(decision:'approved'|'rejected'|'withdrawn'){const version=selected.value;if(!version||busy.value)return;const validated=validation.state.value.status==='success'?validation.state.value.data.validation_id:undefined;if(decision==='approved'&&!validated)return;if(!await confirmed(decision==='approved'?`批准 v${version.number}，固定当前执行账号和所选受众？`:`确认${decision==='rejected'?'退回':'撤回'} v${version.number}？`,'审批决定'))return
 const payload={decision,comment:comment.value,...(decision==='approved'?{validation_id:validated}:{})};busy.value=true;error.value='';try{await managedReportApi.decide(version.review_id,payload,keys.get(`decision:${version.review_id}`,payload));keys.clear();if(alive){await load();emit('changed')}}catch(e){if(alive)error.value=errorText(e)}finally{if(alive)busy.value=false}}
async function release(kind:'publish'|'rollback'|'unpublish',review_id:number){if(!data.value||busy.value)return;if(!await confirmed(kind==='unpublish'?'下线后，所有查看者将无法继续查询此报表。':`${kind==='rollback'?'回滚':'发布'}所选获批版本，使用批准的执行账号并向批准的受众开放？`,'正式报表发布'))return
 const payload={kind,review_id,expected_publication_no:data.value.report.publication_no};busy.value=true;error.value='';try{await managedReportApi.release(props.reportId,payload,keys.get('release',payload));if(alive){await load();emit('changed')}}catch(e){if(alive)error.value=errorText(e)}finally{if(alive)busy.value=false}}
async function transfer(){const report=data.value?.report,owner=newOwner.value;if(!report||!owner||busy.value)return;if(!await confirmed('移交后，新责任人可以开发草稿和提交审批；当前发布内容保持生效。','移交责任人'))return;busy.value=true;error.value='';const payload={version:report.draft_version,owner_id:owner};try{await managedReportApi.transfer(report.id,report.draft_version,owner,keys.get('transfer',payload));if(alive){newOwner.value=undefined;await load();emit('changed')}}catch(e){if(alive)error.value=errorText(e)}finally{if(alive)busy.value=false}}
onMounted(()=>{void load();if(isAdmin.value){void searchUsers();void searchGroups()}})
onScopeDispose(()=>{alive=false;userGeneration++;groupGeneration++;keys.clear()})
</script>
