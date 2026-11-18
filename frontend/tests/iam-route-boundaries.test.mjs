// Run the actual router guard. Only browser history and store transport are
// replaced; no application source fragments or permission results are copied.
import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'

const source=await fs.readFile(new URL('../src/router/index.ts',import.meta.url),'utf8')
async function setup(target=false){
 let guard
 const user={isLoggedIn:true,workspaceId:1,role:'admin',user:{id:1},effective:{snapshot:{target}},actionControlled:target,
  async loadWorkspaces(){},async loadPermissions(){},hasCapability:()=>true,can:()=>false,safeHome:'/reports'}
 const context=vm.createContext({document:{title:''}})
 const values={
  '../navigation':{pageTitles:{}},
  'vue-router':{createWebHistory:()=>({}),createRouter:()=>({beforeEach(fn){guard=fn}})},
  '../stores/user':{useUserStore:()=>user},
  '../utils/iam-mode':{iamUiEnabled:true},
 }
 const module=new vm.SourceTextModule(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText,{context})
 await module.link(specifier=>{const value=values[specifier];assert.ok(value,`unexpected import ${specifier}`);return new vm.SyntheticModule(Object.keys(value),function(){for(const [key,item] of Object.entries(value))this.setExport(key,item)},{context})})
 await module.evaluate()
 const route=(query={},path='/query')=>({name:'query',path,fullPath:path+'?'+new URLSearchParams(query),meta:{},query})
 return {user,route,visit:(to,from=route({},'/login'))=>guard(to,from)}
}

test('legacy and target reject cross-space and malformed explicit origins',async()=>{
 for(const target of [false,true]){
  const a=await setup(target)
  const other=await a.visit(a.route({workspace_id:'2',report_id:'27'}))
  assert.equal(other.path,'/workspace-link');assert.equal(other.query.workspace_id,'2')
  for(const invalid of ['bogus','0','-1','1.5',['1','2']]){
   const result=await a.visit(a.route({workspace_id:invalid,report_id:'27'}))
   assert.equal(result.path,'/workspace-link');assert.equal(result.query.reason,'invalid_workspace')
  }
  assert.equal(a.user.workspaceId,1)
 }
})

test('object links retain their original workspace in both permission modes',async()=>{
 for(const target of [false,true]){
  const a=await setup(target),object=a.route({report_id:'27'})
  assert.equal((await a.visit(object)).path,'/workspace-link')
  const inherited=await a.visit(object,a.route({workspace_id:'2'}))
  assert.equal(inherited.path,'/workspace-link');assert.equal(inherited.query.workspace_id,'2')
  const current=await a.visit(object,a.route({workspace_id:'1'}))
  assert.equal(current.path,'/query');assert.equal(current.query.workspace_id,'1')
 }
})

test('legacy keeps original tools without acquiring target-only action rules',async()=>{
 const a=await setup(false)
 const first=await a.visit(a.route())
 assert.equal(first.path,'/query');assert.equal(first.query.workspace_id,'1')
 assert.equal(await a.visit(a.route({workspace_id:'1'})),true)
 const target=await setup(true)
 assert.equal((await target.visit(target.route({workspace_id:'1'}))).path,'/access-denied')
})

test('metric catalog keeps the confirmed workspace and filters through the compatibility redirect', async () => {
  const target = await setup(true)
  const old = target.route({ workspace_id: '1', q: '收入', page: '2' }, '/metrics')
  const redirected = await target.visit(old)
  assert.equal(redirected.path, '/assets')
  assert.equal(redirected.query.tab, 'metrics')
  assert.equal(redirected.query.workspace_id, '1')
  assert.equal(redirected.query.q, '收入')
  assert.equal(redirected.query.page, '2')
  assert.equal((await target.visit(target.route(redirected.query, '/assets'))).path, '/access-denied')
  target.user.can = action => action === 'catalog.page'
  assert.equal(await target.visit(target.route(redirected.query, '/assets')), true)
  const legacy = await setup(false)
  assert.equal(await legacy.visit(old), true)
})

test('new home respects the permitted menu in both authorization modes',async()=>{
 for(const target of [false,true]){
  for(const home of ['/reports','/dashboards','/no-menu']){
   const a=await setup(target);a.user.safeHome=home
   const result=await a.visit(a.route({workspace_id:'1'},'/home'))
   assert.equal(result.path,home);assert.equal(result.query.workspace_id,'1')
   assert.equal(a.user.actionControlled,target)
  }
 }
})

test('the explicit business home is available only in the confirmed compatibility workspace', async () => {
  const legacy = await setup(false)
  assert.equal(await legacy.visit(legacy.route({ workspace_id: '1', view: 'business' }, '/home')), true)
  const differentWorkspace = await legacy.visit(legacy.route({ workspace_id: '2', view: 'business' }, '/home'))
  assert.equal(differentWorkspace.path, '/workspace-link')
  assert.equal(differentWorkspace.query.workspace_id, '2')
  const target = await setup(true)
  const redirect = await target.visit(target.route({ workspace_id: '1', view: 'business' }, '/home'))
  assert.equal(redirect.path, '/reports')
  assert.equal(redirect.query.workspace_id, '1')
  assert.equal(redirect.query.view, undefined)
})

test('asset details preserve workspace origin independently of directory filters', async () => {
  const a = await setup(true)
  for (const path of ['/assets', '/assets/']) {
    const query = { asset_connection: '7', asset_database: 'catalog', asset_schema: 'public', table: 'orders' }
    assert.equal((await a.visit(a.route(query, path))).path, '/workspace-link')
    assert.equal((await a.visit(a.route({ ...query, workspace_id: '2' }, path))).path, '/workspace-link')
    assert.equal((await a.visit(a.route({ ...query, workspace_id: '1' }, path))).path, '/access-denied')
    a.user.can = action => action === 'catalog.page'
    assert.equal(await a.visit(a.route({ ...query, workspace_id: '1' }, path)), true)
    a.user.can = () => false
  }
})
