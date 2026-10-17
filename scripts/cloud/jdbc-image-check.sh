#!/bin/sh
# Validate a prebuilt JDBC image in isolated containers; never deploy production.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
dbh_state="${DBHUB_STATE_DIR:-/workspace/dbhub-cloud}"
dbh_image="${DBHUB_JDBC_CHECK_IMAGE:-dbhub-jdbc:local}"
dbh_network="${DBHUB_JDBC_CHECK_NETWORK:-dbhub-cloud_default}"
dbh_project="${DBHUB_COMPOSE_PROJECT:-dbhub-cloud}"
dbh_run="$(python3 -c 'import secrets;print(secrets.token_hex(5))')"
dbh_good="dbhub-jdbc-image-$dbh_run"
dbh_bad="dbhub-jdbc-untrusted-$dbh_run"
dbh_pg="dbhub-jdbc-tls-$dbh_run"
dbh_mysql="dbhub-jdbc-tls-mysql-$dbh_run"
dbh_artifacts="$dbh_state/jdbc-image-artifacts/$dbh_run"
dbh_remote="/runtime/jdbc-image-artifacts/$dbh_run"
dbh_id="$(docker image inspect --format '{{.Id}}' "$dbh_image")"
dbh_pg_image="$(docker inspect --format '{{.Config.Image}}' "$dbh_project-postgres-1")"
dbh_mysql_image="$(docker inspect --format '{{.Config.Image}}' "$dbh_project-mysql-1")"
dbh_pg_uid="$(scripts/cloud/dev.sh exec -T postgres id -u postgres)"
dbh_mysql_uid="$(scripts/cloud/dev.sh exec -T mysql id -u mysql)"
umask 077
mkdir -p "$dbh_artifacts"

