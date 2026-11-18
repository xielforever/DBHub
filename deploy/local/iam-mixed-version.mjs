import fs from 'node:fs/promises'
import assert from 'node:assert/strict'

// Fixed synthetic identities, disposable schema, no live service discovery.
const checks = []
async function ready(build) {
  const until=Date.now()+60000
  while(Date.now()<until) {
    try { if((await fetch(`http://mixed-${build}:8080/api/ready`,{signal:AbortSignal.timeout(2000)})).ok)return }
    catch {}
    await new Promise(resolve=>setTimeout(resolve,250))
  }
  throw Error(`isolated mixed-${build} did not become ready`)
}
if(process.argv[2]==='--ready') {
  await ready(process.argv[3])
  console.log(JSON.stringify({build:process.argv[3],ready:true}))
  process.exit(0)
}
async function call(build, path, token, body) {
  const response = await fetch(`http://mixed-${build}:8080/api/v1${path}`, {
    method: body ? 'POST' : 'GET',
    headers: {'Content-Type':'application/json','X-DBHub-CSRF':'1',...(token ? {Authorization:`Bearer ${token}`} : {})},
    ...(body ? {body:JSON.stringify(body)} : {}), signal:AbortSignal.timeout(30000),
  })
  return {status:response.status,body:await response.json()}
}
async function login(build, user) {
  const result = await call(build, '/auth/login', '', {username:`perf-user-${user}`,password:'TestPassword123!'})
  assert.equal(result.status, 200, `${build} login: ${result.body.message}; fields=${Object.keys(result.body.data||{}).join(',')}`)
  assert.ok(result.body.data?.access_token)
  return result.body.data.access_token
}
function check(name, assertion) { assertion(); checks.push({name,pass:true}) }
let failure
try {
  await ready('legacy'); await ready('current')
  const oldTarget = await login('legacy', 1), newTarget = await login('current', 1)
  const oldLegacy = await login('legacy', 2), newLegacy = await login('current', 2)
  const good = await call('current','/workspaces/1/connections',newTarget)
  check('current bridge serves explicitly selected target',()=>{assert.equal(good.status,200);assert.equal(good.body.data.items.length,1)})
  const old = await call('legacy','/workspaces/1/connections',oldTarget)
  check('old process cannot return selected target connection',()=>assert.ok([403,404,409].includes(old.status)||old.status===200&&old.body.data.items.length===0))
  for (const [build,token] of [['legacy',oldLegacy],['current',newLegacy]]) {
    const result = await call(build,'/workspaces/1/connections',token)
    check(`${build} retains unconfirmed principal legacy connection`,()=>{assert.equal(result.status,200);assert.equal(result.body.data.items.length,1)})
  }
  const connection = good.body.data.items[0].id
  const denied = await call('legacy','/workspaces/1/query/execute',oldTarget,{connection_id:connection,sql:'SELECT 1 AS mixed_version_probe',max_rows:1})
  check('old query cannot bypass current metadata guard',()=>assert.notEqual(denied.status,200))
} catch (error) { failure=String(error) }
const result = {format:'IAM_MIXED_PROCESS_PROOF_V1',schema:'iam_mixed_versions',same_schema:true,checks,failure,pass:!failure,product_acceptance:false}
const file=`/evidence/mixed-version-${Date.now()}.json`
await fs.writeFile(file,JSON.stringify(result,null,2),{flag:'wx'})
console.log(JSON.stringify({...result,file}))
if (failure) process.exitCode=1
