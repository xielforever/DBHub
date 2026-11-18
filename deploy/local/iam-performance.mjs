import fs from 'node:fs/promises'
import http from 'node:http'
import crypto from 'node:crypto'
import {performance} from 'node:perf_hooks'

const workload=JSON.parse(await fs.readFile('/fixture/workload.json','utf8'))
if(workload.format!=='dbhub-iam-read-workload-v1'||workload.measurement.measured_requests_per_profile_path!==1000||workload.measurement.repetitions!==3||workload.measurement.concurrency!==8)throw Error('registered workload mismatch')
const runID=process.env.PERF_RUN_ID||new Date().toISOString().replace(/[:.]/g,'-')
const prefix=`/evidence/performance-${runID}`
const samples=await fs.open(`${prefix}-samples.jsonl`,'ax')
const summaries=[]
const authRefreshes=[]
const buildFingerprints=JSON.parse(await fs.readFile('/evidence/performance-build-fingerprints.json','utf8'))
const project=process.env.PERF_COMPOSE_PROJECT||'dbhub-iam-phase1-test'
if(!['dbhub-iam-phase1-test','dbhub-iam-phase1-resumed','dbhub-iam-phase1-resumed2'].includes(project))throw Error('registered isolated benchmark project required')
const names={baseline:`${project}-perf-baseline-1`,candidate:`${project}-perf-candidate-1`}
const base={baseline:'http://perf-baseline:8080',candidate:'http://perf-candidate:8080'}
// The isolated driver can only stop/start its two named benchmark containers.
// No arbitrary container, exec, file, image, network or permission mutation API.
function docker(build,operation){
 if(!Object.hasOwn(names,build)||!['stop','start','json'].includes(operation))throw Error('invalid isolated process control')
 const path=`/containers/${names[build]}/${operation}${operation==='stop'?'?t=15':''}`
 return new Promise((resolve,reject)=>{const req=http.request({socketPath:'/var/run/docker.sock',method:operation==='json'?'GET':'POST',path},res=>{let text='';res.on('data',chunk=>text+=chunk);res.on('end',()=>{if(res.statusCode>=300&&res.statusCode!==304)return reject(Error(`isolated process ${operation} failed (${res.statusCode})`));resolve(operation==='json'?JSON.parse(text):null)})});req.on('error',reject);req.end()})
}
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms))
async function ready(build){const until=Date.now()+120000;while(Date.now()<until){try{if((await fetch(base[build]+'/api/ready',{signal:AbortSignal.timeout(3000)})).ok)return}catch{};await pause(250)};throw Error('isolated process not ready')}
function sessionFrom(data){if(!data?.access_token||!data.refresh_token)throw Error('normal authenticated session required');const claims=JSON.parse(Buffer.from(data.access_token.split('.')[1],'base64url').toString());if(!Number.isFinite(claims.exp))throw Error('session expiry missing');return{token:data.access_token,refresh:data.refresh_token,expires:claims.exp*1000,pending:null}}
async function login(build,profile){const response=await fetch(base[build]+'/api/v1/auth/login',{method:'POST',headers:{'Content-Type':'application/json','X-DBHub-CSRF':'1'},body:JSON.stringify({username:`perf-user-${profile+1}`,password:'TestPassword123!'}),signal:AbortSignal.timeout(20000)});const result=await response.json();if(!response.ok||!result.data?.access_token)throw Error(`fixture login failed ${build}/${profile}: ${response.status}`);return sessionFrom(result.data)}
// The frozen audit workload can outlast the normal access-token lifetime.
// Rotate through the ordinary refresh API before timing a request, just as
// initial login is outside timing. Never extend TTLs, cache a permission result,
// retry a measured failure or remove its sample. Apply this to both builds.
async function currentToken(build,session){
 if(session.expires>Date.now()+60000)return session.token
 if(!session.pending)session.pending=(async()=>{const response=await fetch(base[build]+'/api/v1/auth/refresh',{method:'POST',headers:{'Content-Type':'application/json','X-DBHub-CSRF':'1'},body:JSON.stringify({refresh_token:session.refresh}),signal:AbortSignal.timeout(20000)});const result=await response.json();authRefreshes.push({build,status:response.status,at:new Date().toISOString()});if(!response.ok)throw Error(`normal session refresh failed: ${build}/${response.status}`);const next=sessionFrom(result.data);session.token=next.token;session.refresh=next.refresh;session.expires=next.expires})().finally(()=>{session.pending=null})
 await session.pending;return session.token
}
function expected(profile,path){return path.endsWith('/audit-logs')&&![0,5].includes(profile)?403:200}
function shape(path,data){
 if(path.endsWith('/workspaces'))return {count:Array.isArray(data)?data.length:-1}
 const list=Array.isArray(data)?data:data?.items
 return {count:Array.isArray(list)?list.length:-1,total:data?.total}
}
function validShape(path,value){
 if(path.endsWith('/workspaces'))return value?.count===2
 if(path.endsWith('/connections'))return value?.count===1
 if(path.endsWith('/published-reports'))return value?.total===100
 if(path.endsWith('/published-dashboards'))return value?.total===30
 if(path.endsWith('/audit-logs'))return value?.total>=10000
 return value?.count>=0
}
async function batch(build,rep,profile,path,cache,session,count,warmup){
 let next=0;const rows=[]
 const worker=async()=>{for(;;){const index=next++;if(index>=count)return;const token=await currentToken(build,session),start=performance.now();let status=0,bytes=0,logical
 try{const response=await fetch(base[build]+path,{headers:{Authorization:`Bearer ${token}`,'X-DBHub-CSRF':'1'},signal:AbortSignal.timeout(60000)});status=response.status;const body=await response.text();bytes=Buffer.byteLength(body);if(status===200)logical=shape(path,JSON.parse(body).data)}catch{}
 rows.push({index,ms:performance.now()-start,status,bytes,...(logical?{shape:logical}:{})})}}
 await Promise.all(Array.from({length:workload.measurement.concurrency},worker))
 rows.sort((a,b)=>a.index-b.index)
 await samples.write(rows.map(row=>JSON.stringify({build,rep,profile,path,cache,warmup,...row})).join('\n')+'\n')
 const latency=rows.map(row=>row.ms).sort((a,b)=>a-b),want=expected(profile,path)
 const failed=rows.filter(row=>row.status!==want||want===200&&!validShape(path,row.shape))
 const item={build,rep,profile,path,cache,warmup,count,p50:latency[Math.ceil(count*.5)-1],p95:latency[Math.ceil(count*.95)-1],p99:latency[Math.ceil(count*.99)-1],expected_status:want,unexpected:failed.length,statuses:rows.reduce((m,r)=>(m[r.status]=(m[r.status]||0)+1,m),{}),first_shape:rows[0]?.shape}
 if(!warmup){summaries.push(item);await fs.writeFile(`${prefix}-progress.json`,JSON.stringify({run_id:runID,status:'RUNNING',registered_workload_sha256:crypto.createHash('sha256').update(JSON.stringify(workload)).digest('hex'),summaries},null,2));console.log(JSON.stringify(item))}
 if(failed.length)throw Error(`unexpected outcomes in ${build}/${profile}/${path}/${cache}; all samples retained`)
 return item
}
let status='FAILED',failure,measurementCompleted=false
const thresholdFailures=[]
try{
 await docker('baseline','stop');await docker('candidate','stop')
 for(let rep=1;rep<=workload.measurement.repetitions;rep++)for(let profile=0;profile<workload.legacy_actor_profiles.length;profile++)for(const template of workload.paths){
  const path=template.replace('{original_workspace}','1')
  for(const build of ['baseline','candidate']){
   await docker(build,'start');await ready(build)
   const token=await login(build,profile)
   for(const cache of workload.measurement.cache_profiles){
    // Fresh process for this profile/path's cold phase; its warm phase uses the
    // identical process, pool and data after the complete measured cold phase.
    await batch(build,rep,profile,path,cache,token,workload.measurement.warmup_requests_per_profile_path,true)
    await batch(build,rep,profile,path,cache,token,workload.measurement.measured_requests_per_profile_path,false)
   }
   await docker(build,'stop')
  }
  // Retain every failed gate and finish the complete registered grid. A slow
  // pair still fails acceptance; collecting later pairs never waives it.
  // Unexpected HTTP outcomes or data shapes still stop the run immediately.
  for(const cache of workload.measurement.cache_profiles){const pair=summaries.filter(s=>s.rep===rep&&s.profile===profile&&s.path===path&&s.cache===cache),old=pair.find(s=>s.build==='baseline'),current=pair.find(s=>s.build==='candidate');if(current.p95>old.p95*1.2){const failed={rep,profile,path,cache,baseline_p95:old.p95,candidate_p95:current.p95,ratio:current.p95/old.p95};thresholdFailures.push(failed);console.error(JSON.stringify({threshold_failure:failed,remaining_registered_pairs_continue:true}))}}
 }
 measurementCompleted=true
 if(thresholdFailures.length)failure=`p95 gate failed in ${thresholdFailures.length} registered comparisons; complete failed run retained`
 else status='COMPLETED'
}catch(e){failure=String(e);console.error(failure)}finally{
 await samples.close()
 const comparisons=[]
 for(let rep=1;rep<=3;rep++)for(let profile=0;profile<7;profile++)for(const path of workload.paths.map(p=>p.replace('{original_workspace}','1')))for(const cache of workload.measurement.cache_profiles){const pair=summaries.filter(s=>s.rep===rep&&s.profile===profile&&s.path===path&&s.cache===cache),old=pair.find(p=>p.build==='baseline'),current=pair.find(p=>p.build==='candidate');if(old&&current)comparisons.push({rep,profile,path,cache,expected_status:old.expected_status,baseline_p95:old.p95,candidate_p95:current.p95,ratio:current.p95/old.p95,pass:current.p95<=old.p95*1.2&&current.unexpected===0&&old.unexpected===0})}
 const result={run_id:runID,status,failure,measurement_completed:measurementCompleted,threshold_failures:thresholdFailures,acceptance_pass:status==='COMPLETED'&&measurementCompleted&&comparisons.length===252&&comparisons.every(c=>c.pass),build_fingerprints:buildFingerprints,registered_workload:workload,normal_auth_refreshes:authRefreshes,summaries,comparisons,sample_file:`performance-${runID}-samples.jsonl`,outliers_removed:false}
 await fs.writeFile(`${prefix}-result.json`,JSON.stringify(result,null,2));console.log(JSON.stringify({run_id:runID,status,comparisons:comparisons.length,acceptance_pass:result.acceptance_pass,failure}))
}
if(status!=='COMPLETED')process.exitCode=1
