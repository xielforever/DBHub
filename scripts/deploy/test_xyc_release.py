from contextlib import nullcontext, redirect_stdout
from io import StringIO
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import xyc_release as release


def values():
    return {
        "ingress": {"enabled": False},
        "existingSecret": "dbhub-runtime",
        "imagePullSecrets": [{"name": "default-secret"}],
        "backend": {"image": {"repository": release.REGISTRY + "/dbhub-backend", "digest": "sha256:" + "a" * 64}},
        "frontend": {
            "image": {"repository": release.REGISTRY + "/dbhub-frontend", "digest": "sha256:" + "b" * 64},
            "service": {"type": "LoadBalancer", "nodePort": 30591, "annotations": {release.ELB_ID: "fixture"}},
        },
    }


def envelope(data):
    return json.dumps({"code": 0, "data": data}).encode()


class XycReleaseTests(unittest.TestCase):
    def test_default_only_renders_locally(self):
        with patch.object(release, "read_values", return_value=values()), \
                patch.object(release, "render") as render, patch.object(release, "release") as apply:
            self.assertEqual(release.main([]), 0)
        render.assert_called_once()
        apply.assert_not_called()

    def test_both_images_are_built_before_a_push_and_mismatch_stops_release(self):
        fake_digest = "sha256:" + "c" * 64
        with patch.object(release, "run", side_effect=[None, None, f"digest: {fake_digest}", "sha256:" + "d" * 64]) as run:
            with self.assertRaisesRegex(release.ReleaseError, "读回值不符"):
                release.publish_images(["docker"], release.ROOT / "snapshot", values(), "test")
        self.assertEqual([call.args[0][1:3] for call in run.call_args_list],
                         [["build", "--platform"], ["build", "--platform"], ["image", "push"], ["buildx", "imagetools"]])

    def test_migration_verification_detects_a_missing_version(self):
        with tempfile.TemporaryDirectory() as directory:
            snapshot = Path(directory)
            migrations = snapshot / "backend/internal/db/migrations"
            migrations.mkdir(parents=True)
            for name in ("0001_initial.sql", "0002_update.sql"):
                (migrations / name).touch()
            with patch.object(release, "run", return_value="0001_initial.sql\n"):
                with self.assertRaisesRegex(release.ReleaseError, "迁移记录与构建快照不一致"):
                    release.verify_migrations(["kubectl"], snapshot)

    def test_login_verifies_identity_and_revokes_its_session(self):
        responses = [
            (200, b"ok"), (200, envelope({})), (200, envelope({})), (200, b'<div id="app"></div>'),
            (200, envelope({"access_token": "fixture-token"})),
            (200, envelope({"username": "admin", "role": "admin"})),
            (200, envelope({"revoked": True})),
            (401, b""),
        ]
        with patch.object(release, "request", side_effect=responses) as request:
            release.verify_http(12345, "admin", "fixture-password")
        self.assertEqual([call.args[2] for call in request.call_args_list[-4:]],
                         ["/api/v1/auth/login", "/api/v1/auth/me", "/api/v1/auth/logout", "/api/v1/auth/me"])
        self.assertEqual(request.call_args_list[-2].kwargs["token"], "fixture-token")

    def test_release_uses_recorded_digests_and_records_only_completed_checks(self):
        args = SimpleNamespace(helm="helm", docker_wsl=None, prompt_login_password=False)
        digest = "sha256:" + "c" * 64
        events = []

        def command(_, stage, **kwargs):
            events.append(stage)
            if stage == "读取登录验收凭据":
                return json.dumps({"data": {"APP_ADMIN_PASSWORD": "Zml4dHVyZQ=="}})
            if stage == "读取源码提交":
                return "a" * 40
            if stage.startswith("推送"):
                return f"digest: {digest}"
            if stage.startswith("读回") and stage.endswith("摘要"):
                return digest
            if stage == "核对 Helm 状态":
                return json.dumps({"info": {"status": "deployed"}, "version": 2})
            if stage == "读回前端 Service":
                return "{}"
            return ""

        with tempfile.TemporaryDirectory() as directory:
            records = Path(directory)
            with patch.multiple(release, RELEASES=records, ROOT=records), \
                    patch.object(release, "target_commands", return_value=(["kubectl"], ["helm"])), \
                    patch.object(release, "preflight"), patch.object(release, "snapshot_sources", return_value="b" * 64), \
                    patch.object(release, "run", side_effect=command), \
                    patch.object(release, "render", return_value="manifest"), \
                    patch.object(release, "check_frontend_service"), patch.object(release, "verify_deployments"), \
                    patch.object(release, "verify_migrations", return_value={"count": 1, "latest": "0001_initial.sql"}), \
                    patch.object(release, "forward_frontend", return_value=nullcontext(12345)), \
                    patch.object(release, "verify_http") as verify_http, redirect_stdout(StringIO()):
                release.release(args, values())
            receipt_path, = records.glob("*/release.json")
            receipt = json.loads(receipt_path.read_text(encoding="utf-8"))
            config = release.read_values(receipt_path.with_name("values.yaml"))
            self.assertEqual([config[component]["image"]["digest"] for component in release.COMPONENTS], [digest, digest])
            self.assertEqual(receipt["phase"], "verified")
            self.assertEqual(receipt["helm_revision"], 2)
            self.assertFalse((records / ".lock").exists())
        self.assertLess(events.index("读回 frontend 摘要"), events.index("服务端清单校验"))
        self.assertLess(events.index("服务端清单校验"), events.index("Helm 发布"))
        verify_http.assert_called_once_with(12345, "admin", "fixture")


if __name__ == "__main__":
    unittest.main()
