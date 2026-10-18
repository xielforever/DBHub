<template>
  <el-dialog :model-value="true" :title="formal ? (managed ? '编辑正式报表草稿' : '新建正式报表草稿') : (report ? '编辑报表' : '新建报表')" width="min(960px, calc(100vw - 24px))" class="glass-dialog" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" @close="close">
    <form class="space-y-4" @submit.prevent="save">
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <label class="text-xs text-white/60 space-y-1">报表名称<el-input v-model="form.name" aria-label="报表名称" maxlength="128" :disabled="saving" /></label>
        <label v-if="!formal" class="text-xs text-white/60 space-y-1">可见性<el-select v-model="form.visibility" aria-label="报表可见性" class="w-full" :disabled="saving"><el-option label="仅自己" value="private" /><el-option label="登录后共享" value="shared" /></el-select></label>
      </div>
      <label class="block text-xs text-white/60 space-y-1">描述<el-input v-model="form.description" aria-label="报表描述" maxlength="512" :disabled="saving" /></label>
      <p v-if="formal" class="text-xs text-white/55">使用个人连接开发和预览。保存草稿后提交审批，管理员验证报表执行账号并批准，发布后业务用户按报表授权查看。</p>
      <p v-else class="text-xs text-white/55">共享内容包括 SQL 和图表配置。查看者须有数据源权限，并配置对应修订的个人连接；查询始终使用查看者自己的数据库账号。</p>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <label class="text-xs text-white/60 space-y-1">个人连接<el-select v-model="form.connection_id" aria-label="报表个人连接" placeholder="选择支持只读 SQL 的个人连接" class="w-full" :loading="loadingSources" :disabled="saving"><el-option v-for="source in sources" :key="source.id" :label="source.name" :value="source.id" /></el-select></label>
        <label class="text-xs text-white/60 space-y-1">数据库<el-input v-model="form.database" aria-label="报表数据库" placeholder="留空使用连接默认数据库" :disabled="saving" maxlength="128" /></label>
      </div>
      <p v-if="sourceError" class="text-sm text-rose-300" role="alert">{{ sourceError }}</p>
      <p v-else-if="!loadingSources && !sources.length" class="text-sm text-amber-200">暂无可用连接，请先到<RouterLink to="/connections" class="underline">数据源</RouterLink>配置个人连接。</p>
      <label class="block text-xs text-white/60 space-y-1">只读 SQL<el-input v-model="form.sql" aria-label="报表SQL" type="textarea" :rows="7" :disabled="saving" spellcheck="false" placeholder="SELECT category, SUM(amount) AS total FROM inventory GROUP BY category ORDER BY category" /></label>
      <div class="flex flex-wrap items-center gap-3">
        <button type="button" class="ghost-button text-xs" :disabled="!form.connection_id || !form.sql.trim() || preview.state.value.status === 'loading' || saving" @click="runPreview">{{ preview.state.value.status === 'loading' ? '查询中…' : '运行预览' }}</button>
        <button v-if="preview.state.value.status === 'loading'" type="button" class="ghost-button text-xs" @click="preview.reset">取消预览</button>
        <p class="text-xs text-white/45">仅允许单条 SELECT；最多返回 1000 行、执行 30 秒，并服从服务端更严格的限制。</p>
      </div>
      <p v-if="preview.state.value.status === 'error'" role="alert" class="text-sm text-rose-300">{{ preview.state.value.message }}</p>
      <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <label class="text-xs text-white/60 space-y-1">展示方式<el-select v-model="form.chart_type" aria-label="报表展示方式" class="w-full" :disabled="saving"><el-option v-for="option in chartOptions" :key="option.value" :label="option.label" :value="option.value" /></el-select></label>
        <label v-if="['bar','line','pie'].includes(form.chart_type)" class="text-xs text-white/60 space-y-1">维度<el-select v-model="dimension" aria-label="报表维度" class="w-full" :disabled="saving || !columns.length"><el-option v-for="column in uniqueColumns" :key="column" :label="column" :value="column" /></el-select></label>
        <label v-if="form.chart_type !== 'table'" class="text-xs text-white/60 space-y-1">指标<el-select v-model="metrics" aria-label="报表指标" multiple :multiple-limit="['pie','metric'].includes(form.chart_type) ? 1 : 6" class="w-full" :disabled="saving || !columns.length"><el-option v-for="column in uniqueColumns" :key="column" :label="column" :value="column" /></el-select></label>
      </div>
      <p class="text-xs text-white/50">图表按 SQL 返回顺序展示，汇总和排序请在 SQL 中完成。新建图表前先运行预览，再选择维度和指标。</p>
      <ReportResult v-if="preview.state.value.status === 'success'" :type="form.chart_type" :config="chartConfig" :result="preview.state.value.data" :title="form.name || '预览'" />
      <p v-if="saveError" class="text-sm text-rose-300" role="alert">{{ saveError }}</p>
      <div class="flex justify-end gap-2 pt-3 border-t border-white/10">
        <button type="button" class="ghost-button" :disabled="saving" @click="close">取消</button>
        <button type="submit" class="liquid-button" :disabled="saving || loadingSources || !form.connection_id || preview.state.value.status === 'loading'">{{ saving ? '保存中…' : (formal ? '保存草稿' : '保存报表') }}</button>
      </div>
    </form>
  </el-dialog>
