<template>
  <div ref="rootRef" class="space-y-6">
    <!-- 欢迎条 -->
    <header class="flex flex-col sm:flex-row sm:items-end justify-between gap-2">
      <div>
        <h1 class="text-xl font-semibold">早上好，{{ userStore.username || '管理员' }}</h1>
        <p class="text-sm text-white/45 mt-1">以下是平台今日运行概览</p>
      </div>
      <span class="text-xs text-white/45">{{ today }}</span>
    </header>

    <!-- KPI 卡片 -->
    <section class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-4" aria-label="核心指标">
      <article
        v-for="kpi in kpis"
        :key="kpi.label"
        class="kpi-card glass-card p-5 hover:-translate-y-1"
      >
        <div class="flex items-start justify-between">
          <div
            class="w-10 h-10 rounded-xl flex items-center justify-center transition-transform duration-300"
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
        <p class="mt-4 text-2xl font-bold tracking-tight tabular-nums">
          {{ animated[kpi.key]?.toLocaleString() ?? '0' }}
        </p>
        <p class="text-xs text-white/45 mt-1">{{ kpi.label }}</p>
        <EChart class="mt-2" :option="sparkOption(kpi)" height="36px" :aria-label="kpi.label + '走势'" />
      </article>
    </section>

    <!-- 图表区 -->
    <section class="grid grid-cols-1 lg:grid-cols-3 gap-4">
      <!-- 查询趋势 -->
      <article class="chart-card glass-card p-5 lg:col-span-2">
        <div class="flex flex-wrap items-center justify-between gap-3 mb-4">
          <h2 class="font-medium">查询趋势</h2>
          <div class="flex items-center gap-2">
            <!-- 时间范围切换 -->
            <div class="flex p-0.5 rounded-xl bg-white/5 border border-white/10 text-xs">
              <button
                v-for="r in ranges"
                :key="r.value"
                class="px-2.5 py-1 rounded-lg transition-all duration-200"
                :class="rangeDays === r.value ? 'text-white shadow' : 'text-white/45 hover:text-white/80'"
                :style="rangeDays === r.value ? { backgroundImage: 'var(--image-liquid-gradient)' } : {}"
                @click="switchRange(r.value)"
              >
                {{ r.label }}
              </button>
            </div>
            <!-- 图表类型切换 -->
            <div class="flex p-0.5 rounded-xl bg-white/5 border border-white/10">
              <button
                class="p-1.5 rounded-lg transition-all duration-200"
                :class="chartMode === 'line' ? 'text-white bg-white/15' : 'text-white/40 hover:text-white/80'"
                aria-label="折线图"
                @click="chartMode = 'line'"
              >
                <TrendingUp class="w-3.5 h-3.5" />
              </button>
              <button
                class="p-1.5 rounded-lg transition-all duration-200"
                :class="chartMode === 'bar' ? 'text-white bg-white/15' : 'text-white/40 hover:text-white/80'"
                aria-label="柱状图"
                @click="chartMode = 'bar'"
              >
                <BarChart3 class="w-3.5 h-3.5" />
              </button>
            </div>
            <button
              class="p-1.5 rounded-lg text-white/40 hover:text-white transition-colors"
              aria-label="刷新数据"
              @click="refresh"
            >
              <RefreshCw class="w-4 h-4" :class="spinning ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>
        <EChart ref="trendChartRef" :option="trendOption" height="300px" aria-label="查询趋势图" />
      </article>

      <!-- 数据源类型分布 -->
      <article class="chart-card glass-card p-5">
        <h2 class="font-medium mb-2">数据源类型分布</h2>
        <EChart
          ref="donutChartRef"
          :option="donutOption"
          height="230px"
          aria-label="数据源类型分布环形图"
        />
        <ul class="mt-2 space-y-2 text-sm">
          <li
            v-for="(row, i) in distribution"
            :key="row.label"
            class="flex items-center gap-2 px-2 py-1 rounded-lg cursor-pointer transition-colors hover:bg-white/5"
            @mouseenter="highlightSlice(i)"
            @mouseleave="clearHighlight"
          >
            <span class="w-2.5 h-2.5 rounded-sm" :style="{ background: row.color }" />
            <span class="text-white/60 flex-1">{{ row.label }}</span>
            <span class="text-white/85 tabular-nums">{{ row.value }}</span>
            <span class="text-white/35 text-xs w-10 text-right">{{ row.percent }}%</span>
          </li>
        </ul>
      </article>
    </section>

    <!-- 底部：排行 + 最近查询 -->
    <section class="grid grid-cols-1 lg:grid-cols-3 gap-4">
      <article class="chart-card glass-card p-5">
        <h2 class="font-medium mb-4">库查询量排行</h2>
        <EChart :option="rankOption" height="240px" aria-label="库查询量排行条形图" />
      </article>

      <article class="table-card glass-card p-5 lg:col-span-2">
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
                class="history-row border-b border-white/5 last:border-0 hover:bg-white/5 transition-colors"
              >
                <td class="py-3 text-white/80 whitespace-nowrap">{{ row.connection }}</td>
                <td class="py-3">
                  <code class="text-xs text-indigo-200/80 bg-indigo-400/10 px-2 py-1 rounded">{{ row.sql }}</code>
                </td>
                <td class="py-3 text-white/60 whitespace-nowrap">{{ row.duration }}</td>
                <td class="py-3 text-white/60 tabular-nums">{{ row.rows }}</td>
                <td class="py-3 text-white/35 whitespace-nowrap">{{ row.time }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </article>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, shallowRef } from 'vue'
