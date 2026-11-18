<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import {featureAllowed} from '../../utils/permission'
import {computed,defineComponent,h,nextTick,onMounted,onScopeDispose,ref,useId,watch} from 'vue'
import {useRoute,useRouter} from 'vue-router'
import {useUserStore} from '../../stores/user'
import {operationApi,type OperationTemplate,type OperationPreview,type OperationRequest} from '../../api/business-operations'
import OperationConfiguration from './OperationConfiguration.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
const operationFieldLabels:Record<string,string>={member_no:'会员编号',points:'积分',version:'记录版本',issued_coupons:'已发券数',new_coupon_expiry:'新券有效期',sku:'SKU编号',on_hand:'在库数量',reserved:'预留数量',shipment_no:'运单编号',stage:'运输状态'}
const operationValue=(key:string,value:unknown)=>value===undefined||value===null?'—':key==='stage'?({planned:'待揽收',picked_up:'已揽收',in_transit:'运输中',delivered:'已签收'}[String(value)]||String(value)):String(value)
const OperationChange=defineComponent({props:{preview:{type:Object as ()=>OperationPreview,required:true}},setup:props=>()=>h('div',{class:'overflow-x-auto'},[h('table',{class:'w-full text-sm text-left', 'aria-label':'变更前后值'},[h('thead',null,h('tr',null,['字段','变更前','变更后'].map(text=>h('th',{class:'p-2 border-b border-stroke'},text)))),h('tbody',null,[...new Set([...Object.keys(props.preview.before),...Object.keys(props.preview.after)])].map(key=>h('tr',null,[operationFieldLabels[key]||key,operationValue(key,props.preview.before[key]),operationValue(key,props.preview.after[key])].map(text=>h('td',{class:'p-2 border-b border-divider break-all'},text)))))] )])})
const user=useUserStore(),route=useRoute(),router=useRouter(),reviewer=computed(()=>featureAllowed(user,'operation.review',user.hasCapability('operation_review'))),sourceManager=computed(()=>featureAllowed(user,'operation.account.manage',user.hasCapability('datasource_manage'))),admin=computed(()=>featureAllowed(user,'operation.template.manage',user.role==='admin')),canApply=computed(()=>featureAllowed(user,'operation.apply',true)),canExecute=computed(()=>featureAllowed(user,'operation.execute',true)),canConfirmReceipt=computed(()=>featureAllowed(user,'operation.reconcile',true))
const templates=ref<OperationTemplate[]>([]),requests=ref<OperationRequest[]>([]),selected=ref<OperationTemplate>(),preview=ref<OperationPreview>(),detail=ref<OperationRequest>(),pane=ref(canApply.value?'new':'mine'),page=ref(1),total=ref(0),busy=ref(false),error=ref(''),message=ref(''),targetKey=ref(''),amount=ref(1),reason=ref(''),comment=ref(''),confirmed=ref(false),executeConfirmed=ref(false),validation=ref(''),executionBlocked=ref(new Set<number>()),now=ref(Date.now())
const templateLoading = ref(false)
const templateError = ref('')
const requestLoading = ref(false)
const requestError = ref('')
const detailId = ref<number>()
const detailLoading = ref(false)
const detailError = ref('')
const operationPage = ref<HTMLElement>()
const previewButton = ref<HTMLButtonElement>()
const previewHeading = ref<HTMLElement>()
const previewError = ref('')
const previewErrorId = useId()
const refreshButton = ref<HTMLButtonElement>()
const templateRetry = ref<HTMLButtonElement>()
const requestRetry = ref<HTMLButtonElement>()
const requestPageSummary = ref<HTMLElement>()
const detailStatus = ref<HTMLElement>()
const detailRetry = ref<HTMLButtonElement>()
const templateKeyword = ref('')
const filteredTemplates = computed(() => {
  const keyword = templateKeyword.value.trim().toLowerCase()
  return templates.value.filter(item => item.spec.name.toLowerCase().includes(keyword))
})
const refreshDisabled = computed(() => busy.value || (pane.value === 'new' ? templateLoading.value : requestLoading.value))
const categories = computed(() => [
  ...(canApply.value ? [{ id: 'new', name: '新建操作' }] : []),
  { id: 'mine', name: '我的申请与回执' },
  ...(reviewer.value ? [{ id: 'review', name: '独立审批' }] : []),
  ...(sourceManager.value || admin.value ? [{ id: 'config', name: '模板与专用账号' }] : []),
])
const names:Record<string,string>={member_points_adjust:'会员积分调整',member_coupon_issue:'会员发券',warehouse_stock_adjust:'仓库库存调整',transport_status_advance:'运输状态推进'},statuses:Record<string,string>={pending:'待独立审批',approved:'已批准，待申请人执行',rejected:'已驳回',executing:'执行中，等待核实',executed:'已执行，回执已保存',stale:'条件已变化，未执行',failed:'执行失败',unknown:'执行结果待核实'}
const time=(value?:string)=>value?new Date(value).toLocaleString():'—',keys=new Map<string,string>()
let alive = true
let templateRead: AbortController | undefined
let requestRead: AbortController | undefined
let detailRead: AbortController | undefined
let validationDeadline = 0
let timer: ReturnType<typeof setInterval> | undefined
let submittedDetailId: number | undefined
let restoreRequestId: number | undefined
const key=(op:string)=>{if(!keys.has(op))keys.set(op,crypto.randomUUID());return keys.get(op)!},canReconcile=computed(()=>detail.value?.status==='unknown'||!!detail.value?.execution_started_at&&now.value-Date.parse(detail.value.execution_started_at)>=15000)
async function loadTemplates() {
  templateRead?.abort()
  const read = new AbortController()
  templateRead = read
  templateLoading.value = true
  templateError.value = ''
  try {
    const catalog = await operationApi.templates(false, read.signal)
    if (read.signal.aborted) return
    templates.value = catalog.items
    if (selected.value && !catalog.items.some(item => item.id === selected.value?.id && item.version === selected.value?.version)) {
      selected.value = undefined
      clearPreview()
    }
  } catch (failure) {
    if (!read.signal.aborted) templateError.value = failure instanceof Error ? failure.message : '读取模板失败'
  } finally {
    if (!read.signal.aborted) templateLoading.value = false
  }
  return !read.signal.aborted
}

