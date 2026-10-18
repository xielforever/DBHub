<template>
  <section class="space-y-4" aria-label="仪表盘查看">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <p class="text-xs text-violet-200">
          {{ user.workspace?.name }} ·
          {{
            mode === 'published'
              ? '正式发布'
              : mode === 'review'
                ? '冻结版本审阅'
                : '已保存草稿预览'
          }}
          · v{{ view?.version || '—' }}
        </p>
        <h2 class="text-2xl font-semibold mt-1">
          {{ view?.name || '仪表盘' }}
        </h2>
        <p class="text-sm text-white/60 mt-1">{{ view?.description }}</p>
      </div>
      <div class="flex flex-wrap gap-2">
        <button class="ghost-button" @click="emit('close')">返回列表</button
        ><button class="liquid-button" :disabled="busy" @click="refresh()">
          {{ busy ? '查询中…' : '刷新全部' }}
        </button>
      </div>
    </header>
    <div
      class="glass-panel p-4 flex flex-wrap gap-x-6 gap-y-2 text-sm"
      role="status"
      aria-live="polite"
    >
      <span
        >{{
          partialRefresh ? '部分刷新' : quality.complete ? '完整' : '部分可用'
        }}
        {{ quality.available }}/{{ quality.total }} 张必需卡片</span
      >
      <span v-if="quality.optionalTotal"
        >可选卡片 {{ quality.optionalAvailable }}/{{
          quality.optionalTotal
        }}</span
      ><span>时效：{{ freshnessLabels[quality.freshness] }}</span
      ><span v-if="quality.misaligned" class="text-amber-200"
        >比较组水位未对齐，暂不宜比较</span
      >
      <span class="text-white/65"
        >最近完整查询：{{
          fullCycleAt ? formatTime(fullCycleAt) : '尚未完成'
        }}</span
      >
    </div>
    <p class="text-xs text-white/60">
      刷新可获取最新查询结果。请同时核对业务数据截止时间；本页需手动刷新。
    </p>
    <p v-if="error" role="alert" class="text-rose-200">{{ error }}</p>
    <div class="dashboard-grid">
      <article
        v-for="state in states"
        :key="state.tile.key"
        class="glass-panel p-4 min-w-0 space-y-3"
        :class="`tile-${state.tile.width}`"
        :aria-label="state.tile.report?.name || '受限卡片'"
      >
        <header class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <h3 class="font-semibold">
              {{ state.tile.report?.name || '受限卡片' }}
            </h3>
            <p class="text-xs text-white/55 mt-1">
              {{ state.tile.required ? '必需' : '可选'
              }}<span v-if="state.tile.report">
                ·
                {{ unitLabel(state.tile.report.data_contract.declaration.unit)
                }}<span
                  v-if="state.tile.report.data_contract.declaration.currency"
                >
                  ·
                  {{
                    state.tile.report.data_contract.declaration.currency
                  }}</span
                >
                · 报表发布 #{{ state.tile.report.publication_no }}</span
              >
            </p>
          </div>
          <button
            v-if="state.tile.status === 'ready'"
            class="ghost-button text-xs shrink-0"
            :disabled="busy"
            @click="refresh(state.tile.key)"
          >
            重新查询
          </button>
        </header>
        <p
          v-if="state.status === 'loading'"
          role="status"
          class="text-sm text-violet-200"
        >
          查询中…
        </p>
        <div
          v-if="state.tile.status === 'restricted'"
          class="py-10 text-center space-y-3"
        >
          <p class="text-sm text-white/65">无权访问或报表暂不可用</p>
          <button
            v-if="mode === 'published'"
            class="ghost-button text-xs"
            @click="requestAccess(state.tile.key)"
          >
            申请本人报表访问权
          </button>
        </div>
        <p
          v-if="state.tile.status === 'contract_changed'"
          role="alert"
          class="text-amber-200 text-sm py-8"
        >
          报表口径已变化，需要看板负责人明确采纳新契约并重新审批。
        </p>
        <p v-if="state.message" class="text-sm text-amber-200" role="alert">
          {{ state.message }}
        </p>
        <template
          v-if="state.result && usableResult(state, now) && state.tile.report"
        >
          <p
            class="text-xs"
            :class="
              tileFreshness(state, now) === 'fresh'
                ? 'text-emerald-200'
                : 'text-amber-200'
            "
          >
            {{ state.retained ? '上次成功结果 · ' : ''
            }}{{ freshnessLabels[tileFreshness(state, now)] }} · 查询
            {{ formatTime(state.result.quality.query_completed_at) }} · 数据截止
            {{
              state.result.quality.data_as_of
                ? formatTime(state.result.quality.data_as_of)
                : '未知'
            }}
          </p>
          <ReportResult
            :type="state.tile.report.chart_type"
            :config="state.tile.report.chart_config"
            :result="state.result.result"
            :title="state.tile.report.name"
          />
        </template>
        <button
          v-if="state.status === 'error' && state.tile.execution_id"
          class="ghost-button text-xs"
          @click="checkReceipt(state)"
        >
          查看本次执行回执
        </button>
      </article>
    </div>
    <p
      v-if="!states.length && !busy"
      class="glass-panel p-8 text-center text-white/60"
    >
      尚未添加卡片。
    </p>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import request, { ApiRequestError } from '../../utils/request'
