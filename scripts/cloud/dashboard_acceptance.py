"""Isolated dashboard browser acceptance; reuses installed Chromium and images.

Build /runtime/dbhub-dashboard-server and frontend/dist first. All credentials,
schema, reader role, containers, network and audit volume are owned by this run.
No demo resources are left in the development workspace.
"""
import copy
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import time
import urllib.error
import urllib.request
import uuid
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit
from playwright.sync_api import expect, sync_playwright

repo = Path(__file__).resolve().parents[2]
state = Path(os.environ.get('DBHUB_STATE_DIR', '/workspace/dbhub-cloud'))
suffix = secrets.token_hex(5)
schema, reader = 'dashboard_accept_' + suffix, 'dashboard_reader_' + suffix
backend, frontend = 'dbhub-dashboard-backend-' + suffix, 'dbhub-dashboard-frontend-' + suffix
network, volume = 'dbhub-dashboard-net-' + suffix, 'dbhub-dashboard-audit-' + suffix
artifacts = state / 'dashboard-artifacts' / suffix
artifacts.mkdir(parents=True, mode=0o700)
values = dict(line.rstrip('\n').split('=', 1) for line in (state / 'runtime.env').read_text().splitlines() if '=' in line)
admin_name, author_name, viewer_name = 'dashboard_admin_' + suffix, 'dashboard_author_' + suffix, 'dashboard_viewer_' + suffix
password, reader_password = 'Acceptance9!' + secrets.token_hex(16), secrets.token_hex(24)
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    port = sock.getsockname()[1]
origin = f'http://127.0.0.1:{port}'
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
checks, errors, owned = [], [], []
admin_token = ''
report = {'schema': schema, 'reader_role': reader, 'artifacts': str(artifacts), 'completed': False}
env_path = artifacts / 'runtime.env'
fixture = False

def docker(*args, check=True):
    return subprocess.run(['docker', *args], text=True, capture_output=True, check=check)