async function loadRequests(silent = false) {
  requestRead?.abort()
  const read = new AbortController()
  requestRead = read
  requestLoading.value = !silent
  requestError.value = ''
  try {
    const items = await operationApi.requests(pane.value === 'review', page.value, read.signal)
    if (read.signal.aborted) return
    requests.value = items.items
    total.value = items.total
  } catch (failure) {
    if (read.signal.aborted) return
    requests.value = []
    total.value = 0
    requestError.value = failure instanceof Error ? failure.message : '读取操作记录失败'
  } finally {
    if (requestRead === read) {
      requestRead = undefined
      requestLoading.value = false
    }
  }
  return !read.signal.aborted
}

function refresh() {
  if (pane.value === 'new') return loadTemplates()
  if (pane.value !== 'config') return loadRequests()
}

function refreshFromButton() {
  if (!refreshDisabled.value) void refresh()
}

async function retryPane(event: MouseEvent) {
  const trigger = focusedTrigger(event)
  const currentPane = pane.value
  const applied = await refresh()
  await nextTick()
  if (!applied || pane.value !== currentPane || !canFocusResult(trigger)) return
  const retry = currentPane === 'new' ? templateRetry.value : requestRetry.value
  const result = retry ?? refreshButton.value
  result?.focus()
}

function selectPane(value: string) {
  if (!categories.value.some(item => item.id === value) || pane.value === value) return
  pane.value = value
  page.value = 1
  void refresh()
}

