import assert from 'node:assert/strict'
import { test } from 'node:test'
import { reportPivot } from '../src/views/report/reportPivot.ts'
import { reportChart } from '../src/views/report/reportChart.ts'
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
test('drill links retain exact values and reject unbound target parameters or partial rows',()=>{
 const map={report_id:1,bindings:{q:'amount'}},defs=[{name:'q',label:'编号',type:'text' as const}]
 assert.equal(drillParameters(map,result,0,defs).q,'9007199254740993.123456789')
 assert.throws(()=>drillParameters(map,result,0,[]),/未声明/)
 assert.throws(()=>drillParameters(map,{...result,truncated:true},0,defs),/不完整/)
 assert.throws(()=>drillParameters({report_id:1,bindings:{q:'city'}},{...result,rows:[[{secret:'no'},'web','1']]},0,defs),/标量/)
})
