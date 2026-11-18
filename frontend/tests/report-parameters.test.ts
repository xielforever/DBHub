import test from 'node:test'
import assert from 'node:assert/strict'
import { parameterDefaults, parameterQuery, parametersFromQuery, reportParameterError } from '../src/views/report/reportParameters.ts'
import type { ReportParameter } from '../src/views/report/reportParameters.ts'
import { displayUnit } from '../src/views/report/reportDisplay.ts'

test('filters retain precision, explicit NULL and false through URLs', () => {
  const definitions: ReportParameter[] = [{ name:'id',label:'编号',type:'integer' },{ name:'active',label:'启用',type:'boolean' },{ name:'q',label:'搜索',type:'text' }]
  const values = { id:'9007199254740993',active:false,q:null }
  assert.deepEqual({ ...parametersFromQuery(definitions, parameterQuery(values)) }, values)
  assert.equal(Object.getPrototypeOf(parametersFromQuery(definitions,{p_constructor:'x',p_q:['unexpected']})),null)
  assert.deepEqual({ ...parametersFromQuery(definitions,{p_id:'7',p_password:'secret'}) },{id:'7'})
})

test('relative defaults use business calendar and preserve false and zero', () => {
  const definitions: ReportParameter[] = [{name:'from',label:'开始',type:'date',default_relative_days:-7,timezone:'Asia/Singapore'},{name:'active',label:'启用',type:'boolean',default:false},{name:'n',label:'数量',type:'integer',default:0}]
  assert.deepEqual({ ...parameterDefaults(definitions,new Date('2026-10-19T18:00:00Z')) },{from:'2026-10-13',active:false,n:0})
})

test('viewer parameter errors preserve integer and decimal precision', () => {
  const integer: ReportParameter = { name: 'id', label: '编号', type: 'integer', required: true }
  assert.equal(reportParameterError(integer, null), '请填写编号')
  assert.equal(reportParameterError(integer, '9007199254740993'), undefined)
  assert.equal(reportParameterError(integer, '9223372036854775807'), undefined)
  assert.equal(reportParameterError(integer, '9223372036854775808'), '编号的值、格式或范围无效')
  assert.equal(reportParameterError({ ...integer, min: -3, max: 4 }, '+4'), undefined)
  assert.equal(reportParameterError({ ...integer, min: -3, max: 4 }, '-4'), '编号的值、格式或范围无效')
  assert.equal(reportParameterError(integer, '1.5'), '编号的值、格式或范围无效')
  const decimal: ReportParameter = { name: 'amount', label: '金额', type: 'decimal', min: 1e-7, max: 0.1 }
  assert.equal(reportParameterError(decimal, '0.0000001'), undefined)
  assert.equal(reportParameterError(decimal, '0.0000000999999999999'), '金额的值、格式或范围无效')
  assert.equal(reportParameterError(decimal, '.100'), undefined)
  assert.equal(reportParameterError(decimal, '0.100000000000000000001'), '金额的值、格式或范围无效')
  assert.equal(reportParameterError(decimal, null), undefined)
})

test('date and datetime filters validate calendars and keep accepted zoned timestamps', () => {
  const date: ReportParameter = { name: 'day', label: '日期', type: 'date', required: true }
  const datetime: ReportParameter = { name: 'cutoff', label: '截止时刻', type: 'datetime' }
  assert.equal(reportParameterError(date, '2028-02-29'), undefined)
  assert.equal(reportParameterError(date, '2026-02-29'), '日期的值、格式或范围无效')
  assert.equal(reportParameterError(date, ''), '请填写日期')
  assert.equal(reportParameterError(datetime, '2028-02-29T08:00:00.123456789-05:30'), undefined)
  assert.equal(reportParameterError(datetime, '2026-11-16T8:00:00,5Z'), undefined)
  assert.equal(reportParameterError(datetime, '2026-02-30T08:00:00Z'), '截止时刻的值、格式或范围无效')
  assert.equal(reportParameterError(datetime, '2026-11-16T08:00:00'), '截止时刻的值、格式或范围无效')
})

test('boolean validation distinguishes false, a blank optional filter, and an invalid link', () => {
  const boolean: ReportParameter = { name: 'active', label: '启用', type: 'boolean' }
  assert.equal(reportParameterError(boolean, false), undefined)
  assert.equal(reportParameterError(boolean, null), undefined)
  assert.equal(reportParameterError(boolean, 'false'), undefined)
  assert.equal(reportParameterError(boolean, 'maybe'), '启用的值、格式或范围无效')
  assert.equal(reportParameterError({ ...boolean, required: true }, null), '请填写启用')
})

test('text validation measures Unicode code points like the server', () => {
  const text: ReportParameter = { name: 'q', label: '客户备注', type: 'text', required: true, max_length: 3 }
  assert.equal(reportParameterError(text, '数📊🚀'), undefined)
  assert.equal(reportParameterError(text, '数📊🚀据'), '客户备注的值、格式或范围无效')
  assert.equal(reportParameterError(text, '\0'), '客户备注的值、格式或范围无效')
  assert.equal(reportParameterError(text, '\u0085　'), '请填写客户备注')
  assert.equal(reportParameterError(text, '\uFEFF'), undefined)

  const longest = '~' + '📊'.repeat(4095)
  assert.equal(reportParameterError({ ...text, max_length: 4096 }, longest), undefined)
  assert.equal(parametersFromQuery([text], parameterQuery({ q: longest })).q, longest)
})

test('enum validation checks only approved values and keeps literal null markers', () => {
  const segment: ReportParameter = {
    name: 'segment', label: '客群', type: 'enum', required: true,
    options: [{ label: '指定', value: '~null' }, { label: '空格码', value: ' ' }],
  }
  assert.equal(reportParameterError(segment, '~null'), undefined)
  assert.equal(reportParameterError(segment, ' '), undefined)
  assert.equal(reportParameterError(segment, ''), '请填写客群')
  assert.equal(reportParameterError(segment, '其他'), '客群的值、格式或范围无效')
  assert.equal(reportParameterError({ ...segment, required: false }, null), undefined)
  assert.equal(reportParameterError({ ...segment, max_length: 4 }, '~null'), '客群的值、格式或范围无效')
  for (const value of [null, '~null']) {
    assert.equal(parametersFromQuery([segment], parameterQuery({ segment: value })).segment, value)
  }
})

test('business units have readable labels and retain explicit currency', () => {
  assert.equal(displayUnit('currency','CNY'),'人民币元')
  assert.equal(displayUnit('currency','SGD'),'新加坡元')
  assert.equal(displayUnit('hours'),'小时')
  assert.equal(displayUnit('percent'),'%')
  assert.equal(displayUnit('CNY_10000'),'万元')
})
