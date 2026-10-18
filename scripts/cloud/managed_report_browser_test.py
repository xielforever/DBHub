"""Formal report workflow against real services and production images."""
import json
import os
import sys
import traceback
import urllib.error
import urllib.request
import uuid
from pathlib import Path
from playwright.sync_api import expect, sync_playwright

base = os.environ.get('DBHUB_REPORT_API_BASE', 'http://backend:8080/api/v1')
frontend = os.environ.get('DBHUB_REPORT_FRONTEND', 'http://frontend')
# Only the owned Docker/loopback service names bypass the Internet proxy.
os.environ['NO_PROXY'] = os.environ.get('NO_PROXY', '') + ',backend,frontend,localhost,127.0.0.1'
os.environ['no_proxy'] = os.environ['NO_PROXY']
out = Path('/runtime')
checks, errors, sessions = [], [], []
admin_token = ''
password = os.environ['REPORT_ACCEPTANCE_PASSWORD']
schema = os.environ['REPORT_ACCEPTANCE_SCHEMA']
service_user = os.environ['REPORT_ACCEPTANCE_DB_USER']
service_password = os.environ['REPORT_ACCEPTANCE_DB_PASSWORD']
admin_name = os.environ['APP_ADMIN_USERNAME']
fixture_suffix = os.environ.get('REPORT_ACCEPTANCE_SUFFIX', '')
developer_name, viewer_name = 'report_author' + fixture_suffix, 'report_viewer' + fixture_suffix
source_name = '库存数据源' + fixture_suffix
account_name = '库存受管只读账号' + fixture_suffix
report_name = '库存正式报表' + fixture_suffix
personal_name = '个人库存草稿' + fixture_suffix
code = 0

def api(method, path, payload=None, bearer=None, want=200):
    headers = {'Content-Type': 'application/json'}
    selected = admin_token if bearer is None else bearer
    if selected: headers['Authorization'] = 'Bearer ' + selected
    req = urllib.request.Request(base + path, method=method, headers=headers,
        data=json.dumps(payload).encode() if payload is not None else None)
    try: response = urllib.request.urlopen(req, timeout=45)
    except urllib.error.HTTPError as error: response = error
    with response:
        body = json.load(response)
        assert response.status == want, f'{method} {path}: HTTP {response.status}: {body.get("message")}'
        return body.get('data')

def passed(name):
    checks.append(name)
    print('PASS ' + name, flush=True)

def choose(page, label, option):
    combo = page.get_by_role('combobox', name=label, exact=True)
    wrapper = combo.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")][1]')
    wrapper.click()
    listbox = page.locator('#' + combo.get_attribute('aria-controls'))
    listbox.get_by_role('option', name=option, exact=True).click()
    if combo.get_attribute('aria-expanded') == 'true':
        dialogs = page.get_by_role('dialog')
        if dialogs.count(): dialogs.locator('.el-dialog__header').click()
        else: page.get_by_role('heading', name='报表', exact=True).click()
    expect(listbox).not_to_be_visible()

def login(browser, name):
    context = browser.new_context(viewport={'width': 1440, 'height': 1000})
    page = context.new_page()
    page.on('pageerror', lambda error: errors.append(str(error)))
    page.goto(frontend + '/login', wait_until='networkidle')
    page.locator('#username').fill(name)
    page.locator('#password').fill(password)
    with page.expect_response(lambda response: response.url.endswith('/auth/login')) as reply:
        page.locator('button[type=submit]').click()
    assert reply.value.status == 200
    sessions.append(reply.value.json()['data']['access_token'])
    page.wait_for_url('**/dashboard')
    page.goto(frontend + '/reports', wait_until='networkidle')
    return page

def confirm(page):
    page.get_by_role('dialog').filter(has=page.locator('.el-message-box__btns')).get_by_role('button', name='确认', exact=True).click()

