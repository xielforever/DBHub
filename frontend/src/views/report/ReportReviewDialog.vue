<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import {featureAllowed} from '../../utils/permission'
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { ElDialog, ElMessageBox } from 'element-plus'
import ReportAudienceDialog from './ReportAudienceDialog.vue'
import ReportPromotionDialog from './ReportPromotionDialog.vue'
import ReportDashboardDependencies from './ReportDashboardDependencies.vue'
import { managedReportApi, type ManagedReport, type ReportVersion, type ReportRelease, type ReportAccount, type ReportPage, type ReportValidation } from '../../api/managed-report'
import { adminApi, type UserItem, type UserGroup } from '../../api/admin'
import { useUserStore } from '../../stores/user'
import { ApiRequestError } from '../../utils/request'
import { useReportRun } from './useReportRun'
import { reportCommandKeys, reportDefinitionDiff } from './reportCommands'
import ReportResult from './ReportResult.vue'
import ReportParameterForm from './ReportParameterForm.vue'
import { parameterDefaults, type ParameterValues } from './reportParameters'
const props=defineProps<{reportId:number;inline?:boolean;reviewId?:number}>(),emit=defineEmits<{close:[];changed:[];promoted:[id:number]}>(),user=useUserStore(),isAdmin=computed(()=>featureAllowed(user,'report.review',user.hasCapability('report_review')))
const promotionOpen=ref(false)
const dependenciesOpen=ref(false),audienceOpen=ref(false)
const contractConfirmed=ref(false)
const parameterValues = ref<ParameterValues>({})
const detail=useReportRun<{report:ManagedReport;versions:ReportPage<ReportVersion>;releases:ReportPage<ReportRelease>}>(),validation=useReportRun<ReportValidation>(),keys=reportCommandKeys()
const data=computed(()=>detail.state.value.status==='success'?detail.state.value.data:undefined),busy=ref(false),error=ref(''),versionPage=ref(1),releasePage=ref(1),selectedReview=ref<number>(),accountId=ref<number>(),userIds=ref<number[]>([]),groupIds=ref<number[]>([]),comment=ref(''),newOwner=ref<number>()
const accounts=ref<ReportAccount[]>([]),users=ref<UserItem[]>([]),groups=ref<UserGroup[]>([])
const accountsLoading = ref(false), accountsError = ref('')
const actionMessage = ref('')
const actionReceipt = ref<HTMLElement>()
const operationError = ref<HTMLElement>()
const detailRetry = ref<HTMLButtonElement>()
const retryingDetail = ref(false)
const refreshStatus = ref<HTMLButtonElement>()
const validateButton = ref<HTMLButtonElement>()
const validationReceipt = ref<HTMLElement>()
const dialogOpen = ref(true)
const dialogTitle = ref<HTMLElement>()
const selected=computed(()=>data.value?.versions.items.find(v=>v.review_id===selectedReview.value)),previous=computed(()=>{const versions=data.value?.versions.items??[],index=versions.findIndex(v=>v.review_id===selectedReview.value);return index>=0?versions[index+1]:undefined})
const differences=computed(()=>selected.value&&previous.value?reportDefinitionDiff(previous.value.definition,selected.value.definition):[]),eligibleAccounts=computed(()=>accounts.value.filter(a=>a.enabled&&a.definition_revision_id===selected.value?.definition.definition_revision_id)),writers=computed(()=>users.value.filter(u=>u.is_active&&['admin','developer'].includes(u.role)))
const canReview=computed(()=>isAdmin.value&&selected.value?.status==='pending'&&selected.value.submitted_by!==user.user?.id),activeReview=computed(()=>data.value?.report.active_review_id),wasPublished=computed(()=>selected.value?.was_published)
const statusLabels={pending:'待审批',approved:'已批准',rejected:'已退回',withdrawn:'已撤回'},releaseLabels={publish:'发布',rollback:'回滚',unpublish:'下线'},formatTime=(v:string)=>new Date(v).toLocaleString(),errorText=(e:unknown)=>e instanceof Error?e.message:'操作失败，请刷新状态后核对'
interface ApprovalRecovery {
  reviewId: number
  account: number | undefined
  users: number[]
  groups: number[]
  params: ParameterValues
  comment: string
}
let approvalRecovery: ApprovalRecovery | undefined
let alive=true,userGeneration=0,groupGeneration=0
async function loadAccounts() {
  if (!isAdmin.value || accountsLoading.value) return
  accountsLoading.value = true
  accountsError.value = ''
  accounts.value = []
  validation.reset()
  try {
    const result = await managedReportApi.accounts()
    if (alive && isAdmin.value) accounts.value = result.items
  } catch (e) {
    if (alive) accountsError.value = errorText(e)
  } finally {
    if (alive) accountsLoading.value = false
  }
}
async function load() {
  validation.reset()
  const { reportId, reviewId } = props
  const loaded = await detail.run(`${reportId}:${reviewId ?? ''}:${versionPage.value}:${releasePage.value}`, async signal => {
    const [report, versions, releases] = await Promise.all([
      managedReportApi.get(reportId, signal), managedReportApi.versions(reportId, versionPage.value, signal, reviewId),
      managedReportApi.releases(reportId, releasePage.value, signal),
    ])
    if (reviewId && !versions.items.some(version => version.review_id === reviewId)) {
      throw new Error('未读取到此申请关联的审批版本，请重试。')
    }
    return { report, versions, releases }
  })
  if (!loaded || !data.value) return
  if (reviewId) {
    selectedReview.value = reviewId
  } else if (!data.value.versions.items.some(v => v.review_id === selectedReview.value)) {
    selectedReview.value = data.value.versions.items[0]?.review_id
  }
}
function refresh() { void load(); void loadAccounts() }
async function retryDetail() {
  if (busy.value || retryingDetail.value) return
  const trigger = detailRetry.value
  const restoreFocus = trigger && document.activeElement === trigger
  retryingDetail.value = true
  try {
    if (approvalRecovery) await reloadApproval(approvalRecovery)
    else await load()
  } finally {
    retryingDetail.value = false
  }
  await nextTick()
  const active = document.activeElement
  if (!alive || !restoreFocus || (active !== trigger && active !== document.body)) return
  const target = detail.state.value.status === 'error' ? detailRetry.value : operationError.value ?? actionReceipt.value ?? refreshStatus.value
  target?.focus()
}
async function searchUsers(keyword=''){const run=++userGeneration;try{const result=await adminApi.users({keyword,page:1,page_size:100});if(alive&&run===userGeneration)users.value=result.items}catch(e){if(alive&&run===userGeneration)error.value=errorText(e)}}
async function searchGroups(keyword=''){const run=++groupGeneration;try{const result=await adminApi.groups({keyword,page:1,page_size:100});if(alive&&run===groupGeneration)groups.value=result.items}catch(e){if(alive&&run===groupGeneration)error.value=errorText(e)}}
function close(){if(!busy.value){validation.reset();detail.reset();emit('close')}}
function focusDialogTitle() {
  const title = dialogTitle.value
  const active = document.activeElement
  if (title && (active === title.closest('.el-dialog') || active === title.closest('[role="dialog"]'))) {
    title.focus()
  }
}
function changeVersionPage(step:number){versionPage.value+=step;selectedReview.value=undefined;void load()}
function changeReleasePage(step:number){releasePage.value+=step;void load()}
watch(selected,version=>{contractConfirmed.value=false;validation.reset();parameterValues.value=parameterDefaults(version?.definition.parameters || []);accountId.value=version?.audience_change?.account_id;userIds.value=[...(version?.audience_change?.user_ids||version?.manifest?.user_ids||[])];groupIds.value=[...(version?.audience_change?.group_ids||version?.manifest?.group_ids||[])];comment.value='';error.value=''},{flush:'sync'})
watch(parameterValues, () => validation.reset(), { deep: true, flush: 'sync' })
// Select controls may emit a new array containing the same members on blur.
// Only an actual account/audience change invalidates an in-flight validation.
watch(()=>JSON.stringify([accountId.value,[...userIds.value].sort((a,b)=>a-b),[...groupIds.value].sort((a,b)=>a-b)]),()=>validation.reset(),{flush:'sync'})
const canValidate = computed(() => canReview.value && !busy.value && !accountsLoading.value && !accountsError.value
  && eligibleAccounts.value.some(account => account.id === accountId.value) && !!(userIds.value.length || groupIds.value.length))
