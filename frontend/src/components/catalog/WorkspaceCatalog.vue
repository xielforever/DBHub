<script setup lang="ts">
import { nextTick, onMounted, onScopeDispose, ref, useId, watch } from 'vue'
import { ChevronLeft, ChevronRight, Database } from 'lucide-vue-next'
import { useRoute } from 'vue-router'
import WorkspaceCatalogPanel from './WorkspaceCatalogPanel.vue'

const route = useRoute()
const media = window.matchMedia('(min-width: 1024px)')
const desktop = ref(media.matches)
const open = ref(false)
const pinned = ref(false)
const keyboardFocus = ref(false)
const endInset = ref(24)
const topInset = ref(64)
const launcher = ref<HTMLElement>()
const handle = ref<HTMLButtonElement>()
const panel = ref<InstanceType<typeof WorkspaceCatalogPanel>>()
const panelId = useId()
let viewportObserver: ResizeObserver | undefined
let observedToolbar: HTMLElement | null = null

function alignToContent() {
  endInset.value = 24 + Math.max(0, window.innerWidth - document.documentElement.clientWidth)
  const toolbar = open.value ? document.querySelector<HTMLElement>('[data-catalog-toolbar]') : null
  if (toolbar !== observedToolbar) {
    if (observedToolbar) viewportObserver?.unobserve(observedToolbar)
    if (toolbar) viewportObserver?.observe(toolbar)
    observedToolbar = toolbar
  }
  const bottom = Math.ceil(toolbar?.getBoundingClientRect().bottom ?? 64)
  topInset.value = Math.max(64, Math.min(window.innerHeight - 224, bottom))
}

function viewportChanged(event: MediaQueryListEvent) {
  desktop.value = event.matches
  if (!event.matches) close(false)
}
function dismissOnOutsideClick(event: MouseEvent) {
  if (!open.value || pinned.value || !(event.target instanceof Node)) return
  if (!launcher.value?.contains(event.target) && !panel.value?.contains(event.target)) close(false)
}
onMounted(() => {
  alignToContent()
  viewportObserver = new ResizeObserver(alignToContent)
  viewportObserver.observe(document.documentElement)
  window.addEventListener('resize', alignToContent)
  window.addEventListener('scroll', alignToContent, { passive: true })
  media.addEventListener('change', viewportChanged)
  document.addEventListener('click', dismissOnOutsideClick, true)
})
onScopeDispose(() => {
  viewportObserver?.disconnect()
  window.removeEventListener('resize', alignToContent)
  window.removeEventListener('scroll', alignToContent)
  media.removeEventListener('change', viewportChanged)
  document.removeEventListener('click', dismissOnOutsideClick, true)
})
watch(() => route.path, () => { if (!pinned.value) close(false) })
watch([open, () => route.path], alignToContent, { flush: 'post' })

function showKeyboardShortcut(event: FocusEvent) {
  if (event.target instanceof HTMLElement && event.target.matches(':focus-visible')) keyboardFocus.value = true
}
function hideKeyboardShortcut(event: FocusEvent) {
  if (!(event.relatedTarget instanceof Node) || !launcher.value?.contains(event.relatedTarget)) keyboardFocus.value = false
}
async function close(returnFocus = true) {
  open.value = false
  pinned.value = false
  keyboardFocus.value = false
  if (!returnFocus) return
  await nextTick()
  handle.value?.focus({ preventScroll: true })
}
function toggle() {
  if (open.value) void close()
  else {
    keyboardFocus.value = false
    open.value = true
  }
}
</script>

<template>
  <div v-if="desktop" class="workspace-catalog" :style="{ '--catalog-end': endInset + 'px', '--catalog-start': topInset + 'px' }">
    <WorkspaceCatalogPanel v-if="open" :id="panelId" ref="panel" v-model:pinned="pinned" @close="close()" />
    <div ref="launcher" class="workspace-catalog-launcher" :class="{ 'workspace-catalog-launcher--keyboard': keyboardFocus }"
      @focusin="showKeyboardShortcut" @focusout="hideKeyboardShortcut">
      <button ref="handle" type="button" class="workspace-catalog-handle ui-focus-target"
        :aria-label="open ? '收起数据目录' : '打开数据目录'" :aria-expanded="open"
        :aria-controls="open ? panelId : undefined" @click="toggle">
        <ChevronRight v-if="open" class="size-4" aria-hidden="true" />
        <ChevronLeft v-else class="size-4" aria-hidden="true" />
      </button>
      <div v-if="!open" class="workspace-catalog-shortcuts">
        <button type="button" class="workspace-catalog-shortcut ghost-button ui-control-target text-xs" @click="toggle">
          <Database class="size-3.5 shrink-0" aria-hidden="true" />数据目录
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.workspace-catalog {
  position: fixed;
  inset: var(--catalog-start) var(--catalog-end) 20px auto;
  width: min(400px, 50vw);
  pointer-events: none;
  z-index: 19;
}
.workspace-catalog-launcher {
  position: absolute;
  inset-inline-start: 100%;
  inset-block-end: 22px;
  pointer-events: auto;
}
.workspace-catalog-handle {
  display: grid;
  place-items: center;
  width: 24px;
  min-width: 24px;
  height: 34px;
  padding: 0;
  border: 1px solid var(--db-control-border);
  border-inline-end: 0;
  border-start-start-radius: var(--db-radius-control);
  border-end-start-radius: var(--db-radius-control);
  background: var(--db-surface-solid);
  color: var(--db-text-primary);
  cursor: pointer;
}
.workspace-catalog-handle:hover { background: var(--db-interactive-hover); }
.workspace-catalog-handle:focus-visible { outline-offset: -3px; }
.workspace-catalog-handle[aria-expanded='true'] { color: var(--db-accent); border-color: var(--db-accent); }
.workspace-catalog-shortcuts {
  position: absolute;
  inset-inline-end: 100%;
  inset-block-end: 0;
  padding-inline-end: 5px;
  visibility: hidden;
}
.workspace-catalog-launcher:is(:hover, .workspace-catalog-launcher--keyboard) .workspace-catalog-shortcuts { visibility: visible; }
.workspace-catalog-shortcut {
  display: flex;
  align-items: center;
  gap: 7px;
  height: 34px;
  white-space: nowrap;
  background: var(--db-surface-solid);
  box-shadow: 0 3px 10px rgb(0 0 0 / 12%);
}
</style>
