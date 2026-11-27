<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageState from '../../components/base/PageState.vue'
import {computed,nextTick,onMounted,onScopeDispose,reactive,ref,watch} from 'vue'
import {useRoute,useRouter} from 'vue-router'
import {ElMessageBox} from 'element-plus'
import { ApiRequestError } from '../../utils/request'
import { iamUiEnabled } from '../../utils/iam-mode'
import { useUserStore } from '../../stores/user'
import {subscriptionApi,type DeliveryDetail,type IncidentEvent,type MetricAlertRule,type MetricIncident,type ReportSubscription,type SubscriptionDefinition,type SubscriptionRun} from '../../api/report-subscriptions'
import {managedReportApi,type PublishedReport} from '../../api/managed-report'
import {parameterDefaults,type ParameterValues} from './reportParameters'
import {reportCommandKeys} from './reportCommands'
import ReportParameterForm from './ReportParameterForm.vue'
import ReportResult from './ReportResult.vue'
import {useReportRun} from './useReportRun'
const route=useRoute(),router=useRouter(),keys=reportCommandKeys()
const user = useUserStore()
const canUsePersonal = computed(() => !iamUiEnabled || user.can('report.subscribe'))
const personalPanes = [{ id: 'plans', name: '订阅计划' }, { id: 'runs', name: '交付记录' }, { id: 'incidents', name: '指标告警' }] as const
const teamPane = { id: 'team', name: '团队订阅结果' } as const
type Pane = typeof personalPanes[number]['id'] | typeof teamPane.id
const panes = computed(() => canUsePersonal.value ? [...personalPanes, teamPane] : [teamPane])
const pane = ref<Pane>(route.query.team_delivery !== undefined || !canUsePersonal.value ? 'team' : route.query.delivery ? 'runs' : route.query.incident ? 'incidents' : 'plans')
const plans=ref<ReportSubscription[]>([]),reports=ref<PublishedReport[]>([]),runs=ref<SubscriptionRun[]>([]),incidents=ref<MetricIncident[]>([])
const webhook=ref(false),includeArchived=ref(!!route.query.subscription),runPage=ref(1),incidentPage=ref(1),runTotal=ref(0),incidentTotal=ref(0)
const planList=useReportRun<Awaited<ReturnType<typeof subscriptionApi.list>>>()
const runList=useReportRun<Awaited<ReturnType<typeof subscriptionApi.runs>>>()
const incidentList=useReportRun<Awaited<ReturnType<typeof subscriptionApi.incidents>>>()
const reportList=useReportRun<Awaited<ReturnType<typeof managedReportApi.published>>>()
const teamList = useReportRun<Awaited<ReturnType<typeof subscriptionApi.teamDeliveries>>>()
const teamResults = computed(() => teamList.state.value.status === 'success' ? teamList.state.value.data.items : [])
const listState=computed(() => ({ plans: planList.state.value, runs: runList.state.value, incidents: incidentList.state.value, team: teamList.state.value })[pane.value])
const loading=computed(() => listState.value.status === 'loading'),busy=ref(false),error=ref(''),editing=ref(false),params=ref<ParameterValues>({})
const form=reactive({id:0,version:0,name:'',report:0,interval:60,defaults:true,alert:false,webhook:false,enabled:true}),rule=reactive<MetricAlertRule>({column:'',operator:'gt',threshold:'0'}),selected=computed(()=>reports.value.find(item=>item.id===form.report)),numericColumns=computed(()=>selected.value?.data_contract?.result_schema.filter(column=>['integer','number','lossless_numeric'].includes(column.logical_type))||[])
const operators={lt:'小于',lte:'小于等于',gt:'大于',gte:'大于等于',eq:'等于',ne:'不等于'},runLabels:Record<string,string>={queued:'排队中',running:'执行中',succeeded:'已交付',failed:'执行失败',skipped:'未生成结果',interrupted:'运行中断',review_required:'待本人确认',authorization_changed:'身份或成员权限已变化'},incidentLabels={open:'待确认',acknowledged:'处理中',closed:'已关闭',superseded:'配置变更已结束'},hookLabels={pending:'等待投递',sending:'投递中',sent:'接口已接收',failed:'投递失败'},eventLabels:Record<string,string>={metric_opened:'指标达到阈值',metric_recovered:'指标恢复',acknowledge:'确认接手',comment:'处理记录',close:'核对后关闭',configuration_changed:'配置变更结束跟踪'}
const deliveryOpen=ref(false),delivery=ref<DeliveryDetail>(),incidentOpen=ref(false),incident=ref<{incident:MetricIncident;events:IncidentEvent[]}>(),comment=ref(''),detailError=ref('')
const teamDeliveryFocused = computed(() => route.query.team_delivery !== undefined)
const section = ref<HTMLElement>()
const snapshotConfig=computed(()=>{const config={...(delivery.value?.snapshot.chart_config||{})};delete config.drillthrough;return config})
const watermark=computed(()=>{const snapshot=delivery.value?.snapshot,column=snapshot?.data_contract?.freshness?.column,index=column?snapshot?.result.columns?.indexOf(column):-1;if(!snapshot||index===undefined||index<0)return'未验证';const times=(snapshot.result.rows||[]).map(row=>Date.parse(String(row[index]))).filter(Number.isFinite);return times.length?time(new Date(Math.min(...times)).toISOString()):'结果没有可验证水位'})
const time=(value?:string)=>value?new Date(value).toLocaleString('zh-CN'):'—'
let alive=true,detailGeneration=0,detailController:AbortController|undefined,timer:ReturnType<typeof setInterval>|undefined
async function load() {
  if (busy.value) return
  if (pane.value === 'team') {
    await teamList.run('team', signal => subscriptionApi.teamDeliveries(signal))
    return
  }
  if (!canUsePersonal.value) return
  if (pane.value === 'plans') {
    const archived = includeArchived.value
    if (await planList.run(String(archived), signal => subscriptionApi.list(archived, signal)) && planList.state.value.status === 'success') {
      plans.value = planList.state.value.data.items
      webhook.value = planList.state.value.data.webhook_configured
    }
  } else if (pane.value === 'runs') {
    const page = runPage.value
    if (await runList.run(String(page), signal => subscriptionApi.runs(page, signal)) && runList.state.value.status === 'success') {
      runs.value = runList.state.value.data.items
      runTotal.value = runList.state.value.data.total
    }
  } else {
    const page = incidentPage.value
    if (await incidentList.run(String(page), signal => subscriptionApi.incidents(page, signal)) && incidentList.state.value.status === 'success') {
      incidents.value = incidentList.state.value.data.items
      incidentTotal.value = incidentList.state.value.data.total
    }
  }
}
async function loadReports() {
  if (await reportList.run('published', signal => managedReportApi.published({ page: 1, page_size: 100 }, signal)) && reportList.state.value.status === 'success') {
    reports.value = reportList.state.value.data.items
    if (!form.id && !form.report && reports.value[0]) { form.report = reports.value[0].id; chooseReport() }
  }
}
function selectPane(id: string) {
  const entry = panes.value.find(item => item.id === id)
  if (entry && !busy.value) pane.value = entry.id
}
async function mutate(work:()=>Promise<void>){if(busy.value||!canUsePersonal.value)return;busy.value=true;error.value='';try{await work();keys.clear()}catch(e){if(alive&&e!=='cancel'&&e!=='close')error.value=e instanceof Error?e.message:'操作未确认，请先刷新记录核对'}finally{if(alive)busy.value=false}if(alive)await load()}
function chooseReport(){params.value=parameterDefaults(selected.value?.parameters||[]);form.defaults=!(selected.value?.parameters||[]).some(parameter=>parameter.required&&parameter.default===undefined&&parameter.default_relative_days===undefined);rule.column=numericColumns.value[0]?.name||'';if(!rule.column)form.alert=false}
function create(){Object.assign(form,{id:0,version:0,name:'',report:0,interval:60,defaults:true,alert:false,webhook:false,enabled:true});chooseReport();editing.value=true;pane.value='plans';void loadReports()}
function edit(item:ReportSubscription){Object.assign(form,{id:item.id,version:item.version,name:item.definition.name,report:item.definition.report_id,interval:item.definition.interval_minutes,defaults:!Object.keys(item.definition.params||{}).length,alert:!!item.definition.alert_rule,webhook:item.definition.webhook,enabled:item.definition.enabled});params.value=JSON.parse(JSON.stringify(item.definition.params||{}));Object.assign(rule,item.definition.alert_rule||{column:numericColumns.value[0]?.name||'',operator:'gt',threshold:'0'});editing.value=true;pane.value='plans';void loadReports()}
async function save(){if(reportList.state.value.status!=='success'||!selected.value)return;const definition:SubscriptionDefinition={name:form.name,report_id:selected.value.id,publication_no:selected.value.publication_no,interval_minutes:form.interval,params:form.defaults?{}:{...params.value},...(form.alert?{alert_rule:{...rule}}:{}),webhook:form.webhook,enabled:form.enabled},payload={id:form.id,version:form.version,definition};await mutate(async()=>{await subscriptionApi.save(form.id,form.version,definition,keys.get('subscription-save',payload));if(alive)await cancelEditing()})}
async function runOnce(item:ReportSubscription){await mutate(async()=>{const payload={id:item.id,version:item.version};await subscriptionApi.run(item.id,item.version,keys.get('subscription-run',payload));if(alive)pane.value='runs'})}
async function pause(item:ReportSubscription){await mutate(async()=>{const definition={...item.definition,enabled:false},payload={id:item.id,version:item.version,definition};await subscriptionApi.save(item.id,item.version,definition,keys.get('subscription-pause',payload))})}
async function archive(item:ReportSubscription){await mutate(async()=>{await ElMessageBox.confirm('归档后停止计划，历史快照和处理记录保留。','归档订阅',{confirmButtonText:'归档',cancelButtonText:'取消'});if(!alive)return;await subscriptionApi.archive(item.id,item.version,keys.get('subscription-archive',{id:item.id,version:item.version}))})}