async function changePage(value: number, event: MouseEvent) {
  const trigger = focusedTrigger(event)
  const currentPane = pane.value
  page.value = value
  const applied = await loadRequests()
  await nextTick()
  if (!applied || pane.value !== currentPane || page.value !== value || !canFocusResult(trigger)) return
  const result = requestRetry.value ?? requestPageSummary.value ?? refreshButton.value
  result?.focus()
}
function clearPreview() {
  preview.value = undefined
  previewError.value = ''
  reason.value = ''
}
function choose(item: OperationTemplate) {
  if (selected.value?.id === item.id && selected.value.version === item.version) return
  selected.value = item
  targetKey.value = ''
  amount.value = item.spec.min > 0 ? item.spec.min : 1
  clearPreview()
  message.value = ''
}
async function mutate(action:()=>Promise<void>){if(busy.value)return;busy.value=true;error.value='';message.value='';try{await action()}catch(e){if(alive)error.value=e instanceof Error?e.message:'操作未完成，请刷新核对'}finally{if(alive){busy.value=false;if(pane.value==='mine'||pane.value==='review')await loadRequests(true)}}}
async function previewChange(event: SubmitEvent) {
  const template = selected.value
  if (!template || busy.value || !targetKey.value || !amount.value) return
  const form = event.currentTarget
  const active = document.activeElement
  const trigger = form instanceof HTMLFormElement && active instanceof HTMLElement && form.contains(active) ? active : undefined
  const target = targetKey.value
  const quantity = amount.value
  clearPreview()
  await mutate(async () => {
    try {
      const result = await operationApi.preview(template, target, quantity)
      if (alive) preview.value = result
    } catch (failure) {
      if (alive) previewError.value = failure instanceof Error ? failure.message : '读取变更预览失败，请重新核对业务输入。'
    }
  })
  await nextTick()
  if (pane.value !== 'new' || !canFocusResult(trigger)) return
  const result = previewHeading.value ?? previewButton.value
  result?.focus()
}
async function submit() {
  const previewId = preview.value?.id
  if (!previewId) return
  await mutate(async () => {
    const item = await operationApi.submit(previewId, reason.value, key('submit:' + previewId))
    if (!alive) return
    clearPreview()
    selectPane('mine')
    submittedDetailId = item.id
    await open(item.id, true)
  })
}

async function open(id: number, writeLocation = false) {
  detailRead?.abort()
  const read = new AbortController()
  detailRead = read
  detailId.value = id
  detail.value = undefined
  detailError.value = ''
  detailLoading.value = true
  validation.value = ''
  confirmed.value = false
  executeConfirmed.value = false
  comment.value = ''
  if (writeLocation) {
    error.value = ''
    message.value = ''
    void router.replace({ query: { ...route.query, request: String(id) } })
  }
  try {
    const item = await operationApi.get(id, read.signal)
    if (!read.signal.aborted) detail.value = item
  } catch (failure) {
    if (!read.signal.aborted) detailError.value = failure instanceof Error ? failure.message : '无法读取操作'
  } finally {
    if (!read.signal.aborted) detailLoading.value = false
  }
}

function clearDetail() {
  detailRead?.abort()
  detailId.value = undefined
  detail.value = undefined
  detailLoading.value = false
  detailError.value = ''
  submittedDetailId = undefined
}

function closeDetail(done: () => void) {
  if (busy.value) return
  if (detailId.value === submittedDetailId) restoreRequestId = submittedDetailId
  clearDetail()
  const query = { ...route.query }
  delete query.request
  void router.replace({ query })
  done()
}

async function restoreSubmittedRequestFocus() {
  const id = restoreRequestId
  restoreRequestId = undefined
  if (!id) return
  await nextTick()
  if (detailId.value || document.activeElement !== document.body) return
  const rowButton = operationPage.value?.querySelector<HTMLButtonElement>(`[data-operation-request-id="${id}"]`)
  const selectedTab = operationPage.value?.querySelector<HTMLButtonElement>('[role="tab"][aria-selected="true"]')
  const target = rowButton ?? selectedTab
  target?.focus()
}

