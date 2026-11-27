import type { EChartsCoreOption } from 'echarts/core'

export type ChartColorResolver = (value: string) => string
const colorKeys = new Set(['color', 'backgroundColor', 'borderColor', 'shadowColor', 'fill', 'stroke'])
const dataStyleKeys = new Set(['itemStyle', 'lineStyle', 'areaStyle', 'label', 'labelLine', 'emphasis', 'blur', 'select'])

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

/** 只解析展示颜色；SQL结果、数据集、格式化函数和精确数值保持原样。 */
export function resolveChartColors(value: unknown, resolveColor: ChartColorResolver): unknown {
  if (Array.isArray(value)) return value.map(item => resolveChartColors(item, resolveColor))
  if (!isRecord(value)) return value
  const prototype = Object.getPrototypeOf(value)
  if (prototype !== Object.prototype && prototype !== null && !Array.isArray(value.colorStops)) return value
  const result: Record<string, unknown> = {}
  for (const [key, item] of Object.entries(value)) {
    if (key === 'source' || key === 'dataset') {
      result[key] = item
    } else if (key === 'data') {
      result[key] = Array.isArray(item) ? item.map(point => {
        if (!isRecord(point) || !Object.keys(point).some(part => dataStyleKeys.has(part))) return point
        const styled = { ...point }
        for (const part of dataStyleKeys) if (part in styled) styled[part] = resolveChartColors(styled[part], resolveColor)
        return styled
      }) : item
    } else if (colorKeys.has(key) && typeof item === 'string') {
      result[key] = resolveColor(item)
    } else if (key === 'color' && Array.isArray(item)) {
      result[key] = item.map(color => typeof color === 'string' ? resolveColor(color) : color)
    } else {
      result[key] = resolveChartColors(item, resolveColor)
    }
  }
  return result
}

export function resolveCssColor(value: string): string {
  const mix = /^color-mix\(in srgb,\s*var\((--db-[\w-]+)\)\s+([\d.]+)%,\s*transparent\)$/.exec(value)
  if (mix) {
    const color = resolveCssColor(`var(${mix[1]})`)
    const hex = /^#([\da-f]{6})$/i.exec(color)
    if (hex) {
      const channels = [0, 2, 4].map(index => parseInt(hex[1]!.slice(index, index + 2), 16))
      return `rgba(${channels.join(',')},${Math.min(100, Number(mix[2])) / 100})`
    }
  }
  const token = /^var\((--db-[\w-]+)\)$/.exec(value)
  if (!token || typeof document === 'undefined') return value
  const color = getComputedStyle(document.documentElement).getPropertyValue(token[1]!).trim()
  const rgb = /^rgba?\(([^)]+)\)$/.exec(color)
  if (rgb) {
    const parts = rgb[1]!.trim().split(/[\s,/]+/)
    if (parts.length >= 3) {
      const alpha = parts[3] === undefined ? 1 : Number.parseFloat(parts[3]) / (parts[3].endsWith('%') ? 100 : 1)
      return `rgba(${parts.slice(0, 3).join(',')},${alpha})`
    }
  }
  return color || value
}

/** Canvas颜色必须先解析为实际值，不能把CSS变量或light-dark交给绘图库。 */
export function themedChartOption(option: EChartsCoreOption, reducedMotion: boolean): EChartsCoreOption {
  const resolved = resolveChartColors(option, resolveCssColor) as EChartsCoreOption
  return {
    backgroundColor: 'transparent',
    color: Array.from({ length: 6 }, (_, index) => resolveCssColor(`var(--db-chart-${index + 1})`)),
    textStyle: { color: resolveCssColor('var(--db-text-primary)'), fontFamily: 'system-ui, sans-serif' },
    ...resolved,
    animation: reducedMotion ? false : resolved.animation,
  }
}