def pg(sql):
    result = subprocess.run([str(repo / 'scripts/cloud/dev.sh'), 'exec', '-T', 'postgres', 'sh', '-ec', 'psql -X -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'], input=sql, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError('Owned PostgreSQL fixture operation failed: ' + result.stderr[:500])

def api(method, path, body=None, token=None, want=200):
    headers = {'Content-Type': 'application/json'}
    if token is None:
        token = admin_token
    if token:
        headers['Authorization'] = 'Bearer ' + token
    request = urllib.request.Request(origin + '/api/v1' + path, headers=headers, method=method, data=json.dumps(body).encode() if body is not None else None)
    try:
        response = opener.open(request, timeout=60)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        data = json.load(response)
        assert response.status == want, f'{method} {path}: HTTP {response.status}: {data.get("message")}'
        return data.get('data')

def passed(name):
    checks.append(name)
    print('PASS ' + name, flush=True)

def key():
    return str(uuid.uuid4())

def choose(page, label, text):
    combo = page.get_by_role('combobox', name=label, exact=True)
    combo.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")][1]').click()
    page.locator('#' + combo.get_attribute('aria-controls')).get_by_role('option', name=text, exact=True).click()
    page.keyboard.press('Escape')

def login(browser, name, viewport=None):
    context = browser.new_context(viewport=viewport or {'width': 1440, 'height': 1080}, locale='zh-CN', timezone_id='Asia/Shanghai')
    page = context.new_page()
    page.on('pageerror', lambda error: errors.append(str(error)))
    page.goto(origin + '/login')
    page.get_by_label('用户名', exact=True).fill(name)
    page.get_by_label('密码', exact=True).fill(password)
    page.locator('button[type=submit]').click()
    expect(page.get_by_role('combobox', name='当前工作空间')).to_be_visible(timeout=20000)
    return context, page

try:
    assert (state / 'dbhub-dashboard-server').is_file(), 'Build the dashboard backend binary first'
    assert (repo / 'frontend/dist/index.html').is_file(), 'Build frontend/dist first'
    fixture = True
    pg(f"""CREATE SCHEMA {schema};
CREATE ROLE {reader} LOGIN PASSWORD '{reader_password}';
CREATE TABLE {schema}.warehouse_amount(warehouse TEXT,amount NUMERIC(20,2));
INSERT INTO {schema}.warehouse_amount VALUES('杭州',780),('苏州',320),('成都',180);
CREATE VIEW {schema}.warehouse_report AS SELECT warehouse,amount FROM {schema}.warehouse_amount;
CREATE TABLE {schema}.low_stock(sku TEXT,name TEXT,warehouse TEXT,stock INT,safety_stock INT);
INSERT INTO {schema}.low_stock SELECT 'SKU'||lpad(i::text,3,'0'),'物料'||i,CASE WHEN i%3=0 THEN '杭州' WHEN i%3=1 THEN '苏州' ELSE '成都' END,i,i+12 FROM generate_series(1,12) i;
CREATE VIEW {schema}.low_stock_report AS SELECT * FROM {schema}.low_stock;
CREATE VIEW {schema}.trend_report AS SELECT (current_date-6+i)::text AS day,(1100+i*30)::numeric(20,2) amount FROM generate_series(0,6) i;
GRANT USAGE ON SCHEMA {schema} TO {reader};
GRANT SELECT ON {schema}.warehouse_report,{schema}.low_stock_report,{schema}.trend_report TO {reader};""")
    uri = urlsplit(values['DATABASE_URL'])
    query = dict(parse_qsl(uri.query))
    query['options'] = '-csearch_path=' + schema
    private = dict(values)
    private.update(DATABASE_URL=urlunsplit((uri.scheme, uri.netloc, uri.path, urlencode(query), uri.fragment)), APP_ADMIN_USERNAME=admin_name, APP_ADMIN_PASSWORD=password, APP_SECRET_KEY=secrets.token_hex(32), APP_ENV='production', DBHUB_TARGET_ALLOWLIST='postgres:5432', CORS_ALLOWED_ORIGINS=origin, DBHUB_AUDIT_DIR='/var/lib/dbhub/audit')
    descriptor = os.open(env_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'w') as target:
        for name, value in private.items():
            target.write(name + '=' + value + '\n')
    docker('network', 'create', '--label', 'dbhub.task=dashboard-acceptance', network); owned.append(('network', network))
    docker('volume', 'create', '--label', 'dbhub.task=dashboard-acceptance', volume); owned.append(('volume', volume))
    cloud_network = os.environ.get('DBHUB_COMPOSE_PROJECT', 'dbhub-cloud') + '_default'
    owned.append(('container', backend))
    docker('run', '-d', '--name', backend, '--network', cloud_network, '--env-file', str(env_path), '--read-only', '--cap-drop=ALL', '--security-opt', 'no-new-privileges', '--cpus=2', '--memory=512m', '--mount', f'type=bind,src={state}/dbhub-dashboard-server,dst=/app/server,readonly', '--mount', f'type=volume,src={volume},dst=/var/lib/dbhub/audit', 'dbhub-backend:managed-reports')
    docker('network', 'connect', '--alias', 'backend', network, backend)
    owned.append(('container', frontend))
    docker('run', '-d', '--name', frontend, '--network', network, '--publish', f'127.0.0.1:{port}:80', '--mount', f'type=bind,src={repo}/frontend/dist,dst=/usr/share/nginx/html,readonly', '--cpus=1', '--memory=128m', 'dbhub-frontend:managed-reports')
    for _ in range(45):
        try:
            with opener.open(origin + '/api/ready', timeout=2) as response:
                if response.status == 200:
                    break
        except Exception:
            time.sleep(1)
    else:
        raise RuntimeError('Isolated production runtime readiness failed')
    admin_token = api('POST', '/auth/login', {'username': admin_name, 'password': password}, token='')['access_token']
    author = api('POST', '/users', {'username': author_name, 'password': password, 'role': 'developer'}, want=201)
    viewer = api('POST', '/users', {'username': viewer_name, 'password': password, 'role': 'readonly'}, want=201)
    author_token = api('POST', '/auth/login', {'username': author_name, 'password': password}, token='')['access_token']
    viewer_token = api('POST', '/auth/login', {'username': viewer_name, 'password': password}, token='')['access_token']
    source = api('POST', '/connections', {'name': '库存数据源', 'type': 'postgres', 'host': 'postgres', 'port': 5432, 'database': values['POSTGRES_DB'], 'username': values['POSTGRES_USER'], 'password': values['POSTGRES_PASSWORD'], 'ssl_mode': 'disable', 'environment': 'dev'}, want=201)
    definition = api('POST', f'/connections/{source["id"]}/publish', {'visibility': 'global'}, want=201)
    personal = api('POST', f'/datasource-definitions/{definition["id"]}/connections', {'name': '库存开发连接', 'username': values['POSTGRES_USER'], 'password': values['POSTGRES_PASSWORD'], 'confirmed': True}, token=author_token, want=201)
    account = api('POST', '/report-accounts', {'request_id': key(), 'name': '库存只读报表账号', 'definition_revision_id': definition['id'], 'username': reader, 'password': reader_password})
    assert api('GET', '/connections', token=viewer_token)['total'] == 0
    watermark = (dt.datetime.now(dt.timezone.utc).replace(second=0, microsecond=0) - dt.timedelta(minutes=5)).isoformat()
    wm = f",TIMESTAMPTZ '{watermark}' AS __data_as_of"
    specifications = [
        ('库存总额', 'metric', {'metrics': ['amount']}, f'SELECT sum(amount) AS amount{wm} FROM {schema}.warehouse_report', [('amount', 'lossless_numeric')], 'CNY_10000', 'CNY', 'balance', {'kind': 'current_snapshot'}),
        ('低库存SKU', 'metric', {'metrics': ['count']}, f'SELECT count(*) AS count{wm} FROM {schema}.low_stock_report', [('count', 'integer')], 'item_count', '', 'lowstock', {'kind': 'current_snapshot'}),
        ('近七日库存趋势', 'line', {'dimension': 'day', 'metrics': ['amount']}, f'SELECT day,amount{wm} FROM {schema}.trend_report ORDER BY day', [('day', 'string'), ('amount', 'lossless_numeric')], 'CNY_10000', 'CNY', 'trend', {'kind': 'rolling_days', 'days': 7}),
        ('仓库库存分布', 'bar', {'dimension': 'warehouse', 'metrics': ['amount']}, f'SELECT warehouse,amount{wm} FROM {schema}.warehouse_report ORDER BY amount DESC', [('warehouse', 'string'), ('amount', 'lossless_numeric')], 'CNY_10000', 'CNY', 'balance', {'kind': 'current_snapshot'}),
        ('低库存明细', 'table', {}, f'SELECT sku,name,warehouse,stock,safety_stock{wm} FROM {schema}.low_stock_report ORDER BY sku', [('sku', 'string'), ('name', 'string'), ('warehouse', 'string'), ('stock', 'integer'), ('safety_stock', 'integer')], 'item_count', '', 'lowstock_rows', {'kind': 'current_snapshot'}),
    ]
    reports = []
    recipients = [1, author['id'], viewer['id']]
    for index, (name, chart, mapping, sql, columns, unit, currency, comparison, window) in enumerate(specifications):
        contract = {'semantic_key': 'demo_' + str(index), 'meaning': name + '，三个指定仓库可售库存，不含在途', 'result_schema': [{'name': col, 'logical_type': logical, 'nullable': False} for col, logical in columns] + [{'name': '__data_as_of', 'logical_type': 'timestamp_with_timezone', 'nullable': False}], 'dimensions': [mapping['dimension']] if mapping.get('dimension') else [], 'metrics': mapping.get('metrics', []), 'chart_type': chart, 'unit': unit, 'currency': currency, 'timezone': 'Asia/Shanghai', 'scope_key': 'three_warehouses', 'scope_description': '杭州、苏州、成都；可售库存；不含在途；金额按含税采购成本计价', 'time_window': window, 'comparison_key': comparison, 'freshness': {'mode': 'watermark_column', 'column': '__data_as_of', 'max_data_age_seconds': 900, 'max_future_skew_seconds': 60}}
        payload = {'request_id': key(), 'name': name, 'connection_id': personal['id'], 'sql': sql, 'chart_type': chart, 'chart_config': mapping, 'data_contract': contract}
        created = api('POST', '/managed-reports', payload, token=author_token)
        review = api('POST', f'/managed-reports/{created["id"]}/submit', {'request_id': key(), 'version': 1}, token=author_token)
        validation = key()
        api('POST', f'/report-reviews/{review["id"]}/validate', {'request_id': validation, 'account_id': account['id'], 'user_ids': recipients, 'group_ids': []})
        api('POST', f'/report-reviews/{review["id"]}/decision', {'request_id': key(), 'decision': 'approved', 'validation_id': validation, 'contract_confirmed': True})
        api('POST', f'/managed-reports/{created["id"]}/releases', {'request_id': key(), 'expected_publication_no': 0, 'review_id': review['id'], 'kind': 'publish'})
        reports.append({'id': created['id'], 'payload': payload})
    passed('真实 PostgreSQL 五张报表与独立只读账号发布；查看者没有个人连接')

    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path='/usr/bin/chromium', headless=True, args=['--no-sandbox', '--disable-dev-shm-usage'])
        author_context, page = login(browser, author_name)
        page.goto(origin + '/dashboards')
        page.get_by_role('button', name='新建仪表盘', exact=True).click()
        page.get_by_label('仪表盘名称', exact=True).fill('库存运营看板')
        page.get_by_label('仪表盘说明', exact=True).fill('三个仓库的可售库存 · 业务水位与查询时间分别展示')
        choose(page, '仪表盘个人受众', viewer_name)
        for i, spec in enumerate(specifications, 1):
            choose(page, '添加报表卡片', spec[0])
            page.get_by_role('button', name='添加卡片', exact=True).click()
            if i == 3:
                choose(page, f'卡片{i}宽度', '整行')
        page.get_by_label('卡片1比较组', exact=True).fill('库存余额')
        page.get_by_label('卡片4比较组', exact=True).fill('库存余额')
        page.get_by_role('button', name='保存草稿', exact=True).click()
        expect(page.get_by_role('heading', name='库存运营看板', exact=True)).to_be_visible(timeout=15000)
        page.get_by_role('button', name='提交审阅', exact=True).click()
        choose(page, '仪表盘提交审阅人', admin_name)
        page.get_by_role('button', name='提交冻结版本', exact=True).click()
        expect(page.get_by_role('dialog', name='选择独立审阅人')).not_to_be_visible(timeout=15000)
        passed('浏览器开发：添加五张卡片、调整宽度、比较组和受众，冻结版本送审')

        admin_context, review_page = login(browser, admin_name)
        review_page.goto(origin + '/dashboards')
        review_page.get_by_role('button', name='开发与审阅', exact=True).click()
        review_page.get_by_role('button', name='版本、审阅与发布', exact=True).click()
        review_page.get_by_role('button', name='运行冻结版本真实预览', exact=True).click()
        expect(review_page.get_by_text('完整 5/5 张必需卡片', exact=True)).to_be_visible(timeout=30000)
        review_page.get_by_role('button', name='登记本轮全部卡片验证', exact=True).click()
        expect(review_page.get_by_text('验证时间', exact=False)).to_be_visible(timeout=15000)
        review_page.get_by_role('checkbox', name='已核对卡片样本、口径、比较组、时效策略和全部受众覆盖').check()
        review_page.get_by_role('button', name='批准此版本', exact=True).click()
        expect(review_page.get_by_role('button', name='显式发布此版本', exact=True)).to_be_visible(timeout=15000)
        review_page.get_by_role('button', name='显式发布此版本', exact=True).click()
        review_page.get_by_role('button', name='确定', exact=True).click()
        expect(review_page.get_by_text('发布 #1', exact=False)).to_be_visible(timeout=15000)
        passed('浏览器审阅：冻结版本全部真实预览、验证、批准与显式发布')

        viewer_context, view_page = login(browser, viewer_name)
        view_page.goto(origin + '/dashboards')
        view_page.get_by_role('button', name='库存运营看板', exact=False).click()
        expect(view_page.get_by_text('完整 5/5 张必需卡片', exact=True)).to_be_visible(timeout=30000)
        expect(view_page.get_by_test_id('report-metric').first).to_have_text('1280.00')
        expect(view_page.get_by_test_id('report-metric').nth(1)).to_have_text('12')
        assert view_page.locator('.dashboard-grid').get_by_text('数据截止', exact=False).count() == 5
        assert view_page.get_by_role('button', name='导出', exact=True).count() == 0
        assert view_page.get_by_role('button', name='自动刷新', exact=True).count() == 0
        # Chromium may deliver Playwright requestfinished notifications after a
        # subsequent request event. Measure overlap using browser timestamps.
        view_page.wait_for_function('performance.getEntriesByType("resource").filter(r=>r.name.includes("/tiles/")&&r.name.endsWith("/execute")).length===5')
        timings = view_page.evaluate('performance.getEntriesByType("resource").filter(r=>r.name.includes("/tiles/")&&r.name.endsWith("/execute")).map(r=>[r.startTime,r.responseEnd])')
        peak = max(sum(start <= point < end for start, end in timings) for point, _ in timings)
        assert 1 <= peak <= 2, {'peak': peak, 'timings': timings}
        assert view_page.evaluate('document.documentElement.scrollWidth<=innerWidth+1')
        view_page.screenshot(path=str(artifacts / 'dashboard-desktop.png'), full_page=True)
        passed('正式查看：真实1280.00与12，业务水位独立展示，无个人凭据或额外按钮')
        view_page.set_viewport_size({'width': 390, 'height': 844})
        view_page.wait_for_function('document.querySelector("aside[aria-label=主导航]").getBoundingClientRect().right<=1')
        assert view_page.evaluate('document.documentElement.scrollWidth<=innerWidth+1'), 'mobile horizontal overflow'
        widths = view_page.locator('.dashboard-grid>article').evaluate_all('(nodes)=>nodes.map(n=>Math.round(n.getBoundingClientRect().width))')
        assert max(widths) - min(widths) <= 1
        view_page.screenshot(path=str(artifacts / 'dashboard-mobile.png'), full_page=True)
        passed('手机390px单列布局、无横向溢出，图表保留可读结果表')

        full_query = view_page.get_by_text('最近完整查询：', exact=False).inner_text()
        view_page.get_by_role('button', name='重新查询', exact=True).first.click()
        expect(view_page.get_by_text('部分刷新 5/5 张必需卡片', exact=True)).to_be_visible(timeout=15000)
        expect(view_page.get_by_role('button', name='刷新全部', exact=True)).to_be_enabled(timeout=15000)
        assert view_page.get_by_text('最近完整查询：', exact=False).inner_text() == full_query
        passed('页面同时查询最多两张；单卡重查保持部分刷新，不改写最近完整查询时间')

        # Transport failure is intentionally mocked; data and authorization remain real.
        view_page.route('**/dashboard-refreshes/*/tiles/*/execute', lambda route: route.fulfill(status=503, content_type='application/json', body='{"code":50300,"message":"验收模拟网络故障"}'))
        view_page.get_by_role('button', name='刷新全部', exact=True).click()
        expect(view_page.get_by_text('上次成功结果', exact=False).first).to_be_visible(timeout=15000)
        assert view_page.get_by_test_id('report-metric').first.inner_text() == '1280.00'
        assert '已过期' in view_page.get_by_label('仪表盘查看').inner_text()
        expect(view_page.get_by_text('部分可用 0/5 张必需卡片', exact=True)).to_be_visible(timeout=15000)
        view_page.unroute('**/dashboard-refreshes/*/tiles/*/execute')
        passed('模拟503仅保留带旧时间标记的内存结果，不显示为新鲜数据')
        viewer_context.set_offline(True)
        view_page.get_by_role('button', name='刷新全部', exact=True).click()
        expect(view_page.get_by_text('页面已暂停查询；返回页面或恢复网络后，请手动刷新。', exact=True)).to_be_visible()
        viewer_context.set_offline(False)
        passed('离线时不发起查询，恢复网络后要求手动刷新')
        view_page.clock.install()
        count = {'executions': 0}
        view_page.on('request', lambda request: count.update(executions=count['executions'] + 1) if '/tiles/' in request.url and request.method == 'POST' else None)
        view_page.clock.fast_forward(31 * 60 * 1000)
        expect(view_page.get_by_text('结果保留期已满，请重新查询', exact=True).first).to_be_visible(timeout=5000)
        assert view_page.get_by_test_id('report-metric').count() == 0
        assert count['executions'] == 0
        passed('计时推进31分钟：清空过期结果，期间没有自动执行查询')

        # A second workspace contains no copied sources or dashboard data.
        space = api('POST', '/workspaces', {'name': '隔离验收空间','request_id':key()})
        api('POST', f'/workspaces/{space["id"]}/members', {'username': viewer_name, 'role': 'readonly'})
        view_page.reload()
        choose(view_page, '当前工作空间', '隔离验收空间')
        expect(view_page.get_by_text('暂无获授权的正式仪表盘。', exact=False)).to_be_visible(timeout=15000)
        assert view_page.get_by_test_id('report-metric').count() == 0
        assert api('GET', f'/workspaces/{space["id"]}/connections', token=viewer_token)['total'] == 0
        api('GET', f'/workspaces/{space["id"]}/published-reports/{reports[0]["id"]}', token=viewer_token, want=404)
        passed('工作空间切换销毁旧结果，跨空间已知报表ID与数据源读取被拒绝')
        assert not errors, errors
        browser.close()
    report.update(completed=True, checks=checks, errors=errors, server_sha256=hashlib.sha256((state/'dbhub-dashboard-server').read_bytes()).hexdigest(), runtime_readonly=True, chromium='system installed', source='current workspace build')
finally:
    for kind, name in reversed(owned):
        if kind == 'container':
            if name == backend:
                logs = docker('logs', name, check=False)
                (artifacts/'backend.log').write_text(logs.stdout + logs.stderr)
            docker('stop', '-t', '15', name, check=False)
            docker('rm', '-f', name, check=False)
        elif kind == 'volume':
            docker('volume', 'rm', name, check=False)
        else:
            docker('network', 'rm', name, check=False)
    if fixture:
        pg(f'DROP SCHEMA IF EXISTS {schema} CASCADE; DROP ROLE IF EXISTS {reader};')
    env_path.unlink(missing_ok=True)
    report.update(checks=checks, errors=errors, resources_removed=all(docker(kind, 'inspect', name, check=False).returncode != 0 for kind,name in owned))
    (artifacts/'checks.json').write_text(json.dumps(report, ensure_ascii=False, indent=2))
    print(json.dumps({'completed': report['completed'], 'checks': len(checks), 'artifacts': str(artifacts), 'resources_removed': report['resources_removed']}, ensure_ascii=False), flush=True)
