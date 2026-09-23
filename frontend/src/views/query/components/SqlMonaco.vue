<template>
  <div class="relative w-full h-full min-h-[180px] flex flex-col overflow-hidden">
    <div ref="containerRef" class="flex-1 w-full min-h-0" />
    <div v-if="!monacoLoaded" class="absolute inset-0 flex items-center justify-center text-xs text-white/30">
      编辑器加载中…
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    modelValue: string
    tables?: string[]
    columns?: string[]
    placeholder?: string
    minimap?: boolean
    theme?: string
    fontSize?: number
    wordWrap?: 'on' | 'off'
  }>(),
  {
    tables: () => [],
    columns: () => [],
    placeholder: '',
    minimap: false,
    theme: 'dbhub-dark',
    fontSize: 13,
    wordWrap: 'on',
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', v: string): void
  (e: 'run'): void
  (e: 'format'): void
  (e: 'selection-change', len: number): void
}>()

const containerRef = ref<HTMLDivElement | null>(null)
const monacoLoaded = ref(false)
let editor: any = null
let monaco: any = null
let completionDisposable: any = null
let resizeObserver: ResizeObserver | null = null

const SQL_KEYWORDS = [
  'SELECT', 'FROM', 'WHERE', 'AND', 'OR', 'INSERT', 'INTO', 'VALUES', 'UPDATE', 'SET', 'DELETE',
  'CREATE', 'TABLE', 'ALTER', 'DROP', 'INDEX', 'VIEW', 'JOIN', 'INNER', 'LEFT', 'RIGHT', 'OUTER',
  'ON', 'GROUP BY', 'ORDER BY', 'HAVING', 'LIMIT', 'OFFSET', 'UNION', 'ALL', 'DISTINCT', 'AS',
  'CASE', 'WHEN', 'THEN', 'ELSE', 'END', 'BETWEEN', 'IN', 'IS', 'NULL', 'NOT', 'LIKE', 'ILIKE',
  'EXISTS', 'COUNT', 'SUM', 'AVG', 'MIN', 'MAX', 'COALESCE', 'NOW', 'CURRENT_TIMESTAMP',
]

