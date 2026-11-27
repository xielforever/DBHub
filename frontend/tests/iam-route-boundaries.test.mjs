// Run the actual router guard. Only browser history and store transport are
// replaced; no application source fragments or permission results are copied.
import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'
import { handlingCenterCategories } from '../src/utils/editorial-navigation.ts'

const source=await fs.readFile(new URL('../src/router/index.ts',import.meta.url),'utf8')
async function setup(target=false){
 let guard, routes
 const user={isLoggedIn:true,workspaceId:1,role:'admin',user:{id:1},effective:{snapshot:{target}},actionControlled:target,
  permissionStatus:'ready',visibleMenus:[],
  async loadWorkspaces(){},async loadPermissions(){},hasCapability:()=>true,can:()=>false,safeHome:'/reports'}
 const context=vm.createContext({document:{title:''}})
 const values={
  '../navigation':{pageTitles:{}},
  'vue-router':{createWebHistory:()=>({}),createRouter(config){
   routes=config.routes.flatMap(record=>record.children??[record])
   return {beforeEach(fn){guard=fn}}
  }},
  '../stores/user':{useUserStore:()=>user},
  '../utils/iam-mode':{iamUiEnabled:true},
  '../utils/editorial-navigation':{handlingCenterCategories},
 }
 const module=new vm.SourceTextModule(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText,{context})
 await module.link(specifier=>{const value=values[specifier];assert.ok(value,`unexpected import ${specifier}`);return new vm.SyntheticModule(Object.keys(value),function(){for(const [key,item] of Object.entries(value))this.setExport(key,item)},{context})})
 await module.evaluate()
 const route=(query={},path='/query',hash='')=>{
  const record=routes.find(record=>'/'+record.path.replace(/^\//,'')===path.replace(/\/$/,''))
  return {name:record?.name,path,fullPath:path+'?'+new URLSearchParams(query)+hash,meta:record?.meta??{},query,hash}
 }
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

test('space member and proposal links validate the original workspace before opening', async () => {
  const proposalId = '6e8135e5-b7e1-48e8-a5db-2ec4144fd3b2'
  const selections = [
    { tab: 'members', member_id: '27' },
    { tab: 'governance', proposal_id: proposalId },
    { tab: 'members', member_id: '27', return_proposal_id: proposalId },
  ]
  for (const controlled of [false, true]) {
    const a = await setup(controlled)
    for (const query of selections) {
      const detail = a.route(query, '/space')
      assert.equal(detail.name, 'space-management')
      const unscoped = await a.visit(detail)
      assert.equal(unscoped.path, '/workspace-link')
      assert.equal(unscoped.query.from, detail.fullPath)

      const other = await a.visit(a.route({ ...query, workspace_id: '2' }, '/space'))
      assert.equal(other.path, '/workspace-link')
      assert.equal(other.query.workspace_id, '2')
      const inheritedOther = await a.visit(detail, a.route({ workspace_id: '2' }, '/space'))
      assert.equal(inheritedOther.path, '/workspace-link')
      assert.equal(inheritedOther.query.workspace_id, '2')

      const inheritedCurrent = await a.visit(detail, a.route({ workspace_id: '1' }, '/space'))
      assert.equal(inheritedCurrent.path, '/space')
      assert.deepEqual({ ...inheritedCurrent.query }, { ...query, workspace_id: '1' })

      const scoped = a.route(inheritedCurrent.query, '/space')
      a.user.can = () => false
      assert.equal((await a.visit(scoped)).path, '/access-denied')
      a.user.can = action => action === 'space.page'
      assert.equal(await a.visit(scoped), true)
    }
  }
})

test('management directory and handling center keep the correct scope and visible source boundary', async () => {
  for (const controlled of [false, true]) {
    const a = await setup(controlled)
    assert.equal((await a.visit(a.route({}, '/space'))).name, 'workspace-directory')
    assert.equal(await a.visit(a.route({}, '/spaces')), true)
    const handling = a.route({ workspace_id: '1' }, '/handling')
    assert.equal((await a.visit(handling)).path, '/access-denied')
    a.user.visibleMenus = [{ id: 'M07', path: '/tickets', label: '数据库变更工单' }]
    assert.equal(await a.visit(handling), true)
    const foreign = a.route({ workspace_id: '2', section: 'business', request: '1042' }, '/handling')
    assert.equal((await a.visit(foreign)).path, '/workspace-link')
    assert.equal((await a.visit(foreign)).query.from, foreign.fullPath)
  }
})

test('old platform workspace addresses resolve to the shared directory with only search and page', async () => {
  const a = await setup(true)
  a.user.workspaceId = 0
  const redirect = await a.visit(a.route({ tab: 'spaces', q: '研发', page: '2', workspace_id: '4' }, '/platform'))
  assert.equal(redirect.name, 'workspace-directory')
  assert.deepEqual({ ...redirect.query }, { q: '研发', page: '2' })
  assert.equal(redirect.replace, true)
  assert.equal(await a.visit(a.route(redirect.query, '/spaces')), true)
  for (const tab of ['accounts', 'identity', 'operations']) {
    assert.equal(await a.visit(a.route({ tab }, '/platform')), true)
  }
})

test('explicit space categories without a selected object canonicalize to the current workspace', async () => {
  for (const controlled of [false, true]) {
    const a = await setup(controlled)
    a.user.can = action => action === 'space.page'
    for (const query of [{ tab: 'members' }, { tab: 'governance' }]) {
      const result = await a.visit(a.route(query, '/space'))
      assert.equal(result.path, '/space')
      assert.deepEqual({ ...result.query }, { ...query, workspace_id: '1' })
      assert.equal(await a.visit(a.route(result.query, '/space')), true)
    }
  }
})

test('legacy source task links retain their target and keep both task permissions', async () => {
  const target = await setup(true)
  const query = { workspace_id: '1', connection_id: '27', kind: 'collect', page: '2' }
  const old = target.route({ ...query, tab: 'tasks' }, '/sources', '#records')
  const redirected = await target.visit(old)
  assert.equal(redirected.path, '/source-tasks')
  assert.deepEqual({ ...redirected.query }, query)
  assert.equal(redirected.hash, '#records')
  const canonical = target.route(redirected.query, redirected.path, redirected.hash)
  for (const permission of ['source.task.view', 'connection.task.view']) {
    target.user.can = action => action === permission
    assert.equal(await target.visit(canonical), true)
  }
  target.user.can = action => action === 'source.page'
  assert.equal((await target.visit(canonical)).path, '/access-denied')

  const legacy = await setup(false)
  const legacyRedirect = await legacy.visit(old)
  assert.equal(legacyRedirect.path, '/source-tasks')
  assert.deepEqual({ ...legacyRedirect.query }, query)
  assert.equal(await legacy.visit(canonical), true)
})

test('legacy source task links validate workspace before redirecting', async () => {
  for (const controlled of [false, true]) {
    const a = await setup(controlled)
    const query = { connection_id: '27', kind: 'collect', tab: 'tasks' }
    assert.equal((await a.visit(a.route(query, '/sources'))).path, '/workspace-link')
    const other = await a.visit(a.route({ ...query, workspace_id: '2' }, '/sources'))
    assert.equal(other.path, '/workspace-link')
    assert.equal(other.query.workspace_id, '2')
    const inherited = await a.visit(a.route(query, '/sources'), a.route({ workspace_id: '1' }))
    assert.equal(inherited.path, '/sources')
    assert.equal(inherited.query.workspace_id, '1')
    const resolved = await a.visit(a.route(inherited.query, inherited.path))
    assert.equal(resolved.path, '/source-tasks')
    assert.equal(resolved.query.connection_id, '27')
    assert.equal(resolved.query.kind, 'collect')
  }
})

test('legacy runtime links resolve to the permission-gated dashboard without redirecting back', async () => {
  const target = await setup(true)
  const old = target.route({ workspace_id: '1', tab: 'runtime' }, '/sources')
  const redirected = await target.visit(old)
  assert.equal(redirected.path, '/dashboard')
  assert.deepEqual({ ...redirected.query }, { workspace_id: '1' })
  const dashboard = target.route(redirected.query, redirected.path)
  target.user.can = action => action === 'source.runtime.view'
  assert.equal(await target.visit(dashboard), true)
  target.user.can = action => action === 'source.page'
  assert.equal((await target.visit(dashboard)).path, '/access-denied')
})
