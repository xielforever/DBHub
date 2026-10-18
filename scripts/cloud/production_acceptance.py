"""Owned production-image fault, recovery and optional sustained-load acceptance.

No production credentials are used. Containers, volumes, database fixtures and
temporary credentials are removed in finally; only non-sensitive evidence stays.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import datetime as dt
from decimal import Decimal
import hashlib
import json
import os
from pathlib import Path
import random
import re
import secrets
import socket
import statistics
import subprocess
import threading
import time
import urllib.error
import urllib.request
from urllib.parse import quote
import uuid
from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[2]


def key():
    return str(uuid.uuid4())


class Runtime:
    def __init__(self):
        self.suffix = secrets.token_hex(5)
        self.prefix = 'dbhub-production-' + self.suffix
        self.state = Path(os.environ.get('DBHUB_STATE_DIR', '/workspace/dbhub-cloud')) / 'production-acceptance' / self.suffix
        self.state.mkdir(parents=True, mode=0o700)
        self.network = self.prefix + '-net'
        self.pg, self.redis, self.front = [self.prefix + '-' + name for name in ['postgres', 'redis', 'frontend']]
        self.app = self.prefix + '-backend'
        self.audit = self.prefix + '-audit'
        self.audit_snapshot = self.prefix + '-audit-snapshot'
        self.backend_image = os.environ.get('DBHUB_DASHBOARD_BACKEND_IMAGE', 'dbhub-backend:production-fix-20261018')
        self.frontend_image = os.environ.get('DBHUB_DASHBOARD_FRONTEND_IMAGE', 'dbhub-frontend:production-fix-20261018')
        self.owned, self.private_files = [], []
        self.sessions, self.lock = {}, threading.Lock()
        self.password, self.pg_password = 'Acceptance9!' + secrets.token_hex(24), secrets.token_hex(24)
        self.reader, self.reader_password = 'release_reader_' + self.suffix, secrets.token_hex(24)
        self.dbuser = 'dbhub_release'
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            self.port = sock.getsockname()[1]
        self.origin = f'http://127.0.0.1:{self.port}'
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.values = dict(APP_ENV='production', APP_SECRET_KEY=secrets.token_hex(32), APP_ADMIN_USERNAME='release_admin', APP_ADMIN_PASSWORD=self.password,
                           POSTGRES_USER=self.dbuser, POSTGRES_PASSWORD=self.pg_password, POSTGRES_DB='production_meta',
                           DATABASE_URL=self.dsn('production_meta'), REDIS_URL='redis://redis:6379/0', DBHUB_AUDIT_DIR='/var/lib/dbhub/audit',
                           DBHUB_TARGET_ALLOWLIST='postgres:5432', GOMAXPROCS='2')
        self.result = {'completed': False, 'checks': {}, 'source_commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
                       'data': 'Isolated synthetic PostgreSQL data, independently approved read-only report account, separate metadata and target databases',
                       'source_changed': bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True).strip())}

    def docker(self, *args, data=None, check=True):
        return subprocess.run(['docker', *args], input=data, capture_output=True, check=check)

    def dsn(self, database):
        return f'postgres://{self.dbuser}:{self.pg_password}@postgres:5432/{database}?sslmode=disable'

    def passed(self, name, details=True):
        self.result['checks'][name] = details
        print(json.dumps({'check': name, 'details': details}, ensure_ascii=False), flush=True)

    def environment(self, name, **overrides):
        path = self.state / (name + '.env')
        with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w') as target:
            for k, v in {**self.values, **overrides}.items():
                target.write(k + '=' + str(v) + '\n')
        self.private_files.append(path)
        return path

    def volume(self, name):
        self.docker('volume', 'create', '--label', 'dbhub.task=production-acceptance', name)
        self.owned.append(('volume', name))

    def run(self, name, *args):
        self.owned.append(('container', name))
        self.docker('run', '-d', '--name', name, '--label', 'dbhub.task=production-acceptance', '--network', self.network, *args)

    def sql(self, query, database='production_meta'):
        return self.docker('exec', '-i', self.pg, 'psql', '-X', '-A', '-t', '-v', 'ON_ERROR_STOP=1', '-U', self.dbuser, '-d', database,
                           data=query.encode()).stdout.decode().strip()

    def pg_ready(self):
        for _ in range(60):
            if self.docker('exec', self.pg, 'pg_isready', '-U', self.dbuser, check=False).returncode == 0:
                return
            time.sleep(0.5)
        raise RuntimeError('Owned PostgreSQL not ready')

    def raw(self, method, path, body=None, token=''):
        request = urllib.request.Request(self.origin + path, method=method, headers={'Content-Type': 'application/json', **({'Authorization': 'Bearer ' + token} if token else {})},
                                         data=json.dumps(body).encode() if body is not None else None)
        try:
            response = self.opener.open(request, timeout=35)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read()
            try:
                payload = json.loads(raw)
            except json.JSONDecodeError:
                payload = {}
            return response.status, payload, dict(response.headers)

    def api(self, method, path, body=None, actor='admin', want=200):
        token = ''
        if actor:
            with self.lock:
                session = self.sessions[actor]
                if session['expires'] < time.time() + 30:
                    status, data, _ = self.raw('POST', '/api/v1/auth/refresh', {'refresh_token': session['refresh_token']})
                    assert status == 200, 'Load client session refresh failed'
                    self.store_session(actor, data['data'])
                    session = self.sessions[actor]
                token = session['access_token']
        status, payload, _ = self.raw(method, '/api/v1' + path, body, token)
        assert status == want, f'{method} {path}: expected {want}, received {status}'
        return payload.get('data')

    def store_session(self, actor, session):
        self.sessions[actor] = {**session, 'expires': dt.datetime.fromisoformat(session['expires_at'].replace('Z', '+00:00')).timestamp()}

    def login(self, actor, username):
        data = self.api('POST', '/auth/login', {'username': username, 'password': self.password}, actor=None)
        self.store_session(actor, data)

    def wait_ready(self):
        for _ in range(60):
            try:
                if self.raw('GET', '/api/ready')[0] == 200:
                    return
            except Exception:
                pass
            time.sleep(0.5)
        raise RuntimeError('Candidate production image did not become ready')

    def backend(self, name, database, audit, **overrides):
        env = self.environment(name, DATABASE_URL=self.dsn(database), **overrides)
        self.run(name, '--network-alias', 'backend', '--env-file', str(env), '--read-only', '--cap-drop=ALL', '--security-opt', 'no-new-privileges',
                 '--cpus=2', '--memory=512m', '--mount', f'type=volume,src={audit},dst=/var/lib/dbhub/audit', self.backend_image)
        self.app = name

    def start(self):
        env = self.environment('base')
        self.docker('network', 'create', '--label', 'dbhub.task=production-acceptance', self.network)
        self.owned.append(('network', self.network))
        for volume in [self.prefix + '-pg-data', self.prefix + '-redis-data', self.audit, self.audit_snapshot]:
            self.volume(volume)
        self.run(self.pg, '--network-alias', 'postgres', '--env-file', str(env), '--memory=384m', '--cpus=1',
                 '--mount', f'type=volume,src={self.prefix}-pg-data,dst=/var/lib/postgresql/data',
                 '--mount', f'type=bind,src={ROOT}/scripts/ops,dst=/ops,readonly', 'postgres:16-alpine')
        self.pg_ready()
        self.run(self.redis, '--network-alias', 'redis', '--memory=64m', '--cpus=0.5',
                 '--mount', f'type=volume,src={self.prefix}-redis-data,dst=/data', 'redis:7-alpine', 'redis-server', '--appendonly', 'yes')
        self.backend(self.app, 'production_meta', self.audit)
        self.run(self.front, '--publish', f'127.0.0.1:{self.port}:80', '--memory=128m', '--cpus=1', self.frontend_image)
        self.wait_ready()
        self.login('admin', 'release_admin')
        self.result.update(backend_image=self.docker('image', 'inspect', self.backend_image, '--format', '{{.Id}}').stdout.decode().strip(),
                           frontend_image=self.docker('image', 'inspect', self.frontend_image, '--format', '{{.Id}}').stdout.decode().strip(),
                           backend_sha256=self.docker('exec', self.app, 'sha256sum', '/app/server').stdout.decode().split()[0])
        assert self.sql('SELECT count(*) FROM schema_migrations') == '35'
        self.passed('candidate_images_and_35_migrations')

    def fixture(self):
        self.sql('CREATE DATABASE production_target;')
        self.sql(f"""CREATE ROLE {self.reader} LOGIN PASSWORD '{self.reader_password}';
