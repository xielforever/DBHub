<template>
  <div class="space-y-5">
    <header class="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
      <div><h1 class="text-xl font-semibold">报表</h1><p class="text-sm text-white/50 mt-1">{{ view === 'personal' ? '保存只读查询与图表，使用自己的数据库账号查看最新数据' : '开发、审批与发布正式报表，按报表授权查看数据' }}</p></div>
      <button v-if="canWrite && view==='personal'" class="liquid-button text-sm" @click="create">新建报表</button>
    </header>
    <div role="tablist" aria-label="报表功能" class="flex flex-wrap gap-2 border-b border-white/10 pb-3">
      <button role="tab" class="ghost-button text-sm aria-selected:bg-white/10 aria-selected:text-indigo-200" :aria-selected="view==='personal'" @click="view='personal'">个人报表</button>
      <button role="tab" class="ghost-button text-sm aria-selected:bg-white/10 aria-selected:text-indigo-200" :aria-selected="view==='published'" @click="view='published'">已发布</button>
      <button v-if="canWrite" role="tab" class="ghost-button text-sm aria-selected:bg-white/10 aria-selected:text-indigo-200" :aria-selected="view==='managed'" @click="view='managed'">{{ user.role==='admin'?'开发与审批':'我的开发' }}</button>
      <button v-if="user.role==='admin'" role="tab" class="ghost-button text-sm aria-selected:bg-white/10 aria-selected:text-indigo-200" :aria-selected="view==='accounts'" @click="view='accounts'">执行账号</button>
    </div>
    <template v-if="view==='personal'">
    <form class="flex flex-wrap items-center gap-2" @submit.prevent="reload(1)">
      <el-select v-model="scope" aria-label="报表范围" class="!w-32" @change="reload(1)"><el-option label="全部" value="all" /><el-option label="我的" value="mine" /><el-option label="登录后共享" value="shared" /><el-option label="收藏" value="starred" /></el-select>
      <input v-model.trim="keyword" aria-label="搜索报表" class="glass-input text-sm flex-1 min-w-0 sm:max-w-sm" placeholder="搜索名称、描述或 SQL" />
      <button type="submit" class="ghost-button text-xs">搜索</button>
      <button type="button" class="ghost-button text-xs" :disabled="loading" @click="reload()">刷新</button>
    </form>
    <p v-if="listError" class="text-sm text-rose-300" role="alert">{{ listError }}</p>
    <div v-loading="loading" aria-label="报表列表" class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4 min-h-24">
      <article v-for="rep in items" :key="rep.id" class="glass-card p-4 space-y-3 min-w-0" :aria-label="`报表：${rep.name}`">
        <div class="flex items-start justify-between gap-2"><h2 class="font-medium break-words min-w-0">{{ rep.name }}</h2><span class="text-[11px] shrink-0 text-indigo-200">{{ rep.visibility === 'shared' ? '登录后共享' : '仅自己' }}</span></div>
        <p class="text-xs text-white/50 break-words">{{ rep.description || '无描述' }}</p>
        <p class="text-xs text-white/60">{{ chartLabels[rep.chart_type] }} · {{ rep.source_name }} · 修订 {{ rep.source_revision }}</p>
        <p v-if="!rep.connection_id" class="text-xs text-amber-200">查看前需配置此修订的个人连接</p>
        <div class="flex items-center justify-between gap-2 text-[11px] text-white/40"><span>{{ rep.owner_name }}</span><time :datetime="rep.updated_at">{{ formatTime(rep.updated_at) }}</time></div>
        <div class="flex flex-wrap items-center gap-2 border-t border-white/10 pt-3">
          <button class="ghost-button text-xs flex-1" @click="openReport(rep)">查看</button>
          <button class="ghost-button text-xs" :disabled="pending.has(rep.id)" :aria-pressed="rep.starred" @click="toggleStar(rep)">{{ rep.starred ? '取消收藏' : '收藏' }}</button>
          <template v-if="canManage(rep)"><button class="ghost-button text-xs" @click="edit(rep)">编辑</button><button class="ghost-button text-xs" @click="edit(rep,true)">创建正式草稿</button><button class="ghost-button text-xs text-rose-300" :disabled="pending.has(rep.id)" @click="remove(rep)">删除</button></template>
        </div>
      </article>
      <div v-if="!loading && !listError && !items.length" class="col-span-full glass-card p-10 text-center space-y-2"><p class="text-sm text-white/60">暂无报表</p><p class="text-xs text-white/40">{{ canWrite ? '点击“新建报表”，选择个人连接并保存只读 SQL。' : '共享报表将在获得对应数据源权限后显示。' }}</p></div>
    </div>
    <div class="flex items-center justify-between text-xs text-white/50"><span>共 {{ total }} 个报表</span><div class="flex items-center gap-2"><button class="ghost-button px-2 py-1" :disabled="page <= 1 || loading" @click="reload(page-1)">上一页</button><span>{{ page }} / {{ totalPages }}</span><button class="ghost-button px-2 py-1" :disabled="page >= totalPages || loading" @click="reload(page+1)">下一页</button></div></div>
    <ReportEditor v-if="editorOpen" :report="editing" :formal="formalEditor" @close="editorOpen=false" @saved="saved" @managed-saved="managedSaved" />
    <el-dialog v-model="detailOpen" :title="current?.name || '报表详情'" width="min(960px, calc(100vw - 24px))" class="glass-dialog">
      <div v-if="current" class="space-y-4 min-w-0">
        <p class="text-xs text-white/55">{{ current.source_name }} · 修订 {{ current.source_revision }} · {{ current.database_name || '连接默认数据库' }} · 使用本人数据库账号执行</p>
        <pre class="text-xs font-mono bg-black/25 rounded-lg p-3 max-h-48 overflow-auto whitespace-pre-wrap break-words">{{ current.sql_text }}</pre>
        <div class="flex flex-wrap items-center gap-3"><button class="ghost-button text-xs" :disabled="detail.state.value.status === 'loading'" @click="runDetail">{{ detail.state.value.status === 'loading' ? '查询中…' : '刷新数据' }}</button><button v-if="detail.state.value.status === 'loading'" class="ghost-button text-xs" @click="detail.reset">取消查询</button><RouterLink v-if="!current.connection_id" to="/connections" class="text-xs text-indigo-200 underline">配置个人连接（修订 {{ current.source_revision }}）</RouterLink></div>
        <p v-if="detail.state.value.status === 'loading'" role="status" class="py-8 text-center text-sm text-white/55">正在执行只读查询…</p>
        <p v-else-if="detail.state.value.status === 'error'" role="alert" class="text-sm text-rose-300">{{ detail.state.value.message }}</p>
        <ReportResult v-else-if="detail.state.value.status === 'success'" :key="detail.state.value.data.report.id" :type="detail.state.value.data.report.chart_type" :config="detail.state.value.data.report.chart_config" :result="detail.state.value.data.result" :title="detail.state.value.data.report.name" />
        <p v-else class="text-sm text-white/50">查询已停止，可刷新数据重新执行只读查询。</p>
      </div>
    </el-dialog>
    </template>
    <PublishedReportsPanel v-else-if="view==='published'" :focus-report-id="Number(route.query.report)||undefined" />
    <ManagedReportsPanel v-else-if="view==='managed' && canWrite" :focus-report-id="Number(route.query.report)||undefined" />
    <ReportAccountsPanel v-else-if="view==='accounts' && user.role==='admin'" />
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, onScopeDispose, reactive, ref, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import {useRoute} from 'vue-router'
import { reportApi, type ReportItem } from '../../api/report'
import type { ExecResult } from '../../api/workbench'
import { useUserStore } from '../../stores/user'
import ReportEditor from './ReportEditor.vue'
import PublishedReportsPanel from './PublishedReportsPanel.vue'
import ManagedReportsPanel from './ManagedReportsPanel.vue'
import ReportAccountsPanel from './ReportAccountsPanel.vue'
import ReportResult from './ReportResult.vue'
import { useReportRun } from './useReportRun'
const route=useRoute()
const view = ref<'personal'|'published'|'managed'|'accounts'>(['published','managed','accounts'].includes(String(route.query.view))?route.query.view as 'published'|'managed'|'accounts':'personal')
const user = useUserStore(), canWrite = computed(() => ['admin','developer'].includes(user.role))
const chartLabels = { table:'表格',bar:'柱状图',line:'折线图',pie:'饼图',metric:'指标卡' }
const items = ref<ReportItem[]>([]), total = ref(0), page = ref(1), loading = ref(false), listError = ref(''), keyword = ref(''), scope = ref<'all'|'mine'|'shared'|'starred'>('all')
const totalPages = computed(() => Math.max(1,Math.ceil(total.value/12)))
const pending = reactive(new Set<number>())
let listGeneration = 0, listAbort: AbortController | undefined
async function reload(p = page.value) {
  page.value = p
  const run = ++listGeneration
  listAbort?.abort()
  const controller = new AbortController(); listAbort = controller
  loading.value = true; listError.value = ''
  try { const response = await reportApi.list({ scope:scope.value,q:keyword.value || undefined,page:p,page_size:12 },controller.signal); if (run !== listGeneration) return; items.value=response.items;total.value=response.total; if (p>1 && !response.items.length && response.total<=12*(p-1)) await reload(Math.max(1,Math.ceil(response.total/12))) }
  catch (error) { if (run===listGeneration) { items.value=[];total.value=0;listError.value=error instanceof Error?error.message:'报表加载失败' } }
  finally { if (run===listGeneration) loading.value=false }
}
const canManage = (rep: ReportItem) => canWrite.value && rep.owner_user_id===user.user?.id
const formatTime = (value: string) => new Date(value).toLocaleString()
const editorOpen = ref(false), formalEditor=ref(false), editing = ref<ReportItem>()
function create() { formalEditor.value=false;editing.value=undefined;editorOpen.value=true }
let editGeneration = 0
async function edit(rep: ReportItem, formal=false) { const generation=++editGeneration;try { const data=await reportApi.get(rep.id);if(generation===editGeneration && view.value==='personal'){formalEditor.value=formal;editing.value=data;editorOpen.value=true} } catch { /* API shows the failure. */ } }
function saved() { editorOpen.value=false;void reload() }
function managedSaved(){editorOpen.value=false;view.value='managed'}
async function toggleStar(rep: ReportItem) {
  if (pending.has(rep.id)) return
  pending.add(rep.id)
  try { const result=await reportApi.star(rep.id,!rep.starred);rep.starred=result.starred;if(scope.value==='starred'&&!result.starred)void reload() } catch { /* API shows the failure. */ } finally { pending.delete(rep.id) }
}
async function remove(rep: ReportItem) {
  if (pending.has(rep.id)) return
  try { await ElMessageBox.confirm(`确认删除报表「${rep.name}」？`, '删除报表', { type:'warning',confirmButtonText:'删除',cancelButtonText:'取消' }) } catch { return }
  pending.add(rep.id)
  try { await reportApi.remove(rep.id);await reload() } catch { /* API shows the failure. */ } finally { pending.delete(rep.id) }
}
const detailOpen=ref(false), current=ref<ReportItem>()
const detail=useReportRun<{ report:ReportItem;result:ExecResult }>()
function openReport(rep: ReportItem) { current.value=rep;detailOpen.value=true;void runDetail() }
async function runDetail() {
  const id=current.value?.id
  if (!id) return
  const success=await detail.run(String(id),async signal=>{const report=await reportApi.get(id,signal);const result=await reportApi.execute(id,report.version,signal);return {report,result}})
  if (success&&detail.state.value.status==='success') current.value=detail.state.value.data.report
}
watch(detailOpen,open=>{if(!open)detail.reset()},{flush:'sync'})
watch(view,value=>{editGeneration++;detailOpen.value=false;editorOpen.value=false;if(value==='personal')void reload();else{listGeneration++;listAbort?.abort();loading.value=false}})
onMounted(()=>void reload(1))
onScopeDispose(()=>{editGeneration++;listGeneration++;listAbort?.abort()})
</script>
