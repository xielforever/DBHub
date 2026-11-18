<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import { computed, nextTick, onMounted, onScopeDispose, reactive, ref, useId, watch } from 'vue'
import { ElDialog, ElMessageBox } from 'element-plus'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { connectionApi, type ConnectionItem } from '../../api/datasource'
import { reportApi, type ReportItem, type ReportPayload } from '../../api/report'
import { managedReportApi, type ManagedReport } from '../../api/managed-report'
import type { ChartConfig, ChartKind } from '../../components/business/chart/ChartCard.vue'
import type { ExecResult } from '../../api/workbench'
import { newRequestId } from '../../utils/request-id'
import { featureAllowed } from '../../utils/permission'
import { ApiRequestError } from '../../utils/request'
import { useUserStore } from '../../stores/user'
import ReportResult from './ReportResult.vue'
import ReportContractEditor from './ReportContractEditor.vue'
import ReportDisplayEditor from './ReportDisplayEditor.vue'
import type {DataContract} from '../../api/managed-report'
import { useReportRun } from './useReportRun'
import { reportChart } from './reportChart'
import { reportPivot } from './reportPivot'
import ReportParameterEditor from './ReportParameterEditor.vue'
import ReportParameterForm from './ReportParameterForm.vue'
import ReportAnalysisSettings from './ReportAnalysisSettings.vue'
import { parameterDefaults, type ParameterValues, type ReportParameter } from './reportParameters'
import type { ReportExportPolicy } from '../../api/report-exports'
const props = defineProps<{ report?: ReportItem; managed?: ManagedReport; formal?: boolean; inline?: boolean; focusOnMount?: boolean }>()
const emit = defineEmits<{
  close: []
  saved: [report: ReportItem]
  managedSaved: [id: number]
  managedReloaded: [draft: ManagedReport, restoreFocus: boolean]
}>()
const formal = props.formal || !!props.managed
const managed = props.managed
const report = managed ? { ...managed.draft, connection_id: managed.connection_id, visibility: 'private' as const } : props.report
const form = reactive({ name: report?.name ?? '', description: report?.description ?? '', connection_id: report?.connection_id || undefined as number | undefined, database: report?.database_name ?? '', sql: report?.sql_text ?? '', chart_type: report?.chart_type ?? 'table' as ChartKind, visibility: report?.visibility ?? 'private' as 'private' | 'shared' })
const dimension = ref(report?.chart_config.dimension ?? ''), metrics = ref<string[]>(report?.chart_config.metrics ? [...report.chart_config.metrics] : [])
const parameters = ref<ReportParameter[]>(JSON.parse(JSON.stringify(report?.parameters || [])))
const parameterValues = ref<ParameterValues>(parameterDefaults(parameters.value))
const exportPolicy=ref<ReportExportPolicy|undefined>(managed?.draft.export_policy?JSON.parse(JSON.stringify(managed.draft.export_policy)):undefined)
const chartOptions: {value: ChartKind; label: string}[] = [{value:'table',label:'表格'},{value:'bar',label:'柱状图'},{value:'line',label:'折线图'},{value:'pie',label:'饼图'},{value:'metric',label:'指标卡'},{value:'pivot',label:'透视表'},{value:'stacked_bar',label:'堆叠柱状图'},{value:'dual_axis',label:'双轴图'}]
const pivot=ref<ChartConfig['pivot']>(report?.chart_config.pivot?JSON.parse(JSON.stringify(report.chart_config.pivot)):undefined),dualAxis=ref<ChartConfig['dual_axis']>(report?.chart_config.dual_axis?JSON.parse(JSON.stringify(report.chart_config.dual_axis)):undefined),drillthrough=ref<ChartConfig['drillthrough']>(report?.chart_config.drillthrough?JSON.parse(JSON.stringify(report.chart_config.drillthrough)):undefined)
const display = ref<ChartConfig['display']>(report?.chart_config.display ? JSON.parse(JSON.stringify(report.chart_config.display)) : undefined)
const chartConfig = computed<ChartConfig>(() => ({ ...(form.chart_type === 'table' ? {} : { ...(form.chart_type === 'metric' ? {} : { dimension: dimension.value }), metrics: metrics.value }), ...(form.chart_type==='pivot'?{pivot:pivot.value}:{}),...(form.chart_type==='dual_axis'?{dual_axis:dualAxis.value}:{}),...(drillthrough.value?{drillthrough:drillthrough.value}:{}),...(display.value ? { display: display.value } : {}) }))
const sources = ref<ConnectionItem[]>([]), loadingSources = ref(true), sourceError = ref(''), saving = ref(false), saveError = ref('')
const user = useUserStore()
const draftConflict = ref(false)
const reloadingDraft = ref(false)
const recoveryButton = ref<HTMLButtonElement>()
const recoveryRequest = new AbortController()
const editorBusy = computed(() => saving.value || reloadingDraft.value)
const preview = useReportRun<ExecResult>()
const previewPending = computed(() => preview.state.value.status === 'loading')
const previewButton = ref<HTMLButtonElement>()
const previewControls = ref<HTMLElement>()
const columns = computed(() => preview.state.value.status === 'success' ? preview.state.value.data.columns ?? [] : [])
const uniqueColumns = computed(() => columns.value.filter((name, index, all) => all.indexOf(name) === index && all.lastIndexOf(name) === index))
const dataContract=ref<DataContract|undefined>(managed?.draft.data_contract?JSON.parse(JSON.stringify(managed.draft.data_contract)):undefined)
const previewContractKey=ref(''),previewContractCurrent=computed(()=>previewContractKey.value===JSON.stringify(dataContract.value??null))
const displayColumns = computed(() => uniqueColumns.value.length ? uniqueColumns.value : [...new Set([...(dataContract.value?.result_schema.map(c => c.name) || []), dimension.value, ...metrics.value])].filter(Boolean))
const creationKey = newRequestId()
const editorTitle = formal ? (managed ? '编辑正式报表草稿' : '新建正式报表草稿') : (report ? '编辑报表' : '新建报表')
const heading = ref<HTMLElement>()
const formId = useId()
const activeSection = ref('parameters')
const sections = [
  { id: 'parameters', name: '参数' }, { id: 'display', name: '展示' },
  { id: 'analysis', name: '分析' }, { id: 'contract', name: '契约' },
]
function draftSnapshot() {
  return JSON.stringify([form, parameters.value, chartConfig.value, dataContract.value, exportPolicy.value])
}
const initialDraft = draftSnapshot()
let closePending = false
let alive = true
const sourceAvailable = computed(() => !loadingSources.value && !sourceError.value && sources.value.some(source => source.id === form.connection_id))
const canPreview = computed(() => sourceAvailable.value && !!form.sql.trim() && !editorBusy.value)
const canSave = computed(() => !editorBusy.value && !draftConflict.value && sourceAvailable.value && !previewPending.value)
async function loadSources() {
  loadingSources.value = true
  sourceError.value = ''
  try {
    const response = await connectionApi.list(undefined, { silentError: true })
    if (alive) sources.value = response.items.filter(source => source.capabilities?.sql && source.capabilities.enforced_read_only && (!formal || source.definition?.visibility === 'global'))
  } catch (error) {
    if (alive) sourceError.value = error instanceof Error ? error.message : '连接列表加载失败'
  } finally {
    if (alive) loadingSources.value = false
  }
}
onMounted(async () => {
  if (props.inline && props.focusOnMount !== false) { await nextTick(); heading.value?.focus() }
  void loadSources()
})
onScopeDispose(() => {
  alive = false
  recoveryRequest.abort()
})
watch(() => [form.connection_id, form.database, form.sql], () => preview.reset(), { flush: 'sync' })
watch([parameters, parameterValues], () => preview.reset(), { deep: true, flush: 'sync' })
watch(parameters, () => { parameterValues.value = parameterDefaults(parameters.value) }, { deep: true })
async function mayLeave() {
  if (!props.inline) return true
  if (editorBusy.value || closePending) return false
  if (draftSnapshot() === initialDraft) return true
  closePending = true
  try {
    await ElMessageBox.confirm('本次修改尚未保存。确认返回正式报表管理？', '放弃草稿修改', { confirmButtonText: '放弃修改', cancelButtonText: '继续编辑', type: 'warning' })
    return true
  } catch { return false }
  finally { closePending = false }
}
onBeforeRouteLeave(mayLeave)
onBeforeRouteUpdate(async (to, from) => {
  if (to.query.view !== from.query.view || to.query.workspace_id !== from.query.workspace_id || to.query.report !== from.query.report) {
    if (!await mayLeave()) return false
    if (props.inline) emit('close')
  }
})
async function close() {
  if (saving.value || !await mayLeave()) return
  preview.reset()
  emit('close')
}
async function runPreview() {
  if (!canPreview.value || !form.connection_id) return
  const c=dataContract.value
  const payload = { connection_id: form.connection_id, database: form.database.trim(), sql: form.sql.trim(), parameters: parameters.value, params: parameterValues.value, ...(formal&&c?.result_schema.length&&c.semantic_key&&c.meaning&&c.unit&&c.scope_key&&c.scope_description?{data_contract:c}:{}) }
  previewContractKey.value=JSON.stringify(payload.data_contract??null)
  await preview.run(JSON.stringify(payload), signal => reportApi.preview(payload, signal))
}
function startPreview() {
  if (!previewPending.value) void runPreview()
}
async function cancelPreview(event: MouseEvent) {
  const wasFocused = document.activeElement === event.currentTarget
  preview.reset()
  await nextTick()
  if (!alive || !wasFocused || document.activeElement !== document.body) return
  const target = canPreview.value ? previewButton.value : previewControls.value
  target?.focus()
}
async function save(event: SubmitEvent) {
  if (!canSave.value || !form.connection_id) return
  const trigger = event.submitter
  const focusedWhenSaved = trigger === document.activeElement
  saveError.value = ''
  if (!form.name.trim() || !form.sql.trim()) { saveError.value = '报表名称和 SQL 必填'; return }
  if (form.chart_type !== 'table') {
    if (preview.state.value.status !== 'success') { saveError.value = '请先运行预览，确认图表字段与查询结果'; return }
    const chart = form.chart_type==='pivot' ? reportPivot(chartConfig.value, preview.state.value.data) : reportChart(form.chart_type, chartConfig.value, preview.state.value.data)
    if (chart.error) { saveError.value = chart.error; return }
  }
  const payload: ReportPayload = { ...form, parameters: parameters.value, name: form.name.trim(), connection_id: form.connection_id, chart_config: chartConfig.value }
  saving.value = true
  try {
    if (formal) {
      const { visibility: _visibility, ...definition } = payload
      const contract=dataContract.value?{...dataContract.value,chart_type:form.chart_type,dimensions:form.chart_type==='table'?[]:[...(dimension.value?[dimension.value]:[]),...(form.chart_type==='pivot'&&pivot.value?[pivot.value.column]:[])],metrics:form.chart_type==='table'?[]:[...metrics.value]}:undefined
      const result = await managedReportApi.save(managed?.id ?? 0, { ...definition,export_policy:exportPolicy.value,data_contract:contract, version: managed?.draft_version, request_id: creationKey })
      if (alive) emit('managedSaved', result.id)
    } else {
      const existing = props.report
      const saved = existing ? await reportApi.update(existing.id, { ...payload, version: existing.version }) : await reportApi.create({ ...payload, request_id: creationKey })
      emit('saved', saved)
    }
  }
  catch (error) {
    if (!alive) return
    if (managed && props.inline && error instanceof ApiRequestError && (error.status === 409 || error.status === 403)) {
      draftConflict.value = true
      saveError.value = '草稿或编辑资格已变化，请载入最新草稿核对。'
      await nextTick()
      if (alive && focusedWhenSaved && (document.activeElement === trigger || document.activeElement === document.body)) {
        recoveryButton.value?.focus()
      }
    } else {
      saveError.value = error instanceof Error ? error.message : '保存失败，请稍后重试'
    }
  }
  finally { saving.value = false }
}