cleanup() {
 dbh_exit=$?
 trap - EXIT
 dbh_cleanup=true
 docker rm -f "$dbh_good" "$dbh_bad" "$dbh_pg" "$dbh_mysql" >/dev/null 2>&1 || true
 for dbh_name in "$dbh_good" "$dbh_bad" "$dbh_pg" "$dbh_mysql"; do
  if docker inspect "$dbh_name" >/dev/null 2>&1; then dbh_cleanup=false; fi
 done
 scripts/cloud/dev.sh exec -T -u 0 -e DBHUB_TLS_RUN_DIR="$dbh_remote" jdbc sh -ec 'rm -rf -- "$DBHUB_TLS_RUN_DIR/tls"' >/dev/null 2>&1 || dbh_cleanup=false
 rm -f "$dbh_artifacts/pg.env" "$dbh_artifacts/mysql.env" "$dbh_artifacts/jdbc.env"
 python3 - "$dbh_artifacts/runner.json" "$dbh_id" "$dbh_exit" "$dbh_cleanup" <<'PY'
import json,sys
from pathlib import Path
Path(sys.argv[1]).write_text(json.dumps({'image_id':sys.argv[2], 'exit_code':int(sys.argv[3]), 'cleanup_succeeded':sys.argv[4]=='true'},indent=2)+'\n')
PY
 if [ "$dbh_cleanup" != true ]; then exit 1; fi
 exit "$dbh_exit"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

python3 - "$dbh_state/runtime.env" "$dbh_artifacts" <<'PY'
import os,sys
from pathlib import Path
values=dict(line.split('=',1) for line in Path(sys.argv[1]).read_text().splitlines() if '=' in line)
for file,names in [('pg.env',['POSTGRES_USER','POSTGRES_PASSWORD','POSTGRES_DB']),('mysql.env',['MYSQL_ROOT_PASSWORD','MYSQL_USER','MYSQL_PASSWORD','MYSQL_DATABASE']),('jdbc.env',['DBHUB_JDBC_TOKEN'])]:
 with open(Path(sys.argv[2])/file,'w',opener=lambda p,f:os.open(p,f,0o600)) as stream:
  for name in names:stream.write(name+'='+values[name]+'\n')
PY
scripts/cloud/dev.sh exec -T -u 0 -e DBHUB_TLS_RUN_DIR="$dbh_remote" -e DBHUB_TLS_PG_HOST="$dbh_pg" -e DBHUB_TLS_MYSQL_HOST="$dbh_mysql" -e DBHUB_TLS_PG_UID="$dbh_pg_uid" -e DBHUB_TLS_MYSQL_UID="$dbh_mysql_uid" jdbc sh -ec '
 umask 077
 mkdir -p "$DBHUB_TLS_RUN_DIR/tls/pg" "$DBHUB_TLS_RUN_DIR/tls/mysql" "$DBHUB_TLS_RUN_DIR/tls/client"
 openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=$DBHUB_TLS_PG_HOST" -addext "subjectAltName=DNS:$DBHUB_TLS_PG_HOST,DNS:$DBHUB_TLS_MYSQL_HOST" -addext extendedKeyUsage=serverAuth -keyout "$DBHUB_TLS_RUN_DIR/tls/pg/server.key" -out "$DBHUB_TLS_RUN_DIR/tls/pg/server.crt" >/dev/null 2>&1
 cp "$DBHUB_TLS_RUN_DIR/tls/pg/server.key" "$DBHUB_TLS_RUN_DIR/tls/mysql/server.key"
 cp "$DBHUB_TLS_RUN_DIR/tls/pg/server.crt" "$DBHUB_TLS_RUN_DIR/tls/mysql/server.crt"
 cp /etc/ssl/certs/java/cacerts "$DBHUB_TLS_RUN_DIR/tls/client/truststore.jks"
 keytool -importcert -noprompt -alias dbhub-jdbc-fixture -file "$DBHUB_TLS_RUN_DIR/tls/pg/server.crt" -keystore "$DBHUB_TLS_RUN_DIR/tls/client/truststore.jks" -storetype JKS -storepass changeit >/dev/null 2>&1
 chown "$DBHUB_TLS_PG_UID:$DBHUB_TLS_PG_UID" "$DBHUB_TLS_RUN_DIR/tls/pg/server.key"
 chown "$DBHUB_TLS_MYSQL_UID:$DBHUB_TLS_MYSQL_UID" "$DBHUB_TLS_RUN_DIR/tls/mysql/server.key"
 chmod 755 "$DBHUB_TLS_RUN_DIR/tls" "$DBHUB_TLS_RUN_DIR/tls/pg" "$DBHUB_TLS_RUN_DIR/tls/mysql" "$DBHUB_TLS_RUN_DIR/tls/client"
 chmod 644 "$DBHUB_TLS_RUN_DIR/tls/pg/server.crt" "$DBHUB_TLS_RUN_DIR/tls/mysql/server.crt" "$DBHUB_TLS_RUN_DIR/tls/client/truststore.jks"
'
docker run -d --name "$dbh_pg" --network "$dbh_network" --network-alias "$dbh_pg-wrong" --env-file "$dbh_artifacts/pg.env" --tmpfs /var/lib/postgresql/data:rw,size=1g --mount "type=bind,src=$dbh_artifacts/tls/pg,dst=/tls,readonly" "$dbh_pg_image" postgres -c ssl=on -c ssl_cert_file=/tls/server.crt -c ssl_key_file=/tls/server.key >/dev/null
docker run -d --name "$dbh_mysql" --network "$dbh_network" --network-alias "$dbh_mysql-wrong" --env-file "$dbh_artifacts/mysql.env" --tmpfs /var/lib/mysql:rw,size=2g --mount "type=bind,src=$dbh_artifacts/tls/mysql,dst=/tls,readonly" "$dbh_mysql_image" mysqld --ssl-cert=/tls/server.crt --ssl-key=/tls/server.key --require-secure-transport=ON >/dev/null
docker run -d --name "$dbh_good" --network "$dbh_network" --env-file "$dbh_artifacts/jdbc.env" --read-only --cap-drop ALL --security-opt no-new-privileges --tmpfs /tmp:rw,noexec,nosuid,size=64m,mode=1777 --mount "type=bind,src=$dbh_artifacts/tls/client,dst=/tls,readonly" -e 'JAVA_TOOL_OPTIONS=-Djavax.net.ssl.trustStore=/tls/truststore.jks -Djavax.net.ssl.trustStoreType=JKS -Djavax.net.ssl.trustStorePassword=changeit' "$dbh_image" >/dev/null
docker run -d --name "$dbh_bad" --network "$dbh_network" --env-file "$dbh_artifacts/jdbc.env" --read-only --cap-drop ALL --security-opt no-new-privileges --tmpfs /tmp:rw,noexec,nosuid,size=64m,mode=1777 "$dbh_image" >/dev/null

dbh_ready=false
for dbh_attempt in $(seq 1 120); do
 if docker exec "$dbh_pg" sh -ec 'pg_isready -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' >/dev/null 2>&1 && docker exec "$dbh_mysql" sh -ec 'MYSQL_PWD="$MYSQL_PASSWORD" mysql --protocol=TCP -h127.0.0.1 -u"$MYSQL_USER" -D"$MYSQL_DATABASE" -Nse "SELECT 1"' >/dev/null 2>&1 && docker exec "$dbh_good" curl --fail --silent http://127.0.0.1:8090/health >/dev/null 2>&1 && docker exec "$dbh_bad" curl --fail --silent http://127.0.0.1:8090/health >/dev/null 2>&1; then dbh_ready=true; break; fi
 sleep 1
done
if [ "$dbh_ready" != true ]; then echo 'JDBC image or TLS fixtures did not become ready' >&2; exit 1; fi

# The CA-only JKS uses the JDK's public default integrity password, not a target credential.
docker exec "$dbh_mysql" sh -ec 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot -D"$MYSQL_DATABASE" <<SQL
CREATE DATABASE dbhub_jdbc_fk_fixture;
CREATE TABLE dbhub_jdbc_fk_fixture.parent(id INT PRIMARY KEY);
CREATE TABLE image_fk_child(id INT PRIMARY KEY,parent_id INT,FOREIGN KEY(parent_id) REFERENCES dbhub_jdbc_fk_fixture.parent(id));
SQL'

scripts/cloud/dev.sh exec -T -e DBHUB_JDBC_URL="http://$dbh_good:8090" -e NO_PROXY="localhost,127.0.0.1,postgres,mysql,redis,$dbh_good" backend go test -race -count=1 -json ./internal/server -run '^TestConnectorJDBCRealHTTP$' > "$dbh_artifacts/go-integration.json.log"
scripts/cloud/dev.sh exec -T -e DBHUB_JDBC_IMAGE_URL="http://$dbh_good:8090" -e DBHUB_JDBC_UNTRUSTED_URL="http://$dbh_bad:8090" -e DBHUB_JDBC_IMAGE_HOST="$dbh_good" -e DBHUB_JDBC_UNTRUSTED_HOST="$dbh_bad" -e DBHUB_JDBC_TLS_POSTGRES_HOST="$dbh_pg" -e DBHUB_JDBC_TLS_MYSQL_HOST="$dbh_mysql" -e DBHUB_JDBC_IMAGE_REPORT="$dbh_remote/checks.json" jdbc sh -ec 'export http_proxy="$HTTP_PROXY" https_proxy="$HTTPS_PROXY" no_proxy="$NO_PROXY,$DBHUB_JDBC_IMAGE_HOST,$DBHUB_JDBC_UNTRUSTED_HOST"; exec python3 /app/scripts/cloud/jdbc_image_test.py'
echo "JDBC production image checks passed; private evidence: $dbh_artifacts"
