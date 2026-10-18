#!/usr/bin/env python3
"""Repeatable cloud regressions. Raw logs stay private; summary.json is shareable."""
import argparse
import datetime as dt
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import shlex
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
DEV = './scripts/cloud/dev.sh'
SMOKE_TESTS = (
    'TestWorkspaceIsolation', 'TestSourceAudienceAndDirectory',
    'TestDatasourceReliability', 'TestMetadataSyncReliability',
    'TestExecutionReliability', 'TestAuditDatabaseFailureAndRestart',
    'TestAuditLostAcknowledgementIsIdempotent', 'TestManagedReportLifecycle',
    'TestManagedReportApprovalBoundaries', 'TestDashboardPhase1Lifecycle',
    'TestReviewFixTransactionRechecksSourceBeforeCommit',
    'TestReviewFixPublishedAudienceVersionCanBeRestored',
    'TestReviewFixDashboardBeneficiaryCheckedAtEveryGate',
    'TestConnectorJDBCRealHTTP', 'TestAutomationInbox', 'TestAutomationSourceTasks',
    'TestMembershipRevokeRestoreInvalidatesOldQueryAndTransaction',
    'TestReadinessTracksSessionDependencyRecovery',
    'TestSecurityAuditIndependentApprovals', 'TestSecurityAuditTicketTargetCannotDrift',
    'TestSecurityAuditLegacyShareOwnership', 'TestSecurityAuditHistoryDeletionIsPersonal',
    'TestSecurityAuditLegacyDashboardOwnership',
    'TestSecurityAuditNetworkDependencyDeletion', 'TestSecurityAuditSSHTestHonorsAllowlist',
    'TestSecurityAuditRedisRevocationBeforeDispatch',
    'TestSecurityAuditReportBeneficiaryCannotBypassProposal',
    'TestSecurityAuditCookieMutationNeedsCSRFHeader', 'TestSecurityAuditStaleAdminIdentityContext',
    'TestSecurityAuditConcurrentIndependentDecisions',
    'TestSecurityAuditReportBenefitRecheckedAtEveryGate',
    'TestSecurityAuditOfflineGroupCannotSelfGrant',
)
OPTIONAL_GO_TESTS = {
    'TestJournalCrashHelper': 'Child-process helper, exercised by its parent tests',
    'TestVersionMatrixTarget': 'Run compatibility.py separately for the full version matrix',
    'TestOpenGaussDemoHTTP': 'Run opengauss-demo-check.py separately; commercial GaussDB still needs real acceptance',
}
OUTSIDE_SCOPE = [
    'Production HTTPS, network boundaries, secrets and managed PostgreSQL role provisioning',
    'Upgrade/rollback using a sanitized copy of the actual previous production metadata',
    'Off-site backups, application key/driver/audit volume recovery and agreed RPO/RTO',
    'Real business workload capacity, alert delivery and multi-instance failover',
    'Full datasource version matrix and commercial GaussDB topology/TLS acceptance',
    'Complete Go, JDBC/JRE and operating-system vulnerability scans with current advisory databases',
]


def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT).decode().strip()


def source_fingerprint():
    names = subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard'], cwd=ROOT)
    digest = hashlib.sha256()
    for name in sorted(set(names.split(b'\0')) - {b''}):
        path = ROOT / os.fsdecode(name)
        digest.update(name + b'\0')
        digest.update(hashlib.sha256(path.read_bytes()).digest() if path.is_file() else b'deleted')
    return digest.hexdigest()


def go_results(log, required=SMOKE_TESTS):
    passed, skipped, failures = set(), set(), set()
    count = 0
    for line in log.read_text(errors='replace').splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        name = event.get('Test')
        action = event.get('Action')
        if not name:
            continue
        if action == 'pass':
            count += 1
            if '/' not in name:
                passed.add(name)
        elif action == 'skip':
            skipped.add(name)
        elif action == 'fail':
            failures.add(name)
    missing = sorted(set(required) - passed)
    unexpected_skips = sorted(skipped - OPTIONAL_GO_TESTS.keys())
    result = dict(passed_events_including_subtests=count, skipped=sorted(skipped),
                  optional_skip_reasons={k: OPTIONAL_GO_TESTS[k] for k in sorted(skipped) if k in OPTIONAL_GO_TESTS},
                  missing_required_tests=missing, unexpected_skips=unexpected_skips, failed_tests=sorted(failures))
    if not count or missing or unexpected_skips or failures:
        raise ValueError('Backend coverage incomplete: ' + json.dumps(result, ensure_ascii=False))
    return result


