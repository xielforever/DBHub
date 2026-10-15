"""Exercise actual cloud services; never print credentials, tokens or request bodies."""
import json
import os
import re
import urllib.request
from pathlib import Path
from urllib.parse import quote
from playwright.sync_api import sync_playwright

state = Path('/runtime')
values = dict(line.split('=', 1) for line in (state/'runtime.env').read_text().splitlines() if '=' in line)
api = 'http://backend:8080/api/v1'
frontend = 'http://frontend:5173'
token = ''

def request(method, path, body=None, expected=(200,)):
    raw = json.dumps(body).encode() if body is not None else None
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer '+token
    req = urllib.request.Request(api+path, data=raw, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=35) as response:
        assert response.status in expected, path
        payload = json.load(response)
        assert payload['code'] == 0, path
        return payload.get('data')

token = request('POST', '/auth/login', {'username': values['APP_ADMIN_USERNAME'], 'password': values['APP_ADMIN_PASSWORD']})['access_token']
connections = request('GET', '/connections')['items']
ids = {}
for kind, name, host, port, database, username, password in [
    ('postgres', 'Cloud PostgreSQL', 'postgres', 5432, values['POSTGRES_DB'], values['POSTGRES_USER'], values['POSTGRES_PASSWORD']),
    ('mysql', 'Cloud MySQL', 'mysql', 3306, values['MYSQL_DATABASE'], values['MYSQL_USER'], values['MYSQL_PASSWORD']),
    ('redis', 'Cloud Redis', 'redis', 6379, '3', '', ''),
]:
    existing = next((c for c in connections if c['name'] == name), None)
    if not existing:
        existing = request('POST', '/connections', {'name':name, 'type':kind, 'host':host, 'port':port, 'database':database, 'username':username, 'password':password, 'ssl_mode':'disable', 'connection_timeout':5, 'environment':'dev'}, (201,))
    ids[kind] = existing['id']
    request('POST', f"/connections/{existing['id']}/test")
    if kind != 'redis':
        request('POST', '/query/execute', {'connection_id':existing['id'], 'sql':'CREATE TABLE IF NOT EXISTS dbhub_cloud_demo (id INT PRIMARY KEY, name VARCHAR(100), email VARCHAR(100))'})
        insert = "INSERT INTO dbhub_cloud_demo VALUES (1,'Cloud alpha','alpha@example.test'),(2,'Cloud beta','beta@example.test')"
        insert += ' ON CONFLICT(id) DO NOTHING' if kind=='postgres' else ' ON DUPLICATE KEY UPDATE name=VALUES(name)'
        request('POST', '/query/execute', {'connection_id':existing['id'], 'sql':insert})

out = state/'browser-artifacts'
out.mkdir(exist_ok=True)
with sync_playwright() as p:
    browser = p.chromium.launch(executable_path='/usr/bin/chromium', proxy={'server':os.environ.get('HTTPS_PROXY') or 'http://proxy:8080','bypass':'frontend;backend;localhost;127.0.0.1'}, args=['--no-sandbox','--disable-dev-shm-usage'])
    context = browser.new_context(viewport={'width':1600,'height':1000}, locale='zh-CN', reduced_motion='reduce')
    page = context.new_page()
    page.set_default_timeout(25000)
    errors = []
    page.on('pageerror', lambda error: errors.append(str(error)))
    page.goto(frontend+'/login', wait_until='networkidle')
    if not page.locator('#username').count():
        page.screenshot(path=str(out/'login-failure.png'))
        print(json.dumps({'page_errors':errors,'title':page.title(),'body':page.locator('body').inner_text()[:400]},ensure_ascii=False))
    page.locator('#username').fill(values['APP_ADMIN_USERNAME'])
    page.locator('#password').fill(values['APP_ADMIN_PASSWORD'])
    page.locator('button[type=submit]').click()
    page.wait_for_url('**/dashboard')
    sql = 'SELECT id,name,email FROM dbhub_cloud_demo ORDER BY id'
    page.goto(frontend+f"/query?connection={ids['postgres']}&sql={quote(sql)}",wait_until='networkidle')
    page.wait_for_selector('.monaco-editor .view-lines')
    page.get_by_role('button',name=re.compile('^运行')).first.wait_for(state='visible')
    with page.expect_response(lambda r:'/api/v1/query/execute' in r.url and r.request.method=='POST') as result:
        page.get_by_role('button',name=re.compile('^运行')).first.click()
    assert result.value.status == 200, 'SQL request failed'
    data=result.value.json()['data']
    assert data['rows'][0][1]=='Cloud alpha' and len(data['rows'])==2
    page.get_by_text('Cloud alpha',exact=True).first.wait_for()
    page.screenshot(path=str(out/'real-sql-desktop.png'),animations='disabled')
    page.get_by_role('tab',name='AI 优化',exact=True).click()
    page.get_by_text('模型服务尚未配置，可稍后在云环境设置。',exact=True).wait_for()
    page.get_by_role('tab',name='权限',exact=True).click()
    page.get_by_text('行列访问策略',exact=True).wait_for()
    page.get_by_role('tab',name='结果',exact=True).click()
    page.set_viewport_size({'width':390,'height':844})
    page.screenshot(path=str(out/'real-sql-mobile.png'),animations='disabled',full_page=True)
    page.set_viewport_size({'width':1600,'height':1000})
    page.goto(frontend+'/tickets',wait_until='networkidle')
    assert page.get_by_text('当前为本地演示存储').count()==0
    page.screenshot(path=str(out/'real-tickets.png'),animations='disabled')
    page.goto(frontend+'/proxies',wait_until='networkidle')
    assert '当前为功能占位' not in page.locator('main').inner_text()
    page.screenshot(path=str(out/'real-proxies.png'),animations='disabled')
    assert not errors, 'Browser errors: '+json.dumps(errors,ensure_ascii=False)
    browser.close()
(out/'checks.json').write_text(json.dumps({'real_connection_types':list(ids),'sql_rows':2,'ai':'unconfigured','page_errors':errors},ensure_ascii=False,indent=2))
print('Browser checks passed: real PostgreSQL/MySQL/Redis, SQL UI, AI state, policies, tickets, proxies, desktop/mobile; no page errors')
