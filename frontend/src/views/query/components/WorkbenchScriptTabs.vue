<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { FileCode2, Plus, X } from 'lucide-vue-next'
import type { DropdownInstance } from 'element-plus'
import { scriptTypes, type ScriptType } from '../../../api/workbench-scripts'
import { scriptTabCloseKeys, scriptTabCloseLabels, type ScriptTabCloseAction } from '../script-tab-close'

const props = defineProps<{
  tabs: { key: number; name: string; dirty: boolean }[]
  activeKey: number
  panelId: string
  desktop: boolean
  locked: boolean
}>()
const emit = defineEmits<{
  activate: [key: number]
  close: [key: number, action: ScriptTabCloseAction]
  create: [type: ScriptType]
}>()
const list = ref<HTMLElement>()
const createMenu = ref<DropdownInstance>()
const createTrigger = ref<HTMLButtonElement>()
const contextMenu = ref<{ key: number; left: number; top: number }>()
const menuElement = ref<HTMLElement>()
let contextTrigger: HTMLButtonElement | undefined
const contextActions = computed(() => {
  const keys = props.tabs.map(tab => tab.key)
  const key = contextMenu.value?.key
  return (Object.keys(scriptTabCloseLabels) as ScriptTabCloseAction[]).map(action => ({
    action,
    label: scriptTabCloseLabels[action],
    disabled: key === undefined || !scriptTabCloseKeys(keys, key, action).length,
  }))
})

async function revealActive(focus = false) {
  await nextTick()
  const active = list.value?.querySelector<HTMLButtonElement>('[aria-selected="true"]')
  if (!active) return
  active.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  if (focus) active.focus({ preventScroll: true })
}
watch(() => props.activeKey, () => { void revealActive() })

function onTabKeys(event: KeyboardEvent, key: number) {
  if (event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) {
    void openTabContext(event, key)
    return
  }
  if (props.locked) return
  if (event.key === 'Delete') {
    event.preventDefault()
    emit('close', key, 'current')
    return
  }
  const index = props.tabs.findIndex(tab => tab.key === key)
  const last = props.tabs.length - 1
  const positions: Record<string, number> = {
    ArrowLeft: index > 0 ? index - 1 : last,
    ArrowRight: index < last ? index + 1 : 0,
    Home: 0,
    End: last,
  }
  const position = positions[event.key]
  if (position === undefined) return
  const next = props.tabs[position]
  if (!next) return
  event.preventDefault()
  emit('activate', next.key)
  void revealActive(true)
}
function create(command: string | number | object) {
  if (!props.locked && typeof command === 'string' && Object.prototype.hasOwnProperty.call(scriptTypes, command)) {
    emit('create', command as ScriptType)
  }
}
function dismissCreateMenu() {
  createMenu.value?.handleClose()
  createTrigger.value?.focus({ preventScroll: true })
}

async function openTabContext(event: MouseEvent | KeyboardEvent, key: number) {
  event.preventDefault()
  if (props.locked) return
  const node = event.currentTarget as HTMLElement
  contextTrigger = node instanceof HTMLButtonElement ? node : node.querySelector<HTMLButtonElement>('[role="tab"]') ?? undefined
  if (!contextTrigger) return
  createMenu.value?.handleClose()
  const bounds = contextTrigger.getBoundingClientRect()
  const mouse = event instanceof MouseEvent && (event.clientX || event.clientY)
  const position = { key, left: mouse ? event.clientX : bounds.left, top: mouse ? event.clientY : bounds.bottom }
  contextMenu.value = position
  await nextTick()
  const menu = menuElement.value
  if (!menu || contextMenu.value?.key !== key) return
  contextMenu.value = {
    key,
    left: Math.max(8, Math.min(position.left, window.innerWidth - menu.offsetWidth - 8)),
    top: Math.max(8, Math.min(position.top, window.innerHeight - menu.offsetHeight - 8)),
  }
  menu.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus({ preventScroll: true })
}

function dismissTabContext(restoreFocus = false) {
  if (!contextMenu.value) return
  contextMenu.value = undefined
  if (restoreFocus) contextTrigger?.focus({ preventScroll: true })
  contextTrigger = undefined
}

function onMenuKeys(event: KeyboardEvent) {
  if (event.key === 'Escape' || event.key === 'Tab') {
    if (event.key === 'Escape') event.preventDefault()
    dismissTabContext(true)
    return
  }
  const items = Array.from(menuElement.value?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])
  const index = items.findIndex(item => item === document.activeElement)
  const last = items.length - 1
  const positions: Record<string, number> = {
    ArrowDown: index >= last ? 0 : index + 1,
    ArrowUp: index <= 0 ? last : index - 1,
    Home: 0,
    End: last,
  }
  const next = positions[event.key]
  if (next === undefined) return
  event.preventDefault()
  items[next]?.focus({ preventScroll: true })
}

function onOutsidePointer(event: PointerEvent) {
  if (contextMenu.value && event.target instanceof Node && !menuElement.value?.contains(event.target)) dismissTabContext()
}

function onViewportChange() { dismissTabContext() }

function runContextAction(action: ScriptTabCloseAction) {
  const key = contextMenu.value?.key
  if (key === undefined || props.locked) return
  dismissTabContext()
  emit('close', key, action)
}

