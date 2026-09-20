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

    <div class="px-3 py-2 border-b border-white/10 bg-white/[0.02] flex flex-col gap-2 shrink-0">
      <div class="flex flex-wrap gap-1.5">
        <button
          v-for="q in quickPrompts"
          :key="q.label"
          class="ghost-button !py-1 !px-2 text-[11px] flex items-center gap-1"
          @click="applyQuick(q)"
        >
          <component :is="q.icon" class="w-3 h-3" /> {{ q.label }}
        </button>
      </div>
      <div v-if="currentConnection" class="text-[11px] text-white/40 flex items-center gap-2">
        <span class="px-1.5 py-0.5 rounded bg-white/10">{{ currentConnection.name }}</span>
        <span v-if="tables.length" class="truncate">{{ tables.slice(0,3).join(', ') }}{{ tables.length>3 ? '…' : '' }}</span>
      </div>
    </div>

    <div class="flex-1 overflow-y-auto p-3 space-y-3" ref="chatRef">
      <div v-for="(msg, idx) in messages" :key="idx" :class="['flex', msg.role === 'user' ? 'justify-end' : 'justify-start']">
        <div
          :class="[
            'glass-card !rounded-2xl p-3 text-xs max-w-[90%] whitespace-pre-wrap break-words',
            msg.role === 'user' ? 'text-white/80 ml-auto' : 'text-white/70',
          ]"
          :style="msg.role === 'user' ? 'background-image: var(--image-glass-gradient)' : ''"
        >
          {{ msg.content }}
          <div v-if="msg.role === 'assistant' && msg.sql" class="mt-2 flex gap-1">
            <button class="liquid-button !py-1 !px-2 text-[11px] flex items-center gap-1" @click="emit('insert-sql', msg.sql!)">
              <Plus class="w-3 h-3" /> 插入编辑器
            </button>
            <button class="ghost-button !py-1 !px-2 text-[11px]" @click="copyText(msg.sql!)">复制</button>
          </div>
        </div>
      </div>
      <div v-if="!messages.length" class="space-y-2">
        <div class="glass-card !rounded-2xl p-3 text-xs text-white/60">
          你好！我可以帮你生成 SQL、解释执行计划与优化慢查询。<br/>
          已注入上下文：{{ currentConnection?.name || '未选择连接' }}，选中表 {{ tables.length }} 个。
        </div>
      </div>
    </div>

    <div class="p-3 border-t border-white/10 shrink-0 flex flex-col gap-2">
      <div class="flex gap-2">
        <textarea
          v-model="input"
          rows="2"
          class="glass-input !py-2 text-xs flex-1 resize-none"
          placeholder="输入自然语言，例如：查询订单表近7天销售额"
          @keydown.enter.exact.prevent="send"
          @keydown.ctrl.enter.prevent="send"
        />
        <button class="liquid-button !px-3 self-end" :disabled="!input.trim() || sending" @click="send">
          <Send class="w-4 h-4" />
        </button>
      </div>
      <p class="text-[10px] text-white/25">AI 为占位实现，M5 接入真实模型；当前按规则生成示例 SQL</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { PanelRightClose, Sparkles, Wand2, Search, BarChart3, Plus, Send } from 'lucide-vue-next'
import { ElMessage } from 'element-plus'
import type { ConnectionItem } from '../../../api/datasource'

const props = withDefaults(
  defineProps<{
    embedded?: boolean
    closable?: boolean
    currentConnection?: ConnectionItem | null
    currentSql?: string
    tables?: string[]
  }>(),
  {
    embedded: false,
    closable: true,
    currentConnection: null,
    currentSql: '',
    tables: () => [],
  },
)

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'insert-sql', sql: string): void
}>()

interface ChatMsg {
  role: 'user' | 'assistant'
  content: string
  sql?: string
}

const messages = ref<ChatMsg[]>([])
const input = ref('')
const sending = ref(false)
const chatRef = ref<HTMLDivElement | null>(null)

const quickPrompts = [
  { label: '生成查询', icon: Search, prompt: '根据当前表生成一条查询示例', gen: () => genSelect() },
  { label: '解释SQL', icon: Wand2, prompt: '解释当前编辑器中的SQL', gen: () => explainSQL() },
  { label: '优化', icon: BarChart3, prompt: '优化当前SQL并给出索引建议', gen: () => optimizeSQL() },
  { label: '生成报表', icon: BarChart3, prompt: '生成报表 SQL', gen: () => genReport() },
  { label: 'INSERT', icon: Plus, prompt: '生成 INSERT 示例', gen: () => genInsert() },
]

