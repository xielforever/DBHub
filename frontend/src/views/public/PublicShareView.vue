<template>
  <div class="min-h-screen bg-[#0a0a0f] text-white flex flex-col">
    <header class="h-14 px-6 flex items-center justify-between border-b border-white/10 bg-white/[0.04] backdrop-blur-xl">
      <div class="flex items-center gap-2">
        <span class="w-7 h-7 rounded-lg bg-white text-black grid place-items-center font-bold text-xs">DB</span>
        <span class="font-semibold text-sm">DBHub 分享</span>
      </div>
      <span class="text-[11px] text-white/40">只读分享 · {{ data?.owner_name || '—' }}</span>
    </header>

    <main class="flex-1 p-6 max-w-6xl w-full mx-auto">
      <div v-if="loading" class="py-20 text-center text-white/40 text-sm" v-loading="true">加载中…</div>
      <div v-else-if="error" class="glass-card p-12 text-center">
        <p class="text-rose-300 text-sm">{{ error }}</p>
        <p class="text-white/30 text-xs mt-2">链接可能已过期、被吊销或不存在</p>
      </div>
      <template v-else-if="data">
        <div class="glass-card p-5 mb-5">
          <h1 class="text-lg font-semibold">{{ title }}</h1>
          <p class="text-xs text-white/40 mt-1">类型：{{ data.subject_type === 'dashboard' ? '仪表盘' : '报表' }} · 访问 {{ data.access_count }} 次<span v-if="data.expire_at"> · 过期 {{ formatTime(data.expire_at) }}</span><span v-else> · 永久有效</span></p>
        </div>

        <!-- 仪表盘分享 -->
        <div v-if="data.subject_type === 'dashboard'" class="grid grid-cols-12 gap-4">
          <div v-for="cell in layout" :key="cell.report_id" :class="`col-span-12 md:col-span-${Math.min(12, cell.w || 6)}`" class="glass-card p-3">
            <p class="text-sm font-medium mb-2">{{ reportName(cell.report_id) }}</p>
            <ChartCard
              v-if="reportRows[cell.report_id]"
              :chart-type="reportChartType(cell.report_id)"
              :columns="reportRows[cell.report_id]!.columns"
              :rows="reportRows[cell.report_id]!.rows"
              :config="reportChartConfig(cell.report_id)"
              height="260px"
            />
            <p v-else class="text-xs text-white/30 py-8 text-center">暂无数据</p>
          </div>
        </div>

        <!-- 报表分享 -->
        <div v-else class="space-y-4">
          <ChartCard
            v-if="singleReport && singleRows"
            :chart-type="singleReport.chart_type"
            :columns="singleRows.columns"
            :rows="singleRows.rows"
            :config="singleReport.chart_config"
            height="420px"
          />
          <div v-if="singleReport" class="glass-card p-4">
            <p class="text-xs text-white/40 mb-2">SQL（只读展示）</p>
            <pre class="text-xs text-white/70 whitespace-pre-wrap bg-white/5 rounded-lg p-3">{{ singleReport.sql_text }}</pre>
          </div>
        </div>
      </template>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import ChartCard from '../../components/business/chart/ChartCard.vue'
import { publicShareApi } from '../../api/share'

const route = useRoute()
const token = computed(() => (route.params.token as string) || '')

const loading = ref(true)
const error = ref('')
const data = ref<{
  share_id: number
  subject_type: string
  subject: any
  owner_name: string
  expire_at: string | null
  access_count: number
} | null>(null)

const reportRows = ref<Record<number, { columns: string[]; rows: unknown[][] }>>({})

const title = computed(() => {
  if (!data.value) return '—'
  const s = data.value.subject
  return s?.name || `${data.value.subject_type} #${s?.id || ''}`
})
const layout = computed(() => {
  if (!data.value || data.value.subject_type !== 'dashboard') return []
  const l = data.value.subject?.layout
  return Array.isArray(l) ? l : []
})
const singleReport = computed(() => {
  if (!data.value || data.value.subject_type !== 'report') return null
  return data.value.subject as any
})
const singleRows = computed(() => {
  if (!singleReport.value) return null
  // subject 已经包含 columns/rows 由后端直接执行？目前 PublicHandler 仅返回 subject 元数据
  // 为只读演示，前端需自行展示；若后端未返回 rows，显示空
  // 这里约定：如果 subject 含 columns/rows 则展示，否则尝试取 reportRows
  if (singleReport.value.columns && singleReport.value.rows) {
    return { columns: singleReport.value.columns, rows: singleReport.value.rows }
  }
  return reportRows.value[singleReport.value.id] || null
})

function reportName(id: number) {
  const d = data.value?.subject
  if (!d) return `报表 #${id}`
  const reports = (d as any).reports as any[] | undefined
  if (reports) {
    const r = reports.find((x) => x.id === id)
    if (r) return r.name
  }
  return `报表 #${id}`
}
function reportChartType(id: number) {
  const d = data.value?.subject as any
  const reports = d?.reports as any[] | undefined
  return reports?.find((x) => x.id === id)?.chart_type || 'table'
}
function reportChartConfig(id: number) {
  const d = data.value?.subject as any
  const reports = d?.reports as any[] | undefined
  return reports?.find((x) => x.id === id)?.chart_config
}
function formatTime(s: string) {
  try { return new Date(s).toLocaleString('zh-CN') } catch { return s }
}

onMounted(async () => {
  loading.value = true
  error.value = ''
  try {
    const res = await publicShareApi.get(token.value)
    data.value = res as any
    // 仪表盘分享：subject 可能包含 reports 快照（后端可扩展）；当前仅元数据，rows 留空
    // 报表分享：同上
  } catch (e: any) {
    error.value = e?.message || '加载失败'
  } finally {
    loading.value = false
  }
})
</script>
