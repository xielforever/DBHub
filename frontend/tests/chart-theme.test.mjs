import assert from 'node:assert/strict'
import test from 'node:test'
import { resolveChartColors } from '../src/utils/chart-theme.ts'

test('图表主题只改变展示颜色，保留精确数据、NULL、格式化函数和数据集', () => {
  const formatter = value => String(value)
  const source = Object.freeze([Object.freeze(['区域', '精确金额']), Object.freeze(['华东', '9007199254740993.0123456789'])])
  const exactPoint = Object.freeze({ name: 'var(--db-chart-1)', value: '9007199254740993', color: 'var(--db-chart-1)' })
  const option = Object.freeze({
    color: ['var(--db-chart-1)', '#123456'],
    dataset: Object.freeze({ source }),
    tooltip: Object.freeze({ formatter, textStyle: Object.freeze({ color: 'var(--db-text-primary)' }) }),
    series: Object.freeze([{ type: 'bar', data: [null, '123.4500', exactPoint, { value: 12, itemStyle: { color: 'var(--db-chart-1)' } }], itemStyle: { color: formatter } }]),
  })
  const resolve = value => value === 'var(--db-chart-1)' ? '#0967cc' : value === 'var(--db-text-primary)' ? '#17283e' : value
  const result = resolveChartColors(option, resolve)
  assert.deepEqual(result.color, ['#0967cc', '#123456'])
  assert.equal(result.tooltip.textStyle.color, '#17283e')
  assert.equal(result.tooltip.formatter, formatter)
  assert.equal(result.dataset, option.dataset)
  assert.equal(result.dataset.source, source)
  assert.equal(result.series[0].data[0], null)
  assert.equal(result.series[0].data[1], '123.4500')
  assert.equal(result.series[0].data[2], exactPoint)
  assert.equal(result.series[0].data[3].value, 12)
  assert.equal(result.series[0].data[3].itemStyle.color, '#0967cc')
  assert.equal(result.series[0].itemStyle.color, formatter)
  assert.equal(option.color[0], 'var(--db-chart-1)')
})

test('图表主题保留渐变结构、非样式文本和外部实例', () => {
  const external = new Date('2026-10-26T00:00:00Z')
  const result = resolveChartColors({
    title: { text: 'var(--db-chart-1)' },
    external,
    series: [{ areaStyle: { color: { type: 'linear', x: 0, y: 0, x2: 0, y2: 1, colorStops: [{ offset: 0, color: 'var(--db-chart-1)' }, { offset: 1, color: 'transparent' }] } } }],
  }, value => value === 'var(--db-chart-1)' ? '#0967cc' : value)
  assert.equal(result.external, external)
  assert.equal(result.title.text, 'var(--db-chart-1)')
  assert.equal(result.series[0].areaStyle.color.type, 'linear')
  assert.equal(result.series[0].areaStyle.color.colorStops[0].color, '#0967cc')
  assert.equal(result.series[0].areaStyle.color.colorStops[1].color, 'transparent')
})
