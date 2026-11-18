<script setup lang="ts">
import LoadingState from '../../../components/base/LoadingState.vue'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type * as Monaco from 'monaco-editor'
import EditorWorker from 'monaco-editor/editor/editor.worker.js?worker'
import { useTheme } from '../../../utils/theme'

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
const { resolvedTheme } = useTheme()
let disposed = false
let editor: Monaco.editor.IStandaloneCodeEditor | null = null
let monacoModule: typeof Monaco | null = null
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

function applyEditorTheme() {
  if (!monacoModule) return
  const style = getComputedStyle(document.documentElement)
  const color = (name: string) => style.getPropertyValue(`--db-editor-${name}`).trim()
  const token = (name: string) => color(name).replace('#', '')
  const name = `dbhub-${resolvedTheme.value}`
  monacoModule.editor.defineTheme(name, {
    base: resolvedTheme.value === 'dark' ? 'vs-dark' : 'vs', inherit: true,
    rules: [
      { token: 'keyword', foreground: token('keyword') },
      { token: 'keyword.sql', foreground: token('keyword') },
      { token: 'predefined.sql', foreground: token('keyword') },
      { token: 'string', foreground: token('string') },
      { token: 'string.sql', foreground: token('string') },
      { token: 'number', foreground: token('number') },
      { token: 'number.sql', foreground: token('number') },
      { token: 'comment', foreground: token('comment') },
      { token: 'comment.sql', foreground: token('comment') },
      { token: 'comment.quote.sql', foreground: token('comment') },
      { token: 'delimiter.sql', foreground: token('text') },
      { token: 'operator.sql', foreground: token('text') },
      { token: 'identifier.quote.sql', foreground: token('text') },
    ],
    colors: {
      'editor.background': color('bg'), 'editor.foreground': color('text'),
      'editorLineNumber.foreground': color('muted'), 'editorLineNumber.activeForeground': color('text'),
      'editor.selectionBackground': color('selection'), 'editor.lineHighlightBackground': color('line'),
      'editorCursor.foreground': color('keyword'), 'editorGutter.background': color('bg'),
      'editorWidget.background': color('bg'), 'editorWidget.foreground': color('text'),
      'editorSuggestWidget.background': color('bg'), 'editorSuggestWidget.foreground': color('text'),
      'editorSuggestWidget.selectedBackground': color('selection'),
    },
  })
  monacoModule.editor.setTheme(name)
}

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
    monacoModule = monaco
    applyEditorTheme()
    editor = monaco.editor.create(container.value, {
      value: props.modelValue, language: 'sql', theme: `dbhub-${resolvedTheme.value}`, ariaLabel: 'SQL 编辑器',
      fontSize: 14, lineHeight: 24, fontFamily: 'SFMono-Regular, Consolas, Menlo, monospace',
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

// 切换外观仅更新绘制主题，保留模型、选区与撤销历史。
watch(resolvedTheme, applyEditorTheme, { flush: 'post' })

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

<template>
  <div class="relative flex h-full min-h-0 w-full flex-col overflow-hidden">
    <div v-show="!fallback" ref="container" class="min-h-0 flex-1" />
    <LoadingState v-if="!ready && !fallback" variant="detail" label="编辑器加载中…" />
    <template v-if="fallback">
      <p class="px-4 pt-2 text-xs text-warning">高级编辑器加载失败，已切换为基础编辑器。</p>
      <textarea
        ref="textarea"
        :value="modelValue"
        aria-label="SQL 编辑器"
        placeholder="在此输入 SQL，例如 SELECT 1;"
        spellcheck="false"
        class="min-h-0 flex-1 resize-none bg-transparent p-4 font-mono text-sm leading-6 text-accent outline-none"
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
    <p v-if="ready && !modelValue" class="pointer-events-none absolute left-[68px] top-3 text-sm text-subtle" aria-hidden="true">在此输入 SQL，例如 SELECT 1;</p>
  </div>
</template>