import { gsap } from 'gsap'
import type { EChartsCoreOption } from 'echarts/core'
import {
  Activity,
  BarChart3,
  Database,
  Gauge,
  RefreshCw,
  ScrollText,
  TrendingUp,
} from 'lucide-vue-next'
import EChart from '../../components/base/EChart.vue'
import { useUserStore } from '../../stores/user'

const userStore = useUserStore()
const rootRef = ref<HTMLElement>()
const donutChartRef = shallowRef<InstanceType<typeof EChart>>()

const today = computed(() =>
  new Date().toLocaleDateString('zh-CN', { dateStyle: 'full' }),
)
const prefersReduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches

/* ---------- 玻璃化 tooltip 公共配置 ---------- */
const glassTooltip = {
  backgroundColor: 'rgba(20, 28, 48, 0.94)',
  borderColor: 'rgba(255,255,255,0.14)',
  borderWidth: 1,
  padding: [8, 12],
  textStyle: { color: '#fff', fontSize: 12 },
  extraCssText: 'backdrop-filter: blur(20px); border-radius: 10px; box-shadow: 0 10px 30px rgba(0,0,0,.4);',
}

/* ---------- 确定性伪随机（保证同一范围数据稳定） ---------- */
function seededRandom(seed: number) {
  let s = seed
  return () => {
    s = (s * 9301 + 49297) % 233280
    return s / 233280
  }
}

/* ---------- KPI ---------- */
interface Kpi {
  key: string
  label: string
  value: number
  delta: string
  up: boolean
  icon: unknown
  color: string
  tint: string
  spark: number[]
}
const kpis = reactive<Kpi[]>([
  { key: 'sources', label: '数据源总数', value: 24, delta: '+3 本周', up: true, icon: Database, color: '#818cf8', tint: 'rgba(102,126,234,0.18)', spark: [] },
  { key: 'queries', label: '今日查询数', value: 1284, delta: '+12.4%', up: true, icon: Activity, color: '#34d399', tint: 'rgba(16,185,129,0.18)', spark: [] },
  { key: 'sessions', label: '活跃会话', value: 37, delta: '+5', up: true, icon: Gauge, color: '#f472b6', tint: 'rgba(236,72,153,0.16)', spark: [] },
  { key: 'audits', label: '审计事件', value: 592, delta: '-2.1%', up: false, icon: ScrollText, color: '#fbbf24', tint: 'rgba(251,191,36,0.16)', spark: [] },
])
const animated = reactive<Record<string, number>>({})

function sparkOption(kpi: Kpi): EChartsCoreOption {
  return {
    animationDuration: 1200,
    animationEasing: 'cubicOut',
    grid: { left: 0, right: 0, top: 2, bottom: 0 },
    xAxis: { type: 'category', show: false, boundaryGap: false, data: kpi.spark.map((_, i) => i) },
    yAxis: { type: 'value', show: false, scale: true },
    tooltip: { show: false },
    series: [
      {
        type: 'line',
        data: kpi.spark,
        smooth: true,
        symbol: 'none',
        lineStyle: { width: 2, color: kpi.color },
        areaStyle: {
          color: {
            type: 'linear', x: 0, y: 0, x2: 0, y2: 1,
            colorStops: [
              { offset: 0, color: kpi.color + '55' },
              { offset: 1, color: kpi.color + '00' },
            ],
          },
        },
      },
    ],
  }
}

