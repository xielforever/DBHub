#!/usr/bin/env python3
"""Validate or publish the DBHub Helm release for xyc/dbhub."""

import argparse
import base64
from contextlib import contextmanager
from copy import deepcopy
from datetime import datetime, timezone
import getpass
import hashlib
import http.client
import json
import os
from pathlib import Path
import queue
import re
import secrets
import shutil
import subprocess
import sys
import tempfile
import threading
import time

import yaml


ROOT = Path(__file__).resolve().parents[2]
CHART = ROOT / "deploy/helm/dbhub"
LOCAL = ROOT / ".local-docs/helm"
VALUES = LOCAL / "values.xyc-dbhub.draft.yaml"
RELEASES = LOCAL / "xyc-releases"
RELEASE = CONTEXT = NAMESPACE = "dbhub"
FRONTEND = "dbhub-dbhub-frontend"
REGISTRY = "swr.cn-east-2.myhuaweicloud.com/dx_x2era"
COMPONENTS = ("backend", "frontend")
DIGEST = re.compile(r"sha256:[a-f0-9]{64}")
ELB_ID = "kubernetes.io/elb.id"
ELB_STATUS_ANNOTATION = "kubernetes.io/elb.eip-id"


class ReleaseError(Exception):
    """A failure that can be reported without printing command output or credentials."""


def require(condition, message):
    if not condition:
        raise ReleaseError(message)


