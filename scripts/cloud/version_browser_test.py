"""版本提示的浏览器验收；真实连接结果与未验证状态的契约检查分别记录。"""
import json
import os
import secrets
import urllib.error
import urllib.request
from pathlib import Path
from playwright.sync_api import sync_playwright, expect

state = Path('/runtime')
values = dict(line.split('=',1) for line in (state/'runtime.env').read_text().splitlines() if '=' in line)
base, frontend, token = 'http://backend:8080/api/v1', 'http://frontend:5173', ''
browser_token = ''
created, checks, errors = [], [], []
out = state/'browser-artifacts'
out.mkdir(exist_ok=True)

def api(method, path, body=None, want=200, auth_token=None):
    headers = {'Content-Type':'application/json'}
    selected_token = token if auth_token is None else auth_token
    if selected_token:
        headers['Authorization'] = 'Bearer '+selected_token
    request = urllib.request.Request(base+path, data=json.dumps(body).encode() if body is not None else None, headers=headers, method=method)
    try:
        response = urllib.request.urlopen(request, timeout=25)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        assert response.status==want, 'unexpected API status: '+path
        return json.load(response)['data']

def passed(text):
    checks.append(text)
    print('PASS '+text, flush=True)

token = api('POST','/auth/login',{'username':values['APP_ADMIN_USERNAME'],'password':values['APP_ADMIN_PASSWORD']})['access_token']
try:
    specs = [
        ('postgres',5432,values['POSTGRES_USER'],values['POSTGRES_PASSWORD'],values['POSTGRES_DB']),
        ('mysql',3306,values['MYSQL_USER'],values['MYSQL_PASSWORD'],values['MYSQL_DATABASE']),
        ('redis',6379,'','','0'),
    ]
    for kind, port, username, password, database in specs:
        item = api('POST','/connections',{'name':'Version UI '+kind+' '+secrets.token_hex(4),'type':kind,'host':kind,'port':port,'username':username,'password':password,'database':database,'ssl_mode':'disable','environment':'test'},201)
        created.append(item)
    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path='/usr/bin/chromium',proxy={'server':os.environ.get('HTTPS_PROXY') or 'http://proxy:8080','bypass':'frontend,backend,localhost,127.0.0.1'},args=['--no-sandbox','--disable-dev-shm-usage'])
        context = browser.new_context(viewport={'width':1440,'height':1000},locale='zh-CN',reduced_motion='reduce')
        page = context.new_page()
        page.set_default_timeout(20000)
        page.on('pageerror', lambda error: errors.append(str(error)))
        page.goto(frontend+'/login',wait_until='networkidle')
        page.locator('#username').fill(values['APP_ADMIN_USERNAME'])
        page.locator('#password').fill(values['APP_ADMIN_PASSWORD'])
        with page.expect_response(lambda r:r.url.endswith('/auth/login')) as login:
            page.locator('button[type=submit]').click()
        browser_token = login.value.json()['data']['access_token']
        page.wait_for_url('**/dashboard')
        page.goto(frontend+'/connections',wait_until='networkidle')
        for item in created:
            card = page.locator('article').filter(has=page.get_by_role('heading',name=item['name'],exact=True))
            with page.expect_response(lambda r:r.url.endswith(f"/connections/{item['id']}/test")) as pending:
                card.get_by_role('button',name='测试连接 '+item['name'],exact=True).click()
            result = pending.value.json()['data']['compatibility']
            expect(card.get_by_role('status')).to_contain_text(result['product']+' '+result['version'])
            assert result['status'] in ('verified','branch_verified')
            passed('real '+item['type']+' version is displayed on the saved connection')

        item = created[0]
        card = page.locator('article').filter(has=page.get_by_role('heading',name=item['name'],exact=True))
        card.get_by_role('button',name='编辑连接',exact=True).click()
        dialog = page.get_by_role('dialog')
        expect(dialog.get_by_test_id('version-support-hint')).to_contain_text('14 / 15 / 16 / 17 / 18')
        dialog.get_by_label('密码',exact=True).fill(values['POSTGRES_PASSWORD'])
        with page.expect_response(lambda r:r.url.endswith('/connections/test')):
            dialog.get_by_role('button',name='测试连接',exact=True).click()
        expect(dialog.get_by_test_id('connection-version-result')).to_contain_text('PostgreSQL')
        host = dialog.get_by_label('主机',exact=True)
        host.fill('changed.invalid')
        expect(dialog.get_by_test_id('connection-version-result')).to_have_count(0)
        host.fill('postgres')
        passed('editing a target clears the previous verified result')

        def change_before_delivery(route):
            response = route.fetch()
            assert response.status==200
            host.fill('changed.invalid')
            route.fulfill(response=response)
        page.route('**/api/v1/connections/test',change_before_delivery)
        with page.expect_response(lambda r:r.url.endswith('/connections/test')):
            dialog.get_by_role('button',name='测试连接',exact=True).click()
        expect(dialog.get_by_role('button',name='测试连接',exact=True)).to_be_enabled()
        expect(dialog.get_by_test_id('connection-version-result')).to_have_count(0)
        page.unroute('**/api/v1/connections/test',change_before_delivery)
        passed('late connection-test response cannot verify an edited target')
        host.fill('postgres')

        # 以下只替换兼容信息以验证 UI 状态，不计作 MariaDB 或未知数据库的真实验证。
        def unverified_profile(route):
            response = route.fetch()
            payload = response.json()
            payload['data']['compatibility'] = {'engine':'mariadb','product':'MariaDB','version':'10.11.11','branch':'10.11','status':'unverified','message':'MariaDB 尚未纳入兼容验证。'}
            route.fulfill(status=200,json=payload)
        page.route('**/api/v1/connections/test',unverified_profile)
        dialog.get_by_role('button',name='测试连接',exact=True).click()
        expect(dialog.get_by_test_id('connection-version-result')).to_contain_text('MariaDB 10.11.11 · 兼容性未验证')
        page.unroute('**/api/v1/connections/test',unverified_profile)
        passed('contract fixture: protocol-compatible product is shown as unverified')
        expect(page.locator('.el-message')).to_have_count(0)
        page.screenshot(path=str(out/'versions-unverified-desktop.png'),animations='disabled')
        page.set_viewport_size({'width':390,'height':844})
        assert page.evaluate('document.documentElement.scrollWidth<=document.documentElement.clientWidth')
        expect(dialog.get_by_test_id('connection-version-result')).to_contain_text('兼容性未验证')
        dialog.get_by_test_id('connection-version-result').scroll_into_view_if_needed()
        page.screenshot(path=str(out/'versions-unverified-mobile.png'),animations='disabled',full_page=True)
        passed('mobile connection form keeps the version result readable')

        # 正常状态截图使用真实 PostgreSQL 响应，警示样例单独保留。
        page.set_viewport_size({'width':1440,'height':1000})
        dialog.get_by_role('button',name='测试连接',exact=True).click()
        expect(dialog.get_by_test_id('connection-version-result')).to_contain_text('PostgreSQL')
        expect(page.locator('.el-message')).to_have_count(0)
        page.screenshot(path=str(out/'versions-desktop.png'),animations='disabled')
        page.set_viewport_size({'width':390,'height':844})
        dialog.get_by_test_id('connection-version-result').scroll_into_view_if_needed()
        page.screenshot(path=str(out/'versions-mobile.png'),animations='disabled',full_page=True)
        assert not errors
        context.close()
        browser.close()
    (out/'versions-checks.json').write_text(json.dumps({'scope':'datasource-version-ui','checks':checks,'page_errors':errors,'contract_fixtures':['unverified MariaDB profile only']},ensure_ascii=False,indent=2))
finally:
    for item in created:
        api('DELETE',f"/connections/{item['id']}")
    if browser_token:
        api('POST','/auth/logout',auth_token=browser_token)
    if token:
        api('POST','/auth/logout')
