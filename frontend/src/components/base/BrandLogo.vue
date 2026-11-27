<script setup lang="ts">
withDefaults(defineProps<{ compact?: boolean }>(), { compact: false })
</script>

<template>
  <span class="brand-logo" role="img" aria-label="知数">
    <svg class="brand-logo__mark" viewBox="0 0 100 100" aria-hidden="true" focusable="false">
      <use href="/brand/zhishu-symbols.svg#mark" />
    </svg>
    <svg v-if="!compact" class="brand-logo__name" viewBox="0 0 204 100" aria-hidden="true" focusable="false">
      <use href="/brand/zhishu-symbols.svg#wordmark" />
    </svg>
  </span>
</template>

<style scoped>
.brand-logo { display: inline-flex; align-items: center; gap: 8px; flex-shrink: 0; vertical-align: middle; line-height: 1; }
.brand-logo__mark { width: 28px; height: 28px; fill: var(--db-accent); flex-shrink: 0; }
.brand-logo__name { width: 45px; height: 22px; fill: var(--db-text-primary); flex-shrink: 0; }
@media (forced-colors: active) { .brand-logo__mark, .brand-logo__name { fill: CanvasText; } }
</style>
