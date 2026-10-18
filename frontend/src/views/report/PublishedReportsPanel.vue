<template>
  <section class="space-y-4" aria-label="已发布报表">
    <p class="text-sm text-white/55">查看经过审批的正式版本，无需配置个人数据库账号。</p>
    <form class="flex gap-2" @submit.prevent="load(1)"><input v-model.trim="keyword" aria-label="搜索已发布报表" class="glass-input min-w-0 flex-1 sm:max-w-sm" placeholder="搜索报表名称或描述" /><button class="ghost-button text-xs">搜索</button><button type="button" class="ghost-button text-xs" :disabled="list.state.value.status==='loading'" @click="load(page)">刷新</button></form>
    <p v-if="list.state.value.status==='error'" role="alert" class="text-rose-300 text-sm">{{ list.state.value.message }}</p>
    <p v-else-if="list.state.value.status==='loading'" role="status" class="text-white/50 text-sm">正在加载已发布报表…</p>
    <template v-else-if="list.state.value.status==='success'">
      <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        <article v-for="report in list.state.value.data.items" :key="report.id" :aria-label="`正式报表：${report.name}`" class="glass-card p-4 space-y-3 min-w-0">
          <div class="flex justify-between gap-2"><h2 class="font-medium break-words">{{ report.name }}</h2><span class="text-xs text-emerald-200 shrink-0">v{{ report.version }}</span></div>
          <p class="text-xs text-white/50 break-words">{{ report.description || '无描述' }}</p>
          <button class="ghost-button text-xs w-full" @click="open(report)">查看报表</button>
        </article>
      </div>
      <p v-if="!list.state.value.data.total" class="glass-card p-10 text-center text-sm text-white/50">暂无可访问的已发布报表</p>
      <div class="flex items-center justify-between gap-2 text-xs text-white/50"><span>共 {{ list.state.value.data.total }} 个报表</span><div class="flex items-center gap-2"><button class="ghost-button" :disabled="page<=1" @click="load(page-1)">上一页</button><span>{{ page }}</span><button class="ghost-button" :disabled="page*12>=list.state.value.data.total" @click="load(page+1)">下一页</button></div></div>
    </template>
    <el-dialog v-model="detailOpen" :title="current?.name || '正式报表'" width="min(960px, calc(100vw - 24px))" class="glass-dialog">
      <div class="space-y-4">
        <p class="text-xs text-white/55">已批准版本 v{{ current?.version }} · 使用受管报表执行账号查询</p>
        <div class="flex gap-2"><button class="ghost-button text-xs" :disabled="detail.state.value.status==='loading'" @click="run">刷新数据</button><button v-if="detail.state.value.status==='loading'" class="ghost-button text-xs" @click="detail.reset">取消查询</button></div>
        <p v-if="detail.state.value.status==='loading'" role="status" class="text-sm text-white/50">正在查询…</p>
        <p v-else-if="detail.state.value.status==='error'" role="alert" class="text-sm text-rose-300">{{ detail.state.value.message }}</p>
        <ReportResult v-else-if="detail.state.value.status==='success'" :type="detail.state.value.data.report.chart_type" :config="detail.state.value.data.report.chart_config" :result="detail.state.value.data.result" :title="detail.state.value.data.report.name" />
        <p v-else class="text-xs text-white/50">查询已停止，可刷新数据重新查询。</p>
      </div>
    </el-dialog>
  </section>
</template>
<script setup lang="ts">
import { onMounted, onScopeDispose, ref, watch } from 'vue'
import { managedReportApi, type PublishedReport, type ReportPage } from '../../api/managed-report'
import type { ExecResult } from '../../api/workbench'
import { useReportRun } from './useReportRun'
import ReportResult from './ReportResult.vue'
const list=useReportRun<ReportPage<PublishedReport>>(),detail=useReportRun<{report:PublishedReport;result:ExecResult}>()
const props=defineProps<{focusReportId?:number}>();let alive=true
const keyword=ref(''),page=ref(1),detailOpen=ref(false),current=ref<PublishedReport>()
async function load(next=1){page.value=next;await list.run(`${next}:${keyword.value}`,signal=>managedReportApi.published({q:keyword.value,page:next,page_size:12},signal))}
function open(report:PublishedReport){current.value=report;detailOpen.value=true;void run()}
async function run(){const id=current.value?.id;if(!id)return;const success=await detail.run(String(id),async signal=>{const report=await managedReportApi.publishedGet(id,signal);const result=await managedReportApi.execute(id,report.publication_no,signal);return {report,result}});if(success&&detail.state.value.status==='success')current.value=detail.state.value.data.report}
watch(detailOpen,open=>{if(!open)detail.reset()},{flush:'sync'})
onMounted(async()=>{await load();if(props.focusReportId&&alive){detailOpen.value=true;await detail.run(String(props.focusReportId),async signal=>{const report=await managedReportApi.publishedGet(props.focusReportId!,signal);const result=await managedReportApi.execute(report.id,report.publication_no,signal);return {report,result}});if(alive&&detail.state.value.status==='success')current.value=detail.state.value.data.report}})
onScopeDispose(()=>{alive=false})
</script>
