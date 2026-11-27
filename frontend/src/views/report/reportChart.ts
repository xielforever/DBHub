import type { ChartConfig, ChartKind } from '../../components/business/chart/ChartCard.vue'
import type { ExecResult } from '../../api/workbench'

export const reportChartLabels: Readonly<Record<ChartKind, string>> = {
  table: '表格',
  bar: '柱状图',
  line: '折线图',
  pie: '饼图',
  metric: '指标卡',
  pivot: '透视表',
  stacked_bar: '堆叠柱状图',
  dual_axis: '双轴图',
  horizontal_bar: '横向条形图',
  area: '面积图',
  scatter: '散点图',
  histogram: '直方图',
  funnel: '漏斗图',
  heatmap: '热力图',
}

export const singleMetricChartKinds: readonly ChartKind[] = ['pie', 'metric', 'scatter', 'histogram', 'funnel', 'heatmap']

export type ReportChartPoints =
  | { kind: 'scatter'; values: [number, number][] }
  | { kind: 'heatmap'; yCategories: string[]; values: [number, number, number][] }

export interface ReportChartData {
  error?: string
  categories: string[]
  series: { name: string; values: (number | null)[] }[]
  points?: ReportChartPoints
}

const presentationKeys = new Set(['dimension', 'metrics', 'display', 'pivot', 'dual_axis', 'drillthrough', 'pie_style', 'heatmap_y'])

function numeric(value: unknown): number | null {
  if (value === null || value === undefined) return null
  if (typeof value !== 'number' && typeof value !== 'string') throw new Error('指标包含非数值，请检查 SQL 或改用表格')
  const text = typeof value === 'string' ? value.trim() : ''
  if (typeof value === 'string' && !/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(text)) throw new Error('指标包含非数值，请检查 SQL 或改用表格')
  // SQL decimal padding changes the displayed scale without changing the number.
  const digits = text.replace(/[eE].*$/, '').replace(/\D/g, '').replace(/^0+|0+$/g, '')
  if (digits.length > 15 && !/^[+-]?\d+$/.test(text)) throw new Error('指标精度超出图表安全范围，请改用表格或在 SQL 中明确取整')
  const number = Number(value)
  if (!Number.isFinite(number) || (number === 0 && digits.length > 0) ||
      (Number.isInteger(number) && !Number.isSafeInteger(number))) {
    throw new Error('指标超出图表安全数值范围，请改用表格')
  }
  return number
}

const category = (value: unknown) => String(value ?? '(空)')

function categoryIndex(categories: string[]) {
  return new Map(categories.map((label, index) => [label, index] as const))
}

function scatterPoints(rows: unknown[][], column: number, metric: (number | null)[]): ReportChartPoints {
  return {
    kind: 'scatter',
    values: rows.map((row, index) => {
      const x = numeric(row[column])
      const y = metric[index]
      if (x === null || y === null || y === undefined) throw new Error('散点图的 X、Y 坐标必须为非空数值，请检查 SQL')
      return [x, y]
    }),
  }
}

function heatmapPoints(xLabels: string[], yLabels: string[], metric: (number | null)[]) {
  const categories = [...new Set(xLabels)]
  const yCategories = [...new Set(yLabels)]
  const xIndexes = categoryIndex(categories)
  const yIndexes = categoryIndex(yCategories)
  const seen = new Set<string>()
  const points: ReportChartPoints = {
    kind: 'heatmap',
    yCategories,
    values: xLabels.map((label, index) => {
      const x = xIndexes.get(label)!
      const y = yIndexes.get(yLabels[index]!)!
      const value = metric[index]
      const key = `${x}:${y}`
      if (seen.has(key)) throw new Error('热力图存在重复坐标，请在 SQL 中为每个横纵类别组合返回一行')
      if (value === null || value === undefined) throw new Error('热力图色阶指标必须为非空数值，请检查 SQL')
      seen.add(key)
      return [x, y, value]
    }),
  }
  return { categories, points }
}

