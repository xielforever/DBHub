#!/usr/bin/env python3
"""Production-image browser checks for independent approval and cookie CSRF.

Only synthetic data and owned containers are used. Raw logs stay private; all
temporary containers, networks, volumes and credentials are removed in finally.
"""
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import threading

from playwright.sync_api import expect, sync_playwright

from production_acceptance import Runtime, key


def main():
    rt = Runtime()
    attack_server = None
    try:
        rt.start()
        reviewer_name = 'security_reviewer_' + rt.suffix
        rt.api('POST', '/users', {'username': reviewer_name, 'password': rt.password, 'role': 'admin'}, want=201)
        rt.sql('CREATE DATABASE security_target')
        rt.sql('CREATE TABLE inventory(id INT PRIMARY KEY,n INT); INSERT INTO inventory VALUES(1,0)', 'security_target')
        source = rt.api('POST', '/connections', {'name': '<img data-security-probe>', 'type': 'postgres', 'host': 'postgres', 'port': 5432,
                                               'database': 'security_target', 'username': rt.dbuser, 'password': rt.pg_password,
                                               'ssl_mode': 'disable', 'environment': 'test'}, want=201)
        sql = '/* <img src=x> ' + '完整审阅内容 ' * 40 + '*/\nUPDATE inventory SET n=n+1 WHERE id=1'
        title = 'independent approval ' + rt.suffix
        ticket = rt.api('POST', '/tickets', {'title': title, 'table': 'inventory', 'connection_id': source['id'], 'sql': sql})
        path = '/tickets/' + str(ticket['id'])
        rt.api('POST', path + '/status', {'status': 'approved'}, want=409)
        rt.api('POST', path + '/execute', want=409)

        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=os.environ.get('DBHUB_CHROMIUM_PATH', '/usr/bin/chromium') or None,
                                        headless=True, args=['--no-sandbox'])
            owner_context, reviewer_context = browser.new_context(), browser.new_context()

            def login(context, username):
                page = context.new_page()
                page.goto(rt.origin + '/login')
                page.get_by_label('用户名', exact=True).fill(username)
                page.get_by_label('密码', exact=True).fill(rt.password)
                page.get_by_role('button', name='登 录').click()
                expect(page).not_to_have_url(rt.origin + '/login')
                page.goto(rt.origin + '/tickets')
                expect(page.get_by_text(title, exact=True)).to_be_visible()
                return page

            owner = login(owner_context, 'release_admin')
            own_row = owner.get_by_text(title, exact=True).locator('xpath=../..')
            expect(own_row.get_by_text('等待其他管理员审批', exact=True)).to_be_visible()
            expect(own_row.get_by_role('button', name='批准', exact=True)).to_have_count(0)
            reviewer = login(reviewer_context, reviewer_name)
            review_row = reviewer.get_by_text(title, exact=True).locator('xpath=../..')
            expect(review_row.locator('pre')).to_have_text(sql)
            expect(review_row.locator('pre img')).to_have_count(0)
            expect(review_row.get_by_text('数据源修订', exact=False)).to_be_visible()
            review_row.get_by_role('button', name='批准', exact=True).click()
            dialog = reviewer.get_by_role('dialog', name='批准工单')
            expect(dialog).to_be_visible()
            dialog.get_by_role('button', name='批准', exact=True).click()
            expect(review_row.get_by_text('已批准', exact=True)).to_be_visible()
            owner.get_by_role('button', name='刷新', exact=True).click()
            own_row.get_by_role('button', name='执行 SQL', exact=True).click()
            owner.get_by_role('dialog', name='执行工单').get_by_role('button', name='执行', exact=True).click()
            expect(own_row.get_by_text('已执行', exact=True)).to_be_visible()
            assert rt.sql('SELECT n FROM inventory WHERE id=1', 'security_target') == '1'
            rt.api('POST', path + '/execute', want=409)
            assert rt.sql('SELECT n FROM inventory WHERE id=1', 'security_target') == '1'
            rt.passed('browser_independent_approval_full_sql_target_and_single_execution')

            with owner.expect_response(lambda r: '/metrics/overview' in r.url and r.status == 200) as ranking_response:
                owner.goto(rt.origin + '/dashboard')
            assert any('data-security-probe' in item['name'] for item in ranking_response.value.json()['data']['rank'])
            ranking = owner.get_by_role('img', name='库查询量排行条形图', exact=True)
            expect(ranking).to_be_visible()
            ranking.scroll_into_view_if_needed()
            owner.wait_for_timeout(1200)  # Let the bar's finite entrance animation finish.
            bounds = ranking.bounding_box()
            assert bounds
            ranking.hover(position={'x': bounds['width'] - 36, 'y': bounds['height'] / 2})
            owner.wait_for_timeout(250)
            expect(owner.locator('img[data-security-probe]')).to_have_count(0)
            rt.passed('browser_untrusted_connection_name_is_not_rendered_as_tooltip_html')

            # Removing a publisher binding must not cascade away row policies
            # that other personal identities inherit from its published source.
            rt.sql(f"CREATE ROLE {rt.reader} LOGIN PASSWORD '{rt.reader_password}'")
            rt.sql(f'INSERT INTO inventory VALUES(2,0); GRANT SELECT ON inventory TO {rt.reader}', 'security_target')
            reader_name = 'security_reader_' + rt.suffix
            rt.api('POST', '/users', {'username': reader_name, 'password': rt.password, 'role': 'readonly'}, want=201)
            rt.login('reader', reader_name)
            definition = rt.api('POST', '/connections/' + str(source['id']) + '/publish', {'visibility': 'global'}, want=201)
            rt.api('POST', '/connections/' + str(source['id']) + '/revision', {'definition_revision_id': definition['id'],
                   'username': rt.dbuser, 'password': rt.pg_password, 'confirmed': True})
            binding = rt.api('POST', '/datasource-definitions/' + str(definition['id']) + '/connections',
                             {'username': rt.reader, 'password': rt.reader_password, 'confirmed': True}, actor='reader', want=201)
            policy = rt.api('PUT', '/query/policies', {'connection_id': source['id'], 'database_name': 'security_target',
                            'schema_name': 'public', 'table_name': 'inventory', 'role_code': 'readonly', 'column_permissions': {},
                            'row_conditions': [{'column': 'id', 'operator': '=', 'value': 1}]})

            def restricted_rows():
                return rt.api('POST', '/query/execute', {'request_id': key(), 'connection_id': binding['id'],
                              'sql': 'SELECT id FROM inventory ORDER BY id'}, actor='reader')['rows']

            assert restricted_rows() == [[1]]
            rt.api('DELETE', '/connections/' + str(source['id']), want=409)
            assert restricted_rows() == [[1]]
            rt.api('DELETE', '/query/policies/' + str(policy['id']))
            rt.api('DELETE', '/connections/' + str(source['id']))
            rt.passed('production_publisher_delete_preserves_other_users_row_policy')

            valid_status = owner.evaluate('''async () => (await fetch('/api/v1/snippets', {
                method: 'POST', credentials: 'include', headers: {'Content-Type': 'application/json', 'X-DBHub-CSRF': '1'},
                body: JSON.stringify({name: 'legitimate cookie mutation', sql_text: 'SELECT 1', visibility: 'private'})
            })).status''')
            assert valid_status == 200
            rt.passed('browser_legitimate_cookie_only_mutation_with_csrf_header')

            # Different ports are different origins but the same site. Lax
            # cookies therefore accompany a browser-simple attack request.
            attacks = [{'name': 'simple attack', 'headers': {'Content-Type': 'text/plain'}},
                       {'name': 'preflight attack', 'headers': {'Content-Type': 'application/json', 'X-DBHub-CSRF': '1'}}]
            html = ('<body><script>Promise.all(' + json.dumps(attacks) + '''.map(async a => {
              try { await fetch(''' + json.dumps(rt.origin + '/api/v1/snippets') + ''', {
                method:'POST', credentials:'include',headers:a.headers,
                body:JSON.stringify({name:a.name,sql_text:'SELECT 1',visibility:'private'})
              }); } catch(e) {} })).then(()=>document.body.dataset.done='true')</script></body>''').encode()

            class AttackHandler(BaseHTTPRequestHandler):
                def do_GET(self):
                    self.send_response(200)
                    self.send_header('Content-Type', 'text/html; charset=utf-8')
                    self.end_headers()
                    self.wfile.write(html)

                def log_message(self, *args):
                    pass

            attack_server = ThreadingHTTPServer(('127.0.0.1', 0), AttackHandler)
            threading.Thread(target=attack_server.serve_forever, daemon=True).start()
            attacker = owner_context.new_page()
            attacker.goto('http://127.0.0.1:' + str(attack_server.server_port))
            expect(attacker.locator('body')).to_have_attribute('data-done', 'true')
            assert rt.sql("SELECT count(*) FROM sys_sql_snippets WHERE name IN ('simple attack','preflight attack')") == '0'
            rt.passed('browser_same_site_csrf_simple_post_and_untrusted_preflight_blocked')
            browser.close()
        rt.result['completed'] = True
    except Exception as error:
        rt.result['error_type'] = type(error).__name__
        for name in [rt.app, rt.front]:
            log = rt.docker('logs', name, check=False)
            (rt.state / (name + '.log')).write_bytes(log.stdout + log.stderr)
        raise
    finally:
        if attack_server:
            attack_server.shutdown()
            attack_server.server_close()
        rt.cleanup()


if __name__ == '__main__':
    main()
