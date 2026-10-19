#!/usr/bin/env python3
"""在 Docker 中逐个验证版本，控制 VFS 磁盘占用；不输出凭据。"""
import argparse
import hashlib
import json
import os
import secrets
import signal
import subprocess
import time
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import quote

ROOT = Path(__file__).resolve().parents[2]
STATE = Path(os.environ.get('DBHUB_STATE_DIR', '/workspace/dbhub-cloud'))
WRAPPER = ROOT/'scripts/cloud/dev.sh'
TARGETS = [
    ('redis', '6.2', 'redis:6.2-alpine'),
    ('mysql', '5.7', 'mysql:5.7'), ('mysql', '8.0', 'mysql:8.0'), ('mysql', '8.4', 'mysql:8.4'),
    *[('postgres', str(v), f'postgres:{v}-alpine') for v in range(14, 19)],
    ('redis', '7.2', 'redis:7.2-alpine'), ('redis', '7.4', 'redis:7.4-alpine'), ('redis', '8.0', 'redis:8.0-alpine'),
]
REQUIRED_TESTS = {'TestVersionMatrixTarget', 'TestIntegrationRealSQLAndTransactions', 'TestVersionCompatibilityHTTP', 'TestRealHTTPBackend'}

def required_tests(kind):
    return REQUIRED_TESTS | ({'TestDatasourceReliability', 'TestMetadataSyncReliability'} if kind == 'postgres' else set())

def command(args, **kwargs):
    return subprocess.run([str(a) for a in args], check=False, **kwargs)