try:
    admin_token = api('POST', '/auth/login', {'username': admin_name, 'password': password}, bearer='')['access_token']
    sessions.append(admin_token)
    developer = api('POST', '/users', {'username': developer_name, 'password': password, 'role': 'developer'}, want=201)
    viewer = api('POST', '/users', {'username': viewer_name, 'password': password, 'role': 'readonly'}, want=201)
    developer_token = api('POST', '/auth/login', {'username': developer_name, 'password': password}, bearer='')['access_token']
    viewer_token = api('POST', '/auth/login', {'username': viewer_name, 'password': password}, bearer='')['access_token']
    sessions.extend([developer_token, viewer_token])
    source = api('POST', '/datasource-definitions', {'name': source_name, 'visibility': 'global', 'config': {'type': 'postgres', 'host': 'postgres', 'port': 5432, 'database': os.environ['POSTGRES_DB'], 'ssl_mode': 'disable', 'connection_timeout': 5, 'environment': 'test'}}, want=201)
    connection = api('POST', f'/datasource-definitions/{source["id"]}/connections', {'name': '作者开发连接', 'username': os.environ['POSTGRES_USER'], 'password': os.environ['POSTGRES_PASSWORD'], 'confirmed': True}, bearer=developer_token, want=201)
    personal = api('POST', '/reports', {'request_id':str(uuid.uuid4()),'name':personal_name,'connection_id':connection['id'],'sql':f'SELECT category,amount FROM {schema}.inventory_report ORDER BY category','chart_type':'table','chart_config':{},'visibility':'private'}, bearer=developer_token)
    assert not api('GET', '/connections', bearer=viewer_token)['items']
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(executable_path='/usr/bin/chromium', proxy={'server': os.environ.get('HTTPS_PROXY', 'http://proxy:8080'), 'bypass': 'backend,frontend,postgres,localhost,127.0.0.1'}, args=['--no-sandbox', '--disable-dev-shm-usage'])
        author = login(browser, developer_name)
        administrator = login(browser, admin_name)
        validation_events = []
        administrator.on('request',lambda r:validation_events.append({'event':'request','url':r.url}) if '/report-reviews/' in r.url else None)
        administrator.on('requestfailed',lambda r:validation_events.append({'event':'failed','url':r.url,'failure':r.failure}) if '/report-reviews/' in r.url else None)
        reader = login(browser, viewer_name)

        administrator.get_by_role('tab', name='执行账号', exact=True).click()
        administrator.get_by_role('button', name='新增执行账号', exact=True).click()
        account_editor = administrator.get_by_role('dialog', name='新增报表执行账号', exact=True)
        account_editor.get_by_role('textbox', name='执行账号名称', exact=True).fill(account_name)
        choose(administrator, '执行账号数据源', source_name)
        expect(account_editor).to_contain_text('修订 1 · 启用')
        account_editor.get_by_role('textbox', name='执行账号用户名', exact=True).fill(service_user)
        account_editor.get_by_role('textbox', name='执行账号密码', exact=True).fill(service_password)
        with administrator.expect_response(lambda r: r.request.method == 'POST' and r.url.endswith('/report-accounts')) as saved:
            account_editor.get_by_role('button', name='保存账号', exact=True).click()
        assert saved.value.status == 200
        account_id = saved.value.json()['data']['id']
        expect(account_editor).not_to_be_visible()
        account_card = administrator.get_by_role('article', name='执行账号：' + account_name, exact=True)
        account_card.get_by_role('button', name='测试只读连接', exact=True).click()
        expect(administrator.get_by_label('报表执行账号管理').get_by_role('status')).to_contain_text('只读查询验证通过')
        passed('administrator creates and validates an encrypted independent report account')

        author.get_by_role('article',name='报表：' + personal_name,exact=True).get_by_role('button',name='创建正式草稿',exact=True).click()
        editor = author.get_by_role('dialog', name='新建正式报表草稿', exact=True)
        editor.get_by_role('textbox', name='报表名称', exact=True).fill(report_name)
        choose(author, '报表个人连接', '作者开发连接')
        editor.get_by_role('textbox', name='报表SQL', exact=True).fill(f'SELECT category,amount FROM {schema}.inventory_report ORDER BY category')
        editor.get_by_role('button', name='运行预览', exact=True).click()
        expect(editor.get_by_label('报表查询结果')).to_contain_text('返回 2 行')
        choose(author, '报表展示方式', '柱状图')
        choose(author, '报表维度', 'category')
        choose(author, '报表指标', 'amount')
        with author.expect_response(lambda r: r.request.method == 'POST' and r.url.endswith('/managed-reports')) as saved:
            editor.get_by_role('button', name='保存草稿', exact=True).click()
        assert saved.value.status == 200
        report_id = saved.value.json()['data']['id']
        expect(editor).not_to_be_visible()
        card = author.get_by_role('article', name='开发报表：' + report_name, exact=True)
        card.get_by_role('button', name='提交审批', exact=True).click()
        author.get_by_role('button', name='提交审批', exact=True).last.click()
        review_dialog = author.get_by_role('dialog', name='报表版本与发布', exact=True)
        expect(review_dialog).to_contain_text('待审批')
        expect(review_dialog.get_by_role('button', name='批准此版本', exact=True)).to_have_count(0)
        assert api('GET', '/published-reports', bearer=viewer_token)['total'] == 0
        review_dialog.locator('button.el-dialog__headerbtn').click()
        passed('developer converts a personal report, previews with a personal account and submits an immutable chart version')

        administrator.get_by_role('tab', name='开发与审批', exact=True).click()
        choose(administrator, '正式报表管理范围', '待审批')
        admin_card = administrator.get_by_role('article', name='开发报表：' + report_name, exact=True)
        admin_card.get_by_role('button', name='版本与发布', exact=True).click()
        review_dialog = administrator.get_by_role('dialog', name='报表版本与发布', exact=True)
        expect(review_dialog.get_by_role('button', name='批准此版本', exact=True)).to_be_disabled()
        choose(administrator, '审批执行账号', account_name + ' · ' + service_user)
        choose(administrator, '审批共享用户', viewer_name)
        try:
            with administrator.expect_response(lambda r: r.url.endswith('/validate') and '/report-reviews/' in r.url, timeout=10000) as validation:
                review_dialog.get_by_role('button', name='使用执行账号验证', exact=True).click()
            assert validation.value.status == 200, 'review validation: HTTP ' + str(validation.value.status) + ': ' + validation.value.json().get('message','')
        except Exception:
            (out/'approval-failure.json').write_text(json.dumps({'dialog':review_dialog.inner_text(),'validation_events':validation_events},ensure_ascii=False,indent=2))
            administrator.screenshot(path=str(out/'approval-failure.png'),full_page=True)
            raise
        expect(review_dialog.get_by_label('报表查询结果')).to_contain_text('返回 2 行')
        expect(review_dialog.get_by_role('button', name='批准此版本', exact=True)).to_be_enabled()
        choose(administrator,'审批共享用户',admin_name)
        expect(review_dialog.get_by_role('button',name='批准此版本',exact=True)).to_be_disabled()
        choose(administrator,'审批共享用户',admin_name)
        expect(review_dialog.get_by_role('button',name='批准此版本',exact=True)).to_be_disabled()
        review_dialog.get_by_role('button',name='使用执行账号验证',exact=True).click()
        expect(review_dialog.get_by_role('button',name='批准此版本',exact=True)).to_be_enabled()
        review_dialog.get_by_role('textbox', name='审批意见', exact=True).fill('仅开放库存视图，批准当前版本和指定用户')
        review_dialog.get_by_role('button', name='批准此版本', exact=True).click()
        confirm(administrator)
        expect(review_dialog.get_by_role('button', name='发布此版本', exact=True)).to_be_visible()
        review_dialog.get_by_role('button', name='发布此版本', exact=True).click()
        confirm(administrator)
        expect(review_dialog).to_contain_text('发布序号 1')
        administrator.screenshot(path=str(out / 'managed-report-approval-desktop.png'), full_page=True)
        review_dialog.locator('button.el-dialog__headerbtn').click()
        passed('administrator validates the selected account and audience before approval and explicit publication')

        reader.get_by_role('tab', name='已发布', exact=True).click()
        published_card = reader.get_by_role('article', name='正式报表：' + report_name, exact=True)
        published_card.get_by_role('button', name='查看报表', exact=True).click()
        result_dialog = reader.get_by_role('dialog', name=report_name, exact=True)
        expect(result_dialog.get_by_label('报表查询结果')).to_contain_text('返回 2 行')
        expect(result_dialog.locator('canvas')).to_be_visible()
        expect(result_dialog.locator('pre')).to_have_count(0)
        result_dialog.get_by_text('查看查询结果', exact=True).click()
        expect(result_dialog.get_by_role('region', name='SQL 查询结果表格')).to_contain_text('12.34')
        reader.set_viewport_size({'width': 390, 'height': 844})
        reader.screenshot(path=str(out / 'managed-report-viewer-mobile.png'), full_page=True)
        result_dialog.locator('button.el-dialog__headerbtn').click()
        assert not api('GET', '/connections', bearer=viewer_token)['items']
        api('GET', f'/managed-reports/{report_id}', bearer=viewer_token, want=403)
        passed('unbound business viewer runs a published chart without seeing SQL or credentials, including mobile')

        author.get_by_role('button', name='刷新', exact=True).click()
        card.get_by_role('button', name='编辑草稿', exact=True).click()
        editor = author.get_by_role('dialog', name='编辑正式报表草稿', exact=True)
        editor.get_by_role('textbox', name='报表SQL', exact=True).fill('SELECT 99 AS total')
        choose(author, '报表展示方式', '表格')
        editor.get_by_role('button', name='保存草稿', exact=True).click()
        expect(editor).not_to_be_visible()
        reader.get_by_role('button', name='刷新', exact=True).click()
        published_card.get_by_role('button', name='查看报表', exact=True).click()
        expect(reader.get_by_role('dialog', name=report_name, exact=True).get_by_label('报表查询结果')).to_contain_text('返回 2 行')
        reader.get_by_role('dialog', name=report_name, exact=True).locator('button.el-dialog__headerbtn').click()
        passed('saving a new working draft leaves the approved production version unchanged')

        card.get_by_role('button', name='提交审批', exact=True).click()
        author.get_by_role('button', name='提交审批', exact=True).last.click()
        author.get_by_role('dialog', name='报表版本与发布', exact=True).locator('button.el-dialog__headerbtn').click()
        administrator.get_by_role('button', name='刷新', exact=True).click()
        admin_card.get_by_role('button', name='版本与发布', exact=True).click()
        review_dialog = administrator.get_by_role('dialog', name='报表版本与发布', exact=True)
        review_dialog.get_by_role('textbox', name='审批意见', exact=True).fill('此变更不符合库存统计口径')
        review_dialog.get_by_role('button', name='退回修改', exact=True).click()
        confirm(administrator)
        expect(review_dialog).to_contain_text('已退回')
        expect(review_dialog).to_contain_text('此变更不符合库存统计口径')
        passed('administrator rejects a changed version with an explicit reason and preserves the live release')

        review_dialog.get_by_role('button', name='下线报表', exact=True).click()
        confirm(administrator)
        expect(review_dialog).to_contain_text('发布序号 2')
        api('POST', f'/published-reports/{report_id}/execute', {'request_id': str(uuid.uuid4()), 'publication_no': 1}, bearer=viewer_token, want=404)
        versions = api('GET', f'/managed-reports/{report_id}/versions')['items']
        v1 = next(v for v in versions if v['number'] == 1)
        choose(administrator, '选择报表版本', 'v1 · 已批准 · ' + report_name)
        review_dialog.get_by_role('button', name='回滚到此版本', exact=True).click()
        confirm(administrator)
        expect(review_dialog).to_contain_text('发布序号 3')
        assert api('POST', f'/published-reports/{report_id}/execute', {'request_id': str(uuid.uuid4()), 'publication_no': 3}, bearer=viewer_token)['rows']
        passed('explicit unpublication blocks execution and rollback restores a previously approved version')
        review_dialog.locator('button.el-dialog__headerbtn').click()

        administrator.get_by_role('tab', name='执行账号', exact=True).click()
        account_card.get_by_role('button', name='停用', exact=True).click()
        confirm(administrator)
        expect(account_card.get_by_role('button', name='启用', exact=True)).to_be_visible()
        api('POST', f'/published-reports/{report_id}/execute', {'request_id': str(uuid.uuid4()), 'publication_no': 3}, bearer=viewer_token, want=403)
        account_card.get_by_role('button', name='启用', exact=True).click()
        confirm(administrator)
        expect(account_card.get_by_role('button', name='停用', exact=True)).to_be_visible()
        api('DELETE', f'/connections/{connection["id"]}', bearer=developer_token)
        assert api('POST', f'/published-reports/{report_id}/execute', {'request_id': str(uuid.uuid4()), 'publication_no': 3}, bearer=viewer_token)['rows']
        passed('account disable blocks live execution and removing the author connection does not remove the report')
        assert not errors, errors
        passed('no browser script errors across the real multi-account workflow')
        browser.close()

    delivery = api('GET', '/audit-logs/delivery')
    assert delivery['ready']
    audit = api('GET', '/audit-logs?action=REPORT_PUBLISHED_EXECUTE')
    assert any(row.get('context', {}).get('database_account') == service_user and row.get('context', {}).get('actor_id') == viewer['id'] for row in audit['items'])
    passed('durable audit identifies the business viewer and the actual report database account')
except Exception:
    code = 1
    detail = traceback.format_exc()
    for value in [password, service_password, os.environ.get('APP_ADMIN_PASSWORD'), os.environ.get('POSTGRES_PASSWORD'), *sessions]:
        if value: detail = detail.replace(value, '[redacted]')
    print(detail, file=sys.stderr)
finally:
    for token in reversed(sessions):
        try: api('POST', '/auth/logout', {}, bearer=token)
        except Exception: pass
    summary = {'passed': len(checks), 'checks': checks, 'page_errors': errors, 'mocks': 0, 'failed': code != 0}
    (out / 'managed-report-browser.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2))
    print(json.dumps(summary, ensure_ascii=False), flush=True)
if code == 0 and os.environ.get('DBHUB_RUN_REPORT_REGRESSION') == '1':
    import subprocess
    code = subprocess.run([sys.executable, '/app/scripts/cloud/report_browser_test.py']).returncode
    print('Personal report regression exit code: ' + str(code), flush=True)
sys.exit(code)
