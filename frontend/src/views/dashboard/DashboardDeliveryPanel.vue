<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref, watch, watchEffect } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  dashboardDeliveryApi as api,
  type DashboardDelivery,
  type DashboardDeliveryReceipt,
  type DashboardMetricEvent,
} from '../../api/dashboard-delivery'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import { useUserStore } from '../../stores/user'
import { ApiRequestError } from '../../utils/request'
import ResultGrid from '../query/components/ResultGrid.vue'
import { reportCommandKeys } from '../report/reportCommands'
import { useReportRun } from '../report/useReportRun'

const route = useRoute()
const router = useRouter()
const user = useUserStore()
const list = useReportRun<DashboardDeliveryReceipt[]>()
const detail = useReportRun<DashboardDelivery>()
const alerts = useReportRun<DashboardMetricEvent[]>()
const keys = reportCommandKeys()
const heading = ref<HTMLElement>()
const directory = ref<HTMLElement>()
const alertHeading = ref<HTMLElement>()
const now = ref(Date.now())
const expiredId = ref<number>()
const showAlerts = ref(false)
const alertEvent = ref<number>()
const note = ref('')
const commandError = ref('')
const busy = ref(false)
let command: AbortController | undefined

const kinds = { export: '导出文件', personal: '个人订阅', team: '团队订阅' }
const statuses: Record<string, string> = {
  queued: '已排队', running: '生成中', succeeded: '已生成', failed: '生成失败', skipped: '已跳过',
}
const focusId = computed(() => {
  const raw = route.query.delivery
  const id = typeof raw === 'string' && /^\d+$/.test(raw) ? Number(raw) : 0
  return Number.isSafeInteger(id) && id > 0 ? id : undefined
})
const receipts = computed(() => list.state.value.status === 'success' ? list.state.value.data : [])
const receipt = computed(() => receipts.value.find(item => item.id === focusId.value))
const unavailable = computed(() => receipt.value && receipt.value.status !== 'succeeded' ? receipt.value.status : undefined)
const output = computed(() => detail.state.value.status === 'success' ? detail.state.value.data : undefined)
const alertPlan = computed(() => receipt.value?.kind === 'personal' ? receipt.value.plan_id : undefined)
const events = computed(() => alerts.state.value.status === 'success' ? alerts.state.value.data : [])
const expired = computed(() => focusId.value !== undefined && (expiredId.value === focusId.value ||
  isExpired(output.value?.expires_at ?? receipt.value?.expires_at)))

const time = (value: string) => new Date(value).toLocaleString('zh-CN')
function isExpired(expiresAt?: string, at = now.value) {
  return expiresAt !== undefined && Date.parse(expiresAt) <= at
}
function readError(error: unknown, fallback: string) {
  if (error instanceof ApiRequestError && [403, 404, 409, 410].includes(error.status ?? 0)) {
    return '此记录当前不可用。交付可能已到期、权限已撤回或发布范围已发生变化。'
  }
  return error instanceof Error ? error.message : fallback
}

function loadList() {
  return list.run('own', async signal => (await api.list(signal)).items)
}
function loadDetail(id = focusId.value) {
  if (!id || expired.value) return
  return detail.run(String(id), async signal => {
    try { return await api.get(id, signal) }
    catch (error) { throw new Error(readError(error, '读取交付失败，请重试。')) }
  })
}
function loadAlerts(plan = alertPlan.value) {
  if (!plan) return
  return alerts.run(String(plan), async signal => (await api.alerts(plan, signal)).items ?? [])
}
async function refreshDetail() {
  await loadList()
  void loadDetail()
}

function open(id: number) {
  return router.push({ query: { ...route.query, tab: 'delivery', delivery: String(id) }, hash: route.hash })
}
function close() {
  const query = { ...route.query }
  delete query.delivery
  return router.replace({ query, hash: route.hash })
}

function startCommand() {
  const current = new AbortController()
  command = current
  busy.value = true
  commandError.value = ''
  return current
}
function cancelCommand() {
  command?.abort()
  command = undefined
  busy.value = false
}

