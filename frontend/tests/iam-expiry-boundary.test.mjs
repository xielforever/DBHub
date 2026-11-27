import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'
import {computed,ref} from 'vue'

const source=await fs.readFile(new URL('../src/stores/user.ts',import.meta.url),'utf8')
const iamSource=await fs.readFile(new URL('../src/api/iam.ts',import.meta.url),'utf8')
async function setup(effectiveTimeout){
 let generation=0,next={snapshot:{workspace_id:1,subject:7,config_version:3,authorization_epoch:4,membership_version:5,target:true,mode:'COMPAT_ACTIVE',legacy_data_role:'readonly',denied_actions:[],sources:[]},actions:['report.page','report.view','dashboard.page','dashboard.view'],menus:{items:[]},catalog:{menus:[]}}
 const local=new Map([['dbhub_workspace_id','1'],['dbhub_user',JSON.stringify({id:7})]]),context=vm.createContext({Date,JSON,localStorage:{getItem:k=>local.get(k)??null,setItem:(k,v)=>local.set(k,v),removeItem:k=>local.delete(k)}})
 let get = async () => structuredClone(next)
 const values={pinia:{defineStore:(_id,factory)=>factory},vue:{computed,ref},'../utils/auth-cookie':{setAuthCookie(){},clearAuthCookie(){}},'../utils/request':{default:{get:(url,options)=>get(url,options)}},'../utils/workspace-context':{changeWorkspaceContext(){generation++},workspaceGeneration:()=>generation},'../utils/iam-mode':{iamUiEnabled:true},'../utils/permission':{requiresActionChecks:s=>!!s?.target}}
 const compilerOptions = { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext }
 const api = new vm.SourceTextModule(ts.transpileModule(iamSource, { compilerOptions }).outputText, {
  context, initializeImportMeta(meta) { meta.env = { VITE_IAM_EFFECTIVE_TIMEOUT_MS: effectiveTimeout } },
 })
 const module = new vm.SourceTextModule(ts.transpileModule(source, { compilerOptions }).outputText, { context })
 await module.link(specifier => {
  if (specifier === '../api/iam') return api
  const value = values[specifier]
  assert.ok(value, specifier)
  return new vm.SyntheticModule(Object.keys(value), function () {
   for (const [key, item] of Object.entries(value)) this.setExport(key, item)
  }, { context })
 })
 await module.evaluate()
 return {store:module.namespace.useUserStore(),generation:()=>generation,change:fn=>{fn(next)},setGet:handler=>{get=handler}}
}
test('expiry under unchanged epochs invalidates cached pages and in-flight workspace responses',async()=>{
 const a=await setup();await a.store.loadPermissions(true);const key=a.store.permissionKey.value;assert.equal(a.store.can('report.view'),true)
 a.change(data=>{data.actions=data.actions.filter(x=>x!=='report.view');data.snapshot.denied_actions=['report.view']})
 await a.store.loadPermissions(true)
 assert.equal(a.store.can('report.view'),false);assert.notEqual(a.store.permissionKey.value,key);assert.equal(a.generation(),1)
})
test('permission ordering does not remount unchanged pages; a data identity change does',async()=>{
 const a=await setup();await a.store.loadPermissions(true);const key=a.store.permissionKey.value
 a.change(data=>data.actions.reverse());await a.store.loadPermissions(true)
 assert.equal(a.store.permissionKey.value,key);assert.equal(a.generation(),0)
 a.change(data=>data.snapshot.legacy_data_role='developer');await a.store.loadPermissions(true)
 assert.notEqual(a.store.permissionKey.value,key);assert.equal(a.generation(),1)
})

test('cross-workspace confirmation keeps the current workspace until the live request succeeds', async () => {
 const a = await setup('45000')
 const { store } = a
 await store.loadPermissions(true)
 const current = store.effective.value
 const candidate = { ...current, snapshot: { ...current.snapshot, workspace_id: 2 } }
 store.workspaces.value = [{ id: 1 }, { id: 2 }]
 let resolve
 const response = new Promise(done => { resolve = done })
 a.setGet((url, options) => {
  assert.equal(url, '/workspaces/2/iam/effective')
  assert.equal(options.workspacePermissionCheck, true)
  assert.equal(options.timeout, 45_000)
  return response
 })
 const attempt = new AbortController()
 const confirmation = store.confirmWorkspace(2, attempt.signal)
 assert.equal(store.workspaceId.value, 1)
 assert.equal(store.effective.value.snapshot.workspace_id, 1)
 attempt.abort()
 resolve(candidate)
 assert.equal(await confirmation, false)
 assert.equal(store.workspaceId.value, 1)
 assert.equal(a.generation(), 0)
 assert.equal(await store.confirmWorkspace(2, new AbortController().signal, 'space.page'), false)
 assert.equal(store.workspaceId.value, 1)
 assert.equal(store.effective.value, current)
 assert.equal(await store.confirmWorkspace(2, new AbortController().signal), true)
 assert.equal(store.workspaceId.value, 2)
 assert.equal(store.effective.value.snapshot.workspace_id, 2)
 assert.equal(store.permissionStatus.value, 'ready')
 assert.equal(a.generation(), 1)
})

test('local permission refresh uses its configured deadline and a failure clears the old permissions', async () => {
 const a = await setup('45000')
 await a.store.loadPermissions(true)
 assert.equal(a.store.can('report.view'), true)
 a.setGet((url, options) => {
  assert.equal(url, '/iam/effective')
  assert.equal(options.timeout, 45_000)
  assert.equal(options.workspacePermissionCheck, false)
  throw new Error('synthetic permission read failure')
 })
 await assert.rejects(a.store.loadPermissions(true), /synthetic permission read failure/)
 assert.equal(a.store.effective.value, null)
 assert.equal(a.store.permissionStatus.value, 'error')
 assert.equal(a.store.can('report.view'), false)
})

test('missing or invalid permission deadlines delegate to the shared request default', async () => {
 for (const value of [undefined, '9000', '45000.5', '300001', 'invalid']) {
  const a = await setup(value)
  a.setGet((_url, options) => {
   assert.equal(options.timeout, undefined)
   throw new Error('synthetic check')
  })
  await assert.rejects(a.store.loadPermissions(true), /synthetic check/)
 }
})
