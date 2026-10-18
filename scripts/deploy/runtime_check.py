#!/usr/bin/env python3
"""Isolated Docker/source runtime checks, including Kubernetes-like UID/volume permissions.

This is not a Kubernetes cluster installation test. All synthetic resources and
credentials are removed, including the deployment's deliberately retained audit volume.
"""
import json
import os
import secrets
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts/cloud'))
from production_acceptance import Runtime, key
sys.path.insert(0, str(ROOT / 'scripts/deploy'))
from check import manifests


def main():
    rt = Runtime()
    managed = rt.prefix + '-cli'
    network, audit = managed + '-network', managed + '-audit'
    rt.owned += [('network', network), ('volume', audit)]
    bundle = Path(os.environ['DBHUB_SOURCE_BUNDLE']).resolve()
    frontend_image = rt.frontend_image
    try:
        rt.docker('network', 'create', rt.network);rt.owned.append(('network', rt.network))
        env = rt.environment('dependencies')
        rt.volume(rt.prefix + '-pg-data')
        rt.run(rt.pg, '--network-alias', 'postgres', '--env-file', str(env), '--memory=384m',
               '--mount', f'type=volume,src={rt.prefix}-pg-data,dst=/var/lib/postgresql/data', 'postgres:16-alpine')
        rt.pg_ready()
        rt.run(rt.redis, '--network-alias', 'redis', '--memory=64m', 'redis:7-alpine')
        rt.docker('network', 'create', '--label', 'io.dbhub.deployment=' + managed, network)
        rt.docker('network', 'connect', '--alias', 'postgres', network, rt.pg)
        rt.docker('network', 'connect', '--alias', 'redis', network, rt.redis)
        config = rt.environment('docker', DBHUB_BACKEND_IMAGE=rt.backend_image, DBHUB_FRONTEND_IMAGE=frontend_image)
        manager = [sys.executable, str(ROOT / 'deploy/docker/manage.py')]
        for component in ['jdbc', 'backend', 'frontend']:
            rt.owned.append(('container', managed + '-' + component))

        def manage(action):
            args = ['--env-file', str(config), '--port', str(rt.port)] if action == 'up' else []
            subprocess.run([*manager, action, '--name', managed, *args], check=True)

        manage('up')
        rt.wait_ready();rt.login('admin', 'release_admin')
        connection = rt.api('POST', '/connections', {'name': 'isolated deployment test', 'type': 'postgres',
                            'host': 'postgres', 'port': 5432, 'database': 'production_meta', 'username': rt.dbuser,
                            'password': rt.pg_password, 'ssl_mode': 'disable', 'environment': 'test'}, want=201)
        result = rt.api('POST', '/query/execute', {'connection_id': connection['id'], 'sql': 'SELECT 1 AS value', 'request_id': key()})
        assert result['rows'] == [[1]]
        rt.passed('docker_launcher_real_login_and_sql')
        refused = subprocess.run([*manager, 'up', '--name', managed, '--env-file', str(config)], capture_output=True)
        assert refused.returncode != 0
        manage('down')
        assert rt.docker('volume', 'inspect', audit, check=False).returncode == 0
        manage('up');rt.wait_ready()
        assert rt.api('GET', '/auth/me')['username'] == 'release_admin'
        rt.passed('controlled_restart_preserves_audit_volume_metadata_and_session')
        manage('down')

        # Model a CSI volume root owned by root:fsGroup, not by the application UID.
        pvc = rt.prefix + '-pvc';rt.volume(pvc)
        rt.docker('run', '--rm', '--user', '0:0', '--entrypoint', 'sh',
                  '--mount', f'type=volume,src={pvc},dst=/fixture', rt.backend_image,
                  '-ec', 'chown 0:65532 /fixture; chmod 2770 /fixture; touch /fixture/.initialized')
        helm_backend = rt.prefix + '-helm-backend'
        rt.run(helm_backend, '--network-alias', 'check-dbhub-backend', '--env-file', str(env),
               '--read-only', '--user', '65532:65532', '--cap-drop=ALL',
               '--mount', 'type=volume,src=' + pvc + ',dst=/var/lib/dbhub', rt.backend_image)
        nginx = next(item['data']['default.conf'] for item in manifests() if item['kind'] == 'ConfigMap' and 'default.conf' in item['data'])
        conf = rt.state / 'helm-nginx.conf';conf.write_text(nginx);conf.chmod(0o644)
        helm_front = rt.prefix + '-helm-front'
        rt.run(helm_front, '--publish', f'127.0.0.1:{rt.port}:8080', '--user', '101:101', '--read-only', '--cap-drop=ALL',
               '--entrypoint', 'nginx', '--tmpfs', '/var/cache/nginx:uid=101,gid=101,size=64m',
               '--tmpfs', '/var/run:uid=101,gid=101,size=8m', '--mount', f'type=bind,src={conf},dst=/etc/nginx/conf.d/default.conf,readonly',
               frontend_image, '-g', 'daemon off;')
        rt.wait_ready()
        assert rt.raw('GET', '/')[0] == 200
        rt.docker('exec', helm_backend, 'test', '-d', '/var/lib/dbhub/audit')
        rt.passed('helm_nginx_nonroot_and_csi_audit_directory_permissions_on_docker')
        rt.docker('stop', '-t', '15', helm_front);rt.docker('rm', helm_front)
        rt.docker('stop', '-t', '15', helm_backend);rt.docker('rm', helm_backend)

        source = rt.prefix + '-source'
        source_env = rt.environment('source', HTTP_BIND_HOST='127.0.0.1')
        rt.run(source, '--env-file', str(source_env), '--entrypoint', '/opt/dbhub/current/bin/dbhub',
               '--mount', f'type=bind,src={bundle},dst=/opt/dbhub/current,readonly',
               '--mount', f'type=volume,src={pvc},dst=/var/lib/dbhub', rt.backend_image)
        for _ in range(60):
            status = rt.docker('exec', source, '/opt/dbhub/current/bin/healthcheck', check=False)
            if status.returncode == 0:break
            import time;time.sleep(0.5)
        else:raise AssertionError('Source binary did not become ready')
        source_front = rt.prefix + '-source-front';rt.owned.append(('container', source_front))
        rt.docker('run', '-d', '--name', source_front, '--network', 'container:' + source,
                  '--mount', f'type=bind,src={bundle},dst=/opt/dbhub/current,readonly',
                  '--mount', f'type=bind,src={bundle}/config/nginx.conf,dst=/etc/nginx/conf.d/default.conf,readonly',
                  frontend_image)
        for _ in range(30):
            response = rt.docker('exec', source_front, 'wget', '-Y', 'off', '-qO-', 'http://127.0.0.1:8088/api/ready', check=False)
            if response.returncode == 0:break
            import time;time.sleep(0.5)
        else:raise AssertionError('Source Nginx did not proxy the source binary')
        denied = rt.docker('exec', rt.redis, 'wget', '-Y', 'off', '-q', '-T', '2', '-O', '/dev/null', 'http://' + source + ':8080/api/health', check=False)
        # Redis Alpine includes busybox wget. A failed command must reflect TCP refusal.
        assert denied.returncode and b'refused' in denied.stderr.lower()
        rt.passed('source_bundle_nginx_proxy_and_loopback_only_backend')
        jdbc_image = os.environ.get('DBHUB_JDBC_RUNTIME_IMAGE')
        if jdbc_image:
            token_file = rt.state / 'source-jdbc.env'
            with token_file.open('w') as target:
                target.write('DBHUB_JDBC_TOKEN=' + secrets.token_hex(32) + '\n')
            token_file.chmod(0o600);rt.private_files.append(token_file)
            jdbc = rt.prefix + '-source-jdbc'
            rt.run(jdbc, '--env-file', str(token_file), '-e', 'JDBC_BIND_HOST=127.0.0.1',
                   '-e', 'JDBC_ARTIFACT_LOCK=/opt/dbhub/current/jdbc/artifacts.lock.json',
                   '-e', 'JDBC_ARTIFACT_DIR=/opt/dbhub/current/jdbc/lib', '--read-only', '--cap-drop=ALL',
                   '--tmpfs', '/tmp:rw,noexec,nosuid,size=64m,mode=1777', '--entrypoint', 'java',
                   '--mount', f'type=bind,src={bundle},dst=/opt/dbhub/current,readonly',
                   jdbc_image, '-Xms32m', '-Xmx256m', '-cp',
                   '/opt/dbhub/current/jdbc/classes:/opt/dbhub/current/jdbc/lib/gson-2.13.2.jar', 'DBHubJdbc')
            for _ in range(60):
                response = rt.docker('exec', jdbc, 'curl', '--noproxy', '127.0.0.1', '-fsS', 'http://127.0.0.1:8090/health', check=False)
                if response.returncode == 0:break
                import time;time.sleep(0.5)
            else:raise AssertionError('Source JDBC did not become ready')
            forbidden = rt.docker('exec', jdbc, 'curl', '--noproxy', '127.0.0.1', '-s', '-o', '/dev/null', '-w', '%{http_code}', 'http://127.0.0.1:8090/v1/artifacts')
            assert forbidden.stdout == b'401'
            sockets = rt.docker('exec', jdbc, 'cat', '/proc/net/tcp', '/proc/net/tcp6').stdout.decode().splitlines()
            listeners = [line.split()[1].split(':')[0] for line in sockets if len(line.split()) > 3 and line.split()[3] == '0A' and line.split()[1].endswith(':1F9A')]
            assert listeners and all(address.endswith('0100007F') for address in listeners)
            rt.passed('source_jdbc_java17_loopback_and_authenticated_api')
        rt.result['source_jdbc_executed'] = bool(jdbc_image)
        rt.result.update(completed=True, kubernetes_cluster_install_tested=False, source_bundle=str(bundle))
    finally:
        for kind, name in rt.owned:
            if kind == 'container':
                logs = rt.docker('logs', '--tail', '100', name, check=False)
                if not logs.returncode:
                    (rt.state / (name + '.log')).write_bytes(logs.stdout + logs.stderr)
        rt.cleanup()
        # Private fixture files and synthetic audit journals are local to this run.
        (rt.state / 'helm-nginx.conf').unlink(missing_ok=True)


if __name__ == '__main__':
    main()
