// Capture the expanded router for review. This tool never changes the shipped
// authorization registry or silently approves a newly registered endpoint.
import fs from 'node:fs/promises'
const base='http://acceptance-api:8080/api/v1'
const login=await fetch(base+'/auth/login',{method:'POST',headers:{'Content-Type':'application/json','X-DBHub-CSRF':'1'},body:JSON.stringify({username:'iam-admin',password:'TestPassword123!'})})
const session=(await login.json()).data
if(!session?.access_token)throw Error('synthetic route reader unavailable')
const response=await fetch(base+'/workspaces/1/iam/routes',{headers:{Authorization:`Bearer ${session.access_token}`}})
const data=(await response.json()).data
if(!response.ok||!Array.isArray(data?.items))throw Error('expanded route inventory unavailable')
await fs.writeFile('/evidence/runtime-routes.json',JSON.stringify({version:'DBH-UX-IAM-D1.0',routes:data.items},null,2))
console.log(JSON.stringify({expanded_routes:data.items.length,unclassified:data.items.filter(r=>!r.known&&r.pattern!=='/').length}))
