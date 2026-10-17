"""Orchestrate owned browser fixtures; application execution stays in Docker."""
import json
import os
import re
import secrets
import subprocess
import time
from pathlib import Path

root = Path(__file__).resolve().parents[2]
state = Path(os.environ.get('DBHUB_STATE_DIR', '/workspace/dbhub-cloud'))
state.mkdir(mode=0o700, parents=True, exist_ok=True)
tag = 'personal_ui_' + secrets.token_hex(5)
fixture = {
    'prefix': tag,
    'table': tag + '_stock',
    'reader': tag + '_reader',
    'writer': tag + '_writer',
    'reader_password': secrets.token_urlsafe(24),
    'writer_password': secrets.token_urlsafe(24),
    'app_user': tag + '_test',
    'app_readonly': tag + '_readonly',
    'app_password': 'Fixture9!' + secrets.token_urlsafe(24),
}
path = state / (tag + '.json')
fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
with os.fdopen(fd, 'w') as stream:
    json.dump(fixture, stream)

def identifier(value):
    assert re.fullmatch(r'[a-z0-9_]+', value)
    return '"' + value + '"'

def literal(value):
    return "'" + value.replace("'", "''") + "'"

def sql(text):
    command = [str(root/'scripts/cloud/dev.sh'), 'exec', '-T', 'postgres', 'sh', '-ec',
               'exec psql -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"']
    result = subprocess.run(command, cwd=root, input=text, text=True, capture_output=True)
    if result.returncode:
        # Only fixture secrets could appear here, but redact before diagnostic output.
        error = result.stderr
        for key in ('reader_password', 'writer_password', 'app_password'):
            error = error.replace(fixture[key], '[redacted]')
        raise RuntimeError(error[:2000])

code = 1
try:
    sql(f"""
CREATE ROLE {identifier(fixture['reader'])} LOGIN PASSWORD {literal(fixture['reader_password'])};
CREATE ROLE {identifier(fixture['writer'])} LOGIN PASSWORD {literal(fixture['writer_password'])};
CREATE TABLE public.{identifier(fixture['table'])}(id integer PRIMARY KEY, name text);
INSERT INTO public.{identifier(fixture['table'])} VALUES(1,'Inventory fixture');
GRANT USAGE ON SCHEMA public TO {identifier(fixture['reader'])},{identifier(fixture['writer'])};
GRANT SELECT ON public.{identifier(fixture['table'])} TO {identifier(fixture['reader'])};
GRANT SELECT,UPDATE ON public.{identifier(fixture['table'])} TO {identifier(fixture['writer'])};
""")
    container = 'dbhub-personal-browser'
    status = subprocess.run(['docker', 'container', 'inspect', '--format', '{{.State.Running}}', container], capture_output=True, text=True)
    if status.returncode or status.stdout.strip() != 'true':
        command = [str(root/'scripts/cloud/dev.sh'), 'run', '-d', '--name', container, '--no-deps',
                   '-e', 'DBHUB_BROWSER_TEST_SCRIPT=/tmp/personal-idle.py', 'browser', 'sh', '-ec',
                   'printf "import signal\\nsignal.pause()\\n" > /tmp/personal-idle.py; exec sh /app/scripts/cloud/browser-runtime.sh']
        subprocess.run(command, cwd=root, check=True, capture_output=True)
    for attempt in range(240):
        ready = subprocess.run(['docker', 'exec', container, 'sh', '-ec',
            'test -x /usr/bin/chromium; /tmp/browser-venv/bin/python -c "import playwright.sync_api"'], capture_output=True)
        if ready.returncode == 0:
            break
        time.sleep(1)
    else:
        raise RuntimeError('browser runtime did not become ready; inspect the owned container logs')
    command = ['docker', 'exec', '-e', 'DBHUB_PERSONAL_FIXTURE=/runtime/' + path.name, container,
               'sh', '-ec', 'export http_proxy="$HTTP_PROXY" https_proxy="$HTTPS_PROXY" no_proxy="$NO_PROXY"; exec /tmp/browser-venv/bin/python /app/scripts/cloud/personal_browser_test.py']
    code = subprocess.run(command, cwd=root).returncode
finally:
    try:
        # The generated namespace is the only mutation scope. No general prune,
        # database drop, or deletion of pre-existing user catalog items occurs.
        sql(f"""
DELETE FROM sys_connections WHERE name LIKE {literal(tag+'%')} OR user_id IN
 (SELECT id FROM sys_users WHERE username IN ({literal(fixture['app_user'])},{literal(fixture['app_readonly'])}));
DELETE FROM sys_source_definitions WHERE name LIKE {literal(tag+'%')} OR owner_id IN
 (SELECT id FROM sys_users WHERE username IN ({literal(fixture['app_user'])},{literal(fixture['app_readonly'])}));
DELETE FROM sys_datasource_templates WHERE template_key LIKE {literal(tag+'%')};
DELETE FROM sys_jdbc_drivers WHERE name LIKE {literal(tag+'%')};
DELETE FROM sys_users WHERE username IN ({literal(fixture['app_user'])},{literal(fixture['app_readonly'])});
DROP OWNED BY {identifier(fixture['reader'])},{identifier(fixture['writer'])};
DROP ROLE {identifier(fixture['reader'])};
DROP ROLE {identifier(fixture['writer'])};
DROP TABLE public.{identifier(fixture['table'])};
""")
        print('PASS owned database and catalog fixtures removed', flush=True)
    finally:
        path.unlink(missing_ok=True)
raise SystemExit(code)
