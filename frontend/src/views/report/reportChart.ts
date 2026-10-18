import type { ChartConfig, ChartKind } from '../../components/business/chart/ChartCard.vue'
import type { ExecResult } from '../../api/workbench'

export interface ReportChartData {
  error?: string
  categories: string[]
  series: { name: string; values: (number | null)[] }[]
}
function numeric(value: unknown): number | null {
  if (value === null || value === undefined) return null
  if (typeof value !== 'number' && typeof value !== 'string') throw new Error('指标包含非数值，请检查 SQL 或改用表格')
  if (typeof value === 'string' && !/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(value.trim())) throw new Error('指标包含非数值，请检查 SQL 或改用表格')
  // Preserve large integer / high precision decimal text in the result table.
  // Charts must not silently round financial or identifier values.
  if (typeof value === 'string' && !/^[+-]?\d+$/.test(value.trim()) && value.replace(/[eE].*$/, '').replace(/[^0-9]/g, '').replace(/^0+/, '').length > 15) throw new Error('指标精度超出图表安全范围，请改用表格或在 SQL 中明确取整')
  const number = Number(value)
  if (!Number.isFinite(number) || (Number.isInteger(number) && !Number.isSafeInteger(number))) throw new Error('指标超出图表安全数值范围，请改用表格')
  return number
}
export function reportChart(kind: ChartKind, config: ChartConfig, result: ExecResult): ReportChartData {
  const empty: ReportChartData = { categories: [], series: [] }
  if (kind === 'table') return empty
  try {
    if (result.truncated) throw new Error('结果已截断，暂停图表展示；请先在 SQL 中聚合或缩小范围。下方仅展示已返回的部分数据。')
    if (Object.keys(config).some(key => !['dimension', 'metrics'].includes(key))) throw new Error('旧图表包含浏览器聚合配置，请编辑报表，在 SQL 中完成聚合和排序后重新保存')
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
    if ((kind === 'pie' || kind === 'metric') && metrics.length !== 1) throw new Error('饼图和指标卡需要一个指标')
    if (kind === 'metric' && rows.length !== 1) throw new Error('指标卡需要一行结果，请在 SQL 中完成汇总')
    const series = metrics.map(name => {
      const column = index(name)
      return { name, values: rows.map(row => numeric(row[column])) }
    })
    if (kind === 'pie' && series[0]!.values.some(value => value === null || value < 0)) throw new Error('饼图指标不能包含 NULL 或负数，请在 SQL 中明确处理')
    const dimension = kind === 'metric' ? -1 : index(config.dimension ?? '')
    // Preserve SQL order; do not aggregate, re-sort, or drop result rows.
    return { categories: rows.map(row => dimension < 0 ? '' : String(row[dimension] ?? '(空)')), series }
  } catch (error) {
    return { ...empty, error: error instanceof Error ? error.message : '图表配置无效' }
  }
}