function focusedTrigger(event: MouseEvent) {
  const trigger = event.currentTarget
  return trigger instanceof HTMLElement && trigger === document.activeElement ? trigger : undefined
}

function canFocusResult(trigger?: HTMLElement) {
  const active = document.activeElement
  return !!trigger && (active === trigger || active === document.body)
}

async function focusDetailResult(id: number, trigger?: HTMLElement) {
  if (!trigger) return
  await nextTick()
  if (detailId.value !== id || !canFocusResult(trigger)) return
  const target = detailStatus.value ?? detailRetry.value
  target?.focus()
}

async function refreshDetail(event: MouseEvent) {
  const id = detailId.value
  if (id === undefined) return
  const trigger = focusedTrigger(event)
  await open(id)
  await focusDetailResult(id, trigger)
}
async function validate() {
  const item = detail.value
  if (!item) return
  await mutate(async () => {
    const result = await operationApi.validate(item)
    if (alive && detail.value === item) {
      validation.value = result.validation_id
      validationDeadline = Date.now() + result.expires_in_seconds * 1000
    }
  })
}
async function decide(decision: 'approved' | 'rejected', event: MouseEvent) {
  const item = detail.value
  if (!item) return
  if (decision === 'approved' && Date.now() >= validationDeadline) {
    validation.value = ''
    error.value = '验证已过期，请重新核验当前业务记录'
    return
  }
  const trigger = focusedTrigger(event)
  await mutate(async () => {
    await operationApi.decide(item, decision, validation.value, comment.value, confirmed.value, key(`decide:${item.id}:${item.version}:${decision}`))
    if (!alive || detail.value !== item) return
    const restore = canFocusResult(trigger)
    await open(item.id)
    if (restore) await focusDetailResult(item.id, trigger)
  })
}

async function execute(event: MouseEvent) {
  const item = detail.value
  if (!item || busy.value) return
  const trigger = focusedTrigger(event)
  executionBlocked.value.add(item.id)
  await mutate(async () => {
    try {
      const result = await operationApi.execute(item)
      if (!alive || detail.value !== item) return
      detail.value = result
    } catch (failure) {
      if (alive && detail.value === item) await open(item.id)
      throw failure
    } finally {
      if (alive) await focusDetailResult(item.id, trigger)
    }
  })
}

async function reconcile(event: MouseEvent) {
  const item = detail.value
  if (!item) return
  const trigger = focusedTrigger(event)
  await mutate(async () => {
    const result = await operationApi.reconcile(item.id)
    if (!alive || detail.value !== item) return
    detail.value = result
    if (result.status === 'unknown') message.value = '尚未查到可核实的目标库回执，保留待核实状态，不会重发写操作。'
    await focusDetailResult(item.id, trigger)
  })
}
watch(() => route.query.request, value => {
  const id = typeof value === 'string' ? Number(value) : 0
  if (!Number.isSafeInteger(id) || id <= 0) clearDetail()
  else if (id !== detailId.value) void open(id)
}, { immediate: true })
watch(categories, items => {
  if (!items.some(item => item.id === pane.value)) selectPane('mine')
})
onMounted(() => {
  if (detailId.value) pane.value = 'mine'
  void refresh()
  timer = setInterval(() => {
    now.value = Date.now()
    if (validation.value && now.value >= validationDeadline) validation.value = ''
    if ((pane.value === 'mine' || pane.value === 'review') && !busy.value && !requestRead && !requestError.value && !document.hidden && navigator.onLine) {
      void loadRequests(true)
    }
  }, 15000)
})
onScopeDispose(() => {
  alive = false
  templateRead?.abort()
  requestRead?.abort()
  detailRead?.abort()
  clearInterval(timer)
})
</script>