async function validate() {
  if (!canValidate.value || !selected.value || !accountId.value || validation.state.value.status === 'loading') return
  const trigger = validateButton.value
  const restoreFocus = trigger && document.activeElement === trigger
  const id = selected.value.review_id
  const payload = { account_id: accountId.value, user_ids: [...userIds.value], group_ids: [...groupIds.value], params: { ...parameterValues.value } }
  error.value = ''
  if (!await validation.run(JSON.stringify([id, payload]), signal => managedReportApi.validate(id, payload, signal))) return
  await nextTick()
  if (alive && restoreFocus && document.activeElement === trigger) validationReceipt.value?.focus()
}
function cancelValidation(event: MouseEvent) {
  const focused = document.activeElement === event.currentTarget
  validation.reset()
  if (focused) validateButton.value?.focus()
}
async function confirmed(text:string,title:string,modalClass?:string){try{await ElMessageBox.confirm(text,title,{confirmButtonText:'确认',cancelButtonText:'取消',type:'warning',modalClass});return true}catch{return false}}
async function finishAction(trigger: HTMLElement, restoreFocus: boolean, modalClass: string) {
  if (!alive) return
  busy.value = false
  await nextTick()
  const active = document.activeElement
  if (!alive || !restoreFocus || (active !== trigger && active !== document.body && !active?.closest(`.${modalClass}`))) return
  const target = detailRetry.value ?? operationError.value ?? (actionMessage.value ? actionReceipt.value : trigger)
  target?.focus()
}
async function recoverApproval(reviewId: number) {
  approvalRecovery = {
    reviewId,
    account: accountId.value,
    users: [...userIds.value],
    groups: [...groupIds.value],
    params: { ...parameterValues.value },
    comment: comment.value,
  }
  validation.reset()
  contractConfirmed.value = false
  await reloadApproval(approvalRecovery)
}
async function reloadApproval(inputs: ApprovalRecovery) {
  await Promise.all([load(), loadAccounts()])
  if (!alive) return
  if (detail.state.value.status === 'error') {
    error.value = '本次批准未完成。请重试读取当前版本。'
    return
  }
  approvalRecovery = undefined
  if (selected.value?.review_id !== inputs.reviewId || !canReview.value) {
    error.value = '本次批准未完成。请核对读回的版本状态和当前操作资格。'
    return
  }
  accountId.value = eligibleAccounts.value.some(account => account.id === inputs.account) ? inputs.account : undefined
  userIds.value = inputs.users
  groupIds.value = inputs.groups
  parameterValues.value = inputs.params
  comment.value = inputs.comment
  error.value = '本次批准未完成，原验证已失效。请使用当前执行账号重新验证后再批准。'
}
async function decide(decision: 'approved' | 'rejected' | 'withdrawn', event: MouseEvent) {
  const version = selected.value
  const trigger = event.currentTarget
  if (!version || busy.value || !(trigger instanceof HTMLElement)) return
  const restoreFocus = document.activeElement === trigger
  const validated = validation.state.value.status === 'success' ? validation.state.value.data.validation_id : undefined
  if (decision === 'approved' && (!validated || (version.definition.data_contract && !contractConfirmed.value))) return
  if (decision === 'rejected' && (!comment.value.trim() || validation.state.value.status === 'loading')) return
  const payload = { decision, comment: comment.value, contract_confirmed: decision === 'approved' && contractConfirmed.value,
    ...(decision === 'approved' ? { validation_id: validated } : {}) }
  const message = decision === 'approved' ? `批准 v${version.number}，固定当前执行账号和所选受众？`
    : `确认${decision === 'rejected' ? '退回' : '撤回'} v${version.number}？`
  busy.value = true
  error.value = ''
  actionMessage.value = ''
  try {
    if (!await confirmed(message, '审批决定', 'report-decision-confirmation') || !alive || selected.value !== version) return
    if (decision !== 'withdrawn' && !canReview.value) return
    await managedReportApi.decide(version.review_id, payload, keys.get(`decision:${version.review_id}`, payload))
    keys.clear()
    if (!alive) return
    actionMessage.value = `v${version.number} · ${decision === 'approved' ? '已批准待发布' : statusLabels[decision]}`
    await load()
    if (alive) emit('changed')
  } catch (e) {
    if (!alive) return
    if (decision === 'approved' && e instanceof ApiRequestError && (e.status === 409 || e.status === 403)) {
      await recoverApproval(version.review_id)
    } else {
      error.value = errorText(e)
    }
  }
  finally { await finishAction(trigger, restoreFocus, 'report-decision-confirmation') }
}
async function release(kind: 'publish' | 'rollback' | 'unpublish', reviewId: number, event: MouseEvent) {
  const report = data.value?.report
  const trigger = event.currentTarget
  if (!report || busy.value || !(trigger instanceof HTMLElement)) return
  const restoreFocus = document.activeElement === trigger
  const action = kind === 'unpublish' ? 'report.offline' : 'report.publish'
  busy.value = true
  error.value = ''
  actionMessage.value = ''
  try {
    const blocked = kind === 'unpublish' ? 0 : (await managedReportApi.dashboardImpact(report.id, reviewId)).blocked_dashboards
    if (!alive || !featureAllowed(user, action, isAdmin.value)) return
    const message = kind === 'unpublish' ? '下线后，所有查看者将无法继续查询此报表。'
      : `${kind === 'rollback' ? '回滚' : '发布'}所选获批版本？当前将有 ${blocked} 个仪表盘因契约不兼容而阻断，需明确采纳新口径并重新审批。`
    if (!await confirmed(message, '正式报表发布', 'report-release-confirmation') || !alive || !featureAllowed(user, action, isAdmin.value)) return
    const payload = { kind, review_id: reviewId, expected_publication_no: report.publication_no, expected_affected_dashboards: blocked }
    const result = await managedReportApi.release(report.id, payload, keys.get('release', payload))
    if (!alive) return
    actionMessage.value = `${releaseLabels[kind]}已确认 · 发布记录 #${result.version}`
    await load()
    if (alive) emit('changed')
  } catch (e) {
    if (alive) error.value = errorText(e)
  } finally {
    await finishAction(trigger, restoreFocus, 'report-release-confirmation')
  }
}
async function audienceSubmitted(id:number){audienceOpen.value=false;versionPage.value=1;await load();if(alive){selectedReview.value=id;emit('changed')}}
async function transfer(){const report=data.value?.report,owner=newOwner.value;if(!report||!owner||busy.value)return;if(!await confirmed('移交后，新责任人可以开发草稿和提交审批；当前发布内容保持生效。','移交责任人'))return;busy.value=true;error.value='';const payload={version:report.draft_version,owner_id:owner};try{await managedReportApi.transfer(report.id,report.draft_version,owner,keys.get('transfer',payload));if(alive){newOwner.value=undefined;await load();emit('changed')}}catch(e){if(alive)error.value=errorText(e)}finally{if(alive)busy.value=false}}
onMounted(()=>{refresh();if(isAdmin.value){void searchUsers();void searchGroups()}})
onScopeDispose(()=>{alive=false;approvalRecovery=undefined;userGeneration++;groupGeneration++;keys.clear()})
</script>

