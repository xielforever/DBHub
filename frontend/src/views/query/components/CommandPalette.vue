<template>
  <div v-if="open" class="fixed inset-0 z-[3000] flex items-start justify-center pt-[20vh] bg-black/50 backdrop-blur-sm" @click.self="open = false">
    <div class="w-[480px] max-w-[90vw] glass-panel !p-0 overflow-hidden shadow-2xl border border-white/15 animate-in fade-in zoom-in-95">
      <div class="flex items-center gap-3 px-4 py-3 border-b border-white/10">
        <Search class="w-4 h-4 text-white/40" />
        <input
          ref="inputRef"
          v-model="query"
          placeholder="输入命令或搜索… (Esc 关闭)"
          class="flex-1 bg-transparent outline-none text-sm text-white/90 placeholder:text-white/30"
          @keydown.down.prevent="move(1)"
          @keydown.up.prevent="move(-1)"
          @keydown.enter.prevent="executeSelected"
          @keydown.esc="open = false"
        />
        <span class="text-[10px] px-1.5 py-0.5 rounded bg-white/10 text-white/30">⌘K</span>
      </div>
      <div class="max-h-80 overflow-auto p-1.5">
        <div v-if="!filtered.length" class="py-8 text-center text-xs text-white/35">
          无匹配命令
        </div>
        <button
          v-for="(cmd, idx) in filtered"
          :key="cmd.id"
          class="w-full text-left px-3 py-2.5 rounded-xl flex items-center gap-3 hover:bg-white/10 transition-colors"
          :class="idx === selected ? 'bg-white/10 border border-white/10' : 'border border-transparent'"
          @click="execute(cmd)"
          @mouseenter="selected = idx"
        >
          <div class="w-7 h-7 rounded-lg flex items-center justify-center shrink-0" :class="cmd.iconBg">
            <component :is="cmd.icon" class="w-4 h-4" />
          </div>
          <div class="flex-1 min-w-0">
            <p class="text-xs text-white/80">{{ cmd.label }}</p>
            <p class="text-[11px] text-white/40 truncate">{{ cmd.desc }}</p>
          </div>
          <span v-if="cmd.shortcut" class="text-[10px] font-mono px-1.5 py-0.5 rounded bg-white/10 text-white/30">{{ cmd.shortcut }}</span>
        </button>
      </div>
      <div class="px-3 py-2 border-t border-white/10 bg-white/[0.02] flex items-center gap-2 text-[10px] text-white/30">
        <span class="flex items-center gap-1"><span class="px-1 py-0.5 rounded bg-white/10">↑↓</span> 选择</span>
        <span class="flex items-center gap-1"><span class="px-1 py-0.5 rounded bg-white/10">↵</span> 执行</span>
        <span class="flex items-center gap-1"><span class="px-1 py-0.5 rounded bg-white/10">Esc</span> 关闭</span>
        <div class="flex-1" />
        <span>{{ filtered.length }} 命令</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue'
import { Search } from 'lucide-vue-next'

interface Command {
  id: string
  label: string
  desc: string
  icon: any
  iconBg: string
  shortcut?: string
  action: () => void
  keywords?: string[]
}

const props = defineProps<{
  commands: Command[]
}>()

const open = ref(false)
const query = ref('')
const selected = ref(0)
const inputRef = ref<HTMLInputElement | null>(null)

const filtered = computed(() => {
  const q = query.value.toLowerCase().trim()
  if (!q) return props.commands
  return props.commands.filter(c => {
    const hay = `${c.label} ${c.desc} ${c.keywords?.join(' ') || ''}`.toLowerCase()
    return hay.includes(q)
  })
})

function move(delta: number) {
  selected.value = Math.max(0, Math.min(filtered.value.length - 1, selected.value + delta))
}

function execute(cmd: Command) {
  open.value = false
  query.value = ''
  cmd.action()
}

function executeSelected() {
  const cmd = filtered.value[selected.value]
  if (cmd) execute(cmd)
}

watch(open, async (v) => {
  if (v) {
    await nextTick()
    inputRef.value?.focus()
    selected.value = 0
  }
})

defineExpose({
  open: () => { open.value = true },
  close: () => { open.value = false },
  toggle: () => { open.value = !open.value },
})
</script>
