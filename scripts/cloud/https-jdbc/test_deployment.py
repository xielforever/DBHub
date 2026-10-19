import copy,json,os,subprocess,tempfile,unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch
import config,state,infrastructure,register

class StateTests(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.base=Path(self.tmp.name);self.root=self.base/'private';self.root.mkdir(mode=0o700);self.repo=self.base/'repo';self.repo.mkdir()
  self.raw=b'DBHUB_JDBC_URL=http://jdbc:8090\nDBHUB_JDBC_TOKEN='+b'x'*64+b'\n'
  state.exclusive(self.root/'runtime.env',self.raw)
 def tearDown(self):self.tmp.cleanup()
 def test_exact_unicode_credential_and_idempotent_files(self):
  password='synthetic-原文-$-\"-\n'
  state.prepare(self.root,self.repo,password)
  before={p.name:p.read_bytes() for p in self.root.iterdir()}
  state.prepare(self.root,self.repo,password)
  self.assertEqual(before,{p.name:p.read_bytes() for p in self.root.iterdir()})
  self.assertEqual(json.loads(state.private_file(self.root/'https-jdbc.credentials.json'))['password'],password)
  self.assertEqual((self.root/'runtime.env').read_bytes(),self.raw)
  self.assertEqual((self.root/'https-jdbc.credentials.json').stat().st_mode&0o777,0o600)
 def test_changed_password_rejected_without_overwrite(self):
  state.prepare(self.root,self.repo,'synthetic-one');before=(self.root/'https-jdbc.credentials.json').read_bytes()
  with self.assertRaises(config.DeploymentError):state.prepare(self.root,self.repo,'synthetic-two')
  self.assertEqual((self.root/'https-jdbc.credentials.json').read_bytes(),before)
 def test_runtime_drift_and_unsafe_state_rejected(self):
  state.prepare(self.root,self.repo,'synthetic')
  (self.root/'runtime.env').write_bytes(self.raw+b'EXTRA=changed\n')
  with self.assertRaises(config.DeploymentError):state.prepare(self.root,self.repo,'synthetic')
  with self.assertRaises(config.DeploymentError):state.prepare(self.repo,self.repo,'synthetic')
 def test_symlink_rejected_before_resolve_and_before_creation(self):
  alias=self.base/'alias';alias.symlink_to(self.root,target_is_directory=True)
  with self.assertRaises(config.DeploymentError):state.prepare(alias,self.repo,'synthetic')
  self.assertFalse((self.root/'runtime.env.before-jdbc-router').exists())
  parent=self.base/'parent-alias';parent.symlink_to(self.base,target_is_directory=True)
  with self.assertRaises(config.DeploymentError):state.state_root(parent/'missing',self.repo,must_exist=False)
 def test_allowlist_preserves_empty_and_existing_targets(self):
  with patch.dict(os.environ,{},clear=True):
   self.assertEqual(state.original_allowlist({'DBHUB_TARGET_ALLOWLIST':'postgres:5432,mysql:3306'}),'postgres:5432,mysql:3306')
   self.assertEqual(state.target_allowlist({}),'')
   self.assertEqual(state.target_allowlist({'DBHUB_TARGET_ALLOWLIST':'postgres:5432,mysql:3306'}),'postgres:5432,mysql:3306,wsl-demo-postgres:5432')
   self.assertEqual(state.target_allowlist({'DBHUB_TARGET_ALLOWLIST':'postgres:5432,wsl-demo-postgres:5432'}),'postgres:5432,wsl-demo-postgres:5432')

class FixtureAPI:
 def __init__(self):self.drivers=[];self.templates=[];self.sources=[];self.calls=[];self.bad_password=False
 def __call__(self,method,path,body=None,want=200):
  self.calls.append((method,path,copy.deepcopy(body)))
  if method=='GET':
   if path.endswith('/available'):return {'items':[{'id':register.ARTIFACT,'sha256':register.SHA256,'ready':True,'class_name':'org.postgresql.Driver','version':'42.7.7'}]}
   return {'items':copy.deepcopy({'/connector-drivers':self.drivers,'/datasource-templates':self.templates,'/connections':self.sources}[path])}
  if path=='/connector-drivers':
   item={**body,'id':11,'created_by':1,'enabled':True,'sha256':register.SHA256,'version':'42.7.7','class_name':'org.postgresql.Driver'};self.drivers.append(item);return copy.deepcopy(item)
  if path=='/datasource-templates':
   item={**body,'id':12,'created_by':1,'enabled':True,'revision':1};self.templates.append(item);return copy.deepcopy(item)
  if path=='/connections':
   item={k:v for k,v in body.items() if k!='password'};item.update(id=13,user_id=1,host='wsl-demo-postgres',port=5432,database='dbhub_demo',ssh_tunnel_id=None,proxy_id=None,definition={'visibility':'private','owner_id':1,'enabled':True});self.sources.append(item);return copy.deepcopy(item)
  if path.endswith('/test'):
   if self.bad_password:raise config.DeploymentError('synthetic stored authentication drift')
   return {'ok':True}
  if path=='/query/execute':return {'rows':[['1']]}
  raise AssertionError('unexpected operation')

class RegistrationTests(unittest.TestCase):
 def test_repeat_registration_creates_nothing(self):
  api=FixtureAPI();receipt,created=register.reconcile(api,1,'synthetic')
  self.assertEqual(created,['driver','template','connection'])
  api.calls=[];again,created=register.reconcile(api,1,'synthetic',receipt)
  self.assertEqual(receipt,again);self.assertEqual(created,[])
  self.assertFalse(any(m=='POST' and p in ('/connector-drivers','/datasource-templates','/connections') for m,p,b in api.calls))
 def test_name_collision_fails_before_any_create(self):
  api=FixtureAPI();api.sources=[{'id':99,'name':register.SOURCE_NAME,'parameters':{'host':'other'}}]
  with self.assertRaises(config.DeploymentError):register.reconcile(api,1,'synthetic')
  self.assertTrue(all(m=='GET' for m,p,b in api.calls))
 def test_same_name_different_scope_or_owner_is_not_adopted(self):
  for key,value in [('visibility','global'),('created_by',2),('sha256','changed')]:
   with self.subTest(key=key):
    api=FixtureAPI();receipt,_=register.reconcile(api,1,'synthetic');api.drivers[0][key]=value;api.calls=[]
    with self.assertRaises(config.DeploymentError):register.reconcile(api,1,'synthetic',receipt)
    self.assertTrue(all(m=='GET' for m,p,b in api.calls))
 def test_resource_or_stored_credential_drift_not_repaired(self):
  api=FixtureAPI();receipt,_=register.reconcile(api,1,'synthetic');api.bad_password=True;api.calls=[]
  with self.assertRaises(config.DeploymentError):register.reconcile(api,1,'synthetic',receipt)
  self.assertFalse(any(m in ('PUT','PATCH','DELETE') or p=='/connections' and m=='POST' for m,p,b in api.calls))
  api.bad_password=False;api.sources=[];api.calls=[]
  with self.assertRaises(config.DeploymentError):register.reconcile(api,1,'synthetic',receipt)
  self.assertTrue(all(m=='GET' for m,p,b in api.calls))

class InfrastructureTests(unittest.TestCase):
 def test_existing_dependencies_never_recreated(self):
  calls=[]
  def run(args,**kwargs):
   calls.append(args)
   return SimpleNamespace(stdout=json.dumps({'Running':True,'Health':{'Status':'healthy'}}) if args[1]=='inspect' else 'existing-id\n')
  infrastructure.ensure(run)
  self.assertTrue(all(c[1] in ('ps','inspect') for c in calls))

class EntrypointTests(unittest.TestCase):
 def fixture(self,entry):
  tmp=tempfile.TemporaryDirectory();self.addCleanup(tmp.cleanup);root=Path(tmp.name);cloud=root/'scripts/cloud';feature=cloud/'https-jdbc';feature.mkdir(parents=True)
  source=Path(__file__).resolve().parent
  (cloud/entry).write_bytes((source.parent/entry).read_bytes())
  (feature/'config.py').write_bytes((source/'config.py').read_bytes())
  trace=root/'trace'
  for p in [cloud/'dev.sh',cloud/'backup.sh',feature/entry]:p.write_text('#!/bin/sh\nprintf "%s %s\\n" "'+p.name+'" "$*" >> "$TRACE"\n');p.chmod(0o755)
  bindir=root/'bin';bindir.mkdir();python=bindir/'python3'
  python.write_text('#!/bin/sh\ncase "$1" in *config.py) exec /usr/local/bin/python "$@";; *) exit 0;; esac\n');python.chmod(0o755)
  env={'PATH':str(bindir)+':/usr/bin:/bin','TRACE':str(trace),'NO_PROXY':'localhost'}
  return root,cloud/entry,trace,env
 def test_disabled_start_retains_original_path(self):
  root,script,trace,env=self.fixture('start.sh');subprocess.run(['sh',str(script)],env=env,check=True)
  self.assertEqual(trace.read_text().splitlines(),['dev.sh up -d --wait --wait-timeout 240 jdbc','dev.sh up -d backend frontend','backup.sh '])
 def test_missing_secret_fails_before_setup_or_start_mutations(self):
  for entry in ('setup.sh','start.sh'):
   root,script,trace,env=self.fixture(entry);env.update(DBHUB_HTTPS_JDBC_ENABLED='1',DBHUB_HTTPS_JDBC_ENDPOINT=config.ENDPOINT,HTTPS_PROXY='http://synthetic-proxy:8080')
   r=subprocess.run(['sh',str(script)],env=env,capture_output=True,text=True)
   self.assertNotEqual(r.returncode,0);self.assertFalse(trace.exists());self.assertIn('ready direct PG credential',r.stderr)
 def test_enabled_start_dispatches_automatically(self):
  root,script,trace,env=self.fixture('start.sh');env.update(DBHUB_HTTPS_JDBC_ENABLED='1',DBHUB_HTTPS_JDBC_ENDPOINT=config.ENDPOINT,HTTPS_PROXY='http://synthetic-proxy:8080',DBHUB_DEMO_PG_PASSWORD='synthetic-only')
  subprocess.run(['sh',str(script)],env=env,check=True);self.assertEqual(trace.read_text().splitlines(),['start.sh '])

if __name__=='__main__':unittest.main()
