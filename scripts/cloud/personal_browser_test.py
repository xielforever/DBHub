"""Publish a JDBC inventory source and connect with two real personal identities."""
import json
import os
import re
import urllib.error
import urllib.request
import uuid
from pathlib import Path
from urllib.parse import quote
from playwright.sync_api import expect, sync_playwright

values = os.environ
fixture = json.loads(Path(values['DBHUB_PERSONAL_FIXTURE']).read_text())
prefix = fixture['prefix']
base, frontend = 'http://backend:8080/api/v1', 'http://frontend:5173'
token = ''
tokens, checks, errors = [], [], []
app_users = {}
out = Path('/runtime/browser-artifacts'); out.mkdir(exist_ok=True)

def api(method, path, body=None, want=200, bearer=None):
    headers = {'Content-Type': 'application/json'}
    auth = token if bearer is None else bearer
    if auth:
        headers['Authorization'] = 'Bearer ' + auth
    if method == 'POST' and path == '/query/execute':
        body = {'request_id': str(uuid.uuid4()), **body}
    request = urllib.request.Request(base+path, method=method, headers=headers,
        data=json.dumps(body).encode() if body is not None else None)
    try:
        response = urllib.request.urlopen(request, timeout=40)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        assert response.status == want, f'{method} {path}: HTTP {response.status}'
        return json.load(response).get('data')

def passed(name):
    checks.append(name); print('PASS ' + name, flush=True)

def choose(page, combo, name):
    combo.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")][1]').click()
    page.get_by_role('option', name=name, exact=False).last.click()

def login(page, name, password):
    page.goto(frontend+'/login', wait_until='networkidle')
    page.locator('#username').fill(name); page.locator('#password').fill(password)
    with page.expect_response(lambda r: r.url.endswith('/auth/login')) as response:
        page.locator('button[type=submit]').click()
    assert response.value.status == 200
    data = response.value.json()['data']; tokens.append(data['access_token'])
    page.wait_for_url('**/dashboard')
    return data['access_token']

def assert_no_overflow(page):
    assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth + 1')