export function reportChart(kind: ChartKind, config: ChartConfig, result: ExecResult): ReportChartData {
  const empty: ReportChartData = { categories: [], series: [] }
  if (kind === 'table' || kind === 'pivot') return empty
  try {
    if (result.truncated) throw new Error('结果已截断，暂停图表展示；请先在 SQL 中聚合或缩小范围。下方仅展示已返回的部分数据。')
    if (Object.keys(config).some(key => !presentationKeys.has(key))) throw new Error('旧图表包含浏览器聚合配置，请编辑报表，在 SQL 中完成聚合和排序后重新保存')
    if (config.pie_style !== undefined && (kind !== 'pie' || !['solid', 'donut'].includes(config.pie_style))) throw new Error('饼图样式无效，请重新选择')
    if (config.heatmap_y !== undefined && kind !== 'heatmap') throw new Error('纵向类别只能用于热力图')
    const columns = result.columns ?? []
    const rows = result.rows ?? []
    if (!rows.length) return empty
    const index = (name: string) => {
      const matches = columns.flatMap((column, index) => column === name ? [index] : [])
      if (matches.length !== 1) throw new Error(`字段「${name}」缺失或重名，请在 SQL 中设置唯一别名`)
      return matches[0]!
    }
    const metrics = config.metrics ?? []
    if (!metrics.length || metrics.length > 6) throw new Error('请选择1至6个数值指标')
    if (singleMetricChartKinds.includes(kind) && metrics.length !== 1) throw new Error(`${reportChartLabels[kind]}需要一个指标`)
    if (kind === 'metric' && rows.length !== 1) throw new Error('指标卡需要一行结果，请在 SQL 中完成汇总')
    const series = metrics.map(name => {
      const column = index(name)
      return { name, values: rows.map(row => numeric(row[column])) }
    })
    if (kind === 'stacked_bar' && (metrics.length < 2 || series.some(s => s.values.some(value => value === null)))) throw new Error('堆叠图须有至少两个同单位指标且不能含 NULL；请在 SQL 中明确处理或查看原始结果')
    if (kind === 'dual_axis') {
      const axes = config.dual_axis, right = axes?.right_metrics || []
      if (!axes?.left_unit.trim() || !axes.right_unit.trim() || !right.length || right.length >= metrics.length || new Set(right).size !== right.length || right.some(name => !metrics.includes(name))) throw new Error('双轴图须明确左右单位，并分别分配有效指标')
    }
    if (kind === 'pie' && series[0]!.values.some(value => value === null || value < 0)) throw new Error('饼图指标不能包含 NULL 或负数，请在 SQL 中明确处理')
    const dimension = kind === 'metric' ? -1 : index(config.dimension ?? '')
    const categories = rows.map(row => dimension < 0 ? '' : category(row[dimension]))
    const metric = series[0]!.values
    if (kind === 'scatter') {
      if (config.dimension === metrics[0]) throw new Error('散点图请为 X、Y 选择不同的数值字段')
      return { categories, series, points: scatterPoints(rows, dimension, metric) }
    }
    if (kind === 'heatmap') {
      const y = index(config.heatmap_y ?? '')
      if (y === dimension || metrics.includes(config.heatmap_y!) || metrics.includes(config.dimension!)) throw new Error('热力图请选择互不相同的横向类别、纵向类别和指标')
      return { series, ...heatmapPoints(categories, rows.map(row => category(row[y])), metric) }
    }
    if (kind === 'histogram' || kind === 'funnel') {
      if (new Set(categories).size !== categories.length) throw new Error(`${reportChartLabels[kind]}存在重复的${kind === 'histogram' ? '分箱区间' : '阶段'}，请检查 SQL`)
      if (metric.some(value => value === null || value < 0 || (kind === 'histogram' && !Number.isInteger(value)))) {
        throw new Error(kind === 'histogram' ? '直方图频数必须为非负整数，请检查 SQL' : '漏斗图数量必须为非负数值，请检查 SQL')
      }
    }
    // Preserve SQL order; do not aggregate, re-sort, or drop result rows.
    return { categories, series }
  } catch (error) {
    return { ...empty, error: error instanceof Error ? error.message : '图表配置无效' }
  }
}
