<script setup lang="ts">
import { computed } from 'vue'
import { CircleAlert } from 'lucide-vue-next'
import type { TestResult } from '../../api/datasource'

const props = defineProps<{ result?: TestResult | null; error?: string; canUpdateCredentials?: boolean }>()
const emit = defineEmits<{ recover: [] }>()
const failed = computed(() => props.result?.ok === false ? props.result : undefined)
const diagnostic = computed(() => failed.value?.diagnostic)
const title = computed(() => diagnostic.value?.title || failed.value?.error || (failed.value ? '连接测试失败，原因尚未确定' : '未取得本次检查结果'))
const checkedAt = computed(() => {
  const value = failed.value?.checked_at
  if (!value) return undefined
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return undefined
  return { value, label: date.toLocaleString('zh-CN', { hour12: false }) }
})
const elapsed = computed(() => {
  const value = failed.value?.elapsed_ms
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined
})
</script>

<template>
  <div v-if="failed || error" role="status" class="editorial-form-feedback editorial-form-feedback--error min-w-0"
    data-testid="connection-diagnosis">
    <CircleAlert class="w-4 h-4 shrink-0 mt-0.5 text-danger" aria-hidden="true" />
    <div class="min-w-0 space-y-2 [overflow-wrap:anywhere]">
      <p v-if="diagnostic?.title" class="text-xs font-medium">连接测试失败</p>
      <p class="font-semibold text-danger">{{ title }}</p>
      <dl v-if="checkedAt || elapsed !== undefined" class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted">
        <div v-if="checkedAt" class="flex flex-wrap gap-x-2">
          <dt>检查时间</dt><dd><time :datetime="checkedAt.value">{{ checkedAt.label }}</time></dd>
        </div>
        <div v-if="elapsed !== undefined" class="flex gap-2">
          <dt>总耗时</dt><dd>{{ elapsed }} ms</dd>
        </div>
      </dl>
      <template v-if="diagnostic?.hint">
        <p class="text-xs font-medium">修正建议</p>
        <p class="text-sm">{{ diagnostic.hint }}</p>
      </template>
      <p v-else-if="error" class="text-sm">{{ error }}</p>
      <p v-else class="text-sm text-muted">请按检查时间向管理员核对原因。</p>
      <button v-if="canUpdateCredentials && diagnostic?.action === 'update_credentials'"
        type="button" class="ghost-button text-xs ui-control-target" @click="emit('recover')">更新我的凭据</button>
    </div>
  </div>
</template>