/* ---------- 查询趋势 ---------- */
const ranges = [
  { label: '近 7 天', value: 7 },
  { label: '近 14 天', value: 14 },
  { label: '近 30 天', value: 30 },
]
const rangeDays = ref(14)
const chartMode = ref<'line' | 'bar'>('line')
const spinning = ref(false)
const trend = reactive<{ labels: string[]; queries: number[]; slow: number[] }>({
  labels: [], queries: [], slow: [],
})

function buildTrend(days: number, salt = 0) {
  const rand = seededRandom(days * 7919 + salt + 1)
  const labels: string[] = []
  const queries: number[] = []
  const slow: number[] = []
  const todayDate = new Date()
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(todayDate)
    d.setDate(todayDate.getDate() - i)
    labels.push(`${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`)
    const weekday = d.getDay()
    const weekendDip = weekday === 0 || weekday === 6 ? 0.55 : 1
    const base = 420 + Math.sin(i / 2.4) * 160 + rand() * 380
    queries.push(Math.round(base * weekendDip))
    slow.push(Math.round(8 + rand() * 34))
  }
  trend.labels = labels
  trend.queries = queries
  trend.slow = slow
}

const axisCommon = {
  axisLine: { lineStyle: { color: 'rgba(255,255,255,0.12)' } },
  axisTick: { show: false },
  axisLabel: { color: 'rgba(255,255,255,0.4)', fontSize: 11 },
}

const trendOption = computed<EChartsCoreOption>(() => {
  const isLine = chartMode.value === 'line'
  return {
    animationDuration: 700,
    animationEasing: 'cubicOut',
    tooltip: {
      trigger: 'axis',
      ...glassTooltip,
      axisPointer: {
        type: isLine ? 'line' : 'shadow',
        lineStyle: { color: 'rgba(255,255,255,0.25)' },
      },
    },
    legend: {
      data: ['查询总量', '慢查询'],
      right: 0,
      top: 0,
      itemWidth: 12,
      itemHeight: 8,
      icon: 'roundRect',
      textStyle: { color: 'rgba(255,255,255,0.55)', fontSize: 11 },
    },
    grid: { left: 8, right: 12, top: 34, bottom: 4, containLabel: true },
    xAxis: { type: 'category', data: trend.labels, boundaryGap: isLine ? false : true, ...axisCommon },
    yAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: 'rgba(255,255,255,0.06)', type: 'dashed' } },
      axisLabel: { color: 'rgba(255,255,255,0.4)', fontSize: 11 },
    },
    series: isLine
      ? [
          {
            name: '查询总量',
            type: 'line',
            smooth: true,
            symbol: 'circle',
            symbolSize: 6,
            showSymbol: false,
            lineStyle: { width: 2.5, color: '#818cf8' },
            itemStyle: { color: '#818cf8', borderColor: '#0f172a', borderWidth: 2 },
            emphasis: { focus: 'series', scale: 1.4 },
            areaStyle: {
              color: {
                type: 'linear', x: 0, y: 0, x2: 0, y2: 1,
                colorStops: [
                  { offset: 0, color: 'rgba(129,140,248,0.45)' },
                  { offset: 1, color: 'rgba(129,140,248,0.02)' },
                ],
              },
            },
            data: trend.queries,
          },
          {
            name: '慢查询',
            type: 'line',
            smooth: true,
            symbol: 'none',
            lineStyle: { width: 2, color: '#fbbf24' },
            itemStyle: { color: '#fbbf24' },
            emphasis: { focus: 'series' },
            areaStyle: {
              color: {
                type: 'linear', x: 0, y: 0, x2: 0, y2: 1,
                colorStops: [
                  { offset: 0, color: 'rgba(251,191,36,0.25)' },
                  { offset: 1, color: 'rgba(251,191,36,0.0)' },
                ],
              },
            },
            data: trend.slow,
          },
        ]
      : [
          {
            name: '查询总量',
            type: 'bar',
            barMaxWidth: 14,
            itemStyle: {
              borderRadius: [5, 5, 0, 0],
              color: {
                type: 'linear', x: 0, y: 0, x2: 0, y2: 1,
                colorStops: [
                  { offset: 0, color: '#a5b4fc' },
                  { offset: 1, color: '#6366f1' },
                ],
              },
            },
            emphasis: { itemStyle: { color: '#818cf8' } },
            data: trend.queries,
          },
          {
            name: '慢查询',
            type: 'bar',
            barMaxWidth: 14,
            itemStyle: { borderRadius: [5, 5, 0, 0], color: '#fbbf24' },
            data: trend.slow,
          },
        ],
  }
})

