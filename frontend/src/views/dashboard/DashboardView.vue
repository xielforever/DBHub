<template>
  <div class="space-y-6">
    <!-- 欢迎条 -->
    <header class="flex flex-col sm:flex-row sm:items-end justify-between gap-2">
      <div>
        <h1 class="text-xl font-semibold">早上好，{{ userStore.username || '管理员' }}</h1>
        <p class="text-sm text-white/45 mt-1">以下是平台今日运行概览（示例数据）</p>
      </div>
      <span class="text-xs text-white/35">{{ today }}</span>
    </header>

    <!-- KPI 卡片 -->
    <section class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-4" aria-label="核心指标">
      <article
        v-for="kpi in kpis"
        :key="kpi.label"
        class="glass-card p-5 hover:-translate-y-1"
      >
        <div class="flex items-start justify-between">
          <div
            class="w-10 h-10 rounded-xl flex items-center justify-center"
            :style="{ background: kpi.tint }"
          >
            <component :is="kpi.icon" class="w-5 h-5" :style="{ color: kpi.color }" />
          </div>
          <span
            class="text-xs px-2 py-0.5 rounded-full"
            :class="kpi.up ? 'bg-emerald-400/15 text-emerald-300' : 'bg-rose-400/15 text-rose-300'"
          >
            {{ kpi.delta }}
          </span>
        </div>
        <p class="mt-4 text-2xl font-bold tracking-tight">{{ kpi.value }}</p>
        <p class="text-xs text-white/45 mt-1">{{ kpi.label }}</p>
        <!-- 迷你柱状走势 -->
        <div class="mt-3 flex items-end gap-1 h-8">
          <span
            v-for="(h, i) in kpi.spark"
            :key="i"
            class="flex-1 rounded-sm opacity-70"
            :style="{ height: h + '%', background: kpi.color }"
          />
        </div>
      </article>
    </section>

    <!-- 图表区 -->
    <section class="grid grid-cols-1 lg:grid-cols-3 gap-4">
      <article class="glass-card p-5 lg:col-span-2">
        <div class="flex items-center justify-between mb-4">
          <h2 class="font-medium">查询趋势（近 14 天）</h2>
          <span class="text-xs text-white/35">ECharts 接入中</span>
        </div>
        <div class="h-56 flex items-end gap-1.5">
          <div
            v-for="(bar, i) in trendBars"
            :key="i"
            class="flex-1 rounded-t-md transition-all duration-500 hover:opacity-100"
            :style="{ height: bar + '%', background: trendGradient(i), opacity: 0.55 + bar / 300 }"
            :title="`第 ${i + 1} 天`"
          />
        </div>
      </article>

      <article class="glass-card p-5">
        <h2 class="font-medium mb-4">数据源类型分布</h2>
        <div class="flex items-center justify-center h-44">
          <div
            class="w-36 h-36 rounded-full"
            style="
              background: conic-gradient(
                #667eea 0 55%,
                #764ba2 55% 80%,
                #ec4899 80% 92%,
                rgba(255, 255, 255, 0.15) 92% 100%
              );
            "
          >
            <div class="w-24 h-24 m-6 rounded-full bg-premium-darker flex flex-col items-center justify-center">
              <span class="text-lg font-bold">24</span>
              <span class="text-[10px] text-white/45">数据源</span>
            </div>
          </div>
        </div>
        <ul class="space-y-2 text-sm">
          <li v-for="row in distribution" :key="row.label" class="flex items-center gap-2">
            <span class="w-2.5 h-2.5 rounded-sm" :style="{ background: row.color }" />
            <span class="text-white/60 flex-1">{{ row.label }}</span>
            <span class="text-white/85">{{ row.value }}</span>
          </li>
        </ul>
      </article>
    </section>

    <!-- 最近查询 -->
    <section class="glass-card p-5">
      <h2 class="font-medium mb-4">最近查询历史</h2>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[560px] text-sm">
          <thead>
            <tr class="text-left text-white/40 text-xs border-b border-white/10">
              <th class="pb-3 font-medium">数据源</th>
              <th class="pb-3 font-medium">SQL 摘要</th>
              <th class="pb-3 font-medium">耗时</th>
              <th class="pb-3 font-medium">行数</th>
              <th class="pb-3 font-medium">时间</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(row, i) in recentQueries"
              :key="i"
              class="border-b border-white/5 last:border-0 hover:bg-white/5 transition-colors"
            >
              <td class="py-3 text-white/80">{{ row.connection }}</td>
              <td class="py-3">
                <code class="text-xs text-indigo-200/80 bg-indigo-400/10 px-2 py-1 rounded">{{ row.sql }}</code>
              </td>
              <td class="py-3 text-white/60">{{ row.duration }}</td>
              <td class="py-3 text-white/60">{{ row.rows }}</td>
              <td class="py-3 text-white/35">{{ row.time }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Activity, Database, Gauge, ScrollText } from 'lucide-vue-next'
import { useUserStore } from '../../stores/user'

const userStore = useUserStore()
const today = computed(() => new Date().toLocaleDateString('zh-CN', { dateStyle: 'full' }))

const kpis = [
  {
    label: '数据源总数', value: '24', delta: '+3 本周', up: true,
    icon: Database, color: '#818cf8', tint: 'rgba(102,126,234,0.18)',
    spark: [40, 55, 48, 62, 58, 72, 80],
  },
  {
    label: '今日查询数', value: '1,284', delta: '+12.4%', up: true,
    icon: Activity, color: '#34d399', tint: 'rgba(16,185,129,0.18)',
    spark: [30, 42, 60, 52, 70, 66, 88],
  },
  {
    label: '活跃会话', value: '37', delta: '+5', up: true,
    icon: Gauge, color: '#f472b6', tint: 'rgba(236,72,153,0.16)',
    spark: [50, 48, 55, 60, 58, 62, 65],
  },
  {
    label: '审计事件', value: '592', delta: '-2.1%', up: false,
    icon: ScrollText, color: '#fbbf24', tint: 'rgba(251,191,36,0.16)',
    spark: [70, 64, 58, 62, 50, 48, 44],
  },
]

const trendBars = [34, 42, 38, 55, 48, 62, 58, 70, 66, 52, 74, 82, 68, 90]
const distribution = [
  { label: 'MySQL', value: 13, color: '#667eea' },
  { label: 'PostgreSQL', value: 7, color: '#764ba2' },
  { label: 'Redis', value: 4, color: '#ec4899' },
]
const recentQueries = [
  { connection: 'sales-prod-mysql', sql: 'SELECT * FROM orders WHERE created_at > ?', duration: '45ms', rows: '2,130', time: '2 分钟前' },
  { connection: 'user-center-pg', sql: 'UPDATE users SET status = ? WHERE id = ?', duration: '12ms', rows: '1', time: '14 分钟前' },
  { connection: 'cache-redis-01', sql: 'GET session:user:8821', duration: '2ms', rows: '1', time: '31 分钟前' },
  { connection: 'sales-prod-mysql', sql: 'SELECT COUNT(*) FROM customers', duration: '180ms', rows: '1', time: '1 小时前' },
]

function trendGradient(index: number) {
  return index % 2 === 0
    ? 'linear-gradient(180deg, #818cf8, #667eea)'
    : 'linear-gradient(180deg, #a78bfa, #764ba2)'
}
</script>
