<template>
  <section class="space-y-4" aria-label="正式报表开发管理">
    <div class="flex flex-wrap justify-between items-center gap-3"><p class="text-sm text-white/55">草稿使用个人账号开发，提交后冻结版本，审批通过后正式发布。</p><button class="liquid-button text-xs" :disabled="busy" @click="create">新建正式报表</button></div>
    <form class="flex flex-wrap gap-2" @submit.prevent="load(1)"><el-select v-if="isAdmin" v-model="scope" aria-label="正式报表管理范围" class="!w-36" :disabled="busy" @change="load(1)"><el-option label="全部开发报表" value="all" /><el-option label="我的开发报表" value="mine" /><el-option label="待审批" value="pending" /></el-select><input v-model.trim="keyword" aria-label="搜索开发报表" class="glass-input min-w-0 flex-1 sm:max-w-sm" placeholder="搜索名称或描述" /><button class="ghost-button text-xs" :disabled="busy">搜索</button><button type="button" class="ghost-button text-xs" :disabled="busy" @click="load(page)">刷新</button></form>
    <p v-if="error" role="alert" class="text-sm text-rose-300">{{ error }}</p>
    <p v-if="list.state.value.status==='error'" role="alert" class="text-sm text-rose-300">{{ list.state.value.message }}</p>
    <p v-else-if="list.state.value.status==='loading'" role="status" class="text-sm text-white/50">正在加载开发报表…</p>
    <template v-else-if="list.state.value.status==='success'">
      <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        <article v-for="report in list.state.value.data.items" :key="report.id" :aria-label="`开发报表：${report.draft.name}`" class="glass-card p-4 space-y-3 min-w-0">
          <div class="flex justify-between gap-2"><h2 class="font-medium break-words">{{ report.draft.name }}</h2><span class="text-xs shrink-0" :class="report.active_release_id?'text-emerald-200':'text-white/40'">{{ report.active_release_id?'已发布':'未发布' }}</span></div>
          <p class="text-xs text-white/50 break-words">{{ report.draft.description || '无描述' }}</p>
          <p class="text-xs text-white/55">责任人 {{ report.owner_name }} · {{ report.latest_version }} 个提交版本<span v-if="report.pending_count" class="text-amber-200"> · {{ report.pending_count }} 个待审</span></p>
          <div class="flex flex-wrap gap-2"><template v-if="report.owner_id===user.user?.id"><button class="ghost-button text-xs" :disabled="busy" @click="edit(report)">编辑草稿</button><button class="ghost-button text-xs" :disabled="busy" @click="submit(report)">提交审批</button></template><button class="ghost-button text-xs" :disabled="busy" @click="focusedId=report.id">版本与发布</button></div>
        </article>
      </div>
      <p v-if="!list.state.value.data.total" class="glass-card p-8 text-sm text-white/50 text-center">暂无符合条件的开发报表</p>
      <div class="flex items-center justify-between text-xs text-white/50"><span>共 {{ list.state.value.data.total }} 个报表</span><div class="flex gap-2 items-center"><button class="ghost-button" :disabled="page<=1 || busy" @click="load(page-1)">上一页</button><span>{{ page }}</span><button class="ghost-button" :disabled="page*12>=list.state.value.data.total || busy" @click="load(page+1)">下一页</button></div></div>
    </template>
    <ReportEditor v-if="editorOpen" formal :managed="editing" @close="editorOpen=false" @managed-saved="saved" />
    <ReportReviewDialog v-if="focusedId" :report-id="focusedId" @close="focusedId=undefined" @changed="load(page)" />
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import { managedReportApi, type ManagedReport, type ReportPage } from '../../api/managed-report'
import { useUserStore } from '../../stores/user'
import { useReportRun } from './useReportRun'
import { reportCommandKeys } from './reportCommands'
import ReportEditor from './ReportEditor.vue'
import ReportReviewDialog from './ReportReviewDialog.vue'
const user=useUserStore(),isAdmin=computed(()=>user.role==='admin'),scope=ref<'all'|'mine'|'pending'>(isAdmin.value?'all':'mine'),keyword=ref(''),page=ref(1),busy=ref(false),error=ref(''),editorOpen=ref(false),editing=ref<ManagedReport>(),focusedId=ref<number>()
const list=useReportRun<ReportPage<ManagedReport>>(),keys=reportCommandKeys();let alive=true
const errorText=(e:unknown)=>e instanceof Error?e.message:'操作失败，请刷新状态后核对'
async function load(next=1){page.value=next;await list.run(`${scope.value}:${keyword.value}:${next}`,signal=>managedReportApi.list({scope:scope.value,q:keyword.value,page:next,page_size:12},signal))}
function create(){editing.value=undefined;editorOpen.value=true}
async function edit(report:ManagedReport){if(busy.value)return;busy.value=true;error.value='';try{const data=await managedReportApi.get(report.id);if(alive){editing.value=data;editorOpen.value=true}}catch(e){if(alive)error.value=errorText(e)}finally{if(alive)busy.value=false}}
function saved(){editorOpen.value=false;void load(page.value)}
async function submit(report:ManagedReport){if(busy.value)return;try{await ElMessageBox.confirm(`提交「${report.draft.name}」的当前草稿？提交内容将冻结为新版本，已发布版本继续服务。`,'提交审批',{confirmButtonText:'提交审批',cancelButtonText:'取消'})}catch{return}busy.value=true;error.value='';try{await managedReportApi.submit(report.id,report.draft_version,keys.get(`submit:${report.id}`,{version:report.draft_version}));keys.clear();if(alive){await load(page.value);focusedId.value=report.id}}catch(e){if(alive)error.value=errorText(e)}finally{if(alive)busy.value=false}}
onMounted(()=>void load());onScopeDispose(()=>{alive=false;keys.clear()})
</script>