</template>
<script setup lang="ts">
import { computed, onMounted, onScopeDispose, reactive, ref, watch } from 'vue'
import { connectionApi, type ConnectionItem } from '../../api/datasource'
import { reportApi, type ReportItem, type ReportPayload } from '../../api/report'
import { managedReportApi, type ManagedReport } from '../../api/managed-report'
import type { ChartConfig, ChartKind } from '../../components/business/chart/ChartCard.vue'
import type { ExecResult } from '../../api/workbench'
import { newRequestId } from '../../utils/request-id'
import ReportResult from './ReportResult.vue'
import { useReportRun } from './useReportRun'
import { reportChart } from './reportChart'
const props = defineProps<{ report?: ReportItem; managed?: ManagedReport; formal?: boolean }>()
const emit = defineEmits<{ close: []; saved: [report: ReportItem]; managedSaved: [report: ManagedReport] }>()
const formal = props.formal || !!props.managed
const managed = props.managed
const report = managed ? { ...managed.draft, connection_id: managed.connection_id, visibility: 'private' as const } : props.report
const form = reactive({ name: report?.name ?? '', description: report?.description ?? '', connection_id: report?.connection_id || undefined as number | undefined, database: report?.database_name ?? '', sql: report?.sql_text ?? '', chart_type: report?.chart_type ?? 'table' as ChartKind, visibility: report?.visibility ?? 'private' as 'private' | 'shared' })
const dimension = ref(report?.chart_config.dimension ?? ''), metrics = ref<string[]>(report?.chart_config.metrics ? [...report.chart_config.metrics] : [])
const chartOptions: {value: ChartKind; label: string}[] = [{value:'table',label:'表格'},{value:'bar',label:'柱状图'},{value:'line',label:'折线图'},{value:'pie',label:'饼图'},{value:'metric',label:'指标卡'}]
const chartConfig = computed<ChartConfig>(() => form.chart_type === 'table' ? {} : { ...(form.chart_type === 'metric' ? {} : { dimension: dimension.value }), metrics: metrics.value })
const sources = ref<ConnectionItem[]>([]), loadingSources = ref(true), sourceError = ref(''), saving = ref(false), saveError = ref('')
const preview = useReportRun<ExecResult>()
const columns = computed(() => preview.state.value.status === 'success' ? preview.state.value.data.columns ?? [] : [])
const uniqueColumns = computed(() => columns.value.filter((name, index, all) => all.indexOf(name) === index && all.lastIndexOf(name) === index))
const creationKey = newRequestId()
let alive = true
onMounted(async () => { try { const response = await connectionApi.list(); if (alive) { sources.value = response.items.filter(source => source.capabilities?.sql && source.capabilities.enforced_read_only && (!formal || source.definition?.visibility === 'global')); if(formal && !sources.value.some(source=>source.id===form.connection_id))form.connection_id=undefined } } catch (error) { if (alive) sourceError.value = error instanceof Error ? error.message : '连接列表加载失败' } finally { if (alive) loadingSources.value = false } })
onScopeDispose(() => { alive = false })
watch(() => [form.connection_id, form.database, form.sql], () => preview.reset(), { flush: 'sync' })
function close() { if (!saving.value) { preview.reset(); emit('close') } }
async function runPreview() {
  if (!form.connection_id) return
  const payload = { connection_id: form.connection_id, database: form.database.trim(), sql: form.sql.trim() }
  await preview.run(JSON.stringify(payload), signal => reportApi.preview(payload, signal))
}
async function save() {
  if (saving.value || !form.connection_id) return
  saveError.value = ''
  if (!form.name.trim() || !form.sql.trim()) { saveError.value = '报表名称和 SQL 必填'; return }
  if (form.chart_type !== 'table') {
    if (preview.state.value.status !== 'success') { saveError.value = '请先运行预览，确认图表字段与查询结果'; return }
    const chart = reportChart(form.chart_type, chartConfig.value, preview.state.value.data)
    if (chart.error) { saveError.value = chart.error; return }
  }
  const payload: ReportPayload = { ...form, name: form.name.trim(), connection_id: form.connection_id, chart_config: chartConfig.value }
  saving.value = true
  try {
    if (formal) {
      const { visibility: _visibility, ...definition } = payload
      const result = await managedReportApi.save(managed?.id ?? 0, { ...definition, version: managed?.draft_version, request_id: creationKey })
      emit('managedSaved', await managedReportApi.get(result.id))
    } else {
      const existing = props.report
      const saved = existing ? await reportApi.update(existing.id, { ...payload, version: existing.version }) : await reportApi.create({ ...payload, request_id: creationKey })
      emit('saved', saved)
    }
  }
  catch (error) { saveError.value = error instanceof Error ? error.message : '保存失败，请稍后重试' }
  finally { saving.value = false }
}
</script>
