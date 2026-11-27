import assert from 'node:assert/strict'
import { test } from 'node:test'
import { reportPivot } from '../src/views/report/reportPivot.ts'
import { reportChart } from '../src/views/report/reportChart.ts'
import { reportChartOptions } from '../src/views/report/reportChartOptions.ts'
import { drillParameters } from '../src/views/report/reportDrill.ts'
import type { ExecResult } from '../src/api/workbench.ts'
const result:ExecResult={kind:'query',columns:['city','channel','amount'],rows:[['上海','web','9007199254740993.123456789'],['上海','store',null],['苏州','web','-12.34']],duration_ms:1}
const config={dimension:'city',metrics:['amount'],pivot:{column:'channel'}}
test('pivot rearranges exact SQL values and keeps NULL distinct from absent combinations',()=>{
 const pivot=reportPivot(config,result)
 assert.equal(pivot.error,undefined)
 assert.deepEqual(pivot.columns,['city','web','store'])
 assert.deepEqual(pivot.rows,[['上海','9007199254740993.123456789',null],['苏州','-12.34','—']])
})
test('pivot refuses duplicate cells, partial results and excessive dimensions',()=>{
 assert.match(reportPivot(config,{...result,rows:[...result.rows!,result.rows![0]!]}).error!,/重复/)
 assert.match(reportPivot(config,{...result,truncated:true}).error!,/截断/)
 assert.match(reportPivot(config,{...result,rows:Array.from({length:51},(_,i)=>['上海',String(i),'1'])}).error!,/50列/)
})
test('stacked charts refuse missing values; dual axes require explicit units and both axes',()=>{
 const data:ExecResult={kind:'query',columns:['day','net','refund'],rows:[['2026-10-01','10.20','2.10'],['2026-10-02',null,'3.20']],duration_ms:1}
 assert.match(reportChart('stacked_bar',{dimension:'day',metrics:['net','refund']},data).error!,/NULL/)
 const axes={dimension:'day',metrics:['net','refund'],dual_axis:{left_unit:'元',right_unit:'单',right_metrics:['refund']}}
 assert.equal(reportChart('dual_axis',axes,data).error,undefined)
 assert.deepEqual(reportChart('dual_axis',axes,data).series[0]?.values,[10.2,null])
 assert.match(reportChart('dual_axis',{...axes,dual_axis:{...axes.dual_axis,right_metrics:['missing']}},data).error!,/双轴/)
})

test('horizontal bar and area retain source order, negative values and gaps', () => {
 const data:ExecResult = {kind:'query', columns:['category', 'total'], rows:[['第二项', -3], ['第一项', null], ['第三项', 0]], duration_ms:1}
 const config = {dimension:'category', metrics:['total']}
 const horizontal = reportChartOptions('horizontal_bar', config, reportChart('horizontal_bar', config, data), '元')
 assert.equal(horizontal.xAxis.type, 'value')
 assert.equal(horizontal.xAxis.name, '元')
 assert.deepEqual(horizontal.yAxis.data, ['第二项', '第一项', '第三项'])
 assert.equal(horizontal.yAxis.inverse, true)
 assert.deepEqual(horizontal.series[0].data, [-3, null, 0])
 const area = reportChartOptions('area', config, reportChart('area', config, data), '元')
 assert.equal(area.series[0].type, 'line')
 assert.ok(area.series[0].areaStyle)
 assert.equal(area.series[0].connectNulls, false)
 assert.deepEqual(area.series[0].data, [-3, null, 0])
})

test('pie style preserves the historical donut and only a solid choice changes the radius', () => {
 const data:ExecResult = {kind:'query', columns:['category', 'total'], rows:[['A', 2], ['B', 0]], duration_ms:1}
 const config = {dimension:'category', metrics:['total']}
 for (const style of [undefined, 'donut', 'solid'] as const) {
   const selected = {...config, ...(style ? {pie_style:style} : {})}
   const chart = reportChart('pie', selected, data)
   assert.equal(chart.error, undefined)
   const options = reportChartOptions('pie', selected, chart, '元')
   assert.deepEqual(options.series[0].radius, style === 'solid' ? '65%' : ['35%', '65%'])
   assert.deepEqual(options.series[0].data, [{name:'A', value:2}, {name:'B', value:0}])
   const zero = reportChart('pie', selected, {...data, rows:[['A', 0], ['B', 0]]})
   const zeroOptions = reportChartOptions('pie', selected, zero, '元')
   assert.equal(zeroOptions.series[0].stillShowZeroSum, false)
   assert.deepEqual(zeroOptions.series[0].data, [{name:'A', value:0}, {name:'B', value:0}])
 }
})

