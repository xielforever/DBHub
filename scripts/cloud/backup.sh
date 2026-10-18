#!/bin/sh
# Prepare private libpq files from the existing generated development binding.
# Configuration contents never appear in arguments, logs or the repository.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
export DBHUB_STATE_DIR=${DBHUB_STATE_DIR:-/workspace/dbhub-cloud}
export DBHUB_CLOUD_BACKUP_IMAGE=${DBHUB_CLOUD_BACKUP_IMAGE:-dbhub-backup:cloud}
docker build -t "$DBHUB_CLOUD_BACKUP_IMAGE" -f scripts/ops/Dockerfile scripts/ops
python3 - <<'PY'
import io,os,re,subprocess,tarfile
from pathlib import Path
from urllib.parse import urlsplit,unquote,parse_qs
state=Path(os.environ['DBHUB_STATE_DIR'])
bindings=dict(line.rstrip('\n').split('=',1) for line in (state/'runtime.env').read_text().splitlines() if '=' in line)
url=urlsplit(bindings['DATABASE_URL']);query=parse_qs(url.query)
values={'host':url.hostname or 'postgres','port':str(url.port or 5432),'dbname':unquote(url.path.lstrip('/')),'user':unquote(url.username or ''),'password':unquote(url.password or ''),'sslmode':query.get('sslmode',['disable'])[0]}
if any('\n' in v or '\r' in v for v in values.values()):raise SystemExit('Invalid development libpq binding')
service='[dbhub]\n'+''.join(k+'='+values[k]+'\n' for k in ['host','port','dbname','user','sslmode'])
escape=lambda v:v.replace('\\','\\\\').replace(':','\\:')
password=':'.join(escape(values[k]) for k in ['host','port','dbname','user','password'])+'\n'
project=os.environ.get('DBHUB_COMPOSE_PROJECT','dbhub-cloud');volume=project+'_backup-config'
subprocess.run(['docker','volume','create','--label','com.docker.compose.project='+project,'--label','com.docker.compose.volume=backup-config',volume],check=True,stdout=subprocess.DEVNULL)
archive=io.BytesIO()
with tarfile.open(fileobj=archive,mode='w') as target:
 for name,content in [('pg_service.conf',service),('pgpass',password)]:
  raw=content.encode();info=tarfile.TarInfo(name);info.size=len(raw);info.mode=0o600;info.uid=0;info.gid=0;target.addfile(info,io.BytesIO(raw))
base=re.search(r'^FROM (\S+)',Path('scripts/ops/Dockerfile').read_text()).group(1)
subprocess.run(['docker','run','--rm','-i','--network','none','--read-only','--cap-drop=ALL','--cap-add=CHOWN','--cap-add=FOWNER','--cap-add=DAC_OVERRIDE','--mount',f'type=volume,src={volume},dst=/private',base,'sh','-ec','umask 077; tar -xf - -C /private; chmod 700 /private; chmod 600 /private/pg_service.conf /private/pgpass; chown 65532:65532 /private/pg_service.conf /private/pgpass; chown 65532:65532 /private'],input=archive.getvalue(),check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
print('Private backup configuration prepared')
PY
scripts/cloud/dev.sh --profile operations up -d --renew-anon-volumes metadata-backup
