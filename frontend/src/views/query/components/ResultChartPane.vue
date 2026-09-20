<template>
  <div class="flex flex-col h-full min-h-0">
    <!-- 配置条 -->
    <div class="flex flex-wrap items-center gap-2.5 px-4 py-2.5 border-b border-white/10 bg-white/[0.02]">
      <div class="flex items-center gap-2">
        <span class="text-[11px] text-white/40">图表类型</span>
        <el-select v-model="chartType" class="w-28" size="small" popper-class="glass-popper">
          <el-option label="表格" value="table" />
          <el-option label="柱状图" value="bar" />
          <el-option label="折线图" value="line" />
          <el-option label="饼图" value="pie" />
          <el-option label="指标卡" value="metric" />
        </el-select>
      </div>

      <template v-if="chartType !== 'table'">
        <div class="flex items-center gap-2">
          <span class="text-[11px] text-white/40">维度</span>
          <el-select v-model="config.dimension" class="w-32" size="small" popper-class="glass-popper" clearable placeholder="选择维度">
            <el-option v-for="c in columns" :key="c" :label="c" :value="c" />
          </el-select>
        </div>
        <div class="flex items-center gap-2">
          <span class="text-[11px] text-white/40">指标</span>
          <el-select v-model="config.metrics" class="w-40" size="small" multiple collapse-tags collapse-tags-tooltip popper-class="glass-popper" placeholder="选择指标">
            <el-option v-for="c in numericColumns" :key="c" :label="c" :value="c" />
            <el-option v-for="c in nonNumericColumns" :key="c" :label="`${c} (计数)`" :value="c" />
          </el-select>
        </div>
        <div class="flex items-center gap-2">
          <span class="text-[11px] text-white/40">聚合</span>
          <el-select v-model="config.aggregation" class="w-24" size="small" popper-class="glass-popper">
            <el-option label="求和" value="sum" />
            <el-option label="计数" value="count" />
            <el-option label="平均" value="avg" />
            <el-option label="最小" value="min" />
            <el-option label="最大" value="max" />
          </el-select>
        </div>
        <div class="flex items-center gap-2">
          <span class="text-[11px] text-white/40">排序</span>
          <el-select v-model="config.sort" class="w-20" size="small" popper-class="glass-popper">
            <el-option label="降序" value="desc" />
            <el-option label="升序" value="asc" />
          </el-select>
        </div>
        <div class="flex items-center gap-2">
          <span class="text-[11px] text-white/40">Top</span>
          <el-input-number v-model="config.topN" :min="1" :max="100" size="small" class="w-20" controls-position="right" />
        </div>
      </template>

      <div class="flex-1" />
      <button
        v-if="canWrite && columns.length"
        class="liquid-button text-xs py-1.5 px-3 flex items-center gap-1"
        @click="openSaveDialog"
      >
        <BookmarkPlus class="w-3.5 h-3.5" />另存为报表
      </button>
    </div>

    <!-- AI 建议条 -->
    <div v-if="aiSuggestions.length" class="px-4 py-2 flex flex-wrap gap-2 bg-violet-500/10 border-b border-violet-400/20">
      <span class="text-[11px] text-violet-300 flex items-center gap-1"><Sparkles class="w-3 h-3" /> AI 建议：</span>
      <button
        v-for="s in aiSuggestions"
        :key="s.label"
        class="px-2 py-0.5 rounded-full text-[11px] bg-white/10 hover:bg-white/15 text-white/70 transition-colors"
        @click="applySuggestion(s)"
      >{{ s.label }}</button>
    </div>

    <!-- 洞察条 -->
    <div v-if="insights.length" class="px-4 py-2 grid grid-cols-2 sm:grid-cols-4 gap-2 bg-white/[0.02] border-b border-white/10">
      <div v-for="ins in insights" :key="ins.label" class="rounded-lg bg-white/5 border border-white/10 p-2">
        <p class="text-[10px] text-white/40">{{ ins.label }}</p>
        <p class="text-xs font-medium mt-0.5">{{ ins.value }}</p>
      </div>
    </div>

    <!-- 图表区 -->
    <div class="flex-1 min-h-0 p-3 overflow-auto">
      <div v-if="!columns.length" class="h-full flex items-center justify-center text-sm text-white/35">
        执行 SQL 后可在此配置图表
      </div>
      <ChartCard
        v-else
        :chart-type="chartType"
        :columns="columns"
        :rows="rows"
        :config="config"
        height="320px"
      />
    </div>

    <!-- 另存为报表 Dialog -->
    <el-dialog v-model="saveOpen" title="另存为报表" width="520px" class="glass-dialog" :close-on-click-modal="false">
      <div class="space-y-4">
        <div>
          <p class="text-xs text-white/50 mb-1">报表名称</p>
          <el-input v-model="saveForm.name" placeholder="例如：月度销售趋势" maxlength="128" />
        </div>
        <div>
          <p class="text-xs text-white/50 mb-1">描述（可选）</p>
          <el-input v-model="saveForm.description" type="textarea" :rows="2" placeholder="报表用途说明" maxlength="512" />
        </div>
        <div>
          <p class="text-xs text-white/50 mb-1">可见性</p>
          <el-select v-model="saveForm.visibility" class="w-full" popper-class="glass-popper">
            <el-option label="私有（仅自己可见）" value="private" />
            <el-option label="共享（团队可见）" value="shared" />
          </el-select>
        </div>
        <div class="text-[11px] text-white/30 bg-white/5 rounded-lg p-2.5">
          将固化：数据源 #{{ connectionId }} / 库 {{ database || '默认' }} / SQL + 图表配置（{{ chartType }}）
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="ghost-button" @click="saveOpen=false">取消</button>
          <button class="liquid-button" :disabled="saving" @click="saveReport">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { BookmarkPlus, Sparkles } from 'lucide-vue-next'
