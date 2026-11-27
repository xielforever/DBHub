<script setup lang="ts">
import { computed, nextTick, onScopeDispose, ref, watch, watchEffect } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import { reportExportApi, type ReportExport } from '../../api/report-exports'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import { revealTableAction, scrollTableByKeyboard } from '../../utils/table-focus'
import ResultGrid from '../query/components/ResultGrid.vue'
import { useReportRun } from './useReportRun'

const route = useRoute(), router = useRouter()
defineProps<{ canRequest: boolean }>()
const list = useReportRun<Awaited<ReturnType<typeof reportExportApi.list>>>()
const detail = useReportRun<ReportExport>()
const preview = useReportRun<Awaited<ReturnType<typeof reportExportApi.preview>>>()
const page = ref(1), busy = ref(false), previewOpen = ref(false), comment = ref('')
const commandError = ref(''), receipt = ref(''), detailHeading = ref<HTMLElement>(), directory = ref<HTMLElement>()
const previewHeading = ref<HTMLElement>(), receiptTarget = ref<HTMLElement>(), errorTarget = ref<HTMLElement>()
const previewButton = ref<HTMLButtonElement>(), previewRetry = ref<HTMLButtonElement>(), detailRetry = ref<HTMLButtonElement>()
const downloadButton = ref<HTMLButtonElement>()
const now = ref(Date.now())
const retryingPreview = ref(false), retryingDetail = ref(false)
let previewOwner: number | undefined
let previewReturn: 'opener' | 'receipt' | 'error' = 'opener'
const focusId = computed(() => {
  const value = route.query.export
  const id = typeof value === 'string' && /^\d+$/.test(value) ? Number(value) : 0
  return Number.isSafeInteger(id) && id > 0 ? id : undefined
})
const listed = computed(() => list.state.value.status === 'success' ? list.state.value.data : undefined)
const record = computed(() => detail.state.value.status === 'success' ? detail.state.value.data : undefined)
const snapshot = computed(() => preview.state.value.status === 'success' ? preview.state.value.data : undefined)
const time = (value: string) => new Date(value).toLocaleString('zh-CN')
const message = (e: unknown, fallback: string) => e instanceof Error ? e.message : fallback
let alive = true

function eligible(item: ReportExport, at = now.value) {
  return item.permission_supported !== false && Date.parse(item.expires_at) > at
}
function pickupAvailable(item: ReportExport, at = now.value) {
  return item.permission_supported !== false && item.generation_status === 'succeeded'
    && !!item.file_expires_at && Date.parse(item.file_expires_at) > at
}
function status(item: ReportExport) {
  if (item.permission_supported === false) return '权限版本不支持，暂不交付'
  if (item.generation_status === 'succeeded') {
    return item.file_expires_at && !pickupAvailable(item) ? '正式文件已到期' : '正式文件已生成'
  }
  if (Date.parse(item.expires_at) <= now.value) return '已过期'
  if (item.generation_status === 'queued') return '已批准 · 待生成'
  if (item.generation_status === 'running') return '已批准 · 生成中'
  if (item.generation_status === 'failed') return '正式文件生成失败'
  if (item.generation_status === 'interrupted') return '正式文件生成中断'
  return { pending: '待独立审批', approved: '已批准', rejected: '已退回' }[item.status]
}

function readClock() { now.value = Date.now() }

watchEffect(onCleanup => {
  const items = [...(listed.value?.items ?? []), record.value, snapshot.value?.export]
  let nextExpiry = Infinity
  for (const item of items) {
    if (!item) continue
    for (const deadline of [item.expires_at, item.file_expires_at]) {
      if (!deadline) continue
      const expiry = Date.parse(deadline)
      if (expiry > now.value && expiry < nextExpiry) nextExpiry = expiry
    }
  }
  if (nextExpiry === Infinity) return
  const timer = window.setTimeout(readClock, Math.min(nextExpiry - Date.now(), 2_147_483_647))
  onCleanup(() => window.clearTimeout(timer))
})

