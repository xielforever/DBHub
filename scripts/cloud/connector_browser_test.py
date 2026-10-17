"""Real connector UI acceptance. Credentials remain in the container environment."""
import json
import math
import time
import os
import re
import secrets
import urllib.error
import urllib.request
import uuid
from pathlib import Path
from urllib.parse import quote
from playwright.sync_api import sync_playwright, expect

base,frontend='http://backend:8080/api/v1','http://frontend:5173'
values=os.environ
token=''
prefix='connector-ui-'+secrets.token_hex(4)
checks,errors=[],[]
sources,templates,drivers,users,tokens=[],[],[],[],[]
sql_cleanup=[]
out=Path('/runtime/browser-artifacts');out.mkdir(exist_ok=True)
remote_session=None

def api(method,path,body=None,want=200,bearer=None):
    if method=='POST' and path=='/query/execute' and body is not None:body={'request_id':str(uuid.uuid4()),**body}
    headers={'Content-Type':'application/json'}
    auth=token if bearer is None else bearer
    if auth:headers['Authorization']='Bearer '+auth
    request=urllib.request.Request(base+path,data=json.dumps(body).encode() if body is not None else None,headers=headers,method=method)
    try:response=urllib.request.urlopen(request,timeout=40)
    except urllib.error.HTTPError as e:response=e
    with response:
        assert response.status==want,f'{method} {path}: HTTP {response.status}'
        return json.load(response).get('data')

def rpc(method,body):
    req=urllib.request.Request(values['DBHUB_JDBC_URL']+'/v1/'+method,data=json.dumps(body).encode(),headers={'Authorization':'Bearer '+values['DBHUB_JDBC_TOKEN'],'Content-Type':'application/json'})
    with urllib.request.urlopen(req,timeout=20) as response:return json.load(response)['data']

def passed(name):checks.append(name);print('PASS '+name,flush=True)
def choose(page,combo,name):
    combo.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")][1]').click()
    page.get_by_role('option',name=name,exact=False).last.click()
def login(page,username,password):
    page.goto(frontend+'/login',wait_until='networkidle');page.locator('#username').fill(username);page.locator('#password').fill(password)
    with page.expect_response(lambda r:r.url.endswith('/auth/login')) as pending:page.locator('button[type=submit]').click()
    tokens.append(pending.value.json()['data']['access_token']);page.wait_for_url('**/dashboard')

