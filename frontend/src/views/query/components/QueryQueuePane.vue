<script setup lang="ts">
import LoadingState from '../../../components/base/LoadingState.vue'
import { computed, ref, onMounted, onBeforeUnmount } from 'vue'
import { advancedApi, type QueryJob } from '../../../api/advanced'
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

const serverJobs=ref<QueryJob[]>([]),serverError=ref(false),serverLoading=ref(true)
let serverTimer:ReturnType<typeof setInterval>|undefined
async function loadServer(){try{serverJobs.value=(await advancedApi.jobs()).items;serverError.value=false}catch{serverJobs.value=[];serverError.value=true}finally{serverLoading.value=false}}
async function cancelServer(id:string){try{await advancedApi.cancelJob(id);await loadServer()}catch{}}
onMounted(()=>{loadServer();serverTimer=setInterval(loadServer,2000)})
onBeforeUnmount(()=>clearInterval(serverTimer))
const pendingCount = computed(() => props.queue.filter(x => x.status === 'pending').length)
const runningCount = computed(() => props.queue.filter(x => x.status === 'running').length)

function statusDot(s: string) {
  return {
    pending: 'bg-accent',
    running: 'bg-warning animate-pulse',
    success: 'bg-success',
    failed: 'bg-danger',
    canceled: 'bg-surface-muted',
  }[s] || 'bg-surface-muted'
}
function statusBadge(s: string) {
  return {
    pending: 'bg-accent-soft text-accent',
    running: 'bg-warning-soft text-warning',
    success: 'bg-success-soft text-success',
    failed: 'bg-danger-soft text-danger',
    canceled: 'bg-surface-muted text-subtle',
  }[s] || 'bg-surface-muted text-subtle'
}
function statusLabel(s: string) {
  return { pending: '等待', running: '执行中', success: '成功', failed: '失败', canceled: '已取消' }[s] || s
}

function formatTime(s: string) {
  return new Date(s).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
}
function onConcurrencyChange(v: string) {
  emit('updateConcurrency', Number(v))
}
</script>

