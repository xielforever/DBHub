"""First-phase reporting: real browser/API paths; no mocked business responses."""
import json
import os
import secrets
import sys
import traceback
import urllib.error
import urllib.request
import uuid
from urllib.parse import urlsplit
from pathlib import Path
from playwright.sync_api import expect, sync_playwright

base = os.environ.get('DBHUB_REPORT_API_BASE', 'http://backend:8080/api/v1')
frontend = os.environ.get('DBHUB_REPORT_FRONTEND', 'http://frontend:5173')
local_hosts = ','.join(filter(None, [urlsplit(base).hostname, urlsplit(frontend).hostname, 'localhost', '127.0.0.1']))
os.environ['NO_PROXY'] = os.environ.get('NO_PROXY', '') + ',' + local_hosts
os.environ['no_proxy'] = os.environ['NO_PROXY']
out = Path('/runtime/browser-artifacts')
out.mkdir(exist_ok=True)
suffix = secrets.token_hex(6)
fixture_name, viewer_name = 'report_ui_' + suffix, 'report_viewer_' + suffix
password = 'Fixture9!' + secrets.token_urlsafe(24)
bootstrap = token = viewer_token = ''
sessions, users, connections, reports, checks, errors = [], [], [], [], [], []
code = 0

def api(method, path, payload=None, want=200, bearer=None):
    if method == 'POST' and path == '/reports' and payload is not None:
        payload = {'request_id': str(uuid.uuid4()), **payload}
    selected = token if bearer is None else bearer
    headers = {'Content-Type': 'application/json'}
    if selected: headers['Authorization'] = 'Bearer ' + selected
    request = urllib.request.Request(base + path, method=method, headers=headers,
        data=json.dumps(payload).encode() if payload is not None else None)
    try: response = urllib.request.urlopen(request, timeout=45)
    except urllib.error.HTTPError as error: response = error
    with response:
        assert response.status == want, f'{method} {path}: HTTP {response.status}'
        return json.load(response).get('data')

def passed(name):
    checks.append(name)
    print('PASS ' + name, flush=True)

def choose(page, label, option):
    combo = page.get_by_role('combobox', name=label, exact=True)
    combo.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")][1]').click()
    listbox = page.locator('#' + combo.get_attribute('aria-controls'))
    listbox.get_by_role('option', name=option, exact=True).click()
    if combo.get_attribute('aria-expanded') == 'true':
        combo.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")][1]').click()
    expect(listbox).not_to_be_visible()

def login(page, name):
    page.goto(frontend + '/login', wait_until='networkidle')
    page.locator('#username').fill(name)
    page.locator('#password').fill(password)
    with page.expect_response(lambda response: response.url.endswith('/auth/login')) as response:
        page.locator('button[type=submit]').click()
    assert response.value.status == 200
    sessions.append(response.value.json()['data']['access_token'])
    page.wait_for_url('**/dashboard')
    page.goto(frontend + '/reports', wait_until='networkidle')

