<script setup lang="ts">
import { onMounted, onScopeDispose, ref } from 'vue'
import { createLoadingFeedback, loadingTimings, type LoadingPhase } from '../../utils/loading-feedback'

withDefaults(defineProps<{
  label?: string
  variant?: 'table' | 'detail' | 'chart' | 'inline'
  rows?: number
  announce?: boolean
}>(), { label: '正在读取数据…', variant: 'table', rows: 6, announce: true })
const phase = ref<LoadingPhase>('pending')
let feedback: ReturnType<typeof createLoadingFeedback> | undefined
onMounted(() => {
  feedback = createLoadingFeedback(value => { phase.value = value }, ...loadingTimings())
  feedback.set(true)
})
onScopeDispose(() => feedback?.dispose())
</script>

<template>
  <div class="db-loading-state" :class="`db-loading-state--${variant}`">
    <p :role="announce ? 'status' : undefined" :aria-live="announce ? 'polite' : undefined" class="db-loading-state__label">{{ phase === 'pending' ? '' : label }}<span v-if="phase === 'long'"> 仍在等待响应，可使用其他可用区域。</span></p>
    <div class="db-loading-state__visual" :class="{ 'is-visible': phase === 'visible' || phase === 'long' }" aria-hidden="true" aria-busy="true">
      <span class="db-loading-spinner" />
      <div v-if="variant === 'table'" class="db-loading-skeleton">
        <div v-for="row in Math.max(1, Math.min(rows, 12))" :key="row" class="db-loading-skeleton__row"><i /><i /><i /></div>
      </div>
      <div v-else-if="variant === 'chart'" class="db-loading-chart" />
    </div>

    <slot name="actions" />
  </div>
</template>