def run(command, stage, *, timeout=30, data=None, show_output=False, env=None):
    try:
        result = subprocess.run(
            command, cwd=ROOT, input=data, timeout=timeout, env=env,
            capture_output=not show_output, encoding="utf-8", errors="replace",
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise ReleaseError(f"{stage}失败（{type(exc).__name__}）") from None
    require(result.returncode == 0, f"{stage}失败（退出码 {result.returncode}）")
    return result.stdout


def read_values(path):
    try:
        return yaml.safe_load(path.read_text(encoding="utf-8-sig"))
    except (OSError, yaml.YAMLError):
        raise ReleaseError(f"无法读取 YAML 配置 {path}") from None


def validate_values(values):
    require(values["ingress"]["enabled"] is False, "xyc 发布要求关闭 Ingress")
    service = values["frontend"]["service"]
    require(service["type"] == "LoadBalancer" and isinstance(service.get("nodePort"), int),
            "xyc 前端须配置现有 LoadBalancer 和节点端口")
    annotations = service["annotations"]
    require(bool(annotations.get(ELB_ID)) and "kubernetes.io/elb.autocreate" not in annotations,
            "xyc 前端须指定现有 ELB，避免自动创建")
    require(values["existingSecret"] == "dbhub-runtime", "运行 Secret 须为 dbhub-runtime")
    require({"name": "default-secret"} in values["imagePullSecrets"], "缺少 SWR 拉取 Secret")
    for component in COMPONENTS:
        image = values[component]["image"]
        require(image["repository"] == f"{REGISTRY}/dbhub-{component}",
                f"{component} 镜像仓库与 xyc 发布目标不符")
        require(DIGEST.fullmatch(image["digest"]), f"{component} 镜像缺少有效摘要")


def render(helm, chart, values_path, values):
    flags = ["--values", str(values_path), "--kube-version", "1.34.3"]
    run([helm, "lint", "--strict", str(chart), *flags], "Helm lint")
    manifest = run([helm, "template", RELEASE, str(chart), "--namespace", NAMESPACE, *flags], "Helm 渲染")
    objects = [item for item in yaml.safe_load_all(manifest) if item]
    require(all(item["kind"] not in ("Ingress", "Secret") for item in objects),
            "发布清单必须关闭 Ingress 并引用外部 Secret")
    services = {item["metadata"]["name"]: item for item in objects if item["kind"] == "Service"}
    check_frontend_service(services[FRONTEND], values)
    require(services["dbhub-dbhub-backend"]["spec"]["type"] == "ClusterIP", "后端 Service 须使用 ClusterIP")
    deployments = {item["metadata"]["name"]: item for item in objects if item["kind"] == "Deployment"}
    for component in COMPONENTS:
        pod = deployments[f"dbhub-dbhub-{component}"]["spec"]["template"]["spec"]
        image = values[component]["image"]
        require(pod["containers"][0]["image"] == image["repository"] + "@" + image["digest"],
                f"{component} 清单未按摘要固定镜像")
        require({"name": "default-secret"} in pod.get("imagePullSecrets", []), f"{component} 缺少拉取 Secret")
    frontend = deployments[FRONTEND]["spec"]["template"]["spec"]["containers"][0]
    require(any(port["name"] == "http" and port["containerPort"] == 8080 for port in frontend.get("ports", [])),
            "前端容器的 http 端口须为 8080")
    backend = deployments["dbhub-dbhub-backend"]["spec"]["template"]["spec"]["containers"][0]
    require({"secretRef": {"name": "dbhub-runtime"}} in backend.get("envFrom", []),
            "后端未引用 dbhub-runtime")
    return manifest


def check_frontend_service(service, values):
    expected = values["frontend"]["service"]
    spec = service["spec"]
    ports = spec["ports"]
    require(spec["type"] == "LoadBalancer" and len(ports) == 1, "前端 Service 不是预期的 LoadBalancer")
    port = ports[0]
    require(port["name"] == "http" and port.get("protocol", "TCP") == "TCP"
            and (port["port"], port["targetPort"], port.get("nodePort")) == (80, 8080, expected["nodePort"]),
            "前端 Service 端口与 xyc values 不符")
    require(spec.get("externalTrafficPolicy") == expected.get("externalTrafficPolicy", "Cluster"),
            "前端 Service 流量策略与 xyc values 不符")
    live = service["metadata"].get("annotations", {})
    elb = {key: value for key, value in live.items()
           if key.startswith("kubernetes.io/elb.") and key != ELB_STATUS_ANNOTATION}
    require(elb == expected["annotations"], "前端 Service ELB 注解与 xyc values 不符")


def target_commands(args):
    config = read_values(args.kubeconfig)
    context = next((item["context"] for item in config["contexts"] if item["name"] == CONTEXT), {})
    require(context.get("cluster") == "xyc" and context.get("namespace") == NAMESPACE,
            "kubeconfig 中的 dbhub Context 须指向 xyc/dbhub")
    common = ["--kubeconfig", str(args.kubeconfig.resolve()), "--namespace", NAMESPACE]
    return ([args.kubectl, *common, "--context", CONTEXT],
            [args.helm, *common, "--kube-context", CONTEXT])


def preflight(kubectl, values):
    ns = json.loads(run(kubectl + ["get", "namespace", NAMESPACE, "-o", "json"], "读取命名空间"))
    require(ns["status"]["phase"] == "Active", "dbhub 命名空间尚未就绪")
    for name in ("dbhub-postgres", "dbhub-redis"):
        deployment = json.loads(run(kubectl + ["get", "deployment", name, "-o", "json"], f"检查 {name}"))
        require(deployment.get("status", {}).get("readyReplicas", 0) > 0, f"{name} 未就绪")
    for kind, name in (("secret", "dbhub-runtime"), ("secret", "default-secret"),
                       ("configmap", "dbhub-datastore-ca")):
        run(kubectl + ["get", kind, name, "-o", "name"], f"检查 {name}")
    existing = run(kubectl + ["get", "service", FRONTEND, "--ignore-not-found", "-o", "json"], "核对现有 ELB")
    if existing.strip():
        service = json.loads(existing)
        annotations = service["metadata"].get("annotations", {})
        require(annotations.get("meta.helm.sh/release-name") == RELEASE
                and annotations.get("meta.helm.sh/release-namespace") == NAMESPACE,
                "现有前端 Service 的 Helm 归属与发布目标不符")
        check_frontend_service(service, values)


def docker_command(wsl):
    if not wsl:
        return ["docker"]
    require(os.name == "nt", "--docker-wsl 适用于 Windows")
    mounted = "/mnt/" + ROOT.drive[0].lower() + "/" + ROOT.relative_to(ROOT.anchor).as_posix()
    return ["wsl.exe", "-d", wsl, "--cd", mounted, "--", "docker"]


def snapshot_sources(destination):
    files = run(["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--",
                 "backend", "frontend", "deploy/helm/dbhub"], "枚举构建源码")
    fingerprint = hashlib.sha256()
    for name in sorted(set(files.split("\0")) - {""}):
        source = ROOT / name
        require(not source.is_symlink(), f"构建输入含符号链接 {name}")
        if not source.exists():
            continue
        content = source.read_bytes()
        copied = destination / name
        copied.parent.mkdir(parents=True, exist_ok=True)
        copied.write_bytes(content)
        shutil.copymode(source, copied)
        mode = copied.stat().st_mode & 0o777
        fingerprint.update(name.encode("utf-8") + b"\0" + mode.to_bytes(2, "big") + hashlib.sha256(content).digest())
    require(all((destination / part / "Dockerfile").is_file() for part in COMPONENTS), "构建快照缺少 Dockerfile")
    return fingerprint.hexdigest()


def publish_images(docker, snapshot, values, tag):
    candidate = deepcopy(values)
    references = {}
    for component in COMPONENTS:
        image = candidate[component]["image"]
        reference = image["repository"] + ":" + tag
        references[component] = reference
        context = (snapshot / component).relative_to(ROOT).as_posix()
        run(docker + ["build", "--platform", "linux/amd64", "-t", reference,
                      "-f", context + "/Dockerfile", context],
            f"构建 {component} 镜像", timeout=1200, show_output=True)
    for component, reference in references.items():
        output = run(docker + ["image", "push", "--platform", "linux/amd64", reference],
                     f"推送 {component} 镜像", timeout=900)
        pushed = re.search(r"\bdigest: (sha256:[a-f0-9]{64})\b", output)
        require(pushed, f"SWR 未返回 {component} 镜像摘要")
        actual = run(docker + ["buildx", "imagetools", "inspect", "--format", "{{.Manifest.Digest}}", reference],
                     f"读回 {component} 摘要", timeout=90).strip()
        require(actual == pushed.group(1), f"{component} 的推送摘要与 SWR 读回值不符")
        candidate[component]["image"].update(tag=tag, digest=actual)
        print(f"已推送并核对 {reference} {actual}", flush=True)
    return candidate


def verify_deployments(kubectl, values):
    for component in COMPONENTS:
        name = f"dbhub-dbhub-{component}"
        deployment = json.loads(run(kubectl + ["get", "deployment", name, "-o", "json"], f"读回 {name}"))
        spec, status = deployment["spec"], deployment["status"]
        image = values[component]["image"]
        containers = spec["template"]["spec"]["containers"]
        require(any(c["image"] == image["repository"] + "@" + image["digest"] for c in containers)
                and status.get("observedGeneration") == deployment["metadata"]["generation"]
                and status.get("readyReplicas") == spec["replicas"], f"{name} 的镜像或就绪状态未达到预期")


def verify_migrations(kubectl, snapshot):
    expected = sorted(path.name for path in (snapshot / "backend/internal/db/migrations").glob("*.sql"))
    query = r"""export PGPASSWORD="$DBHUB_DATABASE_PASSWORD" PGSSLMODE=verify-full PGSSLROOTCERT=/etc/dbhub/trust/ca.crt PGCONNECT_TIMEOUT=5
psql -XqAt -h dbhub-postgres -U dbhub_owner -d dbhub -v ON_ERROR_STOP=1 -c 'SELECT version FROM schema_migrations ORDER BY version'
"""
    env = dict(os.environ, KUBECTL_REMOTE_COMMAND_WEBSOCKETS="false")
    actual = run(kubectl + ["exec", "deploy/dbhub-postgres", "--", "sh", "-ec", query],
                 "查询迁移记录", env=env).splitlines()
    require(expected and actual == expected, f"迁移记录与构建快照不一致（实际 {len(actual)} 条，预期 {len(expected)} 条）")
    print(f"迁移验证通过，共 {len(actual)} 条，最新 {actual[-1]}", flush=True)
    return {"count": len(actual), "latest": actual[-1]}


@contextmanager
def forward_frontend(kubectl):
    env = dict(os.environ, KUBECTL_PORT_FORWARD_WEBSOCKETS="false")
    with subprocess.Popen(
        kubectl + ["port-forward", "--address", "127.0.0.1", "service/" + FRONTEND, ":80"],
        cwd=ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        encoding="utf-8", errors="replace",
    ) as process:
        lines = queue.Queue()

        def drain():
            for line in process.stdout:
                lines.put(line)

        threading.Thread(target=drain, daemon=True).start()
        try:
            deadline = time.monotonic() + 25
            while time.monotonic() < deadline:
                try:
                    line = lines.get(timeout=1)
                except queue.Empty:
                    if process.poll() is not None:
                        break
                    continue
                match = re.search(r"Forwarding from 127\.0\.0\.1:(\d+) ->", line)
                if match:
                    yield int(match.group(1))
                    return
            raise ReleaseError("前端临时转发未就绪")
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()


def request(port, method, path, payload=None, token=None):
    headers = {}
    if payload is not None:
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=10)
    try:
        connection.request(method, path, None if payload is None else json.dumps(payload), headers)
        response = connection.getresponse()
        return response.status, response.read(2 * 1024 * 1024)
    finally:
        connection.close()


