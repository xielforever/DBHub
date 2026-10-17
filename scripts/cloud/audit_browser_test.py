"""Real audit list/filter checks plus explicitly mocked queue-state rendering."""
import json
import os
import secrets
import sys
import traceback
import urllib.error
import urllib.request
import uuid
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

base = 'http://backend:8080/api/v1'
frontend = 'http://frontend:5173'
out = Path('/runtime/browser-artifacts')
out.mkdir(exist_ok=True)
token = ''
sessions = []
connection_id = None
fixture_user_id = None
bootstrap_token = ''
fixture_name = 'audit_ui_' + secrets.token_hex(6)
fixture_password = 'Fixture9!' + secrets.token_urlsafe(24)
checks, errors = [], []
code = 0


def api(method, path, payload=None, want=200, bearer=None):
    headers = {'Content-Type': 'application/json'}
    selected = token if bearer is None else bearer
    if selected:
        headers['Authorization'] = 'Bearer ' + selected
    request = urllib.request.Request(base + path, method=method, headers=headers,
        data=json.dumps(payload).encode() if payload is not None else None)
    try:
        response = urllib.request.urlopen(request, timeout=40)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        assert response.status == want, f'{method} {path}: HTTP {response.status}'
        return json.load(response).get('data')


def passed(name):
    checks.append(name)
    print('PASS ' + name, flush=True)


def choose(page, label, option):
    combo = page.get_by_role('combobox', name=label, exact=True)
    combo.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")][1]').click()
    page.get_by_role('option', name=option, exact=True).click()


