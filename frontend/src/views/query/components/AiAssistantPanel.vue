<template>
  <div
    :class="[
      'h-full min-h-0 flex flex-col',
      embedded ? '' : 'glass-panel overflow-hidden',
    ]"
  >
    <header
      v-if="!embedded"
      class="flex items-center justify-between px-4 py-3 border-b border-white/10 shrink-0"
    >
      <h2 class="text-sm font-medium flex items-center gap-2">
        <Sparkles class="w-4 h-4 text-indigo-300" /> AI 助手
      </h2>
      <button
        v-if="closable"
        class="text-white/40 hover:text-white transition-colors"
        aria-label="收起 AI 助手"
        @click="emit('close')"
      >
        <PanelRightClose class="w-4 h-4" />
      </button>
    </header>
    <div class="flex-1 overflow-y-auto p-4 space-y-3">
      <div class="glass-card !rounded-2xl p-3 text-xs text-white/70 max-w-[85%]">
        你好！我可以帮你生成 SQL、解释执行计划与优化慢查询。
      </div>
      <div
        class="glass-card !rounded-2xl p-3 text-xs text-white/70 max-w-[85%] ml-auto text-right"
        style="background-image: var(--image-glass-gradient)"
      >
        优化这条 SQL，谢谢。
      </div>
    </div>
    <div class="p-3 border-t border-white/10 shrink-0">
      <div class="glass-input !py-2 text-xs text-white/50">AI 能力接入中...</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { PanelRightClose, Sparkles } from 'lucide-vue-next'

withDefaults(defineProps<{ embedded?: boolean; closable?: boolean }>(), {
  embedded: false,
  closable: true,
})
const emit = defineEmits<{ close: [] }>()
</script>