<template>
  <component :is="inline ? 'section' : ElDialog" v-bind="inline
    ? { class: 'border-t border-stroke pt-4 min-w-0', 'aria-label': '报表版本与发布' }
    : { modelValue: dialogOpen, 'onUpdate:modelValue': (value: boolean) => dialogOpen = value, title: '报表版本与发布', width: 'min(1020px, calc(100vw - 24px))', class: 'glass-dialog', closeOnClickModal: false, closeOnPressEscape: !busy, showClose: !busy, onOpened: focusDialogTitle, onClosed: close }">
    <template v-if="!inline" #header="{ titleId, titleClass }">
      <h2 :id="titleId" ref="dialogTitle" :class="titleClass" class="ui-focus-target" tabindex="-1">报表版本与发布</h2>
    </template>
    <div class="space-y-4">
      <header v-if="inline" class="page-header">
        <div class="page-header__content">
          <h2 tabindex="-1" class="page-header__title ui-focus-target">报表版本与发布</h2>
          <p v-if="data" class="page-header__description">{{ data.report.draft.name }}</p>
        </div>
        <div class="page-header__actions"><button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="close">关闭版本详情</button></div>
      </header>
      <LoadingState v-if="detail.state.value.status==='loading' && !retryingDetail" label="正在读取版本与发布记录…" />
      <PageState v-else-if="retryingDetail || detail.state.value.status==='error'" :title="retryingDetail ? '正在重试读取版本与发布记录…' : '读取版本与发布记录失败'" :description="retryingDetail ? '' : detail.state.value.status === 'error' ? detail.state.value.message || '请重试读取版本。' : ''" :loading="retryingDetail" tone="danger">
        <template #actions><button ref="detailRetry" type="button" class="ghost-button ui-control-target" :aria-disabled="busy || retryingDetail || undefined" @click="retryDetail">重试读取版本</button></template>
      </PageState>
      <button v-else ref="refreshStatus" class="ghost-button text-xs ui-control-target" :disabled="busy" @click="refresh">刷新状态</button>
      <p v-if="error" ref="operationError" role="alert" tabindex="-1" class="text-sm text-danger ui-focus-target">{{ error }}</p>
      <p v-if="actionMessage" ref="actionReceipt" role="status" tabindex="-1" class="text-sm text-success ui-focus-target">{{ actionMessage }}</p>
      <template v-if="data">
        <div class="glass-panel p-3 space-y-2"><h2 v-if="!inline" class="font-medium break-words">{{ data.report.draft.name }}</h2><p class="text-xs text-muted">责任人 {{ data.report.owner_name }} · {{ data.report.active_release_id?'已发布':'未发布' }} · 发布序号 {{ data.report.publication_no }}</p><p class="text-xs text-muted">修改草稿不影响已发布版本，正式运行始终使用获批版本与报表执行账号。</p></div>