async function init() {
  // 配置 worker，Vite 方式 - 仅需 editor worker 即可支持 SQL
  const monacoModule = await import('monaco-editor')
  monaco = monacoModule

  // 简化：不自定义 MonacoEnvironment，使用 monaco 默认，Vite 会自动处理 worker
  // 如需自定义，可在 vite.config.ts 中配置，此处留空避免 rolldown 解析失败

  monaco.editor.defineTheme('dbhub-dark', {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: 'keyword', foreground: 'A78BFA' },
      { token: 'string', foreground: '6EE7B7' },
      { token: 'number', foreground: 'FBBF24' },
      { token: 'comment', foreground: '6B7280', fontStyle: 'italic' },
    ],
    colors: {
      'editor.background': '#0E0E1A',
      'editor.foreground': '#E0E7FF',
      'editorLineNumber.foreground': '#4B5563',
      'editorLineNumber.activeForeground': '#A5B4FC',
      'editor.selectionBackground': '#6366F140',
      'editor.inactiveSelectionBackground': '#6366F120',
      'editor.lineHighlightBackground': '#FFFFFF08',
      'editorCursor.foreground': '#A78BFA',
      'editorGutter.background': '#0E0E1A',
      'scrollbar.shadow': '#00000000',
      'scrollbarSlider.background': '#FFFFFF15',
      'scrollbarSlider.hoverBackground': '#FFFFFF25',
    },
  })
  monaco.editor.defineTheme('dbhub-light', {
    base: 'vs',
    inherit: true,
    rules: [
      { token: 'keyword', foreground: '7C3AED' },
      { token: 'string', foreground: '059669' },
      { token: 'number', foreground: 'D97706' },
    ],
    colors: {
      'editor.background': '#FFFFFF',
      'editor.foreground': '#1F2937',
      'editorLineNumber.foreground': '#9CA3AF',
      'editor.selectionBackground': '#A5B4FC40',
      'editor.lineHighlightBackground': '#F3F4F6',
    },
  })
  monaco.editor.defineTheme('dbhub-hc', {
    base: 'hc-black',
    inherit: true,
    rules: [],
    colors: {},
  })

  if (!containerRef.value) return

  editor = monaco.editor.create(containerRef.value, {
    value: props.modelValue || '',
    language: 'sql',
    theme: props.theme || 'dbhub-dark',
    fontSize: props.fontSize || 13,
    lineHeight: 20,
    fontFamily: 'JetBrains Mono, Fira Code, Cascadia Code, Menlo, monospace',
    minimap: { enabled: !!props.minimap },
    wordWrap: props.wordWrap || 'on',
    scrollBeyondLastLine: false,
    automaticLayout: false,
    tabSize: 2,
    lineNumbers: 'on',
    glyphMargin: false,
    folding: true,
    renderLineHighlight: 'line',
    padding: { top: 12, bottom: 12 },
    suggest: {
      showKeywords: true,
      showSnippets: true,
    },
    quickSuggestions: true,
  })

  monacoLoaded.value = true

  // 内容同步
  editor.onDidChangeModelContent(() => {
    const v = editor.getValue()
    if (v !== props.modelValue) emit('update:modelValue', v)
  })
  // 选中变化
  editor.onDidChangeCursorSelection((e: any) => {
    try {
      const model = editor.getModel()
      const sel = model?.getValueInRange(e.selection) || ''
      emit('selection-change', sel.length)
    } catch {}
  })

  // 快捷键：Ctrl+Enter 运行
  editor.addAction({
    id: 'run-query',
    label: '运行查询',
    keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter],
    run: () => emit('run'),
  })
  // 格式化
  editor.addAction({
    id: 'format-sql',
    label: '格式化 SQL',
    keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.KeyF],
    run: () => emit('format'),
  })

  // 自动补全
  completionDisposable = monaco.languages.registerCompletionItemProvider('sql', {
    triggerCharacters: ['.', ' ', '_'],
    provideCompletionItems: (model: any, position: any) => {
      const word = model.getWordUntilPosition(position)
      const range = {
        startLineNumber: position.lineNumber,
        endLineNumber: position.lineNumber,
        startColumn: word.startColumn,
        endColumn: word.endColumn,
      }
      const suggestions: any[] = []
      SQL_KEYWORDS.forEach((k) => {
        suggestions.push({
          label: k,
          kind: monaco.languages.CompletionItemKind.Keyword,
          insertText: k,
          range,
        })
      })
      props.tables.forEach((t) => {
        suggestions.push({
          label: t,
          kind: monaco.languages.CompletionItemKind.Class,
          insertText: t,
          detail: '表',
          range,
        })
      })
      props.columns.forEach((c) => {
        suggestions.push({
          label: c,
          kind: monaco.languages.CompletionItemKind.Field,
          insertText: c,
          detail: '列',
          range,
        })
      })
      return { suggestions }
    },
  })

  // resize
  resizeObserver = new ResizeObserver(() => {
    editor?.layout()
  })
  resizeObserver.observe(containerRef.value)

  // 占位提示：若为空且有 placeholder，用装饰器显示
  if (!props.modelValue && props.placeholder) {
    // 简易：在首行显示 placeholder 样式（通过 overlay）
  }
}

watch(
  () => props.modelValue,
  (v) => {
    if (editor && v !== editor.getValue()) {
      editor.setValue(v)
    }
  },
)

watch(
  () => props.minimap,
  (v) => {
    try {
      editor?.updateOptions({ minimap: { enabled: !!v } })
    } catch {}
  },
)
watch(() => props.theme, (v) => {
  try { monaco?.editor.setTheme(v || 'dbhub-dark') } catch {}
})
watch(() => props.fontSize, (v) => {
  try { editor?.updateOptions({ fontSize: v || 13 }) } catch {}
})
watch(() => props.wordWrap, (v) => {
  try { editor?.updateOptions({ wordWrap: v || 'on' }) } catch {}
})

watch(
  () => [props.tables, props.columns],
  () => {
    // 补全会自动读取最新 props，无需重建
  },
)

onMounted(() => {
  init()
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  completionDisposable?.dispose?.()
  editor?.dispose?.()
})

defineExpose({
  focus: () => editor?.focus(),
  getSelection: () => {
    if (!editor) return ''
    const sel = editor.getSelection()
    if (!sel) return ''
    return editor.getModel()?.getValueInRange(sel) || ''
  },
  insertText: (text: string) => {
    if (!editor) return
    const sel = editor.getSelection()
    const op = { range: sel, text, forceMoveMarkers: true }
    editor.executeEdits('insert', [op])
    editor.focus()
  },
})
</script>

<style scoped>
:deep(.monaco-editor) {
  --vscode-editor-background: transparent;
}
</style>
