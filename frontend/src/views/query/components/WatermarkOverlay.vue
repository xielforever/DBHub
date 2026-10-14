<template>
  <div
    v-if="enabled"
    class="pointer-events-none absolute inset-0 z-10 overflow-hidden select-none"
    aria-hidden="true"
  >
    <div class="absolute inset-0 opacity-[0.07]" :style="patternStyle">
      <div
        v-for="i in 30"
        :key="i"
        class="absolute text-[11px] font-mono text-violet-300 whitespace-nowrap rotate-[-22deg]"
        :style="{ left: ((i * 17) % 100) + '%', top: ((i * 23) % 100) + '%' }"
      >
        {{ text }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    enabled: boolean
    text?: string
    opacity?: number
  }>(),
  {
    text: 'DBHub · 机密',
    opacity: 0.07,
  },
)

const patternStyle = computed(() => ({
  opacity: String(props.opacity),
}))
</script>
