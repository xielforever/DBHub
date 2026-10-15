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
        <button class="liquid-button !px-3 self-end" :disabled="!input.trim() || sending || !configured" @click="send">
          <Send class="w-4 h-4" />
        </button>
      </div>
      <p class="text-[10px] text-white/25">{{ configured ? "请求将发送当前 SQL 和所选表名；建议需审阅后手动执行" : "模型服务尚未配置，可稍后在云环境设置" }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref, onMounted } from 'vue'
import { advancedApi } from '../../../api/advanced'
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

const configured = ref(false)
onMounted(async () => { try { configured.value = (await advancedApi.aiStatus()).configured } catch { configured.value = false } })
const quickPrompts = [
 { label: '生成查询', icon: Search, prompt: '根据所选表生成 SQL，未知列请说明' },
 { label: '解释SQL', icon: Wand2, prompt: '解释当前 SQL' },
 { label: '优化', icon: BarChart3, prompt: '审阅 SQL，给出有依据的优化建议' },
]
function applyQuick(q: (typeof quickPrompts)[number]) { input.value = q.prompt; send() }
async function send() {
 if (!configured.value) { ElMessage.info('模型服务尚未配置'); return }
 const text = input.value.trim(); if (!text || sending.value) return
 sending.value = true; messages.value.push({ role: 'user', content: text }); input.value = ''
 try {
  const res = await advancedApi.ai({ prompt: text, sql: props.currentSql, dialect: props.currentConnection?.type, tables: props.tables })
  const sql = res.content.match(/```sql\s*([\s\S]*?)```/i)?.[1]?.trim()
  messages.value.push({ role: 'assistant', content: res.content, sql })
 } catch(e: unknown) { ElMessage.error(e instanceof Error ? e.message : '模型请求失败') }
 finally { sending.value = false; scrollBottom() }
}
function scrollBottom() { nextTick(() => { if(chatRef.value) chatRef.value.scrollTop = chatRef.value.scrollHeight }) }
function copyText(text: string) { navigator.clipboard.writeText(text).then(() => ElMessage.success('已复制')) }
</script>
