// Execute the real interceptor with controlled transport timing. No browser,
// network, tokens or workspace data are read from the developer's machine.
import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'
import axios from 'axios'

const source=await fs.readFile(new URL('../src/utils/request.ts',import.meta.url),'utf8')
const deferred=()=>{let resolve;const promise=new Promise(r=>{resolve=r});return {promise,resolve}}
async function setup(){
 let epoch=0,refreshCalls=0,redirects=0
 const pending=[],refreshes=[]
 const user={token:'test-old-access',refreshToken:'test-old-refresh',user:{id:1},workspaceId:1,effective:null,identityEpoch:()=>epoch,clearPermissions(){},clearAuth(){epoch++;user.token='';user.refreshToken=''}}
 const fakeAxios={...axios}
 fakeAxios.create=()=>axios.create({adapter:config=>new Promise((resolve,reject)=>pending.push({config,resolve,reject}))})
 fakeAxios.post=async()=>{refreshCalls++;const item=deferred();refreshes.push(item);return item.promise}
 const context=vm.createContext({AbortSignal,Promise,Map,Set,Number,String,Date,Error,document:{cookie:''},localStorage:{setItem(){}},console})
 const values={
  axios:{default:fakeAxios,AxiosError:axios.AxiosError},
  'element-plus':{ElMessage:{error(){}}},
  '../stores/user':{useUserStore:()=>user},
  './auth-cookie':{setAuthCookie(){}},
  '../router':{default:{currentRoute:{value:{name:'profile',fullPath:'/profile'}},replace(){redirects++}}},
  './workspace-context':{changeWorkspaceContext(){},workspaceGeneration:()=>0,workspaceSignal:()=>new AbortController().signal},
  './iam-mode':{iamUiEnabled:true},
 }
 const module=new vm.SourceTextModule(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText,{context})
 await module.link(specifier=>{const value=values[specifier];assert.ok(value,`unexpected import ${specifier}`);return new vm.SyntheticModule(Object.keys(value),function(){for(const [key,item] of Object.entries(value))this.setExport(key,item)},{context})})
 await module.evaluate()
 const turn=async()=>{for(let i=0;i<12;i++)await Promise.resolve()}
 const success=(item,data={ok:true})=>item.resolve({status:200,statusText:'OK',headers:{},config:item.config,data:{code:0,data}})
 const unauthorized=item=>item.reject(new axios.AxiosError('synthetic unauthorized',undefined,item.config,undefined,{status:401,statusText:'Unauthorized',headers:{},config:item.config,data:{code:40100,message:'synthetic'}}))
 return {request:module.namespace.default,pending,refreshes,turn,success,unauthorized,counts:()=>({refreshCalls,redirects}),switchIdentity(){epoch++;user.token='test-new-access';user.refreshToken='test-new-refresh';user.user={id:2}}}
}
test('late identity-scoped response is discarded after changing accounts',async()=>{
 const a=await setup(),result=a.request.get('/iam-command-status').catch(e=>e)
 await a.turn();a.switchIdentity();a.success(a.pending[0]);const error=await result
 assert.ok(axios.isCancel(error));assert.deepEqual(a.counts(),{refreshCalls:0,redirects:0})
})
test('late global 401 cannot refresh or replay under the next identity',async()=>{
 const a=await setup(),result=a.request.post('/auth/change-password',{old_password:'synthetic',new_password:'synthetic'}).catch(e=>e)
 await a.turn();a.switchIdentity();a.unauthorized(a.pending[0]);assert.ok(axios.isCancel(await result))
 assert.equal(a.pending.length,1);assert.deepEqual(a.counts(),{refreshCalls:0,redirects:0})
})
test('refresh single-flight is separated by identity and cannot log out a new session',async()=>{
 const a=await setup(),old=a.request.get('/iam-command-status').catch(e=>e)
 await a.turn();a.unauthorized(a.pending[0]);await a.turn();assert.equal(a.refreshes.length,1)
 a.switchIdentity();const current=a.request.get('/iam-command-status')
 await a.turn();a.unauthorized(a.pending[1]);await a.turn();assert.equal(a.refreshes.length,2)
 a.refreshes[0].resolve({data:{data:{access_token:'obsolete-access',refresh_token:'obsolete-refresh'}}});assert.ok(axios.isCancel(await old))
 a.refreshes[1].resolve({data:{data:{access_token:'current-access',refresh_token:'current-refresh'}}});await a.turn()
 assert.equal(a.pending.length,3);assert.equal(a.pending[2].config.headers.Authorization,'Bearer current-access')
 a.success(a.pending[2]);await current;assert.equal(a.counts().redirects,0)
})

test('rollback and legacy restrictions cannot regain buttons through the old role fallback',async()=>{
 const code=await fs.readFile(new URL('../src/utils/permission.ts',import.meta.url),'utf8')
 const context=vm.createContext({}),module=new vm.SourceTextModule(ts.transpileModule(code,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText,{context})
 await module.link(()=>new vm.SyntheticModule(['iamUiEnabled'],function(){this.setExport('iamUiEnabled',true)},{context}));await module.evaluate()
 const check=snapshot=>module.namespace.featureAllowed({effective:{snapshot},can:()=>false},'report.design',true)
 assert.equal(check({target:false}),true)
 assert.equal(check({target:true}),false)
 assert.equal(check({target:false,rollback_guard:true}),false)
 assert.equal(check({target:false,denied_actions:['report.design']}),false)
})
