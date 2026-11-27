import fs from 'node:fs';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
import {runWSL as invoke} from './local-tools.mjs';
import {readBundleDigest,requireMatchingDigest} from './local-recovery-evidence.mjs';

// Orchestration only: restored databases, server binaries and all SQL run in
// fresh containers on an internal network with no access to the live network.
if(!process.argv.includes('--local'))throw new Error('仅供本机恢复演练，请传入 --local');
const resumePath=process.argv.find(arg=>arg.startsWith('--resume='))?.slice(9),previous=resumePath?JSON.parse(fs.readFileSync(resumePath,'utf8')):undefined;
if(previous)assert(!previous.pendingBundle,'此恢复入口仅重用数据库已恢复、尚未开始待写审计演练的现场');
const suffix=previous?previous.resources.network.replace(/^dbhub-drill-|-network$/g,''):Date.now().toString(36),prefix='dbhub-drill-'+suffix,network=prefix+'-network';
assert(/^[a-z0-9]+$/.test(suffix));
const names=previous?.resources.names||{metadata:prefix+'-metadata',business:prefix+'-business',redis:prefix+'-redis',backend:prefix+'-backend',recovered:prefix+'-recovered'};
const volumes=previous?.resources.volumes||{metadata:prefix+'-metadata',business:prefix+'-business',binaries:prefix+'-binaries',audit:prefix+'-audit',recoveredAudit:prefix+'-recovered-audit'};
assert(Object.values(names).every(name=>name.startsWith(prefix+'-'))&&Object.values(volumes).every(name=>name.startsWith(prefix+'-')));
const work='/home/dzz/.local/share/dbhub/drills/'+suffix,folder='.dbhub/audits/department-remediation';fs.mkdirSync(folder,{recursive:true});
const backupRoot='/home/dzz/.local/share/dbhub/backups';
if(previous)fs.writeFileSync(folder+'/restore-drill-'+suffix+'-before-resume.json',JSON.stringify(previous,null,2));
const proof=previous?{...previous,resumedAt:new Date().toISOString(),status:'running'}:{startedAt:new Date().toISOString(),checks:[],resources:{network,names,volumes,work},credentialsIncluded:false};delete proof.failedStep;delete proof.message;
const createdContainers=[],createdVolumes=[];let activeBackend=names.backend;
function run(label,args,input,allowFailure=false){const result=invoke(args,input);if(result.status!==0&&!allowFailure){proof.failedStep=label;invoke(['sh','-c','umask 077; mkdir -p "$1"; cat > "$1/last-error.log"','sh',work],result.stderr);proof.diagnosticsPath=work+'/last-error.log';throw new Error(label+' failed (exit '+result.status+'); isolated resources retained for diagnosis');}return result;}
const wsl=(label,...args)=>run(label,args).stdout;
const docker=(label,...args)=>wsl(label,'docker',...args);
function writePrivate(path,content){run('write private drill configuration',['sh','-c','umask 077; cat > "$1"','sh',path],content);}
const parseEnv=raw=>Object.fromEntries(raw.toString().split(/\r?\n/).filter(line=>line&&!line.startsWith('#')&&line.includes('=')).map(line=>{const i=line.indexOf('=');return[line.slice(0,i),line.slice(i+1).replace(/^['"]|['"]$/g,'')]}));
const record=(name,details={})=>{proof.checks.push({name,status:'passed',...details});console.log('PASS',name);};
async function waitDatabase(container){for(let attempt=0;attempt<45;attempt++){const response=run('database readiness',['docker','exec',container,'pg_isready','-U','restore_bootstrap','-d','restore_bootstrap'],undefined,true);if(response.status===0)return;await new Promise(resolve=>setTimeout(resolve,1000));}throw new Error('Restored database startup timeout');}
async function waitServer(){for(let attempt=0;attempt<45;attempt++){if(run('isolated server readiness',['docker','exec',activeBackend,'/out/healthcheck'],undefined,true).status===0)return;await new Promise(resolve=>setTimeout(resolve,1000));}throw new Error('Restored server startup timeout');}
function psql(container,database,input,role='restore_bootstrap'){return run('restore SQL',['docker','exec','-i',container,'psql','-X','-q','-A','-t','-U',role,'-d',database,'-v','ON_ERROR_STOP=1'],input).stdout.toString().trim();}
function restoreDatabase(container,database,owner,bundle){docker('create empty restore database','exec',container,'createdb','-U','restore_bootstrap','-O',owner,database);run('restore '+database,['sh','-c','docker exec -i "$1" pg_restore -U restore_bootstrap -d "$2" --exit-on-error --single-transaction < "$3"','sh',container,database,bundle+'/'+(database==='dbhub_meta_pending'?'dbhub_meta':database)+'.dump']);}
function http(token,path,body){const configuration=[`url = ${JSON.stringify('http://localhost:8080'+path)}`,`request = ${JSON.stringify(body?'POST':'GET')}`];if(token)configuration.push(`header = ${JSON.stringify('Authorization: Bearer '+token)}`);if(body)configuration.push('header = "Content-Type: application/json"',`data = ${JSON.stringify(JSON.stringify(body))}`);const raw=run('isolated HTTP request',['docker','exec','-i',activeBackend,'curl','--noproxy','*','--include','--silent','--show-error','--connect-timeout','2','--max-time','20','--config','-'],configuration.join('\n')+'\n').stdout;const boundary=raw.indexOf('\r\n\r\n');assert(boundary>=0,'HTTP headers missing');const lines=raw.subarray(0,boundary).toString().split('\r\n'),status=Number(lines.shift().split(' ')[1]),headers=new Map(lines.map(line=>{const i=line.indexOf(':');return[line.slice(0,i).toLowerCase(),line.slice(i+1).trim()]}));return{status,headers,body:raw.subarray(boundary+4)};}
async function api(token,path,body){const response=http(token,'/api/v1'+path,body),packet=JSON.parse(response.body.toString());assert.equal(response.status,200,packet.message);assert.equal(packet.code,0,packet.message);return packet.data;}
function tableCounts(container,database,role){const query=`CREATE OR REPLACE FUNCTION pg_temp.dbhub_counts() RETURNS jsonb LANGUAGE plpgsql AS $$ DECLARE item record; amount bigint; result jsonb:='{}'; BEGIN FOR item IN SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind='r' AND n.nspname NOT IN('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT LIKE 'pg_temp_%' LOOP EXECUTE format('SELECT count(*) FROM %I.%I',item.nspname,item.relname) INTO amount; result:=result||jsonb_build_object(item.nspname||'.'||item.relname,amount); END LOOP; RETURN result; END $$; SELECT pg_temp.dbhub_counts();`;return JSON.parse(psql(container,database,query,role));}
function startBackend(name,database,auditVolume,appEnv){writePrivate(work+'/'+name+'.env',`APP_ENV=development\nPORT=8080\nLOG_LEVEL=warn\nAPP_SECRET_KEY=${appEnv.DBHUB_APP_SECRET}\nDATABASE_URL=postgres://dbhub:${appEnv.DBHUB_META_PASSWORD}@metadata:5432/${database}?sslmode=disable\nREDIS_URL=redis://redis:6379/0\nDBHUB_AUDIT_DIR=/var/lib/dbhub/audit\nDBHUB_TARGET_ALLOWLIST=dbhub-wsl-demo-postgres-1:5432\n`);docker('start isolated server','run','-d','--name',name,'--network',network,'--env-file',work+'/'+name+'.env','--init','-v',volumes.binaries+':/out:ro','-v',auditVolume+':/var/lib/dbhub/audit','--entrypoint','/out/server','dbhub-local/golang:1.26.9-bookworm');createdContainers.push(name);activeBackend=name;}
try{
 const bundle=previous?.bundle||process.argv.find(arg=>arg.startsWith('--bundle='))?.slice(9)||wsl('locate completed backup','sh','-c',"find /home/dzz/.local/share/dbhub/backups -mindepth 1 -maxdepth 1 -type d -name 'bundle-*' | sort -r | head -n 1").toString().trim();
 const bundleSha256=readBundleDigest(args=>run('verify bundle checksums',args).stdout,backupRoot,bundle);
 if(previous)requireMatchingDigest(previous.bundleSha256,bundleSha256);
 proof.bundle=bundle;proof.bundleSha256=bundleSha256;
 if(!previous)record('all archived file digests match before restore');
 wsl('prepare private drill directory','mkdir','-p','-m','0700',work);const appEnv=parseEnv(wsl('read retained application key','cat',bundle+'/application.env'));assert(appEnv.DBHUB_APP_SECRET&&appEnv.DBHUB_META_PASSWORD);
 if(!previous){
 writePrivate(work+'/bootstrap.env',`POSTGRES_USER=restore_bootstrap\nPOSTGRES_DB=restore_bootstrap\nPOSTGRES_PASSWORD=${crypto.randomBytes(24).toString('hex')}\n`);
 docker('create isolated network','network','create','--internal',network);
 for(const value of Object.values(volumes)){docker('create restore volume','volume','create',value);createdVolumes.push(value);}
 for(const [kind,alias] of [['metadata','metadata'],['business','dbhub-wsl-demo-postgres-1']]){docker('start restore PostgreSQL','run','-d','--name',names[kind],'--network',network,'--network-alias',alias,'--env-file',work+'/bootstrap.env','-v',volumes[kind]+':/var/lib/postgresql/data','dbhub-local/postgres:16-existing');createdContainers.push(names[kind]);await waitDatabase(names[kind]);const roles=wsl('read retained role definitions','cat',bundle+'/'+kind+'-roles.sql');psql(names[kind],'restore_bootstrap',roles);}
 restoreDatabase(names.metadata,'dbhub_meta','dbhub',bundle);for(const database of ['dbhub_demo','tms','wms','dbhub_analytics'])restoreDatabase(names.business,database,'demo_admin',bundle);
 record('metadata and four business/analytics databases restore with original roles and ACLs');
 for(const database of ['dbhub_demo','tms','wms','dbhub_analytics']){const original=tableCounts('dbhub-wsl-demo-postgres-1',database,'demo_admin'),restored=tableCounts(names.business,database,'demo_admin');assert.deepEqual(restored,original,database+' table counts changed');record(database+' every user table row count matches',{tables:Object.keys(restored).length,rows:Object.values(restored).reduce((a,b)=>a+b,0)});}
 docker('restore matching binaries','run','--rm','--entrypoint','sh','-v',bundle+':/bundle:ro','-v',volumes.binaries+':/out','dbhub-local/golang:1.26.9-bookworm','-ec','tar -xzf /bundle/server.tar.gz -C /out; tar -xzf /bundle/healthcheck.tar.gz -C /out');
 docker('restore pending audit volume','run','--rm','--entrypoint','tar','-v',bundle+':/bundle:ro','-v',volumes.audit+':/audit','dbhub-local/postgres:16-existing','-xzf','/bundle/audit.tar.gz','-C','/audit');
 docker('start isolated session store','run','-d','--name',names.redis,'--network',network,'--network-alias','redis','dbhub-local/redis:7-alpine');createdContainers.push(names.redis);
 startBackend(names.backend,'dbhub_meta',volumes.audit,appEnv);
 }else{createdContainers.push(names.metadata,names.business,names.redis,names.backend);createdVolumes.push(...Object.values(volumes));}
 await waitServer();
 const identities=JSON.parse(wsl('read retained demo identity','cat',bundle+'/demo-identities.json').toString()),state=JSON.parse(fs.readFileSync('.dbhub/simulation/department-resources.json','utf8'));
 const login=await api('', '/auth/login',identities.users.finance),published=await api(login.access_token,'/workspaces/1/published-reports/'+state.reports['finance/kpi'].id);
 const result=await api(login.access_token,`/workspaces/1/published-reports/${published.id}/execute`,{request_id:crypto.randomUUID(),publication_no:published.publication_no});assert.equal(result.rows.length,1);
 const download=http(login.access_token,'/api/v1/workspaces/1/report-exports/1/download');assert.equal(download.status,200);const exportEvidence=JSON.parse(fs.readFileSync(folder+'/exports-verification.json','utf8'));assert.equal(download.headers.get('x-report-snapshot-sha256'),exportEvidence.exports.find(e=>e.id===1).sha256);assert(download.body.byteLength>10000);
 record('retained key decrypts execution credentials and the approved encrypted export snapshot');
 const restoredReports=Number(psql(names.metadata,'dbhub_meta','SELECT count(*) FROM sys_managed_reports;'));assert(restoredReports>=33);record('published department permissions and report definitions survive',{reports:restoredReports});
 // Exercise the newly added data paths with the restored binary and retained
 // application key. A preview reads the target only; no business change is sent.
 const operationState=JSON.parse(fs.readFileSync('.dbhub/simulation/operation-resources.json','utf8')),operationEvidence=JSON.parse(fs.readFileSync(folder+'/operations-browser-verification.json','utf8'));
 assert.equal(operationEvidence.status,'passed');const savedOperation=operationEvidence.operations.find(item=>item.actor==='sales_agent_a');assert(savedOperation);
 const personalLogin=await api('','/auth/login',identities.users.sales_agent_a),operation=await api(personalLogin.access_token,'/workspaces/1/business-operations/requests/'+savedOperation.request_id);
 assert.equal(operation.status,'executed');assert.equal(operation.receipt_sha256,savedOperation.receipt_sha256);assert.equal(operation.receipt.operation_id,savedOperation.operation_id);
 const templates=(await api(personalLogin.access_token,'/workspaces/1/business-operations/templates')).items,pointsTemplate=templates.find(item=>item.id===operationState.templates.member_points_adjust);assert(pointsTemplate);
 const preview=await api(personalLogin.access_token,'/workspaces/1/business-operations/previews',{template_id:pointsTemplate.id,version:pointsTemplate.version,target_key:'MEMBER-A-001',amount:1});assert.equal(preview.before.points,savedOperation.after.points);
 assert.equal(Number(psql(names.business,'dbhub_analytics',`SELECT count(*) FROM dbhub_operation_sandbox.receipts WHERE operation_id='${savedOperation.operation_id}'::uuid`,'demo_admin')),1);
 record('restored encrypted operation receipt and EXECUTE-only credential work; retained signing key verifies a readonly target preview',{operationId:savedOperation.operation_id,receiptHash:savedOperation.receipt_sha256});
 const deliveries=(await api(login.access_token,'/workspaces/1/report-deliveries')).items,delivery=deliveries.find(item=>item.status==='succeeded');assert(delivery);
 const deliveredSnapshot=await api(login.access_token,'/workspaces/1/report-deliveries/'+delivery.delivery_id);assert.equal(deliveredSnapshot.snapshot.result.rows.length,1);assert(deliveredSnapshot.snapshot.result.columns.includes('net_revenue'));assert(deliveredSnapshot.snapshot.params.start_date&&deliveredSnapshot.snapshot.params.end_date);
 record('encrypted subscription snapshot restores with frozen parameters and current audience checks',{deliveryId:delivery.delivery_id});
 const governanceSQL=`SELECT json_build_object('mfa_count',(SELECT count(*) FROM sys_user_mfa),'mfa_sha256',(SELECT md5(COALESCE(string_agg(md5(to_jsonb(m)::text),'' ORDER BY user_id),'')) FROM sys_user_mfa m),'members_sha256',(SELECT md5(COALESCE(string_agg(md5(to_jsonb(m)::text),'' ORDER BY workspace_id,user_id),'')) FROM sys_workspace_members m),'identities_sha256',(SELECT md5(COALESCE(string_agg(md5(to_jsonb(i)::text),'' ORDER BY issuer,subject),'')) FROM sys_external_identities i))`;
 const governance=JSON.parse(psql(names.metadata,'dbhub_meta',governanceSQL));assert.deepEqual(governance,JSON.parse(psql('dbhub-local-metadata-1','dbhub_meta',governanceSQL,'dbhub')));assert(governance.mfa_count>0);
 record('MFA encrypted material, external identity binding rows and delegated membership capabilities retain exact stored values',{mfaRecords:governance.mfa_count});
 // Produce a real pending audit on the isolated app while its DB is offline.
 docker('stop isolated metadata','stop',names.metadata);docker('isolate unavailable metadata','network','disconnect','--force',network,names.metadata);
 const unavailable=http('','/api/v1/auth/login',{username:'restore_drill_unavailable',password:'synthetic-probe-only'});assert.equal(unavailable.status,503);
 const pending=docker('list durable pending audit','exec',names.backend,'sh','-c','find /var/lib/dbhub/audit -maxdepth 1 -name "*.audit" -printf "%f\\n"').toString().trim().split('\n').filter(Boolean).map(file=>file.replace(/\.audit$/,''));assert(pending.length>0);assert(pending.every(id=>/^[a-f0-9-]{36}$/.test(id)));
 docker('restart metadata without app connectivity','start',names.metadata);await waitDatabase(names.metadata);
 const pendingBase=work+'/pending-bundles';
 run('back up the isolated pending audit',['env','DBHUB_LOCAL_BACKUP_DIR='+pendingBase,'DBHUB_LOCAL_BACKEND_CONTAINER='+names.backend,'DBHUB_LOCAL_METADATA_CONTAINER='+names.metadata,'DBHUB_LOCAL_BUSINESS_CONTAINER='+names.business,'bash','/mnt/c/dev/projects/DBHub/scripts/ops/backup-local-bundle.sh','--local']);
 const pendingBundle=wsl('locate pending-audit bundle','sh','-c','find "$1" -mindepth 1 -maxdepth 1 -type d -name "bundle-*" | sort -r | head -n 1','sh',pendingBase).toString().trim();assert(pendingBundle.startsWith(pendingBase+'/bundle-'));proof.pendingBundle=pendingBundle;
 docker('stop first restored server','stop',names.backend);restoreDatabase(names.metadata,'dbhub_meta_pending','dbhub',pendingBundle);
 assert.equal(Number(psql(names.metadata,'dbhub_meta_pending',`SELECT count(*) FROM sys_audit_logs WHERE event_id IN (${pending.map(id=>"'"+id+"'::uuid").join(',')});`)),0);
 docker('recover original metadata alias','network','connect','--alias','metadata',network,names.metadata);
 docker('restore captured pending audit files','run','--rm','--entrypoint','tar','-v',pendingBundle+':/bundle:ro','-v',volumes.recoveredAudit+':/audit','dbhub-local/postgres:16-existing','-xzf','/bundle/audit.tar.gz','-C','/audit');
 startBackend(names.recovered,'dbhub_meta_pending',volumes.recoveredAudit,appEnv);await waitServer();
 let delivered=0;for(let attempt=0;attempt<20;attempt++){delivered=Number(psql(names.metadata,'dbhub_meta_pending',`SELECT count(DISTINCT event_id) FROM sys_audit_logs WHERE event_id IN (${pending.map(id=>"'"+id+"'::uuid").join(',')});`));if(delivered===pending.length)break;await new Promise(resolve=>setTimeout(resolve,1000));}assert.equal(delivered,pending.length);
 docker('restart after audit recovery','restart',names.recovered);await waitServer();assert.equal(Number(psql(names.metadata,'dbhub_meta_pending',`SELECT count(*) FROM sys_audit_logs WHERE event_id IN (${pending.map(id=>"'"+id+"'::uuid").join(',')});`)),pending.length);
 record('real pending audit survives bundle restore and replays once across restart',{pendingEvents:pending.length});
 // A failed backup must leave previous bundles intact and resume its backend.
 const failure=run('deliberately fail an isolated backup',['env','DBHUB_LOCAL_BACKUP_DIR='+pendingBase,'DBHUB_LOCAL_BACKEND_CONTAINER='+names.recovered,'DBHUB_LOCAL_METADATA_CONTAINER='+names.metadata,'DBHUB_LOCAL_BUSINESS_CONTAINER=dbhub-drill-missing-business','bash','/mnt/c/dev/projects/DBHub/scripts/ops/backup-local-bundle.sh','--local'],undefined,true);assert.notEqual(failure.status,0);assert.equal(docker('verify backend resumed after failed backup','inspect','-f','{{.State.Paused}}',names.recovered).toString().trim(),'false');run('verify previous bundle after failure',['sh','-c','cd "$1"; sha256sum -c SHA256SUMS >/dev/null','sh',pendingBundle]);record('failed backup preserves previous archive and resumes the isolated backend');
 requireMatchingDigest(proof.bundleSha256,readBundleDigest(args=>run('verify source bundle after restore',args).stdout,backupRoot,bundle));
 for(const name of createdContainers.reverse())docker('remove completed drill container','rm','-f',name);
 for(const volume of createdVolumes)docker('remove completed drill volume','volume','rm',volume);
 docker('remove completed drill network','network','rm',network);proof.resources.cleaned=true;
 proof.status='passed';proof.finishedAt=new Date().toISOString();
 console.log(JSON.stringify({status:proof.status,checks:proof.checks.length,bundle:proof.bundle}));
}catch(error){proof.status='failed';proof.message=error.message;console.error(error.message);process.exitCode=1;}finally{fs.writeFileSync(folder+'/restore-drill-'+suffix+'.json',JSON.stringify(proof,null,2));fs.writeFileSync(folder+'/restore-drill-verification.json',JSON.stringify(proof,null,2));}