try:
    token = api('POST', '/auth/login', {'username': os.environ['APP_ADMIN_USERNAME'],
        'password': os.environ['APP_ADMIN_PASSWORD']})['access_token']
    sessions.append(token)
    bootstrap_token = token
    fixture_user_id = api('POST', '/users', {'username': fixture_name,
        'password': fixture_password, 'role': 'admin'}, 201)['id']
    token = api('POST', '/auth/login', {'username': fixture_name, 'password': fixture_password})['access_token']
    sessions.append(token)
    connection = api('POST', '/connections', {
        'name': fixture_name, 'type': 'postgres', 'host': 'postgres', 'port': 5432,
        'database': os.environ['POSTGRES_DB'], 'username': os.environ['POSTGRES_USER'],
        'password': os.environ['POSTGRES_PASSWORD'], 'ssl_mode': 'disable',
        'connection_timeout': 5, 'environment': 'dev'}, 201)
    connection_id = connection['id']
    api('POST', '/query/execute', {'connection_id': connection_id, 'request_id': str(uuid.uuid4()), 'sql': 'SELECT 1'})
    tx = api('POST', '/query/transaction/begin', {'connection_id': connection_id})['transaction_id']
    api('POST', '/query/transaction/commit', {'connection_id': connection_id, 'transaction_id': tx})
    api('GET', '/users', want=401, bearer='')
    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path='/usr/bin/chromium',
            proxy={'server': os.environ.get('HTTPS_PROXY', 'http://proxy:8080'), 'bypass': 'frontend,backend,localhost,127.0.0.1'},
            args=['--no-sandbox', '--disable-dev-shm-usage'])
        page = browser.new_page(viewport={'width': 1440, 'height': 1000})
        page.on('pageerror', lambda error: errors.append(str(error)))
        page.goto(frontend + '/login', wait_until='networkidle')
        page.locator('#username').fill(fixture_name)
        page.locator('#password').fill(fixture_password)
        with page.expect_response(lambda r: r.url.endswith('/auth/login')) as login:
            page.locator('button[type=submit]').click()
        assert login.value.status == 200
        sessions.append(login.value.json()['data']['access_token'])
        page.wait_for_url('**/dashboard')
        page.goto(frontend + '/audit', wait_until='networkidle')
        expect(page.get_by_label('主内容区（可滚动）').get_by_role('heading', name='操作审计', exact=True)).to_be_visible()
        expect(page.get_by_role('status').filter(has_text='审计写入正常')).to_be_visible()
        passed('administrator reads the real delivery status and audit list')

        choose(page, '按操作类型筛选', '提交事务')
        row = page.locator('tbody tr').filter(has_text='提交事务').first
        expect(row).to_contain_text('事务已提交')
        row.get_by_text('请求详情', exact=True).click()
        expect(row).to_contain_text(tx)
        expect(row).to_contain_text('POST /api/v1/query/transaction/commit')
        expect(row).to_contain_text('HTTP 200')
        page.screenshot(path=str(out / 'audit-reliable-desktop.png'), animations='disabled')
        passed('transaction filtering shows real session identity event ID route and outcome')

        choose(page, '按操作类型筛选', '访问拒绝')
        row = page.locator('tbody tr').filter(has_text='访问拒绝').first
        expect(row).to_contain_text('请求拒绝')
        row.get_by_text('请求详情', exact=True).click()
        expect(row).to_contain_text('HTTP 401')
        expect(row).to_contain_text('缺少认证令牌')
        passed('denied read requests are visible with safe authentication reasons')

        page.set_viewport_size({'width': 390, 'height': 844})
        assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth + 1')
        page.screenshot(path=str(out / 'audit-reliable-mobile.png'), animations='disabled')
        passed('audit status filters and details fit a 390 pixel mobile viewport')

        # These responses exercise UI states only; backend outages and recovery
        # are verified against PostgreSQL by audit_integration_test.go.
        delivery_url = '**/api/v1/audit-logs/delivery'
        fixture = {'ready': True, 'pending': 2, 'capacity': 10, 'delivery_failures': 3, 'storage_failures': 0,
            'last_error': 'database_write_failed'}
        page.route(delivery_url, lambda route: route.fulfill(json={'code': 0, 'message': 'ok', 'data': fixture}))
        page.get_by_role('button', name='刷新', exact=True).click()
        expect(page.get_by_role('status').filter(has_text='审计记录已保存，正在等待落库。')).to_be_visible()
        expect(page.get_by_role('status').filter(has_text='待写入 2 / 10')).to_be_visible()
        passed('queue fixture displays retained events waiting for delivery')
        fixture.update(ready=False, pending=10)
        page.get_by_role('button', name='刷新', exact=True).click()
        expect(page.get_by_role('status').filter(has_text='新的审计操作已暂停')).to_be_visible()
        passed('full queue fixture explains admission has paused')
        page.unroute(delivery_url)
        page.route(delivery_url, lambda route: route.fulfill(status=503, json={'code': 50300, 'message': '状态暂不可用'}))
        page.get_by_role('button', name='刷新', exact=True).click()
        expect(page.get_by_role('status').filter(has_text='暂时无法读取审计写入状态')).to_be_visible()
        passed('failed status polling does not leave a stale healthy indicator')
        page.unroute(delivery_url)
        page.get_by_role('button', name='刷新', exact=True).click()
        expect(page.get_by_role('status').filter(has_text='审计写入正常')).to_be_visible()
        assert errors == [], errors
        passed('real delivery status recovers without browser page errors')
        browser.close()
except Exception:
    code = 1
    detail = traceback.format_exc()
    for value in [os.environ.get('APP_ADMIN_PASSWORD'), os.environ.get('POSTGRES_PASSWORD'), fixture_password, *sessions]:
        if value:
            detail = detail.replace(value, '[redacted]')
    print(detail, file=sys.stderr)
finally:
    if connection_id is not None:
        try:
            api('DELETE', f'/connections/{connection_id}')
        except Exception:
            code = 1
            print('owned browser connection cleanup failed', file=sys.stderr)
    for bearer in [value for value in sessions if value != bootstrap_token]:
        try:
            api('POST', '/auth/logout', bearer=bearer)
        except Exception:
            code = 1
            print('owned browser session cleanup failed', file=sys.stderr)
    if fixture_user_id is not None:
        try:
            api('DELETE', f'/users/{fixture_user_id}', bearer=bootstrap_token)
        except Exception:
            code = 1
            print('owned browser user and source cleanup failed', file=sys.stderr)
    if bootstrap_token:
        try:
            api('POST', '/auth/logout', bearer=bootstrap_token)
        except Exception:
            code = 1
    (out / 'audit-reliable-checks.json').write_text(json.dumps({'checks': checks, 'page_errors': errors,
        'mocked_checks': checks[4:7], 'owned_connection_removed': connection_id is not None and code == 0,
        'owned_user_and_sources_removed': fixture_user_id is not None and code == 0}, ensure_ascii=False, indent=2))
sys.exit(code)