<button v-if="data.report.active_release_id&&featureAllowed(user,'report.design',isAdmin||data.report.owner_id===user.user?.id)" class="ghost-button text-xs ui-control-target" :disabled="busy" @click="audienceOpen=true">仅调整正式受众</button>
        <div v-if="!reviewId" class="flex flex-wrap items-center gap-2"><el-select v-model="selectedReview" aria-label="选择报表版本" class="!w-72 max-w-full" :disabled="busy"><el-option v-for="version in data.versions.items" :key="version.id" :value="version.review_id" :label="`v${version.number} · ${statusLabels[version.status]} · ${version.definition.name}`" /></el-select><button class="ghost-button text-xs ui-control-target" :disabled="versionPage<=1 || busy" @click="changeVersionPage(-1)">较新版本</button><span class="text-xs text-muted">{{ versionPage }}</span><button class="ghost-button text-xs ui-control-target" :disabled="versionPage*10>=data.versions.total || busy" @click="changeVersionPage(1)">较早版本</button></div>
        <p v-if="!data.versions.total" class="text-sm text-muted">尚未提交版本，请先从开发列表提交审批。</p>
        <section v-if="selected" class="space-y-3" aria-label="报表版本内容">
          <p class="text-xs text-muted">v{{ selected.number }} · {{ selected.status === 'approved' && !selected.was_published ? '已批准待发布' : statusLabels[selected.status] }} · 提交人 {{ selected.submitter_name }} · {{ formatTime(selected.created_at) }}</p>
          <p class="text-xs text-muted">数据源修订 #{{ selected.definition.definition_revision_id }} · {{ selected.definition.database_name || '默认数据库' }} · {{ selected.definition.chart_type }}</p>
          <dl v-if="selected.definition.data_contract" class="glass-panel p-4 grid gap-3 text-sm sm:grid-cols-2" aria-label="业务口径与数据范围"><div><dt class="text-muted">业务口径</dt><dd class="mt-1">{{selected.definition.data_contract.meaning}}</dd></div><div><dt class="text-muted">数据范围</dt><dd class="mt-1">{{selected.definition.data_contract.scope_description}}</dd></div><div><dt class="text-muted">指标与单位</dt><dd class="mt-1">{{selected.definition.data_contract.metrics.join('、')}} · {{selected.definition.data_contract.unit||'未声明'}}</dd></div><div><dt class="text-muted">数据时效</dt><dd class="mt-1">{{selected.definition.data_contract.freshness.mode==='watermark_column'?'按数据截止字段核验':'数据时效未验证'}}<template v-if="selected.definition.data_contract.freshness.mode==='watermark_column'"> · 最长 {{selected.definition.data_contract.freshness.max_data_age_seconds}} 秒</template></dd></div></dl>
          <details :open="!user.actionControlled||user.can('query.read')" class="text-xs text-muted"><summary class="cursor-pointer py-3">技术核对：冻结的只读查询</summary><pre class="bg-surface-inset rounded-lg p-3 overflow-auto max-h-56 whitespace-pre-wrap break-words">{{ selected.definition.sql_text }}</pre></details>
          <details class="text-xs text-muted"><summary class="cursor-pointer">查看图表与下钻配置</summary><pre class="whitespace-pre-wrap break-words max-h-60 overflow-auto">{{JSON.stringify(selected.definition.chart_config,null,2)}}</pre></details>
          <p v-if="selected.definition.export_policy?.enabled" class="text-sm text-warning">此版本允许申请 CSV、JSON、Excel 快照导出，最多 {{ selected.definition.export_policy.max_rows.toLocaleString() }} 行；下载另需独立审批。</p>
          <details v-if="previous" class="text-xs text-muted space-y-2"><summary class="cursor-pointer py-1">与 v{{ previous.number }} 对比：{{ differences.length?differences.join('、'):'内容相同' }}</summary><div class="grid grid-cols-1 sm:grid-cols-2 gap-2"><pre class="bg-surface-inset p-3 rounded-lg overflow-auto max-h-56 whitespace-pre-wrap break-words">v{{ previous.number }}：{{ JSON.stringify(previous.definition,null,2) }}</pre><pre class="bg-surface-inset p-3 rounded-lg overflow-auto max-h-56 whitespace-pre-wrap break-words">v{{ selected.number }}：{{ JSON.stringify(selected.definition,null,2) }}</pre></div></details>
          <p v-if="selected.comment" class="text-xs text-muted break-words">审批意见：{{ selected.comment }}（{{ selected.decider_name }}）</p>
          <p v-if="selected.account_name" class="text-xs text-muted">批准执行账号：{{ selected.account_name }}；受众：{{ selected.manifest?.user_ids.length ?? 0 }} 位用户、{{ selected.manifest?.group_ids.length ?? 0 }} 个用户组</p>
          <p v-if="selected.audience_change" class="text-xs text-accent">仅调整受众提案 · 基准发布 #{{selected.audience_change.base_publication_no}} · SQL、契约和执行账号由服务端复制，验证必须使用固定受众。</p>
          <p v-if="isAdmin && selected.status==='pending' && selected.submitted_by===user.user?.id" class="text-sm text-warning">不能审批自己提交的版本，请由其他管理员审阅。</p>
          <div v-if="canReview" class="ui-inset-card border border-stroke rounded-lg p-3 space-y-3">
            <ReportParameterForm v-model="parameterValues" :definitions="selected.definition.parameters || []" :disabled="busy" title="验证条件" @apply="validate" @reset="parameterValues = parameterDefaults(selected.definition.parameters || [])" />
            <p class="text-xs text-muted">选择执行账号及受众，用该账号验证冻结的查询，确认数据范围后批准。</p>
            <label class="block text-xs text-muted space-y-1">报表执行账号<el-select v-model="accountId" aria-label="审批执行账号" class="w-full" :disabled="busy || accountsLoading || !!accountsError"><el-option v-for="account in eligibleAccounts" :key="account.id" :value="account.id" :label="`${account.name} · ${account.username}`" /></el-select></label>
            <LoadingState v-if="accountsLoading" label="正在读取报表执行账号…" />
            <div v-else-if="accountsError" class="flex flex-wrap items-center gap-2"><p role="alert" class="text-sm text-danger">{{ accountsError }}</p><button type="button" class="ghost-button ui-control-target" @click="loadAccounts">重试读取执行账号</button></div>
            <p v-else-if="!eligibleAccounts.length" class="text-xs text-warning">此修订没有可用执行账号，请联系数据源管理者添加后刷新状态。</p>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <label class="text-xs text-muted space-y-1">共享用户<el-select v-model="userIds" aria-label="审批共享用户" multiple filterable remote :remote-method="searchUsers" class="w-full" :disabled="busy"><el-option v-for="member in users" :key="member.id" :value="member.id" :label="member.username" :disabled="!member.is_active" /></el-select></label>
              <label class="text-xs text-muted space-y-1">共享用户组<el-select v-model="groupIds" aria-label="审批共享用户组" multiple filterable remote :remote-method="searchGroups" class="w-full" :disabled="busy"><el-option v-for="group in groups" :key="group.id" :value="group.id" :label="group.name" /></el-select></label>
            </div>
            <p class="text-xs text-warning">受众会获得此查询返回的数据。部门数据隔离请使用受限数据库视图或不同执行账号。</p>
            <button ref="validateButton" class="ghost-button text-xs ui-control-target" :disabled="!canValidate" :aria-disabled="validation.state.value.status === 'loading' || undefined" @click="validate">{{ validation.state.value.status==='loading'?'正在验证…':'使用执行账号验证' }}</button>
            <button v-if="validation.state.value.status==='loading'" class="ghost-button text-xs ml-2 ui-control-target" @click="cancelValidation">取消验证</button>
            <p v-if="validation.state.value.status==='error'" role="alert" class="text-sm text-danger">{{ validation.state.value.message }}</p>
            <template v-else-if="validation.state.value.status==='success'"><p ref="validationReceipt" role="status" tabindex="-1" class="text-xs text-success ui-focus-target">执行账号验证通过，15 分钟内可批准；更改账号或受众后需要重新验证。</p><ReportResult :type="selected.definition.chart_type" :config="selected.definition.chart_config" :result="validation.state.value.data.result" :title="selected.definition.name" /></template>
            <label class="block text-xs text-muted space-y-1">审批意见<el-input v-model="comment" aria-label="审批意见" type="textarea" maxlength="1000" :disabled="busy" placeholder="退回必须填写原因" /></label>
            <label v-if="selected.definition.data_contract" class="text-xs flex gap-2"><input v-model="contractConfirmed" type="checkbox">已核对 SQL、返回样本和业务范围，确认符合声明的口径、单位和时效规则</label><details v-if="selected.definition.data_contract" class="text-xs text-muted"><summary>查看冻结的数据契约</summary><pre class="whitespace-pre-wrap">{{JSON.stringify(selected.definition.data_contract,null,2)}}</pre></details>
            <div class="flex flex-wrap gap-2"><button class="liquid-button text-xs ui-control-target" :disabled="(!!selected.definition.data_contract&&!contractConfirmed) || validation.state.value.status!=='success'" :aria-disabled="busy || undefined" @click="decide('approved', $event)">批准此版本</button><button class="ghost-button text-xs ui-control-target" :disabled="!comment.trim() || validation.state.value.status==='loading'" :aria-disabled="busy || undefined" @click="decide('rejected', $event)">退回修改</button></div>
          </div>
          <div class="flex flex-wrap gap-2">
            <button v-if="selected.status==='approved'&&featureAllowed(user,'report.design',['admin','developer'].includes(user.role))" class="ghost-button text-xs ui-control-target" :disabled="busy" @click="promotionOpen=true">迁移到目标环境草稿</button>
            <button v-if="selected.status==='pending' && data.report.owner_id===user.user?.id && featureAllowed(user,'report.design',true)" class="ghost-button text-xs ui-control-target" :aria-disabled="busy || undefined" @click="decide('withdrawn', $event)">撤回申请</button>
            <button v-if="featureAllowed(user,'report.publish',isAdmin) && selected.status==='approved' && (!data.report.active_version || selected.number>=data.report.active_version)" class="liquid-button text-xs ui-control-target" :disabled="activeReview===selected.review_id" :aria-disabled="busy || undefined" @click="release('publish',selected.review_id,$event)">发布此版本</button>
            <button v-if="featureAllowed(user,'report.publish',isAdmin) && selected.status==='approved' && wasPublished && activeReview!==selected.review_id" class="ghost-button text-xs ui-control-target" :aria-disabled="busy || undefined" @click="release('rollback',selected.review_id,$event)">回滚到此版本</button>
            <button v-if="featureAllowed(user,'report.offline',isAdmin) && data.report.active_release_id" class="ghost-button text-xs text-warning ui-control-target" :aria-disabled="busy || undefined" @click="release('unpublish',0,$event)">下线报表</button>
          </div>
        </section>
        <section class="space-y-2 border-t border-stroke pt-4" aria-label="报表发布历史"><h3 class="text-sm font-medium">发布历史</h3><p v-if="!data.releases.total" class="text-xs text-muted">尚无发布记录</p><p v-for="event in data.releases.items" :key="event.id" class="text-xs text-muted break-words">#{{ event.number }} · {{ releaseLabels[event.kind] }}<span v-if="event.version"> v{{ event.version }}</span> · {{ event.performer_name }} · {{ formatTime(event.created_at) }}</p><div v-if="data.releases.total>10" class="flex gap-2"><button class="ghost-button text-xs ui-control-target" :disabled="releasePage<=1 || busy" @click="changeReleasePage(-1)">较新记录</button><button class="ghost-button text-xs ui-control-target" :disabled="releasePage*10>=data.releases.total || busy" @click="changeReleasePage(1)">较早记录</button></div></section>