function personalPlanStatus(item: ReportSubscription) {
  if (item.archived) return '已归档'
  if (item.review_required) {
    if (!item.review_until) return '待本人确认'
    if (Date.parse(item.review_until) <= Date.now()) return '复核已到期'
  }
  return item.definition.enabled ? '已启用' : '已暂停'
}

async function confirmPersonal(item: ReportSubscription) {
  await mutate(async () => {
    const payload = { id: item.id, version: item.version }
    await subscriptionApi.confirm(item.id, item.version, keys.get('subscription-confirm', payload))
  })
}

type DetailKey = 'delivery' | 'team_delivery' | 'incident'
const detailKeys: DetailKey[] = ['delivery', 'team_delivery', 'incident']

function selectObject(key: DetailKey, id: number | string) {
  const query = { ...route.query, view: 'subscriptions', [key]: String(id) }
  for (const other of detailKeys) if (other !== key) delete query[other]
  delete query.subscription
  void router.push({ query, hash: route.hash })
}
function selectDelivery(id:number){selectObject('delivery', id)}
function selectIncident(id:number){selectObject('incident', id)}
function clearFocus(...focus: DetailKey[]) {
  if (!alive || !focus.some(key => route.query[key] !== undefined)) return
  const query = { ...route.query }
  for (const key of focus) delete query[key]
  void router.replace({ query, hash: route.hash })
}
function objectId(value: unknown) {
  const id = typeof value === 'string' && /^\d+$/.test(value) ? Number(value) : 0
  return Number.isSafeInteger(id) && id > 0 ? id : undefined
}
function teamId(value: unknown) {
  return typeof value === 'string' && /^[a-f\d]{8}(?:-[a-f\d]{4}){3}-[a-f\d]{12}$/i.test(value) ? value : undefined
}
async function focus() {
  const teamFocused = teamDeliveryFocused.value
  const teamDeliveryId = teamId(route.query.team_delivery)
  const deliveryId = canUsePersonal.value ? objectId(route.query.delivery) : undefined
  const incidentId = canUsePersonal.value ? objectId(route.query.incident) : undefined
  const current = ++detailGeneration
  detailController?.abort()
  delivery.value = undefined
  incident.value = undefined
  detailError.value = ''
  comment.value = ''
  deliveryOpen.value = teamFocused || !!deliveryId
  incidentOpen.value = !teamFocused && !deliveryId && !!incidentId
  if (teamFocused) {
    pane.value = 'team'
    if (!teamDeliveryId) {
      detailError.value = '团队订阅结果链接无效，请从本人结果列表重新打开。'
      return
    }
  }
  if (!teamDeliveryId && !deliveryId && !incidentId) return
  const controller = new AbortController()
  detailController = controller
  try {
    if (teamDeliveryId) {
      const data = await subscriptionApi.teamDelivery(teamDeliveryId, controller.signal)
      if (alive && current === detailGeneration) delivery.value = { snapshot: data.snapshot, webhook_deliveries: [] }
    } else if (deliveryId) {
      pane.value = 'runs'
      const data = await subscriptionApi.delivery(deliveryId, controller.signal)
      if (alive && current === detailGeneration) delivery.value = data
    } else if (incidentId) {
      pane.value = 'incidents'
      const data = await subscriptionApi.incident(incidentId, controller.signal)
      if (alive && current === detailGeneration) incident.value = data
    }
  } catch (e) {
    if (alive && current === detailGeneration) {
      const unavailable = teamFocused && e instanceof ApiRequestError && [403, 404, 409].includes(e.status ?? 0)
      detailError.value = unavailable ? '当前无法领取此期次。请核对领取资格和结果是否有效。' : e instanceof Error ? e.message : '详情读取失败'
    }
  }
}
async function action(value:'acknowledge'|'comment'|'close'){const item=incident.value?.incident;if(!item)return;await mutate(async()=>{const payload={id:item.id,version:item.version,action:value,comment:comment.value};await subscriptionApi.action(item.id,item.version,value,comment.value,keys.get('metric-action',payload));if(alive)await focus()})}
watch([() => route.query.delivery, () => route.query.team_delivery, () => route.query.incident], ([deliveryId, teamDeliveryId, incidentId], [previousDelivery, previousTeamDelivery, previousIncident]) => {
  void focus()
  if (deliveryId || teamDeliveryId || incidentId) return
  const previousTeamId = teamId(previousTeamDelivery)
  const previous = previousTeamId ?? objectId(previousDelivery) ?? objectId(previousIncident)
  const attribute = previousTeamId ? 'data-team-delivery-id' : previousDelivery ? 'data-delivery-id' : 'data-incident-id'
  if (previous) void nextTick(() => section.value?.querySelector<HTMLElement>(`[${attribute}="${previous}"]`)?.focus())
})
watch(pane, () => void load())
watch([() => route.query.subscription, planList.state], ([raw, state], [previous]) => {
  if (!canUsePersonal.value || teamDeliveryFocused.value) return
  if (raw !== previous && raw) { includeArchived.value = true; pane.value = 'plans'; void load() }
  if (!raw || editing.value || state.status !== 'success' || pane.value !== 'plans') return
  const item = state.data.items.find(plan => plan.id === objectId(raw) && !plan.archived)
  if (item) edit(item)
})
async function cancelEditing() {
  editing.value = false
  if (route.query.subscription) {
    const query = { ...route.query }
    delete query.subscription
    await router.replace({ query, hash: route.hash })
  }
}
onMounted(() => {
  void load()
  void focus()
  timer=setInterval(() => {
    if (!loading.value && !busy.value && !editing.value && !document.hidden && navigator.onLine) void load()
  }, 15000)
})
onScopeDispose(()=>{alive=false;detailGeneration++;detailController?.abort();clearInterval(timer);delivery.value=undefined;incident.value=undefined})
</script>

