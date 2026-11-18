<script setup lang="ts">
import { onScopeDispose, ref } from 'vue'
import type { PublishedReport } from '../../api/managed-report'
import { reportExportApi, type ReportExport } from '../../api/report-exports'
import type { ParameterValues } from './reportParameters'
import { reportCommandKeys } from './reportCommands'
const props=defineProps<{report:PublishedReport;params:ParameterValues}>(),emit=defineEmits<{close:[]}>(),keys=reportCommandKeys()
const format=ref<'xlsx'|'csv'|'json'>('xlsx'),reason=ref(''),busy=ref(false),error=ref(''),created=ref<ReportExport>()
const params:ParameterValues=JSON.parse(JSON.stringify(props.params));let alive=true
onScopeDispose(()=>{alive=false})
async function submit(){if(busy.value||!reason.value)return;busy.value=true;error.value='';const payload={publication_no:props.report.publication_no,format:format.value,reason:reason.value,params};try{const out=await reportExportApi.create(props.report.id,{...payload,request_id:keys.get('export',payload)});if(alive)created.value=out}catch(e){if(alive)error.value=e instanceof Error?e.message:'导出申请失败，请核对已有导出记录'}finally{if(alive)busy.value=false}}
</script>

<template>
  <el-dialog :model-value="true" title="申请完整导出" width="min(620px,calc(100vw - 24px))" :close-on-click-modal="false" :show-close="!busy" :close-on-press-escape="!busy" @close="emit('close')">
    <form v-if="!created" class="space-y-4" @submit.prevent="submit">
      <p class="text-sm">{{ report.name }} · 已批准版本 v{{ report.version }}</p>
      <p class="text-xs text-muted">保存当前筛选下的完整查询快照，最多 {{ report.export_policy?.max_rows.toLocaleString() }} 行。经独立审批人批准后下载；快照有效期为7天。</p>
      <p v-if="report.export_policy?.page_size_parameter" class="text-xs text-muted">导出从首条记录开始，使用批准的分页上限；业务筛选条件保持一致。</p>
      <p v-else class="text-xs text-muted">报表查询自身设置的行数限制仍然适用。</p>
      <label class="block text-xs">文件格式<select v-model="format" aria-label="导出文件格式" class="glass-input block w-full mt-1" :disabled="busy"><option value="xlsx">Excel 工作簿</option><option value="csv">CSV</option><option value="json">JSON（保留原始类型）</option></select></label>
      <label class="block text-xs">导出用途<textarea v-model.trim="reason" aria-label="导出用途" class="glass-input block w-full mt-1" rows="3" maxlength="500" required :disabled="busy" placeholder="例如：本月财务对账、部门经营复盘" /></label>
      <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
      <div class="flex flex-wrap justify-between gap-2"><RouterLink to="/reports?view=exports" class="text-xs text-accent self-center">查看已有导出记录</RouterLink><button class="liquid-button ui-control-target" :disabled="busy || !reason">{{ busy ? '正在生成快照…' : '生成快照并申请审批' }}</button></div>
    </form>
    <div v-else class="space-y-4" role="status"><p class="text-success">完整快照已生成，共 {{ created.row_count.toLocaleString() }} 行，等待独立审批。</p><p class="text-xs text-muted">后续下载使用此快照，不会重新查询业务库。</p><RouterLink :to="`/reports?view=exports&export=${created.id}`" class="liquid-button inline-block" @click="emit('close')">查看审批进度</RouterLink></div>
  </el-dialog>
</template>
