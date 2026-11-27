import fs from 'node:fs/promises'
import crypto from 'node:crypto'
const base='http://release-api:8080/api/v1',cases=[]
async function call(token,method,path,body,want=200){const r=await fetch(base+path,{method,headers:{'Content-Type':'application/json','X-DBHub-CSRF':'1',...(token?{Authorization:`Bearer ${token}`}:{})},...(body?{body:JSON.stringify(body)}:{})}),value=await r.json();cases.push({method,path,status:r.status,expected:want});if(!(Array.isArray(want)?want.includes(r.status):r.status===want))throw Error(`${path} ${r.status}: ${value.message}`);return value.data}
const untilReady=Date.now()+120000;while(Date.now()<untilReady){try{if((await fetch('http://release-api:8080/api/ready')).ok)break}catch{};await new Promise(r=>setTimeout(r,300))}
const login=async username=>(await call(null,'POST','/auth/login',{username,password:'TestPassword123!'})).access_token
const admin=await login('iam-admin'),reviewer=await login('iam-reviewer'),viewer=await login('iam-viewer')
const effective=token=>call(token,'GET','/workspaces/1/iam/effective'),baseline=s=>({config_version:s.snapshot.config_version,authorization_epoch:s.snapshot.authorization_epoch})
const a=await effective(admin),r=await effective(reviewer),v=await effective(viewer),delegations=await call(admin,'GET','/workspaces/1/iam/delegations'),da=delegations.find(d=>d.delegate_id===a.snapshot.subject&&!d.revoked_at&&d.governance_actions.includes('assignment.propose')),dr=delegations.find(d=>d.delegate_id===r.snapshot.subject&&!d.revoked_at&&d.governance_actions.includes('assignment.review'))
if(!da||!dr)throw Error('independent finite delegation required')
async function ban(){return call(admin,'POST','/workspaces/1/iam/restrictions',{request_id:crypto.randomUUID(),baseline:baseline(await effective(admin)),delegation_id:da.id,delegation_version:da.version,kind:'action.revoke',target_id:v.snapshot.subject,action:'report.view',reason:'独立隔离场景：验证禁止操作与限期解除审批'},[200,202])}
function release(sequence,until){return {kind:'action_release',subject_id:v.snapshot.subject,action_id:'report.view',revocation_sequence:sequence,reason:'隔离验收：按精确禁止记录申请限期解除',valid_until:until}}
async function propose(patch){const command={request_id:crypto.randomUUID(),baseline:baseline(await effective(admin)),delegation_id:da.id,delegation_version:da.version,valid_until:patch.valid_until,patch};await call(admin,'POST','/workspaces/1/iam/proposals/preview',command);const proposal=await call(admin,'POST','/workspaces/1/iam/proposals',command);const replay=await call(admin,'POST','/workspaces/1/iam/proposals',command);if(proposal.id!==replay.id)throw Error('duplicate release proposal');return proposal}
function review(proposal,delegation=dr){return {request_id:crypto.randomUUID(),proposal_id:proposal.id,version:proposal.version,decision:'approve',delegation_id:delegation.id,delegation_version:delegation.version,comment:'隔离验收：独立复核精确操作、禁止记录及到期时间'}}
async function approve(p){const command=review(p),value=await call(reviewer,'POST','/workspaces/1/iam/proposals/review',command),replay=await call(reviewer,'POST','/workspaces/1/iam/proposals/review',command);if(value.state!=='applied'||JSON.stringify(value)!==JSON.stringify(replay))throw Error('independent release/receipt mismatch')}
await call(viewer,'GET','/workspaces/1/iam/restrictions',null,403)
const first=await ban();await call(viewer,'GET','/workspaces/1/published-reports',null,403)
const p=await propose(release(first.sequence,new Date(Date.now()+600000).toISOString()));await call(viewer,'GET','/workspaces/1/published-reports',null,403)
await call(admin,'POST','/workspaces/1/iam/proposals/review',review(p,da),403)
await call(viewer,'POST','/workspaces/1/iam/proposals/review',review(p),403)
await approve(p);await call(viewer,'GET','/workspaces/1/published-reports')
const second=await ban();if(second.sequence<=first.sequence)throw Error('ban generation rewound')
await call(viewer,'GET','/workspaces/1/published-reports',null,403)
await call(admin,'POST','/workspaces/1/iam/proposals/preview',{request_id:crypto.randomUUID(),baseline:baseline(await effective(admin)),delegation_id:da.id,delegation_version:da.version,valid_until:p.patch.valid_until,patch:p.patch},409)
const expiry=new Date(Date.now()+4000).toISOString();await approve(await propose(release(second.sequence,expiry)))
await call(viewer,'GET','/workspaces/1/published-reports')
await new Promise(r=>setTimeout(r,Math.max(1,new Date(expiry).getTime()-Date.now()+80)))
await call(viewer,'GET','/workspaces/1/published-reports',null,403)
await approve(await propose(release(second.sequence,new Date(Date.now()+600000).toISOString())))
await call(viewer,'GET','/workspaces/1/published-reports')
const history=await call(admin,'GET','/workspaces/1/iam/restrictions'),record=history.find(x=>x.sequence===second.sequence)
if(!record?.release_until||record.release_history.length!==2)throw Error('renewal overwrote release history')
// Leave a distinct latest ban for the browser to submit and independently
// approve through the product pages. This is a cloned synthetic namespace.
const browserBan=await ban()
await fs.writeFile(`/evidence/action-release-http-${Date.now()}.json`,JSON.stringify({format:'IAM_ACTION_RELEASE_HTTP_V1',status:'PASS',schema:'iam_release_acceptance',synthetic_only:true,real_activation:false,first_sequence:first.sequence,second_sequence:second.sequence,browser_ban_sequence:browserBan.sequence,checks:cases.length,cases},null,2))
console.log(JSON.stringify({status:'PASS',checks:cases.length,browser_ban_sequence:browserBan.sequence,real_activation:false}))