try:
    bootstrap = api('POST', '/auth/login', {'username': os.environ['APP_ADMIN_USERNAME'], 'password': os.environ['APP_ADMIN_PASSWORD']}, bearer='')['access_token']
    sessions.append(bootstrap)
    for name, role in [(fixture_name, 'admin'), (viewer_name, 'readonly')]:
        users.append(api('POST', '/users', {'username': name, 'password': password, 'role': role}, 201, bearer=bootstrap)['id'])
    token = api('POST', '/auth/login', {'username': fixture_name, 'password': password}, bearer='')['access_token']; sessions.append(token)
    viewer_token = api('POST', '/auth/login', {'username': viewer_name, 'password': password}, bearer='')['access_token']; sessions.append(viewer_token)
    definition = api('POST', '/datasource-definitions', {'name': 'report target ' + suffix, 'visibility': 'global', 'config': {'type': 'postgres', 'host': 'postgres', 'port': 5432, 'database': os.environ['POSTGRES_DB'], 'ssl_mode': 'disable', 'connection_timeout': 5, 'environment': 'test'}}, 201)
    binding = {'name': 'report connection ' + suffix, 'username': os.environ['POSTGRES_USER'], 'password': os.environ['POSTGRES_PASSWORD'], 'confirmed': True}
    conn = api('POST', f'/datasource-definitions/{definition["id"]}/connections', binding, 201); connections.append((conn['id'], token))
    viewer_conn = api('POST', f'/datasource-definitions/{definition["id"]}/connections', binding, 201, bearer=viewer_token); connections.append((viewer_conn['id'], viewer_token))
    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path='/usr/bin/chromium', proxy={'server': os.environ.get('HTTPS_PROXY', 'http://proxy:8080'), 'bypass': local_hosts}, args=['--no-sandbox', '--disable-dev-shm-usage'])
        page = browser.new_page(viewport={'width': 1440, 'height': 1000})
        page.on('pageerror', lambda error: errors.append(str(error)))
        execute_paths = []
        page.on('request', lambda request: execute_paths.append(request.url) if request.method == 'POST' and ('/execute' in request.url or '/preview' in request.url) else None)
        login(page, fixture_name)
        expect(page.get_by_role('link', name='仪表盘', exact=True)).to_have_count(0)
        page.get_by_role('button', name='新建报表', exact=True).click()
        dialog = page.get_by_role('dialog', name='新建报表', exact=True)
        report_name = '库存统计 ' + suffix
        dialog.get_by_role('textbox', name='报表名称', exact=True).fill(report_name)
        choose(page, '报表个人连接', binding['name'])
        choose(page, '报表可见性', '登录后共享')
        dialog.get_by_role('textbox', name='报表SQL', exact=True).fill("SELECT 'b' AS category, 12.34::numeric AS amount UNION ALL SELECT 'a', 8.50::numeric")
        with page.expect_response(lambda response: response.url.endswith('/reports/preview')) as preview:
            dialog.get_by_role('button', name='运行预览', exact=True).click()
        assert preview.value.status == 200
        expect(dialog.get_by_label('报表查询结果')).to_contain_text('返回 2 行')
        choose(page, '报表展示方式', '柱状图')
        choose(page, '报表维度', 'category')
        choose(page, '报表指标', 'amount')
        with page.expect_response(lambda response: response.request.method == 'POST' and response.url.endswith('/reports')) as saved:
            dialog.get_by_role('button', name='保存报表', exact=True).click()
        assert saved.value.status == 200
        report = saved.value.json()['data']; reports.append(report['id'])
        expect(dialog).not_to_be_visible()
        card = page.get_by_role('article', name='报表：' + report_name, exact=True)
        expect(card).to_be_visible()
        passed('independent creation previews a real read-only query and saves a shared chart')
        with page.expect_response(lambda response: response.url.endswith(f'/reports/{report["id"]}/execute')) as result:
            card.get_by_role('button', name='查看', exact=True).click()
        assert result.value.status == 200
        detail = page.get_by_role('dialog', name=report_name, exact=True)
        expect(detail.get_by_label('报表查询结果')).to_contain_text('返回 2 行')
        expect(detail.locator('canvas')).to_be_visible()
        detail.get_by_text('查看查询结果', exact=True).click()
        expect(detail.get_by_role('region', name='SQL 查询结果表格')).to_contain_text('12.34')
        page.screenshot(path=str(out / 'report-phase1-desktop.png'), full_page=True)
        detail.locator('button.el-dialog__headerbtn').click()
        passed('view executes the saved definition through the dedicated report route and exposes chart source rows')

        card.get_by_role('button', name='编辑', exact=True).click()
        editor = page.get_by_role('dialog', name='编辑报表', exact=True)
        editor.get_by_role('button', name='运行预览', exact=True).click()
        expect(editor.get_by_label('报表查询结果')).to_contain_text('返回 2 行')
        updated = api('PUT', f'/reports/{report["id"]}', {'version': report['version'], 'description': 'concurrent real update'})
        editor.get_by_role('textbox', name='报表描述', exact=True).fill('stale browser update')
        with page.expect_response(lambda response: response.request.method == 'PUT' and response.url.endswith(f'/reports/{report["id"]}')) as conflict:
            editor.get_by_role('button', name='保存报表', exact=True).click()
        assert conflict.value.status == 409
        expect(editor.get_by_role('alert')).to_contain_text('版本')
        editor.get_by_role('button', name='取消', exact=True).click()
        assert api('GET', f'/reports/{report["id"]}')['description'] == 'concurrent real update'
        passed('concurrent edits produce a visible conflict without overwriting the saved definition')

        for kind, config, sql in [
            ('line', {'dimension': 'category', 'metrics': ['amount']}, "SELECT 'b' AS category, 12.34::numeric AS amount UNION ALL SELECT 'a', 8.50::numeric"),
            ('pie', {'dimension': 'category', 'metrics': ['amount']}, "SELECT 'b' AS category, 12.34::numeric AS amount UNION ALL SELECT 'a', 8.50::numeric"),
            ('metric', {'metrics': ['total']}, 'SELECT 42::bigint AS total'),
        ]:
            item = api('POST', '/reports', {'name': kind + ' ' + suffix, 'connection_id': conn['id'], 'sql': sql, 'chart_type': kind, 'chart_config': config})
            reports.append(item['id'])
            page.get_by_role('button', name='刷新', exact=True).click()
            page.get_by_role('article', name='报表：' + item['name'], exact=True).get_by_role('button', name='查看', exact=True).click()
            chart_dialog = page.get_by_role('dialog', name=item['name'], exact=True)
            expect(chart_dialog.get_by_label('报表查询结果')).to_be_visible()
            if kind == 'metric': expect(chart_dialog.get_by_test_id('report-metric')).to_have_text('42')
            else: expect(chart_dialog.locator('canvas')).to_be_visible()
            chart_dialog.locator('button.el-dialog__headerbtn').click()
        passed('line, pie and metric charts render complete real query results without client aggregation')

        precise = api('POST', '/reports', {'name': '数值精度 ' + suffix, 'connection_id': conn['id'], 'chart_type': 'table', 'sql': "SELECT 9007199254740993::bigint AS big, 12345678901234567890.123456789::numeric AS amount, '{\"id\":9007199254740993}'::jsonb AS document"})
        reports.append(precise['id'])
        page.get_by_role('button', name='刷新', exact=True).click()
        page.get_by_role('article', name='报表：' + precise['name'], exact=True).get_by_role('button', name='查看', exact=True).click()
        precise_dialog = page.get_by_role('dialog', name=precise['name'], exact=True)
        cells = precise_dialog.get_by_role('region', name='SQL 查询结果表格').locator('tbody tr.data-row').first.locator('td')
        expect(cells.nth(0)).to_have_text('9007199254740993')
        expect(cells.nth(1)).to_have_text('12345678901234567890.123456789')
        expect(cells.nth(2)).to_contain_text('9007199254740993')
        precise_dialog.locator('button.el-dialog__headerbtn').click()
        passed('large integers, high precision decimals and JSON cell numbers retain their exact values')

        partial = api('POST', '/reports', {'name': '截断结果 ' + suffix, 'connection_id': conn['id'], 'sql': 'SELECT generate_series(1,1001) AS id', 'chart_type': 'table'})
        reports.append(partial['id'])
        page.get_by_role('button', name='刷新', exact=True).click()
        part_card = page.get_by_role('article', name='报表：' + partial['name'], exact=True)
        part_card.get_by_role('button', name='查看', exact=True).click()
        partial_dialog = page.get_by_role('dialog', name=partial['name'], exact=True)
        expect(partial_dialog.get_by_role('alert')).to_contain_text('结果已截断')
        grid = partial_dialog.get_by_role('region', name='SQL 查询结果表格')
        grid.evaluate('(element) => { element.scrollTop = element.scrollHeight }')
        expect(grid).to_contain_text('1000')
        page.set_viewport_size({'width': 390, 'height': 844})
        expect(partial_dialog).to_be_visible()
        assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth')
        page.screenshot(path=str(out / 'report-phase1-mobile.png'), full_page=True)
        partial_dialog.locator('button.el-dialog__headerbtn').click()
        passed('truncation is explicit, row 1000 remains reachable, and the mobile page fits the viewport')

        bad = api('POST', '/reports', {'name': '执行错误 ' + suffix, 'connection_id': conn['id'], 'sql': 'SELECT * FROM missing_report_browser_table', 'chart_type': 'table'})
        reports.append(bad['id'])
        page.get_by_role('button', name='刷新', exact=True).click()
        page.get_by_role('article', name='报表：' + bad['name'], exact=True).get_by_role('button', name='查看', exact=True).click()
        failure = page.get_by_role('dialog', name=bad['name'], exact=True)
        expect(failure.get_by_role('alert')).to_contain_text('执行失败')
        expect(failure.get_by_label('报表查询结果')).to_have_count(0)
        failure.locator('button.el-dialog__headerbtn').click()
        passed('a real SQL error is visible and does not masquerade as an empty or old result')
        assert not any('/query/execute' in path for path in execute_paths), execute_paths

        viewer_page = browser.new_page(viewport={'width': 1440, 'height': 1000})
        viewer_page.on('pageerror', lambda error: errors.append(str(error)))
        login(viewer_page, viewer_name)
        expect(viewer_page.get_by_role('button', name='新建报表', exact=True)).to_have_count(0)
        shared_card = viewer_page.get_by_role('article', name='报表：' + report_name, exact=True)
        expect(shared_card.get_by_role('button', name='编辑', exact=True)).to_have_count(0)
        shared_card.get_by_role('button', name='收藏', exact=True).click()
        expect(shared_card.get_by_role('button', name='取消收藏', exact=True)).to_be_visible()
        api('DELETE', f'/connections/{conn["id"]}'); connections.remove((conn['id'], token))
        with viewer_page.expect_response(lambda response: response.url.endswith(f'/reports/{report["id"]}/execute')) as viewed:
            shared_card.get_by_role('button', name='查看', exact=True).click()
        assert viewed.value.status == 200
        expect(viewer_page.get_by_role('dialog', name=report_name).get_by_label('报表查询结果')).to_contain_text('返回 2 行')
        assert api('GET', f'/reports/{report["id"]}', bearer=viewer_token)['connection_id'] == viewer_conn['id']
        passed('readonly viewer uses a personal binding and keeps access after the publisher deletes their connection')
        assert api('GET', f'/reports/{report["id"]}')['connection_id'] == 0
        api('POST', f'/reports/{report["id"]}/execute', {'version': updated['version'], 'request_id': str(uuid.uuid4())}, 409)
        api('POST', '/shares', {'subject_type': 'report', 'subject_id': report['id']}, 403)
        passed('unbound owner cannot borrow viewer credentials and anonymous sharing remains disabled')
        assert errors == [], errors
        passed('all real browser paths finish without page script errors')
        browser.close()
