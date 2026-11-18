import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'
import {computed,ref,watch,reactive,nextTick} from 'vue'

const file=await fs.readFile(new URL('../src/views/governance/WorkspaceLinkView.vue',import.meta.url),'utf8')
const source=file.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
class ApiRequestError extends Error {
 constructor(message, status) { super(message); this.status = status }
}
async function setup(query, workspaces = [{id:1,name:'first'},{id:2,name:'second'}]){
 const route=reactive({query,fullPath:'/workspace-link?'+new URLSearchParams(query)}),switches=[],navigations=[]
 let resolve, reject, signalPermissionStarted
 const pending=new Promise((r,j)=>{resolve=r;reject=j})
 const permissionStarted=new Promise(r=>{signalPermissionStarted=r})
 const user=reactive({workspaces,workspaceId:1,safeHome:'/reports',
  async confirmWorkspace(id,signal){
   signalPermissionStarted()
   await pending
   if(signal.aborted||!this.workspaces.some(space=>space.id===id))return false
   switches.push(id)
   this.workspaceId=id
   return true
  },
  async loadWorkspaces(){}})
 const context=vm.createContext({Error,AbortController})
 const values={vue:{computed,nextTick,ref,watch,onMounted(){},onScopeDispose(){}},'vue-router':{useRoute:()=>route,useRouter:()=>({resolve:value=>{const [path,query]=value.split('?');return {path,query:Object.fromEntries(new URLSearchParams(query))}},replace:value=>navigations.push(value)})},'../../stores/user':{useUserStore:()=>user},
  '../../utils/request':{ApiRequestError},
  '../../components/base/LoadingState.vue':{default:{}},'../../components/base/PageHeader.vue':{default:{}}}
 const module=new vm.SourceTextModule(ts.transpileModule(source+'\nexport {confirm,selected,desired,invalidOrigin,canConfirm}',{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText,{context})
 await module.link(specifier=>{const value=values[specifier];assert.ok(value);return new vm.SyntheticModule(Object.keys(value),function(){for(const [key,item]of Object.entries(value))this.setExport(key,item)},{context})})
 await module.evaluate()
 return {component:module.namespace,user,route,switches,navigations,resolve,reject,permissionStarted,nextTick}
}

test('malformed workspace links cannot be reinterpreted through a manual selection',async()=>{
 const a=await setup({reason:'invalid_workspace',from:'/reports?report_id=27'})
 a.component.selected.value=1
 await a.component.confirm()
 assert.equal(a.component.invalidOrigin.value,true)
 assert.equal(a.switches.length,0);assert.equal(a.navigations.length,0)
})

test('reused confirmation pages reset their origin and discard late navigation',async()=>{
 const a=await setup({workspace_id:'2',from:'/reports?report_id=27'})
 const confirming=a.component.confirm()
 await a.permissionStarted
 assert.deepEqual(a.switches,[])
 a.route.query={workspace_id:'1',from:'/reports?report_id=31'}
 a.route.fullPath='/workspace-link?workspace_id=1&from=new-link'
 await a.nextTick()
 assert.equal(a.component.desired.value,1);assert.equal(a.component.selected.value,1)
 a.route.query={workspace_id:'2',from:'/reports?report_id=27'}
 a.route.fullPath='/workspace-link?'+new URLSearchParams(a.route.query)
 await a.nextTick()
 a.resolve();await confirming
 assert.deepEqual(a.switches,[])
 assert.equal(a.user.workspaceId,1)
 assert.equal(a.navigations.length,0)
})

test('only the available original workspace can enable confirmation and it is rechecked after loading',async()=>{
 const a=await setup({workspace_id:'2',from:'/reports?report_id=27'}, [{id:1,name:'first'}])
 assert.equal(a.component.selected.value,undefined)
 assert.equal(a.component.canConfirm.value,false)
 a.component.selected.value=1
 assert.equal(a.component.canConfirm.value,false)
 await a.component.confirm()
 assert.deepEqual(a.switches,[])
 a.user.workspaces.push({id:2,name:'second'})
 await a.nextTick()
 assert.equal(a.component.selected.value,2)
 assert.equal(a.component.canConfirm.value,true)
 const confirming=a.component.confirm()
 await a.permissionStarted
 assert.equal(a.component.canConfirm.value,false)
 await a.component.confirm()
 assert.deepEqual(a.switches,[])
 a.user.workspaces.pop()
 a.resolve()
 await confirming
 assert.deepEqual(a.switches,[])
 assert.deepEqual(a.navigations,[])
 assert.equal(a.component.canConfirm.value,false)
})

test('conflicting workspace IDs cannot reinterpret the object',async()=>{
 const a=await setup({workspace_id:'2',from:'/reports?workspace_id=1&report_id=27'})
 assert.equal(a.component.invalidOrigin.value,true)
 assert.equal(a.component.canConfirm.value,false)
 await a.component.confirm()
 assert.deepEqual(a.switches,[])
})

test('confirmation rechecks membership before switching and preserves the matching workspace',async()=>{
 const a=await setup({workspace_id:'2',from:'/reports?workspace_id=2&report_id=27'})
 let rechecked=false
 a.user.loadWorkspaces=async force=>{assert.equal(force,true);rechecked=true}
 const confirming=a.component.confirm()
 a.resolve();await confirming
 assert.equal(rechecked,true)
 assert.deepEqual(a.switches,[2])
 assert.equal(a.navigations[0].query.workspace_id,'2')
 assert.equal(a.navigations[0].query.report_id,'27')
})

test('权限拒绝后重读成员资格，撤下已失效的原空间和确认操作', async () => {
 const fixture = await setup({workspace_id:'2',from:'/reports?workspace_id=2&report_id=27'})
 let reads = 0
 fixture.user.loadWorkspaces = async force => {
  assert.equal(force, true)
  if (++reads === 2) fixture.user.workspaces.pop()
 }
 const confirmation = fixture.component.confirm()
 await fixture.permissionStarted
 fixture.reject(new ApiRequestError('成员资格已失效', 404))
 await confirmation
 assert.equal(reads, 2)
 assert.equal(fixture.component.selected.value, undefined)
 assert.equal(fixture.component.canConfirm.value, false)
 assert.deepEqual(fixture.navigations, [])
})
