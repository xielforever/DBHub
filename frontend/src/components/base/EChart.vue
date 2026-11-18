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
import { useTheme } from '../../utils/theme'
import { themedChartOption } from '../../utils/chart-theme'

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
const emit = defineEmits<{ select: [index: number] }>()
const chart = shallowRef<echarts.ECharts | null>(null)
const { resolvedTheme, reducedMotion } = useTheme()
let resizeObserver: ResizeObserver | null = null

function paint(notMerge: boolean) {
  chart.value?.setOption(themedChartOption(props.option, reducedMotion.value), { notMerge })
}

onMounted(() => {
  if (!el.value) return
  chart.value = echarts.init(el.value, undefined, { renderer: 'canvas' })
  paint(true)
  chart.value.on('click', event => {
    if (event.componentType === 'series' && typeof event.dataIndex === 'number') emit('select', event.dataIndex)
  })
  resizeObserver = new ResizeObserver(() => chart.value?.resize())
  resizeObserver.observe(el.value)
})

// option 变化时整体替换，保证切换数据/图表类型有过渡动画
watch(
  () => props.option,
  () => paint(true),
  { deep: true },
)

// 外观变化只合并展示配置，保留实例、缩放、图例选择和数据。
watch([resolvedTheme, reducedMotion], () => paint(false), { flush: 'post' })

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

<template>
  <div
    ref="el"
    class="echart w-full"
    :style="{ height }"
    role="img"
    :aria-label="ariaLabel"
  />
</template>
