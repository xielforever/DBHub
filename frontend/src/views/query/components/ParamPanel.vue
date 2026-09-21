<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0">
      <div class="w-6 h-6 rounded-lg bg-amber-500/20 border border-amber-400/30 flex items-center justify-center">
        <Braces class="w-3.5 h-3.5 text-amber-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">查询参数</p>
        <p class="text-[10px] text-white/40">支持 &#123;&#123;param&#125;&#125;、:param、$1 语法</p>
      </div>
      <span v-if="params.length" class="px-2 py-0.5 rounded-full bg-amber-500/20 text-amber-300 text-[11px]">{{ params.length }} 个</span>
    </div>

    <div v-if="!params.length" class="flex-1 flex flex-col items-center justify-center gap-3 p-6 text-center">
      <div class="w-12 h-12 rounded-2xl bg-white/5 border border-white/10 flex items-center justify-center">
        <SearchCode class="w-6 h-6 text-white/20" />
      </div>
      <p class="text-xs text-white/50">未检测到参数</p>
      <p class="text-[11px] text-white/30 leading-5">在 SQL 中使用 <span class="font-mono px-1 py-0.5 rounded bg-white/10 text-amber-300">&#123;&#123;user_id&#125;&#125;</span> 或 <span class="font-mono px-1 py-0.5 rounded bg-white/10 text-amber-300">:status</span> 或 <span class="font-mono px-1 py-0.5 rounded bg-white/10 text-amber-300">$1</span><br/>即可自动识别为可填充参数</p>
      <div class="mt-2 p-2.5 rounded-xl bg-white/5 border border-white/10 text-left w-full">
        <p class="text-[11px] text-white/40 mb-1">示例</p>
        <pre class="text-[11px] font-mono text-white/60 whitespace-pre-wrap">SELECT * FROM orders
WHERE user_id = &#123;&#123;user_id&#125;&#125;
  AND status = :status
  AND created_at > $1</pre>
      </div>
    </div>

    <div v-else class="flex-1 overflow-auto p-3 space-y-3">
      <div class="flex items-center gap-2">
        <button class="ghost-button !py-1 !px-2 text-[11px] flex items-center gap-1" @click="loadTemplates">
          <Library class="w-3 h-3" /> 模板
        </button>
        <button class="ghost-button !py-1 !px-2 text-[11px] flex items-center gap-1" @click="saveAsTemplate">
          <Bookmark class="w-3 h-3" /> 保存模板
        </button>
        <div class="flex-1" />
        <button class="ghost-button !py-1 !px-2 text-[11px]" @click="clearValues">清空</button>
      </div>

      <div v-for="p in params" :key="p.name" class="rounded-xl bg-white/5 border border-white/10 p-2.5">
        <div class="flex items-center justify-between mb-1.5">
          <span class="text-xs font-mono text-amber-300 flex items-center gap-1.5">
            {{ p.raw }}
            <span v-if="p.required" class="w-1 h-1 rounded-full bg-rose-400" title="必填" />
          </span>
          <span class="text-[10px] px-1.5 py-0.5 rounded bg-white/10 text-white/40">{{ p.type }}</span>
        </div>
        <input
          v-model="values[p.name]"
          :type="p.type === 'number' ? 'number' : 'text'"
          :placeholder="p.placeholder"
          class="glass-input !py-1.5 text-xs w-full"
          @keydown.enter="emitApply"
        />
        <p v-if="p.description" class="text-[10px] text-white/30 mt-1">{{ p.description }}</p>
      </div>

      <div class="rounded-xl bg-indigo-500/10 border border-indigo-400/20 p-2.5">
        <p class="text-[11px] text-indigo-300/80 font-medium mb-1 flex items-center gap-1">
          <Eye class="w-3 h-3" /> 预览（替换后）
        </p>
        <pre class="text-[11px] font-mono text-white/60 whitespace-pre-wrap break-all max-h-32 overflow-auto">{{ previewSql }}</pre>
      </div>

      <div class="flex gap-2">
        <button class="liquid-button flex-1 !py-2 text-xs flex items-center justify-center gap-1.5" :disabled="!allFilled" @click="emitApply">
          <Play class="w-3.5 h-3.5" /> 应用参数并执行
        </button>
        <button class="ghost-button !py-2 !px-3 text-xs" @click="emitCopy">复制 SQL</button>
      </div>

      <div v-if="templates.length" class="space-y-1.5">
        <p class="text-[11px] text-white/40">已保存模板 · {{ templates.length }}</p>
        <div v-for="t in templates" :key="t.id" class="flex items-center gap-2 px-2.5 py-2 rounded-lg bg-white/5 border border-white/10 hover:bg-white/10 group">
          <div class="flex-1 min-w-0">
            <p class="text-xs text-white/70 truncate">{{ t.name }}</p>
            <p class="text-[10px] text-white/30 truncate">{{ Object.keys(t.values).length }} 参数 · {{ formatTime(t.created_at) }}</p>
          </div>
          <button class="ghost-button !py-1 !px-2 text-[10px] opacity-0 group-hover:opacity-100" @click="applyTemplate(t)">应用</button>
          <button class="ghost-button !py-1 !px-1.5 text-[10px] text-rose-300/60 opacity-0 group-hover:opacity-100" @click="deleteTemplate(t.id)"><Trash2 class="w-3 h-3" /></button>
        </div>
      </div>
    </div>

    <div class="px-3 py-2 border-t border-white/10 bg-white/[0.02] shrink-0">
      <p class="text-[10px] text-white/25 leading-4">💡 参数值会自动持久化到本地；支持保存为模板在团队间复用（本地存储，后端占位）</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Bookmark, Braces, Eye, Library, Play, SearchCode, Trash2 } from 'lucide-vue-next'

