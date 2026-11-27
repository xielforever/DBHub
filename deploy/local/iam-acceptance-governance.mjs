import fs from 'node:fs/promises'
import crypto from 'node:crypto'
const base='http://acceptance-api:8080/api/v1',receipt=[]
async function call(token,method,path,body,status=200){const response=await fetch(base+path,{method,headers:{'Content-Type':'application/json','X-DBHub-CSRF':'1',...(token?{Authorization:`Bearer ${token}`}:{})},...(body?{body:JSON.stringify(body)}:{})});const value=await response.json();receipt.push({method,path,status:response.status,expected:status});if(!(Array.isArray(status)?status.includes(response.status):response.status===status))throw Error(`${method} ${path}: ${response.status} ${value.message}`);return value.data}
async function login(name,password='TestPassword123!'){return (await call(null,'POST','/auth/login',{username:name,password})).access_token}
const admin=await login('iam-admin'),reviewer=await login('iam-reviewer'),viewer=await login('iam-viewer'),legacy=await login('iam-legacy',process.env.IAM_DEMO_PASSWORD)
const effective=token=>call(token,'GET','/workspaces/1/iam/effective')
const a=await effective(admin),v=await effective(viewer),r=await effective(reviewer)
if(a.actions.includes('report.view')||!v.actions.includes('report.view'))throw Error('fixture permission boundary mismatch')
const delegations=await call(admin,'GET','/workspaces/1/iam/delegations'),da=delegations.find(d=>d.delegate_id===a.snapshot.subject&&!d.revoked_at&&d.governance_actions.includes('role.propose')),dr=delegations.find(d=>d.delegate_id===r.snapshot.subject&&!d.revoked_at&&d.governance_actions.includes('role.review'))
if(!da||!dr)throw Error('explicit independent delegations missing')
const baseline=data=>({config_version:data.snapshot.config_version,authorization_epoch:data.snapshot.authorization_epoch}),until=new Date(Date.now()+30*60*1000).toISOString(),roleID=crypto.randomUUID()
async function propose(patch){const snapshot=await effective(admin),command={request_id:crypto.randomUUID(),baseline:baseline(snapshot),delegation_id:da.id,delegation_version:da.version,valid_until:until,patch};await call(admin,'POST','/workspaces/1/iam/proposals/preview',command);return call(admin,'POST','/workspaces/1/iam/proposals',command)}
async function approve(proposal){return call(reviewer,'POST','/workspaces/1/iam/proposals/review',{request_id:crypto.randomUUID(),proposal_id:proposal.id,version:proposal.version,decision:'approve',delegation_id:dr.id,delegation_version:dr.version,comment:'隔离HTTP验收：独立复核精确许可与受益范围'})}
const definition=await propose({kind:'role_upsert',role_id:roleID,role_version:0,name:'隔离验收 · 自定义经营查看角色',actions:['report.page','report.view'],enabled:true,valid_until:until})
await call(admin,'POST','/workspaces/1/iam/proposals/review',{request_id:crypto.randomUUID(),proposal_id:definition.id,version:definition.version,decision:'approve',delegation_id:da.id,delegation_version:da.version,comment:'应拒绝自审'},403)
if((await effective(admin)).actions.includes('report.view'))throw Error('pending definition changed administrator data access')
if((await approve(definition)).state!=='applied')throw Error('definition not applied')
const assignment=await propose({kind:'assignment_add',role_id:roleID,role_version:1,role_revision:1,subject_type:'user',subject_id:v.snapshot.subject,valid_until:until})
if((await approve(assignment)).state!=='applied')throw Error('assignment not applied')
const current=await effective(viewer)
if(current.snapshot.sources.filter(s=>s.actions.includes('report.view')).length<2)throw Error('independent source union missing')
const sources=await call(admin,'GET','/workspaces/1/iam/assignments'),newSource=sources.find(s=>s.role_id===roleID&&!s.revoked_at)
if(!newSource)throw Error('applied assignment missing')
await call(admin,'POST','/workspaces/1/iam/restrictions',{request_id:crypto.randomUUID(),baseline:baseline(await effective(admin)),delegation_id:da.id,delegation_version:da.version,kind:'assignment.revoke',id:newSource.id,version:newSource.version,reason:'隔离验收：只撤销本次新增来源'},[200,202])
if(!(await effective(viewer)).actions.includes('report.view'))throw Error('revoking one source removed a separate valid source')
await call(admin,'GET','/workspaces/1/published-reports',null,403)
await call(viewer,'GET','/workspaces/1/reports',null,403)
const unchanged=await effective(legacy);if(unchanged.snapshot.target||!unchanged.actions.includes('query.read'))throw Error('unknown principal lost full legacy path')
const root=await login('iam-owner',process.env.IAM_DEMO_PASSWORD)
const other=await call(root,'POST','/workspaces',{name:'阶段1 · 跨空间异常验收',request_id:crypto.randomUUID()})
await call(root,'POST',`/workspaces/${other.id}/members`,{username:'iam-viewer',role:'readonly'})
const second=await call(viewer,'GET',`/workspaces/${other.id}/iam/effective`)
if(second.snapshot.target||!second.actions.includes('query.read'))throw Error('one principal could not retain a different legacy space')
await call(viewer,'GET',`/workspaces/${other.id}/published-reports/1`,null,404)
const members=await call(root,'GET',`/workspaces/${other.id}/members`),member=members.items.find(m=>m.username==='iam-viewer')
await call(root,'PATCH',`/workspaces/${other.id}/members/${member.id}`,{role:'readonly',active:false,expected_version:member.membership_version})
await call(viewer,'GET',`/workspaces/${other.id}/iam/effective`,null,404)
if(!(await effective(viewer)).actions.includes('report.view'))throw Error('exiting a different space removed original-space access')
const result={format:'iam-isolated-http-governance-v1',synthetic_only:true,real_activation:false,role_id:roleID,proposal_ids:[definition.id,assignment.id],revoked_source_id:newSource.id,other_workspace_id:other.id,cases:receipt}
await fs.writeFile(`/evidence/acceptance-governance-${Date.now()}.json`,JSON.stringify(result,null,2));console.log(JSON.stringify({status:'PASS',checks:receipt.length,other_workspace_id:other.id,real_activation:false}))
