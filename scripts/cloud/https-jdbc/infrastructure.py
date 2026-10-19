"""Start existing infrastructure by ID; never recreate or remove it."""
import json,os,subprocess,time
from pathlib import Path
from config import DeploymentError

def ensure(run=subprocess.run):
    project=os.environ.get('DBHUB_COMPOSE_PROJECT','dbhub-cloud')
    dev=str(Path(__file__).resolve().parents[1]/'dev.sh')
    for service in ('postgres','mysql','redis','jdbc'):
        out=run(['docker','ps','-aq','--filter','label=com.docker.compose.project='+project,'--filter','label=com.docker.compose.service='+service],capture_output=True,text=True,check=True).stdout.split()
        if len(out)>1:raise DeploymentError('ambiguous infrastructure service')
        if out:
            state=json.loads(run(['docker','inspect','--format','{{json .State}}',out[0]],capture_output=True,text=True,check=True).stdout)
            if not state.get('Running'):run(['docker','start',out[0]],check=True,stdout=subprocess.DEVNULL)
        else:
            run(['sh',dev,'up','-d','--no-deps',service],check=True)
        deadline=time.monotonic()+240
        while True:
            ids=out or run(['docker','ps','-aq','--filter','label=com.docker.compose.project='+project,'--filter','label=com.docker.compose.service='+service],capture_output=True,text=True,check=True).stdout.split()
            if len(ids)!=1:raise DeploymentError('infrastructure service identity missing')
            state=json.loads(run(['docker','inspect','--format','{{json .State}}',ids[0]],capture_output=True,text=True,check=True).stdout)
            health=state.get('Health',{}).get('Status')
            if state.get('Running') and health=='healthy':break
            if health=='unhealthy' or time.monotonic()>deadline:raise DeploymentError('infrastructure health deadline exceeded')
            time.sleep(1)

if __name__=='__main__':
    try:ensure()
    except Exception:raise SystemExit('HTTPS JDBC infrastructure check failed; existing services were not recreated') from None