def acceptance_result(paths):
    if len(paths) != 1:
        raise ValueError('Expected exactly one new acceptance report')
    path = paths[0]
    result = json.loads(path.read_text())
    cleaned = result.get('temporary_resources_removed', result.get('resources_removed'))
    if result.get('completed') is not True or cleaned is not True or not result.get('checks'):
        raise ValueError('Acceptance incomplete or temporary resources remain: ' + str(path))
    return dict(report=str(path), checks=len(result['checks']), resources_removed=True)


def frontend_results(log):
    # Node 24 chooses the spec reporter in Compose; CI may choose TAP.
    output = re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', log.read_text(errors='replace'))
    values = {key: [] for key in ['tests', 'pass', 'fail', 'cancelled', 'skipped', 'todo']}
    for key, count in re.findall(r'^(?:#|ℹ) (tests|pass|fail|cancelled|skipped|todo) (\d+)[ \t]*$', output, re.M):
        values[key].append(int(count))
    if (any(len(counts) != 4 for counts in values.values()) or
            any(count == 0 for count in values['tests']) or values['pass'] != values['tests'] or
            any(any(values[key]) for key in ['fail', 'cancelled', 'skipped', 'todo'])):
        raise ValueError('Expected four complete, passing frontend suites without skipped tests')
    return sum(values['pass'])


def commands(profile, suffix, soak_seconds, clients):
    test = ['go', 'test', '-race', '-p=4', '-json', './...', '-count=1', '-timeout=15m']
    if profile == 'smoke':
        test[5] = './internal/server'
        test += ['-run', '^(' + '|'.join(SMOKE_TESTS) + ')$']
    steps = [('dependencies', [DEV, 'up', '-d', '--wait', '--wait-timeout', '240', 'postgres', 'mysql', 'redis', 'jdbc']),
             ('backend', [DEV, 'run', '--rm', '--no-deps', 'backend', *test])]
    if profile != 'smoke':
        steps.append(('vet', [DEV, 'run', '--rm', '--no-deps', 'backend', 'go', 'vet', '-p=4', './...']))
    frontend = 'npm ci --no-audit --no-fund; '
    if profile != 'smoke':
        frontend += 'npm audit --audit-level=low; '
    frontend += '; '.join('npm run test:' + name for name in ['query', 'report', 'dashboard', 'automation'])
    if profile != 'smoke':
        frontend += '; npm run build'
    steps.append(('frontend', [DEV, 'run', '--rm', '--no-deps', 'frontend', 'sh', '-ec', frontend]))
    images = {name: 'dbhub-' + name + ':regression-' + suffix for name in ['backend', 'frontend', 'backup']}
    if profile == 'release':
        for name, context in [('backend', 'backend'), ('frontend', 'frontend'), ('backup', 'scripts/ops')]:
            cmd = ['docker', 'build', '--label', 'org.opencontainers.image.revision=' + git('rev-parse', 'HEAD'),
                   '-t', images[name], '-f', context + '/Dockerfile']
            ca = os.environ.get('CODEX_PROXY_CERT') or os.environ.get('SSL_CERT_FILE')
            if name == 'frontend' and ca:
                cmd += ['--secret', 'id=proxy_ca,src=' + ca]
            steps.append(('image-' + name, cmd + [context]))
        steps += [('dashboard', [sys.executable, 'scripts/cloud/dashboard_acceptance.py']),
                  ('security-browser', [sys.executable, 'scripts/cloud/security_acceptance.py']),
                  ('production', [sys.executable, 'scripts/cloud/production_acceptance.py', '--duration', str(soak_seconds), '--clients', str(clients)]),
                  ('automation', [sys.executable, 'scripts/cloud/automation_acceptance.py'])]
    return steps, images


