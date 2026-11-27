import fs from 'node:fs/promises'
import assert from 'node:assert/strict'
import crypto from 'node:crypto'
const origin='http://b6-api:8080', calls=[]
async function call(token,method,path,body,status=200){
 const response=await fetch(origin+'/api/v1'+path,{method,headers:{'Content-Type':'application/json','X-DBHub-CSRF':'1',...(token?{Authorization:`Bearer ${token}`}:{})},...(body?{body:JSON.stringify(body)}:{}),signal:AbortSignal.timeout(30000)})
 const value=await response.json(); calls.push({method,path,status:response.status,expected:status})
 assert.ok((Array.isArray(status)?status:[status]).includes(response.status),`${method} ${path}: ${response.status} ${value.message}`)
 return value.data
}
const until=Date.now()+60000
while(true){try{if((await fetch(origin+'/api/ready',{signal:AbortSignal.timeout(1000)})).ok)break}catch{};if(Date.now()>until)throw Error('isolated B6 API not ready');await new Promise(r=>setTimeout(r,250))}
const login=async name=>(await call(null,'POST','/auth/login',{username:name,password:'TestPassword123!'})).access_token
const effective=token=>call(token,'GET','/workspaces/1/iam/effective')
const base=e=>({config_version:e.snapshot.config_version,authorization_epoch:e.snapshot.authorization_epoch})
const author=await login('iam-b6-author')
if(process.argv[2]==='seed-users'){
 const existing=new Set((await call(author,'GET','/workspaces/1/users')).items.map(u=>u.username))
 let created=0
 for(const [username,role] of [['iam-b6-reviewer','admin'],['iam-b6-member','readonly']])if(!existing.has(username)){await call(author,'POST','/workspaces/1/users',{username,password:'TestPassword123!',role},201);created++}
 console.log(JSON.stringify({synthetic_users_ready:2,created,real_activation:false}));process.exit(0)
}
const schema=process.env.IAM_B6_SCHEMA||'iam_b6_acceptance'
if(!['iam_b6_acceptance','iam_b6_v66','iam_b6_v68','iam_b6_v68_ceiling','iam_b6_v69','iam_b6_v70','iam_b6_v72','iam_b6_v73','iam_b6_v77','iam_b6_v81'].includes(schema))throw Error('registered isolated B6 namespace required')
let failure,result={format:'IAM_B6_HTTP_ACCEPTANCE_V1',schema,real_activation:false,human_approval:false}
try{
 const reviewer=await login('iam-b6-reviewer'),member=await login('iam-b6-member'),before=await effective(member)
 assert.equal(before.snapshot.target,false);assert.ok(before.actions.includes('query.read'))
 const a=await effective(author),r=await effective(reviewer),delegations=await call(author,'GET','/workspaces/1/iam/delegations')
 const da=delegations.find(d=>d.delegate_id===a.snapshot.subject),dr=delegations.find(d=>d.delegate_id===r.snapshot.subject)
 assert.ok(da&&dr)
 const expiry=new Date(Date.now()+1800000).toISOString(),role=crypto.randomUUID()
 async function propose(patch){return call(author,'POST','/workspaces/1/iam/proposals',{request_id:crypto.randomUUID(),baseline:base(await effective(author)),delegation_id:da.id,delegation_version:da.version,valid_until:expiry,patch})}
 async function approve(p){return call(reviewer,'POST','/workspaces/1/iam/proposals/review',{request_id:crypto.randomUUID(),proposal_id:p.id,version:p.version,decision:'approve',delegation_id:dr.id,delegation_version:dr.version,comment:'隔离双身份复核，不代表真实启用批准'})}
 await approve(await propose({kind:'role_upsert',role_id:role,role_version:0,name:'B6 自定义经营查看',actions:['report.page','report.view','dashboard.page','dashboard.view'],enabled:true,valid_until:expiry}))
 await approve(await propose({kind:'assignment_add',role_id:role,role_version:1,role_revision:1,subject_type:'user',subject_id:before.snapshot.subject,valid_until:expiry}))
 const shadow=await call(author,'POST','/workspaces/1/iam/migration/shadow',{user_ids:[before.snapshot.subject]})
 assert.equal(shadow.read_only,true);assert.equal(shadow.target_connections_opened,0);assert.equal(shadow.principals[0].unknown,0)
 assert.equal((await effective(member)).snapshot.target,false)
 const differences=Object.fromEntries(shadow.principals.map(p=>[p.user_id,p.differences.map(d=>({...d,reason:'隔离试点明确收窄为经营查看，保留原数据身份'}))]))
 const bundle=await call(author,'POST','/workspaces/1/iam/bundles',{request_id:crypto.randomUUID(),baseline:base(await effective(author)),delegation_id:da.id,delegation_version:da.version,user_ids:[before.snapshot.subject],shadow_fingerprint:shadow.fingerprint,differences,valid_until:expiry})
 assert.equal(bundle.activation_applied,false)
 const bridge=await call(author,'GET','/workspaces/1/iam/migration/bridge');assert.equal(bridge.isolated_activation_enabled,true)
 const proposal=await propose({kind:'bundle_activate',bundle_id:bundle.id,valid_until:expiry})
 await call(author,'POST','/workspaces/1/iam/proposals/review',{request_id:crypto.randomUUID(),proposal_id:proposal.id,version:proposal.version,decision:'approve',delegation_id:da.id,delegation_version:da.version},403)
 await approve(proposal)
 const selected=await effective(member);assert.equal(selected.snapshot.target,true);assert.ok(selected.actions.includes('report.view'));assert.ok(!selected.actions.includes('query.read'))
 await call(member,'GET','/workspaces/1/query/jobs',null,403)
 const assignment=(await call(author,'GET','/workspaces/1/iam/assignments')).find(s=>s.role_id===role&&!s.revoked_at)
 await call(author,'POST','/workspaces/1/iam/restrictions',{request_id:crypto.randomUUID(),baseline:base(await effective(author)),delegation_id:da.id,delegation_version:da.version,kind:'assignment.revoke',id:assignment.id,version:assignment.version,reason:'隔离回退前撤销唯一查看来源'},[200,202])
 const rollback=await call(author,'POST','/workspaces/1/iam/bundles/rollback',{request_id:crypto.randomUUID(),baseline:base(await effective(author)),bundle_id:bundle.id,delegation_id:da.id,delegation_version:da.version,reason:'隔离演练，不允许恢复已撤销来源或原技术功能'})
 const after=await effective(member);assert.equal(after.snapshot.target,false);assert.equal(after.snapshot.rollback_guard,true);assert.ok(!after.actions.includes('report.view')&&!after.actions.includes('query.read'))
 await call(member,'GET','/workspaces/1/query/jobs',null,403)
 assert.equal((await effective(reviewer)).snapshot.target,false)
 if(process.env.IAM_B6_RELEASE_CHECK==='1'){
  const ban=await call(author,'POST','/workspaces/1/iam/restrictions',{request_id:crypto.randomUUID(),baseline:base(await effective(author)),delegation_id:da.id,delegation_version:da.version,kind:'action.revoke',target_id:before.snapshot.subject,action:'report.view',reason:'隔离回退后：解除禁止不能恢复已撤销角色来源'},[200,202])
  await approve(await propose({kind:'action_release',subject_id:before.snapshot.subject,action_id:'report.view',revocation_sequence:ban.sequence,reason:'隔离复核：只限期解除当前禁止，不重建已撤销来源',valid_until:expiry}))
  const released=await effective(member)
  assert.equal(released.snapshot.target,false);assert.equal(released.snapshot.rollback_guard,true)
  assert.ok(!released.actions.includes('report.view')&&!released.actions.includes('query.read'))
  result.release_after_rollback_does_not_restore_revoked_sources=true
 }
 result={...result,bundle_id:bundle.id,member_id:before.snapshot.subject,read_only_shadow:true,target_connections_opened:0,self_review_rejected:true,target_applied:true,rollback_guard:true,revocation_preserved:true,unknown_principal_legacy_preserved:true,rollback}
}catch(e){failure=String(e)}
result={...result,pass:!failure,failure,calls}
const file=`/evidence/b6-http-${Date.now()}.json`;await fs.writeFile(file,JSON.stringify(result,null,2),{flag:'wx'})
console.log(JSON.stringify({pass:!failure,checks:calls.length,file,failure,real_activation:false}))
if(failure)process.exitCode=1
