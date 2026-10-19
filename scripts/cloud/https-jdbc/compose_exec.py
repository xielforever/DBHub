import os,sys
from pathlib import Path
root=Path(os.environ['DBHUB_REPO_DIR'])
base_only=sys.argv[1:2]==['--base']
operations=sys.argv[2:] if base_only else sys.argv[1:]
args=['docker','compose','--project-name',os.environ.get('DBHUB_COMPOSE_PROJECT','dbhub-cloud'),'--project-directory',str(root),'--env-file',str(Path(os.environ['DBHUB_STATE_DIR'])/'runtime.env'),'-f',str(root/'scripts/cloud/compose.yaml')]
args+=['-f',str(Path(__file__).parent/('rollback.yaml' if base_only else 'compose.yaml'))]
if os.environ.get('DBHUB_HTTPS_JDBC_COMPOSE_EXTRA'):args+=['-f',os.environ['DBHUB_HTTPS_JDBC_COMPOSE_EXTRA']]
os.execvp('docker',args+operations)
