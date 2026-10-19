"""Fixed JDBC protocol router. Never logs request bodies, credentials or upstream errors."""
import hashlib,hmac,json,os,re,ssl,threading
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from urllib.request import Request,build_opener,ProxyHandler,HTTPSHandler,HTTPRedirectHandler,proxy_bypass
from urllib.error import HTTPError

REMOTE='https://dbhub-demo-wsl.tail1184b4.ts.net'
MATCH='jdbc:postgresql://wsl-demo-postgres:5432/dbhub_demo'
MAPPED='jdbc:postgresql://postgres:5432/dbhub_demo'
PREFIX='wsl-v1:'
LIMIT=2*1024*1024
SESSION=re.compile(r'^[A-Za-z0-9_-]{1,256}$')
OPS={'artifacts','validate-template','open','probe','execute','metadata','begin','commit','rollback','close','cancel'}

def error(kind='transport',state='',definite=False):
    return {'error':{'kind':kind,'message':'JDBC request failed; operation outcome may require verification','sql_state':state,'definite':definite}}

def decode(raw):
    def pairs(items):
        out={}
        for k,v in items:
            if k in out: raise ValueError()
            out[k]=v
        return out
    obj=json.loads(raw,object_pairs_hook=pairs,parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
    if not isinstance(obj,dict): raise ValueError()
    return obj

class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self,*args,**kwargs): return None

class Transport:
    def __init__(self,token,remote_token,default='http://jdbc:8090'):
        # Default is fixed to the pre-existing internal service; no client-supplied endpoints.
        if default!='http://jdbc:8090': raise ValueError('unsupported default endpoint')
        self.token,self.remote_token,self.default=token,remote_token,default
        proxy=os.environ.get('HTTPS_PROXY') or os.environ.get('https_proxy')
        if not proxy or proxy_bypass('dbhub-demo-wsl.tail1184b4.ts.net'): raise ValueError('official HTTPS proxy required')
        self.remote=build_opener(ProxyHandler({'https':proxy}),HTTPSHandler(context=ssl.create_default_context()),NoRedirect())
        self.local=build_opener(ProxyHandler({}),NoRedirect())
    def __call__(self,remote,op,body):
        raw=json.dumps(body,allow_nan=False).encode()
        req=Request((REMOTE if remote else self.default)+'/v1/'+op,data=raw,headers={'Authorization':'Bearer '+(self.remote_token if remote else self.token),'Content-Type':'application/json'},method='POST')
        try:
            response=(self.remote if remote else self.local).open(req,timeout=125)
        except HTTPError as e:
            response=e
        with response:
            raw=response.read(16*1024*1024+1)
            if len(raw)>16*1024*1024: raise ValueError()
            packet=decode(raw)
            if response.code!=200 and 'error' not in packet: raise ValueError()
            return response.code,packet

class Router:
    def __init__(self,transport): self.transport=transport
    def call(self,op,body):
        if op not in OPS: return 404,error('invalid_config',definite=True)
        body=dict(body)
        remote=False
        if op=='open':
            if body.get('url')==MATCH:
                if body.get('username')!='demo_reader' or body.get('database')!='dbhub_demo' or body.get('ssl_mode')!='disable':
                    return 400,error('invalid_config',definite=True)
                remote=True
                body['url']=MAPPED
        elif op not in {'artifacts','validate-template'}:
            sid=body.get('session_id')
            if not isinstance(sid,str): return 400,error('invalid_config',definite=True)
            remote=sid.startswith(PREFIX)
            original=sid[len(PREFIX):] if remote else sid
            if not SESSION.fullmatch(original): return 400,error('invalid_config',definite=True)
            body['session_id']=original
        try:
            status,packet=self.transport(remote,op,body)
            if 'error' in packet:
                e=packet['error']
                state=e.get('sql_state','') if isinstance(e,dict) else ''
                if not isinstance(state,str) or not re.fullmatch('[A-Z0-9]{5}',state): state=''
                kind=e.get('kind') if isinstance(e,dict) else None
                if kind not in {'authentication','permission','invalid_config','session_expired','resource_exhausted','sql','timeout','transport','unsupported','unavailable','statement_rejected','deadline','artifact_changed','duplicate','canceled','authentication_failed'}: kind='transport'
                return status,error(kind,state,isinstance(e,dict) and e.get('definite') is True)
            if status!=200 or 'data' not in packet: raise ValueError()
            if op=='open':
                data=packet['data']
                sid=data.get('session_id')
                if data.get('protocol')!=1 or not isinstance(sid,str) or not SESSION.fullmatch(sid): raise ValueError()
                if remote: data['session_id']=PREFIX+sid
            return status,packet
        except Exception:
            return 502,error()

