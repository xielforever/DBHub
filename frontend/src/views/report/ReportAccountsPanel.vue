<template>
  <section class="space-y-4" aria-label="报表执行账号管理">
    <div class="flex flex-wrap items-center justify-between gap-3"><p class="text-sm text-white/55">维护数据库只读账号，供经过审批的正式报表使用。</p><div class="flex gap-2"><button class="ghost-button text-xs" :disabled="busy" @click="load">刷新</button><button class="liquid-button text-xs" :disabled="busy" @click="create">新增执行账号</button></div></div>
    <p v-if="message" role="status" class="text-sm text-emerald-200">{{ message }}</p>
    <p v-if="error" role="alert" class="text-sm text-rose-300">{{ error }}</p>
    <p v-if="loading" role="status" class="text-sm text-white/50">正在加载执行账号…</p>
    <div v-else class="grid grid-cols-1 lg:grid-cols-2 gap-4">
      <article v-for="account in accounts" :key="account.id" class="glass-card p-4 space-y-3 min-w-0" :aria-label="`执行账号：${account.name}`">
        <div class="flex justify-between gap-2"><h2 class="font-medium break-words">{{ account.name }}</h2><span class="text-xs shrink-0" :class="account.enabled?'text-emerald-200':'text-amber-200'">{{ account.enabled?'启用':'停用' }}</span></div>
        <p class="text-xs text-white/55 break-words">{{ account.source_name }} · 数据源修订 #{{ account.definition_revision_id }} · {{ account.username }}</p>
        <p class="text-xs text-white/50">当前服务 {{ account.report_count }} 份已发布报表</p>
        <div class="flex flex-wrap gap-2"><button class="ghost-button text-xs" :disabled="busy || !account.enabled" @click="test(account)">测试只读连接</button><button class="ghost-button text-xs" :disabled="busy" @click="rotate(account)">轮换密码</button><button class="ghost-button text-xs" :disabled="busy" @click="setEnabled(account)">{{ account.enabled?'停用':'启用' }}</button></div>
      </article>
      <p v-if="!accounts.length && !error" class="glass-card p-8 text-sm text-white/50">暂无报表执行账号</p>
    </div>
    <el-dialog v-model="editorOpen" :title="rotating?'轮换执行账号密码':'新增报表执行账号'" width="min(600px, calc(100vw - 24px))" class="glass-dialog" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving">
      <form class="space-y-4" @submit.prevent="save">
        <template v-if="!rotating">
          <label class="block text-xs text-white/60 space-y-1">账号名称<el-input v-model="form.name" aria-label="执行账号名称" maxlength="100" :disabled="saving" /></label>
          <label class="block text-xs text-white/60 space-y-1">数据源<el-select v-model="familySource" aria-label="执行账号数据源" filterable remote :remote-method="searchSources" :loading="sourcesLoading" class="w-full" :disabled="saving" @change="loadRevisions(1)"><el-option v-for="source in sources" :key="source.id" :value="source.id" :label="source.name" /></el-select></label>
          <label v-if="familySource" class="block text-xs text-white/60 space-y-1">固定修订<el-select v-model="form.definition_revision_id" aria-label="执行账号修订" class="w-full" :disabled="saving || sourcesLoading"><el-option v-for="source in revisions" :key="source.id" :value="source.id" :label="`修订 ${source.revision} · ${source.enabled?'启用':'停用'}`" :disabled="!source.enabled" /></el-select></label>
          <div v-if="revisionTotal>10" class="flex gap-2 text-xs"><button type="button" class="ghost-button" :disabled="revisionPage<=1 || saving || sourcesLoading" @click="loadRevisions(revisionPage-1)">较新修订</button><button type="button" class="ghost-button" :disabled="revisionPage*10>=revisionTotal || saving || sourcesLoading" @click="loadRevisions(revisionPage+1)">较早修订</button></div>
          <label class="block text-xs text-white/60 space-y-1">数据库用户名<el-input v-model="form.username" aria-label="执行账号用户名" maxlength="128" autocomplete="off" :disabled="saving" /></label>
        </template>
        <p v-else class="text-sm text-white/60">{{ rotating.name }} · {{ rotating.username }}。保持数据库身份不变，仅更新密码。</p>
        <label class="block text-xs text-white/60 space-y-1">数据库密码<el-input v-model="form.password" aria-label="执行账号密码" type="password" show-password autocomplete="new-password" :disabled="saving" /></label>
        <p class="text-xs text-white/45">密码加密保存。请在数据库中限制账号的读取范围；正式报表必须使用选定账号验证成功，才能批准发布。</p>
        <p v-if="formError" role="alert" class="text-sm text-rose-300">{{ formError }}</p>
        <div class="flex justify-end gap-2"><button type="button" class="ghost-button" :disabled="saving" @click="editorOpen=false">取消</button><button class="liquid-button" :disabled="saving || sourcesLoading">{{ saving?'保存中…':'保存账号' }}</button></div>
      </form>
    </el-dialog>
  </section>
