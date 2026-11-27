import type { ChartConfig } from '../../components/business/chart/ChartCard.vue'
import type { ExecResult } from '../../api/workbench'

/** Rearrange SQL aggregates without converting exact values or summing in JS. */
export function reportPivot(config: ChartConfig, result: ExecResult): { columns: string[]; rows: unknown[][]; error?: string } {
  const empty = { columns: [] as string[], rows: [] as unknown[][] }
  try {
    if (result.truncated) throw new Error('结果已截断，不能生成完整透视表；请缩小范围或在 SQL 中汇总')
    const field = (name: string | undefined) => {
      const matches = (result.columns || []).flatMap((column, i) => column === name ? [i] : [])
      if (matches.length !== 1) throw new Error('透视表字段缺失或重名，请检查 SQL 别名')
      return matches[0]!
    }
    if (config.metrics?.length !== 1) throw new Error('透视表须选择一个已由 SQL 汇总的指标')
    const rowIndex = field(config.dimension), colIndex = field(config.pivot?.column), valueIndex = field(config.metrics[0])
    if (new Set([rowIndex, colIndex, valueIndex]).size !== 3) throw new Error('行维度、列维度和指标须使用不同字段')
    const key = (value: unknown) => {
      if (value !== null && !['string', 'number', 'boolean'].includes(typeof value)) throw new Error('透视维度须为标量')
      if (typeof value === 'number' && (!Number.isFinite(value) || Number.isInteger(value) && !Number.isSafeInteger(value))) throw new Error('透视维度数值精度不安全')
      return JSON.stringify([typeof value, value])
    }
    const columns = new Map<string, unknown>(), rows = new Map<string, { label: unknown; cells: Map<string, unknown> }>()
    for (const row of result.rows || []) {
      if (row.length !== result.columns?.length) throw new Error('透视表数据行与字段不一致')
      const rk = key(row[rowIndex]), ck = key(row[colIndex])
      columns.set(ck, row[colIndex])
      if (!rows.has(rk)) rows.set(rk, { label: row[rowIndex], cells: new Map() })
      const current = rows.get(rk)!
      if (current.cells.has(ck)) throw new Error('存在重复的行列组合，请在 SQL 中按两个维度汇总；页面不会自动相加')
      current.cells.set(ck, row[valueIndex])
      if (columns.size > 50 || rows.size > 200) throw new Error('透视表超过200行或50列，请缩小维度范围；原始结果仍可查看')
    }
    return { columns: [config.display?.labels?.[config.dimension || ''] || config.dimension || '', ...[...columns.values()].map(v => v === null ? 'NULL' : String(v))], rows: [...rows.values()].map(row => [row.label, ...[...columns.keys()].map(col => row.cells.has(col) ? row.cells.get(col) : '—')]) }
  } catch (error) { return { ...empty, error: error instanceof Error ? error.message : '透视表配置无效' } }
}
