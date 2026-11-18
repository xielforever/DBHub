// Real HTTP workflow against the disposable fixture, using separate identities.
// It creates synthetic content and never connects to the existing WSL business PG.
import {randomUUID} from 'node:crypto'
import assert from 'node:assert/strict'
import {writeFile} from 'node:fs/promises'
const origin='http://acceptance-api:8080/api/v1'
const tokens=new Map(),receipts=[]
async function call(user,method,path,body){const url=origin+(path.startsWith('/auth/')?path:'/workspaces/1'+path);const response=await fetch(url,{method,headers:{'Content-Type':'application/json',...(tokens.has(user)?{Authorization:`Bearer ${tokens.get(user)}`}:{})},...(body===undefined?{}:{body:JSON.stringify(body)})});const result=await response.json();receipts.push({user,method,path,status:response.status,code:result.code});if(!response.ok||result.code!==0)throw Error(`${user}: ${method} ${path} -> ${response.status}: ${result.message}`);return result.data}
for(const user of ['iam-owner','iam-developer','iam-supervisor','iam-viewer']){const login=await call(user,'POST','/auth/login',{username:user,password:user==='iam-owner'?process.env.IAM_DEMO_PASSWORD:'TestPassword123!'});tokens.set(user,login.access_token)}
const members=await call('iam-owner','GET','/iam/members');const idOf=user=>members.find(m=>m.username===user)?.id;const users=['iam-viewer','iam-developer','iam-supervisor'].map(idOf)
if(users.some(id=>!id))throw Error('Missing isolated identities')
const definitions=await call('iam-owner','GET','/datasource-definitions');let definition=definitions.items.find(d=>d.name==='隔离模拟业务数据')
if(!definition)definition=await call('iam-owner','POST','/datasource-definitions',{name:'隔离模拟业务数据',description:'仅阶段1隔离验收，会员、订单、券、TMS和WMS',visibility:'global',access_mode:'global',config:{type:'postgres',host:'metadata',port:5432,database:'iam_test',ssl_mode:'disable',connection_timeout:5,environment:'dev'}})
const connections=await call('iam-developer','GET','/connections');let connection=connections.items.find(c=>c.name==='隔离开发连接')
if(!connection)connection=await call('iam-developer','POST',`/datasource-definitions/${definition.id}/connections`,{name:'隔离开发连接',username:'iam_acceptance_writer',password:process.env.IAM_WRITER_PASSWORD,confirmed:true})
const accounts=await call('iam-owner','GET','/report-accounts');let account=accounts.items.find(a=>a.name==='隔离报表只读账号')
if(!account){const result=await call('iam-owner','POST','/report-accounts',{request_id:randomUUID(),name:'隔离报表只读账号',definition_revision_id:definition.id,username:'iam_acceptance_reader',password:process.env.IAM_READER_PASSWORD});account={id:result.id}}
const specs=[
 {key:'member',name:'会员与订单经营汇总',sql:"SELECT department,members,orders,sales,date_trunc('minute',now()) AS watermark FROM iam_business.member_overview ORDER BY department",columns:[['department','string'],['members','integer'],['orders','integer'],['sales','lossless_numeric']]},
 {key:'wms',name:'WMS仓储库存汇总',sql:"SELECT warehouse,available,reserved,date_trunc('minute',now()) AS watermark FROM iam_business.wms_overview ORDER BY warehouse",columns:[['warehouse','string'],['available','integer'],['reserved','integer']]},
 {key:'tms',name:'TMS运输经营汇总',sql:"SELECT route,shipments,delivered,freight,date_trunc('minute',now()) AS watermark FROM iam_business.tms_overview ORDER BY route",columns:[['route','string'],['shipments','integer'],['delivered','integer'],['freight','lossless_numeric']]}
]
const reportIDs=[]
for(const spec of specs){
 let report=(await call('iam-developer','GET','/managed-reports?scope=mine&page_size=100')).items.find(r=>r.draft.name===spec.name)
 if(!report){
  const data_contract={semantic_key:'iam_fixture_'+spec.key,meaning:spec.name+'（全部为模拟数据）',result_schema:[...spec.columns.map(([name,logical_type])=>({name,logical_type,nullable:false})),{name:'watermark',logical_type:'timestamp_with_timezone',nullable:false}],chart_type:'table',unit:'synthetic_record',currency:'',timezone:'UTC',scope_key:'isolated_synthetic_only',scope_description:'仅iam_business模拟业务表，不读取真实部门数据',time_window:{kind:'current_snapshot'},comparison_key:'iam_fixture_'+spec.key,freshness:{mode:'watermark_column',column:'watermark',max_data_age_seconds:900,max_future_skew_seconds:60}}
  const saved=await call('iam-developer','POST','/managed-reports',{request_id:randomUUID(),name:spec.name,description:'隔离验收：开发、独立审批、发布及业务查看',connection_id:connection.id,sql:spec.sql,chart_type:'table',chart_config:{},data_contract});report={id:saved.id,draft_version:1,publication_no:0}
 }
 reportIDs.push(report.id)
 if(!report.active_release_id){
  const versions=await call('iam-developer','GET',`/managed-reports/${report.id}/versions`)
  let review=versions.items.find(v=>v.status==='approved'||v.status==='pending')
  if(!review){const submitted=await call('iam-developer','POST',`/managed-reports/${report.id}/submit`,{request_id:randomUUID(),version:report.draft_version});review={review_id:submitted.id,status:'pending'}}
  const reviewID=review.review_id||review.id
  if(review.status!=='approved'){const validation_id=randomUUID();await call('iam-supervisor','POST',`/report-reviews/${reviewID}/validate`,{request_id:validation_id,account_id:account.id,user_ids:users,group_ids:[]});await call('iam-supervisor','POST',`/report-reviews/${reviewID}/decision`,{request_id:randomUUID(),decision:'approved',validation_id,contract_confirmed:true})}
  await call('iam-supervisor','POST',`/managed-reports/${report.id}/releases`,{request_id:randomUUID(),expected_publication_no:report.publication_no||0,review_id:reviewID,kind:'publish'})
 }
 await call('iam-viewer','POST',`/published-reports/${report.id}/execute`,{request_id:randomUUID(),publication_no:1})
}
const dashboardFixtures=(await call('iam-developer','GET','/managed-dashboards?scope=mine&page_size=100')).items.filter(d=>d.draft?.name==='会员 · 仓储 · 运输总览'&&d.lifecycle==='active').sort((a,b)=>b.id-a.id)
let dashboard=dashboardFixtures[0]
// An earlier fixture reader looked for the name outside draft and created
// duplicates. Explicit cleanup archives only these synthetic duplicates;
// versions, releases, approvals and prior evidence remain recoverable.
const archivedFixtures=[]
if(process.argv.includes('--archive-duplicate-fixtures'))for(const duplicate of dashboardFixtures.slice(1)){
 await call('iam-developer','POST',`/managed-dashboards/${duplicate.id}/archive`,{request_id:randomUUID(),version:duplicate.draft_version})
 archivedFixtures.push(duplicate.id)
}
if(!dashboard){const definition={name:'会员 · 仓储 · 运输总览',description:'隔离模拟场景的正式仪表盘',tiles:reportIDs.map((id,i)=>({key:'tile'+i,report_id:id,required:true,width:'full',binding_policy:'follow_published'})),user_ids:users,group_ids:[],quality:{allow_unknown_freshness:false,max_watermark_skew_seconds:300}};const saved=await call('iam-developer','POST','/managed-dashboards',{request_id:randomUUID(),definition});dashboard={id:saved.id,draft_version:1,publication_no:0}}
if(!dashboard.active_release_id){
 const reviews=(await call('iam-developer','GET',`/managed-dashboards/${dashboard.id}/versions`)).items||[];let review=reviews.find(v=>v.status==='approved'||v.status==='pending')
 if(!review){const submitted=await call('iam-developer','POST',`/managed-dashboards/${dashboard.id}/submit`,{request_id:randomUUID(),version:dashboard.draft_version,reviewer_id:idOf('iam-supervisor')});review={id:submitted.id,status:'pending'}}
 if(review.status!=='approved'){const refresh=await call('iam-supervisor','POST',`/managed-dashboards/${dashboard.id}/refreshes`,{mode:'review',review_id:review.id});for(let i=0;i<reportIDs.length;i++)await call('iam-supervisor','POST',`/dashboard-refreshes/${refresh.refresh_id}/tiles/tile${i}/execute`,{});await call('iam-supervisor','POST',`/dashboard-reviews/${review.id}/validate`,{request_id:randomUUID(),refresh_id:refresh.refresh_id});await call('iam-supervisor','POST',`/dashboard-reviews/${review.id}/decision`,{request_id:randomUUID(),decision:'approved'})}
 await call('iam-supervisor','POST',`/managed-dashboards/${dashboard.id}/releases`,{request_id:randomUUID(),expected_publication_no:dashboard.publication_no||0,review_id:review.id,kind:'publish'})
}
await call('iam-viewer','GET',`/published-dashboards/${dashboard.id}`)
const publishedRefresh=await call('iam-viewer','POST',`/managed-dashboards/${dashboard.id}/refreshes`,{mode:'published'})
assert.equal(publishedRefresh.tiles.length,3)
const tileProof=[]
for(const tile of publishedRefresh.tiles){
 assert.equal(tile.status,'ready')
 const execution=await call('iam-viewer','POST',`/dashboard-refreshes/${publishedRefresh.refresh_id}/tiles/${tile.key}/execute`,{})
 assert.ok(execution.execution_id)
 assert.ok(execution.result.rows.length>0)
 assert.equal(execution.quality.freshness,'fresh')
 tileProof.push({tile:tile.key,report_id:tile.report_id,execution_id:execution.execution_id,rows:execution.result.rows.length,freshness:execution.quality.freshness})
}
const result={synthetic_only:true,report_ids:reportIDs,dashboard_id:dashboard.id,definition_id:definition.id,connection_id:connection.id,published_tile_proof:tileProof,archived_duplicate_fixtures:archivedFixtures,receipts};await writeFile(`/evidence/acceptance-workflow-${Date.now()}.json`,JSON.stringify(result,null,2));process.stdout.write(JSON.stringify({report_count:reportIDs.length,dashboard_id:dashboard.id,published_tiles_completed:tileProof.length,archived_duplicate_fixtures:archivedFixtures,real_business_pg_touched:false})+'\n')
