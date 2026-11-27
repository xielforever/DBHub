<script setup lang="ts">
import { shallowRef, ref } from 'vue'
import { ElTooltip } from 'element-plus'

defineProps<{ text: string }>()
const label = shallowRef<HTMLElement>()
const overflowing = ref(false)

function show() {
  const node = label.value
  overflowing.value = !!node && node.scrollWidth > node.clientWidth
}
</script>

<template>
  <span ref="label" class="block min-w-0 truncate" @mouseenter="show" @mouseleave="overflowing = false">
    {{ text }}
    <ElTooltip v-if="overflowing" :virtual-ref="label" virtual-triggering :visible="true" :content="text"
      placement="top" :enterable="false" :offset="6" popper-class="catalog-text-tooltip" />
  </span>
</template>

<style scoped>
:global(.catalog-text-tooltip) {
  max-width: min(28rem, calc(100vw - 2rem));
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
