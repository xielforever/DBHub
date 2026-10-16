"""Check the basic workbench against local real services; never print credentials."""
import json
import os
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
from urllib.parse import quote, urlsplit
from playwright.sync_api import expect, sync_playwright

state = Path('/runtime')
values = dict(line.split('=', 1) for line in (state/'runtime.env').read_text().splitlines() if '=' in line)
api, frontend, token = 'http://backend:8080/api/v1', 'http://frontend:5173', ''

def request(method, path, body=None, expected=(200,)):
    if method == 'POST' and path == '/query/execute' and body is not None:
        body = {'request_id':str(uuid.uuid4()), **body}
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(api+path, data=json.dumps(body).encode() if body is not None else None, headers=headers, method=method)
    try:
        response = urllib.request.urlopen(req, timeout=35)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        assert response.status in expected, f'{method} {path}: unexpected HTTP {response.status}'
        payload = json.load(response)
        if response.status < 400:
            assert payload['code'] == 0, path
        return payload.get('data')

token = request('POST', '/auth/login', {'username':values['APP_ADMIN_USERNAME'], 'password':values['APP_ADMIN_PASSWORD']})['access_token']
connections, ids = request('GET', '/connections')['items'], {}
for kind, name, host, port, database, username, password in [
    ('postgres', 'Cloud PostgreSQL', 'postgres', 5432, values['POSTGRES_DB'], values['POSTGRES_USER'], values['POSTGRES_PASSWORD']),
    ('mysql', 'Cloud MySQL', 'mysql', 3306, values['MYSQL_DATABASE'], values['MYSQL_USER'], values['MYSQL_PASSWORD']),
]:
    item = next((item for item in connections if item['name'] == name), None)
    if not item:
        item = request('POST', '/connections', {'name':name, 'type':kind, 'host':host, 'port':port, 'database':database, 'username':username, 'password':password, 'ssl_mode':'disable', 'connection_timeout':5, 'environment':'dev'}, (201,))
    ids[kind] = item['id']
    request('POST', f"/connections/{item['id']}/test")
    request('POST', '/query/execute', {'connection_id':item['id'], 'sql':'CREATE TABLE IF NOT EXISTS dbhub_cloud_demo (id INT PRIMARY KEY, name VARCHAR(100), email VARCHAR(100))'})
    sql = "INSERT INTO dbhub_cloud_demo VALUES (1,'Cloud alpha','alpha@example.test'),(2,'Cloud beta','beta@example.test')"
    sql += ' ON CONFLICT(id) DO NOTHING' if kind == 'postgres' else ' ON DUPLICATE KEY UPDATE name=VALUES(name)'
    request('POST', '/query/execute', {'connection_id':item['id'], 'sql':sql})

out = state/'browser-artifacts'
out.mkdir(exist_ok=True)
checks, errors = [], []

def passed(name):
    checks.append(name)
    print('PASS '+name, flush=True)

