import json,unittest,threading,http.client,hmac,hashlib
from unittest.mock import patch
import router as r
class RoutingTests(unittest.TestCase):
 def setUp(self):
  self.calls=[]
  def upstream(remote,op,body):
   self.calls.append((remote,op,body));return 200,{'data':{'session_id':'abc-123','protocol':1} if op=='open' else {'ok':True}}
  self.router=r.Router(upstream)
 def test_precise_open_and_session_affinity(self):
  body={'url':r.MATCH,'username':'demo_reader','database':'dbhub_demo','ssl_mode':'disable','owner':'owner','password':'synthetic-test-only'}
  status,p=self.router.call('open',body)
  self.assertEqual(status,200);self.assertEqual(p['data']['session_id'],'wsl-v1:abc-123')
  self.assertEqual(body['url'],r.MATCH);self.assertEqual(self.calls[-1][2]['url'],r.MAPPED)
  for op in ('execute','metadata','probe','begin','commit','rollback','close','cancel'):
   self.router.call(op,{'session_id':p['data']['session_id'],'owner':'owner'})
   self.assertEqual(self.calls[-1],(True,op,{'session_id':'abc-123','owner':'owner'}))
 def test_default_unchanged(self):
  for op,body in [('artifacts',{}),('validate-template',{'url':r.MATCH}),('open',{'url':'jdbc:h2:mem:demo'}),('execute',{'session_id':'ordinary','sql':'SELECT 1'})]:
   self.router.call(op,body);self.assertEqual(self.calls[-1],(False,op,body))
 def test_target_mismatch_fails_without_forwarding(self):
  for field,value in [('username','other'),('database','other'),('ssl_mode','verify-full')]:
   body={'url':r.MATCH,'username':'demo_reader','database':'dbhub_demo','ssl_mode':'disable'};body[field]=value
   self.assertEqual(self.router.call('open',body)[0],400)
  self.assertFalse(self.calls)
 def test_invalid_session_not_sent_default(self):
  for sid in ('wsl-v1:','wsl-v1:a/b','http://example','wsl-v1:wsl-v1:abc'):
   self.assertEqual(self.router.call('execute',{'session_id':sid})[0],400)
  self.assertFalse(self.calls)
 def test_transport_failure_never_retried_or_leaked(self):
  calls=[]
  def failed(*args): calls.append(args);raise RuntimeError('synthetic-sensitive-value')
  status,p=r.Router(failed).call('execute',{'session_id':'wsl-v1:abc'})
  self.assertEqual(len(calls),1);self.assertEqual(status,502);self.assertNotIn('synthetic-sensitive-value',json.dumps(p));self.assertFalse(p['error']['definite'])
 def test_sqlstate_preserved_message_redacted(self):
  status,p=r.Router(lambda *args:(400,{'error':{'kind':'sql','message':'synthetic-sensitive-value','sql_state':'42501','definite':True}})).call('probe',{'session_id':'wsl-v1:abc'})
  self.assertEqual(p['error']['sql_state'],'42501');self.assertNotIn('synthetic-sensitive-value',json.dumps(p))
 def test_known_error_kinds_and_outcome_preserved(self):
  cases=[('statement_rejected','42501',True),('authentication_failed','28P01',False),('deadline','57014',False),('artifact_changed','',False),('duplicate','',False),('canceled','57014',False)]
  for remote in (False,True):
   for kind,state,definite in cases:
    with self.subTest(remote=remote,kind=kind):
     calls=[]
     def upstream(*args):
      calls.append(args)
      return 400,{'error':{'kind':kind,'message':'synthetic-sensitive-value','sql_state':state,'definite':definite}}
     status,p=r.Router(upstream).call('execute',{'session_id':('wsl-v1:' if remote else '')+'abc'})
     self.assertEqual(status,400);self.assertEqual(len(calls),1)
     self.assertEqual(p['error']['kind'],kind);self.assertEqual(p['error']['sql_state'],state);self.assertEqual(p['error']['definite'],definite)
     self.assertNotIn('synthetic-sensitive-value',json.dumps(p))
 def test_unknown_kind_and_invalid_sqlstate_sanitized(self):
  status,p=r.Router(lambda *args:(400,{'error':{'kind':'synthetic-sensitive-value','message':'synthetic-sensitive-value','sql_state':'bad-state','definite':'true'}})).call('probe',{'session_id':'wsl-v1:abc'})
  self.assertEqual(p['error']['kind'],'transport');self.assertEqual(p['error']['sql_state'],'');self.assertFalse(p['error']['definite'])
  self.assertNotIn('synthetic-sensitive-value',json.dumps(p))
 def test_json_limits(self):
  for raw in ('[]','{"x":1,"x":2}','{"x":NaN}'):
   with self.assertRaises(ValueError):r.decode(raw)
 def test_redirects_disabled(self): self.assertIsNone(r.NoRedirect().redirect_request(None,None,None,None,None,None))
 def test_hmac_contract(self):
  self.assertEqual(hmac.new(b'key',b'DBHub WSL JDBC HTTPS v1',hashlib.sha256).hexdigest(),hashlib.sha256(bytes(x^0x5c for x in b'key'.ljust(64,b'\0'))+hashlib.sha256(bytes(x^0x36 for x in b'key'.ljust(64,b'\0'))+b'DBHub WSL JDBC HTTPS v1').digest()).hexdigest() )
class ProtocolTests(unittest.TestCase):
 @classmethod
 def setUpClass(cls):
  cls.calls=[]
  cls.s=r.Server(('127.0.0.1',0),r.Handler);cls.s.token='test-only-token'
  def upstream(*args):cls.calls.append(args);return 200,{'data':{'ok':True}}
  cls.s.router=r.Router(upstream);cls.t=threading.Thread(target=cls.s.serve_forever,daemon=True);cls.t.start()
 @classmethod
 def tearDownClass(cls):cls.s.shutdown();cls.s.server_close();cls.t.join()
 def request(self,method,path,body=b'{}',auth=True,headers=None):
  h={'Content-Type':'application/json'}
  if auth:h['Authorization']='Bearer '+self.s.token
  if headers:h.update(headers)
  c=http.client.HTTPConnection('127.0.0.1',self.s.server_port,timeout=2);c.request(method,path,body,h);res=c.getresponse();data=res.read();c.close();return res.status,json.loads(data)
 def test_auth_and_allowed_protocol(self):
  self.assertEqual(self.request('POST','/v1/artifacts',auth=False)[0],401)
  self.assertEqual(self.request('CONNECT','example:443')[0],405)
  self.assertEqual(self.request('GET','/v1/artifacts')[0],405)
  self.assertEqual(self.request('POST','/v1/artifacts?x=1')[0],404)
  self.assertEqual(self.request('POST','/v1/artifacts')[0],200)
 def test_oversize_and_bad_json(self):
  self.assertEqual(self.request('POST','/v1/open',headers={'Content-Length':str(r.LIMIT+1)})[0],413)
  self.assertEqual(self.request('POST','/v1/open',b'[]')[0],400)
if __name__=='__main__':unittest.main()
