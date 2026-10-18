import assert from 'node:assert/strict'
import { test } from 'node:test'
import { effectScope } from 'vue'
import { useReportRun } from '../src/views/report/useReportRun.ts'
import { reportChart } from '../src/views/report/reportChart.ts'
import { reportCommandKeys, reportDefinitionDiff } from '../src/views/report/reportCommands.ts'
import type { ExecResult } from '../src/api/workbench.ts'
function deferred<T>() { let resolve!: (value:T)=>void;let reject!: (error:Error)=>void;const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject} }
const result:ExecResult={kind:'query',columns:['day','amount'],rows:[['2026-01-02','20.50'],['2026-01-01',null],['2026-01-01','10.25']],duration_ms:5}
test('uncertain publication retries keep a UUID and an explicit new action gets a new UUID',()=>{
 const keys=reportCommandKeys(),payload={review_id:1,kind:'publish',expected_publication_no:0},first=keys.get('release',payload)
 assert.equal(first,keys.get('release',{...payload}))
 assert.notEqual(first,keys.get('release',{...payload,expected_publication_no:1}))
 assert.notEqual(first,keys.get('decision',payload))
 keys.clear();assert.notEqual(first,keys.get('release',payload))
})
test('version comparison includes datasource, SQL, and chart mapping changes',()=>{
 const before={name:'库存',description:'',definition_revision_id:1,database_name:'inventory',sql_text:'SELECT 1',chart_type:'table' as const,chart_config:{}}
 assert.deepEqual(reportDefinitionDiff(before,{...before,definition_revision_id:2,sql_text:'SELECT 2',chart_config:{metrics:['total']}}),['数据源修订','SQL','图表配置'])
 assert.deepEqual(reportDefinitionDiff(before,{...before}),[])
})
test('report charts preserve SQL order, duplicate dimensions and null gaps without client aggregation',()=>{
 const data=reportChart('line',{dimension:'day',metrics:['amount']},result)
 assert.equal(data.error,undefined)
 assert.deepEqual(data.categories,['2026-01-02','2026-01-01','2026-01-01'])
 assert.deepEqual(data.series[0]?.values,[20.5,null,10.25])
})
test('a truncated result cannot be displayed as a complete chart',()=>{
 assert.match(reportChart('bar',{dimension:'day',metrics:['amount']},{...result,truncated:true}).error!,/截断/)
 assert.equal(reportChart('table',{}, {...result,truncated:true}).error,undefined)
})
test('missing and duplicate field names fail visibly',()=>{
 assert.match(reportChart('bar',{dimension:'missing',metrics:['amount']},result).error!,/缺失/)
 assert.match(reportChart('bar',{dimension:'day',metrics:['amount']},{...result,columns:['day','amount','amount']}).error!,/重名/)
})
test('unsafe numbers and invalid types fall back to exact result-table values',()=>{
 for (const value of ['9007199254740993','123456789012345.6789','NaN','',true,Number.MAX_SAFE_INTEGER+1,Infinity]) {
  assert.ok(reportChart('bar',{dimension:'day',metrics:['amount']},{...result,rows:[['d',value]]}).error)
 }
})
test('metric requires a single result row and keeps null distinct from zero',()=>{
 assert.match(reportChart('metric',{metrics:['amount']},result).error!,/一行/)
 assert.deepEqual(reportChart('metric',{metrics:['amount']},{...result,rows:[['d',null]]}).series[0]?.values,[null])
 assert.deepEqual(reportChart('metric',{metrics:['amount']},{...result,rows:[['d',0]]}).series[0]?.values,[0])
})
test('legacy browser aggregation is explicitly rejected until the report is edited',()=>{
 assert.match(reportChart('bar',{dimension:'day',metrics:['amount'],aggregation:'sum'},result).error!,/旧图表/)
})
test('pie rejects null and negative quantities rather than silently coercing them',()=>{
 assert.ok(reportChart('pie',{dimension:'day',metrics:['amount']},result).error)
 assert.ok(reportChart('pie',{dimension:'day',metrics:['amount']},{...result,rows:[['d',-1]]}).error)
})
test('empty results remain empty instead of fabricating a zero statistic',()=>{
 const data=reportChart('metric',{metrics:['amount']},{...result,rows:[]})
 assert.equal(data.error,undefined);assert.deepEqual(data.series,[])
})
test('switching report invalidates the old response even when transport ignores abort',async()=>{
 const scope=effectScope(),run=scope.run(()=>useReportRun<string>())!,old=deferred<string>(),next=deferred<string>()
 let signal!:AbortSignal
 const first=run.run('old',current=>{signal=current;return old.promise})
 const second=run.run('next',()=>next.promise)
 assert.equal(signal.aborted,true)
 next.resolve('next report');await second;old.resolve('old report');await first
 assert.deepEqual(run.state.value,{status:'success',data:'next report'});scope.stop()
})
test('repeated clicks issue one request while the same report is running',async()=>{
 const scope=effectScope(),run=scope.run(()=>useReportRun<string>())!,pending=deferred<string>();let calls=0
 const first=run.run('same',()=>{calls++;return pending.promise})
 assert.equal(await run.run('same',async()=>{calls++;return 'unexpected'}),false)
 assert.equal(calls,1);pending.resolve('done');await first;scope.stop()
})
test('closing/unmounting drops late success and errors',async()=>{
 const scope=effectScope(),run=scope.run(()=>useReportRun<string>())!,pending=deferred<string>()
 const first=run.run('first',()=>pending.promise);run.reset();pending.resolve('late');await first
 assert.equal(run.state.value.status,'idle')
 const next=deferred<string>(),second=run.run('second',()=>next.promise);scope.stop();next.reject(new Error('late failure'));await second
 assert.equal(run.state.value.status,'idle');assert.equal(await run.run('disposed',async()=>''),false)
})
test('a failed refresh clears the previous result and explains the failure',async()=>{
 const scope=effectScope(),run=scope.run(()=>useReportRun<string>())!
 await run.run('r',async()=>'old');await run.run('r',async()=>{throw new Error('权限已撤销')})
 assert.deepEqual(run.state.value,{status:'error',message:'权限已撤销'});scope.stop()
})
