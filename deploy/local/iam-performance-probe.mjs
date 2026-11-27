const build=process.argv[2]
if(!['baseline','candidate'].includes(build))throw Error('isolated build required')
const base=`http://perf-${build}:8080`
const until=Date.now()+120000
while(Date.now()<until){try{if((await fetch(base+'/api/ready',{signal:AbortSignal.timeout(3000)})).ok)break}catch{};await new Promise(r=>setTimeout(r,300))}
for(let i=1;i<=7;i++){
 const login=await fetch(base+'/api/v1/auth/login',{method:'POST',headers:{'Content-Type':'application/json','X-DBHub-CSRF':'1'},body:JSON.stringify({username:`perf-user-${i}`,password:'TestPassword123!'})})
 const data=await login.json();if(!data.data?.access_token)throw Error(`fixture login ${build}/${i} failed`)
 for(const path of ['/workspaces','/workspaces/1/connections','/workspaces/1/published-reports','/workspaces/1/published-dashboards','/workspaces/1/inbox/todos','/workspaces/1/audit-logs']){
  const started=performance.now(),response=await fetch(base+'/api/v1'+path,{headers:{Authorization:`Bearer ${data.data.access_token}`}}),body=await response.json(),value=body.data
  console.log(JSON.stringify({build,profile:i,path,status:response.status,duration_ms:performance.now()-started,count:Array.isArray(value)?value.length:value?.items?.length,total:value?.total}))
  if(response.status!==(path.endsWith('/audit-logs')&&![1,6].includes(i)?403:200))throw Error('unexpected fixture outcome')
 }
}