token = api('POST', '/auth/login', {'username': values['APP_ADMIN_USERNAME'], 'password': values['APP_ADMIN_PASSWORD']})['access_token']
tokens.append(token)
try:
    for name, role in [(fixture['app_user'], 'developer'), (fixture['app_readonly'], 'readonly')]:
        app_users[name] = api('POST', '/users', {'username': name, 'password': fixture['app_password'], 'role': role}, 201)
    artifacts = api('GET', '/connector-drivers/available')['items']
    artifact = next(a for a in artifacts if a['name'] == 'postgresql')
    existing = next((d for d in api('GET', '/connector-drivers')['items'] if d['artifact_id'] == artifact['id'] and d['visibility'] == 'global'), None)
    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path='/usr/bin/chromium',
            proxy={'server': values.get('HTTPS_PROXY') or 'http://proxy:8080', 'bypass': 'frontend,backend,jdbc,localhost,127.0.0.1'},
            args=['--no-sandbox', '--disable-dev-shm-usage'])
        admin = browser.new_context(viewport={'width': 1440, 'height': 1000}, locale='zh-CN', reduced_motion='reduce')
        page = admin.new_page(); page.set_default_timeout(20000); page.on('pageerror', lambda e: errors.append(str(e)))
        login(page, values['APP_ADMIN_USERNAME'], values['APP_ADMIN_PASSWORD'])
        page.goto(frontend+'/settings', wait_until='networkidle')
        if existing is None:
            section = page.get_by_role('region', name='驱动管理')
            choose(page, section.get_by_role('combobox', name='可用驱动制品'), 'postgresql '+artifact['version'])
            section.get_by_label('驱动名称', exact=True).fill(prefix+' PG')
            choose(page, section.get_by_role('combobox', name='驱动可见范围'), '全局发布')
            with page.expect_response(lambda r: r.url.endswith('/connector-drivers') and r.request.method == 'POST') as response:
                section.get_by_role('button', name='登记驱动', exact=True).click()
            assert response.value.status == 201; existing = response.value.json()['data']
        passed('administrator uses an installed and verified globally published JDBC driver')
        section = page.get_by_role('region', name='数据源模板')
        section.get_by_label('模板标识', exact=True).fill(prefix+'_pg')
        section.get_by_label('模板名称', exact=True).fill(prefix+' PG 规则')
        choose(page, section.get_by_role('combobox', name='模板驱动版本'), existing['name']+' '+existing['version'])
        choose(page, section.get_by_role('combobox', name='连接规则可见范围'), '全局发布')
        choose(page, section.get_by_role('combobox', name='模板 SQL 能力'), 'PostgreSQL 方言与基础执行')
        with page.expect_response(lambda r: r.url.endswith('/datasource-templates') and r.request.method == 'POST') as response:
            section.get_by_role('button', name='发布模板', exact=True).click()
        assert response.value.status == 201
        passed('administrator publishes reusable global connection rules in settings')

        page.goto(frontend+'/connections', wait_until='networkidle')
        page.get_by_role('button', name='新建个人数据源', exact=True).click()
        dialog = page.get_by_role('dialog', name='新建数据源')
        source_name = prefix+' 个人配置示例'
        dialog.get_by_label('数据源名称', exact=True).fill(source_name)
        choose(page, dialog.get_by_label('数据源类型', exact=True), '自定义 JDBC')
        choose(page, dialog.get_by_role('combobox', name='数据源模板'), prefix+' PG 规则')
        dialog.get_by_label('主机', exact=True).fill('postgres')
        dialog.get_by_label('端口', exact=True).fill('5432')
        dialog.get_by_label('数据库', exact=True).fill(values['POSTGRES_DB'])
        dialog.get_by_label('用户名', exact=True).fill(values['POSTGRES_USER'])
        dialog.get_by_label('密码', exact=True).fill(values['POSTGRES_PASSWORD'])
        choose(page, dialog.get_by_label('SSL 模式', exact=True), '禁用')
        with page.expect_response(lambda r: r.url.endswith('/connections') and r.request.method == 'POST') as response:
            dialog.get_by_role('button', name='保存', exact=True).click()
        assert response.value.status == 201; admin_source = response.value.json()['data']
        card = page.locator('article').filter(has=page.get_by_role('heading', name=source_name, exact=True))
        card.get_by_role('button', name='发布数据源定义', exact=True).click()
        with page.expect_response(lambda r: r.url.endswith('/publish') and r.request.method == 'POST') as response:
            page.get_by_role('dialog', name='发布数据源定义').get_by_role('button', name='发布定义', exact=True).click()
        assert response.value.status == 201; definition = response.value.json()['data']
        assert 'username' not in definition['config'] and 'password' not in definition['config']
        expect(page.get_by_role('tab', name='数据源目录')).to_have_attribute('aria-selected', 'true')
        page.screenshot(path=str(out/'personal-catalog-desktop.png'), animations='disabled')
        passed('publishing the inventory source shares the target without administrator credentials')

        page.get_by_role('button', name='新建数据源定义', exact=True).click()
        dialog = page.get_by_role('dialog', name='新建数据源定义')
        source_name = prefix+' 库存数据源'
        dialog.get_by_label('数据源名称', exact=True).fill(source_name)
        choose(page, dialog.get_by_label('类型', exact=True), '自定义 JDBC')
        choose(page, dialog.get_by_label('驱动连接规则', exact=True), prefix+' PG 规则')
        dialog.get_by_label('主机', exact=True).fill('postgres')
        dialog.get_by_label('端口', exact=True).fill('5432')
        dialog.get_by_label('数据库', exact=True).fill(values['POSTGRES_DB'])
        choose(page, dialog.get_by_label('TLS', exact=True), '禁用')
        assert dialog.locator('input[type=password]').count() == 0
        expect(dialog.get_by_role('button', name='保存数据源定义', exact=True)).to_be_in_viewport()
        with page.expect_response(lambda r: r.url.endswith('/datasource-definitions') and r.request.method == 'POST') as response:
            dialog.get_by_role('button', name='保存数据源定义', exact=True).click()
        assert response.value.status == 201; definition = response.value.json()['data']
        assert 'username' not in definition['config'] and 'password' not in definition['config']
        expect(dialog).not_to_be_visible()
        page.screenshot(path=str(out/'personal-catalog-desktop.png'), animations='disabled')
        passed('administrators can publish an inventory target without configuring any database credentials')

        mobile = browser.new_context(viewport={'width': 390, 'height': 844}, locale='zh-CN', reduced_motion='reduce')
        test = mobile.new_page(); test.set_default_timeout(20000); test.on('pageerror', lambda e: errors.append(str(e)))
        test_token = login(test, fixture['app_user'], fixture['app_password'])
        test.goto(frontend+'/connections', wait_until='networkidle')
        expect(test.locator('article')).to_have_count(0)
        api('GET', f"/connections/{admin_source['id']}", want=404, bearer=test_token)
        passed('a newly invited user has no inherited administrator connections')
        test.get_by_role('tab', name='数据源目录', exact=True).click()
        card = test.locator('article').filter(has=test.get_by_role('heading', name=source_name, exact=True))
        card.get_by_role('button', name='添加到我的连接', exact=True).click()
        dialog = test.get_by_role('dialog', name='添加到我的连接')
        expect(dialog.get_by_label('我的数据库账号', exact=True)).to_have_value('')
        expect(dialog.get_by_role('button', name='保存我的连接')).to_be_disabled()
        dialog.get_by_label('我的连接名称', exact=True).fill(prefix+' 我的库存')
        dialog.get_by_label('我的数据库账号', exact=True).fill(fixture['reader'])
        dialog.get_by_label('我的数据库密码', exact=True).fill(fixture['reader_password'])
        dialog.locator('.identity-confirm').click()
        expect(dialog.get_by_role('checkbox', name='确认连接目标')).to_be_checked()
        with test.expect_response(lambda r: '/connections?test=true' in r.url) as response:
            dialog.get_by_role('button', name='测试我的连接', exact=True).click()
        assert response.value.status == 200
        expect(dialog.get_by_role('status')).to_contain_text('个人连接测试成功')
        assert_no_overflow(test)
        passed('mobile users explicitly confirm the target and test their own credentials')
        with test.expect_response(lambda r: r.url.endswith('/connections') and '/datasource-definitions/' in r.url) as response:
            dialog.get_by_role('button', name='保存我的连接', exact=True).click()
        assert response.value.status == 201; test_source = response.value.json()['data']
        expect(test.get_by_role('tab', name='我的连接')).to_have_attribute('aria-selected', 'true')
        expect(test.locator('article')).to_contain_text(fixture['reader'])
        test.screenshot(path=str(out/'personal-connections-mobile.png'), animations='disabled')
        passed('the saved inventory binding belongs only to the current user')
        test.goto(frontend+'/query?connection='+str(test_source['id'])+'&sql='+quote('SELECT current_user AS database_user'), wait_until='networkidle')
        test.wait_for_selector('.monaco-editor .view-lines')
        with test.expect_response(lambda r: r.url.endswith('/query/execute')) as response:
            test.get_by_role('button', name='运行', exact=True).click()
        assert response.value.status == 200
        expect(test.get_by_role('cell', name=fixture['reader'], exact=True)).to_be_visible()
        buttons = [b.inner_text().strip() for b in test.get_by_test_id('sql-workbench').get_by_role('button').all() if b.is_visible()]
        assert buttons == ['运行', '格式化'], buttons
        assert_no_overflow(test)
        test.screenshot(path=str(out/'personal-workspace-mobile.png'), animations='disabled')
        passed('JDBC SQL executes as the personal database principal and retains two workspace actions')

        test.goto(frontend+'/settings', wait_until='networkidle')
        section = test.get_by_role('region', name='驱动管理')
        choose(test, section.get_by_role('combobox', name='可用驱动制品'), 'postgresql '+artifact['version'])
        section.get_by_label('驱动名称', exact=True).fill(prefix+' 私有 PG')
        with test.expect_response(lambda r: r.url.endswith('/connector-drivers') and r.request.method == 'POST') as response:
            section.get_by_role('button', name='登记驱动', exact=True).click()
        assert response.value.status == 201; private_driver = response.value.json()['data']
        assert private_driver['visibility'] == 'private'
        assert all(d['id'] != private_driver['id'] for d in api('GET', '/connector-drivers')['items'])
        passed('ordinary users can add private configurations without publishing them globally')

        page.goto(frontend+'/connections', wait_until='networkidle')
        page.get_by_role('tab', name='数据源目录', exact=True).click()
        page.locator('article').filter(has=page.get_by_role('heading', name=source_name, exact=True)).get_by_role('button', name='管理 '+source_name, exact=True).click()
        page.get_by_role('menuitem', name='新修订', exact=True).click()
        revision_dialog = page.get_by_role('dialog', name='发布数据源新修订')
        revision_dialog.get_by_label('说明', exact=True).fill('新修订由使用者确认后切换')
        with page.expect_response(lambda r: r.url.endswith('/revisions')) as response:
            revision_dialog.get_by_role('button', name='发布新修订', exact=True).click()
        assert response.value.status == 201; new_definition = response.value.json()['data']
        saved = api('GET', f"/connections/{test_source['id']}", bearer=test_token)
        assert saved['definition_revision_id'] == definition['id']
        test.goto(frontend+'/connections', wait_until='networkidle')
        test.get_by_role('tab', name='数据源目录', exact=True).click()
        test.locator('article').filter(has=test.get_by_role('heading', name=source_name, exact=True)).get_by_role('button', name='查看并切换修订', exact=True).click()
        dialog = test.get_by_role('dialog', name='确认新修订与个人身份')
        expect(dialog.get_by_role('button', name='确认并切换', exact=True)).to_be_disabled()
        dialog.get_by_label('我的数据库账号', exact=True).fill(fixture['reader'])
        dialog.get_by_label('我的数据库密码', exact=True).fill(fixture['reader_password'])
        dialog.locator('.identity-confirm').click()
        expect(dialog.get_by_role('checkbox', name='确认连接目标')).to_be_checked()
        with test.expect_response(lambda r: r.url.endswith('/revision')) as response:
            dialog.get_by_role('button', name='确认并切换', exact=True).click()
        assert response.value.status == 200
        assert response.value.json()['data']['definition_revision_id'] == new_definition['id']
        passed('published revisions leave existing bindings unchanged until the user confirms and re-enters credentials')

        readonly = browser.new_context(viewport={'width': 1200, 'height': 900}, locale='zh-CN', reduced_motion='reduce')
        ro = readonly.new_page(); ro.set_default_timeout(20000); ro.on('pageerror', lambda e: errors.append(str(e)))
        ro_token = login(ro, fixture['app_readonly'], fixture['app_password'])
        ro.goto(frontend+'/connections', wait_until='networkidle'); ro.get_by_role('tab', name='数据源目录', exact=True).click()
        ro.locator('article').filter(has=ro.get_by_role('heading', name=source_name, exact=True)).get_by_role('button', name='添加到我的连接', exact=True).click()
        dialog = ro.get_by_role('dialog', name='添加到我的连接')
        dialog.get_by_label('我的数据库账号', exact=True).fill(fixture['writer'])
        dialog.get_by_label('我的数据库密码', exact=True).fill(fixture['writer_password'])
        dialog.locator('.identity-confirm').click()
        expect(dialog.get_by_role('checkbox', name='确认连接目标')).to_be_checked()
        with ro.expect_response(lambda r: r.url.endswith('/connections') and '/datasource-definitions/' in r.url) as response:
            dialog.get_by_role('button', name='保存我的连接', exact=True).click()
        assert response.value.status == 201; readonly_source = response.value.json()['data']
        result = api('POST', '/query/execute', {'connection_id': readonly_source['id'], 'sql': 'SELECT current_user'}, bearer=ro_token)
        assert result['rows'][0][0] == fixture['writer']
        api('POST', '/query/execute', {'connection_id': readonly_source['id'], 'sql': f"UPDATE {fixture['table']} SET name='blocked' WHERE id=1"}, 400, bearer=ro_token)
        api('GET', f"/connections/{test_source['id']}", want=404, bearer=ro_token)
        api('GET', f"/connections/{test_source['id']}", want=404)
        assert all(d['id'] != private_driver['id'] for d in api('GET', '/connector-drivers', bearer=ro_token)['items'])
        passed('readonly users configure their own identity while platform write restrictions remain enforced')
        passed('a second user and administrator cannot use another personal binding or private driver')

        # A real failed JDBC login is persisted and repaired with the credential
        # owner's own form. No mocked connection result is used in this flow.
        saved = api('GET', f"/connections/{test_source['id']}", bearer=test_token)
        credential_payload = {key: saved[key] for key in ['name', 'type', 'host', 'port', 'database', 'username', 'ssl_mode', 'connection_timeout', 'environment', 'template_revision_id', 'parameters']}
        credential_payload['password'] = 'IncorrectFixtureCredential9!'
        api('PUT', f"/connections/{test_source['id']}", credential_payload, bearer=test_token)
        test.goto(frontend+'/connections', wait_until='networkidle')
        card = test.locator('article').filter(has=test.get_by_role('heading', name=saved['name'], exact=True))
        with test.expect_response(lambda r: r.url.endswith(f"/connections/{test_source['id']}/test")) as response:
            card.get_by_role('button', name='测试连接 '+saved['name'], exact=True).click()
        assert response.value.status == 400
        assert response.value.json()['data']['diagnostic']['code'] == 'authentication_failed'
        expect(card.get_by_test_id('connection-diagnosis')).to_contain_text('核对本人的数据库账号')
        passed('real JDBC authentication failures provide safe categorized recovery guidance')
        test.reload(wait_until='networkidle')
        card = test.locator('article').filter(has=test.get_by_role('heading', name=saved['name'], exact=True))
        expect(card.get_by_role('button', name='更新我的凭据', exact=True)).to_be_visible()
        assert_no_overflow(test)
        test.screenshot(path=str(out/'connection-diagnosis-mobile.png'), animations='disabled')
        passed('failure diagnosis survives reload and fits the mobile connection card')
        card.get_by_role('button', name='更新我的凭据', exact=True).click()
        dialog = test.get_by_role('dialog', name='编辑数据源')
        expect(dialog.get_by_label('密码', exact=True)).to_have_value('')
        expect(dialog.get_by_label('密码', exact=True)).to_be_focused()
        dialog.get_by_label('密码', exact=True).fill(fixture['reader_password'])
        with test.expect_response(lambda r: r.url.endswith(f"/connections/{test_source['id']}/test")) as response:
            dialog.get_by_role('button', name='测试连接', exact=True).click()
        assert response.value.status == 200
        expect(dialog.get_by_test_id('connection-diagnosis')).to_have_count(0)
        with test.expect_response(lambda r: r.url.endswith(f"/connections/{test_source['id']}") and r.request.method == 'PUT') as response:
            dialog.get_by_role('button', name='保存', exact=True).click()
        assert response.value.status == 200
        expect(dialog).not_to_be_visible()
        expect(test.get_by_role('button', name='更新我的凭据', exact=True)).to_have_count(0)
        passed('credential recovery focuses an empty personal password field and clears stale failures')

        # Publish an unbound future target, keeping both real user bindings on
        # revision 2. The historical revision now needs management in the UI.
        page.goto(frontend+'/connections', wait_until='networkidle')
        page.get_by_role('tab', name='数据源目录', exact=True).click()
        card = page.locator('article').filter(has=page.get_by_role('heading', name=source_name, exact=True))
        card.get_by_role('button', name='管理 '+source_name, exact=True).click()
        page.get_by_role('menuitem', name='新修订', exact=True).click()
        dialog = page.get_by_role('dialog', name='发布数据源新修订')
        dialog.get_by_label('主机', exact=True).fill('inventory-future.example.test')
        dialog.get_by_label('说明', exact=True).fill('目标迁移由使用者确认')
        with page.expect_response(lambda r: r.url.endswith('/revisions') and r.request.method == 'POST') as response:
            dialog.get_by_role('button', name='发布新修订', exact=True).click()
        assert response.value.status == 201
        latest = response.value.json()['data']
        assert latest['revision'] == 3
        card = page.locator('article').filter(has=page.get_by_role('heading', name=source_name, exact=True))
        card.get_by_role('button', name='修订记录', exact=True).click()
        history = page.get_by_role('dialog', name='数据源修订记录')
        expect(history.locator('article')).to_have_count(3)
        old = history.locator('article[data-revision="2"]')
        expect(old.get_by_test_id('binding-count')).to_have_text('绑定 2 个个人连接')
        expect(history.locator('article[data-revision="3"]')).to_contain_text('inventory-future.example.test')
        expect(history.locator('article[data-revision="3"]')).to_contain_text('之前：postgres')
        assert_no_overflow(page)
        expect(history.get_by_role('button', name='下一页', exact=True)).to_be_in_viewport()
        page.screenshot(path=str(out/'source-revisions-desktop.png'), animations='disabled')
        passed('administrators inspect historical bindings and actual target differences across revisions')

        test.goto(frontend+'/connections', wait_until='networkidle')
        test.get_by_role('tab', name='数据源目录', exact=True).click()
        test.locator('article').filter(has=test.get_by_role('heading', name=source_name, exact=True)).get_by_role('button', name='修订记录', exact=True).click()
        personal_history = test.get_by_role('dialog', name='数据源修订记录')
        expect(personal_history.locator('article')).to_have_count(3)
        expect(personal_history.get_by_test_id('binding-count')).to_have_count(0)
        expect(personal_history.get_by_role('button', name='停用此修订', exact=True)).to_have_count(0)
        expect(personal_history.locator('article[data-revision="2"]')).to_contain_text('我的连接正在使用')
        assert_no_overflow(test)
        expect(personal_history.get_by_role('button', name='下一页', exact=True)).to_be_in_viewport()
        test.screenshot(path=str(out/'source-revisions-mobile.png'), animations='disabled')
        passed('mobile users see their pinned revision and differences without management counts or controls')

        mutations = []
        page.on('request', lambda r: mutations.append(r.url) if r.method == 'PATCH' and '/datasource-definitions/' in r.url else None)
        old.get_by_role('button', name='停用此修订', exact=True).click()
        confirmation = page.get_by_role('dialog', name='确认停用影响')
        expect(confirmation).to_contain_text('影响 2 个个人连接')
        with page.expect_response(lambda r: '/datasource-definitions/' in r.url and '/revisions' in r.url and r.request.method == 'GET') as response:
            confirmation.get_by_role('button', name='取消', exact=True).click()
        assert response.value.status == 200
        assert not mutations
        passed('canceling a historical revision stop sends no mutation')

        # A new binding arrives after the impact count was displayed.
        admin_binding = api('POST', f"/datasource-definitions/{new_definition['id']}/connections", {'username': values['POSTGRES_USER'], 'password': values['POSTGRES_PASSWORD'], 'confirmed': True}, 201)
        old.get_by_role('button', name='停用此修订', exact=True).click()
        with page.expect_response(lambda r: r.url.endswith(f"/datasource-definitions/{new_definition['id']}/status") and r.request.method == 'PATCH') as response:
            page.get_by_role('dialog', name='确认停用影响').get_by_role('button', name='确认停用', exact=True).click()
        assert response.value.status == 409
        expect(old.get_by_test_id('binding-count')).to_have_text('绑定 3 个个人连接')
        expect(old.get_by_role('button', name='停用此修订', exact=True)).to_be_visible()
        passed('a changed binding count rejects stale stop confirmation and refreshes the impact')

        old.get_by_role('button', name='停用此修订', exact=True).click()
        confirmation = page.get_by_role('dialog', name='确认停用影响')
        expect(confirmation).to_contain_text('影响 3 个个人连接')
        with page.expect_response(lambda r: r.url.endswith(f"/datasource-definitions/{new_definition['id']}/status") and r.request.method == 'PATCH') as response:
            confirmation.get_by_role('button', name='确认停用', exact=True).click()
        assert response.value.status == 200
        expect(old.get_by_role('button', name='启用此修订', exact=True)).to_be_visible()
        expect(history.locator('article[data-revision="3"]')).to_contain_text('可用')
        api('POST', '/query/execute', {'connection_id': test_source['id'], 'sql': 'SELECT current_user'}, 400, bearer=test_token)
        passed('confirmed stop blocks the historical personal bindings and preserves other revisions')
        with page.expect_response(lambda r: r.url.endswith(f"/datasource-definitions/{new_definition['id']}/status") and r.request.method == 'PATCH') as response:
            old.get_by_role('button', name='启用此修订', exact=True).click()
        assert response.value.status == 200
        result = api('POST', '/query/execute', {'connection_id': test_source['id'], 'sql': 'SELECT current_user'}, bearer=test_token)
        assert result['rows'][0][0] == fixture['reader']
        assert api('GET', f"/connections/{test_source['id']}", bearer=test_token)['definition_revision_id'] == new_definition['id']
        passed('reenabling a historical revision restores the original database identity without rebinding')
        if values.get('DBHUB_AUDIENCE_BROWSER') == '1':
            from audience_browser_checks import run_checks
            run_checks(page, test, fixture, values, prefix, source_name, test_source, readonly_source,
                       test_token, ro_token, latest, new_definition, app_users, out,
                       api, choose, passed, assert_no_overflow)
        browser.close()
    assert not errors, 'browser page errors occurred'
    passed('desktop and mobile complete without browser page errors')
finally:
    for bearer in tokens:
        try:
            api('POST', '/auth/logout', bearer=bearer)
        except Exception:
            pass
    (out/'personal-checks.json').write_text(json.dumps({'checks': checks, 'page_errors': errors}, ensure_ascii=False, indent=2))