</template>
<script setup lang="ts">
import { onMounted, onScopeDispose, reactive, ref, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import { managedReportApi, type ReportAccount } from '../../api/managed-report'
import { sourceCatalogApi, type SourceDefinition, type DefinitionRevision } from '../../api/source-catalog'
import { reportCommandKeys } from './reportCommands'
const accounts=ref<ReportAccount[]>([]),loading=ref(false),busy=ref(false),error=ref(''),message=ref(''),editorOpen=ref(false),saving=ref(false),formError=ref(''),rotating=ref<ReportAccount>()
const form=reactive({name:'',definition_revision_id:undefined as number|undefined,username:'',password:''})
const sources=ref<SourceDefinition[]>([]),revisions=ref<DefinitionRevision[]>([]),familySource=ref<number>(),sourcesLoading=ref(false),revisionPage=ref(1),revisionTotal=ref(0),keys=reportCommandKeys()
let alive=true,sourceGeneration=0,listGeneration=0
const errorText=(e:unknown)=>e instanceof Error?e.message:'操作失败，请刷新状态后核对'
async function load(){const generation=++listGeneration;loading.value=true;error.value='';try{const data=await managedReportApi.accounts();if(alive&&generation===listGeneration)accounts.value=data.items}catch(e){if(alive&&generation===listGeneration){accounts.value=[];error.value=errorText(e)}}finally{if(alive&&generation===listGeneration)loading.value=false}}
async function searchSources(keyword=''){const generation=++sourceGeneration;sourcesLoading.value=true;try{const data=await sourceCatalogApi.list({keyword,page:1,page_size:100});if(alive&&generation===sourceGeneration)sources.value=data.items.filter(s=>s.visibility==='global'&&s.enabled&&['postgres','mysql','jdbc'].includes(s.config.type))}catch(e){if(alive&&generation===sourceGeneration)formError.value=errorText(e)}finally{if(alive&&generation===sourceGeneration)sourcesLoading.value=false}}
async function loadRevisions(page=1){const source=familySource.value;if(!source)return;const generation=++sourceGeneration;revisionPage.value=page;sourcesLoading.value=true;form.definition_revision_id=undefined;try{const data=await sourceCatalogApi.revisions(source,page);if(alive&&generation===sourceGeneration){revisions.value=data.items;revisionTotal.value=data.total;form.definition_revision_id=data.items.find(s=>s.enabled&&s.visibility==='global')?.id}}catch(e){if(alive&&generation===sourceGeneration)formError.value=errorText(e)}finally{if(alive&&generation===sourceGeneration)sourcesLoading.value=false}}
function create(){rotating.value=undefined;form.name='';form.username='';form.password='';form.definition_revision_id=undefined;familySource.value=undefined;revisions.value=[];revisionTotal.value=0;formError.value='';keys.clear();editorOpen.value=true;void searchSources()}
function rotate(account:ReportAccount){rotating.value=account;form.password='';formError.value='';keys.clear();editorOpen.value=true}
async function save(){if(saving.value)return;formError.value='';if(!form.password||(!rotating.value&&(!form.name.trim()||!form.username.trim()||!form.definition_revision_id))){formError.value='请填写完整的账号配置';return}saving.value=true
 try{if(rotating.value){const account=rotating.value,payload={version:account.version,password:form.password};await managedReportApi.updateAccount(account.id,payload,keys.get(`rotate:${account.id}`,payload))}else{const payload={name:form.name.trim(),definition_revision_id:form.definition_revision_id!,username:form.username.trim(),password:form.password};await managedReportApi.createAccount(payload,keys.get('create',payload))}form.password='';editorOpen.value=false;await load()}catch(e){formError.value=errorText(e)}finally{saving.value=false}}
async function setEnabled(account:ReportAccount){if(busy.value)return;try{await ElMessageBox.confirm(account.enabled?`停用后，当前 ${account.report_count} 份已发布报表将无法查询。确认停用？`:`确认启用「${account.name}」？`,'执行账号状态',{confirmButtonText:'确认',cancelButtonText:'取消',type:'warning'})}catch{return}busy.value=true;error.value='';message.value='';const payload={version:account.version,enabled:!account.enabled,...(account.enabled?{expected_reports:account.report_count}:{})};try{await managedReportApi.updateAccount(account.id,payload,keys.get(`status:${account.id}`,payload));await load()}catch(e){error.value=errorText(e)}finally{busy.value=false}}
async function test(account:ReportAccount){if(busy.value)return;busy.value=true;message.value='';error.value='';try{await managedReportApi.testAccount(account.id);if(alive)message.value=`「${account.name}」连接及强制只读查询验证通过`}catch(e){if(alive)error.value=errorText(e)}finally{if(alive)busy.value=false}}
watch(editorOpen,open=>{if(!open){form.password='';keys.clear();sourceGeneration++;sourcesLoading.value=false}},{flush:'sync'})
onMounted(()=>void load());onScopeDispose(()=>{alive=false;listGeneration++;sourceGeneration++;form.password='';keys.clear()})
</script>
