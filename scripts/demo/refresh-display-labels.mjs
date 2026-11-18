import fs from 'node:fs';
import crypto from 'node:crypto';
import {departmentReports} from './department-reports.mjs';
import {reportChartConfig} from './display-labels.mjs';
if(!process.argv.includes('--local'))throw new Error('仅供本机合成数据，请传入 --local');
const credentials=JSON.parse(fs.readFileSync('.dbhub/simulation/department-credentials.json','utf8'));
const resources=JSON.parse(fs.readFileSync('.dbhub/simulation/department-resources.json','utf8'));
const origin='http://localhost:5173/api/v1',tokens={};
async function call(who,method,url,body,scope=true){const r=await fetch(origin+(scope?'/workspaces/1':'')+url,{method,headers:{...(tokens[who]?{Authorization:'Bearer '+tokens[who]}:{}),...(body?{'Content-Type':'application/json'}:{})},body:body?JSON.stringify(body):undefined,signal:AbortSignal.timeout(90000)});const e=await r.json();if(!r.ok||e.code!==0)throw new Error(`${method} ${url}: ${r.status} ${e.message}`);return e.data;}
for(const who of ['developer','reviewer'])tokens[who]=(await call(who,'POST','/auth/login',credentials.users[who],false)).access_token;
const evidence=[];
for(const spec of departmentReports){
 const id=resources.reports[spec.department+'/'+spec.key].id,current=await call('developer','GET',`/managed-reports/${id}`),config=reportChartConfig(spec);
 if(Object.keys(config.display.labels).every(key=>current.draft.chart_config.display?.labels?.[key]===config.display.labels[key]))continue;
 if(current.draft.sql_text!==spec.sql)throw new Error('演示报表SQL已被修改，请保留现场后核对：'+spec.name);
 const d=current.draft;
 const saved=await call('developer','PUT',`/managed-reports/${id}/draft`,{request_id:crypto.randomUUID(),version:current.draft_version,name:d.name,description:d.description,connection_id:current.connection_id,database:d.database_name,sql:d.sql_text,chart_type:d.chart_type,chart_config:{...d.chart_config,display:{...d.chart_config.display,...config.display}},parameters:d.parameters,data_contract:d.data_contract});
 const submitted=await call('developer','POST',`/managed-reports/${id}/submit`,{request_id:crypto.randomUUID(),version:saved.version});
 const versions=(await call('developer','GET',`/managed-reports/${id}/versions?page_size=100`)).items;
 const approved=versions.find(v=>v.review_id===current.active_review_id);
 if(!approved?.manifest)throw new Error('原批准受众缺失：'+spec.name);
 const manifest=approved.manifest;
 const validation=await call('reviewer','POST',`/report-reviews/${submitted.id}/validate`,{request_id:crypto.randomUUID(),account_id:manifest.account_id,user_ids:manifest.user_ids,group_ids:manifest.group_ids});
 await call('reviewer','POST',`/report-reviews/${submitted.id}/decision`,{request_id:crypto.randomUUID(),decision:'approved',validation_id:validation.validation_id,contract_confirmed:true,comment:'仅补充中文展示名称；独立验证原执行账号和受众，保留原SQL、参数及业务契约。'});
 const released=await call('reviewer','POST',`/managed-reports/${id}/releases`,{request_id:crypto.randomUUID(),expected_publication_no:current.publication_no,review_id:submitted.id,kind:'publish'});
 evidence.push({id,name:spec.name,previousPublication:current.publication_no,releaseId:released.id});console.log('Labels published:',spec.name);
}
fs.writeFileSync('.dbhub/simulation/display-labels-verification.json',JSON.stringify({verifiedAt:new Date().toISOString(),reports:evidence},null,2));
