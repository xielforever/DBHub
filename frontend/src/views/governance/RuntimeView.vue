<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref, useId } from 'vue'
import { CircleAlert, Info } from 'lucide-vue-next'
import { dashboardApi, type MetricsOverview } from '../../api/dashboard'
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { ApiRequestError } from '../../utils/request'

const props = defineProps<{ embedded?: boolean }>()

const id = useId()
const controller = new AbortController()
const loading = ref(false)
const error = ref('')
const denied = ref(false)
const snapshot = ref<MetricsOverview['kpi']>()
const readAt = ref<Date>()
const refreshButton = ref<HTMLButtonElement>()
const retryButton = ref<HTMLButtonElement>()
const contentHeading = computed(() => props.embedded ? 'h3' : 'h2')
const formatCount = new Intl.NumberFormat('zh-CN')
const formatTime = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'medium' })

const metrics = [
  { key: 'connections', label: '我的连接', unit: '个', source: '本人连接记录' },
  { key: 'today_queries', label: '今日查询', unit: '次', source: '本人查询历史' },
  { key: 'today_active_users', label: '今日活跃用户', unit: '人', source: '可见查询记录去重' },
  { key: 'audit_events', label: '审计事件', unit: '条', source: '可见审计日志累计' },
] as const satisfies readonly { key: keyof MetricsOverview['kpi']; label: string; unit: string; source: string }[]

const entries = computed(() => metrics.map(metric => {
  const value = snapshot.value?.[metric.key]
  return {
    ...metric,
    count: typeof value === 'number' && Number.isInteger(value) && value >= 0 ? formatCount.format(value) : undefined,
  }
}))
const hasMetrics = computed(() => entries.value.some(item => item.count !== undefined))

async function load(event?: MouseEvent) {
  if (loading.value || controller.signal.aborted) return
  const button = event?.currentTarget
  const trigger = button instanceof HTMLButtonElement && document.activeElement === button ? button : undefined
  const retrying = button != null && button === retryButton.value
  loading.value = true
  if (!retrying) {
    error.value = ''
    denied.value = false
  }
  snapshot.value = undefined
  readAt.value = undefined
  try {
    const result = await dashboardApi.overview(14, { signal: controller.signal, silentError: true })
    if (controller.signal.aborted) return
    error.value = ''
    snapshot.value = result.kpi
    readAt.value = new Date()
  } catch (cause) {
    if (controller.signal.aborted) return
    denied.value = cause instanceof ApiRequestError && cause.status === 403
    error.value = denied.value
      ? '当前身份没有读取运行统计的权限。'
      : cause instanceof Error ? cause.message : '暂时无法读取统计，请稍后重试。'
  } finally {
    if (!controller.signal.aborted) loading.value = false
  }
  if (!trigger || document.activeElement !== trigger) return
  await nextTick()
  if (controller.signal.aborted || (document.activeElement !== trigger && document.activeElement !== document.body)) return
  const focusTarget = retryButton.value ?? refreshButton.value
  focusTarget?.focus()
}

onMounted(() => void load())
onScopeDispose(() => controller.abort())
</script>

<template>
  <section class="ui-page-stack min-w-0">
    <PageHeader title="空间运行统计" :heading="embedded ? 'h2' : 'h1'"
      description="查看当前工作空间入口可见的连接、查询和审计汇总。">
      <template #actions>
        <span v-if="readAt" class="text-xs text-muted" role="status">
          读取时间 <time :datetime="readAt.toISOString()">{{ formatTime.format(readAt) }}</time>
        </span>
        <button ref="refreshButton" type="button" class="liquid-button ui-control-target aria-disabled:opacity-60"
          :aria-disabled="loading || undefined" @click="load">刷新数据</button>
      </template>
    </PageHeader>

    <section :aria-labelledby="id + '-metrics'" :aria-busy="loading" class="space-y-3 min-w-0">
      <component :is="contentHeading" :id="id + '-metrics'" class="text-lg font-semibold">关键运行指标</component>
      <p v-if="loading && error" role="status" class="text-sm text-muted">正在重试读取运行统计…</p>
      <LoadingState v-if="loading && !error" label="正在读取运行统计…" />
      <PageState v-else-if="error" :heading="contentHeading" :title="denied ? '无权查看运行统计' : '无法读取运行统计'"
        :description="error" :tone="denied ? 'warning' : 'danger'">
        <template v-if="!denied" #actions>
          <button ref="retryButton" type="button" class="ghost-button ui-control-target aria-disabled:opacity-60"
            :aria-disabled="loading || undefined" @click="load">重试</button>
        </template>
      </PageState>
      <PageState v-else-if="!hasMetrics" :heading="contentHeading" title="暂无运行统计" description="本次响应没有可显示的指标。" />
      <dl v-else class="runtime-metrics">
        <div v-for="metric in entries" :key="metric.key" class="runtime-metric">
          <dt class="text-sm font-medium">{{ metric.label }}</dt>
          <dd class="space-y-2">
            <p v-if="metric.count !== undefined" class="text-2xl font-semibold tabular-nums">
              {{ metric.count }} <span class="text-sm font-normal text-muted">{{ metric.unit }}</span>
            </p>
            <p v-else class="text-sm text-muted">暂无数据</p>
            <p class="border-t border-stroke pt-2 text-xs text-muted">{{ metric.source }}</p>
          </dd>
        </div>
      </dl>
    </section>

    <section class="editorial-form-feedback border-warning bg-warning-soft" :aria-labelledby="id + '-host'">
      <CircleAlert class="w-4 h-4 shrink-0 mt-0.5 text-warning" aria-hidden="true" />
      <div class="space-y-1">
        <component :is="contentHeading" :id="id + '-host'" class="font-semibold text-warning">未接入主机指标采集</component>
        <p>当前视图暂无 CPU、内存、磁盘或网络的实测数据。</p>
      </div>
    </section>
    <section class="editorial-form-feedback" :aria-labelledby="id + '-scope'">
      <Info class="w-4 h-4 shrink-0 mt-0.5 text-muted" aria-hidden="true" />
      <div class="space-y-1">
        <component :is="contentHeading" :id="id + '-scope'" class="font-semibold">统计口径</component>
        <p>连接与查询统计当前用户的记录，活跃用户按可见查询记录去重，审计事件按当前可见范围累计。「今日」以元数据库日期为准。</p>
      </div>
    </section>
  </section>
</template>

<style scoped>
.runtime-metrics {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 1px;
  border-block: 1px solid var(--db-divider);
  background: var(--db-divider);
}
.runtime-metric {
  min-width: 0;
  padding: var(--db-card-padding);
  background: var(--db-surface-data);
  display: grid;
  gap: 8px;
}
@media (min-width: 640px) {
  .runtime-metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (min-width: 1024px) {
  .runtime-metrics { grid-template-columns: repeat(4, minmax(0, 1fr)); }
}
</style>