function switchRange(days: number) {
  rangeDays.value = days
  buildTrend(days)
}

/* ---------- 数据源分布环图（悬停中心联动） ---------- */
const distribution = reactive([
  { label: 'MySQL', value: 13, percent: 54, color: '#818cf8' },
  { label: 'PostgreSQL', value: 7, percent: 29, color: '#a78bfa' },
  { label: 'Redis', value: 4, percent: 17, color: '#f472b6' },
])
const centerValue = ref(24)
const centerName = ref('数据源')

const donutOption = computed<EChartsCoreOption>(() => ({
  animationDuration: 1100,
  animationEasing: 'cubicOut',
  tooltip: {
    trigger: 'item',
    ...glassTooltip,
    formatter: '{b}: {c} 个 ({d}%)',
  },
  title: {
    text: String(centerValue.value),
    subtext: centerName.value,
    left: 'center',
    top: '38%',
    textStyle: { color: '#fff', fontSize: 24, fontWeight: 700 },
    subtextStyle: { color: 'rgba(255,255,255,0.45)', fontSize: 11 },
    itemGap: 4,
  },
  series: [
    {
      type: 'pie',
      radius: ['60%', '78%'],
      center: ['50%', '46%'],
      avoidLabelOverlap: true,
      label: { show: false },
      labelLine: { show: false },
      itemStyle: {
        borderRadius: 7,
        borderColor: '#121a30',
        borderWidth: 4,
      },
      emphasis: {
        scale: true,
        scaleSize: 6,
        itemStyle: {
          shadowBlur: 20,
          shadowColor: 'rgba(102,126,234,0.5)',
        },
      },
      data: distribution.map((d) => ({
        name: d.label,
        value: d.value,
        itemStyle: {
          color: {
            type: 'linear', x: 0, y: 0, x2: 1, y2: 1,
            colorStops: [
              { offset: 0, color: d.color },
              { offset: 1, color: d.color + '99' },
            ],
          },
        },
      })),
    },
  ],
}))

function highlightSlice(index: number) {
  const chart = donutChartRef.value?.getChart()
  chart?.dispatchAction({ type: 'highlight', seriesIndex: 0, dataIndex: index })
  const d = distribution[index]
  if (d) {
    centerValue.value = d.value
    centerName.value = d.label
  }
}
function clearHighlight() {
  const chart = donutChartRef.value?.getChart()
  chart?.dispatchAction({ type: 'downplay', seriesIndex: 0 })
  centerValue.value = 24
  centerName.value = '数据源'
}

/* ---------- 库查询量排行 ---------- */
const rankData = [
  { name: 'sales_db', value: 4820 },
  { name: 'user_center', value: 3660 },
  { name: 'billing', value: 2410 },
  { name: 'analytics', value: 1880 },
  { name: 'session_cache', value: 960 },
]
const rankOption = computed<EChartsCoreOption>(() => ({
  animationDuration: 900,
  animationDelay: (idx: number) => idx * 90,
  tooltip: {
    trigger: 'axis',
    axisPointer: { type: 'shadow' },
    ...glassTooltip,
    formatter: (params: Array<{ name: string; value: number }>) =>
      `${params[0]?.name}: ${params[0]?.value.toLocaleString()} 次`,
  },
  grid: { left: 8, right: 28, top: 6, bottom: 4, containLabel: true },
  xAxis: {
    type: 'value',
    splitLine: { lineStyle: { color: 'rgba(255,255,255,0.06)', type: 'dashed' } },
    axisLabel: { color: 'rgba(255,255,255,0.35)', fontSize: 10 },
  },
  yAxis: {
    type: 'category',
    inverse: true,
    data: rankData.map((d) => d.name),
    axisLine: { show: false },
    axisTick: { show: false },
    axisLabel: { color: 'rgba(255,255,255,0.65)', fontSize: 11 },
  },
  series: [
    {
      type: 'bar',
      barWidth: 12,
      data: rankData.map((d) => d.value),
      itemStyle: {
        borderRadius: [0, 6, 6, 0],
        color: {
          type: 'linear', x: 0, y: 0, x2: 1, y2: 0,
          colorStops: [
            { offset: 0, color: 'rgba(102,126,234,0.35)' },
            { offset: 1, color: '#818cf8' },
          ],
        },
      },
      emphasis: {
        itemStyle: {
          color: {
            type: 'linear', x: 0, y: 0, x2: 1, y2: 0,
            colorStops: [
              { offset: 0, color: 'rgba(167,139,250,0.5)' },
              { offset: 1, color: '#a78bfa' },
            ],
          },
        },
      },
      label: {
        show: true,
        position: 'right',
        color: 'rgba(255,255,255,0.55)',
        fontSize: 10,
        formatter: (p: { value: number }) => p.value.toLocaleString(),
      },
    },
  ],
}))