async function download() {
  const id = focusId.value
  const item = output.value
  const format = item?.format
  if (!id || item?.kind !== 'export' || busy.value || !format || isExpired(item.expires_at, Date.now())) return
  const current = startCommand()
  try {
    const grant = await api.credential(id, current.signal)
    if (current.signal.aborted) return
    const blob = await api.redeem(id, grant.credential, current.signal)
    if (current.signal.aborted || isExpired(item.expires_at, Date.now())) return
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `dashboard-delivery-${id}.${format}`
    link.click()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (error) {
    if (!current.signal.aborted) {
      commandError.value = readError(error, '领取失败，请重新核对交付状态。')
      void loadDetail(id)
    }
  } finally {
    if (command === current) cancelCommand()
  }
}

async function openAlerts() {
  showAlerts.value = true
  void loadAlerts()
  await nextTick()
  alertHeading.value?.focus()
}
async function saveDisposition(event: number) {
  const plan = alertPlan.value
  const text = note.value.trim()
  if (!plan || busy.value || !text) return
  const current = startCommand()
  try {
    await api.disposeAlert(plan, event, text, keys.get('dashboard-alert-disposition', [plan, event, text]), current.signal)
    if (!current.signal.aborted) {
      alertEvent.value = undefined
      note.value = ''
      await loadAlerts(plan)
      if (!current.signal.aborted) alertHeading.value?.focus()
    }
  } catch (error) {
    if (!current.signal.aborted) {
      commandError.value = readError(error, '保存失败，请重新核对告警状态。')
      void loadAlerts(plan)
    }
  } finally {
    if (command === current) cancelCommand()
  }
}

watchEffect(onCleanup => {
  let next = Infinity
  for (const value of [output.value, ...receipts.value]) {
    const at = Date.parse(value?.expires_at ?? '')
    if (at > now.value && at < next) next = at
  }
  if (next !== Infinity) {
    const timer = window.setTimeout(() => { now.value = Date.now() }, Math.min(next - Date.now(), 2_147_483_647))
    onCleanup(() => window.clearTimeout(timer))
  }
})

watch(expired, value => {
  if (!value) return
  expiredId.value = focusId.value
  if (output.value?.kind === 'export') cancelCommand()
  detail.reset()
})

function resetDetail() {
  cancelCommand()
  expiredId.value = undefined
  showAlerts.value = false
  alertEvent.value = undefined
  note.value = ''
  commandError.value = ''
  detail.reset()
  alerts.reset()
  keys.clear()
}

watch(focusId, async (id, previous) => {
  resetDetail()
  if (id) void loadDetail(id)
  await nextTick()
  if (focusId.value !== id) return
  if (id) heading.value?.focus()
  else if (previous) {
    const trigger = directory.value?.querySelector<HTMLElement>(`[data-delivery="${previous}"]`)
    const target = trigger ?? directory.value
    target?.focus()
  }
}, { flush: 'sync' })

watch([() => user.user?.id, () => user.permissionKey], () => {
  list.reset()
  resetDetail()
  if (!user.workspaceId) return
  void loadList()
  if (focusId.value) void loadDetail()
}, { immediate: true, flush: 'sync' })
onMounted(() => { if (focusId.value) heading.value?.focus() })
onScopeDispose(cancelCommand)
</script>

<template>
  <section v-if="focusId !== undefined" class="ui-page-stack" aria-label="仪表盘交付详情">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 ref="heading" tabindex="-1" class="ui-focus-target font-semibold">仪表盘交付 #{{ focusId }}</h2>
      <button type="button" class="ghost-button ui-control-target" @click="close">返回交付列表</button>
    </div>
    <p v-if="commandError" role="alert" class="text-sm text-danger">{{ commandError }}</p>
    <PageState v-if="expired" title="交付已到期" description="此期次的文件或快照已停止领取。" />
    <PageState v-else-if="unavailable && !output" :title="statuses[unavailable] ?? '交付不可用'" description="此期次没有可领取的文件或快照。">
      <template #actions><button type="button" class="ghost-button ui-control-target" :disabled="list.state.value.status === 'loading'"
        @click="refreshDetail">刷新交付状态</button></template>
    </PageState>
    <LoadingState v-else-if="detail.state.value.status === 'loading'" label="正在核对交付…" variant="detail" />
    <PageState v-else-if="detail.state.value.status === 'error'" title="无法打开此期次"
      :description="detail.state.value.message ?? '读取交付失败，请重试。'" tone="warning">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="loadDetail()">重试核对交付</button></template>
    </PageState>
    <template v-else-if="output">
      <section class="glass-panel ui-card-padding space-y-2" aria-label="交付信息">
        <p>{{ kinds[output.kind] }} · {{ output.tile_key ? `指定卡片 ${output.tile_key}` : '整板' }}</p>
        <p class="text-sm text-muted">仪表盘 #{{ output.dashboard_id }} · 发布 #{{ output.publication_no }}</p>
        <p class="text-sm text-muted">有效至 <time :datetime="output.expires_at">{{ time(output.expires_at) }}</time></p>
        <button v-if="output.kind === 'export' && output.format" type="button" class="liquid-button ui-control-target"
          :disabled="busy" @click="download">{{ busy ? '正在领取…' : '领取文件' }}</button>
      </section>
      <section v-for="card in output.snapshot?.cards ?? []" :key="card.tile_key" class="glass-panel ui-card-padding space-y-2">
        <h3 class="font-semibold break-words">{{ card.snapshot?.name || `卡片 ${card.tile_key}` }}</h3>
        <template v-if="card.status === 'ready' && card.snapshot">
          <p class="text-sm text-muted">{{ card.quality?.data_as_of ? '数据时间' : '快照时间' }}
            {{ time(card.quality?.data_as_of ?? card.snapshot.created_at) }}</p>
          <ResultGrid :columns="card.snapshot.result.columns ?? []" :rows="card.snapshot.result.rows ?? []" />
        </template>
        <p v-else class="text-sm text-muted">{{ card.status === 'restricted' ? '可选卡片受限' : '卡片在此期次不可用' }}</p>
      </section>
    </template>

    <button v-if="alertPlan && !showAlerts" type="button" class="ghost-button ui-control-target" @click="openAlerts">查看指标告警</button>
    <section v-if="alertPlan && showAlerts" class="glass-panel ui-card-padding space-y-3" aria-label="指标卡告警记录">
      <h3 ref="alertHeading" tabindex="-1" class="ui-focus-target font-semibold">指标卡告警记录</h3>
      <LoadingState v-if="alerts.state.value.status === 'loading'" label="正在读取指标告警…" variant="inline" />
      <PageState v-else-if="alerts.state.value.status === 'error'" title="无法读取指标告警" heading="h3"
        :description="alerts.state.value.message ?? '请重新检查告警记录。'" tone="warning">
        <template #actions><button type="button" class="ghost-button ui-control-target" @click="loadAlerts()">重试读取告警</button></template>
      </PageState>
      <p v-else-if="!events.length" class="text-sm text-muted">暂无当前可查看的告警记录。</p>
      <article v-for="event in events" :key="event.id" class="space-y-2 border-b border-stroke pb-3 last:border-b-0">
        <p class="font-medium">{{ event.active ? '触发' : '恢复' }} · <time :datetime="event.observed_at">{{ time(event.observed_at) }}</time></p>
        <p v-if="event.observed_value !== undefined" class="text-sm">原观测值 {{ event.observed_value }}</p>
        <p v-if="event.disposition" class="text-sm break-words">处置记录 {{ event.disposition }}
          <time v-if="event.disposed_at" :datetime="event.disposed_at" class="text-muted">{{ time(event.disposed_at) }}</time></p>
        <form v-else-if="alertEvent === event.id" class="space-y-2" @submit.prevent="saveDisposition(event.id)">
          <label class="block text-sm">处置说明
            <textarea v-model="note" class="glass-input block w-full mt-1" rows="2" maxlength="500" :disabled="busy" required />
          </label>
          <div class="flex flex-wrap gap-2">
            <button type="submit" class="liquid-button ui-control-target" :disabled="busy || !note.trim()">保存处置记录</button>
            <button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="alertEvent = undefined; note = ''">取消</button>
          </div>
        </form>
        <button v-else type="button" class="ghost-button ui-control-target" :disabled="busy"
          @click="alertEvent = event.id; note = ''">记录处置</button>
      </article>
    </section>
  </section>

  <section v-else ref="directory" tabindex="-1" class="ui-focus-target ui-page-stack" aria-label="我的仪表盘交付">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 class="font-semibold">我的仪表盘交付</h2>
      <button type="button" class="ghost-button ui-control-target" :disabled="list.state.value.status === 'loading'" @click="loadList">刷新交付列表</button>
    </div>
    <LoadingState v-if="list.state.value.status === 'loading'" label="正在读取交付列表…" />
    <PageState v-else-if="list.state.value.status === 'error'" title="读取交付列表失败"
      :description="list.state.value.message ?? '请重新读取交付列表。'" tone="danger">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="loadList">重试</button></template>
    </PageState>
    <PageState v-else-if="!receipts.length" title="暂无本人交付" description="已为你生成的文件和订阅期次会显示在这里。" />
    <div v-else class="editorial-table-wrap" role="region" tabindex="0" aria-label="仪表盘交付列表">
      <table class="editorial-table min-w-[520px]" aria-label="我的仪表盘交付记录">
        <thead><tr><th scope="col">交付</th><th scope="col">期次</th><th scope="col">状态</th><th scope="col">有效至</th><th scope="col">操作</th></tr></thead>
        <tbody>
          <tr v-for="item in receipts" :key="item.id">
            <td>{{ kinds[item.kind] }} #{{ item.id }}<p class="editorial-table__meta">仪表盘 #{{ item.dashboard_id }}</p></td>
            <td><time :datetime="item.slot">{{ time(item.slot) }}</time></td>
            <td>{{ isExpired(item.expires_at) ? '已到期' : statuses[item.status] ?? item.status }}</td>
            <td><time v-if="item.expires_at" :datetime="item.expires_at">{{ time(item.expires_at) }}</time><span v-else>—</span></td>
            <td><button type="button" class="ghost-button ui-control-target whitespace-nowrap" :data-delivery="item.id"
              :aria-label="`查看交付 #${item.id}`" @click="open(item.id)">查看</button></td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
