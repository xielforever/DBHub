<template>
  <div class="relative flex h-full min-h-0 w-full flex-col overflow-hidden">
    <div v-show="!fallback" ref="container" class="min-h-0 flex-1" />
    <p v-if="!ready && !fallback" class="absolute inset-0 flex items-center justify-center text-xs text-white/50" role="status">编辑器加载中…</p>
    <template v-if="fallback">
      <p class="px-4 pt-2 text-[11px] text-amber-200/80">高级编辑器加载失败，已切换为基础编辑器。</p>
      <textarea
        ref="textarea"
        :value="modelValue"
        aria-label="SQL 编辑器"
        placeholder="在此输入 SQL，例如 SELECT 1;"
        spellcheck="false"
        class="min-h-0 flex-1 resize-none bg-transparent p-4 font-mono text-[13px] leading-6 text-indigo-100 outline-none"
        @input="onInput"
        @select="updateSelection"
        @keyup="updateSelection"
        @click="updateSelection"
        @keydown.ctrl.enter.prevent="emit('run')"
        @keydown.meta.enter.prevent="emit('run')"
        @keydown.ctrl.shift.f.prevent="emit('format')"
        @keydown.meta.shift.f.prevent="emit('format')"
      />
    </template>
    <p v-if="ready && !modelValue" class="pointer-events-none absolute left-[68px] top-3 text-[13px] text-white/40" aria-hidden="true">在此输入 SQL，例如 SELECT 1;</p>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type * as Monaco from 'monaco-editor'
import EditorWorker from 'monaco-editor/editor/editor.worker.js?worker'

;(self as unknown as { MonacoEnvironment: { getWorker: () => Worker } }).MonacoEnvironment = {
  getWorker: () => new EditorWorker(),
}
const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{
  (event: 'update:modelValue', value: string): void
  (event: 'run'): void
  (event: 'format'): void
}>()
const container = ref<HTMLDivElement | null>(null)
const textarea = ref<HTMLTextAreaElement | null>(null)
const ready = ref(false)
const fallback = ref(false)
const selectedLength = ref(0)
let disposed = false
let editor: Monaco.editor.IStandaloneCodeEditor | null = null
let observer: ResizeObserver | null = null
const subscriptions: Monaco.IDisposable[] = []

function getSelection(): string {
  if (editor) {
    const selection = editor.getSelection()
    return selection ? editor.getModel()?.getValueInRange(selection) ?? '' : ''
  }
  const input = textarea.value
  return input ? input.value.slice(input.selectionStart, input.selectionEnd) : ''
}
function updateSelection() { selectedLength.value = getSelection().length }
function onInput(event: Event) { emit('update:modelValue', (event.target as HTMLTextAreaElement).value) }

function disposeEditor() {
  observer?.disconnect()
  subscriptions.splice(0).forEach(subscription => subscription.dispose())
  const model = editor?.getModel()
  editor?.dispose()
  model?.dispose()
  editor = null
}

onMounted(async () => {
  try {
    const monaco = await import('monaco-editor')
    if (disposed || !container.value) return
    monaco.editor.defineTheme('dbhub-dark', {
      base: 'vs-dark', inherit: true,
      rules: [
        { token: 'keyword', foreground: 'A78BFA' },
        { token: 'string', foreground: '6EE7B7' },
        { token: 'number', foreground: 'FBBF24' },
        { token: 'comment', foreground: '8792A7' },
      ],
      colors: {
        'editor.background': '#161a2c', 'editor.foreground': '#E0E7FF',
        'editorLineNumber.foreground': '#8792A7', 'editorLineNumber.activeForeground': '#C4B5FD',
        'editor.selectionBackground': '#6366F150', 'editor.lineHighlightBackground': '#FFFFFF08',
        'editorCursor.foreground': '#C4B5FD', 'editorGutter.background': '#161a2c',
      },
    })
    editor = monaco.editor.create(container.value, {
      value: props.modelValue, language: 'sql', theme: 'dbhub-dark', ariaLabel: 'SQL 编辑器',
      fontSize: 13, lineHeight: 22, fontFamily: 'JetBrains Mono, Menlo, monospace',
      minimap: { enabled: false }, wordWrap: 'on', scrollBeyondLastLine: false,
      tabSize: 2, padding: { top: 12, bottom: 12 }, automaticLayout: false,
    })
    subscriptions.push(editor.onDidChangeModelContent(() => {
      const value = editor?.getValue() ?? ''
      if (value !== props.modelValue) emit('update:modelValue', value)
    }))
    subscriptions.push(editor.onDidChangeCursorSelection(updateSelection))
    subscriptions.push(editor.addAction({
      id: 'run-query', label: '运行 SQL',
      keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter],
      run: () => emit('run'),
    }))
    subscriptions.push(editor.addAction({
      id: 'format-sql', label: '格式化 SQL',
      keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.KeyF],
      run: () => emit('format'),
    }))
    observer = new ResizeObserver(() => editor?.layout())
    observer.observe(container.value)
    ready.value = true
  } catch {
    disposeEditor()
    if (!disposed) { ready.value = false; fallback.value = true }
  }
})

watch(() => props.modelValue, value => {
  if (!editor || value === editor.getValue()) return
  const model = editor.getModel()
  if (!model) return
  editor.pushUndoStop()
  editor.executeEdits('format-sql', [{ range: model.getFullModelRange(), text: value, forceMoveMarkers: true }])
  editor.pushUndoStop()
})

onBeforeUnmount(() => {
  disposed = true
  disposeEditor()
})

defineExpose({ getSelection, selectedLength })
</script>
