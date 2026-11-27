import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'
import {computed,nextTick,ref,reactive} from 'vue'
import crypto from 'node:crypto'

const file=await fs.readFile(new URL('../src/views/governance/ChangeDialog.vue',import.meta.url),'utf8')
const source=file.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
async function setup(t, propose){
 const props=reactive({kind:'action_release',restriction:{sequence:91,subject_id:8,username:'synthetic-reader',action_id:'report.view',reason:'old prohibition'},roles:[],members:[{id:8,username:'synthetic-reader'}],groups:[],delegations:[{id:'finite',version:4,delegate_id:2,governance_actions:['assignment.propose'],grantable_actions:['report.view'],target_ids:[8],valid_until:new Date(Date.now()+86400000).toISOString()}]})
 const user={user:{id:2},workspace:{name:'isolated'},effective:{snapshot:{config_version:15,authorization_epoch:31},runtime_enabled:['report.view'],catalog:{capabilities:[]}}},submissions=[],events=[],previews=[]
 let resolve;const pending=new Promise(r=>{resolve=r})
 const cleanups=[]
 const dispose=()=>{for(const callback of cleanups.splice(0))callback()}
 t.after(dispose)
 const timers=[]
 const schedule=(callback,delay)=>{
  const timer=setTimeout(callback,delay).unref()
  timers.push(timer)
  return timer
 }
 const context=vm.createContext({Date,crypto,setTimeout:schedule,clearTimeout,defineProps:()=>props,defineEmits:()=>event=>events.push(event)})
 const values={vue:{computed,nextTick,onScopeDispose:cleanup=>cleanups.push(cleanup),ref},'../../stores/user':{useUserStore:()=>user},'../../api/iam':{iamApi:{preview:input=>{previews.push(input);return pending},propose:async input=>{submissions.push(input);await propose?.()}}},'../../utils/iam-labels':{actionLabel:x=>x,changeLabels:{},governanceLabels:{}}}
 const module=new vm.SourceTextModule(ts.transpileModule(source+'\nexport {choices,releaseReason,preview,resetPreview,submit,command,impact}',{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText,{context})
 await module.link(specifier=>{const value=values[specifier];assert.ok(value,specifier);return new vm.SyntheticModule(Object.keys(value),function(){for(const [key,item]of Object.entries(value))this.setExport(key,item)},{context})});await module.evaluate()
 return {component:module.namespace,props,submissions,events,previews,resolve,dispose,timers}
}
test('release form carries the exact ban generation through preview and submission',async t=>{
 const a=await setup(t);a.component.releaseReason.value='independent temporary restoration'
 const preparing=a.component.preview();a.resolve({beneficiaries:[8],actions:['report.view'],request_hash:'bounded'});await preparing;await a.component.submit()
 assert.equal(a.submissions.length,1)
 const sent=JSON.parse(JSON.stringify(a.submissions[0]));assert.equal(sent.patch.kind,'action_release');assert.equal(sent.patch.subject_id,8);assert.equal(sent.patch.action_id,'report.view');assert.equal(sent.patch.revocation_sequence,91);assert.equal(sent.patch.reason,'independent temporary restoration');assert.equal(sent.patch.role_id,undefined)
 assert.equal(sent.request_id,a.previews[0].request_id);assert.deepEqual(a.events,['saved'])
})
test('changed release intent discards a late preview and prevents unreviewed submission',async t=>{
 const a=await setup(t);a.component.releaseReason.value='first intent'
 const preparing=a.component.preview();a.component.releaseReason.value='changed intent';a.component.resetPreview();a.resolve({beneficiaries:[8],actions:['report.view']});await preparing;await a.component.submit()
 assert.equal(a.component.impact.value,undefined);assert.equal(a.component.command.value,undefined);assert.equal(a.submissions.length,0)
})
test('release chooser requires both the exact member and action within delegation',async t=>{
 const a=await setup(t);assert.equal(a.component.choices.value.length,1)
 a.props.delegations[0].target_ids=[]
 assert.equal(a.component.choices.value.length,0)
 const b=await setup(t);b.props.delegations[0].grantable_actions=[];assert.equal(b.component.choices.value.length,0)
 const c=await setup(t);c.props.delegations[0].target_ids=[];assert.equal(c.component.choices.value.length,0)
})
test('a late submission failure cannot restart a disposed preview timer',async t=>{
 let rejectSubmission
 const response=new Promise((_,reject)=>{rejectSubmission=reject})
 const a=await setup(t,()=>response)
 a.component.releaseReason.value='temporary restoration'
 const preparing=a.component.preview()
 a.resolve({beneficiaries:[8],actions:['report.view']})
 await preparing
 assert.equal(a.timers.length,1)
 const submitting=a.component.submit()
 a.dispose()
 rejectSubmission(new Error('late failure'))
 await submitting
 assert.equal(a.submissions.length,1)
 assert.equal(a.timers.length,1)
})
