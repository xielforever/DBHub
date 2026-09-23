<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-white/[0.02]">
      <div class="w-6 h-6 rounded-lg bg-rose-500/20 border border-rose-400/30 flex items-center justify-center">
        <Activity class="w-3.5 h-3.5 text-rose-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">性能监控</p>
        <p class="text-[10px] text-white/40">执行趋势 · 慢查询 · 资源</p>
      </div>
      <button class="ghost-button !py-1 !px-2.5 text-xs flex items-center gap-1" @click="refresh">
        <RefreshCw class="w-3 h-3" :class="{ 'animate-spin': loading }" /> 刷新
      </button>
    </div>

    <div class="flex-1 overflow-auto p-3 space-y-3" v-loading="loading">
      <!-- KPI -->
      <div class="grid grid-cols-4 gap-2">
        <div v-for="k in kpis" :key="k.label" class="rounded-xl bg-white/5 border border-white/10 p-2.5 text-center">
          <p class="text-[10px] text-white/40">{{ k.label }}</p>
          <p class="text-sm font-mono font-medium mt-1" :style="{ color: k.color }">{{ k.value }}</p>
          <p class="text-[10px] mt-0.5" :class="k.trend > 0 ? 'text-rose-300' : 'text-emerald-300'">{{ k.trend > 0 ? '↑' : '↓' }} {{ Math.abs(k.trend) }}%</p>
        </div>
      </div>

      <!-- 执行时间趋势 -->
      <div class="rounded-xl bg-black/30 border border-white/10 overflow-hidden">
        <div class="px-3 py-2 border-b border-white/10 bg-white/5 flex items-center gap-2 text-[11px] text-white/40">
          <span>近 20 次执行耗时趋势</span>
          <div class="flex-1" />
          <span class="px-1.5 py-0.5 rounded bg-white/10">平均 {{ avgDuration }}ms</span>
        </div>
        <div class="p-3">
          <div class="flex items-end gap-1 h-20">
            <div
              v-for="(d, i) in durations"
              :key="i"
              class="flex-1 rounded-t-md transition-all hover:brightness-125 cursor-pointer"
              :style="{ height: (d / maxDuration * 100) + '%', background: d > 200 ? '#F87171' : d > 100 ? '#FBBF24' : '#34D399' }"
              :title="`${d}ms`"
            />
          </div>
          <div class="mt-2 flex justify-between text-[10px] text-white/30">
            <span>20 次前</span>
            <span>当前</span>
          </div>
        </div>
      </div>

      <!-- 慢查询 -->
      <div class="rounded-xl bg-white/5 border border-white/10 overflow-hidden">
        <div class="px-3 py-2 border-b border-white/10 bg-white/5 flex items-center gap-2 text-[11px] text-white/40">
          <span>慢查询 TOP 5</span>
          <div class="flex-1" />
          <span class="px-1.5 py-0.5 rounded bg-rose-500/15 text-rose-300">{{ slowQueries.length }} 条</span>
        </div>
        <div class="divide-y divide-white/5">
          <div v-for="q in slowQueries" :key="q.id" class="px-3 py-2.5 flex items-center gap-2 hover:bg-white/5 cursor-pointer" @click="emit('reuse', q.sql)">
            <span class="px-1.5 py-0.5 rounded text-[10px] bg-rose-500/15 text-rose-300">{{ q.duration }}ms</span>
            <span class="flex-1 text-[11px] font-mono text-white/60 truncate">{{ q.sql }}</span>
            <span class="text-[10px] text-white/30">{{ formatTime(q.time) }}</span>
          </div>
        </div>
      </div>

      <!-- 资源 -->
      <div class="grid grid-cols-2 gap-2">
        <div class="rounded-xl bg-white/5 border border-white/10 p-2.5">
          <p class="text-[11px] text-white/40">连接池</p>
          <div class="mt-2 space-y-1.5">
            <div class="flex justify-between text-[11px]"><span class="text-white/40">活跃</span><span class="text-white/80 font-mono">{{ pool.active }}/{{ pool.total }}</span></div>
            <div class="h-1.5 rounded-full bg-white/10 overflow-hidden">
              <div class="h-full bg-indigo-400" :style="{ width: (pool.active / pool.total * 100) + '%' }" />
            </div>
          </div>
        </div>
        <div class="rounded-xl bg-white/5 border border-white/10 p-2.5">
          <p class="text-[11px] text-white/40">缓存命中</p>
          <div class="mt-2">
            <p class="text-sm font-mono text-emerald-300">{{ cacheHit }}%</p>
            <div class="mt-1.5 h-1.5 rounded-full bg-white/10 overflow-hidden">
              <div class="h-full bg-emerald-400" :style="{ width: cacheHit + '%' }" />
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { Activity, RefreshCw } from 'lucide-vue-next'

const emit = defineEmits<{
  (e: 'reuse', sql: string): void
}>()

const loading = ref(false)
const durations = ref<number[]>([])
const slowQueries = ref<{ id: number; sql: string; duration: number; time: string }[]>([])
const pool = ref({ active: 7, total: 20 })
const cacheHit = ref(87)

const kpis = ref([
  { label: '平均耗时', value: '68ms', color: '#A78BFA', trend: -5 },
  { label: 'P95 耗时', value: '210ms', color: '#FBBF24', trend: 12 },
  { label: '慢查询', value: '3', color: '#F87171', trend: 8 },
  { label: 'QPS', value: '12.4', color: '#34D399', trend: -2 },
])

const maxDuration = computed(() => Math.max(...durations.value, 1))
const avgDuration = computed(() => durations.value.length ? Math.round(durations.value.reduce((a, b) => a + b, 0) / durations.value.length) : 0)

function refresh() {
  loading.value = true
  setTimeout(() => {
    durations.value = Array.from({ length: 20 }, () => 20 + Math.floor(Math.random() * 280))
    slowQueries.value = [
      { id: 1, sql: 'SELECT * FROM orders WHERE created_at < NOW() - INTERVAL 1 YEAR', duration: 420, time: new Date(Date.now() - 3600 * 1000).toISOString() },
      { id: 2, sql: 'SELECT COUNT(*) FROM users WHERE status = 1', duration: 310, time: new Date(Date.now() - 7200 * 1000).toISOString() },
      { id: 3, sql: 'SELECT * FROM orders_archive JOIN users ON ...', duration: 285, time: new Date(Date.now() - 10800 * 1000).toISOString() },
    ]
    pool.value = { active: 5 + Math.floor(Math.random() * 10), total: 20 }
    cacheHit.value = 80 + Math.floor(Math.random() * 15)
    loading.value = false
  }, 600)
}

function formatTime(s: string) {
  return new Date(s).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

onMounted(() => refresh())
</script>