except Exception:
    code = 1
    detail = traceback.format_exc()
    for value in [password, os.environ.get('APP_ADMIN_PASSWORD'), os.environ.get('POSTGRES_PASSWORD'), *sessions]:
        if value: detail = detail.replace(value, '[redacted]')
    print(detail, file=sys.stderr)
finally:
    for report_id in reports:
        try: api('DELETE', f'/reports/{report_id}')
        except Exception: code = 1; print('owned report cleanup failed', file=sys.stderr)
    for conn_id, bearer in connections:
        try: api('DELETE', f'/connections/{conn_id}', bearer=bearer)
        except Exception: code = 1; print('owned report binding cleanup failed', file=sys.stderr)
    for bearer in [value for value in sessions if value != bootstrap]:
        try: api('POST', '/auth/logout', bearer=bearer)
        except Exception: code = 1; print('owned session cleanup failed', file=sys.stderr)
    for user_id in reversed(users):
        try: api('DELETE', f'/users/{user_id}', bearer=bootstrap)
        except Exception: code = 1; print('owned fixture user cleanup failed', file=sys.stderr)
    if bootstrap:
        try: api('POST', '/auth/logout', bearer=bootstrap)
        except Exception: code = 1
    (out / 'report-phase1-checks.json').write_text(json.dumps({'checks': checks, 'page_errors': errors, 'mocked_checks': [], 'cleanup_succeeded': code == 0}, ensure_ascii=False, indent=2))
sys.exit(code)