def verify_http(port, username, password):
    for path in ("/healthz", "/api/health", "/api/ready", "/login"):
        status, body = request(port, "GET", path)
        require(status == 200, f"{path} 检查失败（HTTP {status}）")
        if path == "/healthz":
            require(body.strip() == b"ok", "前端健康检查返回异常")
        elif path == "/login":
            require(b'id="app"' in body, "前端登录页返回异常")
        else:
            require(json.loads(body).get("code") == 0, f"{path} 返回异常")
    status, body = request(port, "POST", "/api/v1/auth/login", {"username": username, "password": password})
    require(status == 200, f"登录失败（HTTP {status}）")
    token = (json.loads(body).get("data") or {}).get("access_token")
    require(isinstance(token, str) and token, "登录未签发会话，请核对管理员认证方式")
    identity_ok = False
    try:
        status, body = request(port, "GET", "/api/v1/auth/me", token=token)
        identity = json.loads(body).get("data") or {}
        identity_ok = status == 200 and identity.get("username") == username and identity.get("role") == "admin"
    finally:
        status, body = request(port, "POST", "/api/v1/auth/logout", token=token)
        require(status == 200 and (json.loads(body).get("data") or {}).get("revoked") is True,
                "验收会话退出失败")
    require(identity_ok, "登录会话身份核对失败")
    require(request(port, "GET", "/api/v1/auth/me", token=token)[0] == 401, "退出后会话仍可用")
    print("健康检查、管理员登录和退出验证通过", flush=True)


