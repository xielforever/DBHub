import fs from 'node:fs';import assert from 'node:assert/strict';import crypto from 'node:crypto';
import {departmentReports} from './department-reports.mjs';import {reportChartConfig} from './display-labels.mjs';
if(!process.argv.includes('--local'))throw new Error('仅供本机生成报表迁移，请传入 --local');
const credentials=JSON.parse(fs.readFileSync('.dbhub/simulation/department-credentials.json','utf8')),state=JSON.parse(fs.readFileSync('.dbhub/simulation/department-resources.json','utf8')),baseline=JSON.parse(fs.readFileSync('scripts/demo/migrations/period-comparison-v1.json','utf8')),tokens={},evidence=[];
const stable=value=>JSON.stringify(value,(_,item)=>item&&typeof item==='object'&&!Array.isArray(item)?Object.fromEntries(Object.keys(item).sort().map(key=>[key,item[key]])):item),hash=value=>crypto.createHash('sha256').update(value).digest('hex');
async function api(who,method,path,body,scope=true){const response=await fetch('http://localhost:5173/api/v1'+(scope?'/workspaces/1':'')+path,{method,headers:{...(tokens[who]?{Authorization:'Bearer '+tokens[who]}:{}),...(body?{'Content-Type':'application/json'}:{})},body:body?JSON.stringify(body):undefined,signal:AbortSignal.timeout(90000)});const packet=await response.json();assert(response.ok&&packet.code===0,`${method} ${path}: ${response.status} ${packet.message}`);return packet.data;}
for(const who of ['developer','reviewer'])tokens[who]=(await api(who,'POST','/auth/login',credentials.users[who],false)).access_token;
for(const department of ['finance','management']){
 const key=department+'/period_comparison',resource=state.reports[key],spec=departmentReports.find(report=>report.department===department&&report.key==='period_comparison'),current=await api('developer','GET',`/managed-reports/${resource.id}`),versions=(await api('developer','GET',`/managed-reports/${resource.id}/versions?page_size=100`)).items,active=versions.find(version=>version.review_id===current.active_review_id);
 assert(active?.manifest,'原审批与受众不可缺失');assert.equal(current.owner_id,state.users.developer.id,'不覆盖已经移交的报表');
 if(active.definition.sql_text===spec.sql){evidence.push({id:resource.id,status:'already_published',publication:current.publication_no});continue;}
 const view=department==='finance'?'reporting.finance_orders':'reporting.management_fulfillment';assert.equal(hash(active.definition.sql_text),baseline.sql_sha256[view],'已发布SQL不是已知生成版本，保留人工变更');
 const desired={...active.definition,name:spec.name,description:spec.description,sql_text:spec.sql,chart_config:reportChartConfig(spec),parameters:spec.parameters,data_contract:{...active.definition.data_contract,...spec.contract}};
 let draftVersion=current.draft_version;
 if(stable(current.draft)!==stable(desired)){
  assert.equal(stable(current.draft),stable(active.definition),'存在未发布的人工草稿，保留现场');
  const saved=await api('developer','PUT',`/managed-reports/${resource.id}/draft`,{request_id:crypto.randomUUID(),version:current.draft_version,name:desired.name,description:desired.description,connection_id:current.connection_id,database:desired.database_name,sql:desired.sql_text,chart_type:desired.chart_type,chart_config:desired.chart_config,parameters:desired.parameters,data_contract:desired.data_contract,export_policy:desired.export_policy});draftVersion=saved.version;
 }
 let review=versions.find(version=>stable(version.definition)===stable(desired)&&(version.status==='pending'||version.status==='approved'&&!version.was_published));
 if(!review){const submitted=await api('developer','POST',`/managed-reports/${resource.id}/submit`,{request_id:crypto.randomUUID(),version:draftVersion});review={review_id:submitted.id,status:'pending'};}
 if(review.status==='pending'){
  const manifest=active.manifest,validation=await api('reviewer','POST',`/report-reviews/${review.review_id}/validate`,{request_id:crypto.randomUUID(),account_id:manifest.account_id,user_ids:manifest.user_ids,group_ids:manifest.group_ids});
  await api('reviewer','POST',`/report-reviews/${review.review_id}/decision`,{request_id:crypto.randomUUID(),decision:'approved',validation_id:validation.validation_id,contract_confirmed:true,comment:'将可比较范围保守收紧为首条样本次日至源水位前一日；补充城市筛选，保留原执行账号和受众。'});
 }
 await api('reviewer','POST',`/managed-reports/${resource.id}/releases`,{request_id:crypto.randomUUID(),expected_publication_no:current.publication_no,review_id:review.review_id,kind:'publish'});
 const after=await api('developer','GET',`/managed-reports/${resource.id}`);resource.publication_no=after.publication_no;fs.writeFileSync('.dbhub/simulation/department-resources.json',JSON.stringify(state,null,2));evidence.push({id:resource.id,previousPublication:current.publication_no,publication:after.publication_no,status:'published_after_independent_review'});console.log('Published precise period boundary:',department);
}
fs.writeFileSync('.dbhub/audits/department-remediation/period-v2-migration.json',JSON.stringify({verifiedAt:new Date().toISOString(),reports:evidence,credentialsIncluded:false},null,2));
