<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { BookmarkPlus, Maximize2, Minimize2 } from 'lucide-vue-next'
import type { ChartConfig } from '../../../components/business/chart/ChartCard.vue'
import type { ExecResult } from '../../../api/workbench'
import { reportApi } from '../../../api/report'
import { featureAllowed } from '../../../utils/permission'
import { useUserStore } from '../../../stores/user'
import { reportChart } from '../../report/reportChart'
import ReportResult from '../../report/ReportResult.vue'

const props = defineProps<{
  result: ExecResult
  sql: string
  connectionId: number
  database: string
  table: boolean
  expanded: boolean
  toolbarTarget?: string
}>()
const emit = defineEmits<{ expand: [] }>()
const chartType = defineModel<'bar' | 'line'>('chartType', { required: true })
const config = defineModel<ChartConfig>('config', { required: true })
const user = useUserStore()
const canSave = computed(() => featureAllowed(user, 'report.design', user.role !== 'readonly'))
const type = computed(() => props.table ? 'table' : chartType.value)
const validation = computed(() => reportChart(type.value, config.value, props.result))
const columns = computed(() => [...new Set(props.result.columns ?? [])])
const saveOpen = ref(false)
const saving = ref(false)
const status = ref('')
const error = ref('')
const form = reactive({ name: '', description: '' })

function selectDimension(event: Event) {
  config.value = { ...config.value, dimension: (event.target as HTMLSelectElement).value }
}
function toggleMetric(column: string, event: Event) {
  const current = config.value.metrics ?? []
  const checked = (event.target as HTMLInputElement).checked
  config.value = { ...config.value, metrics: checked ? [...current, column] : current.filter(item => item !== column) }
}
function openSave() {
  form.name = ''
  form.description = ''
  error.value = ''
  status.value = ''
  saveOpen.value = true
}
async function saveReport() {
  const name = form.name.trim()
  if (!name || !canSave.value || saving.value || validation.value.error) return
  saving.value = true
  error.value = ''
  try {
    await reportApi.create({
      name,
      description: form.description.trim(),
      connection_id: props.connectionId,
      database: props.database,
      sql: props.sql,
      chart_type: type.value,
      chart_config: props.table ? {} : { dimension: config.value.dimension, metrics: config.value.metrics },
      visibility: 'private',
    })
    saveOpen.value = false
    status.value = '个人报表已保存。'
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '保存报表失败，请重试。'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div v-if="!table || canSave" :class="table && toolbarTarget ? 'contents' : 'flex shrink-0 flex-wrap items-center gap-2 border-b border-stroke px-3 py-1 text-xs'">
    <template v-if="!table">
      <label class="flex items-center gap-1"><span class="shrink-0 whitespace-nowrap">类型</span>
        <select v-model="chartType" class="glass-input ui-control-target" aria-label="图表类型">
          <option value="bar">柱状图</option>
          <option value="line">折线图</option>
        </select>
      </label>
      <label class="flex items-center gap-1"><span class="shrink-0 whitespace-nowrap">维度</span>
        <select :value="config.dimension" class="glass-input ui-control-target max-w-32" aria-label="图表维度" @change="selectDimension">
          <option v-for="column in columns" :key="column" :value="column">{{ column }}</option>
        </select>
      </label>
      <details class="relative">
        <summary class="ghost-button ui-control-target cursor-pointer">指标（{{ config.metrics?.length ?? 0 }}）</summary>
        <div class="glass-panel absolute left-0 z-20 grid max-h-52 min-w-40 gap-1 overflow-auto p-2 shadow-lg">
          <label v-for="column in columns" :key="column" class="flex items-center gap-2 break-all">
            <input type="checkbox" :checked="config.metrics?.includes(column)"
              :disabled="(config.metrics?.length ?? 0) >= 6 && !config.metrics?.includes(column)"
              @change="toggleMetric(column, $event)" />
            {{ column }}
          </label>
        </div>
      </details>
      <button type="button" class="ghost-button ui-control-target inline-flex items-center gap-1" :aria-pressed="expanded" @click="emit('expand')">
        <Minimize2 v-if="expanded" class="size-4" aria-hidden="true" />
        <Maximize2 v-else class="size-4" aria-hidden="true" />
        {{ expanded ? '恢复图表尺寸' : '放大图表' }}
      </button>
    </template>
    <Teleport defer :to="toolbarTarget ?? 'body'" :disabled="!toolbarTarget">
      <button v-if="canSave && columns.length" type="button" class="ghost-button ui-control-target ml-auto inline-flex items-center gap-1"
        :class="{ 'ui-icon-button': toolbarTarget }" aria-label="另存为报表" title="另存为报表" :disabled="!!validation.error" @click="openSave">
        <BookmarkPlus class="size-4" aria-hidden="true" /><span v-if="!toolbarTarget">另存为报表</span>
      </button>
      <span v-if="status" role="status" class="text-muted">{{ status }}</span>
    </Teleport>
  </div>
  <div v-if="!table" class="flex min-h-0 flex-1 flex-col overflow-auto p-2">
    <ReportResult :type="type" :config="config" :result="result" title="查询图表" compact hide-details fill />
  </div>
  <el-dialog v-model="saveOpen" title="另存为报表" width="min(520px, calc(100vw - 24px))" class="glass-dialog"
    :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving">
    <form class="space-y-3" @submit.prevent="saveReport">
      <label class="block text-sm">报表名称
        <input v-model="form.name" class="glass-input mt-1 block w-full" maxlength="128" required />
      </label>
      <label class="block text-sm">描述（可选）
        <textarea v-model="form.description" class="glass-input mt-1 block w-full" rows="2" maxlength="512" />
      </label>
      <p class="text-xs text-muted">使用本次执行的 SQL、数据源和数据库 {{ database }}。保存后仅本人可见。</p>
      <p v-if="error" role="alert" class="break-words text-sm text-danger">{{ error }}</p>
      <div class="flex justify-end gap-2">
        <button type="button" class="ghost-button ui-control-target" :disabled="saving" @click="saveOpen = false">取消</button>
        <button type="submit" class="liquid-button ui-control-target" :disabled="saving || !form.name.trim()">{{ saving ? '保存中…' : '保存' }}</button>
      </div>
    </form>
  </el-dialog>
</template>