<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-stroke flex items-center gap-2 shrink-0 bg-surface-muted">
      <div class="w-6 h-6 rounded-lg bg-accent-soft border border-accent/30 flex items-center justify-center">
        <ListOrdered class="w-3.5 h-3.5 text-accent" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-content">浏览器待发队列</p>
        <p class="text-xs text-subtle">并发 {{ concurrency }} · {{ pendingCount }} 等待 · {{ runningCount }} 执行中</p>
      </div>
      <div class="flex items-center gap-1.5">
        <span class="text-xs px-1.5 py-0.5 rounded-full" :class="paused ? 'bg-warning-soft text-warning' : 'bg-success-soft text-success'">{{ paused ? '已暂停' : '运行中' }}</span>
        <button class="ghost-button !py-1 !px-2 text-xs ui-control-target" @click="emit('togglePause')">{{ paused ? '继续' : '暂停' }}</button>
        <button class="ghost-button !py-1 !px-2 text-xs ui-control-target" :disabled="!queue.length" @click="emit('clear')"><Trash2 class="w-3 h-3" /></button>
      </div>
    </div>

    <div class="px-3 py-2 border-b border-stroke flex items-center gap-2 shrink-0 bg-surface-inset">
      <span class="text-xs text-subtle">最大并发</span>
      <input type="range" :value="concurrency" min="1" max="5" step="1" class="flex-1 accent-indigo-400" @input="onConcurrencyChange(($event.target as HTMLInputElement).value)" />
      <span class="text-xs font-mono text-muted w-4 text-center">{{ concurrency }}</span>
      <div class="flex-1" />
      <label class="flex items-center gap-1.5 text-xs text-muted cursor-pointer">
        <input type="checkbox" :checked="queueEnabled" @change="emit('toggleEnabled')" />
        启用队列
      </label>
    </div>

    <div class="flex-1 overflow-auto">
      <div class="p-3 border-b border-stroke space-y-2">
        <p class="text-xs text-accent">服务端执行队列 · 单用户最多 2 个并发</p>
        <LoadingState v-if="serverLoading" variant="detail" label="正在读取服务端查询队列…" /><p v-if="serverError" class="text-xs text-danger">队列状态暂不可用</p>
        <p v-if="!serverLoading && !serverError && !serverJobs.length" class="text-xs text-subtle">暂无服务端任务</p>
        <div v-for="job in serverJobs" :key="job.id" class="rounded-lg bg-surface-muted p-2 flex items-center gap-2"><span class="text-xs text-muted">{{ job.status }}</span><span class="text-xs font-mono text-muted truncate flex-1">{{ job.sql }}</span><button v-if="['pending','running'].includes(job.status)" class="ghost-button text-xs ui-control-target" @click="cancelServer(job.id)">取消</button></div>
      </div>
      <div v-if="!queue.length" class="flex flex-col items-center justify-center py-14 gap-2 text-subtle">
        <ListOrdered class="w-8 h-8 text-subtle" />
        <p class="text-xs">队列为空</p>
        <p class="text-xs text-subtle">执行中再次点击「运行」将自动入队</p>

      </div>

      <div v-for="(item, idx) in queue" :key="item.id" class="group px-3 py-2.5 border-b border-stroke hover:bg-interactive flex items-center gap-3" :class="{ 'bg-warning-soft': item.status === 'running', 'opacity-60': item.status === 'canceled' || item.status === 'failed' }">
        <span class="text-xs font-mono text-subtle w-5 text-center">{{ idx + 1 }}</span>
        <div class="w-2 h-2 rounded-full shrink-0" :class="statusDot(item.status)" />
        <div class="flex-1 min-w-0">
          <p class="text-xs font-mono text-muted truncate" :title="item.sql">{{ item.sql.replace(/\s+/g,' ').slice(0,120) }}</p>
          <p class="text-xs text-subtle flex items-center gap-2 mt-0.5">
            <span class="flex items-center gap-1"><Database class="w-3 h-3" />{{ item.connectionName }} · {{ item.database || '默认' }}</span>
            <span>{{ formatTime(item.enqueuedAt) }}</span>
            <span v-if="item.duration" :class="item.status === 'success' ? 'text-success' : 'text-subtle'">{{ item.duration }}ms</span>
            <span v-if="item.status === 'pending'" class="text-accent">等待发送</span>
          </p>
          <p v-if="item.error" class="text-xs text-danger truncate">{{ item.error }}</p>
        </div>
        <span class="px-1.5 py-0.5 rounded text-xs shrink-0" :class="statusBadge(item.status)">{{ statusLabel(item.status) }}</span>
        <div class="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
          <button v-if="item.status === 'pending'" class="ghost-button !py-1 !px-1.5 text-xs ui-control-target" @click="emit('moveUp', item.id)" :disabled="idx===0"><ArrowUp class="w-3 h-3" /></button>
          <button v-if="item.status === 'pending'" class="ghost-button !py-1 !px-1.5 text-xs ui-control-target" @click="emit('moveDown', item.id)" :disabled="idx===queue.length-1"><ArrowDown class="w-3 h-3" /></button>
          <button v-if="item.status === 'pending' || item.status === 'running'" class="ghost-button !py-1 !px-1.5 text-xs text-warning ui-control-target" @click="emit('cancel', item.id)"><Square class="w-3 h-3" /></button>
          <button v-if="item.status === 'success' || item.status === 'failed' || item.status === 'canceled'" class="ghost-button !py-1 !px-1.5 text-xs ui-control-target" @click="emit('requeue', item.id)"><RotateCcw class="w-3 h-3" /></button>
          <button class="ghost-button !py-1 !px-1.5 text-xs text-danger ui-control-target" @click="emit('remove', item.id)"><Trash2 class="w-3 h-3" /></button>
        </div>
      </div>
    </div>

    <div class="px-3 py-2 border-t border-stroke flex items-center gap-2 text-xs text-subtle shrink-0 bg-surface-muted">
      <span>队列 {{ queue.length }} 项</span>
      <span class="w-px h-3 bg-surface-muted" />
      <span>运行 {{ runningCount }} / {{ concurrency }}</span>
      <div class="flex-1" />

    </div>
  </div>
</template>
