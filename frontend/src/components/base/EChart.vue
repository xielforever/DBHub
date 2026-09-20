<template>
  <div
    ref="el"
    class="echart w-full"
    :style="{ height }"
    role="img"
    :aria-label="ariaLabel"
  />
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import * as echarts from 'echarts/core'
import { BarChart, LineChart, PieChart } from 'echarts/charts'
import {
  GridComponent,
  LegendComponent,
  TitleComponent,
  TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsCoreOption } from 'echarts/core'

echarts.use([
  LineChart,
  BarChart,
  PieChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  TitleComponent,
  CanvasRenderer,
])

const props = withDefaults(
  defineProps<{
    option: EChartsCoreOption
    height?: string
    ariaLabel?: string
  }>(),
  { height: '280px', ariaLabel: '图表' },
)

const el = ref<HTMLDivElement>()
const chart = shallowRef<echarts.ECharts | null>(null)
let resizeObserver: ResizeObserver | null = null

onMounted(() => {
  if (!el.value) return
  chart.value = echarts.init(el.value, undefined, { renderer: 'canvas' })
  chart.value.setOption(props.option)
  resizeObserver = new ResizeObserver(() => chart.value?.resize())
  resizeObserver.observe(el.value)
})

// option 变化时整体替换，保证切换数据/图表类型有过渡动画
watch(
  () => props.option,
  (option) => chart.value?.setOption(option, { notMerge: true }),
  { deep: true },
)

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  chart.value?.dispose()
  chart.value = null
})

defineExpose({
  getChart: () => chart.value,
  resize: () => chart.value?.resize(),
})
</script>
