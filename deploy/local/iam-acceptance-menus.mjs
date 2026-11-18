// Display-only acceptance against the disposable site. No role/source change.
import fs from 'node:fs/promises'
import {randomUUID,createHash} from 'node:crypto'
const base='http://acceptance-api:8080/api/v1',tokens={}
const mode=process.argv[2]
if(!['hide','restore'].includes(mode))throw Error('explicit isolated display test mode required')
async function call(user,method,path,body,status=200){
 const response=await fetch(base+path,{method,headers:{'Content-Type':'application/json',...(tokens[user]?{Authorization:`Bearer ${tokens[user]}`}:{})},...(body===undefined?{}:{body:JSON.stringify(body)})})
 const result=await response.json();if(response.status!==status||(status<300&&result.code!==0))throw Error(`isolated display case ${method} ${path}: ${response.status}`)
 return result.data
}
for(const user of ['iam-admin','iam-viewer']){const session=await call(user,'POST','/auth/login',{username:user,password:'TestPassword123!'});tokens[user]=session.access_token}
const path='/workspaces/1/iam',admin=await call('iam-admin','GET',path+'/effective')
if(!admin.snapshot.target||admin.catalog.version!=='DBH-UX-IAM-D1.0'||admin.menus.items.length!==11)throw Error('isolated target fixture required')
const baseline=s=>({config_version:s.config_version,authorization_epoch:s.authorization_epoch})
const hash=actions=>createHash('sha256').update(JSON.stringify([...actions].sort())).digest('hex')
if(mode==='hide'){
 const viewer=await call('iam-viewer','GET',path+'/effective'),snapshotPath=`/evidence/acceptance-menu-${Date.now()}.json`
 await fs.writeFile(snapshotPath,JSON.stringify({synthetic_only:true,configuration:admin.menus,actions_sha256:hash(viewer.actions)},null,2),{flag:'wx'})
 const configuration=structuredClone(admin.menus)
 for(const item of configuration.items)if(['M01','M02'].includes(item.id))item.visible=false
 await call('iam-admin','PUT',path+'/menus',{request_id:randomUUID(),baseline:baseline(admin.snapshot),configuration})
 const hidden=await call('iam-viewer','GET',path+'/effective')
 if(hash(hidden.actions)!==hash(viewer.actions)||hidden.menus.items.filter(i=>['M01','M02'].includes(i.id)).some(i=>i.visible))throw Error('hiding changed rights or left business entry visible')
 await call('iam-viewer','GET','/workspaces/1/published-reports')
 await call('iam-viewer','PUT',path+'/home',{menu_id:'M01'},403)
 console.log(JSON.stringify({status:'HIDDEN_FOR_BROWSER_CHECK',snapshot_file:snapshotPath,rights_unchanged:true,direct_report_api:200,hidden_home:403,real_permissions_changed:false}))
}else{
 const snapshotPath=process.argv[3]
 if(!/^\/evidence\/acceptance-menu-\d+\.json$/.test(snapshotPath||''))throw Error('explicit saved display snapshot required')
 const original=JSON.parse(await fs.readFile(snapshotPath,'utf8'));if(!original.synthetic_only)throw Error('isolated snapshot required')
 const configuration={...original.configuration,version:admin.menus.version}
 await call('iam-admin','PUT',path+'/menus',{request_id:randomUUID(),baseline:baseline(admin.snapshot),configuration})
 const viewer=await call('iam-viewer','GET',path+'/effective')
 if(hash(viewer.actions)!==original.actions_sha256||!viewer.menus.items.find(i=>i.id==='M01')?.visible)throw Error('display restore changed original rights')
 console.log(JSON.stringify({status:'RESTORED',rights_unchanged:true,real_permissions_changed:false}))
}
