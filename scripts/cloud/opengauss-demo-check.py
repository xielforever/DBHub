#!/usr/bin/env python3
"""Host orchestration only: build/run/tests execute in existing Docker services.

Pins a community openGauss 6.0.0 single-node PG-mode demo, bounds resources,
and destroys its own container, data and newly pulled image in finally.
The commercial GaussDB product and HA/distributed modes remain unverified.
"""
import json
import os
from pathlib import Path
import secrets
import signal
from datetime import datetime, timezone
import shutil
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
IMAGE = 'enmotech/opengauss@sha256:6ce3ec3d591e721882c611fa26de1e56ad35ca63304c789267ed52cc6cbca70d'
NAME = 'dbhub-opengauss-demo'
STATE = Path(os.environ.get('DBHUB_STATE_DIR', '/workspace/dbhub-cloud'))

def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)

def cleanup(directory, remove_image):
    directory = Path(directory)
    if directory.is_symlink() or directory.resolve().parent != Path('/tmp') or not directory.name.startswith('dbhub-opengauss-demo'):
        raise RuntimeError('refusing cleanup outside the owned demo directory')
    inspect = subprocess.run(['docker', 'inspect', NAME, '--format', '{{index .Config.Labels "dbhub.fixture"}}'], capture_output=True, text=True)
    if inspect.returncode == 0:
        if inspect.stdout.strip() != 'opengauss-demo':
            raise RuntimeError('refusing to remove an unrelated container')
        run(['docker', 'rm', '-fv', NAME], stdout=subprocess.DEVNULL)
    if (directory / 'data').exists():
        # The database changes ownership to UID 70. Use an isolated helper with
        # just this owned bind mount to remove those files; no Docker prune.
        run(['docker', 'run', '--rm', '--network', 'none', '--mount', f'type=bind,src={directory},dst=/fixture', '--entrypoint', '/bin/sh', IMAGE, '-ec', 'rm -rf /fixture/data'], stdout=subprocess.DEVNULL)
    shutil.rmtree(directory, ignore_errors=False)
    (STATE / 'opengauss-demo.json').unlink(missing_ok=True)
    if remove_image and subprocess.run(['docker', 'image', 'inspect', IMAGE], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
        run(['docker', 'image', 'rm', IMAGE], stdout=subprocess.DEVNULL)

def main():
    if (STATE / 'opengauss-demo.json').exists():
        raise SystemExit('existing fixture file found; refusing to overwrite it')
    if subprocess.run(['docker', 'inspect', NAME], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
        raise SystemExit('demo container already exists; refusing to adopt or overwrite it')
    image_exists = subprocess.run(['docker', 'image', 'inspect', IMAGE], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
    if shutil.disk_usage('/').free < (2 if image_exists else 4) * 1024**3 or shutil.disk_usage('/tmp').free < 1024**3:
        raise SystemExit('insufficient temporary disk capacity')
    directory = Path(tempfile.mkdtemp(prefix='dbhub-opengauss-demo-', dir='/tmp'))
    report = {'started_at': datetime.now(timezone.utc).isoformat(), 'driver': 'org.opengauss:opengauss-jdbc:6.0.0-og', 'product': 'community openGauss', 'mode': 'single node, PG compatibility', 'image': IMAGE, 'limits': {'memory_mib': 1536, 'cpus': 2, 'pids': 256}, 'commercial_gaussdb': 'unverified'}
    code = 1
    try:
        with (STATE / 'opengauss-demo-pull.log').open('w') as log:
            run(['docker', 'pull', '--platform', 'linux/amd64', IMAGE], stdout=log, stderr=subprocess.STDOUT)
        password = 'Aa9!' + secrets.token_hex(10)
        (directory / 'password').write_text(password + '\n'); (directory / 'password').chmod(0o600)
        (directory / 'data').mkdir(mode=0o700)
        run(['docker', 'run', '-d', '--name', NAME, '--label', 'dbhub.fixture=opengauss-demo', '--network', 'dbhub-cloud_default', '--network-alias', 'dbhub-gauss-demo', '--cap-add', 'SYS_NICE', '--cpus', '2', '--memory', '1536m', '--memory-swap', '1536m', '--pids-limit', '256', '--shm-size', '256m', '--ulimit', 'nofile=65535:65535', '--mount', f'type=bind,src={directory}/data,dst=/var/lib/opengauss/data', '--mount', f'type=bind,src={directory}/password,dst=/run/demo/password,readonly', '--mount', f'type=bind,src={ROOT}/scripts/cloud/opengauss-demo-init.sh,dst=/run/demo/init.sh,readonly', '--entrypoint', '/bin/sh', IMAGE, '/run/demo/init.sh'], stdout=subprocess.DEVNULL)
        shell = 'export GAUSSHOME=/usr/local/opengauss; export PATH="$GAUSSHOME/bin:$PATH" LD_LIBRARY_PATH="$GAUSSHOME/lib"; exec gosu omm gsql -d postgres -v ON_ERROR_STOP=1 -At'
        for _ in range(90):
            result = subprocess.run(['docker', 'exec', '-i', NAME, '/bin/sh', '-ec', shell], input=b'SELECT 1;\n', capture_output=True)
            # Initial-password expiry is expected; the server is ready to accept
            # ALTER ROLE through its local administration socket.
            if result.returncode == 0 or b'ALTER ROLE' in result.stderr:
                break
            time.sleep(1)
        else:
            raise RuntimeError('demo readiness timed out (inspect private container logs)')
        admin_password = 'Bb8!' + secrets.token_hex(10)
        sql = f"ALTER ROLE omm PASSWORD '{admin_password}'; CREATE USER demo_writer WITH LOGIN PASSWORD '{password}'; GRANT CREATE ON SCHEMA public TO demo_writer;\n"
        result = subprocess.run(['docker', 'exec', '-i', NAME, '/bin/sh', '-ec', shell], input=sql.encode(), capture_output=True)
        if result.returncode:
            raise RuntimeError('isolated role initialization failed; secret SQL output suppressed')
        version = run(['docker', 'exec', '-i', NAME, '/bin/sh', '-ec', shell], input=b'SELECT version();\n', capture_output=True)
        report['server_version'] = version.stdout.decode().strip()
        fixture = STATE / 'opengauss-demo.json'
        fd = os.open(fixture, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as file:
            json.dump({'Host': 'dbhub-gauss-demo', 'Password': password}, file)
        (STATE / 'opengauss-demo-test.json.log').unlink(missing_ok=True)
        with (STATE / 'opengauss-demo-test-command.log').open('w') as log:
            result = subprocess.run([str(ROOT / 'scripts/cloud/dev.sh'), 'exec', '-T', 'backend', 'sh', '-ec', 'DBHUB_OPENGAUSS_FIXTURE=/runtime/opengauss-demo.json go test -timeout 180s -race -p=2 -json ./internal/server -run TestOpenGaussDemoHTTP > /runtime/opengauss-demo-test.json.log'], cwd=ROOT, stdout=log, stderr=subprocess.STDOUT)
        events = [json.loads(line) for line in (STATE / 'opengauss-demo-test.json.log').read_text().splitlines()]
        report['passed_test_items'] = [e['Test'] for e in events if e.get('Action') == 'pass' and 'Test' in e]
        report['failed_test_items'] = [e.get('Test', e.get('Package')) for e in events if e.get('Action') in ('fail', 'build-fail')]
        code = result.returncode
        report['test_exit_code'] = code
        report['observed_memory_usage'] = run(['docker','stats','--no-stream','--format','{{.MemUsage}}',NAME],capture_output=True,text=True).stdout.strip()
        if not report['passed_test_items'] or report['failed_test_items']:
            code = code or 1
    finally:
        cleanup(directory, remove_image=not image_exists)
        report['cleanup'] = {'container_removed': True, 'data_removed': not directory.exists(), 'new_image_removed': not image_exists, 'fixture_secret_removed': not (STATE / 'opengauss-demo.json').exists()}
        report['finished_at'] = datetime.now(timezone.utc).isoformat()
        (STATE / 'opengauss-demo-report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    raise SystemExit(code)

def stop(signum, frame):
    raise SystemExit(128 + signum)

if __name__ == '__main__':
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGHUP, stop)
    main()
