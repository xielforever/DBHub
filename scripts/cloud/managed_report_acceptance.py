"""Run production-image acceptance in an owned schema, then remove all fixtures.

Requires the existing Cloud PostgreSQL service and prebuilt managed-reports images.
The schema, DB role, containers, network and audit volume are unique to this run.
"""
import json
import os
import secrets
import shutil
import tempfile
import socket
import subprocess
import time
from pathlib import Path
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

repo = Path(__file__).resolve().parents[2]
state = Path(os.environ.get('DBHUB_STATE_DIR', '/workspace/dbhub-cloud'))
suffix = secrets.token_hex(6)
schema, role = 'managed_accept_' + suffix, 'managed_reader_' + suffix
backend, frontend, browser = ['dbhub-managed-' + name + '-' + suffix for name in ['backend', 'frontend', 'browser']]
network, volume = 'dbhub-managed-net-' + suffix, 'dbhub-managed-audit-' + suffix
artifacts = state / 'managed-report-artifacts' / suffix
artifacts.mkdir(parents=True, mode=0o700)
env_path = artifacts / 'runtime.env'
backend_image = os.environ.get('DBHUB_MANAGED_BACKEND_IMAGE', 'dbhub-backend:managed-reports')
frontend_image = os.environ.get('DBHUB_MANAGED_FRONTEND_IMAGE', 'dbhub-frontend:managed-reports')
owned, fixture_created = [], False
browser_work = Path(tempfile.mkdtemp(prefix='dbhub-managed-browser-',dir=os.environ.get('DBHUB_BROWSER_TMPDIR','/tmp')))
for name in ['tmp','cache','apt-lists','apt-cache']:
    (browser_work/name).mkdir(mode=0o755)
(browser_work/'tmp').chmod(0o1777)
report = {'schema': schema, 'role': role, 'containers': [backend, frontend, browser], 'network': network, 'audit_volume': volume, 'artifacts': str(artifacts), 'completed': False, 'browser_work_dir': str(browser_work)}
record = artifacts / 'runtime.json'
record.write_text(json.dumps(report, indent=2))

def docker(*args, check=True, **kwargs):
    return subprocess.run(['docker', *args], check=check, capture_output=True, text=True, **kwargs)

def pg(statement):
    result = subprocess.run([str(repo / 'scripts/cloud/dev.sh'), 'exec', '-T', 'postgres', 'sh', '-ec', 'psql -X -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'], input=statement, text=True, capture_output=True)
    if result.returncode: raise RuntimeError('owned acceptance schema operation failed; inspect PostgreSQL readiness')

def remember(kind, name):
    owned.append((kind, name))