interface DetectedParam {
  name: string
  raw: string
  type: 'string' | 'number' | 'date'
  placeholder: string
  required: boolean
  description?: string
}

interface ParamTemplate {
  id: number
  name: string
  sql_hash: string
  values: Record<string, string>
  created_at: string
}

const props = defineProps<{
  sql: string
}>()

const emit = defineEmits<{
  (e: 'apply', values: Record<string, string>, finalSql: string): void
  (e: 'update:values', values: Record<string, string>): void
}>()

const STORAGE_TEMPLATES = 'dbhub_param_templates'
const STORAGE_VALUES = 'dbhub_param_values'

const values = reactive<Record<string, string>>({})
const templates = ref<ParamTemplate[]>([])

function detectParams(sql: string): DetectedParam[] {
  const found = new Map<string, DetectedParam>()
  // {{param}}
  const re1 = /\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}/g
  let m: RegExpExecArray | null
  while ((m = re1.exec(sql)) !== null) {
    const name = m[1]!
    if (!found.has(name)) {
      found.set(name, { name, raw: m[0]!, type: inferType(name), placeholder: placeholderFor(name), required: true })
    }
  }
  // :param (but not :: and not :1)
  const re2 = /(?<!:):([a-zA-Z_][a-zA-Z0-9_]*)\b/g
  while ((m = re2.exec(sql)) !== null) {
    const name = m[1]!
    if (['true','false','null'].includes(name.toLowerCase())) continue
    if (!found.has(name)) {
      found.set(name, { name, raw: `:${name}`, type: inferType(name), placeholder: placeholderFor(name), required: true })
    }
  }
  // $1, $2
  const re3 = /\$(\d+)\b/g
  while ((m = re3.exec(sql)) !== null) {
    const name = `$${m[1]}`
    if (!found.has(name)) {
      found.set(name, { name, raw: name, type: 'string', placeholder: `参数 ${name}`, required: false, description: '位置参数 $n' })
    }
  }
  return Array.from(found.values())
}

function inferType(name: string): 'string' | 'number' | 'date' {
  const n = name.toLowerCase()
  if (/(id|count|num|age|price|amount|qty|limit|offset)$/.test(n) || /^(id|count|limit|offset)/.test(n)) return 'number'
  if (/(date|time|at)$/.test(n) || n.includes('date')) return 'date'
  return 'string'
}

function placeholderFor(name: string): string {
  const t = inferType(name)
  if (t === 'number') return '例如：123'
  if (t === 'date') return '例如：2024-01-01'
  return `输入 ${name}`
}

const params = computed(() => detectParams(props.sql))

const previewSql = computed(() => {
  let out = props.sql
  for (const p of params.value) {
    const v = values[p.name]
    if (!v) continue
    const escaped = isNumeric(v) ? v : `'${v.replace(/'/g, "''")}'`
    // replace all occurrences
    if (p.raw.startsWith('{{')) {
      out = out.split(p.raw).join(escaped)
    } else if (p.raw.startsWith(':')) {
      out = out.replace(new RegExp(`(?<!:):${p.name}\\b`, 'g'), escaped)
    } else if (p.raw.startsWith('$')) {
      out = out.split(p.raw).join(escaped)
    }
  }
  return out
})

function isNumeric(s: string): boolean {
  return s !== '' && !isNaN(Number(s)) && !isNaN(parseFloat(s))
}

const allFilled = computed(() => {
  if (!params.value.length) return false
  return params.value.filter(p => p.required).every(p => (values[p.name] || '').trim().length > 0)
})

function emitApply() {
  if (!allFilled.value) {
    ElMessage.warning('请填写所有必填参数')
    return
  }
  emit('apply', { ...values }, previewSql.value)
}

function emitCopy() {
  navigator.clipboard.writeText(previewSql.value).then(() => ElMessage.success('SQL 已复制'))
}

function clearValues() {
  Object.keys(values).forEach(k => delete values[k])
  try { localStorage.removeItem(STORAGE_VALUES) } catch {}
}

function loadTemplates() {
  try {
    const raw = localStorage.getItem(STORAGE_TEMPLATES)
    if (raw) templates.value = JSON.parse(raw)
  } catch {}
}

function saveAsTemplate() {
  const name = `参数模板 ${new Date().toLocaleString('zh-CN')}`
  const t: ParamTemplate = {
    id: Date.now(),
    name,
    sql_hash: hashSql(props.sql),
    values: { ...values },
    created_at: new Date().toISOString(),
  }
  templates.value.unshift(t)
  try {
    localStorage.setItem(STORAGE_TEMPLATES, JSON.stringify(templates.value.slice(0, 30)))
  } catch {}
  ElMessage.success('已保存模板')
}

function applyTemplate(t: ParamTemplate) {
  Object.assign(values, t.values)
  ElMessage.success(`已应用模板：${t.name}`)
}

function deleteTemplate(id: number) {
  templates.value = templates.value.filter(x => x.id !== id)
  try { localStorage.setItem(STORAGE_TEMPLATES, JSON.stringify(templates.value)) } catch {}
}

function hashSql(s: string): string {
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0
  return h.toString(16)
}

function formatTime(s: string): string {
  return new Date(s).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

// persist values
watch(values, () => {
  try { localStorage.setItem(STORAGE_VALUES, JSON.stringify(values)) } catch {}
  emit('update:values', { ...values })
}, { deep: true })

// load persisted
try {
  const raw = localStorage.getItem(STORAGE_VALUES)
  if (raw) Object.assign(values, JSON.parse(raw))
} catch {}
loadTemplates()

// when sql changes, ensure values for removed params are kept but not required
watch(() => props.sql, () => {
  // keep values, but no auto clear
})
</script>
