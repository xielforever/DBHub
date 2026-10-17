#!/bin/sh
# Ephemeral single-node acceptance fixture. Never use the publisher's verbose
# entrypoint: it echoes SQL commands containing the initial password.
set -eu
export GAUSSHOME=/usr/local/opengauss
export PATH="$GAUSSHOME/bin:$PATH"
export LD_LIBRARY_PATH="$GAUSSHOME/lib:${LD_LIBRARY_PATH:-}"
export LANG=C.UTF-8
export PGDATA=/var/lib/opengauss/data
mkdir -p "$PGDATA" /var/run/opengauss /home/omm/pg_log
chown -R omm:omm "$PGDATA" /var/run/opengauss /home/omm/pg_log
chmod 700 "$PGDATA"
if [ ! -f "$PGDATA/PG_VERSION" ]; then
install -o omm -g omm -m 600 /run/demo/password /tmp/init-password
gosu omm gs_initdb -D "$PGDATA" --nodename=dbhub_demo --encoding=UTF-8 --no-locale --dbcompatibility=PG --pwfile=/tmp/init-password
rm -f /tmp/init-password
fi
gosu omm gs_guc set -D "$PGDATA" \
  -c "listen_addresses='*'" -c 'port=5432' -c 'max_connections=20' \
  -c 'use_workload_manager=off' -c 'cstore_buffers=16MB' -c 'shared_buffers=64MB' -c 'max_process_memory=2GB' \
  -c 'work_mem=8MB' -c 'maintenance_work_mem=16MB' \
  -c "log_statement='none'"
gosu omm gs_guc set -D "$PGDATA" -h 'host all all 0.0.0.0/0 sha256'
# NUMA binding is unavailable in an ordinary container; publisher recommends this.
printf '%s\n' 'enable_numa = false' > "$PGDATA/mot.conf"
chown omm:omm "$PGDATA/mot.conf"
exec gosu omm gaussdb -D "$PGDATA" --single_node