def release(args, values):
    kubectl, helm = target_commands(args)
    helm_env = dict(os.environ, HELM_DRIVER="secret")
    preflight(kubectl, values)
    username = values.get("config", {}).get("APP_ADMIN_USERNAME", "admin")
    if args.prompt_login_password:
        require(sys.stdin.isatty(), "请输入密码的终端必须支持交互")
        password = getpass.getpass(f"{username} 的当前 DBHub 登录密码：")
    else:
        secret = json.loads(run(kubectl + ["get", "secret", "dbhub-runtime", "-o", "json"], "读取登录验收凭据"))
        password = base64.b64decode(secret["data"]["APP_ADMIN_PASSWORD"], validate=True).decode("utf-8")
    require(password, "登录验收密码为空")
    RELEASES.mkdir(parents=True, exist_ok=True)
    lock = RELEASES / ".lock"
    try:
        lock.mkdir()
    except FileExistsError:
        raise ReleaseError(f"已有发布进程占用 {lock}") from None
    try:
        with tempfile.TemporaryDirectory(prefix="snapshot-", dir=RELEASES) as directory:
            snapshot = Path(directory)
            head = run(["git", "rev-parse", "HEAD"], "读取源码提交").strip()
            fingerprint = snapshot_sources(snapshot)
            tag = f"xyc-{datetime.now(timezone.utc):%Y%m%dT%H%M%SZ}-{head[:8]}-{fingerprint[:10]}-{secrets.token_hex(3)}".lower()
            print(f"构建快照 {head} / sha256:{fingerprint}", flush=True)
            candidate = publish_images(docker_command(args.docker_wsl), snapshot, values, tag)
            record = RELEASES / tag
            record.mkdir()
            release_values = record / "values.yaml"
            release_values.write_text(yaml.safe_dump(candidate, sort_keys=False, allow_unicode=True), encoding="utf-8")
            receipt = {"git_head": head, "source_sha256": fingerprint, "tag": tag, "phase": "images_pushed"}
            receipt_path = record / "release.json"

            def save_receipt():
                temporary = receipt_path.with_suffix(".tmp")
                temporary.write_text(json.dumps(receipt, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
                temporary.replace(receipt_path)

            save_receipt()
            print(f"已回填发布 values {release_values}", flush=True)
            chart = snapshot / "deploy/helm/dbhub"
            manifest = render(args.helm, chart, release_values, candidate)
            preflight(kubectl, candidate)
            run(kubectl + ["apply", "--dry-run=server", "-f", "-"], "服务端清单校验", data=manifest)
            receipt["phase"] = "deploying"
            save_receipt()
            run(helm + ["upgrade", "--install", RELEASE, str(chart), "--values", str(release_values),
                        "--wait", "--timeout", "10m"], "Helm 发布", timeout=660, show_output=True, env=helm_env)
            status = json.loads(run(helm + ["status", RELEASE, "-o", "json"], "核对 Helm 状态", env=helm_env))
            require(status["info"]["status"] == "deployed", "Helm 发布状态未就绪")
            receipt.update(phase="deployed", helm_revision=status["version"])
            save_receipt()
            live = json.loads(run(kubectl + ["get", "service", FRONTEND, "-o", "json"], "读回前端 Service"))
            check_frontend_service(live, candidate)
            verify_deployments(kubectl, candidate)
            receipt["migrations"] = verify_migrations(kubectl, snapshot)
            save_receipt()
            with forward_frontend(kubectl) as port:
                verify_http(port, username, password)
            receipt["phase"] = "verified"
            save_receipt()
            print(f"发布验收通过，记录 {receipt_path}", flush=True)
    finally:
        lock.rmdir()


def main(argv=None):
    parser = argparse.ArgumentParser(description="xyc/dbhub 本地发布，默认仅离线校验")
    parser.add_argument("--apply", action="store_true", help="构建并推送镜像、部署和验收")
    parser.add_argument("--values", type=Path, default=VALUES)
    parser.add_argument("--kubeconfig", type=Path, default=ROOT / "dbhub-kubeconfig.yaml")
    parser.add_argument("--helm", default="helm")
    parser.add_argument("--kubectl", default="kubectl")
    parser.add_argument("--docker-wsl", metavar="DISTRO")
    parser.add_argument("--prompt-login-password", action="store_true")
    args = parser.parse_args(argv)
    try:
        values = read_values(args.values)
        validate_values(values)
        render(args.helm, CHART, args.values.resolve(), values)
        print("本地 Helm 校验通过，前端为 LoadBalancer 80 → 8080，Ingress 关闭", flush=True)
        if args.apply:
            release(args, values)
    except ReleaseError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    except (KeyError, OSError, ValueError, TypeError, yaml.YAMLError, http.client.HTTPException) as exc:
        print(f"发布操作失败（{type(exc).__name__}）", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