class Handler(BaseHTTPRequestHandler):
    protocol_version='HTTP/1.0'
    def setup(self):
        super().setup(); self.connection.settimeout(10)
    def log_message(self,*args): pass
    def send_packet(self,status,packet):
        raw=json.dumps(packet,allow_nan=False).encode()
        self.send_response(status); self.send_header('Content-Type','application/json');self.send_header('Cache-Control','no-store');self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
    def do_POST(self):
        try:
            auth=self.headers.get_all('Authorization',[])
            if len(auth)!=1 or not hmac.compare_digest(auth[0].encode(),('Bearer '+self.server.token).encode()):
                return self.send_packet(401,error('authentication',definite=True))
            if self.path not in {'/v1/'+x for x in OPS}: return self.send_packet(404,error('invalid_config',definite=True))
            lengths=self.headers.get_all('Content-Length',[])
            if self.headers.get('Transfer-Encoding') or len(lengths)!=1 or not lengths[0].isdigit():
                return self.send_packet(400,error('invalid_config',definite=True))
            n=int(lengths[0])
            if n>LIMIT: return self.send_packet(413,error('invalid_config',definite=True))
            if self.headers.get_content_type()!='application/json': return self.send_packet(415,error('invalid_config',definite=True))
            raw=self.rfile.read(n)
            if len(raw)!=n: raise ValueError()
            body=decode(raw)
            status,packet=self.server.router.call(self.path[4:],body)
            self.send_packet(status,packet)
        except Exception:
            try: self.send_packet(400,error('invalid_config',definite=True))
            except Exception: pass
    def do_GET(self): self.send_packet(405,error('invalid_config',definite=True))
    do_CONNECT=do_GET
    do_PUT=do_GET
    do_DELETE=do_GET
    do_PATCH=do_GET
    do_OPTIONS=do_GET
    def send_error(self,code,message=None,explain=None): self.send_packet(code,error('invalid_config',definite=True))

class Server(ThreadingHTTPServer):
    daemon_threads=True
    def __init__(self,*args,**kwargs):
        super().__init__(*args,**kwargs); self.slots=threading.BoundedSemaphore(16)
    def process_request(self,request,address):
        if not self.slots.acquire(False): self.shutdown_request(request);return
        try: super().process_request(request,address)
        except Exception: self.slots.release();raise
    def process_request_thread(self,request,address):
        try: super().process_request_thread(request,address)
        finally: self.slots.release()
    def handle_error(self,*args): pass

def main():
    from pathlib import Path
    credentials=json.loads(Path(os.environ['DBHUB_ROUTER_CREDENTIALS_FILE']).read_text())
    token=credentials.pop('service_token')
    password=credentials.pop('password')
    if credentials: raise ValueError('unexpected credential fields')
    del credentials
    if len(token)<32 or not password: raise ValueError('credential readiness required')
    remote_token=hmac.new(password.encode('utf-8'),b'DBHub WSL JDBC HTTPS v1',hashlib.sha256).hexdigest()
    del password
    transport=Transport(token,remote_token)
    server=Server(('0.0.0.0',8091),Handler);server.token=token;server.router=Router(transport)
    server.serve_forever()
if __name__=='__main__': main()