with sync_playwright() as p:
    # 使用逗号分隔，同时适用于 Chromium 与故障模拟中的 Playwright API 请求。
    browser = p.chromium.launch(executable_path='/usr/bin/chromium', proxy={'server':os.environ.get('HTTPS_PROXY') or 'http://proxy:8080', 'bypass':'frontend,backend,localhost,127.0.0.1'}, args=['--no-sandbox','--disable-dev-shm-usage'])
    context = browser.new_context(viewport={'width':1600,'height':1000}, locale='zh-CN', reduced_motion='reduce')
    page = context.new_page()
    page.set_default_timeout(20000)
    page.on('pageerror', lambda error: errors.append(str(error)))
    calls = []
    page.on('request', lambda req: calls.append((req.method, urlsplit(req.url).path)))
    page.goto(frontend+'/login', wait_until='networkidle')
    page.locator('#username').fill(values['APP_ADMIN_USERNAME'])
    page.locator('#password').fill(values['APP_ADMIN_PASSWORD'])
    page.locator('button[type=submit]').click()
    page.wait_for_url('**/dashboard')

    def open_sql(kind, sql):
        page.goto(frontend+f"/query?connection={ids[kind]}&sql={quote(sql)}", wait_until='networkidle')
        page.wait_for_selector('.monaco-editor .view-lines')
        expect(page.get_by_role('button', name='运行', exact=True)).to_be_enabled()
        return page.get_by_test_id('sql-workbench')

    def set_sql(sql):
        page.locator('.monaco-editor').click(position={'x':90,'y':20})
        page.keyboard.press('Control+A')
        page.keyboard.insert_text(sql)

    def execute(sql=None):
        if sql is not None:
            set_sql(sql)
        with page.expect_response(lambda response:'/api/v1/query/execute' in response.url and response.request.method=='POST') as pending:
            page.get_by_role('button', name='运行', exact=True).click()
        response = pending.value
        expect(page.get_by_role('button', name='运行', exact=True)).to_be_enabled()
        return response, response.json(), response.request.post_data_json

    sql = 'SELECT id,name,email FROM dbhub_cloud_demo ORDER BY id'
    workbench = open_sql('postgres', sql)
    response, payload, body = execute()
    assert response.status == 200 and payload['data']['rows'][0][1] == 'Cloud alpha'
    assert body['sql'] == sql and not body.get('params') and not body.get('transaction_id')
    assert uuid.UUID(body['request_id'])
    expect(page.get_by_role('cell', name='Cloud alpha', exact=True)).to_be_visible()
    buttons = [button.inner_text().strip() for button in workbench.get_by_role('button').all() if button.is_visible()]
    assert buttons == ['运行','格式化'], buttons
    assert workbench.get_by_role('tab').count() == 0
    page.screenshot(path=str(out/'phase1-sql-desktop.png'), animations='disabled')
    passed('real PostgreSQL, two actions, unchanged SQL and no implicit transaction')

    page.set_viewport_size({'width':390,'height':844})
    expect(page.get_by_role('cell', name='Cloud alpha', exact=True)).to_be_visible()
    assert page.evaluate('document.documentElement.scrollWidth <= document.documentElement.clientWidth')
    assert page.get_by_role('cell', name='Cloud alpha', exact=True).bounding_box()['y'] < 844
    page.screenshot(path=str(out/'phase1-sql-mobile.png'), animations='disabled', full_page=True)
    page.set_viewport_size({'width':1600,'height':1000})
    passed('mobile SQL and result visible without page overflow')

    fetch_sql = 'SELECT id,name FROM dbhub_cloud_demo ORDER BY id FETCH FIRST 1 ROW ONLY'
    response, payload, body = execute(fetch_sql)
    assert response.status == 200 and len(payload['data']['rows']) == 1 and body['sql'] == fetch_sql, json.dumps({'status':response.status,'sent_sql':body['sql'],'message':payload.get('message'),'row_count':len((payload.get('data') or {}).get('rows',[]))})
    passed('FETCH FIRST remains valid without automatic LIMIT')

    set_sql('SELECT 1 AS selected_value;\nSELECT 2 AS unused_value;')
    page.locator('.monaco-editor').click(position={'x':90,'y':20})
    page.keyboard.press('Control+Home')
    page.keyboard.press('Shift+End')
    with page.expect_response(lambda response:'/api/v1/query/execute' in response.url) as pending:
        page.keyboard.press('Control+Enter')
    assert pending.value.status == 200
    assert pending.value.request.post_data_json['sql'] == 'SELECT 1 AS selected_value;'
    expect(page.get_by_role('columnheader', name='selected_value', exact=True)).to_be_visible()
    passed('keyboard shortcut runs only the selected statement')

    response, _, _ = execute('SELECT 1; SELECT 2;')
    assert response.status == 400
    expect(page.get_by_test_id('query-result').get_by_role('alert')).to_contain_text('每次只允许执行一条 SQL')
    response, _, _ = execute('SELECT missing_column FROM dbhub_cloud_demo')
    assert response.status == 400
    expect(page.get_by_test_id('query-result').get_by_role('alert')).to_contain_text('执行失败')
    passed('multiple statements and SQL errors are clearly reported')

    response, payload, _ = execute('SELECT generate_series(1,1100) AS value')
    assert response.status == 200 and payload['data']['truncated']
    count = len(payload['data']['rows'])
    expect(page.get_by_test_id('query-result')).to_contain_text(f'结果已截断，仅显示前 {count} 行')
    grid = page.get_by_role('region', name='SQL 查询结果表格')
    assert grid.get_by_role('rowheader').count() < count
    grid.evaluate('node => { node.scrollTop = node.scrollHeight }')
    expect(grid.get_by_role('rowheader', name=str(count), exact=True)).to_be_visible()
    passed('real truncation count and virtual scrolling to the last row')

    slow_sql = f'SELECT pg_sleep(10) /* phase1-cancel-{time.time_ns()} */'
    set_sql(slow_sql)
    before = sum(method=='POST' and path.endswith('/query/execute') for method,path in calls)
    with page.expect_request(lambda req:'/api/v1/query/execute' in req.url):
        page.keyboard.press('Control+Enter')
    page.keyboard.press('Control+Enter')
    expect(page.get_by_role('button', name='执行中', exact=True)).to_be_disabled()
    job = None
    for _ in range(30):
        job = next((item for item in request('GET','/query/jobs')['items'] if item['sql']==slow_sql), None)
        if job and job['status']=='running':
            break
        time.sleep(0.1)
    assert job and job['status']=='running'
    assert sum(method=='POST' and path.endswith('/query/execute') for method,path in calls)==before+1
    page.get_by_role('button', name='取消', exact=True).click()
    expect(page.get_by_text('执行结果待确认', exact=True)).to_be_visible()
    expect(page.get_by_role('button', name='运行', exact=True)).to_be_disabled()
    canceled = False
    try:
        for _ in range(40):
            latest = next(item for item in request('GET','/query/jobs')['items'] if item['id']==job['id'])
            if latest['status']=='cancelled':
                canceled = True
                break
            time.sleep(0.1)
    finally:
        if not canceled:
            request('POST',f"/query/jobs/{job['id']}/cancel")
    assert canceled, 'browser cancellation did not reach the server query'
    passed('duplicate shortcuts make one request; canceled server query reports uncertain outcome')

    saved_id = page.evaluate("Object.entries(sessionStorage).find(([key]) => key.startsWith('dbhub:pending-execution:'))?.[1]")
    assert saved_id and str(uuid.UUID(saved_id)) == saved_id
    page.reload(wait_until='networkidle')
    expect(page.get_by_text('执行结果待确认', exact=True)).to_be_visible()
    expect(page.get_by_test_id('query-result')).to_contain_text(saved_id)
    expect(page.get_by_test_id('query-result')).to_contain_text('Cloud PostgreSQL / '+values['POSTGRES_DB'])
    expect(page.get_by_role('button', name='运行', exact=True)).to_be_disabled()
    page.screenshot(path=str(out/'phase1-sql-unknown.png'), animations='disabled')
    page.get_by_role('button', name='核实状态', exact=True).click()
    expect(page.get_by_text('执行结果待确认', exact=True)).to_be_visible()
    page.get_by_role('button', name='已核实，继续', exact=True).click()
    page.get_by_role('button', name='我已核实，继续', exact=True).click()
    expect(page.get_by_role('button', name='运行', exact=True)).to_be_enabled()
    assert page.evaluate("Object.entries(sessionStorage).every(([key,value]) => !key.startsWith('dbhub:pending-execution:'))")
    passed('refresh restores only the pending execution ID and requires verification before a new run')

    response, payload, _ = execute('SELECT 7 AS after_cancel')
    assert response.status==200 and payload['data']['rows']==[[7]]
    passed('fresh execution succeeds after cancellation')

    request('POST', '/query/execute', {'connection_id':ids['postgres'], 'sql':"UPDATE dbhub_cloud_demo SET name='Cloud beta' WHERE id=2"})
    lost_writes = []
    def lose_write_response(route):
        lost_writes.append(route.request.post_data_json['request_id'])
        upstream = route.fetch()
        assert upstream.status == 200, f'write-response simulator received HTTP {upstream.status}: {upstream.text()[:200]}'
        route.abort('connectionreset')
    page.route('**/api/v1/query/execute', lose_write_response)
    set_sql("UPDATE dbhub_cloud_demo SET name=name || '-receipt' WHERE id=2")
    page.get_by_role('button', name='运行', exact=True).click()
    expect(page.get_by_text('执行成功，影响 1 行', exact=True)).to_be_visible()
    page.unroute('**/api/v1/query/execute', lose_write_response)
    assert len(lost_writes) == 1
    recovered = request('GET', '/query/executions/'+lost_writes[0])
    assert recovered['status'] == 'succeeded' and recovered['affected_rows'] == 1 and recovered['database'] == values['POSTGRES_DB']
    actual = request('POST', '/query/execute', {'connection_id':ids['postgres'], 'sql':'SELECT name FROM dbhub_cloud_demo WHERE id=2'})
    assert actual['rows'] == [['Cloud beta-receipt']]
    passed('lost committed-write response is recovered from a receipt without repeating the write')

    page.get_by_role('combobox', name='选择连接').focus()
    page.keyboard.press('ArrowDown')
    page.get_by_role('option').filter(has_text='Cloud MySQL').click()
    expect(page.get_by_role('button', name='运行', exact=True)).to_be_enabled()
    set_sql('select `id`,`name` from `dbhub_cloud_demo` order by `id`')
    page.get_by_role('button', name='格式化', exact=True).click()
    expect(page.get_by_role('button', name='格式化', exact=True)).to_be_enabled()
    assert workbench.get_by_role('alert').count()==0
    response, payload, body = execute()
    assert response.status==200 and len(payload['data']['rows'])==2
    assert body['connection_id']==ids['mysql']
    assert body['sql'].startswith('SELECT') and '\n' in body['sql'] and '`id`' in body['sql']
    passed('real MySQL execution and dialect-aware formatting')

    page.wait_for_timeout(2200)
    forbidden = ('/query/jobs','/query/history','/query/settings','/query/policies','/query/exports','/query/shares','/ai/','/presence','/snippets','/lineage')
    assert not [path for _,path in calls if '/api/v1' in path and any(part in path for part in forbidden)]
    assert not errors, 'Browser page errors occurred'
    passed('no advanced panels or background feature requests')
    context.close()
    browser.close()

(out/'phase1-checks.json').write_text(json.dumps({'scope':'sql-workbench-phase1','checks':checks,'real_databases':list(ids),'page_errors':errors}, ensure_ascii=False, indent=2))
print('Phase-one browser checks passed: '+str(len(checks)), flush=True)
