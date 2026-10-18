import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('docker_deploy', ROOT / 'deploy/docker/manage.py')
manage = importlib.util.module_from_spec(spec);spec.loader.exec_module(manage)


class DeploymentGuards(unittest.TestCase):
    def test_unowned_resources_are_never_modified(self):
        for kind in ['container', 'network', 'volume']:
            payload = {'Config': {'Labels': {}}} if kind == 'container' else {'Labels': {}}
            with self.subTest(kind=kind), patch.object(manage, 'docker', return_value=subprocess.CompletedProcess([], 0, json.dumps([payload]))), self.assertRaises(ValueError):
                manage.owned(kind, 'existing', 'test')

    def test_private_config_and_literal_secret_values(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'env';sentinel = Path(temp) / 'never-created'
            values = {'APP_SECRET_KEY': 'a' * 32, 'APP_ADMIN_PASSWORD': '$(touch ' + str(sentinel) + ')',
                      'DATABASE_URL': 'postgres://example/meta', 'REDIS_URL': 'redis://example',
                      'DBHUB_TARGET_ALLOWLIST': 'example:5432', 'DBHUB_BACKEND_IMAGE': 'backend:test', 'DBHUB_FRONTEND_IMAGE': 'frontend:test'}
            path.write_text(''.join(k + '=' + v + '\n' for k, v in values.items()));path.chmod(0o600)
            self.assertEqual(manage.read_config(path), values)
            self.assertFalse(sentinel.exists())
            path.chmod(0o644)
            with self.assertRaises(ValueError):manage.read_config(path)

    def test_source_build_refuses_nonempty_or_wrong_revision_output(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp);sentinel = path / 'important';sentinel.write_text('preserve')
            for marker in [None, 'wrong-revision']:
                if marker:(path / '.dbhub-build').write_text(marker)
                result = subprocess.run(['sh', str(ROOT / 'deploy/source/build.sh'), 'backend', temp], capture_output=True,
                                        env={**os.environ, 'DBHUB_ALLOW_DIRTY_BUILD': '1'})
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(sentinel.read_text(), 'preserve')


if __name__ == '__main__':
    unittest.main()
