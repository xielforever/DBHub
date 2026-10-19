"""Explicit opt-in configuration; failures never contain input/credential values."""
import os
from urllib.request import proxy_bypass

ENDPOINT = 'https://dbhub-demo-wsl.tail1184b4.ts.net'
ENABLED = 'DBHUB_HTTPS_JDBC_ENABLED'

class DeploymentError(Exception):
    pass

def enabled(env=None):
    env = os.environ if env is None else env
    value = env.get(ENABLED, '0')
    if value not in ('0', '1'):
        raise DeploymentError('enable flag must be 0 or 1')
    return value == '1'

def validate(env=None):
    env = os.environ if env is None else env
    if not enabled(env):
        return False
    if env.get('DBHUB_COMPOSE_FILE'):
        raise DeploymentError('custom base Compose is unsupported for this adapter')
    if env.get('DBHUB_HTTPS_JDBC_ENDPOINT') != ENDPOINT:
        raise DeploymentError('explicit fixed HTTPS endpoint required')
    if not env.get('DBHUB_DEMO_PG_PASSWORD'):
        raise DeploymentError('ready direct PG credential required')
    if not (env.get('HTTPS_PROXY') or env.get('https_proxy')):
        raise DeploymentError('inherited HTTPS proxy required')
    if env is os.environ and proxy_bypass('dbhub-demo-wsl.tail1184b4.ts.net'):
        raise DeploymentError('remote HTTPS proxy bypass is forbidden')
    return True

if __name__ == '__main__':
    try:
        import sys
        active = validate()
        if len(sys.argv) > 1 and sys.argv[1] == 'enabled':
            raise SystemExit(0 if active else 1)
    except DeploymentError as e:
        raise SystemExit(str(e)) from None
