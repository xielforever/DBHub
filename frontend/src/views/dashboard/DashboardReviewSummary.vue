<template>
  <section class="space-y-4" aria-label="仪表盘审批变更摘要">
    <p v-if="loading" role="status" class="text-sm text-white/65">
      正在整理版本变更和当前可见口径…
    </p>
    <p v-if="error" role="alert" class="text-sm text-rose-200">
      {{ error }}
      <button class="ghost-button text-xs" @click="load">重新读取摘要</button>
    </p>
    <template v-if="summary">
      <div class="rounded-xl border border-white/10 p-4 space-y-2">
        <h4 class="font-semibold">审批变更摘要</h4>
        <p class="text-xs text-white/65">
          {{ baseline }} · 独立审阅人 {{ summary.reviewer_name }}
        </p>
        <p class="text-sm text-white/80">
          个人受众：{{ summary.users.map((u) => u.name).join('、') || '无' }}
        </p>
        <p class="text-sm text-white/80">
          用户组：{{ summary.groups.map((g) => g.name).join('、') || '无' }}
        </p>
        <p class="text-xs text-white/60">
          看板受众控制入口；报表访问权仍独立校验。
        </p>
      </div>
      <ol
        v-if="summary.changes.length"
        class="rounded-xl border border-white/10 divide-y divide-white/10 max-h-72 overflow-y-auto"
        aria-label="版本差异"
      >
        <li
          v-for="(change, index) in summary.changes"
          :key="index"
          class="p-3 grid sm:grid-cols-3 gap-2 text-sm"
        >
          <div>
            <span class="text-xs text-violet-200">{{ change.area }}</span>
            <p class="font-medium">{{ change.subject }}</p>
          </div>
          <p class="text-white/65 break-words whitespace-pre-wrap">
            <span class="text-xs text-white/50">之前：</span
            >{{ change.before || '—' }}
          </p>
          <p class="break-words whitespace-pre-wrap">
            <span class="text-xs text-white/50">现在：</span
            >{{ change.after || '—' }}
          </p>
        </li>
      </ol>
      <p v-else class="text-sm text-white/65">
        与比较版本的布局、预期口径和受众一致。
      </p>
      <div class="grid sm:grid-cols-2 gap-3">
        <h4 class="font-medium text-sm sm:col-span-2">
          引用报表与当前业务口径
        </h4>
        <article
          v-for="card in summary.cards"
          :key="card.key"
          class="rounded-xl border border-white/10 p-3 space-y-2"
        >
          <div class="flex flex-wrap justify-between gap-2">
            <h5 class="font-medium text-sm">{{ card.name }}</h5>
            <span class="text-xs text-white/60"
              >{{ card.required ? '必需' : '可选' }} · {{ widths[card.width]
              }}<span v-if="card.comparison_group">
                · 比较组 {{ card.comparison_group }}</span
              ></span
            >
          </div>
          <p
            v-if="card.contract_status !== 'compatible'"
            class="text-xs text-amber-200"
          >
            {{
              card.contract_status === 'changed'
                ? '当前报表口径已变化，请先处理口径并重新验证；以下为当前正式口径。'
                : '当前无权读取或报表不可用，无法确认业务口径。'
            }}
          </p>
          <template v-if="card.contract"
            ><p class="text-sm text-white/80">{{ card.contract.meaning }}</p>
            <p class="text-xs text-white/65">
              单位 {{ displayUnit(card.contract.unit)
              }}<span v-if="card.contract.currency">
                · 币种 {{ card.contract.currency }}</span
              >
              · {{ windowLabel(card.contract.time_window) }} ·
              {{ card.contract.timezone }}
            </p>
            <p class="text-xs text-white/65">
              统计范围：{{ card.contract.scope_description }}
            </p>
            <p class="text-xs text-white/65">
              数据时效：{{
                card.contract.freshness.mode === 'watermark_column'
                  ? `按业务水位校验，允许数据年龄 ${card.contract.freshness.max_data_age_seconds} 秒`
                  : '未提供业务水位，须明确允许未知时效'
              }}
            </p></template
          >
        </article>
      </div>
    </template>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import {
  managedDashboardApi as api,
  type DashboardReviewSummary,
} from '../../api/managed-dashboard'
import { displayUnit } from '../report/reportDisplay'
const props = defineProps<{ reviewId: number }>()
const emit = defineEmits<{
  loaded: [state: { ready: boolean; reviewerName: string }]
}>()
const summary = ref<DashboardReviewSummary>(),
  loading = ref(false),
  error = ref('')
const widths = {
  full: '整行',
  half: '半行',
  wide: '三分之二',
  narrow: '三分之一',
}
const baseline = computed(() =>
  !summary.value || summary.value.baseline_kind === 'initial'
    ? '首次送审，展示本次发布内容'
    : summary.value.baseline_kind === 'current_release'
      ? `与当前正式 v${summary.value.baseline_version}（发布 #${summary.value.baseline_publication_no}）比较`
      : `与前一送审版本 v${summary.value.baseline_version} 比较`,
)
let alive = true,
  generation = 0,
  controller = new AbortController()
onMounted(load)
onScopeDispose(() => {
  alive = false
  generation++
  controller.abort()
})
function windowLabel(window: Record<string, unknown>) {
  if (window.kind === 'current_snapshot') return '当前时点'
  if (window.kind === 'rolling_days') return `滚动 ${window.days} 天`
  if (window.kind === 'calendar_day') return '自然日'
  if (window.kind === 'fixed_range')
    return `固定期间 ${window.start || ''} 至 ${window.end || ''}`
  return typeof window.kind === 'string' ? window.kind : '已声明时间窗'
}
async function load() {
  const current = ++generation
  controller.abort()
  controller = new AbortController()
  loading.value = true
  error.value = ''
  summary.value = undefined
  emit('loaded', { ready: false, reviewerName: '' })
  try {
    const result = await api.reviewSummary(props.reviewId, controller.signal)
    if (alive && current === generation) {
      summary.value = result
      emit('loaded', {
        ready:
          result.cards.length > 0 &&
          result.cards.every((c) => c.contract_status === 'compatible'),
        reviewerName: result.reviewer_name,
      })
    }
  } catch (e) {
    if (alive && current === generation)
      error.value = e instanceof Error ? e.message : '摘要加载失败，请重新读取'
  } finally {
    if (alive && current === generation) loading.value = false
  }
}
</script>
<style scoped>
button {
  min-height: 44px;
}
</style>
