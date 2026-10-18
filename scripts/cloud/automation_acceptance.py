"""Actual production-image acceptance for inbox, source jobs and automatic backups.

All identities, databases, credentials, containers and volumes are owned fixtures.
No messages leave this environment. Only sanitized checks survive cleanup.
"""
import io
import json
import os
from pathlib import Path
import re
import secrets
import tarfile
import time

from playwright.sync_api import expect, sync_playwright
from production_acceptance import ROOT, Runtime, key


class AutomationRuntime(Runtime):
    def __init__(self):
        super().__init__()
        self.values['DBHUB_BACKUP_EXPECTED_HOURS'] = '1'
        self.backup_image = os.environ.get('DBHUB_BACKUP_IMAGE', 'dbhub-backup:automation-20261018')
        self.backups = self.prefix + '-backups'
        self.backup_runs = 0

    def api(self, method, path, *args, **kwargs):
        if not path.startswith(('/auth/', '/platform/', '/workspaces')):
            path = '/workspaces/1' + path
        return super().api(method, path, *args, **kwargs)

    def wait(self, predicate, timeout=45):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if predicate():
                return
            time.sleep(.3)
        raise AssertionError('Owned acceptance condition did not become true')

    def source_jobs(self):
        body = {'connection_id': self.personal, 'kind': 'check', 'request_id': key()}
        job = self.api('POST', '/source-tasks', body, actor='author', want=202)
        again = self.api('POST', '/source-tasks', body, actor='author', want=202)
        assert again['id'] == job['id']
        self.wait(lambda: self.api('GET', '/source-tasks/' + job['id'], actor='author')['status'] == 'succeeded')
        self.api('GET', '/source-tasks/' + job['id'], actor='viewer', want=404)
        self.api('GET', '/source-tasks/' + job['id'], actor='admin', want=404)
        history = self.api('GET', '/source-check-history', actor='author')
        assert history['total'] >= 1 and history['items'][0]['current_config']
        raw = json.dumps(self.api('GET', '/source-tasks', actor='author'))
        assert self.pg_password not in raw and 'worker_token' not in raw and 'request_hash' not in raw
        self.passed('durable_jobs_idempotency_personal_identity_history_and_secret_exclusion')
        collected = self.api('POST', '/source-tasks', {'connection_id': self.personal, 'kind': 'collect', 'request_id': key()}, actor='author', want=202)
        self.wait(lambda: self.api('GET', '/source-tasks/' + collected['id'], actor='author')['status'] == 'succeeded')
        assert self.api('GET', '/source-tasks/' + collected['id'], actor='author')['result']['views'] >= 1
        self.passed('background_collection_publishes_real_target_view')
        plan = self.api('PUT', '/source-schedules', {'connection_id': self.personal, 'kind': 'check', 'interval_minutes': 5, 'enabled': True, 'version': 0}, actor='author')
        self.api('PUT', '/source-schedules', {'connection_id': self.personal, 'kind': 'check', 'interval_minutes': 5, 'enabled': False, 'version': plan['version']}, actor='author')
        assert not self.api('GET', '/source-schedules', actor='author')['items'][0]['enabled']
        self.passed('schedule_creation_and_pause_without_replaying_old_intervals')

    def browser(self):
        report = self.api('POST', '/managed-reports', {'request_id': key(), 'name': '待办验收报表', 'connection_id': self.personal,
                          'sql': 'SELECT sum(amount) AS amount FROM business.inventory_report', 'chart_type': 'table', 'chart_config': {}}, actor='author')
        self.api('POST', f'/managed-reports/{report["id"]}/submit', {'request_id': key(), 'version': 1}, actor='author')
        assert self.api('GET', '/inbox/todos')['total'] == 1
        errors, violations = [], []
        with sync_playwright() as playwright:
            executable = os.environ.get('DBHUB_CHROMIUM_PATH', '/usr/bin/chromium')
            browser = playwright.chromium.launch(**({'executable_path': executable} if Path(executable).is_file() else {}), headless=True,
                                                 args=['--no-sandbox', '--disable-dev-shm-usage'])
            def select_workspace(page, name):
                page.locator('.el-select').filter(has=page.get_by_role('combobox', name='当前工作空间')).locator('.el-select__wrapper').click()
                page.get_by_role('option', name=name, exact=True).click()
            def login(actor):
                context = browser.new_context(viewport={'width': 1440, 'height': 1000}, locale='zh-CN', timezone_id='Asia/Shanghai')
                page = context.new_page()
                page.set_default_timeout(20000)
                page.on('pageerror', lambda error: errors.append(str(error)))
                page.on('console', lambda msg: violations.append(msg.text) if msg.type == 'error' and 'Content Security Policy' in msg.text else None)
                page.goto(self.origin + '/login')
                page.get_by_label('用户名', exact=True).fill('release_' + actor)
                page.get_by_label('密码', exact=True).fill(self.password)
                page.locator('button[type=submit]').click()
                expect(page.get_by_role('combobox', name='当前工作空间')).to_be_visible()
                select_workspace(page, '默认工作空间')
                return context, page
            author_context, author = login('author')
            author.goto(self.origin + f'/source-tasks?connection_id={self.personal}')
            with author.expect_response(lambda r: '/source-tasks' in r.url and r.request.method == 'POST') as pending:
                author.get_by_role('button', name='加入队列', exact=True).click()
            assert pending.value.status == 202
            expect(author.get_by_text('连接检查通过', exact=True).first).to_be_visible()
            author.get_by_role('tab', name='检查历史', exact=True).click()
            expect(author.get_by_text('检查通过', exact=True).first).to_be_visible()
            author.get_by_role('tab', name='定时计划', exact=True).click()
            expect(author.get_by_text('已暂停', exact=True).first).to_be_visible()
            author.set_viewport_size({'width': 390, 'height': 844})
            author.goto(self.origin + '/inbox?tab=notifications')
            expect(author.get_by_role('heading', name='通知与待办', exact=True)).to_be_visible()
            assert author.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
            author.screenshot(path=str(self.state / 'inbox-mobile.png'), full_page=True)
            self.passed('browser_source_tasks_history_plans_and_390px_inbox')
            admin_context, admin = login('admin')
            admin.goto(self.origin + '/inbox?tab=todos')
            expect(admin.get_by_text('报表待审批', exact=False).first).to_be_visible()
            admin.get_by_role('button', name='去处理', exact=True).first.click()
            expect(admin.get_by_role('dialog')).to_be_visible()
            assert f'report={report["id"]}' in admin.url
            self.passed('live_todo_opens_frozen_report_review')
            admin.goto(self.origin + '/inbox?tab=notifications')
            admin.get_by_role('button', name='当前通知全部已读', exact=True).click()
            self.wait(lambda: self.api('GET', '/inbox')['unread'] == 0)
            assert self.api('GET', '/inbox/todos')['total'] == 1
            select_workspace(admin, '隔离验收空间')
            expect(admin.get_by_text('暂无通知', exact=True)).to_be_visible()
            expect(admin.get_by_role('tab', name='待我处理（0）', exact=True)).to_be_visible()
            select_workspace(admin, '默认工作空间')
            self.passed('browser_workspace_switch_clears_inbox_and_pending_counts')
            admin.goto(self.origin + '/settings?section=operations')
            expect(admin.get_by_role('heading', name='运行与备份', exact=True)).to_be_visible()
            expect(admin.get_by_text('审计留存：自动清理关闭', exact=False)).to_be_visible()
            self.passed('browser_read_highwater_keeps_pending_work_and_platform_operations')
            assert not errors and not violations, {'page_errors': errors, 'csp_violations': violations}
            self.result['browser'] = {'page_errors': 0, 'csp_violations': 0, 'widths': [1440, 390], 'mocked_business_responses': 0}
            author_context.close(); admin_context.close(); browser.close()

    def backup_config(self, name, user, password):
        volume = self.prefix + '-' + name
        self.volume(volume)
        services = ''.join(f'[{service}]\nhost=postgres\nport=5432\nuser={user}\ndbname={database}\nsslmode=disable\n'
                           for service, database in [('dbhub', 'production_meta'), ('restore', 'automation_restored')])
        escape = lambda value: value.replace('\\', '\\\\').replace(':', '\\:')
        pgpass = ':'.join(escape(value) for value in ['postgres', '5432', '*', user, password]) + '\n'
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode='w') as target:
            for path, value in [('pg_service.conf', services), ('pgpass', pgpass)]:
                raw = value.encode(); entry = tarfile.TarInfo(path); entry.size = len(raw); entry.mode = 0o600
                target.addfile(entry, io.BytesIO(raw))
        self.docker('run', '--rm', '-i', '--network', 'none', '--mount', f'type=volume,src={volume},dst=/private',
                    'postgres:16-alpine', 'sh', '-ec', 'tar -xf - -C /private; chmod 700 /private; chmod 600 /private/*; chown -R 65532:65532 /private', data=archive.getvalue())
        return volume

    def backup_once(self, config, want=0):
        self.backup_runs += 1; name = self.prefix + '-backup-' + str(self.backup_runs)
        self.run(name, '--read-only', '--cap-drop=ALL', '--security-opt', 'no-new-privileges', '--cpus=0.5', '--memory=128m',
                 '--tmpfs', '/var/lib/postgresql/data:rw,noexec,nosuid,size=1m,uid=65532,gid=65532,mode=0700',
                 '--mount', f'type=volume,src={self.backups},dst=/var/lib/dbhub/backups', '--mount', f'type=volume,src={config},dst=/config,readonly',
                 '-e', 'PGSERVICEFILE=/config/pg_service.conf', '-e', 'PGPASSFILE=/config/pgpass', '-e', 'DBHUB_BACKUP_ONCE=1',
                 '-e', 'DBHUB_BACKUP_INTERVAL_SECONDS=60', '-e', 'DBHUB_BACKUP_RETENTION_DAYS=1', self.backup_image)
        actual = int(self.docker('wait', name).stdout.strip())
        if actual != want:
            diagnostics = self.docker('logs', name).stdout.decode()
            diagnostics += self.docker('logs', name).stderr.decode()
            diagnostics += self.backup_files('cat /var/lib/dbhub/backups/.status-log /var/lib/dbhub/backups/.backup-log 2>/dev/null || true')
            for secret in [self.password, self.pg_password, self.reader_password]:
                diagnostics = diagnostics.replace(secret, '[redacted]')
            (self.state / 'backup-diagnostic.txt').write_text(diagnostics)
            print(json.dumps({'unexpected_backup_exit': actual, 'diagnostic': diagnostics[-1800:]}), flush=True)
        assert actual == want

    def backup_files(self, command):
        return self.docker('run', '--rm', '--network', 'none', '--read-only', '--cap-drop=ALL', '--mount', f'type=volume,src={self.backups},dst=/var/lib/dbhub/backups',
                           '--entrypoint', 'sh', self.backup_image, '-ec', command).stdout.decode().strip()

    def backups_and_restore(self):
        self.volume(self.backups)
        root = self.backup_config('backup-config', self.dbuser, self.pg_password)
        self.backup_once(root)
        digest = self.backup_files('cd /var/lib/dbhub/backups; sha256sum -c metadata-*.sha256')
        assert 'OK' in digest
        self.result['backup_image'] = self.docker('image', 'inspect', self.backup_image, '--format', '{{.Id}}').stdout.decode().strip()
        self.passed('nonroot_readonly_automatic_backup_checksum_and_status')
        limited, password = 'backup_probe_' + self.suffix, secrets.token_hex(24)
        self.sql(f"CREATE ROLE {limited} LOGIN BYPASSRLS PASSWORD '{password}'; GRANT USAGE ON SCHEMA public TO {limited}; GRANT SELECT,INSERT,UPDATE ON sys_operations TO {limited}; GRANT INSERT,SELECT ON sys_audit_logs TO {limited}; GRANT USAGE,SELECT ON SEQUENCE sys_audit_logs_id_seq TO {limited};")
        bad = self.backup_config('limited-backup-config', limited, password)
        self.backup_once(bad, want=1)
        assert self.backup_files('cd /var/lib/dbhub/backups; sha256sum -c metadata-*.sha256') == digest
        assert self.sql("SELECT status||':'||(last_success_at IS NOT NULL)::text FROM sys_operations WHERE name='backup'") == 'failed:true'
        self.wait(lambda: any(a['code'] == 'backup_failed' and a['active'] for a in self.api('GET', '/platform/operations')['alerts']))
        self.passed('failed_dump_keeps_previous_archives_and_notifies_platform_admin')
        self.backup_once(root)
        self.wait(lambda: any(a['code'] == 'backup_failed' and not a['active'] for a in self.api('GET', '/platform/operations')['alerts']))
        self.passed('backup_recovery_resolves_alert_without_clearing_history')
        self.backup_files("cd /var/lib/dbhub/backups; sample=$(find . -maxdepth 1 -type f -name 'metadata-*.dump' | head -n 1); for n in 1 2 3; do name=metadata-20200101T000000Z-00000000-0000-0000-0000-00000000000$n.dump; cp \"$sample\" \"$name\"; sha256sum \"$PWD/$name\" >\"$name.sha256\"; touch -t 202001010000 \"$name\"; done; printf preserve > manual-preserve.dump; ln -s manual-preserve.dump metadata-20190101T000000Z-00000000-0000-0000-0000-000000000001.dump")
        self.backup_once(root)
        assert self.backup_files("find /var/lib/dbhub/backups -maxdepth 1 -type f -name 'metadata-202001*.dump'") == ''
        self.backup_files("find /var/lib/dbhub/backups -maxdepth 1 -type f -name 'metadata-*.dump' -exec touch -t 202001010000 {} +")
        self.backup_once(root)
        assert self.backup_files("find /var/lib/dbhub/backups -maxdepth 1 -type f -name 'metadata-*.dump' | wc -l") == '2'
        assert self.backup_files('cat /var/lib/dbhub/backups/manual-preserve.dump') == 'preserve'
        assert self.backup_files('test -L /var/lib/dbhub/backups/metadata-20190101T000000Z-00000000-0000-0000-0000-000000000001.dump; echo preserved') == 'preserved'
        self.passed('retention_keeps_two_successful_archives_and_skips_foreign_files_and_symlinks')
        self.docker('stop', '-t', '30', self.app); self.docker('rm', '-f', self.app)
        verify = (ROOT / 'scripts/ops/verify-metadata.sql').read_text(); expected = self.sql(verify)
        self.backup_once(root)
        archive = self.backup_files("find /var/lib/dbhub/backups -maxdepth 1 -type f -name 'metadata-*.dump' | sort -r | head -n 1")
        self.sql('CREATE DATABASE automation_restored;')
        self.docker('run', '--rm', '--network', self.network, '--user', '65532:65532', '--read-only', '--cap-drop=ALL',
                    '--mount', f'type=volume,src={root},dst=/config,readonly', '--mount', f'type=volume,src={self.backups},dst=/var/lib/dbhub/backups,readonly',
                    '-e', 'PGSERVICEFILE=/config/pg_service.conf', '-e', 'PGPASSFILE=/config/pgpass',
                    '--entrypoint', 'sh', self.backup_image, '/ops/restore-metadata.sh', 'restore', archive)
        assert self.sql(verify, 'automation_restored') == expected
        self.backend(self.prefix + '-restored-backend', 'automation_restored', self.audit)
        self.wait_ready(); self.validate_restored_objects()
        assert self.api('GET', '/source-tasks', actor='author')['total'] >= 3
        assert self.api('GET', '/source-check-history', actor='author')['total'] >= 2
        assert self.api('GET', '/inbox/todos')['total'] == 1
        self.passed('automatic_archive_restores_20_digests_jobs_inbox_encryption_and_live_reports')


def main():
    runtime = AutomationRuntime()
    try:
        runtime.start(); runtime.fixture(); runtime.source_jobs()
        runtime.api('POST', '/workspaces', {'request_id': key(), 'name': '隔离验收空间'})
        runtime.browser(); runtime.backups_and_restore()
        # Exercise cleanup of an inherited image VOLUME as well as the normal
        # tmpfs configuration; this container never receives credentials.
        runtime.run(runtime.prefix + '-anonymous-cleanup-probe', '--read-only', '--cap-drop=ALL',
                    '--entrypoint', 'sh', runtime.backup_image, '-ec', 'exit 0')
        runtime.result['completed'] = True
    finally:
        runtime.cleanup()


if __name__ == '__main__':
    main()