async function reloadDraft(event: MouseEvent) {
  if (!managed || editorBusy.value || !draftConflict.value) return
  const trigger = event.currentTarget
  const restoreFocus = trigger === document.activeElement
  const modalClass = 'report-draft-recovery-confirmation'
  const stillInRecovery = () => {
    const active = document.activeElement
    return active === trigger || active === document.body || !!active?.closest(`.${modalClass}`)
  }
  reloadingDraft.value = true
  try {
    if (draftSnapshot() !== initialDraft) {
      const confirmed = await ElMessageBox.confirm('载入最新草稿会放弃本次未保存的修改。', '载入最新草稿？', {
        type: 'warning', confirmButtonText: '载入最新草稿', cancelButtonText: '继续编辑', modalClass,
      }).then(() => true, () => false)
      if (!confirmed || !alive) return
    }
    const latest = await managedReportApi.get(managed.id, recoveryRequest.signal)
    if (!alive) return
    const canDesign = featureAllowed(user, 'report.design', ['admin', 'developer'].includes(user.role))
    if (!canDesign || latest.owner_id !== user.user?.id) {
      saveError.value = '此草稿当前不可编辑。你可以保留本次输入，或返回正式报表管理。'
      return
    }
    emit('managedReloaded', latest, restoreFocus && stillInRecovery())
  } catch (error) {
    if (alive) saveError.value = error instanceof Error ? error.message : '无法载入最新草稿，请重试'
  } finally {
    if (alive) {
      reloadingDraft.value = false
      await nextTick()
      if (alive && restoreFocus && stillInRecovery()) recoveryButton.value?.focus()
    }
  }
}
</script>