token=api('POST','/auth/login',{'username':values['APP_ADMIN_USERNAME'],'password':values['APP_ADMIN_PASSWORD']})['access_token']
try:
    artifacts=api('GET','/connector-drivers/available')['items']
    h2_artifact=next(a for a in artifacts if a['name']=='h2')
    known=api('GET','/connector-drivers')['items']
    with sync_playwright() as p:
        browser=p.chromium.launch(executable_path='/usr/bin/chromium',proxy={'server':values.get('HTTPS_PROXY') or 'http://proxy:8080','bypass':'frontend,backend,jdbc,localhost,127.0.0.1'},args=['--no-sandbox','--disable-dev-shm-usage'])
        context=browser.new_context(viewport={'width':1440,'height':1000},locale='zh-CN',reduced_motion='reduce');page=context.new_page();page.set_default_timeout(20000);page.on('pageerror',lambda e:errors.append(str(e)))
        login(page,values['APP_ADMIN_USERNAME'],values['APP_ADMIN_PASSWORD'])
        expect(page).to_have_title('概览 · DBHub');page.goto(frontend+'/settings',wait_until='networkidle')
        driver_section=page.get_by_role('region',name='驱动管理')
        registered=next((d for d in known if d['artifact_id']==h2_artifact['id']),None)
        if not registered:
            choose(page,driver_section.get_by_role('combobox',name='可用驱动制品'),'h2 '+h2_artifact['version'])
            driver_section.get_by_label('驱动名称',exact=True).fill(prefix+' H2 driver')
            with page.expect_response(lambda r:r.url.endswith('/connector-drivers') and r.request.method=='POST') as response:driver_section.get_by_role('button',name='登记驱动',exact=True).click()
            assert response.value.status==201;registered=response.value.json()['data'];drivers.append(registered['id'])
        driver_name=registered['name']
        passed('admin registers an actually installed and checksum-verified JDBC artifact')
        section=page.get_by_role('region',name='数据源模板')
        section.get_by_label('模板标识',exact=True).fill(prefix)
        section.get_by_label('模板名称',exact=True).fill(prefix+' template')
        choose(page,section.get_by_role('combobox',name='模板驱动版本'),driver_name+' '+registered['version'])
        expect(section.get_by_label('JDBC URL 模板',exact=True)).to_have_value('jdbc:h2:mem:{database};DB_CLOSE_DELAY=-1')
        with page.expect_response(lambda r:r.url.endswith('/datasource-templates') and r.request.method=='POST') as response:section.get_by_role('button',name='发布模板',exact=True).click()
        assert response.value.status==201;template=response.value.json()['data'];templates.append(template['id'])
        passed('admin publishes a reusable metadata-only JDBC template')

        page.goto(frontend+'/connections',wait_until='networkidle');expect(page).to_have_title('数据源 · DBHub')
        page.get_by_role('button',name='新建数据源',exact=True).click();dialog=page.get_by_role('dialog',name='新建数据源')
        dialog.get_by_label('数据源名称',exact=True).fill(prefix+' H2 source')
        choose(page,dialog.get_by_label('数据源类型',exact=True),'自定义 JDBC')
        choose(page,dialog.get_by_role('combobox',name='数据源模板'),prefix+' template')
        database='browser_'+secrets.token_hex(6);password=secrets.token_urlsafe(18)
        dialog.get_by_label('数据库',exact=True).fill(database);dialog.get_by_label('用户名',exact=True).fill('sa');dialog.get_by_label('密码',exact=True).fill(password)
        with page.expect_response(lambda r:r.url.endswith('/connections/test')) as response:dialog.get_by_role('button',name='测试连接',exact=True).click()
        assert response.value.status==200;expect(dialog.get_by_test_id('connection-version-result')).to_contain_text('H2')
        expect(page.locator('.el-message')).to_have_count(0);page.screenshot(path=str(out/'connector-create-desktop.png'),animations='disabled')
        with page.expect_response(lambda r:r.url.endswith('/connections') and r.request.method=='POST') as response:dialog.get_by_role('button',name='保存',exact=True).click()
        assert response.value.status==201;h2_source=response.value.json()['data'];sources.append(h2_source['id'])
        passed('custom JDBC source is tested and saved through the real form')

        # Seed fixture SQL through the private service only; H2 public SQL stays disabled.
        owner=prefix+'-seed';opened=rpc('open',{'artifact_id':h2_artifact['id'],'sha256':h2_artifact['sha256'],'url':'jdbc:h2:mem:'+database+';DB_CLOSE_DELAY=-1','dialect':'','database':database,'owner':owner,'username':'sa','password':password,'ssl_mode':'disable','budget_ms':5000})
        remote_session={'session_id':opened['session_id'],'owner':owner}
        rpc('execute',{**remote_session,'execution_key':str(uuid.uuid4()),'sql':'CREATE TABLE PUBLIC.BROWSER_DEMO(ID INT PRIMARY KEY, NAME VARCHAR(100))','args':[],'budget_ms':5000})
        card=page.locator('article').filter(has=page.get_by_role('heading',name=h2_source['name'],exact=True));card.get_by_role('button',name='打开',exact=True).click()
        expect(page).to_have_title('数据工作台 · DBHub');expect(page.get_by_role('heading',name='结构浏览',exact=True)).to_be_visible();expect(page.get_by_role('cell',name='ID',exact=True)).to_be_visible()
        expect(page.get_by_role('button',name='运行',exact=True)).to_have_count(0);expect(page.get_by_role('button',name='格式化',exact=True)).to_have_count(0)
        passed('H2 opens the metadata view without an unsupported SQL editor')
        with page.expect_response(lambda r:r.url.endswith('/assets/sync')) as response:page.get_by_role('button',name='采集结构',exact=True).click()
        assert response.value.status==200 and response.value.json()['data']['tables']==1
        expect(page.get_by_role('status')).to_contain_text('已采集 1 张表')
        passed('metadata collection publishes the actual H2 table snapshot')

        # Schema-less drivers legitimately return an empty schema list; this is a UI contract fixture.
        def no_schemas(route):route.fulfill(status=200,json={'code':0,'message':'ok','data':{'items':[]}})
        page.route('**/api/v1/metadata/schemas?**',no_schemas)
        page.reload(wait_until='networkidle')
        choose(page,page.get_by_role('combobox',name='结构表'),'BROWSER_DEMO · 表')
        expect(page.get_by_role('cell',name='NAME',exact=True)).to_be_visible()
        page.unroute('**/api/v1/metadata/schemas?**',no_schemas)
        page.reload(wait_until='networkidle')
        passed('contract fixture: an empty Schema list still allows default-scope metadata browsing')

        page.set_viewport_size({'width':390,'height':844});page.get_by_role('heading',name='结构浏览',exact=True).scroll_into_view_if_needed()
        assert page.evaluate('document.documentElement.scrollWidth <= document.documentElement.clientWidth')
        expect(page.locator('.el-message')).to_have_count(0);page.screenshot(path=str(out/'connector-metadata-mobile.png'),animations='disabled',full_page=True)
        passed('metadata browsing remains readable on a 390px screen')
        page.set_viewport_size({'width':1440,'height':1000});page.goto(frontend+'/settings',wait_until='networkidle')
        section=page.get_by_role('region',name='数据源模板');row=section.get_by_role('listitem').filter(has_text=prefix+' template');row.get_by_role('button',name='新修订',exact=True).click();section.get_by_label('模板名称',exact=True).fill(prefix+' revision 2')
        with page.expect_response(lambda r:r.url.endswith('/datasource-templates/'+str(template['id'])) and r.request.method=='PUT') as response:section.get_by_role('button',name='发布新修订',exact=True).click()
        assert response.value.status==201;templates.append(response.value.json()['data']['id']);assert api('GET','/connections/'+str(h2_source['id']))['template']['revision']==1
        passed('publishing a revision preserves the source original template binding')
        page.set_viewport_size({'width':390,'height':844});section.get_by_role('heading',name='数据源模板',exact=True).scroll_into_view_if_needed();assert page.evaluate('document.documentElement.scrollWidth <= document.documentElement.clientWidth');expect(page.locator('.el-message')).to_have_count(0);page.screenshot(path=str(out/'connector-settings-mobile.png'),animations='disabled',full_page=True);page.set_viewport_size({'width':1440,'height':1000})
        passed('driver and template settings do not overflow the mobile page')

        pg_driver=next((d for d in api('GET','/connector-drivers')['items'] if d['artifact_id'].startswith('postgresql-')),None)
        if not pg_driver:
            artifact=next(a for a in artifacts if a['name']=='postgresql');pg_driver=api('POST','/connector-drivers',{'artifact_id':artifact['id'],'name':prefix+' pg driver'},201);drivers.append(pg_driver['id'])
        pg_template=api('POST','/datasource-templates',{'template_key':prefix+'-pg','name':prefix+' PG template','product':'PostgreSQL','driver_id':pg_driver['id'],'dialect':'postgres','url_template':'jdbc:postgresql://{host}:{port}/{database}','fields':[{'name':'host','label':'主机','type':'string','required':True},{'name':'port','label':'端口','type':'integer','required':True},{'name':'database','label':'数据库','type':'string','required':True}]},201);templates.append(pg_template['id'])
        pg=api('POST','/connections',{'name':prefix+' PG source','type':'jdbc','template_revision_id':pg_template['id'],'parameters':{'host':'postgres','port':'5432','database':values['POSTGRES_DB']},'username':values['POSTGRES_USER'],'password':values['POSTGRES_PASSWORD'],'ssl_mode':'disable','environment':'test'},201);sources.append(pg['id'])
        pg_table=prefix.replace('-','_')+'_pg'
        api('POST','/query/execute',{'connection_id':pg['id'],'sql':'CREATE TABLE public.'+pg_table+'(id INT PRIMARY KEY, label VARCHAR(80))'})
        sql_cleanup.append((pg['id'],'DROP TABLE IF EXISTS public.'+pg_table))
        for invalid in ['0','invalid','9007199254740992','2147483647']:
            page.goto(frontend+'/query?connection='+invalid,wait_until='networkidle')
            expect(page.get_by_role('alert')).to_contain_text('指定数据源不可用')
            expect(page.get_by_role('button',name='运行',exact=True)).to_have_count(0)
        choose(page,page.get_by_role('combobox',name='选择数据源'),pg['name'])
        expect(page.get_by_role('alert')).to_have_count(0)
        page.wait_for_selector('.monaco-editor .view-lines')
        passed('invalid and missing source IDs never silently open another source')

        page.goto(frontend+'/query?connection='+str(pg['id'])+'&sql='+quote("SELECT 'JDBC UI result' AS value"),wait_until='networkidle');page.wait_for_selector('.monaco-editor .view-lines');expect(page.get_by_role('button',name='运行',exact=True)).to_be_enabled()
        with page.expect_response(lambda r:r.url.endswith('/query/execute') and r.request.method=='POST') as response:page.get_by_role('button',name='运行',exact=True).click()
        assert response.value.status==200;expect(page.get_by_role('cell',name='JDBC UI result',exact=True)).to_be_visible()
        visible=[b.inner_text().strip() for b in page.get_by_test_id('sql-workbench').get_by_role('button').all() if b.is_visible()];assert visible==['运行','格式化'],visible
        expect(page.locator('.el-message')).to_have_count(0);page.screenshot(path=str(out/'connector-sql-desktop.png'),animations='disabled')
        passed('PostgreSQL JDBC runs SQL through the two-action data workspace')

        native=api('POST','/connections',{'name':prefix+' native comparison','type':'postgres','host':'postgres','port':5432,'database':values['POSTGRES_DB'],'username':values['POSTGRES_USER'],'password':values['POSTGRES_PASSWORD'],'ssl_mode':'disable','environment':'test'},201);sources.append(native['id'])
        timing={'scope':'sequential HTTP comparison with fresh target connection per execution','samples_per_case':20,'percentile_method':'nearest rank','results':[],'limitations':['Small development sample; no capacity or production SLA claim.','Includes API authorization, receipt persistence, driver/RPC execution and JSON decoding.']}
        for workload,sql,count in [('short_query','SELECT 1 AS value',1),('1000_rows','SELECT generate_series(1,1000) AS value',1000),('aggregation','SELECT sum(n) AS value FROM generate_series(1,10000) n',1)]:
            for implementation,connection in [('native',native['id']),('jdbc',pg['id'])]:
                for _ in range(2):api('POST','/query/execute',{'connection_id':connection,'sql':sql})
                elapsed=[]
                for _ in range(20):
                    start=time.perf_counter();result=api('POST','/query/execute',{'connection_id':connection,'sql':sql});elapsed.append((time.perf_counter()-start)*1000)
                    assert len(result['rows'])==count
                ordered=sorted(elapsed)
                timing['results'].append({'workload':workload,'implementation':implementation,'n':len(elapsed),'p50_ms':round(ordered[math.ceil(len(ordered)*.5)-1],3),'p95_ms':round(ordered[math.ceil(len(ordered)*.95)-1],3),'max_ms':round(max(elapsed),3)})
        (out/'connector-timing.json').write_text(json.dumps(timing,ensure_ascii=False,indent=2))
        passed('native and JDBC execute the same three workloads with recorded end-to-end timings')

        collected=api('POST','/assets/sync',{'connection_id':pg['id']})
        assert collected['connections'][0]['status']=='succeeded' and collected['tables']>=1
        snapshot=api('GET','/assets/table?connection_id='+str(pg['id'])+'&database='+quote(values['POSTGRES_DB'])+'&schema=public&table='+pg_table)['snapshot']
        assert len(snapshot['raw_columns'])==2 and snapshot['metadata_status']['ddl']=='unsupported'
        passed('PostgreSQL JDBC collects the configured database and publishes its real table snapshot')

        mysql_driver=next((d for d in api('GET','/connector-drivers')['items'] if d['artifact_id'].startswith('mysql-')),None)
        if not mysql_driver:
            artifact=next(a for a in artifacts if a['name']=='mysql');mysql_driver=api('POST','/connector-drivers',{'artifact_id':artifact['id'],'name':prefix+' MySQL driver'},201);drivers.append(mysql_driver['id'])
        mysql_template=api('POST','/datasource-templates',{'template_key':prefix+'-mysql','name':prefix+' MySQL template','product':'MySQL','driver_id':mysql_driver['id'],'dialect':'mysql','url_template':'jdbc:mysql://{host}:{port}/{database}','fields':pg_template['fields']},201);templates.append(mysql_template['id'])
        mysql=api('POST','/connections',{'name':prefix+' MySQL source','type':'jdbc','template_revision_id':mysql_template['id'],'parameters':{'host':'mysql','port':'3306','database':values['MYSQL_DATABASE']},'username':values['MYSQL_USER'],'password':values['MYSQL_PASSWORD'],'ssl_mode':'disable','environment':'test'},201);sources.append(mysql['id'])
        mysql_table=prefix.replace('-','_')+'_mysql'
        api('POST','/query/execute',{'connection_id':mysql['id'],'sql':'CREATE TABLE `'+mysql_table+'` (id INT PRIMARY KEY, label VARCHAR(80))'})
        sql_cleanup.append((mysql['id'],'DROP TABLE IF EXISTS `'+mysql_table+'`'))
        api('POST','/query/execute',{'connection_id':mysql['id'],'sql':"INSERT INTO `"+mysql_table+"` VALUES(1,'MySQL JDBC UI result')"})
        page.goto(frontend+'/query?connection='+str(mysql['id'])+'&sql='+quote('select `id`,`label` from `'+mysql_table+'` order by `id`'),wait_until='networkidle');page.wait_for_selector('.monaco-editor .view-lines')
        page.get_by_role('button',name='格式化',exact=True).click();expect(page.get_by_role('button',name='格式化',exact=True)).to_be_enabled()
        with page.expect_response(lambda r:r.url.endswith('/query/execute') and r.request.method=='POST') as response:page.get_by_role('button',name='运行',exact=True).click()
        assert response.value.status==200 and '`id`' in response.value.request.post_data_json['sql']
        expect(page.get_by_role('cell',name='MySQL JDBC UI result',exact=True)).to_be_visible()
        collected=api('POST','/assets/sync',{'connection_id':mysql['id']});assert collected['connections'][0]['status']=='succeeded' and collected['tables']>=1
        snapshot=api('GET','/assets/table?connection_id='+str(mysql['id'])+'&database='+quote(values['MYSQL_DATABASE'])+'&schema='+quote(values['MYSQL_DATABASE'])+'&table='+mysql_table)['snapshot']
        assert len(snapshot['raw_columns'])==2 and snapshot['metadata_status']['ddl']=='unsupported'
        passed('MySQL JDBC preserves its formatting dialect, runs SQL and publishes a real metadata snapshot')

        redis=api('POST','/connections',{'name':prefix+' Redis','type':'redis','host':'redis','port':6379,'database':'15','ssl_mode':'disable'},201);sources.append(redis['id'])
        key=prefix+':key';api('POST','/redis/key',{'connection_id':redis['id'],'key':key,'type':'string','value':'Redis browser value','ttl':-1})
        page.goto(frontend+'/query?connection='+str(redis['id']),wait_until='networkidle');expect(page.get_by_role('heading',name='键值浏览',exact=True)).to_be_visible();page.get_by_label('键名筛选').fill(prefix+'*');page.locator('form').first.get_by_role('button',name='刷新',exact=True).click();page.get_by_role('button').filter(has_text=key).click();expect(page.get_by_test_id('redis-value')).to_contain_text('Redis browser value');expect(page.get_by_role('button',name='运行',exact=True)).to_have_count(0)
        page.get_by_label('TTL 秒数').fill('120');page.get_by_role('button',name='更新 TTL',exact=True).click();expect(page.get_by_text(re.compile(r'TTL (120|11[0-9]) 秒'))).to_be_visible();expect(page.get_by_role('button').filter(has_text=key)).to_contain_text(re.compile(r'TTL (120|11[0-9])'))
        page.set_viewport_size({'width':390,'height':844});assert page.evaluate('document.documentElement.scrollWidth <= document.documentElement.clientWidth');expect(page.locator('.el-message')).to_have_count(0);page.screenshot(path=str(out/'connector-redis-mobile.png'),animations='disabled',full_page=True)
        passed('Redis opens its key view and updates TTL without SQL controls')

        readonly_password=secrets.token_urlsafe(20)
        user=api('POST','/users',{'username':prefix+'-ro','password':readonly_password,'role':'readonly'},201);users.append(user['id'])
        ro_context=browser.new_context(viewport={'width':390,'height':844},locale='zh-CN');ro=ro_context.new_page();ro.on('pageerror',lambda e:errors.append(str(e)));login(ro,user['username'],readonly_password)
        ro.goto(frontend+'/settings',wait_until='networkidle');expect(ro.get_by_role('heading',name='驱动管理',exact=True)).to_have_count(0)
        ro.goto(frontend+'/query?connection='+str(redis['id']),wait_until='networkidle');expect(ro.get_by_role('heading',name='键值浏览',exact=True)).to_be_visible();expect(ro.get_by_role('button',name='新建字符串键',exact=True)).to_have_count(0)
        ro.goto(frontend+'/query?connection='+str(h2_source['id']),wait_until='networkidle');expect(ro.get_by_role('heading',name='结构浏览',exact=True)).to_be_visible();expect(ro.get_by_role('button',name='采集结构',exact=True)).to_have_count(0)
        passed('readonly users can browse while driver management and write controls stay hidden')
        ro_context.close();context.close();browser.close()
        api('DELETE','/redis/key?connection_id='+str(redis['id'])+'&key='+quote(key))
        assert not errors,errors
    (out/'connector-checks.json').write_text(json.dumps({'scope':'connector-ui','checks':checks,'page_errors':errors,'fixtures':['H2 fixture DDL uses private JDBC protocol only; public H2 SQL remains disabled','Empty Schema list is a UI contract fixture, not an additional driver validation']},ensure_ascii=False,indent=2))
finally:
    if remote_session:
        try:rpc('execute',{**remote_session,'execution_key':str(uuid.uuid4()),'sql':'DROP ALL OBJECTS','args':[],'budget_ms':5000});rpc('close',remote_session)
        except Exception:pass
    for source,sql in reversed(sql_cleanup):api('POST','/query/execute',{'connection_id':source,'sql':sql})
    for source in reversed(sources):api('DELETE','/connections/'+str(source))
    for template in reversed(templates):api('DELETE','/datasource-templates/'+str(template))
    for driver in reversed(drivers):api('DELETE','/connector-drivers/'+str(driver))
    for user in users:api('DELETE','/users/'+str(user))
    for session_token in tokens+[token]:
        if session_token:
            try:api('POST','/auth/logout',bearer=session_token)
            except Exception:pass
