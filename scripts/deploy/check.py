#!/usr/bin/env python3
"""Offline Helm/Compose contracts and optional Kubernetes JSON-schema validation."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import yaml

ROOT = Path(__file__).resolve().parents[2]
HELM_IMAGE = 'alpine/helm:3.19.0@sha256:aef9b56f64e866207d9591d0abd8f6d767b36aadd12edf68f8a719716d9d29c9'


def helm(*args, ok=True):
    command = ['docker', 'run', '--rm', '-v', str(ROOT / 'deploy/helm') + ':/charts:ro', HELM_IMAGE, *args]
    result = subprocess.run(command, capture_output=True, text=True)
    if ok and result.returncode:
        raise RuntimeError('Helm validation failed: ' + result.stderr)
    if not ok and not result.returncode:
        raise AssertionError('Invalid Helm configuration was accepted')
    return result.stdout


def options(**overrides):
    values = {'existingSecret': 'test-secrets', 'config.DBHUB_TARGET_ALLOWLIST': 'postgres:5432',
              'backend.image.repository': 'dbhub-backend', 'backend.image.tag': 'test',
              'frontend.image.repository': 'dbhub-frontend', 'frontend.image.tag': 'test', **overrides}
    return [item for key, value in values.items() for item in ['--set', key + '=' + str(value).lower() if isinstance(value, bool) else key + '=' + str(value)]]


def manifests(**overrides):
    return [item for item in yaml.safe_load_all(helm('template', 'check', '/charts/dbhub', *options(**overrides))) if item]


def validate(items):
    deployments = {item['metadata']['name'].rsplit('-', 1)[-1]: item['spec'] for item in items if item['kind'] == 'Deployment'}
    backend = deployments['backend']
    assert backend['replicas'] == 1 and backend['strategy']['type'] == 'Recreate'
    for name, spec in deployments.items():
        pod = spec['template']['spec']
        assert pod['automountServiceAccountToken'] is False
        assert pod['securityContext']['runAsNonRoot'] is True
        for container in pod['containers']:
            assert container['securityContext']['allowPrivilegeEscalation'] is False
            assert container['securityContext']['readOnlyRootFilesystem'] is True
        if name == 'backend':
            assert pod['securityContext']['fsGroupChangePolicy'] == 'OnRootMismatch'
            c = pod['containers'][0]
            assert {v['name']: v['value'] for v in c['env']}['APP_ENV'] == 'production'
            assert c['readinessProbe']['httpGet']['path'] == '/api/ready'
            assert c['livenessProbe']['httpGet']['path'] == '/api/health'
            assert any('persistentVolumeClaim' in v for v in pod['volumes'])
    for item in items:
        if item['kind'] == 'Secret':
            raise AssertionError('Chart must reference externally managed secrets')
        if item['kind'] == 'Service':
            assert item['spec']['type'] == 'ClusterIP'
        if item['kind'] == 'PersistentVolumeClaim':
            assert item['metadata']['annotations']['helm.sh/resource-policy'] == 'keep'
        if item['kind'] == 'ConfigMap' and 'default.conf' in item['data']:
            nginx = item['data']['default.conf']
            assert '127.0.0.11' not in nginx and 'listen 8080;' in nginx
            assert 'proxy_next_upstream off;' in nginx and 'dbhub-security-headers.conf' in nginx
        if item['kind'] == 'CronJob':
            assert item['spec']['concurrencyPolicy'] == 'Forbid'
            pod = item['spec']['jobTemplate']['spec']['template']['spec']
            assert 'chmod 600' in pod['initContainers'][0]['args'][0]
            assert any('secret' in v for v in pod['volumes'])


def compose_contract():
    with tempfile.TemporaryDirectory() as temp:
        path = Path(temp) / 'env'
        path.write_text('APP_SECRET_KEY=synthetic-test-key-longer-than-32-bytes\nAPP_ADMIN_PASSWORD=SyntheticTest9!\n'
                        'DATABASE_URL=postgres://fake:fake@postgres/dbhub\nREDIS_URL=redis://redis/0\n'
                        'DBHUB_TARGET_ALLOWLIST=postgres:5432\nDBHUB_BACKEND_IMAGE=backend:test\n'
                        'DBHUB_FRONTEND_IMAGE=frontend:test\nDBHUB_JDBC_IMAGE=jdbc:test\nDBHUB_BACKUP_IMAGE=backup:test\n'
                        'DBHUB_JDBC_TOKEN=synthetic-token-longer-than-32-bytes\nDBHUB_BACKUP_CONFIG_DIR=' + temp + '\n')
        result = subprocess.run(['docker', 'compose', '--env-file', str(path), '-f', str(ROOT / 'docker-compose.prod.yaml'),
                                 '-f', str(ROOT / 'docker-compose.ops.yaml'), '--profile', 'jdbc', '--profile', 'operations',
                                 'config', '--format', 'json'], capture_output=True, check=True)
        services = json.loads(result.stdout)['services']
        assert services['backend']['environment']['APP_ENV'] == 'production'
        assert not services['backend'].get('ports') and not services['jdbc'].get('ports')
        assert any(v['target'] == '/var/lib/dbhub/audit' for v in services['backend']['volumes'])
        assert any('postgresql/data' in p for p in services['metadata-backup']['tmpfs'])
        assert services['frontend']['ports'][0]['host_ip'] == '127.0.0.1'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--schema', action='store_true')
    parser.add_argument('--kubeconform-image', default='ghcr.io/yannh/kubeconform:v0.7.0@sha256:85dbef6b4b312b99133decc9c6fc9495e9fc5f92293d4ff3b7e1b30f5611823c')
    args = parser.parse_args()
    variants = [{}, {'jdbc.enabled': True, 'jdbc.image.repository': 'jdbc', 'jdbc.image.tag': 'test',
                    'ingress.enabled': True, 'ingress.host': 'dbhub.example.com', 'ingress.tlsSecretName': 'test-tls',
                    'backup.enabled': True, 'backup.existingSecret': 'test-backup', 'backup.image.repository': 'backup', 'backup.image.tag': 'test'},
                {'backend.audit.existingClaim': 'old-audit'},
                {'backend.image.digest': 'sha256:' + 'a' * 64}]
    with tempfile.TemporaryDirectory() as temp:
        for index, variant in enumerate(variants):
            helm('lint', '--strict', '/charts/dbhub', *options(**variant))
            items = manifests(**variant)
            validate(items)
            if args.schema:
                path = Path(temp) / 'rendered.yaml';path.write_text(yaml.safe_dump_all(items))
                command = ['docker', 'run', '--rm', '-v', temp + ':/manifests:ro']
                ca = os.environ.get('SSL_CERT_FILE') or os.environ.get('CODEX_PROXY_CERT')
                if ca:
                    command += ['-v', ca + ':/run/proxy-ca.pem:ro', '-e', 'SSL_CERT_FILE=/run/proxy-ca.pem']
                subprocess.run([*command, args.kubeconform_image, '-strict', '-summary', '-exit-on-error',
                                '-kubernetes-version', '1.32.0', '/manifests/rendered.yaml'], check=True)
            print('PASS Helm variant ' + str(index + 1), flush=True)
    for invalid in [{'existingSecret': ''}, {'backend.replicas': 2}, {'config.APP_SECRET_KEY': 'forbidden'},
                    {'ingress.enabled': True}, {'backup.enabled': True}, {'backend.image.digest': 'bad'}]:
        helm('template', 'check', '/charts/dbhub', *options(**invalid), ok=False)
    compose_contract()
    print('PASS six invalid Helm configurations rejected; production Compose contracts verified')


if __name__ == '__main__':
    main()