import ChartCard, { type ChartKind, type ChartConfig } from '../../../components/business/chart/ChartCard.vue'
import { reportApi } from '../../../api/report'
import { useUserStore } from '../../../stores/user'

const props = defineProps<{
  columns: string[]
  rows: unknown[][]
  sql: string
  connectionId: number
  database: string
}>()

const userStore = useUserStore()
const canWrite = computed(() => userStore.role !== 'readonly')

const chartType = ref<ChartKind>('table')
const config = reactive<ChartConfig>({
  dimension: '',
  metrics: [],
  aggregation: 'sum',
  sort: 'desc',
  topN: 20,
})

// 自动推断维度与指标
const numericColumns = computed(() => {
  if (!props.rows.length) return props.columns.slice(0, 2)
  const sample = props.rows.slice(0, 20)
  return props.columns.filter((_, idx) => {
    return sample.every(row => {
      const v = (row as unknown[])[idx]
      return v === null || v === undefined || !isNaN(Number(v))
    })
  })
})
const nonNumericColumns = computed(() => props.columns.filter(c => !numericColumns.value.includes(c)))

watch(() => props.columns, (cols) => {
  if (!cols.length) return
  if (!config.dimension) config.dimension = cols[0]
  if (!config.metrics?.length) {
    config.metrics = numericColumns.value.slice(0, 2).length ? numericColumns.value.slice(0,2) : cols.slice(1,3)
  }
}, { immediate: true })