def capture(args):
    result = command(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    if result.returncode:
        raise RuntimeError('无法读取 Docker/Git 状态')
    return result.stdout.strip()

def source_fingerprint():
    digest = hashlib.sha256()
    for path in sorted((ROOT/'backend').rglob('*.go')):
        if 'vendor' in path.parts:
            continue
        digest.update(str(path.relative_to(ROOT)).encode())
        digest.update(path.read_bytes())
    for name in ('go.mod', 'go.sum', 'vendor/modules.txt'):
        digest.update((ROOT/'backend'/name).read_bytes())
    # 兼容矩阵 JSON 是运行结果的记录，最终才填入实测补丁号，不参与协议实现指纹。
    return digest.hexdigest()

def migration_fingerprint():
    digest = hashlib.sha256()
    for path in sorted((ROOT/'backend/internal/db/migrations').glob('*.sql')):
        digest.update(path.name.encode())
        digest.update(path.read_bytes())
    return digest.hexdigest()

def save_json(path, value):
    with open(path, 'w', opener=lambda p, flags: os.open(p, flags, 0o600)) as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.write('\n')

def completed_target(target):
    return (target.get('valid') and target.get('cleanup_succeeded')
            and not target.get('skipped_tests') and target.get('version')
            and required_tests(target.get('engine')).issubset(target.get('passed_tests', [])))

def record_verified(path):
    report = json.loads(path.read_text())
    if not report.get('valid') or report.get('backend_go_sha256') != source_fingerprint() or report.get('backend_migrations_sha256') != migration_fingerprint():
        raise RuntimeError('报告无效或后端实现已变化，不能更新已验证版本')
    targets = report['targets']
    expected = {(kind, branch) for kind, branch, _ in TARGETS}
    if {(t['engine'], t['branch']) for t in targets} != expected or len(targets) != len(expected):
        raise RuntimeError('只有完整 12 分支的报告可以更新版本记录')
    if not all(completed_target(t) for t in targets):
        raise RuntimeError('报告包含未通过、未清理或未运行的测试')
    matrix = [{'engine':t['engine'], 'branch':t['branch'], 'tested_version':t['version']} for t in targets]
    matrix.sort(key=lambda t: (t['engine'], [int(p) for p in t['branch'].split('.')]))
    (ROOT/'backend/internal/target/compatibility_matrix.json').write_text(json.dumps(matrix, indent=2)+'\n')
    print('已按完整报告更新 12 个实测版本；请审核并提交记录。', flush=True)

def exercise(kind, branch, image, out, base, fingerprint, migration_hash, keep_images):
    label = kind+'-'+branch
    name = 'dbhub-compat-'+secrets.token_hex(6)
    log = out/(label+'.log')
    private = out/(label+'.env')
    redis_conf = out/(label+'.conf')
    password = secrets.token_urlsafe(24)
    result = {'engine':kind, 'branch':branch, 'image':image, 'valid':False, 'cleanup_succeeded':False}
    existed = command(['docker','image','inspect',image], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
    try:
        with log.open('w') as logs:
            print(f'开始 {kind} {branch}：拉取并启动独立目标', flush=True)
            if command(['docker','pull',image], stdout=logs, stderr=logs).returncode:
                raise RuntimeError('镜像拉取失败')
            image_info = json.loads(capture(['docker','image','inspect',image]))[0]
            result['image_id'] = image_info['Id']
            result['image_digests'] = image_info.get('RepoDigests', [])
            if os.statvfs('/').f_bavail*os.statvfs('/').f_frsize < 1200*1024*1024:
                raise RuntimeError('VFS 可用磁盘不足 1.2 GiB，停止创建目标以保护已有服务')
            args = ['docker','run','-d','--name',name,'--label','dbhub.compatibility=true','--network','dbhub-cloud_default']
            env = dict(os.environ)
            if kind == 'postgres':
                server_env = {'POSTGRES_USER':'dbhub', 'POSTGRES_PASSWORD':password, 'POSTGRES_DB':'dbhub'}
                # PostgreSQL 18 改用主版本子目录；旧 data 挂载会被入口脚本当作未升级的数据卷。
                data_root = '/var/lib/postgresql' if int(branch) >= 18 else '/var/lib/postgresql/data'
                args += ['--tmpfs',data_root+':rw,size=1g']
                ready = ['docker','exec','-e','PGPASSWORD',name,'psql','-h','127.0.0.1','-U','dbhub','-d','dbhub','-Atc','SELECT 1']
                env['PGPASSWORD'] = password
            elif kind == 'mysql':
                server_env = {'MYSQL_ROOT_PASSWORD':secrets.token_urlsafe(24), 'MYSQL_USER':'dbhub', 'MYSQL_PASSWORD':password, 'MYSQL_DATABASE':'dbhub'}
                args += ['--tmpfs','/var/lib/mysql:rw,size=2g']
                ready = ['docker','exec','-e','MYSQL_PWD',name,'mysql','--protocol=TCP','-h','127.0.0.1','-udbhub','-Ddbhub','-Nse','SELECT 1']
                env['MYSQL_PWD'] = password
            else:
                server_env = {}
                redis_conf.write_text('save ""\nappendonly no\nrequirepass '+password+'\n')
                redis_conf.chmod(0o600)
                args += ['--user',f'{os.getuid()}:{os.getgid()}','--tmpfs','/data:rw,size=128m,mode=1777','--mount',f'type=bind,src={redis_conf},dst=/run/compat.conf,readonly']
                ready = ['docker','exec','-e','REDISCLI_AUTH',name,'redis-cli','--raw','PING']
                env['REDISCLI_AUTH'] = password
            with open(private, 'w', opener=lambda p, flags: os.open(p, flags, 0o600)) as stream:
                for key, value in server_env.items():
                    stream.write(key+'='+value+'\n')
            args += ['--env-file',private,image]
            if kind == 'redis':
                args += ['redis-server','/run/compat.conf']
            if command(args, stdout=logs, stderr=logs).returncode:
                raise RuntimeError('目标容器启动失败')
            deadline = time.monotonic()+180
            while command(ready, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
                state = capture(['docker','inspect','--format','{{.State.Status}}',name])
                if state != 'running' or time.monotonic() > deadline:
                    command(['docker','logs','--tail','20',name], stdout=logs, stderr=logs)
                    raise RuntimeError('目标数据库未就绪')
                time.sleep(1)
            test_env = dict(os.environ)
            test_env.update({key:base[key] for key in ('TEST_POSTGRES_DSN','TEST_MYSQL_DSN','TEST_REDIS_URL')})
            # Keep the application metadata on its supported PostgreSQL 16
            # instance while exercising each independent business datasource.
            test_env['TEST_METADATA_DSN'] = base['TEST_POSTGRES_DSN']
            encoded = quote(password, safe='')
            if kind == 'postgres':
                test_env['TEST_POSTGRES_DSN'] = f'postgres://dbhub:{encoded}@{name}:5432/dbhub?sslmode=disable'
            elif kind == 'mysql':
                test_env['TEST_MYSQL_DSN'] = f'dbhub:{password}@tcp({name}:3306)/dbhub?parseTime=true'
            else:
                test_env['TEST_REDIS_URL'] = f'redis://:{encoded}@{name}:6379/15'
            test_env.update(DBHUB_MATRIX_ENGINE=kind, DBHUB_MATRIX_BRANCH=branch)
            exec_args = [WRAPPER,'exec','-T']
            for key in ('TEST_POSTGRES_DSN','TEST_METADATA_DSN','TEST_MYSQL_DSN','TEST_REDIS_URL','DBHUB_MATRIX_ENGINE','DBHUB_MATRIX_BRANCH'):
                exec_args += ['-e', key]
            exec_args += ['backend','go','test','-race','-count=1','-json']
            print(f'验证 {kind} {branch}：真实目标操作和 HTTP 契约', flush=True)
            server_pattern = '^(TestVersionCompatibilityHTTP|TestRealHTTPBackend'+('|TestDatasourceReliability|TestMetadataSyncReliability' if kind == 'postgres' else '')+')$'
            for package, pattern in [('./internal/target','^(TestVersionMatrixTarget|TestIntegrationRealSQLAndTransactions)$'),('./internal/server',server_pattern)]:
                if source_fingerprint() != fingerprint or migration_fingerprint() != migration_hash:
                    raise RuntimeError('运行期间后端源文件改变，本轮结果无效')
                if command(exec_args+[package,'-run',pattern], env=test_env, stdout=logs, stderr=logs).returncode:
                    raise RuntimeError('真实兼容测试失败')
        events = []
        for line in log.read_text().splitlines():
            if line.startswith('{'):
                events.append(json.loads(line))
        passes = [e['Test'] for e in events if e.get('Action')=='pass' and e.get('Test')]
        skips = [e['Test'] for e in events if e.get('Action')=='skip' and e.get('Test')]
        profiles = [json.loads(e['Output'].split('DBHUB_COMPAT_RESULT ',1)[1]) for e in events if 'DBHUB_COMPAT_RESULT ' in e.get('Output','')]
        if skips or not required_tests(kind).issubset(passes) or len(profiles)!=1 or profiles[0]['engine']!=kind or profiles[0]['branch']!=branch:
            raise RuntimeError('测试未完整执行或目标版本不匹配')
        result.update(version=profiles[0]['version'], passed_tests=passes, skipped_tests=skips, valid=True)
    except Exception as error:
        result['failure'] = str(error) if isinstance(error, RuntimeError) else '验收运行器发生错误'
    finally:
        command(['docker','rm','-f',name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        remaining = command(['docker','ps','-a','--filter','name=^/'+name+'$','--quiet'], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
        result['cleanup_succeeded'] = remaining.returncode == 0 and not remaining.stdout.strip()
        private.unlink(missing_ok=True)
        redis_conf.unlink(missing_ok=True)
        if not existed and not keep_images:
            command(['docker','image','rm',image], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        result['valid'] = result['valid'] and result['cleanup_succeeded']
    save_json(out/(label+'.json'), result)
    print(f'{"PASS" if result["valid"] else "FAIL"} {kind} {branch} '+result.get('version','')+f'；报告 {out/(label+".json")}', flush=True)
    return result

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--engine', choices=['mysql','postgres','redis'])
    parser.add_argument('--branch')
    parser.add_argument('--keep-images', action='store_true')
    parser.add_argument('--record-from', type=Path, help='核对完整报告与当前后端实现指纹后更新实测版本')
    parser.add_argument('--resume-from', type=Path, help='后端实现未变化时复用完整报告中的已通过项，仅重跑失败项')
    opts = parser.parse_args()
    if opts.record_from:
        record_verified(opts.record_from)
        return 0
    previous = None
    if opts.resume_from:
        if opts.engine or opts.branch:
            parser.error('--resume-from 只用于完整矩阵，不能与版本筛选同时使用')
        previous = json.loads(opts.resume_from.read_text())
        expected = {(kind, branch, image) for kind, branch, image in TARGETS}
        actual = {(t['engine'], t['branch'], t['image']) for t in previous['targets']}
        if previous.get('backend_go_sha256') != source_fingerprint() or previous.get('backend_migrations_sha256') != migration_fingerprint() or actual != expected or len(previous['targets']) != len(TARGETS):
            raise RuntimeError('只能恢复同一后端实现的完整矩阵报告')
    targets = [t for t in TARGETS if (not opts.engine or t[0]==opts.engine) and (not opts.branch or t[1]==opts.branch)]
    if not targets:
        parser.error('没有匹配的版本')
    base = dict(line.split('=',1) for line in (STATE/'runtime.env').read_text().splitlines() if '=' in line)
    if not all(base.get(k) for k in ('TEST_POSTGRES_DSN','TEST_MYSQL_DSN','TEST_REDIS_URL')):
        raise RuntimeError('先启动 Cloud 开发服务并准备真实测试连接')
    out = STATE/'compatibility-artifacts'/datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    out.mkdir(parents=True, mode=0o700, exist_ok=False)
    fingerprint = source_fingerprint()
    migration_hash = migration_fingerprint()
    report = {'started_at':datetime.now(timezone.utc).isoformat(), 'source_revision':capture(['git','-C',ROOT,'rev-parse','HEAD']), 'source_dirty':bool(capture(['git','-C',ROOT,'status','--porcelain'])), 'backend_go_sha256':fingerprint, 'backend_migrations_sha256':migration_hash, 'runner_go_version':capture([WRAPPER,'exec','-T','backend','go','version']), 'metadata_postgres_version':capture([WRAPPER,'exec','-T','postgres','sh','-ec','psql -X -At -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SHOW server_version"']), 'targets':[]}
    if previous:
        report['resumed_from'] = opts.resume_from.parent.name+'/'+opts.resume_from.name
        report['previous_report_sha256'] = hashlib.sha256(opts.resume_from.read_bytes()).hexdigest()
    for target in targets:
        reused = next((t for t in previous['targets'] if (t['engine'],t['branch']) == target[:2] and completed_target(t)), None) if previous else None
        if reused:
            result = dict(reused, reused_from=report['resumed_from'])
            save_json(out/(target[0]+'-'+target[1]+'.json'), result)
            print(f'复用 PASS {target[0]} {target[1]} {result["version"]}（同一后端实现）', flush=True)
        else:
            result = exercise(*target,out,base,fingerprint,migration_hash,opts.keep_images)
        report['targets'].append(result)
    report['finished_at'] = datetime.now(timezone.utc).isoformat()
    report['valid'] = all(t['valid'] for t in report['targets']) and source_fingerprint()==fingerprint and migration_fingerprint()==migration_hash
    save_json(out/'matrix.json', report)
    print(f'矩阵 {len(targets)} 项，整体有效={report["valid"]}；{out/"matrix.json"}', flush=True)
    return 0 if report['valid'] else 1

if __name__ == '__main__':
    def interrupted(signum, frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    raise SystemExit(main())