test('scatter preserves both numeric axes and original row indices, including duplicate points', () => {
 const data:ExecResult = {kind:'query', columns:['sample_x', 'sample_y'], rows:[[2, 0], [1, -1], [2, 0]], duration_ms:1}
 const config = {dimension:'sample_x', metrics:['sample_y'], display:{labels:{sample_x:'时长', sample_y:'偏差'}}}
 const chart = reportChart('scatter', config, data)
 assert.deepEqual(chart.points, {kind:'scatter', values:[[2, 0], [1, -1], [2, 0]]})
 const options = reportChartOptions('scatter', config, chart, '毫秒')
 assert.equal(options.xAxis.name, '时长')
 assert.equal(options.yAxis.name, '偏差（毫秒）')
 assert.equal(options.xAxis.type, 'value')
 assert.equal(options.yAxis.type, 'value')
 assert.deepEqual(options.series[0].data, [[2, 0], [1, -1], [2, 0]])
 assert.match(reportChart('scatter', {...config, dimension:'sample_y'}, data).error!, /不同/)
 assert.match(reportChart('scatter', config, {...data, rows:[[null, 0]]}).error!, /非空数值/)
 assert.match(reportChart('scatter', config, {...data, rows:[['9007199254740993', 0]]}).error!, /安全数值/)
})

test('SQL bins and funnel stages use visible original order and validate their measures', () => {
 const data:ExecResult = {kind:'query', columns:['category', 'total'], rows:[['阶段 3', 0], ['阶段 2', 5]], duration_ms:1}
 const config = {dimension:'category', metrics:['total']}
 const histogram = reportChartOptions('histogram', config, reportChart('histogram', config, data), '个')
 assert.deepEqual(histogram.xAxis.data, ['阶段 3', '阶段 2'])
 assert.deepEqual(histogram.series[0].data, [0, 5])
 assert.equal(histogram.series[0].barCategoryGap, '0%')
 assert.match(reportChart('histogram', config, {...data, rows:[['20–30', '1.5']]}).error!, /非负整数/)
 const funnel = reportChartOptions('funnel', config, reportChart('funnel', config, data), '个')
 assert.equal(funnel.series[0].sort, 'none')
 assert.deepEqual(funnel.series[0].data, [{name:'阶段 3', value:0}, {name:'阶段 2', value:5}])
 assert.match(reportChart('funnel', config, {...data, rows:[['阶段 3', -1]]}).error!, /非负数值/)
 assert.match(reportChart('funnel', config, {...data, rows:[['阶段 3', 2],['阶段 3', 4]]}).error!, /重复/)
})

test('heatmap localizes category labels while preserving sparse cells and actual numeric values', () => {
 const data:ExecResult = {kind:'query', columns:['city','shift','amount'], rows:[['上海','早',0],['苏州','晚',-2],['上海','晚',7]], duration_ms:1}
 const config = {dimension:'city', heatmap_y:'shift', metrics:['amount']}
 const chart = reportChart('heatmap', config, data)
 const expectedPoints = [[0, 0, 0], [1, 1, -2], [0, 1, 7]]
 assert.deepEqual(chart.categories, ['上海', '苏州'])
 assert.deepEqual(chart.points, {kind:'heatmap', yCategories:['早', '晚'], values:expectedPoints})
 const options = reportChartOptions('heatmap', config, chart, '件')
 assert.deepEqual(options.series[0].data, expectedPoints)
 assert.deepEqual(options.series[0].itemStyle, {borderColor:'var(--db-chart-2)', borderWidth:1})
 assert.deepEqual(options.yAxis.data, ['早', '晚'])
 assert.equal(options.visualMap.min, -2)
 assert.equal(options.visualMap.max, 7)
 assert.equal(options.tooltip.formatter({value:[0, 0, 0]}), 'city：上海\nshift：早\namount（件）：0')
 assert.match(reportChart('heatmap', config, {...data, rows:[...data.rows!, ['上海','早',1]]}).error!, /重复坐标/)
 assert.match(reportChart('heatmap', config, {...data, rows:[['上海','早',null]]}).error!, /非空数值/)
 assert.match(reportChart('heatmap', {...config, heatmap_y:'city'}, data).error!, /互不相同/)
 assert.deepEqual(reportChart('heatmap', config, {...data, rows:[]}), {categories:[], series:[]})
})

test('drill links retain exact values and reject unbound target parameters or partial rows',()=>{
 const map={report_id:1,bindings:{q:'amount'}},defs=[{name:'q',label:'编号',type:'text' as const}]
 assert.equal(drillParameters(map,result,0,defs).q,'9007199254740993.123456789')
 assert.throws(()=>drillParameters(map,result,0,[]),/未声明/)
 assert.throws(()=>drillParameters(map,{...result,truncated:true},0,defs),/不完整/)
 assert.throws(()=>drillParameters({report_id:1,bindings:{q:'city'}},{...result,rows:[[{secret:'no'},'web','1']]},0,defs),/标量/)
})
