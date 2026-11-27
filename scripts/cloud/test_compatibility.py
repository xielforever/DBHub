import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import compatibility


class CompatibilityReportTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.backend = self.root / 'backend'
        root_patch = patch.object(compatibility, 'ROOT', self.root)
        root_patch.start()
        self.addCleanup(root_patch.stop)
        self.inputs = {
            'go.mod': 'module dbhub',
            'go.sum': '',
            'internal/iam/catalog.json': '{}',
            'internal/iam/routes.json': '{}',
            'internal/businessops/protocol.sql': 'SELECT 1',
            'internal/db/migrations/0001.sql': 'SELECT 1',
            'internal/target/compatibility.go': 'package target',
            'vendor/example/driver.c': 'int example = 1;',
            'internal/target/compatibility_matrix.json': '[]',
        }
        for name, contents in self.inputs.items():
            path = self.backend / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(contents, encoding='utf-8')
        self.fingerprints = compatibility.source_fingerprints()
        self.matrix_path = self.backend / 'internal/target/compatibility_matrix.json'
        self.report_path = self.root / 'matrix.json'

    def write_report(self, fingerprints):
        targets = [
            {
                'engine': engine,
                'branch': branch,
                'image': image,
                'version': branch + '.1',
                'valid': True,
                'cleanup_succeeded': True,
                'skipped_tests': [],
                'passed_tests': sorted(compatibility.required_tests(engine)),
            }
            for engine, branch, image in compatibility.TARGETS
        ]
        self.report_path.write_text(
            json.dumps({'valid': True, **fingerprints, 'targets': targets}),
            encoding='utf-8',
        )

    def test_embedded_resources_and_vendor_changes_invalidate_report(self):
        self.write_report(self.fingerprints)
        for name in (
            'internal/iam/catalog.json',
            'internal/iam/routes.json',
            'internal/businessops/protocol.sql',
            'vendor/example/driver.c',
        ):
            with self.subTest(path=name):
                path = self.backend / name
                path.write_text('changed', encoding='utf-8')
                changed = compatibility.source_fingerprints()
                self.assertNotEqual(changed['backend_source_sha256'], self.fingerprints['backend_source_sha256'])
                self.assertEqual(changed['backend_migrations_sha256'], self.fingerprints['backend_migrations_sha256'])
                with self.assertRaisesRegex(RuntimeError, '不能更新已验证版本'):
                    compatibility.record_verified(self.report_path)
                path.write_text(self.inputs[name], encoding='utf-8')
        self.assertEqual(self.matrix_path.read_text(encoding='utf-8'), '[]')

    def test_migration_is_a_separate_input(self):
        (self.backend / 'internal/db/migrations/0001.sql').write_text('SELECT 2', encoding='utf-8')
        changed = compatibility.source_fingerprints()
        self.assertEqual(changed['backend_source_sha256'], self.fingerprints['backend_source_sha256'])
        self.assertNotEqual(changed['backend_migrations_sha256'], self.fingerprints['backend_migrations_sha256'])

    def test_legacy_report_cannot_update_or_resume(self):
        old_report = dict(self.fingerprints)
        old_report['backend_go_sha256'] = old_report.pop('backend_source_sha256')
        self.write_report(old_report)
        with self.assertRaisesRegex(RuntimeError, '不能更新已验证版本'):
            compatibility.record_verified(self.report_path)
        with patch.object(sys, 'argv', ['compatibility.py', '--resume-from', str(self.report_path)]):
            with self.assertRaisesRegex(RuntimeError, '只能恢复同一后端实现'):
                compatibility.main()

    def test_resume_rejects_report_after_embedded_change(self):
        self.write_report(self.fingerprints)
        (self.backend / 'internal/iam/routes.json').write_text('[]', encoding='utf-8')
        with patch.object(sys, 'argv', ['compatibility.py', '--resume-from', str(self.report_path)]):
            with self.assertRaisesRegex(RuntimeError, '只能恢复同一后端实现'):
                compatibility.main()

    def test_complete_current_report_can_update_matrix_without_changing_inputs(self):
        self.write_report(self.fingerprints)
        compatibility.record_verified(self.report_path)
        matrix = json.loads(self.matrix_path.read_text(encoding='utf-8'))
        self.assertEqual(len(matrix), len(compatibility.TARGETS))
        self.assertEqual(compatibility.source_fingerprints(), self.fingerprints)


if __name__ == '__main__':
    unittest.main()