/* ---------- 最近查询 ---------- */
const recentQueries = [
  { connection: 'sales-prod-mysql', sql: 'SELECT * FROM orders WHERE created_at > ?', duration: '45ms', rows: '2,130', time: '2 分钟前' },
  { connection: 'user-center-pg', sql: 'UPDATE users SET status = ? WHERE id = ?', duration: '12ms', rows: '1', time: '14 分钟前' },
  { connection: 'cache-redis-01', sql: 'GET session:user:8821', duration: '2ms', rows: '1', time: '31 分钟前' },
  { connection: 'sales-prod-mysql', sql: 'SELECT COUNT(*) FROM customers', duration: '180ms', rows: '1', time: '1 小时前' },
  { connection: 'billing-postgres', sql: 'SELECT sum(amount) FROM invoices', duration: '96ms', rows: '1', time: '2 小时前' },
]

/* ---------- 数字滚动 ---------- */
function countUp() {
  kpis.forEach((kpi, i) => {
    const target = { v: animated[kpi.key] ?? 0 }
    gsap.to(target, {
      v: kpi.value,
      duration: prefersReduced ? 0 : 1.1,
      delay: prefersReduced ? 0 : 0.15 + i * 0.08,
      ease: 'power2.out',
      onUpdate: () => {
        animated[kpi.key] = Math.round(target.v)
      },
    })
  })
}

function buildSparks(salt = 0) {
  kpis.forEach((kpi, i) => {
    const rand = seededRandom(i * 131 + salt + 7)
    const base = [40, 30, 50, 55][i] ?? 40
    kpi.spark = Array.from({ length: 14 }, (_, j) =>
      Math.round(base + Math.sin(j / 2 + i) * 18 + rand() * 28),
    )
  })
}

function refresh() {
  if (spinning.value) return
  spinning.value = true
  // 模拟实时数据刷新：趋势数据加扰动
  buildTrend(rangeDays.value, Date.now() % 1000)
  buildSparks(Date.now() % 1000)
  countUp()
  setTimeout(() => (spinning.value = false), 700)
}

/* ---------- 入场动画 ---------- */
function playEntrance() {
  if (prefersReduced || !rootRef.value) return
  const ctx = gsap.context(() => {
    gsap.from('.kpi-card', {
      y: 26, opacity: 0, duration: 0.65, stagger: 0.08, ease: 'power3.out', delay: 0.1,
    })
    gsap.from('.chart-card', {
      y: 30, opacity: 0, duration: 0.7, stagger: 0.12, ease: 'power3.out', delay: 0.3,
    })
    gsap.from('.table-card', {
      y: 24, opacity: 0, duration: 0.7, ease: 'power3.out', delay: 0.55,
    })
    gsap.from('.history-row', {
      x: -14, opacity: 0, duration: 0.4, stagger: 0.06, ease: 'power2.out', delay: 0.7,
    })
  }, rootRef.value)
  return ctx
}

let entranceCtx: gsap.Context | undefined

onMounted(() => {
  buildTrend(rangeDays.value)
  buildSparks()
  countUp()
  entranceCtx = playEntrance()
  // 环形图悬停 -> 中心数字联动
  const chart = donutChartRef.value?.getChart()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  chart?.on('mouseover', (params: any) => {
    if (params?.componentType === 'series' && typeof params.dataIndex === 'number') {
      centerValue.value = Number(params.value)
      centerName.value = String(params.name)
    }
  })
  chart?.on('globalout', () => {
    centerValue.value = 24
    centerName.value = '数据源'
  })
})

onBeforeUnmount(() => entranceCtx?.revert())
</script>
