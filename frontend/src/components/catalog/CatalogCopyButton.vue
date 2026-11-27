<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { Copy } from 'lucide-vue-next'

const props = defineProps<{ value: string; label: string }>()

function copyFromSelection(value: string, trigger: HTMLButtonElement) {
  // 普通 HTTP 页面可能没有 Clipboard API。用户点击时尝试浏览器的选区复制。
  const input = document.createElement('textarea')
  input.value = value
  input.readOnly = true
  input.tabIndex = -1
  input.setAttribute('aria-hidden', 'true')
  input.style.cssText = 'position:fixed;inset:0 auto auto 0;opacity:0;pointer-events:none'
  document.body.append(input)
  try {
    input.focus({ preventScroll: true })
    input.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    input.remove()
    if (trigger.isConnected) trigger.focus({ preventScroll: true })
  }
}

async function copy(event: MouseEvent) {
  const trigger = event.currentTarget as HTMLButtonElement
  const value = props.value
  try {
    await navigator.clipboard.writeText(value)
  } catch {
    if (!copyFromSelection(value, trigger)) {
      ElMessage.error('复制失败，请检查浏览器剪贴板权限。')
      return
    }
  }
  ElMessage.success('已复制')
}
</script>

<template>
  <button type="button" class="catalog-copy ghost-button ui-icon-button" :aria-label="label" :title="label" @click="copy">
    <Copy class="size-3.5" aria-hidden="true" />
  </button>
</template>

<style scoped>
.catalog-copy {
  color: var(--db-text-secondary);
  border-color: transparent;
  background: transparent;
}
.catalog-copy:is(:hover, :focus-visible) { color: var(--db-accent); background: var(--db-interactive-hover); }
</style>