watch(() => props.locked, locked => { if (locked) dismissTabContext() })
onMounted(() => {
  document.addEventListener('pointerdown', onOutsidePointer, true)
  window.addEventListener('resize', onViewportChange)
  document.addEventListener('scroll', onViewportChange, true)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onOutsidePointer, true)
  window.removeEventListener('resize', onViewportChange)
  document.removeEventListener('scroll', onViewportChange, true)
})
defineExpose({ focusActive: () => revealActive(true) })
</script>

<template>
  <div class="script-tabs">
    <div ref="list" class="script-tab-list" role="tablist" aria-label="已打开的脚本">
      <div v-for="tab in tabs" :key="tab.key" class="script-tab" role="presentation"
        :class="{ 'script-tab--active': tab.key === activeKey }" @contextmenu="openTabContext($event, tab.key)">
        <button type="button" role="tab" :id="`${panelId}-tab-${tab.key}`" :aria-controls="panelId"
          :aria-selected="tab.key === activeKey" :aria-disabled="locked || undefined" :tabindex="tab.key === activeKey ? 0 : -1"
          aria-haspopup="menu" :aria-expanded="contextMenu?.key === tab.key || undefined"
          class="script-tab-select ui-focus-target" :title="tab.name"
          :aria-label="tab.dirty ? `${tab.name}，有未保存修改` : tab.name"
          @click="!locked && emit('activate', tab.key)" @keydown="onTabKeys($event, tab.key)">
          <FileCode2 v-if="desktop" :size="15" aria-hidden="true" />
          <span class="truncate">{{ tab.name }}</span>
          <span v-if="tab.dirty" class="script-tab-dirty" aria-hidden="true" />
        </button>
        <button type="button" class="script-tab-close ui-focus-target" :disabled="locked"
          :aria-label="`关闭 ${tab.name}`" :title="`关闭 ${tab.name}`" @click="emit('close', tab.key, 'current')">
          <X :size="14" aria-hidden="true" />
        </button>
      </div>
    </div>
    <el-dropdown ref="createMenu" :trigger="desktop ? ['hover', 'click'] : 'click'" :disabled="locked" placement="bottom-end"
      :show-timeout="0" :hide-timeout="180" @command="create">
      <button ref="createTrigger" type="button" class="script-tab-add ui-focus-target" aria-label="新建脚本" title="新建脚本"
        :disabled="locked" @keydown.esc.stop.prevent="dismissCreateMenu">
        <Plus :size="18" aria-hidden="true" />
      </button>
      <template #dropdown>
        <el-dropdown-menu aria-label="选择脚本类型" @keydown.esc.stop.prevent="dismissCreateMenu">
          <el-dropdown-item v-for="(name, type) in scriptTypes" :key="type" :command="type">{{ name }} 脚本</el-dropdown-item>
        </el-dropdown-menu>
      </template>
    </el-dropdown>
    <Teleport to="body">
      <div v-if="contextMenu" ref="menuElement" role="menu" aria-label="标签页操作"
        class="script-tab-menu glass-panel shadow-lg"
        :style="{ left: `${contextMenu.left}px`, top: `${contextMenu.top}px` }" @keydown.stop="onMenuKeys"
        @contextmenu.prevent>
        <button v-for="item in contextActions" :key="item.action" type="button" role="menuitem" tabindex="-1"
          class="ghost-button ui-control-target text-left" :disabled="item.disabled" @click="runContextAction(item.action)">
          {{ item.label }}
        </button>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.script-tabs {
  display: flex;
  flex: none;
  min-width: 0;
  border-bottom: 1px solid var(--db-border);
  background: var(--db-surface-soft);
}
.script-tab-list { display: flex; min-width: 0; overflow: auto hidden; scrollbar-width: thin; }
.script-tab { display: flex; flex: none; max-width: 14rem; border-inline-end: 1px solid var(--db-border); }
.script-tab--active { box-shadow: inset 0 -2px var(--db-action-primary); background: var(--db-surface-solid); color: var(--db-accent); }
.script-tab-select, .script-tab-close, .script-tab-add {
  display: inline-flex;
  align-items: center;
  min-height: 40px;
  border: 0;
  background: transparent;
  color: inherit;
}
.script-tab-select { min-width: 0; gap: 6px; padding: 0 6px 0 11px; font-size: 13px; }
.script-tab-select svg, .script-tab-dirty { flex: none; }
.script-tab--active .script-tab-select { font-weight: 600; }
.script-tab-close, .script-tab-add { flex: none; justify-content: center; width: 36px; }
.script-tab-add { width: 44px; border-inline-start: 1px solid var(--db-divider); }
.script-tab-dirty { width: 7px; height: 7px; border-radius: 50%; background: var(--db-action-primary); }
.script-tab-select:hover, .script-tab-close:hover:not(:disabled), .script-tab-add:hover:not(:disabled) { background: var(--db-interactive-hover); }
.script-tab-close:disabled, .script-tab-add:disabled { opacity: .55; }
.script-tab-menu {
  position: fixed;
  z-index: 2500;
  display: grid;
  gap: 3px;
  min-width: 10rem;
  max-width: calc(100vw - 16px);
  padding: 4px;
  box-shadow: var(--db-shadow-overlay);
}
.script-tab-menu > button { justify-content: flex-start; border-color: transparent; background: transparent; box-shadow: none; }
.script-tab-menu > button:not(:disabled):hover { background: var(--db-interactive-hover); }
@media (max-width: 1023px) {
  .script-tab-list { flex: 1; }
  .script-tab { max-width: min(11rem, 48vw); }
  .script-tab-close, .script-tab-add, .script-tab-select { min-height: 44px; }
  .script-tab-close { width: 44px; }
}
</style>
