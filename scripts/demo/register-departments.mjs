import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {departmentReports} from './department-reports.mjs';
import {reportChartConfig} from './display-labels.mjs';
if(!process.argv.includes('--local'))throw new Error('仅供本机合成数据，请显式传入 --local');
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..'),folder=path.join(root,'.dbhub/simulation');
const credentialPath=path.join(folder,'department-credentials.json'),statePath=path.join(folder,'department-resources.json');
const credentials=JSON.parse(fs.readFileSync(credentialPath,'utf8'));
const state=fs.existsSync(statePath)?JSON.parse(fs.readFileSync(statePath,'utf8')):{users:{},groups:{},reports:{},dashboards:{},accounts:{}};
const origin='http://127.0.0.1:5173/api/v1',sessions={};
const labels={marketing:'营销运营',sales:'华东销售客服',transport:'运输业务',warehouse:'仓库1与2作业组',finance:'财务分析',management:'经营管理',developer:'BI开发',reviewer:'独立审批'};
let ready=false;for(let attempt=0;attempt<60;attempt++){try{const response=await fetch('http://localhost:8080/api/ready',{signal:AbortSignal.timeout(2000)});if(response.ok){ready=true;break;}}catch{}await new Promise(resolve=>setTimeout(resolve,1000));}if(!ready)throw new Error('本机后端尚未就绪，请先完成容器构建启动');
const persist=()=>{fs.writeFileSync(credentialPath,JSON.stringify(credentials,null,2),{mode:0o600});fs.writeFileSync(statePath,JSON.stringify(state,null,2));};
async function call(who,method,url,body,scope=true,expected){const response=await fetch(origin+(scope?'/workspaces/1':'')+url,{method,headers:{...(sessions[who]?{Authorization:'Bearer '+sessions[who]}:{}),...(body===undefined?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(150000)});const envelope=await response.json();if(expected&&response.status===expected)return envelope.data;if(!response.ok||envelope.code!==0)throw new Error(`${who}: ${method} ${url}: ${response.status} ${envelope.message}`);return envelope.data;}
const admin=await call('admin','POST','/auth/login',{username:'admin',password:'admin123'},false);sessions.admin=admin.access_token;state.adminId=admin.user.id;
let users=(await call('admin','GET','/users?page_size=100')).items;
for(const department of Object.keys(labels)){
 const role=department==='developer'?'developer':'readonly';
 const stored=credentials.users[department];
 const identity=stored||{username:'demo_'+department,password:'Dbh-'+crypto.randomBytes(22).toString('base64url')};
 credentials.users[department]=identity;persist();
 let user=users.find(u=>u.username===identity.username);
 if(!user){user=await call('admin','POST','/users',{username:identity.username,password:identity.password,role,email:identity.username+'@example.test'});users.push(user);}
 state.users[department]={id:user.id,username:identity.username,role};persist();
}
let members=(await call('admin','GET','/workspaces/1/members',undefined,false)).items;
const reviewer=members.find(m=>m.id===state.users.reviewer.id);
if(!reviewer)throw new Error('Reviewer is not a workspace member');
if(reviewer.role!=='readonly'||JSON.stringify(reviewer.capabilities||[])!==JSON.stringify(['report_review']))await call('admin','PATCH','/workspaces/1/members/'+reviewer.id,{role:'readonly',capabilities:['report_review'],expected_version:reviewer.membership_version},false);
state.users.reviewer.role='readonly';state.users.reviewer.platform_role='readonly';state.users.reviewer.capabilities=['report_review'];persist();
for(const who of Object.keys(labels)){const identity=credentials.users[who];sessions[who]=(await call(who,'POST','/auth/login',identity,false)).access_token;}
const groups=(await call('admin','GET','/user-groups?page_size=100')).items;
for(const [department,label] of Object.entries(labels)){
 let group=groups.find(g=>g.name==='演示·'+label);
 if(!group){group=await call('admin','POST','/user-groups',{name:'演示·'+label,description:'本机合成业务角色验证：'+label,member_ids:[state.users[department].id],expected_version:0});groups.push(group);}
 state.groups[department]={id:group.id,name:group.name};persist();
}
if(!state.source){const found=(await call('admin','GET','/datasource-definitions?page_size=100')).items.find(d=>d.name==='演示·统一履约分析库');state.source=found||await call('admin','POST','/datasource-definitions',{name:'演示·统一履约分析库',description:'商城、TMS、WMS的只读统一分析数据集；按部门数据库账号限定视图。',visibility:'global',access_mode:'restricted',config:{type:'postgres',host:'dbhub-wsl-demo-postgres-1',port:5432,database:credentials.database,ssl_mode:'disable',connection_timeout:10,environment:'dev'}});persist();}
const sourceId=state.source.id,access=await call('admin','GET',`/datasource-definitions/${sourceId}/access`);
const allowedUsers=[state.adminId,state.users.reviewer.id],allowedGroups=[state.groups.developer.id];
if(access.mode!=='restricted'||JSON.stringify([...(access.user_ids||[])].sort())!==JSON.stringify([...allowedUsers].sort())||JSON.stringify(access.group_ids||[])!==JSON.stringify(allowedGroups))await call('admin','PUT',`/datasource-definitions/${sourceId}/access`,{mode:'restricted',user_ids:allowedUsers,group_ids:allowedGroups,expected_version:access.version});
const devIdentity=credentials.databaseRoles.developer;
const devConnections=(await call('developer','GET','/connections')).items;
let connection=devConnections.find(c=>c.definition_revision_id===sourceId);
if(!connection)connection=await call('developer','POST',`/datasource-definitions/${sourceId}/connections`,{name:'BI·统一履约开发连接',username:devIdentity.username,password:devIdentity.password,confirmed:true});
state.developerConnection=connection.id;persist();
for(const department of ['marketing','sales','transport','warehouse','finance','management']){
 if(!state.accounts[department]){const identity=credentials.databaseRoles[department];const accounts=(await call('admin','GET','/report-accounts')).items;let account=accounts.find(a=>a.name==='演示·'+labels[department]+'执行账号');if(!account){const created=await call('admin','POST','/report-accounts',{name:'演示·'+labels[department]+'执行账号',definition_revision_id:sourceId,username:identity.username,password:identity.password,request_id:crypto.randomUUID()});account={id:created.id};}state.accounts[department]=account.id;persist();}
 await call('admin','POST',`/report-accounts/${state.accounts[department]}/test`,{});
}
for(const spec of departmentReports){
 const key=spec.department+'/'+spec.key;
 if(spec.drillTarget){const target=state.reports[spec.drillTarget];if(!target)throw new Error('下钻目标尚未发布：'+spec.drillTarget);spec.analysis={...spec.analysis,drillthrough:{report_id:target.id,bindings:spec.drillBindings}};}
 let resource=state.reports[key];
 if(!resource){const existing=(await call('developer','GET','/managed-reports?scope=mine&page_size=100')).items.find(r=>r.draft.name===spec.name);const draft=existing||await call('developer','POST','/managed-reports',{request_id:crypto.randomUUID(),name:spec.name,description:spec.description,connection_id:connection.id,database:credentials.database,sql:spec.sql,chart_type:spec.chart,chart_config:reportChartConfig(spec),parameters:spec.parameters,data_contract:spec.contract,export_policy:spec.exportPolicy});resource={id:draft.id};state.reports[key]=resource;persist();}
 let current=await call('developer','GET',`/managed-reports/${resource.id}`);
 if(!current.active_release_id){
  const versions=(await call('developer','GET',`/managed-reports/${resource.id}/versions?page_size=100`)).items;
  let pending=versions.find(v=>v.status==='pending');
  if(!pending){const submitted=await call('developer','POST',`/managed-reports/${resource.id}/submit`,{request_id:crypto.randomUUID(),version:current.draft_version});pending={review_id:submitted.id};}
  const validation=await call('reviewer','POST',`/report-reviews/${pending.review_id}/validate`,{request_id:crypto.randomUUID(),account_id:state.accounts[spec.department],user_ids:[state.adminId,state.users.reviewer.id,state.users.developer.id],group_ids:[state.groups[spec.department].id]});
  await call('reviewer','POST',`/report-reviews/${pending.review_id}/decision`,{request_id:crypto.randomUUID(),decision:'approved',validation_id:validation.validation_id,contract_confirmed:true,comment:'独立验证限定视图、参数绑定、金额口径及实际数据后批准本地合成演示。'});
  await call('reviewer','POST',`/managed-reports/${resource.id}/releases`,{request_id:crypto.randomUUID(),expected_publication_no:current.publication_no,review_id:pending.review_id,kind:'publish'});
 }
 const published=await call(spec.department,'GET',`/published-reports/${resource.id}`);
 const result=await call(spec.department,'POST',`/published-reports/${resource.id}/execute`,{request_id:crypto.randomUUID(),publication_no:published.publication_no});
 resource.name=spec.name;resource.department=spec.department;resource.chart=spec.chart;resource.publication_no=published.publication_no;resource.verifiedRows=result.rows.length;persist();
 console.log('Published and verified:',spec.name,result.rows.length,'rows');
}
const catalog=(await call('developer','GET','/dashboard-report-catalog')).items;
for(const department of ['marketing','sales','transport','warehouse','finance','management']){
 let resource=state.dashboards[department];
 if(!resource){
  const chosen=departmentReports.filter(r=>r.department===department&&r.chart!=='table').slice(0,3);
  const parameters=[];
  for(const spec of chosen)for(const p of spec.parameters)if(!parameters.some(d=>d.name===p.name))parameters.push(p);
  const tiles=chosen.map((spec,i)=>{const report=state.reports[department+'/'+spec.key],entry=catalog.find(c=>c.id===report.id);if(!entry)throw new Error('Published report missing from dashboard catalog');return{key:spec.key,report_id:report.id,required:true,width:i===0?'full':'half',comparison_group:'',contract_hash:entry.data_contract.hash,binding_policy:'follow_published',parameter_bindings:Object.fromEntries(spec.parameters.map(p=>[p.name,p.name]))};});
  const definition={name:labels[department]+'·经营仪表盘',description:'本地合成数据；水位为原始业务快照时间。'+(department==='sales'?'仅华东区域。':department==='warehouse'?'仅仓库1、2。':''),parameters,tiles,user_ids:[state.adminId,state.users.reviewer.id,state.users.developer.id],group_ids:[state.groups[department].id],quality:{allow_unknown_freshness:false,reason:'',max_watermark_skew_seconds:300}};
  const created=await call('developer','POST','/managed-dashboards',{request_id:crypto.randomUUID(),definition});resource={id:created.id};state.dashboards[department]=resource;persist();
 }
 const d=await call('developer','GET',`/managed-dashboards/${resource.id}`);
 if(!d.active_release_id){
  const reviews=(await call('developer','GET',`/managed-dashboards/${resource.id}/versions`)).items;
  let review=reviews.find(r=>r.status==='pending'||r.status==='approved'&&!r.was_published);
  if(!review){const preflight=await call('developer','POST',`/managed-dashboards/${resource.id}/preflight`,{version:d.draft_version});if(!preflight.can_submit)throw new Error('Dashboard preflight failed: '+JSON.stringify(preflight.issues));const submitted=await call('developer','POST',`/managed-dashboards/${resource.id}/submit`,{request_id:crypto.randomUUID(),version:d.draft_version,reviewer_id:state.users.reviewer.id});review={id:submitted.id,status:'pending'};}
  if(review.status!=='approved'){
   const refreshed=await call('reviewer','POST',`/managed-dashboards/${resource.id}/refreshes`,{mode:'review',review_id:review.id});
   for(const tile of refreshed.tiles)await call('reviewer','POST',`/dashboard-refreshes/${refreshed.refresh_id}/tiles/${tile.key}/execute`,{});
   await call('reviewer','POST',`/dashboard-reviews/${review.id}/validate`,{request_id:crypto.randomUUID(),refresh_id:refreshed.refresh_id});
   await call('reviewer','POST',`/dashboard-reviews/${review.id}/decision`,{request_id:crypto.randomUUID(),decision:'approved',comment:'已实际刷新全部卡片并验证部门受众及统一筛选。'});
  }
  await call('reviewer','POST',`/managed-dashboards/${resource.id}/releases`,{request_id:crypto.randomUUID(),expected_publication_no:d.publication_no,review_id:review.id,kind:'publish'});
 }
 const refreshed=await call(department,'POST',`/managed-dashboards/${resource.id}/refreshes`,{mode:'published'});
 for(const tile of refreshed.tiles)await call(department,'POST',`/dashboard-refreshes/${refreshed.refresh_id}/tiles/${tile.key}/execute`,{});
 resource.verifiedAt=new Date().toISOString();persist();console.log('Dashboard verified:',department,resource.id);
}
const denials=[];
for(const who of ['marketing','sales','transport','warehouse','finance','management']){
 const allowed=(await call(who,'GET','/published-reports?page_size=100')).items;
 for(const report of Object.values(state.reports)){const shouldAllow=report.department===who;const visible=allowed.some(r=>r.id===report.id);if(visible!==shouldAllow)throw new Error('Department report scope mismatch: '+who+'/'+report.name);}
 const foreign=Object.values(state.reports).find(r=>r.department!==who);
 const response=await fetch(origin+`/workspaces/1/published-reports/${foreign.id}`,{headers:{Authorization:'Bearer '+sessions[who]}});if(response.status!==404)throw new Error('Foreign report was not denied');denials.push({actor:who,foreignReport:foreign.id,status:response.status});
 const conns=(await call(who,'GET','/connections')).items;if(conns.length)throw new Error('Business viewer unexpectedly owns a database connection');
}
const evidence={verifiedAt:new Date().toISOString(),sourceId,users:state.users,groups:state.groups,reportCount:Object.keys(state.reports).length,dashboards:state.dashboards,denials,credentialsIncluded:false};
fs.writeFileSync(path.join(folder,'departments-verification.json'),JSON.stringify(evidence,null,2));
console.log(JSON.stringify({reportCount:evidence.reportCount,dashboardCount:Object.keys(state.dashboards).length,departmentDenials:denials.length},null,2));