<details @toggle="dependenciesOpen=($event.target as HTMLDetailsElement).open"><summary class="text-xs cursor-pointer">查看当前、开发和历史仪表盘引用</summary><ReportDashboardDependencies v-if="dependenciesOpen" :id="reportId" /></details>
        <section v-if="user.role==='admin'&&featureAllowed(user,'report.design',true)" class="space-y-2 border-t border-stroke pt-4"><h3 class="text-sm font-medium">移交责任人</h3><div class="flex flex-wrap gap-2"><el-select v-model="newOwner" aria-label="报表新责任人" filterable remote :remote-method="searchUsers" class="!w-64 max-w-full" :disabled="busy"><el-option v-for="member in writers" :key="member.id" :value="member.id" :label="member.username" /></el-select><button class="ghost-button text-xs ui-control-target" :disabled="busy || !newOwner || newOwner===data.report.owner_id" @click="transfer">移交</button></div></section>
      </template>
    </div>
    <ReportAudienceDialog v-if="audienceOpen" :report-id="reportId" @close="audienceOpen=false" @submitted="audienceSubmitted" />
    <ReportPromotionDialog v-if="promotionOpen&&selected" :report-id="reportId" :version="selected" @close="promotionOpen=false" @created="id=>{promotionOpen=false;emit('promoted',id)}" />
  </component>
</template>
