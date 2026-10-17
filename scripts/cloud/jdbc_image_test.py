"""Private JDBC image acceptance against real TLS targets; never print credentials."""
import json
import os
import urllib.error
import urllib.request
import uuid
from pathlib import Path

values = os.environ
trusted = values.get('DBHUB_JDBC_IMAGE_URL', 'http://jdbc-image:8090')
untrusted = values.get('DBHUB_JDBC_UNTRUSTED_URL', 'http://jdbc-untrusted:8090')
checks = []
tls_profiles = []


def rpc(endpoint, operation, body, expected=200, authenticated=True):
    headers = {'Content-Type': 'application/json'}
    if authenticated:
        headers['Authorization'] = 'Bearer ' + values['DBHUB_JDBC_TOKEN']
    request = urllib.request.Request(endpoint+'/v1/'+operation, data=json.dumps(body).encode(), headers=headers)
    try:
        response = urllib.request.urlopen(request, timeout=25)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        assert response.status == expected, operation + ': unexpected HTTP ' + str(response.status)
        return json.load(response)


def passed(message):
    checks.append(message)
    print('PASS ' + message, flush=True)


artifacts = rpc(trusted, 'artifacts', {})['data']
assert artifacts['protocol'] == 1
drivers = {driver['name']: driver for driver in artifacts['items']}
assert {'postgresql', 'mysql', 'h2'}.issubset(drivers)
assert all(len(driver['sha256']) == 64 and driver['ready'] for driver in drivers.values())
locked = {item['id']: item for item in json.loads(Path('/app/services/jdbc-runner/artifacts.lock.json').read_text()) if item['class_name']}
assert {item['id'] for item in drivers.values()} == set(locked)
assert all(driver['sha256'] == locked[driver['id']]['sha256'] for driver in drivers.values())
passed('production image loads checksum-verified PostgreSQL, MySQL and H2 artifacts')
rpc(trusted, 'artifacts', {}, 401, False)
passed('private JDBC operations reject a missing service identity')

h2 = drivers['h2']
owner = 'image-check-' + uuid.uuid4().hex
opened = rpc(trusted, 'open', {'artifact_id': h2['id'], 'sha256': h2['sha256'], 'owner': owner, 'url': 'jdbc:h2:mem:'+owner+';DB_CLOSE_DELAY=-1', 'database': owner, 'username': 'sa', 'password': '', 'budget_ms': 5000})['data']
session = {'session_id': opened['session_id'], 'owner': owner}
try:
    rpc(trusted, 'probe', {**session, 'owner': 'different-owner'}, 403)
    passed('a real session rejects a different owner')
    operation = {**session, 'execution_key': str(uuid.uuid4()), 'sql': 'CREATE TABLE ONCE_ONLY(ID INT)', 'budget_ms': 5000}
    rpc(trusted, 'execute', operation)
    duplicate = rpc(trusted, 'execute', operation, 409)
    assert duplicate['error']['kind'] == 'duplicate'
    passed('the Java execution key rejects a duplicate without redispatch')
finally:
    rpc(trusted, 'execute', {**session, 'execution_key': str(uuid.uuid4()), 'sql': 'DROP ALL OBJECTS', 'budget_ms': 5000})
    rpc(trusted, 'close', session)

for product, port, host in [('postgresql', 5432, values.get('DBHUB_JDBC_TLS_POSTGRES_HOST', 'dbhub-jdbc-tls')), ('mysql', 3306, values.get('DBHUB_JDBC_TLS_MYSQL_HOST', 'dbhub-jdbc-tls-mysql'))]:
    driver = drivers[product]
    database = values['POSTGRES_DB'] if product == 'postgresql' else values['MYSQL_DATABASE']
    username = values['POSTGRES_USER'] if product == 'postgresql' else values['MYSQL_USER']
    password = values['POSTGRES_PASSWORD'] if product == 'postgresql' else values['MYSQL_PASSWORD']
    spec = {'artifact_id': driver['id'], 'sha256': driver['sha256'], 'owner': 'tls-'+uuid.uuid4().hex, 'url': f'jdbc:{product}://{host}:{port}/{database}', 'database': database, 'username': username, 'password': password, 'ssl_mode': 'verify-full', 'dialect': 'postgres' if product == 'postgresql' else 'mysql', 'budget_ms': 10000}
    opened = rpc(trusted, 'open', spec)['data']
    session = {'session_id': opened['session_id'], 'owner': spec['owner']}
    try:
        probe = rpc(trusted, 'probe', session)['data']
        tls_profiles.append({'product': product, 'server_version': probe['version'], 'driver_version': probe.get('driver_version')})
        result = rpc(trusted, 'execute', {**session, 'execution_key': str(uuid.uuid4()), 'sql': 'SELECT 1 AS value', 'read_only': True, 'cursor': True, 'budget_ms': 5000})['data']
        assert result['rows'][0][0]['text'] == '1' and probe['version']
        tls_sql = 'SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid()' if product == 'postgresql' else "SHOW SESSION STATUS LIKE 'Ssl_cipher'"
        tls = rpc(trusted, 'execute', {**session, 'execution_key': str(uuid.uuid4()), 'sql': tls_sql, 'read_only': True, 'cursor': True, 'budget_ms': 5000})['data']['rows'][0]
        assert tls[0].get('value') is True if product == 'postgresql' else bool(tls[1]['text'])
        passed(product + ' verifies a trusted TLS certificate and executes a server-readonly query')
        if product == 'mysql':
            described = rpc(trusted, 'metadata', {**session, 'operation': 'describe', 'schema': database, 'table': 'image_fk_child'})['data']
            foreign = next(key for key in described['keys'] if key['kind'] == 'foreign_key')
            assert foreign['ref_schema'] == 'dbhub_jdbc_fk_fixture' and foreign['ref_table'] == 'parent'
            passed('MySQL JDBC preserves the referenced catalog of a cross-database foreign key')
    finally:
        rpc(trusted, 'close', session)
    bad_name = {**spec, 'owner': 'mismatch-'+uuid.uuid4().hex, 'url': spec['url'].replace(host+':', host+'-wrong:')}
    assert rpc(trusted, 'open', bad_name, 400)['error']['kind'] == 'authentication'
    passed(product + ' rejects a certificate hostname mismatch')
    assert rpc(untrusted, 'open', spec, 400)['error']['kind'] == 'authentication'
    passed(product + ' rejects a certificate outside the default trust store')

for endpoint in [trusted, untrusted]:
    with urllib.request.urlopen(endpoint+'/health', timeout=5) as response:
        assert json.load(response)['sessions'] == 0
passed('owned sessions are removed after success and TLS failures')

Path(values.get('DBHUB_JDBC_IMAGE_REPORT', '/runtime/jdbc-image-checks.json')).write_text(json.dumps({'scope': 'jdbc-production-image', 'checks': checks, 'artifacts': artifacts['items'], 'tls_profiles': tls_profiles, 'fixtures': ['Private protocol creates the H2 execution-key fixture; public H2 SQL remains disabled.', 'Short-lived self-signed TLS fixture is trusted only by an isolated image-check container.']}, ensure_ascii=False, indent=2))
