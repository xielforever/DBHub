<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-white/[0.02]">
      <div class="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-400/30 flex items-center justify-center">
        <ListOrdered class="w-3.5 h-3.5 text-indigo-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">查询队列</p>
        <p class="text-[10px] text-white/40">并发 {{ concurrency }} · {{ pendingCount }} 等待 · {{ runningCount }} 执行中</p>
      </div>
      <div class="flex items-center gap-1.5">
        <span class="text-[10px] px-1.5 py-0.5 rounded-full" :class="paused ? 'bg-amber-500/20 text-amber-300' : 'bg-emerald-500/20 text-emerald-300'">{{ paused ? '已暂停' : '运行中' }}</span>
        <button class="ghost-button !py-1 !px-2 text-[11px]" @click="emit('togglePause')">{{ paused ? '继续' : '暂停' }}</button>
        <button class="ghost-button !py-1 !px-2 text-[11px]" :disabled="!queue.length" @click="emit('clear')"><Trash2 class="w-3 h-3" /></button>
      </div>
    </div>

    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-black/20">
      <span class="text-[11px] text-white/40">最大并发</span>
      <input type="range" :value="concurrency" min="1" max="5" step="1" class="flex-1 accent-indigo-400" @input="onConcurrencyChange(($event.target as HTMLInputElement).value)" />
      <span class="text-xs font-mono text-white/70 w-4 text-center">{{ concurrency }}</span>
      <div class="flex-1" />
      <label class="flex items-center gap-1.5 text-[11px] text-white/50 cursor-pointer">
        <input type="checkbox" :checked="queueEnabled" @change="emit('toggleEnabled')" />
        启用队列
      </label>
    </div>

    <div class="flex-1 overflow-auto">
      <div v-if="!queue.length" class="flex flex-col items-center justify-center py-14 gap-2 text-white/35">
        <ListOrdered class="w-8 h-8 text-white/15" />
        <p class="text-xs">队列为空</p>
        <p class="text-[11px] text-white/25">执行中再次点击「运行」将自动入队</p>
        <button class="ghost-button !py-1 !px-3 text-[11px] mt-1" @click="emit('addDemo')">加入演示任务</button>
      </div>

      <div v-for="(item, idx) in queue" :key="item.id" class="group px-3 py-2.5 border-b border-white/5 hover:bg-white/5 flex items-center gap-3" :class="{ 'bg-amber-500/5': item.status === 'running', 'opacity-60': item.status === 'canceled' || item.status === 'failed' }">
        <span class="text-[11px] font-mono text-white/30 w-5 text-center">{{ idx + 1 }}</span>
        <div class="w-2 h-2 rounded-full shrink-0" :class="statusDot(item.status)" />
        <div class="flex-1 min-w-0">
          <p class="text-xs font-mono text-white/70 truncate" :title="item.sql">{{ item.sql.replace(/\s+/g,' ').slice(0,120) }}</p>
          <p class="text-[10px] text-white/30 flex items-center gap-2 mt-0.5">
            <span class="flex items-center gap-1"><Database class="w-3 h-3" />{{ item.connectionName }} · {{ item.database || '默认' }}</span>
            <span>{{ formatTime(item.enqueuedAt) }}</span>
            <span v-if="item.duration" :class="item.status === 'success' ? 'text-emerald-300/70' : 'text-white/30'">{{ item.duration }}ms</span>
            <span v-if="item.status === 'pending'" class="text-indigo-300/60">等待中 · 预计 {{ estimateWait(idx) }}</span>
          </p>
          <p v-if="item.error" class="text-[11px] text-rose-300/70 truncate">{{ item.error }}</p>
        </div>
        <span class="px-1.5 py-0.5 rounded text-[10px] shrink-0" :class="statusBadge(item.status)">{{ statusLabel(item.status) }}</span>
        <div class="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
          <button v-if="item.status === 'pending'" class="ghost-button !py-1 !px-1.5 text-[10px]" @click="emit('moveUp', item.id)" :disabled="idx===0"><ArrowUp class="w-3 h-3" /></button>
          <button v-if="item.status === 'pending'" class="ghost-button !py-1 !px-1.5 text-[10px]" @click="emit('moveDown', item.id)" :disabled="idx===queue.length-1"><ArrowDown class="w-3 h-3" /></button>
          <button v-if="item.status === 'pending' || item.status === 'running'" class="ghost-button !py-1 !px-1.5 text-[10px] text-amber-300/70" @click="emit('cancel', item.id)"><Square class="w-3 h-3" /></button>
          <button v-if="item.status === 'success' || item.status === 'failed' || item.status === 'canceled'" class="ghost-button !py-1 !px-1.5 text-[10px]" @click="emit('requeue', item.id)"><RotateCcw class="w-3 h-3" /></button>
          <button class="ghost-button !py-1 !px-1.5 text-[10px] text-rose-300/60" @click="emit('remove', item.id)"><Trash2 class="w-3 h-3" /></button>
        </div>
      </div>
    </div>

    <div class="px-3 py-2 border-t border-white/10 flex items-center gap-2 text-[11px] text-white/35 shrink-0 bg-white/[0.02]">
      <span>队列 {{ queue.length }} 项</span>
      <span class="w-px h-3 bg-white/10" />
      <span>运行 {{ runningCount }} / {{ concurrency }}</span>
      <div class="flex-1" />
      <span v-if="nextEta" class="text-indigo-300/60">下一任务约 {{ nextEta }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { ArrowDown, ArrowUp, Database, ListOrdered, RotateCcw, Square, Trash2 } from 'lucide-vue-next'

export interface QueueItem {
  id: number
  sql: string
  database: string
  connectionId: number
  connectionName: string
  status: 'pending' | 'running' | 'success' | 'failed' | 'canceled'
  enqueuedAt: string
  startedAt?: string
  finishedAt?: string
  duration?: number
  error?: string
}

const props = defineProps<{
  queue: QueueItem[]
  concurrency: number
  paused: boolean
  queueEnabled: boolean
}>()

const emit = defineEmits<{
  (e: 'togglePause'): void
  (e: 'toggleEnabled'): void
  (e: 'clear'): void
  (e: 'cancel', id: number): void
  (e: 'remove', id: number): void
  (e: 'requeue', id: number): void
  (e: 'moveUp', id: number): void
  (e: 'moveDown', id: number): void
  (e: 'updateConcurrency', v: number): void
  (e: 'addDemo'): void
}>()

const pendingCount = computed(() => props.queue.filter(x => x.status === 'pending').length)
const runningCount = computed(() => props.queue.filter(x => x.status === 'running').length)
const nextEta = computed(() => {
  const running = props.queue.find(x => x.status === 'running')
  if (running) return '30秒后'
  if (pendingCount.value) return '立即'
  return ''
})

function statusDot(s: string) {
  return {
    pending: 'bg-indigo-400/60',
    running: 'bg-amber-400 animate-pulse',
    success: 'bg-emerald-400',
    failed: 'bg-rose-400',
    canceled: 'bg-white/20',
  }[s] || 'bg-white/20'
}
function statusBadge(s: string) {
  return {
    pending: 'bg-indigo-500/15 text-indigo-300',
    running: 'bg-amber-500/15 text-amber-300',
    success: 'bg-emerald-500/15 text-emerald-300',
    failed: 'bg-rose-500/15 text-rose-300',
    canceled: 'bg-white/10 text-white/40',
  }[s] || 'bg-white/10 text-white/40'
}
function statusLabel(s: string) {
  return { pending: '等待', running: '执行中', success: '成功', failed: '失败', canceled: '已取消' }[s] || s
}
function estimateWait(idx: number) {
  const pendingIdx = props.queue.slice(0, idx).filter(x => x.status === 'pending').length
  if (pendingIdx === 0) return '下一批'
  return `${pendingIdx * 2}秒后`
}
function formatTime(s: string) {
  return new Date(s).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
}
function onConcurrencyChange(v: string) {
  emit('updateConcurrency', Number(v))
}
</script>
