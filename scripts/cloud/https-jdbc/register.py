"""Reconcile only this administrator's canonical personal demo resources via API.

No PUT/PATCH/DELETE, credential update, mutation retry or remote response logging.
"""
import fcntl,json,os,sys,urllib.request,urllib.error,uuid
from pathlib import Path
from config import ENDPOINT,DeploymentError
from state import env_values,exclusive,private_file

ARTIFACT='postgresql-42.7.7'
SHA256='157963d60ae66d607e09466e8c0cdf8087e9cb20d0159899ffca96bca2528460'
DRIVER_NAME='PostgreSQL HTTPS demo 42.7.7'
TEMPLATE_NAME='WSL PostgreSQL HTTPS demo'
TEMPLATE_KEY='wsl-https-postgres-v1'
SOURCE_NAME='WSL local PostgreSQL via HTTPS'
FIELDS=[{'name':'host','label':'主机','type':'string','required':True},{'name':'port','label':'端口','type':'integer','required':True},{'name':'database','label':'数据库','type':'string','required':True}]
PARAMETERS={'host':'wsl-demo-postgres','port':'5432','database':'dbhub_demo'}

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args):return None

class API:
    def __init__(self):
        self.token=''
        # Backend is the fixed internal application, never the remote HTTPS target.
        self.op=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect())
    def __call__(self,method,path,body=None,want=200):
        if not path.startswith('/') or method not in ('GET','POST'):raise DeploymentError('unsupported registration operation')
        headers={'Content-Type':'application/json'}
        if self.token:headers['Authorization']='Bearer '+self.token
        request=urllib.request.Request('http://backend:8080/api/v1'+path,data=json.dumps(body).encode() if body is not None else None,headers=headers,method=method)
        try:
            with self.op.open(request,timeout=30) as response:
                raw=response.read(2*1024*1024+1)
                if response.status!=want or len(raw)>2*1024*1024:raise DeploymentError('API response rejected')
                packet=json.loads(raw)
                if packet.get('code')!=0:raise DeploymentError('API operation rejected')
                return packet['data']
        except urllib.error.HTTPError as e:
            code=e.code;e.close()
            raise DeploymentError('API request failed with HTTP '+str(code)+'; no automatic retry') from None
        except DeploymentError:raise
        except Exception:raise DeploymentError('API request outcome uncertain; stop and reconcile on next startup') from None

def one(items,predicate):
    found=[x for x in items if predicate(x)]
    if len(found)>1:raise DeploymentError('ambiguous canonical resource; no replacement')
    return found[0] if found else None

def identity(item):
    if type(item.get('id')) is not int or item['id']<1:
        raise DeploymentError('invalid canonical resource identity')
    return item

def require(actual,expected):
    if any(actual.get(k)!=v for k,v in expected.items()):raise DeploymentError('canonical resource conflict; no replacement')

