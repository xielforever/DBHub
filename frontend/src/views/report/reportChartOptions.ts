import type { EChartsCoreOption } from 'echarts/core'
import type { ChartConfig, ChartKind } from '../../components/business/chart/ChartCard.vue'
import type { ReportChartData } from './reportChart'
import { displayLabel, formatReportNumber } from './reportDisplay.ts'

const axisColor = 'var(--db-text-secondary)'
const colors = [2, 1, 3, 4, 5, 6].map(index => `var(--db-chart-${index})`)
const textStyle = { color: axisColor }

function categoryAxis(data: string[], limitedLabelWidth?: number) {
  return {
    type: 'category',
    data,
    axisLabel: {
      ...textStyle,
      ...(limitedLabelWidth ? { hideOverlap: true, overflow: 'truncate', width: limitedLabelWidth } : {}),
    },
  }
}

function valueAxis(name: string, formatter: (value: unknown) => string) {
  return { type: 'value', name, nameTextStyle: textStyle, axisLabel: { ...textStyle, formatter } }
}

function tooltipValues(event: unknown): unknown[] {
  if (!event || typeof event !== 'object' || !('value' in event)) return []
  return Array.isArray(event.value) ? event.value : []
}

export function reportChartOptions(kind: ChartKind, config: ChartConfig, data: ReportChartData, unit: string): EChartsCoreOption {
  const valueFormatter = (value: unknown) => formatReportNumber(value, config.display)
  const label = (field: string) => displayLabel(field, config.display)
  const series = data.series.map(item => ({ ...item, label: label(item.name) }))
  const metric = series[0]

  if (kind === 'pie' || kind === 'funnel') {
    const points = data.categories.map((name, index) => ({ name, value: metric?.values[index] }))
    return {
      color: colors,
      tooltip: { trigger: 'item', valueFormatter },
      ...(kind === 'pie'
        ? {
          legend: { type: 'scroll', bottom: 0, textStyle },
          series: [{
            type: 'pie',
            radius: config.pie_style === 'solid' ? '65%' : ['35%', '65%'],
            stillShowZeroSum: false,
            data: points,
          }],
        }
        : {
          series: [{
            type: 'funnel',
            sort: 'none',
            min: 0,
            max: points.reduce((maximum, point) => Math.max(maximum, point.value ?? 0), 0) || 1,
            minSize: '0%',
            left: '8%',
            width: '84%',
            top: 20,
            bottom: 20,
            label: { position: 'inside', overflow: 'truncate', width: 120 },
            data: points,
          }],
        }),
    }
  }

  const dimension = label(config.dimension ?? '')
  const metricName = metric?.label ?? ''
  const metricWithUnit = unit ? `${metricName}（${unit}）` : metricName
  if (data.points?.kind === 'scatter') {
    return {
      color: colors,
      tooltip: {
        trigger: 'item',
        renderMode: 'richText',
        formatter(event: unknown) {
          const [x, y] = tooltipValues(event)
          return `${dimension}：${valueFormatter(x)}\n${metricWithUnit}：${valueFormatter(y)}`
        },
      },
      grid: { left: 16, right: 16, top: 44, bottom: 44, containLabel: true },
      xAxis: { ...valueAxis(dimension, valueFormatter), scale: true, nameLocation: 'middle', nameGap: 27 },
      yAxis: { ...valueAxis(metricWithUnit, valueFormatter), scale: true },
      series: [{ type: 'scatter', name: metricName, data: data.points.values }],
    }
  }

  if (data.points?.kind === 'heatmap') {
    const points = data.points
    const yDimension = label(config.heatmap_y ?? '')
    let min = Infinity
    let max = -Infinity
    for (const [, , value] of points.values) {
      min = Math.min(min, value)
      max = Math.max(max, value)
    }
    return {
      tooltip: {
        trigger: 'item',
        renderMode: 'richText',
        formatter(event: unknown) {
          const [x, y, value] = tooltipValues(event)
          return `${dimension}：${data.categories[Number(x)] ?? ''}\n${yDimension}：${points.yCategories[Number(y)] ?? ''}\n${metricWithUnit}：${valueFormatter(value)}`
        },
      },
      grid: { left: 12, right: 12, top: 28, bottom: 62, containLabel: true },
      xAxis: { ...categoryAxis(data.categories, 75), splitArea: { show: false } },
      yAxis: { ...categoryAxis(points.yCategories, 104), inverse: true },
      visualMap: {
        type: 'continuous',
        dimension: 2,
        min,
        max,
        orient: 'horizontal',
        left: 'center',
        bottom: 8,
        itemWidth: 10,
        itemHeight: 112,
        textStyle,
        formatter: valueFormatter,
        inRange: { color: ['color-mix(in srgb, var(--db-chart-2) 22%, transparent)', 'var(--db-chart-2)'] },
      },
      series: [{
        type: 'heatmap',
        name: metricWithUnit,
        data: points.values,
        // 已观测的低值单元格仍须和无记录的空格可辨。
        itemStyle: { borderColor: 'var(--db-chart-2)', borderWidth: 1 },
      }],
    }
  }

  const horizontal = kind === 'horizontal_bar'
  const histogram = kind === 'histogram'
  const axes = kind === 'dual_axis' ? config.dual_axis : undefined
  return {
    color: colors,
    tooltip: { trigger: 'axis', valueFormatter },
    legend: { type: 'scroll', top: 0, textStyle },
    grid: { left: 16, right: 16, top: 52, bottom: horizontal ? 38 : 20, containLabel: true },
    xAxis: horizontal
      ? { ...valueAxis(unit, valueFormatter), nameLocation: 'middle', nameGap: 27 }
      : categoryAxis(data.categories, histogram || kind === 'area' ? 84 : undefined),
    yAxis: horizontal
      ? { ...categoryAxis(data.categories, 108), inverse: true }
      : axes
        ? [axes.left_unit, axes.right_unit].map((name, index) => ({
          ...valueAxis(name, valueFormatter), position: index ? 'right' : 'left', splitLine: { show: index === 0 },
        }))
        : valueAxis(unit, valueFormatter),
    series: series.map(item => {
      const rightAxis = axes?.right_metrics.includes(item.name) === true
      return {
        name: item.label,
        type: kind === 'line' || kind === 'area' || rightAxis ? 'line' : 'bar',
        stack: kind === 'stacked_bar' ? 'approved-metrics' : undefined,
        yAxisIndex: rightAxis ? 1 : 0,
        data: item.values,
        connectNulls: false,
        ...(kind === 'area' ? { areaStyle: { opacity: 0.18 } } : {}),
        ...(histogram
          ? { barCategoryGap: '0%', itemStyle: { borderColor: 'var(--db-surface-solid)', borderWidth: 1 } }
          : {}),
      }
    }),
  }
}