CREATE SCHEMA business;
CREATE TABLE business.inventory(warehouse TEXT,amount NUMERIC(20,2),visible BOOLEAN);
INSERT INTO business.inventory VALUES('杭州',780,true),('苏州',320,true),('成都',180,true),('隐藏',99999,false);
CREATE VIEW business.inventory_report AS SELECT warehouse,amount FROM business.inventory WHERE visible;
GRANT USAGE ON SCHEMA business TO {self.reader};
GRANT SELECT ON business.inventory_report TO {self.reader};""", 'production_target')
        self.users = {}
        for actor, role in [('author', 'developer'), ('viewer', 'readonly'), ('outsider', 'readonly')]:
            self.users[actor] = self.api('POST', '/users', {'username': 'release_' + actor, 'password': self.password, 'role': role}, want=201)['id']
            self.login(actor, 'release_' + actor)
        group = self.api('POST', '/user-groups', {'name': '恢复验收受众组', 'description': '', 'member_ids': [self.users['viewer']]})
        source = self.api('POST', '/connections', {'name': '恢复验收数据源', 'type': 'postgres', 'host': 'postgres', 'port': 5432, 'database': 'production_target',
                                                  'username': self.dbuser, 'password': self.pg_password, 'ssl_mode': 'disable', 'environment': 'dev'}, want=201)
        definition = self.api('POST', f'/connections/{source["id"]}/publish', {'visibility': 'global'}, want=201)
        self.personal = self.api('POST', f'/datasource-definitions/{definition["id"]}/connections', {'name': '恢复验收开发连接', 'username': self.dbuser, 'password': self.pg_password, 'confirmed': True}, actor='author', want=201)['id']
        self.account = self.api('POST', '/report-accounts', {'request_id': key(), 'name': '恢复验收只读账号', 'definition_revision_id': definition['id'], 'username': self.reader, 'password': self.reader_password})['id']
        self.reports = []
        for index, chart in enumerate(['metric', 'bar', 'table']):
            aggregate = chart == 'metric'
            columns = ([('amount', 'lossless_numeric')] if aggregate else [('warehouse', 'string'), ('amount', 'lossless_numeric')]) + [('watermark', 'timestamp_with_timezone')]
            mapping = {'metrics': ['amount']} if aggregate else {'dimension': 'warehouse', 'metrics': ['amount']} if chart == 'bar' else {}
            mapping['display'] = {'labels': {'amount': '库存金额'}, 'fraction_digits': 2, 'grouping': True}
            sql = 'SELECT ' + ('sum(amount) AS amount' if aggregate else 'warehouse,amount') + ",date_trunc('minute',now()) AS watermark FROM business.inventory_report" + ('' if aggregate else ' ORDER BY warehouse')
            contract = dict(semantic_key=f'recovery_{index}', meaning='三个仓库可售库存，不含隐藏仓库', result_schema=[dict(name=col, logical_type=kind, nullable=False) for col, kind in columns],
                            dimensions=['warehouse'] if chart == 'bar' else [], metrics=mapping.get('metrics', []), chart_type=chart, unit='CNY_10000', currency='CNY', timezone='Asia/Shanghai',
                            scope_key='visible_warehouses', scope_description='仅三个可售仓库，不含隐藏仓库', time_window={'kind': 'current_snapshot'}, comparison_key='inventory_balance',
                            freshness={'mode': 'watermark_column', 'column': 'watermark', 'max_data_age_seconds': 900, 'max_future_skew_seconds': 60})
            report = self.api('POST', '/managed-reports', {'request_id': key(), 'name': ['库存总额', '仓库分布', '库存明细'][index], 'connection_id': self.personal,
                                                        'sql': sql, 'chart_type': chart, 'chart_config': mapping, 'data_contract': contract}, actor='author')['id']
            review = self.api('POST', f'/managed-reports/{report}/submit', {'request_id': key(), 'version': 1}, actor='author')['id']
            validation = key()
            self.api('POST', f'/report-reviews/{review}/validate', {'request_id': validation, 'account_id': self.account, 'user_ids': [1, self.users['author']], 'group_ids': [group['id']]})
            self.api('POST', f'/report-reviews/{review}/decision', {'request_id': key(), 'decision': 'approved', 'validation_id': validation, 'contract_confirmed': True})
            self.api('POST', f'/managed-reports/{report}/releases', {'request_id': key(), 'expected_publication_no': 0, 'review_id': review, 'kind': 'publish'})
            self.reports.append(report)
        definition = {'name': '恢复验收库存看板', 'description': '独立受众组与只读账号恢复验证', 'tiles': [{'key': f'card{i}', 'report_id': report, 'required': True, 'width': 'half' if i == 0 else 'full', 'binding_policy': 'follow_published'} for i, report in enumerate(self.reports)],
                      'user_ids': [], 'group_ids': [group['id']], 'quality': {'allow_unknown_freshness': False, 'max_watermark_skew_seconds': 300}}
        self.dashboard = self.api('POST', '/managed-dashboards', {'request_id': key(), 'definition': definition}, actor='author')['id']
        preflight = self.api('POST', f'/managed-dashboards/{self.dashboard}/preflight', {'version': 1}, actor='author')
        assert preflight['can_submit']
        review = self.api('POST', f'/managed-dashboards/{self.dashboard}/submit', {'request_id': key(), 'version': 1, 'reviewer_id': 1}, actor='author')['id']
        context = self.api('POST', f'/managed-dashboards/{self.dashboard}/refreshes', {'mode': 'review', 'review_id': review})
        self.execute_cards(context, 'admin')
        self.api('POST', f'/dashboard-reviews/{review}/validate', {'request_id': key(), 'refresh_id': context['refresh_id']})
        self.api('POST', f'/dashboard-reviews/{review}/decision', {'request_id': key(), 'decision': 'approved'})
        self.api('POST', f'/managed-dashboards/{self.dashboard}/releases', {'request_id': key(), 'expected_publication_no': 0, 'review_id': review, 'kind': 'publish'})
        self.validate_restored_objects()
        self.passed('approved_reports_dashboard_group_and_encrypted_account_fixture')

    def execute_cards(self, context, actor='viewer'):
        for index, tile in enumerate(context['tiles']):
            data = self.api('POST', f'/dashboard-refreshes/{context["refresh_id"]}/tiles/{tile["key"]}/execute', {}, actor=actor)
            result = data['result']
            if index == 0:
                assert Decimal(str(result['rows'][0][0])) == Decimal('1280.00')
            else:
                assert len(result['rows']) == 3 and sum(Decimal(str(row[1])) for row in result['rows']) == Decimal('1280.00')
            assert data['quality']['freshness'] == 'fresh'

    def validate_restored_objects(self):
        assert self.api('GET', '/connections', actor='viewer')['total'] == 0
        view = self.api('GET', f'/published-dashboards/{self.dashboard}', actor='viewer')
        assert view['publication_no'] == 1 and len(view['tiles']) == 3
        raw = json.dumps(view)
        assert 'sql_text' not in raw and self.reader not in raw and 'report_account_id' not in raw
        context = self.api('POST', f'/managed-dashboards/{self.dashboard}/refreshes', {'mode': 'published'}, actor='viewer')
        self.execute_cards(context)
        result = self.api('POST', f'/published-reports/{self.reports[0]}/execute', {'request_id': key(), 'publication_no': 1}, actor='viewer')
        assert Decimal(str(result['rows'][0][0])) == Decimal('1280.00')
        self.api('GET', f'/published-reports/{self.reports[0]}', actor='outsider', want=404)
        self.api('GET', f'/published-dashboards/{self.dashboard}', actor='outsider', want=404)
        self.api('POST', f'/report-accounts/{self.account}/test', {})

    def login_protection(self):
        for _ in range(10):
            self.api('POST', '/auth/login', {'username': 'nonexistent_probe', 'password': 'Wrong9!'}, actor=None, want=401)
        status, _, headers = self.raw('POST', '/api/v1/auth/login', {'username': 'nonexistent_probe', 'password': 'Wrong9!'})
        assert status == 429 and 1 <= int(headers['Retry-After']) <= 300
        self.passed('default_login_limit_and_retry_after', {'allowed_attempts': 10, 'next_http': status})

    def copy_audit(self, source, destination):
        self.docker('run', '--rm', '--network', 'none', '--mount', f'type=volume,src={source},dst=/source,readonly',
                    '--mount', f'type=volume,src={destination},dst=/dest',
                    'debian:bookworm-slim', 'cp', '-a', '/source/.', '/dest/')

    def faults(self):
        token = self.sessions['viewer']['access_token']
        self.docker('stop', '-t', '5', self.redis)
        assert self.raw('GET', '/api/ready')[0] == 503
        assert self.raw('GET', '/api/health')[0] == 200
        assert self.raw('GET', '/api/v1/auth/me', token=token)[0] == 503
        self.docker('start', self.redis)
        self.wait_ready()
        assert self.raw('GET', '/api/v1/auth/me', token=token)[0] == 200
        self.passed('redis_outage_503_liveness_and_same_session_recovery')

        self.docker('stop', '-t', '5', self.pg)
        started = time.monotonic()
        assert self.raw('GET', '/api/ready')[0] == 503
        assert time.monotonic() - started < 3
        assert self.raw('GET', '/api/v1/auth/me', token=token)[0] == 503
        assert self.raw('POST', '/api/v1/auth/login', {'username': 'release_admin', 'password': self.password})[0] == 503
        # Persist the pending event, then recreate with the same volume after
        # the database returns. Preserve a copy to test later duplicate replay.
        self.docker('stop', '-t', '30', self.app)
        self.copy_audit(self.audit, self.audit_snapshot)
        self.docker('start', self.pg)
        self.pg_ready()
        self.docker('start', self.app)
        self.wait_ready()
        assert self.raw('GET', '/api/v1/auth/me', token=token)[0] == 200
        for _ in range(40):
            if self.api('GET', '/audit-logs/delivery')['pending'] == 0:
                break
            time.sleep(0.5)
        else:
            raise AssertionError('Pending audit events did not recover')
        self.audit_failures = int(self.sql("SELECT count(*) FROM sys_audit_logs WHERE action='LOGIN' AND context->>'http_status'='503'"))
        assert self.audit_failures == 1
        self.passed('postgres_outage_503_bounded_probe_session_and_durable_audit_recovery')

    def browser_security(self):
        with self.opener.open(self.origin + '/index.html', timeout=10) as response:
            html = response.read().decode()
        asset = re.search(r'<script[^>]*src="([^"]+)"', html).group(1)
        for path in ['/', '/index.html', asset, '/assets/absent-test.js']:
            status, _, headers = self.raw('GET', path)
            headers = {k.lower(): v for k, v in headers.items()}
            assert status == (404 if 'absent-test' in path else 200)
            assert headers.get('x-content-type-options') == 'nosniff'
            assert headers.get('x-frame-options') == 'DENY'
            policy = headers['content-security-policy']
            script = next(part for part in policy.split(';') if part.strip().startswith('script-src'))
            assert "'unsafe-eval'" not in script and "'unsafe-inline'" not in script
        status, _, headers = self.raw('GET', '/api/v1/auth/me', token=self.sessions['viewer']['access_token'])
        assert status == 200 and headers.get('Cache-Control') == 'no-store'

        errors, violations = [], []
        with sync_playwright() as playwright:
            executable = os.environ.get('DBHUB_CHROMIUM_PATH', '/usr/bin/chromium')
            browser = playwright.chromium.launch(**({'executable_path': executable} if Path(executable).is_file() else {}), headless=True,
                                                 args=['--no-sandbox', '--disable-dev-shm-usage'])
            context = browser.new_context(viewport={'width': 1440, 'height': 1000}, locale='zh-CN', timezone_id='Asia/Shanghai')
            page = context.new_page()
            page.set_default_timeout(20000)
            page.on('pageerror', lambda error: errors.append(str(error)))
            page.on('console', lambda message: violations.append(message.text) if message.type == 'error' and 'Content Security Policy' in message.text else None)
            page.goto(self.origin + '/login')
            page.get_by_label('用户名', exact=True).fill('release_author')
            page.get_by_label('密码', exact=True).fill(self.password)
            page.locator('button[type=submit]').click()
            expect(page.get_by_role('combobox', name='当前工作空间')).to_be_visible()
            statement = 'SELECT sum(amount) AS amount FROM business.inventory_report'
            page.goto(self.origin + f'/query?connection={self.personal}&sql=' + quote(statement))
            page.wait_for_selector('.monaco-editor .view-lines')
            with page.expect_response(lambda response: '/query/execute' in response.url and response.request.method == 'POST') as pending:
                page.get_by_role('button', name='运行', exact=True).click()
            response = pending.value
            assert response.status == 200 and Decimal(str(response.json()['data']['rows'][0][0])) == Decimal('1280.00')
            self.passed('monaco_query_runs_under_strict_production_csp')

            before = page.evaluate('() => [localStorage.getItem("dbhub_access_token"),localStorage.getItem("dbhub_refresh_token")]')
            report_route = '**/api/v1/workspaces/*/reports?*'
            page.route(report_route, lambda route: route.fulfill(status=401, content_type='application/json', body='{"code":40100,"message":"expired access for acceptance"}'))
            page.route('**/api/v1/auth/refresh', lambda route: route.fulfill(status=503, content_type='application/json', body='{"code":50300,"message":"验收模拟会话服务暂不可用"}'))
            page.goto(self.origin + '/reports')
            expect(page.get_by_text('验收模拟会话服务暂不可用', exact=False).first).to_be_visible()
            assert not page.url.startswith(self.origin + '/login')
            assert page.evaluate('() => [localStorage.getItem("dbhub_access_token"),localStorage.getItem("dbhub_refresh_token")]') == before
            page.unroute(report_route)
            page.unroute('**/api/v1/auth/refresh')
            with page.expect_response(lambda response: '/reports?' in response.url and response.request.method == 'GET') as retry:
                page.get_by_role('button', name='刷新', exact=True).click()
            assert retry.value.status == 200
            self.passed('expired_access_refresh_503_preserves_both_tokens_and_recovers')
            assert not errors and not violations, {'page_errors': errors, 'csp_violations': violations}
            self.result['browser'] = {'page_errors': len(errors), 'csp_violations': len(violations), 'refresh_503': 'explicit browser transport mock'}
            browser.close()
        self.passed('security_headers_for_html_assets_errors_and_private_api')

    def recovery(self):
        self.sql('CREATE DATABASE restored_meta;')
        services = ''.join(f'[{name}]\nhost=127.0.0.1\nport=5432\nuser={self.dbuser}\npassword={self.pg_password}\ndbname={database}\n'
                           for name, database in [('source', 'production_meta'), ('restore', 'restored_meta')])
        self.docker('exec', '-i', self.pg, 'sh', '-ec', 'umask 077; cat > /tmp/recovery-services.conf', data=services.encode())
        def operation(script, service, check=True):
            return self.docker('exec', '-e', 'PGSERVICEFILE=/tmp/recovery-services.conf', self.pg, '/ops/' + script,
                               service, '/tmp/metadata.dump', check=check)
        # Let the last response's durable execution bookkeeping complete, then
        # capture metadata while the app is idle but its runtime role exists.
        verify = (ROOT / 'scripts/ops/verify-metadata.sql').read_text()
        for _ in range(30):
            if self.sql("SELECT count(*) FROM sys_query_executions WHERE status IN('running','queued')") == '0':
                break
            time.sleep(0.1)
        expected = self.sql(verify)
        operation('backup-metadata.sh', 'source')
        archived = self.docker('exec', self.pg, 'pg_restore', '--list', '/tmp/metadata.dump').stdout
        assert b' ACL ' not in archived
        original_hash = self.docker('exec', self.pg, 'sha256sum', '/tmp/metadata.dump').stdout.split()[0]
        assert operation('backup-metadata.sh', 'source', check=False).returncode != 0
        assert operation('restore-metadata.sh', 'source', check=False).returncode != 0
        assert self.docker('exec', self.pg, 'sha256sum', '/tmp/metadata.dump').stdout.split()[0] == original_hash
        self.docker('stop', '-t', '30', self.app)
        self.docker('rm', '-f', self.app)
        assert self.sql(verify) == expected
        operation('restore-metadata.sh', 'restore')
        assert self.sql(verify, 'restored_meta') == expected
        self.passed('metadata_backup_restore_16_object_counts_and_digests', {'objects': 16, 'old_acl_excluded': True, 'overwrite_refused': True, 'nonempty_restore_refused': True})
        recovered_audit = self.prefix + '-recovered-audit'
        self.volume(recovered_audit)
        self.copy_audit(self.audit_snapshot, recovered_audit)
        self.backend(self.prefix + '-restored-backend', 'restored_meta', recovered_audit)
        self.wait_ready()
        # Existing sessions survive, as do encrypted source/account credentials,
        # group-based grants and independent report/dashboard publications.
        self.validate_restored_objects()
        for _ in range(40):
            if self.api('GET', '/audit-logs/delivery')['pending'] == 0:
                break
            time.sleep(0.5)
        assert int(self.sql("SELECT count(*) FROM sys_audit_logs WHERE action='LOGIN' AND context->>'http_status'='503'", 'restored_meta')) == self.audit_failures
        self.passed('restored_encryption_groups_publications_permissions_and_audit_deduplication')
        self.metadata = 'restored_meta'

    def sustained_load(self, duration, clients):
        if duration == 0:
            self.result['load'] = {'executed': False, 'reason': 'Run --duration 1800 for real sustained acceptance'}
            return
        before = int(self.sql('SELECT count(*) FROM sys_query_executions', self.metadata))
        measurements, errors, samples, lock = [], [], [], threading.Lock()
        stop = threading.Event()
        started = time.monotonic()
        def worker(index):
            rng = random.Random(index)
            while not stop.is_set():
                kind = rng.choice(['report', 'dashboard', 'workbench'])
                began = time.monotonic()
                try:
                    if kind == 'report':
                        result = self.api('POST', f'/published-reports/{self.reports[0]}/execute', {'request_id': key(), 'publication_no': 1}, actor='viewer')
                        assert Decimal(str(result['rows'][0][0])) == Decimal('1280.00')
                    elif kind == 'dashboard':
                        context = self.api('POST', f'/managed-dashboards/{self.dashboard}/refreshes', {'mode': 'published'}, actor='viewer')
                        self.execute_cards(context)
                    else:
                        result = self.api('POST', '/query/execute', {'request_id': key(), 'connection_id': self.personal,
                                          'sql': 'SELECT sum(amount) AS amount FROM business.inventory_report'}, actor='author')
                        assert Decimal(str(result['rows'][0][0])) == Decimal('1280.00')
                    with lock:
                        measurements.append((kind, (time.monotonic() - began) * 1000))
                except Exception as error:
                    with lock:
                        errors.append(type(error).__name__ + ': ' + str(error))
                    stop.set()
                stop.wait(0.5)
        def sample():
            status = self.docker('exec', self.app, 'cat', '/proc/1/status').stdout.decode()
            rss = int(re.search(r'^VmRSS:\s+(\d+)', status, re.M).group(1)) * 1024
            active = int(self.sql("SELECT count(*) FROM pg_stat_activity WHERE datname='restored_meta' AND state<>'idle'", self.metadata))
            return {'elapsed_seconds': round(time.monotonic() - started, 2), 'rss_bytes': rss, 'active_metadata_connections': active}
        with ThreadPoolExecutor(max_workers=clients) as pool:
            futures = [pool.submit(worker, index) for index in range(clients)]
            while not stop.is_set() and time.monotonic() - started < duration:
                samples.append(sample())
                with lock:
                    count = len(measurements)
                print(json.dumps({'load_elapsed_seconds': round(time.monotonic() - started, 1), 'completed_cycles': count}), flush=True)
                stop.wait(min(30, max(0, duration - (time.monotonic() - started))))
            stop.set()
            for future in futures:
                future.result()
        elapsed = time.monotonic() - started
        samples.append(sample())
        after = int(self.sql('SELECT count(*) FROM sys_query_executions', self.metadata))
        counts = {kind: sum(1 for k, _ in measurements if k == kind) for kind in ['report', 'dashboard', 'workbench']}
        expected = counts['report'] + counts['workbench'] + 3 * counts['dashboard']
        timings = sorted(value for _, value in measurements)
        p95 = timings[min(len(timings) - 1, int(len(timings) * .95))] if timings else None
        self.result['load'] = {'executed': True, 'requested_seconds': duration, 'elapsed_seconds': round(elapsed, 2), 'clients': clients, 'cycles': counts,
                               'expected_sql_executions': expected, 'actual_execution_receipts': after - before, 'errors': errors,
                               'cycle_p95_ms': round(p95, 2) if p95 is not None else None, 'cycle_mean_ms': round(statistics.mean(timings), 2) if timings else None,
                               'samples': samples, 'backend_limit': '2 CPU, 512 MiB', 'data_kind': 'Synthetic three-warehouse view; no production SQL or capacity certification'}
        assert not errors and timings and elapsed >= duration and after - before == expected
        assert all(counts.values()) and p95 < 5000
        assert self.sql('SELECT count(*) FROM sys_dashboard_query_leases', self.metadata) == '0'
        self.passed('sustained_mixed_load_exact_receipts_and_empty_leases', {'elapsed_seconds': round(elapsed, 2), 'cycles': counts, 'sql_executions': expected, 'p95_ms': round(p95, 2), 'errors': 0})

    def cleanup(self):
        failures = []
        for kind, name in reversed(self.owned):
            if kind == 'container':
                self.docker('stop', '-t', '15', name, check=False)
                self.docker('rm', '-f', name, check=False)
            else:
                self.docker(kind, 'rm', name, check=False)
            if self.docker(kind, 'inspect', name, check=False).returncode == 0:
                failures.append(name)
        for path in self.private_files:
            path.unlink(missing_ok=True)
        self.result.update(temporary_resources_removed=not failures, cleanup_failures=failures)
        (self.state / 'checks.json').write_text(json.dumps(self.result, ensure_ascii=False, indent=2) + '\n')
        print(json.dumps({'completed': self.result['completed'], 'report': str(self.state / 'checks.json'), 'resources_removed': not failures}), flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--duration', type=int, default=0, help='Real sustained-load seconds; 1800 for the release exercise')
    parser.add_argument('--clients', type=int, default=4)
    args = parser.parse_args()
    if not 0 <= args.duration <= 7200 or not 1 <= args.clients <= 4:
        parser.error('duration must be 0–7200 seconds and clients 1–4')
    runtime = Runtime()
    try:
        runtime.start()
        runtime.fixture()
        runtime.login_protection()
        runtime.faults()
        runtime.browser_security()
        runtime.recovery()
        runtime.sustained_load(args.duration, args.clients)
        runtime.result['completed'] = True
    finally:
        runtime.cleanup()


if __name__ == '__main__':
    main()
