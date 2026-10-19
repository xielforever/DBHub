"""Create private startup state outside the repository; never rotate credentials."""
import fcntl,hmac,json,os,stat,sys
from pathlib import Path
from config import DeploymentError,validate

def private_file(path):
    if path.is_symlink() or not stat.S_ISREG(path.stat().st_mode) or path.stat().st_mode & 0o077:
        raise DeploymentError('private regular state file required')
    return path.read_bytes()

def env_values(raw):
    out={}
    for line in raw.decode().splitlines():
        if line and not line.startswith('#') and '=' in line:
            k,v=line.split('=',1)
            if k in out:raise DeploymentError('duplicate state key')
            out[k]=v
    return out

def exclusive(path,raw):
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    with os.fdopen(fd,'wb') as f:
        f.write(raw);f.flush();os.fsync(f.fileno())

def state_root(root,repo,must_exist=True):
    root,repo=Path(root).absolute(),Path(repo).resolve()
    # Reject the original lexical path and its ancestors before resolving it.
    if any(p.is_symlink() for p in [root,*root.parents]):
        raise DeploymentError('private state path cannot contain symlinks')
    root=root.resolve()
    if root==repo or repo in root.parents:
        raise DeploymentError('private state must be outside repository')
    if must_exist or root.exists():
        if not root.is_dir() or root.stat().st_mode & 0o077:
            raise DeploymentError('private state directory required')
    return root

def router_copy(root):
    source=root/'https-jdbc.credentials.json'
    target=root/'https-jdbc-router.credentials.json'
    if not target.exists() and not target.is_symlink():exclusive(target,private_file(source))
    if target.is_symlink() or not stat.S_ISREG(target.stat().st_mode) or target.stat().st_mode & 0o077:
        raise DeploymentError('private regular router credential file required')
    # Content/owner are checked by the narrowly scoped root helper, since the
    # host task may not be permitted to read a 0600 file already owned by 65532.

def prepare(root,repo,password):
    root=state_root(root,repo)
    lock=os.open(root/'https-jdbc-state.lock',os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600)
    with os.fdopen(lock,'r+b') as guard:
        fcntl.flock(guard,fcntl.LOCK_EX)
        raw=private_file(root/'runtime.env');values=env_values(raw)
        if values.get('DBHUB_JDBC_URL')!='http://jdbc:8090' or len(values.get('DBHUB_JDBC_TOKEN',''))<32:
            raise DeploymentError('unexpected original JDBC configuration')
        backup=root/'runtime.env.before-jdbc-router'
        if backup.exists():
            if private_file(backup)!=raw:raise DeploymentError('runtime backup drift; refusing overwrite')
        else:exclusive(backup,raw)
        legacy=root/'jdbc-router.env'
        if legacy.exists():
            previous=env_values(private_file(legacy))
            if previous.get('DBHUB_JDBC_TOKEN')!=values['DBHUB_JDBC_TOKEN'] or not hmac.compare_digest(previous.get('DBHUB_DEMO_PG_PASSWORD','').encode(),password.encode()):
                raise DeploymentError('legacy router credential drift')
        credentials={'service_token':values['DBHUB_JDBC_TOKEN'],'password':password}
        target=root/'https-jdbc.credentials.json'
        if target.exists():
            previous=json.loads(private_file(target))
            if set(previous)!=set(credentials) or any(not hmac.compare_digest(str(previous[k]).encode(),v.encode()) for k,v in credentials.items()):
                raise DeploymentError('router credential drift; refusing overwrite')
        else:exclusive(target,json.dumps(credentials,ensure_ascii=False).encode())
    return values

def original_allowlist(values):
    value=os.environ.get('DBHUB_TARGET_ALLOWLIST',values.get('DBHUB_TARGET_ALLOWLIST',''))
    if not value.strip():return ''
    if any(c in value for c in '\r\n\x00\"\''):raise DeploymentError('unsupported allowlist encoding')
    return value

def target_allowlist(values):
    value=original_allowlist(values)
    if not value:return ''
    return value if 'wsl-demo-postgres:5432' in [v.strip() for v in value.split(',')] else value+',wsl-demo-postgres:5432'

if __name__=='__main__':
    try:
        root=Path(os.environ.get('DBHUB_STATE_DIR','/workspace/dbhub-cloud'))
        repo=Path(__file__).resolve().parents[3]
        mode=sys.argv[1] if len(sys.argv)>1 else 'prepare'
        root=state_root(root,repo,must_exist=mode!='check-root')
        if mode=='check-root':pass
        elif mode=='original-allowlist':
            print(original_allowlist(env_values(private_file(root/'runtime.env'))))
        else:
            validate()
            values=prepare(root,repo,os.environ['DBHUB_DEMO_PG_PASSWORD'])
            if mode=='allowlist':print(target_allowlist(values))
            elif mode=='router-copy':router_copy(root)
    except Exception:
        raise SystemExit('HTTPS JDBC private state validation failed; no credentials changed') from None