def reconcile(api,owner,password,previous=None):
    if type(owner) is not int or owner<1:raise DeploymentError('invalid bootstrap identity')
    installed=api('GET','/connector-drivers/available')['items']
    artifact=one(installed,lambda d:d.get('id')==ARTIFACT)
    if not artifact:raise DeploymentError('required JDBC artifact unavailable')
    require(artifact,{'sha256':SHA256,'ready':True,'class_name':'org.postgresql.Driver','version':'42.7.7'})
    drivers=api('GET','/connector-drivers')['items']
    templates=api('GET','/datasource-templates')['items']
    sources=api('GET','/connections')['items']
    driver=one(drivers,lambda d:d.get('name')==DRIVER_NAME or (d.get('artifact_id')==ARTIFACT and d.get('created_by')==owner and d.get('visibility')=='private'))
    template=one(templates,lambda t:t.get('name')==TEMPLATE_NAME or t.get('template_key')==TEMPLATE_KEY)
    source=one(sources,lambda c:c.get('name')==SOURCE_NAME or c.get('parameters')==PARAMETERS)
    driver_spec={'artifact_id':ARTIFACT,'name':DRIVER_NAME,'sha256':SHA256,'version':'42.7.7','class_name':'org.postgresql.Driver','enabled':True,'created_by':owner,'visibility':'private'}
    template_spec={'template_key':TEMPLATE_KEY,'name':TEMPLATE_NAME,'product':'PostgreSQL','dialect':'postgres','url_template':'jdbc:postgresql://{host}:{port}/{database}','fields':FIELDS,'enabled':True,'created_by':owner,'visibility':'private','revision':1}
    source_spec={'name':SOURCE_NAME,'type':'jdbc','host':'wsl-demo-postgres','port':5432,'database':'dbhub_demo','username':'demo_reader','parameters':PARAMETERS,'ssl_mode':'disable','environment':'test','connection_timeout':10,'user_id':owner,'ssh_tunnel_id':None,'proxy_id':None}
    # Inspect every possible collision before creating anything.
    if driver:identity(driver);require(driver,driver_spec)
    if template:
        identity(template)
        if not driver:raise DeploymentError('template without canonical driver')
        require(template,{**template_spec,'driver_id':driver['id']})
    if source:
        identity(source)
        if not template:raise DeploymentError('source without canonical template')
        require(source,{**source_spec,'template_revision_id':template['id']})
        require(source.get('definition') or {},{'visibility':'private','owner_id':owner,'enabled':True})
    if previous:
        require(previous,{'endpoint':ENDPOINT,'owner_id':owner})
        for key,value in [('driver_id',driver),('template_revision_id',template),('connection_id',source)]:
            if not value or previous.get(key)!=value['id']:raise DeploymentError('registered resource drift; no recreation')
    created=[]
    if not driver:
        driver=api('POST','/connector-drivers',{'artifact_id':ARTIFACT,'name':DRIVER_NAME,'visibility':'private'},201);identity(driver);require(driver,driver_spec);created.append('driver')
    if not template:
        request={k:template_spec[k] for k in ('template_key','name','product','dialect','url_template','fields','visibility')};request['driver_id']=driver['id']
        template=api('POST','/datasource-templates',request,201);identity(template);require(template,{**template_spec,'driver_id':driver['id']});created.append('template')
    if not source:
        request={k:source_spec[k] for k in ('name','type','username','parameters','ssl_mode','environment','connection_timeout')}
        request.update(template_revision_id=template['id'],password=password)
        api('POST','/connections/test',request)
        source=api('POST','/connections',request,201);identity(source);require(source,{**source_spec,'template_revision_id':template['id']});require(source.get('definition') or {},{'visibility':'private','owner_id':owner,'enabled':True});created.append('connection')
    # Test stored credentials, never replace them with the supplied password.
    api('POST','/connections/'+str(source['id'])+'/test')
    result=api('POST','/query/execute',{'request_id':str(uuid.uuid4()),'connection_id':source['id'],'sql':'SELECT 1 AS value','max_rows':1,'timeout_ms':10000})
    if result.get('rows')!=[['1']]:raise DeploymentError('saved personal connection did not pass verification')
    receipt={'endpoint':ENDPOINT,'owner_id':owner,'driver_id':driver['id'],'template_revision_id':template['id'],'connection_id':source['id']}
    return receipt,created

def main():
    root=Path('/runtime')
    values=env_values(private_file(root/'runtime.env'))
    credentials=json.loads(private_file(root/'https-jdbc.credentials.json'))
    lock=os.open(root/'https-jdbc-registration.lock',os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600)
    with os.fdopen(lock,'r+b') as guard:
        fcntl.flock(guard,fcntl.LOCK_EX)
        api=API();auth=api('POST','/auth/login',{'username':values['APP_ADMIN_USERNAME'],'password':values['APP_ADMIN_PASSWORD']})
        api.token=auth['access_token'];owner=auth['user']['id']
        if auth['user'].get('role')!='admin':raise DeploymentError('bootstrap account must be administrator')
        path=root/'https-jdbc-registration.json'
        previous=json.loads(private_file(path)) if path.exists() else None
        receipt,created=reconcile(api,owner,credentials['password'],previous)
        if previous is None:exclusive(path,json.dumps(receipt,sort_keys=True).encode())
        elif previous!=receipt:raise DeploymentError('registration receipt drift')
        print(json.dumps({'https_jdbc':receipt,'created':created}))

if __name__=='__main__':
    try:main()
    except DeploymentError as e:raise SystemExit(str(e)) from None
    except Exception:raise SystemExit('HTTPS JDBC registration failed; no secret details logged') from None
