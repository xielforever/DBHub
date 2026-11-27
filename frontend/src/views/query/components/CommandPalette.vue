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

<template>
  <el-dialog v-model="open" title="命令面板" width="min(480px, calc(100vw - 24px))" top="20vh" append-to-body :show-close="false" class="command-palette-dialog">
    <template #header><h2 class="sr-only">命令面板</h2></template>
    <div class="overflow-hidden">
      <div class="flex items-center gap-3 px-4 py-3 border-b border-stroke">
        <Search class="w-4 h-4 text-subtle" aria-hidden="true" />
        <input
          ref="inputRef"
          v-model="query"
          placeholder="输入命令或搜索… (Esc 关闭)"
          aria-label="搜索命令"
          class="flex-1 bg-transparent outline-none text-sm text-content placeholder:text-subtle"
          @keydown.down.prevent="move(1)"
          @keydown.up.prevent="move(-1)"
          @keydown.enter.prevent="executeSelected"
          @keydown.esc="open = false"
        />
        <span class="text-xs px-1.5 py-0.5 rounded bg-surface-muted text-subtle">⌘K</span>
      </div>
      <div class="max-h-80 overflow-auto p-1.5">
        <div v-if="!filtered.length" class="py-8 text-center text-xs text-subtle">
          无匹配命令
        </div>
        <button
          v-for="(cmd, idx) in filtered"
          :key="cmd.id"
          type="button"
          class="w-full text-left px-3 py-2.5 rounded-xl flex items-center gap-3 hover:bg-interactive transition-colors ui-control-target"
          :class="idx === selected ? 'bg-surface-muted border border-stroke' : 'border border-transparent'"
          @click="execute(cmd)"
          @mouseenter="selected = idx"
        >
          <div class="w-7 h-7 rounded-lg flex items-center justify-center shrink-0" :class="cmd.iconBg">
            <component :is="cmd.icon" class="w-4 h-4" aria-hidden="true" />
          </div>
          <div class="flex-1 min-w-0">
            <p class="text-xs text-content">{{ cmd.label }}</p>
            <p class="text-xs text-subtle truncate">{{ cmd.desc }}</p>
          </div>
          <span v-if="cmd.shortcut" class="text-xs font-mono px-1.5 py-0.5 rounded bg-surface-muted text-subtle">{{ cmd.shortcut }}</span>
        </button>
      </div>
      <div class="px-3 py-2 border-t border-stroke bg-surface-muted flex items-center gap-2 text-xs text-subtle">
        <span class="flex items-center gap-1"><span class="px-1 py-0.5 rounded bg-surface-muted">↑↓</span> 选择</span>
        <span class="flex items-center gap-1"><span class="px-1 py-0.5 rounded bg-surface-muted">↵</span> 执行</span>
        <span class="flex items-center gap-1"><span class="px-1 py-0.5 rounded bg-surface-muted">Esc</span> 关闭</span>
        <div class="flex-1" />
        <span>{{ filtered.length }} 命令</span>
      </div>
    </div>
  </el-dialog>
</template>

<style scoped>
:global(.command-palette-dialog .el-dialog__header) { padding: 0; border: 0; }
:global(.command-palette-dialog .el-dialog__body) { padding: 0; }
</style>