function genSelect() {
  const t = props.tables[0] || 'your_table'
  const db = props.currentConnection?.database || ''
  const qualified = db ? `${db}.${t}` : t
  return `SELECT * FROM ${qualified} LIMIT 20;`
}
function genReport() {
  const t = props.tables[0] || 'orders'
  return `SELECT DATE(created_at) as day, COUNT(*) as cnt, SUM(amount) as total\nFROM ${t}\nWHERE created_at >= NOW() - INTERVAL '30 days'\nGROUP BY 1 ORDER BY 1;`
}
function genInsert() {
  const t = props.tables[0] || 'your_table'
  return `INSERT INTO ${t} (id, name, created_at) VALUES (1, '示例', NOW());`
}
function explainSQL() {
  const sql = (props.currentSql || '').slice(0, 200) || 'SELECT * FROM table'
  return `-- 解释：\n-- ${sql}\n-- 该查询扫描全表，建议添加 WHERE 过滤条件与 LIMIT`
}
function optimizeSQL() {
  const sql = props.currentSql || 'SELECT * FROM orders'
  return `-- 优化建议：\n-- 原始：${sql.slice(0, 100)}\n-- 1. 避免 SELECT *，明确列名\n-- 2. 为 WHERE 条件添加索引\n-- 3. 大表分页使用 keyset\nSELECT id, amount, created_at FROM orders WHERE created_at >= NOW() - INTERVAL '7 days' ORDER BY id DESC LIMIT 100;`
}

function applyQuick(q: (typeof quickPrompts)[0]) {
  const sql = q.gen()
  messages.value.push({ role: 'user', content: q.prompt })
  messages.value.push({ role: 'assistant', content: `已生成示例：\n${sql}`, sql })
  scrollBottom()
}

function detectIntent(text: string): { type: string; sql: string; explain: string } {
  const t = props.tables[0] || 'orders'
  const lower = text.toLowerCase()
  if (lower.includes('top') || text.includes('前') || text.includes('排行')) {
    return {
      type: 'top',
      sql: `SELECT * FROM ${t} ORDER BY id DESC LIMIT 10;`,
      explain: `按 ${t} 倒序取前 10 条，常用于排行榜场景`
    }
  }
  if (lower.includes('count') || text.includes('总数') || text.includes('多少')) {
    return {
      type: 'count',
      sql: `SELECT COUNT(*) as total FROM ${t};`,
      explain: `统计 ${t} 总行数`
    }
  }
  if (lower.includes('group') || text.includes('分组') || text.includes('分布')) {
    const col = props.tables[1] || 'status'
    return {
      type: 'group',
      sql: `SELECT ${col}, COUNT(*) as cnt FROM ${t} GROUP BY ${col} ORDER BY cnt DESC;`,
      explain: `按 ${col} 分组统计分布`
    }
  }
  if (text.includes('近7天') || lower.includes('7 days') || text.includes('周')) {
    return {
      type: 'trend',
      sql: `SELECT DATE(created_at) as day, COUNT(*) as cnt, SUM(amount) as total\nFROM ${t}\nWHERE created_at >= NOW() - INTERVAL '7 days'\nGROUP BY 1 ORDER BY 1;`,
      explain: `近7天趋势，建议用折线图展示`
    }
  }
  if (text.includes('近30天') || lower.includes('30 days') || text.includes('月')) {
    return {
      type: 'trend',
      sql: `SELECT DATE(created_at) as day, COUNT(*) as cnt, SUM(amount) as total\nFROM ${t}\nWHERE created_at >= NOW() - INTERVAL '30 days'\nGROUP BY 1 ORDER BY 1;`,
      explain: `近30天趋势，建议用柱状图或折线图`
    }
  }
  if (lower.includes('join') || text.includes('关联')) {
    const t2 = props.tables[1] || 'users'
    return {
      type: 'join',
      sql: `SELECT a.*, b.name as ${t2}_name\nFROM ${t} a\nLEFT JOIN ${t2} b ON a.user_id = b.id\nLIMIT 100;`,
      explain: `${t} 关联 ${t2} 查询示例`
    }
  }
  if (text.includes('慢') || lower.includes('slow') || lower.includes('optim')) {
    const sql = props.currentSql || `SELECT * FROM ${t}`
    return {
      type: 'optimize',
      sql: `${sql.includes('LIMIT') ? sql : sql + ' LIMIT 100'}`,
      explain: `优化建议：1) 避免 SELECT * 2) 添加 WHERE 过滤 3) 为常用过滤列建索引 4) 大表用 keyset 分页`
    }
  }
  return {
    type: 'generic',
    sql: `-- 根据：${text}\nSELECT * FROM ${t} LIMIT 20;`,
    explain: `根据你的描述生成查询，可进一步细化`
  }
}

async function send() {
  const text = input.value.trim()
  if (!text) return
  sending.value = true
  messages.value.push({ role: 'user', content: text })
  input.value = ''
  await nextTick()
  scrollBottom()

  setTimeout(() => {
    const intent = detectIntent(text)
    const content = `${intent.explain}\n\n${intent.sql}`
    messages.value.push({ role: 'assistant', content, sql: intent.sql })
    sending.value = false
    scrollBottom()
    try {
      localStorage.setItem('dbhub_ai_history', JSON.stringify(messages.value.slice(-20)))
    } catch {}
  }, 500)
}

function scrollBottom() {
  nextTick(() => {
    if (chatRef.value) chatRef.value.scrollTop = chatRef.value.scrollHeight
  })
}

function copyText(t: string) {
  navigator.clipboard.writeText(t).then(() => ElMessage.success('已复制'))
}

// 恢复历史
try {
  const raw = localStorage.getItem('dbhub_ai_history')
  if (raw) {
    const parsed = JSON.parse(raw)
    if (Array.isArray(parsed)) messages.value = parsed
  }
} catch {}
</script>