try:
    values = {}
    with (state / 'runtime.env').open() as source:
        for line in source:
            if '=' in line:
                key, value = line.rstrip('\n').split('=', 1)
                values[key] = value
    password, db_password = 'Acceptance9!' + secrets.token_hex(20), secrets.token_hex(24)
    pg(f"""BEGIN;
CREATE SCHEMA {schema};
CREATE ROLE {role} LOGIN PASSWORD '{db_password}';
CREATE TABLE {schema}.inventory_rows(category TEXT,amount NUMERIC(18,2),scope TEXT);
INSERT INTO {schema}.inventory_rows VALUES('A',12.34,'allowed'),('B',8.50,'allowed'),('private',900,'hidden');
CREATE VIEW {schema}.inventory_report AS SELECT category,amount FROM {schema}.inventory_rows WHERE scope='allowed';
GRANT USAGE ON SCHEMA {schema} TO {role};
GRANT SELECT ON {schema}.inventory_report TO {role};
COMMIT;""")
    fixture_created = True
    uri = urlsplit(values['DATABASE_URL'])
    query = dict(parse_qsl(uri.query))
    query['options'] = (query.get('options', '') + ' -csearch_path=' + schema).strip()
    values.update(DATABASE_URL=urlunsplit((uri.scheme, uri.netloc, uri.path, urlencode(query), uri.fragment)),
        APP_ADMIN_USERNAME='report_acceptance_admin', APP_ADMIN_PASSWORD=password, APP_SECRET_KEY=secrets.token_hex(32),
        APP_ENV='production', DBHUB_TARGET_ALLOWLIST='postgres:5432',
        REPORT_ACCEPTANCE_PASSWORD=password, REPORT_ACCEPTANCE_SCHEMA=schema,
        REPORT_ACCEPTANCE_DB_USER=role, REPORT_ACCEPTANCE_DB_PASSWORD=db_password,
        DBHUB_REPORT_API_BASE='http://backend:8080/api/v1', DBHUB_REPORT_FRONTEND='http://frontend',
        DBHUB_BROWSER_TEST_SCRIPT='/app/scripts/cloud/managed_report_browser_test.py', DBHUB_RUN_REPORT_REGRESSION='1')
    fd = os.open(env_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as target:
        for key, value in values.items(): target.write(key + '=' + value + '\n')
    cloud_network = os.environ.get('DBHUB_COMPOSE_PROJECT', 'dbhub-cloud') + '_default'
    for kind, name in [('container', backend), ('container', frontend), ('container', browser), ('network', network), ('volume', volume)]:
        if docker(kind, 'inspect', name, check=False).returncode == 0: raise RuntimeError('refusing to overwrite an existing acceptance resource')
    docker('network', 'create', '--label', 'dbhub.task=managed-report-acceptance', network); remember('network', network)
    docker('volume', 'create', '--label', 'dbhub.task=managed-report-acceptance', volume); remember('volume', volume)
    remember('container', backend)
    docker('run', '-d', '--name', backend, '--label', 'dbhub.task=managed-report-acceptance', '--network', cloud_network, '--env-file', str(env_path), '--read-only', '--cap-drop=ALL', '--security-opt', 'no-new-privileges', '--cpus=1.5', '--memory=512m', '--mount', 'type=volume,src=' + volume + ',dst=/var/lib/dbhub/audit', backend_image)
    docker('network', 'connect', '--alias', 'backend', network, backend)
    remember('container', frontend)
    docker('run', '-d', '--name', frontend, '--label', 'dbhub.task=managed-report-acceptance', '--network', network, '--network-alias', 'frontend', '--cpus=0.5', '--memory=128m', frontend_image)
    for _ in range(50):
        if docker('exec', backend, './healthcheck', check=False).returncode == 0 and docker('exec', frontend, 'wget', '-Y', 'off', '-q', '-O', '/dev/null', 'http://127.0.0.1/api/ready', check=False).returncode == 0: break
        time.sleep(1)
    else:
        # Logs contain startup diagnostics, never the environment-file contents.
        (artifacts / 'backend-startup.log').write_text(docker('logs', backend, check=False).stderr)
        raise RuntimeError('production readiness failed; see the private acceptance artifacts')
    ca = os.environ.get('CODEX_PROXY_CERT') or os.environ.get('SSL_CERT_FILE') or '/etc/ssl/certs/ca-certificates.crt'
    proxy = urlsplit(os.environ.get('HTTPS_PROXY') or os.environ.get('HTTP_PROXY') or 'http://proxy:8080')
    command = ['docker', 'run', '--name', browser, '--label', 'dbhub.task=managed-report-acceptance', '--network', network, '--env-file', str(env_path), '--cpus=2', '--memory=1536m', '--shm-size=256m', '--mount', 'type=bind,src=' + str(browser_work/'tmp') + ',dst=/tmp', '--mount', 'type=bind,src=' + str(browser_work/'cache') + ',dst=/root/.cache', '--mount', 'type=bind,src=' + str(browser_work/'apt-lists') + ',dst=/var/lib/apt/lists', '--mount', 'type=bind,src=' + str(browser_work/'apt-cache') + ',dst=/var/cache/apt', '--add-host', proxy.hostname + ':' + socket.gethostbyname(proxy.hostname), '-e', 'NO_PROXY=backend,frontend,postgres,localhost,127.0.0.1', '--mount', 'type=bind,src=' + str(repo) + ',dst=/app,readonly', '--mount', 'type=bind,src=' + str(artifacts) + ',dst=/runtime', '--mount', 'type=bind,src=' + ca + ',dst=/run/cloud-ca.pem,readonly', 'debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587', 'sh', '/app/scripts/cloud/browser-runtime.sh']
    remember('container', browser)
    if shutil.disk_usage('/').free < 1536*1024*1024: raise RuntimeError('at least 1.5 GiB free disk is required before browser acceptance')
    result = subprocess.run(command)
    report.update(backend_image=docker('image', 'inspect', backend_image, '--format', '{{.Id}}').stdout.strip(), frontend_image=docker('image', 'inspect', frontend_image, '--format', '{{.Id}}').stdout.strip(), backend_uid='65532:65532', backend_readonly_root=True, target_allowlist=['postgres:5432'], completed=result.returncode == 0)
    if result.returncode: raise RuntimeError('formal reporting browser acceptance failed')
finally:
    cleanup_errors = []
    if docker('container','inspect',backend,check=False).returncode == 0:
        logs = docker('logs',backend,check=False)
        (artifacts/'backend-runtime.log').write_text(logs.stdout+logs.stderr)
    for kind, name in reversed(owned):
        if kind == 'container':
            docker('stop', '-t', '15', name, check=False)
            result = docker('rm', '-f', name, check=False)
        elif kind == 'volume': result = docker('volume', 'rm', name, check=False)
        else: result = docker('network', 'rm', name, check=False)
        if result.returncode and docker(kind, 'inspect', name, check=False).returncode == 0: cleanup_errors.append(kind + ':' + name)
    if fixture_created:
        try: pg(f'DROP SCHEMA {schema} CASCADE; DROP ROLE {role};')
        except Exception: cleanup_errors.append('schema/role:' + schema)
    if env_path.exists(): env_path.unlink()
    if docker('container','inspect',browser,check=False).returncode != 0:
        try:
            # Package installation creates files as container root. Normalize
            # ownership only inside this run's private work directory; -h does
            # not follow the virtualenv's links to files outside the mount.
            docker('run','--rm','--network','none','--mount','type=bind,src='+str(browser_work)+',dst=/work','debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587','chown','-Rh',str(os.getuid())+':'+str(os.getgid()),'/work')
            shutil.rmtree(browser_work)
        except Exception: cleanup_errors.append('browser-work:'+str(browser_work))
    else: cleanup_errors.append('browser-work:'+str(browser_work))
    report['cleanup_errors'] = cleanup_errors
    record.write_text(json.dumps(report, indent=2))
    print(json.dumps(report), flush=True)
    if cleanup_errors: raise RuntimeError('owned acceptance cleanup requires attention')