const detailExpired = computed(() => {
  const item = snapshot.value?.export ?? record.value
  return !!item && Date.parse(item.expires_at) <= now.value
})
watch(detailExpired, async expired => {
  if (!expired) return
  if (previewOpen.value) {
    previewOpen.value = false
    return
  }
  const focused = document.activeElement
  if (focused !== previewButton.value && focused !== downloadButton.value) return
  await nextTick()
  if (detailExpired.value && document.activeElement === document.body) detailHeading.value?.focus()
})
watch(() => !!record.value && pickupAvailable(record.value), async (available, previous) => {
  if (available || !previous || document.activeElement !== downloadButton.value) return
  await nextTick()
  if (document.activeElement === document.body) detailHeading.value?.focus()
})
async function load(next = page.value) {
  page.value = next
  const loaded = await list.run(String(next), signal => reportExportApi.list(next, signal))
  if (loaded && listed.value && !listed.value.items.length) {
    const last = Math.max(1, Math.ceil(listed.value.total / 20))
    if (next > last) await load(last)
  }
}
function loadDetail(id = focusId.value) {
  if (id) return detail.run(String(id), signal => reportExportApi.get(id, signal))
}
function refresh() { void load(); void loadDetail() }
function select(id: number) {
  if (id === focusId.value) { detailHeading.value?.focus(); return }
  return router.push({ query: { ...route.query, view: 'exports', export: String(id) }, hash: route.hash })
}
function closeDetail() {
  const query = { ...route.query }
  delete query.export
  return router.replace({ query, hash: route.hash })
}
function loadPreview() {
  if (record.value && eligible(record.value, Date.now()) && (record.value.can_review || record.value.can_download)) {
    const id = record.value.id
    return preview.run(String(id), signal => reportExportApi.preview(id, signal))
  }
}
async function retryPreview() {
  if (retryingPreview.value || !previewOpen.value) return
  const trigger = previewRetry.value
  const restore = document.activeElement === trigger
  retryingPreview.value = true
  const loaded = await loadPreview()
  retryingPreview.value = false
  await nextTick()
  if (loaded && previewOpen.value && restore &&
      (document.activeElement === trigger || document.activeElement === document.body)) {
    previewHeading.value?.focus()
  }
}
async function retryDetail() {
  if (retryingDetail.value || busy.value) return
  const id = focusId.value
  const trigger = detailRetry.value
  const restore = document.activeElement === trigger
  retryingDetail.value = true
  const loaded = await loadDetail(id)
  retryingDetail.value = false
  await nextTick()
  if (loaded && alive && focusId.value === id && restore &&
      (document.activeElement === trigger || document.activeElement === document.body)) {
    (receiptTarget.value ?? detailHeading.value)?.focus()
  }
}
function openPreview() {
  if (!record.value || !eligible(record.value, Date.now())) return
  comment.value = ''
  commandError.value = ''
  previewOwner = focusId.value
  previewReturn = 'opener'
  previewOpen.value = true
  void loadPreview()
}
function restorePreviewFocus() {
  if (!alive || previewOpen.value || focusId.value !== previewOwner) return
  const active = document.activeElement
  if (active !== document.body && active !== previewButton.value && !active?.closest('.report-export-preview')) return
  const target = (previewReturn === 'receipt'
    ? detailRetry.value ?? receiptTarget.value
    : previewReturn === 'error'
      ? errorTarget.value
      : previewButton.value) ?? detailHeading.value
  target?.focus()
}
async function review(item: ReportExport, decision: 'approved' | 'rejected', event: MouseEvent) {
  if (busy.value || !eligible(item, Date.now()) || !item.can_review) return
  const trigger = event.currentTarget
  const restore = trigger instanceof HTMLElement && document.activeElement === trigger
  busy.value = true
  commandError.value = ''
  receipt.value = ''
  const payload = { version: item.version, decision, comment: comment.value }
  try {
    try {
      await ElMessageBox.confirm(`${decision === 'approved' ? '批准' : '退回'}“${item.report_name}”的 ${item.row_count.toLocaleString()} 行快照？`, '导出审批', { confirmButtonText: '确认', cancelButtonText: '取消', type: 'warning', modalClass: 'report-export-confirm' })
    } catch {
      const active = document.activeElement
      if (alive && focusId.value === item.id && restore &&
          (active === document.body || active?.closest('.report-export-confirm'))) trigger.focus()
      return
    }
    if (!alive || focusId.value !== item.id) return
    if (!eligible(item, Date.now())) {
      if (restore && document.activeElement === document.body) detailHeading.value?.focus()
      return
    }
    await reportExportApi.review(item.id, payload)
    if (!alive || focusId.value !== item.id) return
    if (decision === 'approved') {
      receipt.value = item.generation_status ? '已批准，正式文件将由后台生成。' : '已批准此快照。'
    } else {
      receipt.value = '已退回此快照。'
    }
    void load()
    await loadDetail(item.id)
    if (!alive || focusId.value !== item.id) return
    previewReturn = 'receipt'
    previewOpen.value = false
  } catch (e) {
    if (alive && focusId.value === item.id) {
      commandError.value = message(e, '审批结果未确认，请刷新记录核对。')
      previewReturn = 'error'
      previewOpen.value = false
      refresh()
    }
  } finally { if (alive) busy.value = false }
}
async function download(item: ReportExport) {
  const formal = !!item.generation_status
  const available = () => formal ? pickupAvailable(item, Date.now()) : eligible(item, Date.now()) && item.can_download
  if (busy.value || !available()) return
  busy.value = true
  commandError.value = ''
  try {
    let blob: Blob
    if (formal) {
      const { credential } = await reportExportApi.credential(item.id)
      if (!alive || focusId.value !== item.id || !available()) return
      blob = await reportExportApi.redeem(item.id, credential)
    } else {
      blob = await reportExportApi.download(item.id)
    }
    if (!alive || focusId.value !== item.id || !available()) return
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `report-${item.report_id}-${formal ? 'export' : 'snapshot'}-${item.id}.${item.format}`
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (e) {
    if (alive && focusId.value === item.id) { commandError.value = message(e, '下载失败，请刷新授权状态。'); void loadDetail() }
  } finally { if (alive) busy.value = false }
}
watch(previewOpen, open => { if (!open) preview.reset() }, { flush: 'sync' })
watch(focusId, async (id, previous) => {
  previewOpen.value = false
  detail.reset()
  receipt.value = ''
  commandError.value = ''
  if (id) void loadDetail(id)
  await nextTick()
  if (focusId.value !== id) return
  if (id) detailHeading.value?.focus()
  else if (previous) {
    const opener = directory.value?.querySelector<HTMLElement>(`[data-export-id="${previous}"]`) ?? directory.value
    opener?.focus()
  }
}, { immediate: true })
void load()
window.addEventListener('focus', readClock)
document.addEventListener('visibilitychange', readClock)
onScopeDispose(() => {
  alive = false
  window.removeEventListener('focus', readClock)
  document.removeEventListener('visibilitychange', readClock)
})
</script>

<template>
  <section ref="directory" tabindex="-1" class="space-y-4 min-w-0" aria-label="报表导出">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <RouterLink v-if="canRequest" :to="{ query: { workspace_id: route.query.workspace_id, view: 'published' } }" class="editorial-text-action">前往已发布报表申请导出</RouterLink>
      <button type="button" class="ghost-button ui-control-target" :disabled="busy || list.state.value.status === 'loading'" @click="refresh">刷新记录</button>
    </div>
    <LoadingState v-if="list.state.value.status === 'loading'" label="正在读取导出记录…" />
    <PageState v-else-if="list.state.value.status === 'error'" title="读取导出记录失败" :description="list.state.value.message || '请重试读取记录。'" tone="danger">
      <template #actions><button class="ghost-button ui-control-target" @click="load()">重试读取记录</button></template>
    </PageState>
    <template v-else-if="listed">
      <div v-if="listed.items.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="导出记录表格" @focusin="revealTableAction" @keydown.self="scrollTableByKeyboard">
        <table class="editorial-table" aria-label="导出记录">
          <thead><tr><th scope="col">序号</th><th scope="col">报表名称</th><th scope="col">格式</th><th scope="col">审批状态</th><th scope="col">有效至</th><th scope="col">领取资格</th><th scope="col">操作</th></tr></thead>
          <tbody><tr v-for="(item, index) in listed.items" :key="item.id" :aria-label="`导出快照${item.id}`">
            <td class="editorial-table__number">{{ String((page - 1) * 20 + index + 1).padStart(2, '0') }}</td>
            <td class="editorial-table__name"><h2 class="editorial-object-title">{{ item.report_name }}</h2><p class="editorial-table__meta">{{ item.requester_name }} · 快照 #{{ item.id }}</p></td>
            <td>{{ item.format.toUpperCase() }}</td><td><span class="status-badge">{{ status(item) }}</span></td>
            <td class="whitespace-nowrap">{{ time(item.expires_at) }}</td>
            <td class="whitespace-nowrap">{{ item.generation_status === 'succeeded' ? '在详情核对' : eligible(item) && item.can_download ? '可下载' : '当前不可下载' }}</td>
            <td><button class="editorial-text-action whitespace-nowrap" :data-export-id="item.id" :aria-expanded="focusId === item.id" @click="select(item.id)">查看详情</button></td>
          </tr></tbody>
        </table>
      </div>
      <PageState v-else title="暂无导出记录" :description="canRequest ? '可在获准的已发布报表中申请导出。' : '获准查看的申请会在这里显示。'" />
      <div class="flex flex-wrap justify-between gap-2 text-xs text-muted"><span>共 {{ listed.total }} 条</span><div class="flex items-center gap-2"><button class="ghost-button ui-control-target" :disabled="page <= 1" @click="load(page - 1)">上一页</button><span>{{ page }} / {{ Math.max(1, Math.ceil(listed.total / 20)) }}</span><button class="ghost-button ui-control-target" :disabled="page * 20 >= listed.total" @click="load(page + 1)">下一页</button></div></div>
    </template>
    <section v-if="focusId" class="space-y-4 border-t border-stroke pt-4" aria-label="导出快照详情">
      <header class="flex flex-wrap items-center justify-between gap-2"><h2 ref="detailHeading" tabindex="-1" class="page-header__title ui-focus-target">导出快照 #{{ focusId }}</h2><button class="ghost-button ui-control-target" :disabled="busy" @click="closeDetail">关闭导出详情</button></header>
      <p v-if="receipt" ref="receiptTarget" tabindex="-1" class="text-success text-sm ui-focus-target" role="status">{{ receipt }}</p>
      <p v-if="commandError && !previewOpen" ref="errorTarget" tabindex="-1" class="text-danger text-sm ui-focus-target" role="alert">{{ commandError }}</p>
      <LoadingState v-if="detail.state.value.status === 'loading' && !retryingDetail" label="正在核对导出快照…" />
      <PageState v-else-if="retryingDetail || detail.state.value.status === 'error'"
        :title="retryingDetail ? '正在重试读取导出详情…' : '读取导出详情失败'"
        :description="detail.state.value.status === 'error' ? detail.state.value.message || '请重试核对快照。' : ''"
        :loading="retryingDetail" tone="danger">
        <template #actions><button ref="detailRetry" class="ghost-button ui-control-target" :aria-disabled="busy || retryingDetail || undefined" @click="retryDetail">重试读取详情</button></template>
      </PageState>
      <template v-else-if="record">
        <h3 class="editorial-object-title">{{ record.report_name }}</h3>
        <p role="status" class="text-sm text-muted">{{ status(record) }} · {{ record.row_count.toLocaleString() }} 行 · {{ record.format.toUpperCase() }} · 发布 #{{ record.publication_no }} · {{ record.requester_name }}</p>
        <p class="text-sm break-words">用途：{{ record.reason }}</p><p v-if="record.review_comment" class="text-sm break-words">审批意见：{{ record.review_comment }}（{{ record.reviewer_name }}）</p>
        <p class="text-xs text-muted">生成于 {{ time(record.created_at) }} · 有效至 {{ time(record.expires_at) }}</p>
        <p v-if="record.file_expires_at" class="text-xs text-muted">正式文件有效至 {{ time(record.file_expires_at) }}</p>
        <p v-else-if="record.generation_status === 'succeeded'" class="text-sm text-muted">当前不可领取，或文件已到期。仅原申请人可领取有效文件。</p>
        <details class="text-xs text-muted"><summary class="cursor-pointer">核验信息</summary><p class="break-all select-text mt-1">SHA256：{{ record.snapshot_sha256 }}</p></details>
        <div v-if="eligible(record)" class="flex flex-wrap gap-2">
          <button v-if="record.can_review || record.can_download" ref="previewButton" class="ghost-button ui-control-target" :disabled="busy" @click="openPreview">{{ record.can_review ? '预览并审批' : '预览快照' }}</button>
          <button v-if="record.can_download && !record.generation_status" ref="downloadButton" class="liquid-button ui-control-target" :disabled="busy" @click="download(record)">下载{{ record.format.toUpperCase() }}</button>
        </div>
        <button v-if="pickupAvailable(record)" ref="downloadButton" class="liquid-button ui-control-target" :disabled="busy" @click="download(record)">领取文件</button>
      </template>
    </section>
    <el-dialog v-model="previewOpen" title="导出快照预览" class="report-export-preview" width="min(1100px,calc(100vw - 24px))" :close-on-click-modal="false" :show-close="!busy" :close-on-press-escape="!busy" @closed="restorePreviewFocus">
      <LoadingState v-if="preview.state.value.status === 'loading' && !retryingPreview" label="正在读取已保存快照…" />
      <PageState v-else-if="retryingPreview || preview.state.value.status === 'error'"
        :title="retryingPreview ? '正在重试读取快照预览…' : '读取快照预览失败'"
        :description="preview.state.value.status === 'error' ? preview.state.value.message || '请重试预览。' : ''"
        :loading="retryingPreview" tone="danger">
        <template #actions><button ref="previewRetry" class="ghost-button ui-control-target" :aria-disabled="retryingPreview || undefined" @click="retryPreview">重试读取预览</button></template>
      </PageState>
      <div v-else-if="snapshot" class="space-y-3 max-h-[70vh] overflow-y-auto pr-1">
        <h3 ref="previewHeading" tabindex="-1" class="font-medium ui-focus-target">{{ snapshot.export.report_name }}</h3><p class="text-xs text-muted">预览 {{ snapshot.result.rows?.length || 0 }} 行，审批作用于完整的 {{ snapshot.export.row_count.toLocaleString() }} 行快照。</p>
        <p class="text-xs text-muted">数据范围：{{ snapshot.scope || '使用此版本获批的数据范围' }}</p>
        <details class="text-xs text-muted"><summary class="cursor-pointer">查看筛选条件</summary><pre class="whitespace-pre-wrap break-words mt-2">{{ JSON.stringify(snapshot.params,null,2) }}</pre></details>
        <div class="h-80 max-h-[40vh] flex min-w-0 overflow-hidden"><ResultGrid :columns="(snapshot.result.columns || []).map(name => snapshot?.labels?.[name] || name)" :rows="snapshot.result.rows || []" /></div>
        <template v-if="snapshot.export.can_review && eligible(snapshot.export)"><label class="block text-xs">审批意见<textarea v-model.trim="comment" aria-label="导出审批意见" class="glass-input block w-full mt-1" maxlength="500" rows="2" :disabled="busy" /></label><div class="flex flex-wrap justify-end gap-2"><button class="ghost-button text-danger ui-control-target" :disabled="!comment" :aria-disabled="busy || undefined" @click="review(snapshot.export, 'rejected', $event)">退回导出</button><button class="liquid-button ui-control-target" :aria-disabled="busy || undefined" @click="review(snapshot.export, 'approved', $event)">{{ snapshot.export.generation_status ? '批准并生成正式文件' : '批准此快照下载' }}</button></div></template>
      </div>
    </el-dialog>
  </section>
</template>