def arguments(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--profile', choices=['smoke', 'full', 'release'], default='smoke')
    parser.add_argument('--soak-seconds', type=int, default=0, help='release only; 1800 for the real-clock release exercise')
    parser.add_argument('--clients', type=int, default=4, choices=range(1, 5))
    parser.add_argument('--plan', action='store_true', help='Print commands without starting services or writing artifacts')
    args = parser.parse_args(argv)
    if not 0 <= args.soak_seconds <= 7200 or (args.soak_seconds and args.profile != 'release'):
        parser.error('--soak-seconds must be 0–7200 and requires --profile release')
    return args


def main():
    args = arguments()
    commit, dirty = git('rev-parse', 'HEAD'), bool(git('status', '--porcelain'))
    suffix = commit[:12] + '-' + secrets.token_hex(3)
    steps, images = commands(args.profile, suffix, args.soak_seconds, args.clients)
    if args.plan:
        for name, command in steps:
            print(name + ': ' + shlex.join(command))
        return 0
    if args.profile == 'release' and dirty:
        print('Release regression requires a clean, committed checkout.', file=sys.stderr)
        return 1
    if args.profile == 'release' and importlib.util.find_spec('playwright') is None:
        print('Release regression requires Playwright 1.62.0 and Chromium; see scripts/cloud/README.md.', file=sys.stderr)
        return 1
    state = Path(os.environ.get('DBHUB_STATE_DIR', '/workspace/dbhub-cloud')).resolve()
    state.mkdir(parents=True, exist_ok=True, mode=0o700)
    with (state / 'regression.lock').open('w') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print('Another regression is running in this state directory.', file=sys.stderr)
            return 1
        run = state / 'regression-artifacts' / (dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '-' + suffix)
        run.mkdir(parents=True, mode=0o700)
        report = dict(schema_version=1, source_commit=commit, source_changed=dirty, source_sha256=source_fingerprint(),
                      profile=args.profile, completed=False, production_approval='not_evaluated', steps=[],
                      sustained_load=dict(executed=False, requested_seconds=args.soak_seconds), outside_scope=OUTSIDE_SCOPE,
                      not_executed=['Production-image browser/fault/backup acceptance'] if args.profile != 'release' else [])
        summary = run / 'summary.json'

        def save():
            summary.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')

        env = {**os.environ, 'DBHUB_STATE_DIR': str(state), 'DBHUB_DASHBOARD_USE_IMAGE': '1',
               'DBHUB_DASHBOARD_BACKEND_IMAGE': images['backend'], 'DBHUB_DASHBOARD_FRONTEND_IMAGE': images['frontend'],
               'DBHUB_BACKUP_IMAGE': images['backup']}
        save()
        try:
            for name, command in steps:
                item = dict(name=name, status='running')
                report['steps'].append(item)
                save()
                log, started = run / (name + '.log'), time.monotonic()
                folder = state / ('dashboard-artifacts' if name == 'dashboard' else 'production-acceptance')
                previous = set(folder.glob('*/checks.json'))
                print('RUN ' + name, flush=True)
                with log.open('w') as stream:
                    result = subprocess.run(command, cwd=ROOT, env=env, stdout=stream, stderr=subprocess.STDOUT)
                item.update(exit_code=result.returncode, seconds=round(time.monotonic() - started, 2), status='failed')
                if name == 'backend':
                    try:
                        item['coverage'] = go_results(log)
                    except ValueError as error:
                        item['coverage_error'] = str(error)
                        raise
                if result.returncode:
                    raise RuntimeError(name + ' failed; inspect private log ' + str(log))
                if name == 'frontend':
                    item['tests_passed'] = frontend_results(log)
                elif name in ('dashboard', 'security-browser', 'production', 'automation'):
                    item.update(acceptance_result(sorted(set(folder.glob('*/checks.json')) - previous)))
                    if name == 'production':
                        load = json.loads(Path(item['report']).read_text()).get('load', {})
                        if args.soak_seconds and (load.get('executed') is not True or load.get('elapsed_seconds', 0) < args.soak_seconds):
                            raise ValueError('Requested sustained load was not completed')
                        report['sustained_load'] = load
                elif name.startswith('image-'):
                    item['image_id'] = subprocess.check_output(['docker', 'image', 'inspect', images[name[6:]], '--format', '{{.Id}}'], text=True).strip()
                    item['image_tag'] = images[name[6:]]
                item['status'] = 'passed'
                save()
                print('PASS ' + name + ' (' + str(item['seconds']) + 's)', flush=True)
            if source_fingerprint() != report['source_sha256']:
                raise ValueError('Repository files changed during regression; rerun on a stable checkout')
            report['completed'] = True
            return 0
        except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
            report['error'] = str(error)
            print(str(error), file=sys.stderr)
            return 1
        except KeyboardInterrupt:
            report['error'] = 'Interrupted; check owned acceptance resources before retrying'
            return 130
        finally:
            if report['steps'] and report['steps'][-1]['status'] == 'running':
                report['steps'][-1]['status'] = 'failed'
            save()
            print('Regression summary: ' + str(summary), flush=True)


if __name__ == '__main__':
    sys.exit(main())
