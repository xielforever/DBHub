#!/usr/bin/env python3
"""Docker CLI deployment. Preserve audit volumes; never evaluate env-file content."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time

LABEL = 'io.dbhub.deployment'


def docker(*args, check=True):
    result = subprocess.run(['docker', *args], capture_output=True, text=True)
    if check and result.returncode:
        # Docker errors can echo credentials passed in environment options.
        raise RuntimeError('Docker command failed: ' + args[0] + '; inspect the owned resource locally')
    return result


def read_config(path):
    if path.stat().st_mode & 0o077:
        raise ValueError('Env file must have permissions 0600 or stricter')
    values = {}
    for line in path.read_text().splitlines():
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        key, sep, value = line.partition('=')
        if not sep or not re.fullmatch('[A-Z][A-Z0-9_]*', key) or key in values:
            raise ValueError('Use unique, literal KEY=value entries without export or shell syntax')
        values[key] = value
    for key in ['APP_SECRET_KEY', 'APP_ADMIN_PASSWORD', 'DATABASE_URL', 'REDIS_URL', 'DBHUB_TARGET_ALLOWLIST',
                'DBHUB_BACKEND_IMAGE', 'DBHUB_FRONTEND_IMAGE']:
        if not values.get(key) or 'REPLACE' in values[key]:
            raise ValueError('Configure ' + key + ' before starting')
    if len(values['APP_SECRET_KEY']) < 32 or values['APP_ADMIN_PASSWORD'] == 'admin123':
        raise ValueError('A strong master key and administrator password are required')
    return values


def owned(kind, name, owner):
    result = docker(kind, 'inspect', name, check=False)
    if result.returncode:
        return False
    item = json.loads(result.stdout)[0]
    labels = item.get('Labels') if kind != 'container' else item['Config'].get('Labels')
    if (labels or {}).get(LABEL) != owner:
        raise ValueError('Refusing to modify an unowned ' + kind + ': ' + name)
    return True


def wait_healthy(name, seconds=180):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        result = docker('inspect', name, '--format', '{{json .State}}', check=False)
        if not result.returncode:
            state = json.loads(result.stdout)
            if state.get('Health', {}).get('Status') == 'healthy':
                return
            if state.get('Status') in ['exited', 'dead']:
                break
        time.sleep(1)
    raise RuntimeError('Container did not become healthy: ' + name)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['up', 'down', 'status'])
    parser.add_argument('--env-file', type=Path)
    parser.add_argument('--name', default='dbhub')
    parser.add_argument('--jdbc', action='store_true')
    parser.add_argument('--bind', default='127.0.0.1')
    parser.add_argument('--port', type=int, default=8088)
    parser.add_argument('--ca-dir', type=Path, help='Optional directory of public CA files mounted at /etc/dbhub-ca')
    args = parser.parse_args(argv)
    if not re.fullmatch('[a-z][a-z0-9-]{0,39}', args.name) or not 1 <= args.port <= 65535:
        parser.error('Use a lowercase deployment name and port 1–65535')
    # Restrict bind to IPv4 literals; IPv6 publication is configured manually.
    import ipaddress
    if not isinstance(ipaddress.ip_address(args.bind), ipaddress.IPv4Address):
        parser.error('--bind must be an IPv4 address')
    owner, network, audit = args.name, args.name + '-network', args.name + '-audit'
    names = [args.name + '-' + part for part in ['frontend', 'backend', 'jdbc']]
    if args.action in ['down', 'status']:
        for name in names:
            if owned('container', name, owner):
                if args.action == 'down':
                    docker('stop', '--time', '30', name)
                    docker('rm', name)
                else:
                    print(name + ': ' + docker('inspect', name, '--format', '{{.State.Status}}').stdout.strip())
        if args.action == 'down' and owned('network', network, owner):
            attached = json.loads(docker('network', 'inspect', network).stdout)[0].get('Containers', {})
            if attached:
                print('Network retained because other containers are attached: ' + network)
            else:
                docker('network', 'rm', network)
        return
    if not args.env_file:
        parser.error('up requires --env-file')
    path = args.env_file.resolve()
    values = read_config(path)
    ca_options = []
    if args.ca_dir:
        if not args.ca_dir.is_dir():
            raise ValueError('--ca-dir must be a directory of public CA files')
        ca_options = ['--mount', 'type=bind,src=' + str(args.ca_dir.resolve()) + ',dst=/etc/dbhub-ca,readonly']
    if args.jdbc and (not values.get('DBHUB_JDBC_IMAGE') or len(values.get('DBHUB_JDBC_TOKEN', '')) < 32):
        raise ValueError('--jdbc requires DBHUB_JDBC_IMAGE and a token of at least 32 characters')
    for name in names:
        if owned('container', name, owner):
            raise ValueError('Deployment exists; use status, then down before a controlled upgrade')
    if not owned('network', network, owner):
        docker('network', 'create', '--label', LABEL + '=' + owner, network)
    if not owned('volume', audit, owner):
        docker('volume', 'create', '--label', LABEL + '=' + owner, audit)
    started = []

    def start(part, image, *options):
        name = args.name + '-' + part
        docker('run', '-d', '--name', name, '--label', LABEL + '=' + owner, '--network', network,
               '--network-alias', part, '--restart', 'unless-stopped', '--stop-timeout', '30',
               '--init', *options, image)
        started.append(name)

    try:
        if args.jdbc:
            with tempfile.NamedTemporaryFile(mode='w') as token_file:
                token_file.write('DBHUB_JDBC_TOKEN=' + values['DBHUB_JDBC_TOKEN'] + '\n')
                token_file.flush()
                start('jdbc', values['DBHUB_JDBC_IMAGE'], '--env-file', token_file.name, '--read-only', '--cap-drop=ALL',
                      '--security-opt', 'no-new-privileges', '--tmpfs', '/tmp:rw,noexec,nosuid,size=64m,mode=1777', *ca_options)
            wait_healthy(args.name + '-jdbc')
        options = ['--env-file', str(path), '-e', 'APP_ENV=production', '-e', 'PORT=8080', '-e', 'HTTP_BIND_HOST=0.0.0.0',
                   '-e', 'DBHUB_AUDIT_DIR=/var/lib/dbhub/audit', '--read-only', '--cap-drop=ALL', '--security-opt',
                   'no-new-privileges', '--mount', 'type=volume,src=' + audit + ',dst=/var/lib/dbhub/audit']
        if args.jdbc:
            options += ['-e', 'DBHUB_JDBC_URL=http://jdbc:8090']
        start('backend', values['DBHUB_BACKEND_IMAGE'], *options, *ca_options)
        wait_healthy(args.name + '-backend')
        start('frontend', values['DBHUB_FRONTEND_IMAGE'], '--publish', f'{args.bind}:{args.port}:80')
        wait_healthy(args.name + '-frontend')
        print('Deployment healthy: ' + args.name + '; only the frontend port is published')
    except BaseException:
        for name in reversed(started):
            docker('stop', '--time', '30', name, check=False)
            docker('rm', name, check=False)
        # Audit may contain unflushed events; preserve it even on a failed start.
        docker('network', 'rm', network, check=False)
        raise


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, RuntimeError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