<template>
  <section ref="operationPage" class="ui-page-stack" aria-label="受控业务操作">
    <PageHeader title="受控业务操作" description="选择模板 → 核对变更预览 → 独立审批 → 执行并保存回执。对象由数据库按本人分配范围校验。">
      <template #actions>
        <button v-if="pane !== 'config'" ref="refreshButton" class="ghost-button ui-control-target aria-disabled:opacity-50" :aria-disabled="refreshDisabled || undefined" @click="refreshFromButton">
          {{ pane === 'new' ? '刷新模板' : '刷新操作记录' }}
        </button>
      </template>
    </PageHeader>
    <p v-if="error" role="alert" class="text-sm text-danger">{{error}}</p><p v-if="message" role="status" class="text-sm text-success">{{message}}</p>
    <PageCategoryTabs label="业务操作分类" :categories="categories" :active-id="pane" @select="selectPane">
    <template v-if="pane === 'new'">
      <LoadingState v-if="templateLoading" label="正在读取操作模板…" />
      <PageState v-else-if="templateError" title="读取操作模板失败" :description="templateError" tone="danger">
        <template #actions><button ref="templateRetry" class="ghost-button ui-control-target" @click="retryPane">重试读取模板</button></template>
      </PageState>
      <PageState v-else-if="!templates.length" title="暂无获准操作模板" description="请联系管理员配置受众及专用执行账号。" />
      <div v-else class="grid gap-4 md:grid-cols-[minmax(12rem,1fr)_minmax(0,3fr)]">
      <section class="glass-panel ui-card-padding space-y-3 self-start" aria-labelledby="operation-catalog-title">
        <h2 id="operation-catalog-title" class="font-semibold">获准操作目录</h2>
        <label class="block text-sm">搜索操作名称<input v-model.trim="templateKeyword" class="glass-input block w-full mt-1" /></label>
        <ul class="space-y-1">
          <li v-for="item in filteredTemplates" :key="item.id"><button class="ghost-button ui-control-target w-full text-left break-words" :class="{ 'bg-accent-soft text-accent': selected?.id === item.id }" :aria-pressed="selected?.id === item.id" :disabled="busy" @click="choose(item)">{{ item.spec.name }}</button></li>
        </ul>
        <p v-if="!filteredTemplates.length" class="text-sm text-muted">没有匹配的操作模板。</p>
      </section>
      <form v-if="selected" class="glass-panel min-w-0 space-y-4 ui-card-padding" aria-label="业务操作申请" @submit.prevent="previewChange">
        <h2 class="font-semibold break-words">{{ selected.spec.name }}</h2><p class="text-sm text-muted">模板版本 {{ selected.version }} · {{ selected.spec.description }}</p>
        <div class="grid sm:grid-cols-2 gap-3"><label class="text-sm">{{selected.spec.target_label}}<input v-model.trim="targetKey" aria-label="操作对象编号" required maxlength="80" pattern="[A-Za-z0-9_-]+" class="glass-input w-full block mt-1" :disabled="busy" @input="clearPreview" /></label><label class="text-sm">{{selected.spec.amount_label}}<input v-model.number="amount" aria-label="操作数量" required type="number" step="1" :min="selected.spec.min" :max="selected.spec.max" :disabled="busy" class="glass-input w-full block mt-1" @input="clearPreview" /></label></div>
        <button ref="previewButton" type="submit" class="liquid-button ui-control-target aria-disabled:opacity-50" :disabled="!targetKey || !amount" :aria-disabled="busy || undefined" :aria-describedby="previewError ? previewErrorId : undefined">生成变更预览</button>
        <p v-if="previewError" :id="previewErrorId" class="text-sm text-danger" role="alert">{{ previewError }}</p>
        <div v-if="preview" class="space-y-3">
          <h3 ref="previewHeading" class="font-medium ui-focus-target" tabindex="-1">变更预览</h3>
          <OperationChange :preview="preview" />
          <p class="text-xs text-muted">预览有效至 {{time(preview.expires_at)}}；提交后冻结变更前后值，30分钟内审批及执行。记录变化后必须重新预览。</p>
          <label class="text-sm block">业务原因<textarea v-model.trim="reason" aria-label="操作业务原因" aria-required="true" maxlength="1000" class="glass-input block w-full mt-1" :disabled="busy" /></label>
          <button type="button" class="liquid-button ui-control-target" :disabled="busy||!reason||!preview.id" @click="submit">提交独立审批</button>
        </div>
      </form>
      <PageState v-else title="选择操作模板" description="选择获准的操作，填写对象与变更值后生成预览。" />
      </div>
    </template>
    <template v-else-if="pane === 'mine' || pane === 'review'">
      <LoadingState v-if="requestLoading" label="正在读取操作记录…" />
      <PageState v-else-if="requestError" title="读取操作记录失败" :description="requestError" tone="danger">
        <template #actions><button ref="requestRetry" class="ghost-button ui-control-target" @click="retryPane">重试读取记录</button></template>
      </PageState>
      <PageState v-else-if="!total" title="暂无申请记录" description="当前范围内没有可见的业务操作申请。" />
      <div v-else class="space-y-3">
        <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="业务操作申请表格">
          <table class="editorial-table min-w-[570px]" aria-label="业务操作申请记录">
            <thead><tr><th scope="col">申请编号</th><th scope="col">操作</th><th scope="col">申请人</th><th scope="col">申请时间</th><th scope="col">状态</th></tr></thead>
            <tbody>
              <tr v-for="item in requests" :key="item.id">
                <td><button class="ghost-button ui-control-target font-mono" :aria-label="'查看操作 #' + item.id" :data-operation-request-id="item.id" :disabled="busy" @click="open(item.id, true)">#{{ item.id }}</button></td>
                <td>{{ names[item.kind] || item.kind }}</td><td>{{ item.user_name }}</td><td>{{ time(item.created_at) }}</td><td>{{ statuses[item.status] }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="!requests.length" class="text-sm text-muted">此页没有记录，可返回上一页。</p>
        <div class="flex flex-wrap gap-2 justify-between items-center text-sm text-muted">
          <span ref="requestPageSummary" class="ui-focus-target" tabindex="-1" aria-live="polite">共 {{ total }} 项 · 第 {{ page }} 页</span>
          <div class="flex gap-2"><button class="ghost-button ui-control-target" :disabled="page <= 1 || busy" @click="changePage(page - 1, $event)">上一页</button><button class="ghost-button ui-control-target" :disabled="page * 20 >= total || busy" @click="changePage(page + 1, $event)">下一页</button></div>
        </div>
      </div>
    </template>
    <OperationConfiguration v-else-if="pane === 'config' && (sourceManager || admin)" :source-manager="sourceManager" :admin="admin" />
    </PageCategoryTabs>
    <el-dialog :model-value="!!detailId" title="业务操作审阅与回执" width="min(850px,94vw)" append-to-body :close-on-click-modal="false" :before-close="closeDetail" @closed="restoreSubmittedRequestFocus">
      <LoadingState v-if="detailLoading" label="正在读取业务操作详情…" variant="detail" />
      <PageState v-else-if="detailError && detailId" title="读取操作详情失败" :description="detailError" tone="danger">
        <template #actions><button ref="detailRetry" class="ghost-button ui-control-target" @click="refreshDetail">重试读取详情</button></template>
      </PageState>
      <div v-else-if="detail" class="space-y-4"><p v-if="error" role="alert" class="text-sm text-danger">{{error}}</p><p v-if="message" role="status" class="text-sm text-success">{{message}}</p>
        <div class="flex flex-wrap justify-between gap-2">
          <h3 class="font-medium">{{ names[detail.kind] || detail.kind }} #{{ detail.id }}</h3>
          <span ref="detailStatus" role="status" tabindex="-1" class="ui-focus-target">{{ statuses[detail.status] }}</span>
        </div>
        <p class="text-sm text-muted">申请人 {{detail.user_name}} · {{time(detail.created_at)}} · 审批截止 {{time(detail.expires_at)}}</p><OperationChange :preview="detail.preview" /><p class="text-sm whitespace-pre-wrap break-words">业务原因：{{detail.reason}}</p><p v-if="detail.review_comment" class="text-sm whitespace-pre-wrap break-words">审批意见：{{detail.review_comment}}</p>
        <template v-if="reviewer && detail.status === 'pending' && detail.user_id !== user.user?.id">
          <p class="text-xs text-muted">重新读取目标库核对冻结预览后，验证在60秒内有效。执行时仍会再次核对版本。</p>
          <button class="ghost-button ui-control-target" :disabled="busy" @click="validate">核验当前业务记录</button>
          <p v-if="validation" role="status" class="text-sm text-success">当前记录与冻结预览一致</p>
          <label class="block text-sm">审批意见<textarea v-model.trim="comment" aria-label="业务操作审批意见" maxlength="1000" class="glass-input w-full block mt-1" :disabled="busy" /></label>
          <label class="flex gap-2 text-sm"><input v-model="confirmed" type="checkbox" :disabled="busy || !validation" />已核对申请范围、业务原因和变更前后值</label>
          <div class="flex flex-wrap gap-2">
            <button class="liquid-button ui-control-target" :disabled="busy || !validation || !confirmed || !comment" @click="decide('approved', $event)">批准本次操作</button>
            <button class="ghost-button ui-control-target" :disabled="busy || !comment" @click="decide('rejected', $event)">驳回申请</button>
          </div>
        </template>
        <template v-if="canExecute&&detail.user_id===user.user?.id&&detail.status==='approved'"><label class="flex gap-2 text-sm"><input v-model="executeConfirmed" type="checkbox" :disabled="busy" />确认按已审批的预览执行本次操作</label><button class="liquid-button ui-control-target" :disabled="busy||!executeConfirmed||executionBlocked.has(detail.id)" @click="execute">执行已批准操作</button></template>
        <template v-if="detail.status==='unknown'||detail.status==='executing'"><p role="alert" class="text-sm text-warning">执行结果尚未核实，请勿重复提交同一业务变更。核实只读取目标库回执；未查到回执也不能证明旧请求已回滚。</p><button v-if="canConfirmReceipt&&detail.user_id===user.user?.id" class="ghost-button ui-control-target" :disabled="busy||!canReconcile" @click="reconcile">核实目标库执行回执</button><p v-if="detail.status==='executing'&&!canReconcile" class="text-xs text-muted">执行正在处理，15秒后可核实回执。</p></template>
        <div v-if="detail.receipt" class="glass-panel p-4 space-y-2" role="region" aria-label="已核实执行回执"><h4 class="font-medium text-success">目标库回执已核实</h4><p class="text-sm">执行时间 {{time(detail.receipt.executed_at)}} · 审批人 #{{detail.receipt.reviewer_id}} · 影响 {{detail.receipt.affected_rows}} 条业务记录</p><details class="text-xs text-muted"><summary class="cursor-pointer min-h-9 flex items-center">回执校验信息</summary><p class="break-all">执行编号 {{detail.operation_id}}</p><p class="break-all mt-2">SHA-256 {{detail.receipt_sha256}}</p></details></div>
        <p v-if="detail.status==='stale'" role="alert" class="text-sm text-warning">目标记录、授权或业务条件发生变化，本次未执行。需要重新预览并独立审批。</p><button class="ghost-button ui-control-target" :disabled="busy" @click="refreshDetail">刷新此操作状态</button>
      </div>
    </el-dialog>
  </section>
</template>