interface Suggestion { label: string; chartType: ChartKind; dimension?: string; metrics?: string[]; aggregation?: ChartConfig['aggregation'] }
const aiSuggestions = computed<Suggestion[]>(() => {
  if (!props.columns.length || !props.rows.length) return []
  const suggestions: Suggestion[] = []
  const numCols = numericColumns.value
  const catCols = nonNumericColumns.value
  const firstCat = catCols[0]
  const firstNum = numCols[0]
  if (firstCat && firstNum) {
    suggestions.push({ label: `按 ${firstCat} 看 ${firstNum} 总和`, chartType: 'bar', dimension: firstCat, metrics: [firstNum], aggregation: 'sum' })
    suggestions.push({ label: `${firstNum} 占比`, chartType: 'pie', dimension: firstCat, metrics: [firstNum], aggregation: 'sum' })
    if (numCols.length > 1) {
      suggestions.push({ label: `${firstCat} 趋势对比`, chartType: 'line', dimension: firstCat, metrics: numCols.slice(0,2), aggregation: 'sum' })
    }
  }
  if (firstNum) {
    suggestions.push({ label: `${firstNum} 指标卡`, chartType: 'metric', metrics: [firstNum], aggregation: 'sum' })
  }
  const timeCol = props.columns.find(c => /time|date|created|updated/i.test(c))
  if (timeCol && firstNum) {
    suggestions.push({ label: `按 ${timeCol} 趋势`, chartType: 'line', dimension: timeCol, metrics: [firstNum], aggregation: 'sum' })
  }
  return suggestions.slice(0,5)
})
function applySuggestion(s: Suggestion) {
  chartType.value = s.chartType
  if (s.dimension) config.dimension = s.dimension
  if (s.metrics) config.metrics = s.metrics
  if (s.aggregation) config.aggregation = s.aggregation
  ElMessage.success(`已应用：${s.label}`)
}
const insights = computed(() => {
  if (!props.columns.length || !props.rows.length) return []
  const rows = props.rows
  const numCols = numericColumns.value
  const list: { label: string; value: string }[] = []
  list.push({ label: '总行数', value: `${rows.length}` })
  list.push({ label: '列数', value: `${props.columns.length}` })
  const firstNum = numCols[0]
  if (firstNum) {
    const idx = props.columns.indexOf(firstNum)
    if (idx >= 0) {
      const nums = rows.map(r => Number((r as any)[idx]) || 0)
      const sum = nums.reduce((a,b)=>a+b,0)
      const avg = sum / (nums.length || 1)
      const max = Math.max(...nums)
      const min = Math.min(...nums)
      list.push({ label: `${firstNum} 求和`, value: sum.toLocaleString() })
      list.push({ label: `${firstNum} 均值`, value: avg.toFixed(2) })
      list.push({ label: `${firstNum} 范围`, value: `${min} ~ ${max}` })
    }
  }
  // NULL 统计
  let nullCount = 0
  rows.forEach(r => { (r as any[]).forEach(v => { if (v === null || v === undefined) nullCount++ }) })
  if (nullCount > 0) list.push({ label: 'NULL 数', value: `${nullCount}` })
  return list.slice(0,8)
})

// 保存报表
const saveOpen = ref(false)
const saving = ref(false)
const saveForm = reactive({
  name: '',
  description: '',
  visibility: 'private' as 'private' | 'shared',
})

function openSaveDialog() {
  if (!props.sql.trim()) {
    ElMessage.warning('请先执行 SQL')
    return
  }
  saveForm.name = `报表 ${new Date().toLocaleDateString()}`
  saveForm.description = ''
  saveForm.visibility = 'private'
  saveOpen.value = true
}

async function saveReport() {
  if (!saveForm.name.trim()) {
    ElMessage.warning('报表名称必填')
    return
  }
  saving.value = true
  try {
    await reportApi.create({
      name: saveForm.name.trim(),
      description: saveForm.description.trim(),
      connection_id: props.connectionId,
      database: props.database,
      sql: props.sql,
      chart_type: chartType.value,
      chart_config: config,
      visibility: saveForm.visibility,
    })
    ElMessage.success('报表已保存，可在报表中心查看')
    saveOpen.value = false
  } catch {
    /* 拦截器提示 */
  } finally {
    saving.value = false
  }
}
</script>