<template>
  <section ref="section" class="space-y-4" aria-label="订阅与指标告警">
    <header class="flex flex-wrap justify-between gap-3 items-start">
      <p class="text-xs text-muted">快照保留 7 天。</p>
      <div class="flex flex-wrap gap-2"><button class="ghost-button text-xs ui-control-target" :disabled="loading||busy" @click="load">刷新订阅记录</button><button v-if="canUsePersonal" class="liquid-button text-xs ui-control-target" :disabled="busy" @click="create">新建订阅</button></div>
    </header>
    <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
    <PageCategoryTabs label="订阅分类" :categories="panes" :active-id="pane" @select="selectPane">
    <form v-if="editing && pane === 'plans'" class="glass-panel p-4 space-y-4" aria-label="订阅计划编辑" @submit.prevent="save">
      <h3 class="font-medium">{{form.id?'编辑订阅计划':'新建订阅计划'}}</h3>
      <LoadingState v-if="reportList.state.value.status === 'loading'" label="正在读取已发布报表选项…" />
      <div v-else-if="reportList.state.value.status === 'error'" class="flex flex-wrap items-center gap-2"><p role="alert" class="text-sm text-danger">{{ reportList.state.value.message }}</p><button type="button" class="ghost-button ui-control-target" @click="loadReports">重试读取报表</button></div>
      <div class="grid sm:grid-cols-2 gap-3"><label class="text-xs min-w-0">订阅名称<input v-model.trim="form.name" aria-label="订阅名称" class="glass-input block w-full mt-1" maxlength="120" required :disabled="busy" /></label><label class="text-xs min-w-0">已发布报表<select v-model.number="form.report" aria-label="订阅的已发布报表" class="glass-input block w-full mt-1" :disabled="busy || reportList.state.value.status !== 'success'" @change="chooseReport"><option :value="0">请选择</option><option v-for="report in reports" :key="report.id" :value="report.id">{{report.name}} · 发布 #{{report.publication_no}}</option></select></label></div>
      <p v-if="reportList.state.value.status === 'success' && !reports.length" class="text-xs text-muted">当前没有可用于订阅的已发布报表。</p>
      <p v-else-if="reportList.state.value.status === 'success' && form.report&&!selected" role="alert" class="text-xs text-warning">原报表不在当前可用列表中，请选择可用的已发布报表。</p>
      <label class="block text-xs">执行间隔（分钟，5–10080）<input v-model.number="form.interval" aria-label="订阅执行间隔" type="number" min="5" max="10080" step="1" class="glass-input block w-full sm:w-48 mt-1" required :disabled="busy" /></label>
      <p class="text-xs text-muted">需要复核的计划须由本人确认后才会调度。计划启用后按设置的间隔执行，停机期间错过的期次不会集中补发。</p>
      <label class="text-sm flex gap-2"><input v-model="form.defaults" type="checkbox" :disabled="busy" />每次执行使用报表默认筛选（相对日期会随执行日更新）</label>
      <template v-if="!form.defaults&&selected"><ReportParameterForm v-model="params" :definitions="selected.parameters||[]" :disabled="busy" :actions="false" title="固定订阅筛选" @reset="params=parameterDefaults(selected.parameters||[])" /><p class="text-xs text-muted">保存这些固定值后，日期不会自动移动。</p></template>
      <label class="text-sm flex gap-2"><input v-model="form.alert" type="checkbox" :disabled="busy||!numericColumns.length" />启用指标阈值告警</label>
      <div v-if="form.alert" class="glass-panel p-3 space-y-3"><p class="text-xs text-muted">告警需要返回唯一一行有效数值的汇总报表。无数据、NULL、截断或查询失败不会被当成“正常恢复”。</p><div class="grid sm:grid-cols-3 gap-3"><label class="text-xs">数值指标<select v-model="rule.column" aria-label="告警数值指标" class="glass-input w-full mt-1" :disabled="busy"><option v-for="column in numericColumns" :key="column.name" :value="column.name">{{column.name}}</option></select></label><label class="text-xs">比较关系<select v-model="rule.operator" aria-label="告警比较关系" class="glass-input w-full mt-1" :disabled="busy"><option v-for="(label,key) in operators" :key="key" :value="key">{{label}}</option></select></label><label class="text-xs">阈值<input v-model.trim="rule.threshold" aria-label="告警阈值" class="glass-input w-full mt-1" maxlength="256" required :disabled="busy" /></label></div></div>
      <label class="text-sm flex gap-2"><input v-model="form.webhook" type="checkbox" :disabled="busy||(!webhook&&!form.webhook)" />通过环境配置的外部接口投递事件</label><p class="text-xs text-muted">{{webhook?'只发送事件信息及登录后可查看的链接，不发送数据行、筛选值或令牌。':'此环境未配置外部接口，站内交付仍可使用。'}}</p>
      <label class="text-sm flex gap-2"><input v-model="form.enabled" type="checkbox" :disabled="busy" />启用计划</label>
      <div class="flex flex-wrap gap-2"><button class="liquid-button text-xs ui-control-target" :disabled="busy||reportList.state.value.status!=='success'||!selected||!form.name">保存订阅计划</button><button type="button" class="ghost-button text-xs ui-control-target" :disabled="busy" @click="cancelEditing">取消编辑</button></div>
    </form>
    <LoadingState v-if="loading" :label="`正在读取${panes.find(item => item.id === pane)?.name}…`" />
    <PageState v-else-if="listState.status === 'error'" title="读取当前订阅分类失败" :description="listState.message || '请重试读取记录。'" tone="danger">
      <template #actions><button class="ghost-button ui-control-target" @click="load">重试读取分类</button></template>
    </PageState>
    <template v-else-if="listState.status === 'success' && pane==='plans'">
      <label class="text-xs flex gap-2"><input v-model="includeArchived" type="checkbox" :disabled="loading" @change="load" />包含已归档计划</label>
      <div v-if="plans.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="订阅计划表格">
        <table class="editorial-table" aria-label="订阅计划">
          <thead><tr><th scope="col">序号</th><th scope="col">计划名称</th><th scope="col">频率</th><th scope="col">最近执行</th><th scope="col">状态</th><th scope="col">操作</th></tr></thead>
          <tbody><tr v-for="(item, index) in plans" :key="item.id" :aria-label="`订阅计划${item.id}`">
            <td class="editorial-table__number">{{ String(index + 1).padStart(2, '0') }}</td>
            <td class="editorial-table__name"><h2 class="editorial-object-title">{{ item.definition.name }}</h2><p class="editorial-table__meta">报表发布 #{{ item.definition.publication_no }}</p></td>
            <td class="whitespace-nowrap">每 {{ item.definition.interval_minutes }} 分钟<p v-if="item.definition.enabled && !item.archived" class="text-xs text-muted">下次 {{ time(item.next_run_at) }}</p></td>
            <td class="whitespace-nowrap">{{ runLabels[item.last_status] || item.last_status || '—' }}<p v-if="item.last_run_at" class="text-xs text-muted">{{ time(item.last_run_at) }}</p></td>
            <td>
              <span class="status-badge" :class="{'status-badge--success': item.definition.enabled && !item.archived}">{{ personalPlanStatus(item) }}</span>
              <p v-if="item.review_until" class="text-xs text-muted whitespace-nowrap">复核有效至 {{ time(item.review_until) }}</p>
            </td>
            <td>
              <div class="editorial-table__actions">
                <button v-if="item.review_required && !item.archived" class="liquid-button text-xs ui-control-target whitespace-nowrap" :disabled="busy" @click="confirmPersonal(item)">{{ item.review_until ? '确认续期' : '确认开通' }}</button>
                <button class="editorial-text-action whitespace-nowrap" :disabled="busy||item.archived" @click="edit(item)">编辑计划</button>
                <button class="ghost-button text-xs ui-control-target whitespace-nowrap" :disabled="busy||item.archived||!item.definition.enabled" @click="runOnce(item)">执行一次</button>
                <button class="ghost-button text-xs ui-control-target whitespace-nowrap" :disabled="busy||item.archived||!item.definition.enabled" @click="pause(item)">暂停计划</button>
                <button class="ghost-button text-xs ui-control-target whitespace-nowrap" :disabled="busy||item.archived" @click="archive(item)">归档计划</button>
              </div>
            </td>
          </tr></tbody>
        </table>
      </div>
      <PageState v-else title="暂无订阅计划" description="可选择自己能查看的已发布报表创建订阅。" />
    </template>
    <template v-else-if="listState.status === 'success' && pane==='runs'">
      <div v-if="runs.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="交付记录表格">
        <table class="editorial-table" aria-label="订阅交付记录">
          <thead><tr><th scope="col">序号</th><th scope="col">快照名称</th><th scope="col">交付状态</th><th scope="col">生成时间</th><th scope="col">有效至</th><th scope="col">操作</th></tr></thead>
          <tbody><tr v-for="(run, index) in runs" :key="run.id" :aria-label="`订阅交付${run.delivery_id}`">
            <td class="editorial-table__number">{{ String((runPage - 1) * 20 + index + 1).padStart(2, '0') }}</td>
            <td class="editorial-table__name"><h2 class="editorial-object-title">{{ run.name }}</h2><p v-if="run.message" class="editorial-table__meta">{{ run.message }}</p></td>
            <td><span class="status-badge">{{ run.permission_supported === false ? '权限版本不支持，已暂停' : runLabels[run.status] || run.status }}</span></td>
            <td class="whitespace-nowrap">{{ time(run.created_at) }}<p class="text-xs text-muted">{{ run.row_count }} 行</p></td>
            <td class="whitespace-nowrap">{{ time(run.expires_at) }}</td>
            <td><button v-if="run.status === 'succeeded' && run.permission_supported !== false && Date.parse(run.expires_at) > Date.now()" class="editorial-text-action whitespace-nowrap" :data-delivery-id="run.delivery_id" @click="selectDelivery(run.delivery_id)">查看受控快照</button><span v-else-if="run.status === 'succeeded' && Date.parse(run.expires_at) <= Date.now()" class="text-xs text-muted">快照已过期</span></td>
          </tr></tbody>
        </table>
      </div>
      <PageState v-else title="暂无交付记录" description="订阅产生的期次与受控快照在此显示。" />
      <div class="flex flex-wrap justify-between gap-2 text-xs text-muted"><span>共 {{runTotal}} 条</span><div class="flex items-center gap-2"><button class="ghost-button ui-control-target" :disabled="runPage<=1" @click="runPage--;load()">上一页</button><span>{{runPage}}</span><button class="ghost-button ui-control-target" :disabled="runPage*20>=runTotal" @click="runPage++;load()">下一页</button></div></div>
    </template>
    <template v-else-if="listState.status === 'success' && pane === 'team'">
      <div v-if="teamResults.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="团队订阅结果表格">
        <table class="editorial-table" aria-label="团队订阅结果">
          <thead>
            <tr><th scope="col">订阅计划</th><th scope="col">期次</th><th scope="col">交付状态</th><th scope="col">操作</th></tr>
          </thead>
          <tbody>
            <tr v-for="item in teamResults" :key="item.id" :aria-label="`团队订阅计划 ${item.plan_id} 期次 ${item.period_id}`">
              <td class="whitespace-nowrap">计划 #{{ item.plan_id }}</td>
              <td class="whitespace-nowrap">期次 #{{ item.period_id }}</td>
              <td><span class="status-badge">{{ runLabels[item.status] || item.status }}</span></td>
              <td><button v-if="item.status === 'succeeded'" type="button" class="editorial-text-action whitespace-nowrap" :data-team-delivery-id="item.id" @click="selectObject('team_delivery', item.id)">查看本人快照</button></td>
            </tr>
          </tbody>
        </table>
      </div>
      <PageState v-else title="暂无本人团队订阅结果" description="团队计划为你生成的期次会在这里显示。" />
    </template>
    <template v-else-if="listState.status === 'success' && pane==='incidents'">
      <div v-if="incidents.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="指标告警表格">
        <table class="editorial-table" aria-label="指标告警">
          <thead><tr><th scope="col">序号</th><th scope="col">跟踪名称</th><th scope="col">状态</th><th scope="col">观测值与阈值</th><th scope="col">最近观测</th><th scope="col">操作</th></tr></thead>
          <tbody><tr v-for="(item, index) in incidents" :key="item.id" :aria-label="`指标跟踪${item.id}`">
            <td class="editorial-table__number">{{ String((incidentPage - 1) * 20 + index + 1).padStart(2, '0') }}</td>
            <td class="editorial-table__name"><h2 class="editorial-object-title">{{ item.name }}</h2><p class="editorial-table__meta">首次 {{ time(item.opened_at) }}</p></td>
            <td><span class="status-badge">{{ incidentLabels[item.status] }}</span><p v-if="item.status !== 'superseded'" class="text-xs text-muted whitespace-nowrap">{{ item.condition_active ? '仍超阈值' : '指标已恢复' }}</p></td>
            <td class="break-all">{{ item.rule.column }}：{{ item.value }}<p class="text-xs text-muted">{{ operators[item.rule.operator] }} {{ item.rule.threshold }}</p></td>
            <td class="whitespace-nowrap">{{ time(item.last_seen_at) }}</td>
            <td><button class="editorial-text-action whitespace-nowrap" :data-incident-id="item.id" @click="selectIncident(item.id)">查看与跟踪处理</button></td>
          </tr></tbody>
        </table>
      </div>
      <PageState v-else title="当前页无可查看的指标告警" :description="incidentTotal ? '部分历史跟踪因报表版本或授权变化不可查看。' : '达到配置阈值的指标会在此显示。'" />
      <div class="flex flex-wrap justify-between gap-2 text-xs text-muted"><span>共 {{incidentTotal}} 条历史跟踪</span><div class="flex items-center gap-2"><button class="ghost-button ui-control-target" :disabled="incidentPage<=1" @click="incidentPage--;load()">上一页</button><span>{{incidentPage}}</span><button class="ghost-button ui-control-target" :disabled="incidentPage*20>=incidentTotal" @click="incidentPage++;load()">下一页</button></div></div>
    </template>
    </PageCategoryTabs>
    <el-dialog v-model="deliveryOpen" :title="teamDeliveryFocused ? '团队订阅查询快照' : '订阅查询快照'" width="min(1100px,calc(100vw - 24px))" @close="clearFocus('delivery', 'team_delivery')">
      <div v-if="detailError" class="flex flex-wrap items-center gap-2">
        <p role="alert" class="text-sm text-danger">{{ detailError }}</p>
        <button type="button" class="ghost-button ui-control-target" @click="focus">重试读取详情</button>
      </div>
      <div v-else-if="delivery" class="space-y-4">
        <h3 class="font-medium">{{ delivery.snapshot.name }}</h3>
        <p class="text-xs text-muted">历史快照 · 生成于 {{ time(delivery.snapshot.created_at) }} · 发布 #{{ delivery.snapshot.publication_no }}。此处不会重新查询业务库。</p>
        <p class="text-xs text-muted">范围：{{ delivery.snapshot.data_contract?.scope_description || '旧版本未声明范围' }}</p>
        <p class="text-xs text-muted">执行时的数据水位：{{ watermark }}</p>
        <details class="text-xs text-muted">
          <summary>冻结筛选与核验信息</summary>
          <pre class="whitespace-pre-wrap break-all mt-2">{{ JSON.stringify(delivery.snapshot.params, null, 2) }}</pre>
          <p class="break-all">口径校验值：{{ delivery.snapshot.manifest_sha256 }}</p>
        </details>
        <ReportResult :type="delivery.snapshot.chart_type" :config="snapshotConfig" :result="delivery.snapshot.result" :title="delivery.snapshot.name" :unit="delivery.snapshot.data_contract?.unit" :currency="delivery.snapshot.data_contract?.currency" />
        <p v-for="hook in delivery.webhook_deliveries" :key="hook.id" class="text-xs text-muted">外部投递 {{ hook.event_type }}：{{ hookLabels[hook.status] }} · 尝试 {{ hook.attempts }} 次</p>
      </div>
      <LoadingState v-else variant="detail" label="读取快照中…" />
    </el-dialog>
    <el-dialog v-model="incidentOpen" title="指标异常跟踪" width="min(760px,calc(100vw - 24px))" @close="clearFocus('incident')">
      <div v-if="detailError" class="flex flex-wrap items-center gap-2">
        <p role="alert" class="text-sm text-danger">{{ detailError }}</p>
        <button type="button" class="ghost-button ui-control-target" @click="focus">重试读取详情</button>
      </div>
      <div v-else-if="incident" class="space-y-4">
        <h3 class="font-medium">{{ incident.incident.name }}</h3>
        <p class="text-sm">{{ incidentLabels[incident.incident.status] }} · {{ incident.incident.status === 'superseded' ? '原配置已结束跟踪' : incident.incident.condition_active ? '仍超阈值' : incident.incident.status === 'closed' ? '指标已恢复，处理已结束' : '指标已恢复，待核对关闭' }}</p>
        <p class="text-sm">当前值 {{ incident.incident.rule.column }}：{{ incident.incident.value }} · 告警条件：{{ operators[incident.incident.rule.operator] }} {{ incident.incident.rule.threshold }}</p>
        <p class="text-xs text-muted">只有实际观测恢复后才能关闭。更改订阅规则会结束原跟踪，但不会将其标记为业务恢复。</p>
        <template v-if="['open', 'acknowledged'].includes(incident.incident.status)">
          <label class="text-xs block">处理记录<textarea v-model.trim="comment" aria-label="指标异常处理记录" class="glass-input w-full mt-1" rows="3" maxlength="1000" :disabled="busy" /></label>
          <div class="flex flex-wrap gap-2">
            <button v-if="incident.incident.status === 'open'" class="liquid-button text-xs ui-control-target" :disabled="busy || !comment" @click="action('acknowledge')">确认并接手</button>
            <button class="ghost-button text-xs ui-control-target" :disabled="busy || !comment" @click="action('comment')">补充处理记录</button>
            <button class="ghost-button text-xs ui-control-target" :disabled="busy || !comment || incident.incident.condition_active" @click="action('close')">确认恢复并关闭</button>
          </div>
        </template>
        <article v-for="event in incident.events" :key="event.id" class="glass-panel p-3 text-xs space-y-1">
          <p>{{ eventLabels[event.event_type] || event.event_type }} · {{ time(event.created_at) }}</p>
          <p v-if="event.observed_value !== undefined" class="break-words">当次观测值 {{ event.observed_value }}</p>
          <p v-if="event.comment" class="text-muted break-words">{{ event.comment }}</p>
        </article>
      </div>
      <LoadingState v-else variant="detail" label="读取跟踪记录中…" />
    </el-dialog>
  </section>
</template>