<template>
  <component :is="inline ? 'section' : ElDialog" v-bind="inline
    ? { class: 'ui-page-stack min-w-0', 'aria-label': '报表设计' }
    : { modelValue: true, title: editorTitle, width: 'min(960px, calc(100vw - 24px))', class: 'glass-dialog', closeOnClickModal: false, closeOnPressEscape: !saving, showClose: !saving, onClose: close }">
    <div v-if="inline" ref="heading" tabindex="-1">
      <PageHeader :title="editorTitle" :description="managed ? `草稿修订 ${managed.draft_version} · 负责人 ${managed.owner_name}` : '配置数据源、只读查询与展示。保存后可提交审批。'">
        <template #actions>
          <button type="button" class="ghost-button ui-control-target" :disabled="editorBusy" @click="close">返回管理</button>
          <button type="submit" :form="formId" class="liquid-button ui-control-target" :disabled="!canSave">{{ saving ? '保存中…' : '保存草稿' }}</button>
        </template>
      </PageHeader>
    </div>
    <form :id="formId" class="space-y-4 min-w-0" @submit.prevent="save">
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <label class="text-xs text-muted space-y-1">报表名称<el-input v-model="form.name" aria-label="报表名称" maxlength="128" :disabled="editorBusy" /></label>
        <label v-if="!formal" class="text-xs text-muted space-y-1">可见性<el-select v-model="form.visibility" aria-label="报表可见性" class="w-full" :disabled="editorBusy"><el-option label="仅自己" value="private" /><el-option label="登录后共享" value="shared" /></el-select></label>
      </div>
      <label class="block text-xs text-muted space-y-1">描述<el-input v-model="form.description" aria-label="报表描述" maxlength="512" :disabled="editorBusy" /></label>
      <p v-if="formal" class="text-xs text-muted">使用个人连接开发和预览。保存草稿后提交审批，审批人验证报表执行账号并批准，发布后业务用户按报表授权查看。</p>
      <p v-if="managed?.draft.promotion" class="text-xs text-accent">迁移来源：报表 #{{managed.draft.promotion.source_report_id}} v{{managed.draft.promotion.source_version}} · 原获批内容校验值 {{managed.draft.promotion.source_content_hash}}。目标仍需独立审批与发布。</p>
      <p v-if="!formal" class="text-xs text-muted">共享内容包括 SQL 和图表配置。查看者须有数据源权限，并配置对应修订的个人连接；查询始终使用查看者自己的数据库账号。</p>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <label class="text-xs text-muted space-y-1">个人连接<el-select v-model="form.connection_id" aria-label="报表个人连接" placeholder="选择支持只读 SQL 的个人连接" class="w-full" :loading="loadingSources" :disabled="editorBusy || loadingSources || !!sourceError"><el-option v-for="source in sources" :key="source.id" :label="source.name" :value="source.id" /></el-select></label>
        <label class="text-xs text-muted space-y-1">数据库<el-input v-model="form.database" aria-label="报表数据库" placeholder="留空使用连接默认数据库" :disabled="editorBusy" maxlength="128" /></label>
      </div>
      <div v-if="sourceError" class="flex flex-wrap items-center gap-2">
        <p class="text-sm text-danger" role="alert">{{ sourceError }}</p>
        <button type="button" class="ghost-button ui-control-target" @click="loadSources">重试读取连接</button>
      </div>
      <p v-else-if="!loadingSources && !sources.length" class="text-sm text-warning">暂无可用连接，请到<RouterLink :to="{ path: '/connections', query: { workspace_id: $route.query.workspace_id } }" class="underline">我的连接</RouterLink>配置个人连接。</p>
      <p v-else-if="!loadingSources && form.connection_id && !sourceAvailable" class="text-sm text-warning">草稿原连接当前不可用，请选择其他连接。</p>
      <component :is="inline ? PageCategoryTabs : 'div'" v-bind="inline ? { label: '报表设计配置', categories: sections, activeId: activeSection } : {}" @select="activeSection = $event">
      <ReportParameterEditor v-if="!inline || activeSection === 'parameters'" v-model="parameters" :disabled="editorBusy" class="w-full" />
      <div v-if="!inline || activeSection === 'display'" class="space-y-4">
      <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <label class="text-xs text-muted space-y-1">展示方式<el-select v-model="form.chart_type" aria-label="报表展示方式" class="w-full" :disabled="editorBusy"><el-option v-for="option in chartOptions" :key="option.value" :label="option.label" :value="option.value" /></el-select></label>
        <label v-if="!['table','metric'].includes(form.chart_type)" class="text-xs text-muted space-y-1">{{form.chart_type==='pivot'?'行维度':'维度'}}<el-select v-model="dimension" aria-label="报表维度" class="w-full" :disabled="editorBusy || !columns.length"><el-option v-for="column in uniqueColumns" :key="column" :label="column" :value="column" /></el-select></label>
        <label v-if="form.chart_type !== 'table'" class="text-xs text-muted space-y-1">指标<el-select v-model="metrics" aria-label="报表指标" multiple :multiple-limit="['pie','metric'].includes(form.chart_type) ? 1 : 6" class="w-full" :disabled="editorBusy || !columns.length"><el-option v-for="column in uniqueColumns" :key="column" :label="column" :value="column" /></el-select></label>
      </div>
      <p class="text-xs text-muted">图表按 SQL 返回顺序展示，汇总和排序请在 SQL 中完成。新建图表前先运行预览，再选择维度和指标。</p>
      <ReportDisplayEditor v-model="display" :columns="displayColumns" :disabled="editorBusy" :numeric="form.chart_type !== 'table'" />
      </div>
      <ReportAnalysisSettings v-if="!inline || activeSection === 'analysis'" v-model:pivot="pivot" v-model:dual-axis="dualAxis" v-model:drillthrough="drillthrough" :kind="form.chart_type" :columns="uniqueColumns" :metrics="metrics" :disabled="editorBusy" />
      <div v-if="formal && (!inline || activeSection === 'contract')" class="space-y-4">
      <p v-if="preview.state.value.status==='success' && dataContract && !previewContractCurrent" class="text-xs text-warning">当前契约尚未校验或已修改，请完整填写后重新运行预览。</p>
      <section v-if="preview.state.value.status==='success' && preview.state.value.data.contract_check && previewContractCurrent" class="glass-panel p-3 space-y-2 text-xs" aria-label="字段变化与指标校验">
        <p :class="preview.state.value.data.contract_check.passed?'text-success':'text-warning'">{{preview.state.value.data.contract_check.passed?'当前预览通过契约与指标校验':'当前预览未通过契约校验；正式执行将阻断，请先核对字段和口径'}}</p>
        <p>声明字段：{{preview.state.value.data.contract_check.expected_schema.map(c=>`${c.name}（${c.logical_type}）`).join('、')}}</p><p>实际字段：{{preview.state.value.data.contract_check.actual_columns.join('、')}}</p>
        <p v-for="issue in preview.state.value.data.contract_check.issues" :key="issue" class="text-warning">{{issue}}</p><p v-for="check in preview.state.value.data.contract_check.metric_checks" :key="check.id" :class="check.passed?'text-success':'text-danger'">{{check.label}}：{{check.message}}</p>
        <p>若字段或指标规则改变，使用此契约的仪表盘需在发布前确认影响，并采纳新口径重新审批。</p>
      </section>
      <ReportContractEditor v-model="dataContract" :columns="columns" :disabled="editorBusy" />
      <fieldset class="glass-panel p-3 space-y-3" :disabled="editorBusy">
        <legend class="text-sm px-1">受控导出策略</legend>
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" :checked="exportPolicy?.enabled" aria-label="允许受控导出" @change="exportPolicy=($event.target as HTMLInputElement).checked?{enabled:true,max_rows:10000}:undefined" />允许此版本申请完整快照导出</label>
        <template v-if="exportPolicy"><label class="block text-xs">最大导出行数<input v-model.number="exportPolicy.max_rows" type="number" min="1" max="100000" aria-label="最大导出行数" class="glass-input block w-full mt-1" /></label><div class="grid sm:grid-cols-2 gap-3"><label class="block text-xs">页大小参数<select v-model="exportPolicy.page_size_parameter" aria-label="导出页大小参数" class="glass-input block w-full mt-1"><option value="">查询不使用分页参数</option><option v-for="p in parameters.filter(p=>p.type==='integer')" :key="p.name" :value="p.name">{{ p.label }}</option></select></label><label class="block text-xs">起始位置参数<select v-model="exportPolicy.offset_parameter" aria-label="导出起始位置参数" class="glass-input block w-full mt-1"><option value="">不覆盖起始位置</option><option v-for="p in parameters.filter(p=>p.type==='integer')" :key="p.name" :value="p.name">{{ p.label }}</option></select></label></div><p class="text-xs text-muted">导出权限、行数上限和分页设置随版本审批。页大小参数须允许最大行数加1，供系统检测超限。完整结果超过行数或16MiB时拒绝导出；文件下载另需独立审批。</p></template>
      </fieldset>
      </div>
      </component>
      <label class="block text-xs text-muted space-y-1">只读 SQL<el-input v-model="form.sql" aria-label="报表SQL" type="textarea" :rows="7" :disabled="editorBusy" spellcheck="false" placeholder="SELECT category, SUM(amount) AS total FROM inventory GROUP BY category ORDER BY category" /></label>
      <div ref="previewControls" role="group" aria-label="报表预览操作" tabindex="-1" class="flex flex-wrap items-center gap-3 ui-focus-target">
        <ReportParameterForm v-model="parameterValues" :definitions="parameters" :disabled="editorBusy" title="预览条件" @apply="runPreview" @reset="parameterValues = parameterDefaults(parameters)" class="w-full" />
        <button ref="previewButton" type="button" class="ghost-button text-xs ui-control-target"
          :disabled="!canPreview" :aria-disabled="previewPending || undefined" @click="startPreview">{{ previewPending ? '查询中…' : '运行预览' }}</button>
        <button v-if="previewPending" type="button" class="ghost-button text-xs ui-control-target" @click="cancelPreview">取消预览</button>
        <p class="text-xs text-muted">仅允许单条 SELECT。最多返回 1000 行、执行 30 秒，并服从服务端更严格的限制。</p>
      </div>
      <LoadingState v-if="preview.state.value.status === 'loading'" label="正在运行报表预览…" />
      <p v-else-if="preview.state.value.status === 'error'" role="alert" class="text-sm text-danger">{{ preview.state.value.message }}</p>
      <ReportResult v-else-if="preview.state.value.status === 'success'" :type="form.chart_type" :config="chartConfig" :result="preview.state.value.data" :title="form.name || '预览'" :unit="dataContract?.unit" :currency="dataContract?.currency" />
      <div v-if="draftConflict" class="flex flex-wrap items-center gap-2">
        <p class="text-sm text-danger" role="alert">{{ saveError }}</p>
        <button ref="recoveryButton" type="button" class="ghost-button ui-control-target"
          :aria-disabled="reloadingDraft || undefined" @click="reloadDraft">载入最新草稿</button>
        <p v-if="reloadingDraft" class="text-sm text-muted" role="status">正在核对最新草稿…</p>
      </div>
      <p v-else-if="saveError" class="text-sm text-danger" role="alert">{{ saveError }}</p>
      <div v-if="!inline" class="flex justify-end gap-2 pt-3 border-t border-stroke">
        <button type="button" class="ghost-button ui-control-target" :disabled="editorBusy" @click="close">取消</button>
        <button type="submit" class="liquid-button ui-control-target" :disabled="!canSave">{{ saving ? '保存中…' : (formal ? '保存草稿' : '保存报表') }}</button>
      </div>
    </form>
  </component>
</template>
