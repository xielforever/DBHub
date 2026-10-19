"""One-shot owner setup. Only the two individual credential files are mounted."""
import hmac,os,stat
from pathlib import Path

def pin(source,target):
    source,target=Path(source),Path(target)
    for path in (source,target):
        info=path.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_mode&0o777!=0o600:
            raise ValueError('private regular file required')
    if not hmac.compare_digest(source.read_bytes(),target.read_bytes()):
        raise ValueError('router credential content drift')
    os.chown(target,65532,65532)
    info=target.stat()
    if (info.st_uid,info.st_gid,info.st_mode&0o777)!=(65532,65532,0o600):
        raise ValueError('router credential ownership unavailable')

if __name__=='__main__':
    try:
        pin('/run/source.json','/run/router.json')
        print('Dedicated router credential ownership verified')
    except Exception:
        raise SystemExit('Router credential ownership check failed; no content overwritten') from None