import { useUserStore } from '../../stores/user'
import {
  managedDashboardApi as api,
  type DashboardView,
  type DashboardRefresh,
} from '../../api/managed-dashboard'
import ReportResult from '../report/ReportResult.vue'
import {
  dashboardQuality,
  tileFreshness,
  usableResult,
  mustClearPrevious,
  retainOnFailure,
  RETAIN_MS,
  type TileState,
} from './dashboard-quality'
const props = withDefaults(
  defineProps<{
    id: number
    mode?: 'published' | 'draft' | 'review'
    reviewId?: number
  }>(),
  { mode: 'published', reviewId: 0 },
)
const emit = defineEmits<{ close: []; refreshed: [id: string] }>(),
  user = useUserStore()
const fullRefreshId = ref(''),
  partialRefresh = ref(false)
const view = ref<DashboardView>(),
  states = ref<TileState[]>([]),
  busy = ref(false),
  error = ref(''),
  fullCycleAt = ref(''),
  now = ref(0)
let baseline = 0,
  started = performance.now(),
  generation = 0,
  alive = true,
  controller = new AbortController()
const defaultQuality = {
  allow_unknown_freshness: false,
  reason: '',
  max_watermark_skew_seconds: 300,
}
const quality = computed(() =>
  dashboardQuality(
    states.value,
    view.value?.quality || defaultQuality,
    now.value,
    fullRefreshId.value,
    partialRefresh.value,
  ),
)
const freshnessLabels: Record<string, string> = {
  fresh: '新鲜',
  stale: '已过期',
  unknown: '未知',
  missing: '尚无结果',
}
const unitNames: Record<string, string> = {
  CNY_10000: '万元',
  CNY: '元',
  item_count: '项',
  item_quantity: '件',
  percent: '%',
}
const unitLabel = (value: string) => unitNames[value] || value
const formatTime = (value: string) => new Date(value).toLocaleString()
const timer = setInterval(() => {
  now.value = baseline + performance.now() - started
  for (const state of states.value) {
    if (
      state.result &&
      now.value - Date.parse(state.result.quality.query_completed_at) >=
        RETAIN_MS
    ) {
      state.result = undefined
      state.message = '结果保留期已满，请重新查询'
      state.retained = false
    }
  }
}, 1000)
function pauseUnavailablePage() {
  if (
    !busy.value ||
    (navigator.onLine && document.visibilityState === 'visible')
  )
    return
  generation++
  controller.abort()
  busy.value = false
  error.value = '页面已暂停查询；返回页面或恢复网络后，请手动刷新。'
  for (const state of states.value) {
    if (state.status === 'loading' || state.status === 'idle') {
      state.status = 'error'
      state.retained = !!state.result
      state.message = '查询已暂停，请手动刷新'
    }
  }
}
onMounted(() => {
  document.addEventListener('visibilitychange', pauseUnavailablePage)
  window.addEventListener('offline', pauseUnavailablePage)
  void refresh()
})
onScopeDispose(() => {
  alive = false
  generation++
  controller.abort()
  clearInterval(timer)
  document.removeEventListener('visibilitychange', pauseUnavailablePage)
  window.removeEventListener('offline', pauseUnavailablePage)
  states.value = []
})
async function refresh(only?: string) {
  if (busy.value) return
  if (!navigator.onLine || document.visibilityState !== 'visible') {
    error.value = '页面已暂停查询；返回页面或恢复网络后，请手动刷新。'
    return
  }
  busy.value = true
  error.value = ''
  controller.abort()
  controller = new AbortController()
  const current = ++generation,
    signal = controller.signal
  partialRefresh.value = !!only
  if (!only) fullRefreshId.value = `pending:${current}`
  const isCurrent = () => alive && current === generation && !signal.aborted
  try {
    const context: DashboardRefresh = await api.refresh(
      props.id,
      props.mode,
      props.reviewId,
      signal,
    )
    if (!isCurrent()) return
    if (!only) fullRefreshId.value = context.refresh_id
    baseline = Date.parse(context.server_time)
    started = performance.now()
    now.value = baseline
    const changed =
      !!view.value &&
      (view.value.publication_no !== context.publication_no ||
        view.value.lifecycle_epoch !== context.lifecycle_epoch)
    if (changed) {
      fullCycleAt.value = ''
      if (only) error.value = '看板布局已更新，请刷新全部卡片以完成本轮查询'
    }
    const previous = changed ? [] : states.value
    states.value = context.tiles.map((tile) => {
      const prior = previous.find((s) => s.tile.key === tile.key)
      const state: TileState = {
        tile,
        status: tile.status === 'ready' ? 'idle' : tile.status,
      }
      if (
        prior &&
        !mustClearPrevious(prior, tile) &&
        usableResult(prior, now.value)
      ) {
        state.result = prior.result
        state.retained = only && tile.key !== only ? prior.retained : true
        state.cycle = prior.cycle
        if (only && tile.key !== only) {
          state.status = prior.status
          state.message = prior.message
        }
      }
      return state
    })
    view.value = context
    const targets = states.value.filter(
      (s) => s.tile.status === 'ready' && (!only || s.tile.key === only),
    )
    let position = 0
    async function worker() {
      while (position < targets.length && isCurrent()) {
        const state = targets[position++]!
        state.status = 'loading'
        try {
          const result = await api.execute(
            context.refresh_id,
            state.tile.key,
            signal,
          )
          if (!isCurrent()) return
          if (
            result.refresh_id !== context.refresh_id ||
            result.tile_key !== state.tile.key ||
            result.dashboard_publication_no !== context.publication_no ||
            result.lifecycle_epoch !== context.lifecycle_epoch ||
            result.report_publication_no !==
              state.tile.report?.publication_no ||
            result.quality.contract_hash !== state.tile.contract_hash
          )
            throw new ApiRequestError('执行结果版本不一致，请重新刷新', 409)
          state.result = result
          state.status = 'success'
          state.retained = false
          state.cycle = context.refresh_id
          state.message = undefined
        } catch (e) {
          if (!isCurrent()) return
          const status = e instanceof ApiRequestError ? e.status : undefined
          state.status = 'error'
          state.message =
            e instanceof Error ? e.message : '查询失败，请核对执行回执'
          if (!retainOnFailure(status)) {
            state.result = undefined
            state.retained = false
          } else {
            state.retained = !!state.result
            if (state.result) state.message += '；暂显示上次成功结果'
          }
        }
      }
    }
    await Promise.all([worker(), worker()])
    if (!isCurrent()) return
    const required = states.value.filter((s) => s.tile.required)
    if (
      !only &&
      required.length &&
      required.every(
        (s) => s.status === 'success' && s.cycle === context.refresh_id,
      )
    ) {
      fullCycleAt.value = new Date(
        Math.max(
          ...required.map((s) =>
            Date.parse(s.result!.quality.query_completed_at),
          ),
        ),
      ).toISOString()
    }
    emit('refreshed', context.refresh_id)
  } catch (e) {
    if (!isCurrent()) return
    error.value = e instanceof Error ? e.message : '看板加载失败'
    if (e instanceof ApiRequestError && !retainOnFailure(e.status)) {
      states.value = []
      view.value = undefined
      fullCycleAt.value = ''
    } else {
      for (const state of states.value) {
        if (state.result) {
          state.retained = true
          state.status = 'error'
        }
      }
    }
  } finally {
    if (isCurrent()) busy.value = false
  }
}
async function requestAccess(tile: string) {
  try {
    const input = await ElMessageBox.prompt(
      '申请将授予独立的报表查看权，获批并正式发布后生效。请说明用途。',
      '申请本人权限',
      {
        inputPlaceholder: '申请理由',
        inputValidator: (v) => !!v?.trim() || '请填写理由',
      },
    )
    await api.requestAccess(props.id, tile, input.value)
    if (alive) error.value = '访问申请已提交，可在站内申请队列查看进度。'
  } catch (e) {
    if (e instanceof Error && alive) error.value = e.message
  }
}
async function checkReceipt(state: TileState) {
  try {
    const receipt = await request.get<unknown, { status: string }>(
      `/query/executions/${state.tile.execution_id}`,
      { silentError: true },
    )
    if (alive)
      state.message = `执行回执：${receipt.status}。回执不保存结果行；重新查询将创建新的执行编号。`
  } catch (e) {
    if (alive) {
      state.message = e instanceof Error ? e.message : '回执暂不可用'
      if (e instanceof ApiRequestError && !retainOnFailure(e.status)) {
        state.result = undefined
        state.retained = false
      }
    }
  }
}
</script>
<style scoped>
.dashboard-grid {
  display: grid;
  grid-template-columns: repeat(12, minmax(0, 1fr));
  gap: 1rem;
}
.tile-full {
  grid-column: span 12;
}
.tile-half {
  grid-column: span 6;
}
.tile-wide {
  grid-column: span 8;
}
.tile-narrow {
  grid-column: span 4;
}
@media (max-width: 767px) {
  .dashboard-grid > article {
    grid-column: span 12;
  }
}
</style>
