import type { ChartConfig } from '../../components/business/chart/ChartCard.vue'
import type { ExecResult } from '../../api/workbench'
import type { ReportParameter, ParameterValues } from './reportParameters'

export function drillParameters(config: NonNullable<ChartConfig['drillthrough']>, result: ExecResult, index: number, definitions: ReportParameter[]): ParameterValues {
  if (result.truncated || !Number.isInteger(index) || index < 0 || index >= (result.rows?.length || 0)) throw new Error('结果不完整或明细行已变化，请刷新后重试')
  const row = result.rows![index]!, values: ParameterValues = Object.create(null)
  for (const [parameter, column] of Object.entries(config.bindings)) {
    if (!definitions.some(d => d.name === parameter)) throw new Error(`目标报表未声明参数「${parameter}」，请联系开发者更新下钻映射`)
    const matches = (result.columns || []).flatMap((name, i) => name === column ? [i] : [])
    if (matches.length !== 1) throw new Error(`下钻字段「${column}」缺失或重名`)
    const value = row[matches[0]!]
    if (value !== null && !['string', 'number', 'boolean'].includes(typeof value) || typeof value === 'number' && (!Number.isFinite(value) || Number.isInteger(value) && !Number.isSafeInteger(value)) || typeof value === 'string' && value.length > 1024) throw new Error('下钻值不是安全的标量，请在 SQL 中返回精确编号')
    values[parameter] = value as string | number | boolean | null
  }
  if (!Object.keys(values).length || JSON.stringify(values).length > 6000) throw new Error('下钻参数为空或超过链接上限')
  return values
}
