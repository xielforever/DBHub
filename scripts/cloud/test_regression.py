"""Guard against successful commands disguising missing regression coverage."""
import contextlib
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

import regression


class RegressionEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / 'checks.json'

    def go_log(self, *events):
        self.path.write_text('\n'.join(json.dumps(event) for event in events))
        return self.path

    def test_no_tests_or_missing_required_test_cannot_pass(self):
        for events in [[], [dict(Action='pass', Test='TestOther')]]:
            with self.subTest(events=events), self.assertRaises(ValueError):
                regression.go_results(self.go_log(*events), required=['TestRequired'])

    def test_skipped_subtest_or_failed_test_cannot_pass(self):
        for action in ['skip', 'fail']:
            log = self.go_log(dict(Action='pass', Test='TestRequired'), dict(Action=action, Test='TestRequired/real-database'))
            with self.subTest(action=action), self.assertRaises(ValueError):
                regression.go_results(log, required=['TestRequired'])

    def test_optional_matrix_stays_explicitly_skipped(self):
        result = regression.go_results(self.go_log(dict(Action='pass', Test='TestRequired'),
                                                  dict(Action='skip', Test='TestVersionMatrixTarget')), required=['TestRequired'])
        self.assertEqual(result['passed_events_including_subtests'], 1)
        self.assertEqual(result['skipped'], ['TestVersionMatrixTarget'])
        self.assertIn('TestVersionMatrixTarget', result['optional_skip_reasons'])

    def test_incomplete_or_uncleaned_acceptance_cannot_pass(self):
        for result in [dict(completed=False, resources_removed=True, checks=['one']),
                       dict(completed=True, resources_removed=False, checks=['one']),
                       dict(completed=True, temporary_resources_removed=False, checks=['one']),
                       dict(completed=True, resources_removed=True, checks=[])]:
            self.path.write_text(json.dumps(result))
            with self.subTest(result=result), self.assertRaises(ValueError):
                regression.acceptance_result([self.path])

    def test_each_acceptance_must_create_one_new_report(self):
        for paths in [[], [self.path, self.path]]:
            with self.subTest(paths=paths), self.assertRaises(ValueError):
                regression.acceptance_result(paths)
        for field in ['resources_removed', 'temporary_resources_removed']:
            self.path.write_text(json.dumps(dict(completed=True, checks=['one'], **{field: True})))
            self.assertEqual(regression.acceptance_result([self.path])['checks'], 1)

    def frontend_log(self, prefix='#', counts=(14, 18, 10, 2), skipped=0):
        self.path.write_text('\n'.join(f'{prefix} {key} {value}' for count in counts for key, value in
                                      [('tests', count), ('pass', count - skipped), ('fail', 0),
                                       ('cancelled', 0), ('skipped', skipped), ('todo', 0)]))
        return self.path

    def test_frontend_tap_spec_and_color_reporters(self):
        for prefix in ['#', 'ℹ', '\x1b[36mℹ']:
            with self.subTest(prefix=prefix):
                self.assertEqual(regression.frontend_results(self.frontend_log(prefix)), 44)

    def test_frontend_missing_empty_or_skipped_suites_cannot_pass(self):
        for counts, skipped in [((14, 18, 10), 0), ((14, 18, 10, 0), 0), ((14, 18, 10, 2), 1)]:
            with self.subTest(counts=counts, skipped=skipped), self.assertRaises(ValueError):
                regression.frontend_results(self.frontend_log(counts=counts, skipped=skipped))

    def test_runner_persists_failure_exit_and_rejects_source_changes(self):
        def fake_git(*args):
            return '' if args == ('status', '--porcelain') else 'a' * 40
        images = dict(backend='unused', frontend='unused', backup='unused')
        for command, fingerprints, expected in [('raise SystemExit(7)', ['stable'], 1),
                                                 ('pass', ['before', 'after'], 1),
                                                 ('pass', ['stable', 'stable'], 0)]:
            with self.subTest(command=command, fingerprints=fingerprints), tempfile.TemporaryDirectory() as state:
                steps = [('probe', [sys.executable, '-c', command])]
                with patch.object(regression, 'git', side_effect=fake_git), \
                        patch.object(regression, 'source_fingerprint', side_effect=fingerprints), \
                        patch.object(regression, 'commands', return_value=(steps, images)), \
                        patch.dict(os.environ, DBHUB_STATE_DIR=state), \
                        patch.object(sys, 'argv', ['regression.py', '--profile', 'full']), \
                        contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                    self.assertEqual(regression.main(), expected)
                result = json.loads(next(Path(state).glob('regression-artifacts/*/summary.json')).read_text())
                self.assertEqual(result['completed'], expected == 0)
                if command != 'pass':
                    self.assertEqual(result['steps'][0]['exit_code'], 7)
                    self.assertEqual(result['steps'][0]['status'], 'failed')
                if fingerprints == ['before', 'after']:
                    self.assertIn('changed during regression', result['error'])

    def test_profile_scope_and_invalid_soak_arguments(self):
        for profile in ['smoke', 'full', 'release']:
            steps, _ = regression.commands(profile, 'test', 1800 if profile == 'release' else 0, 4)
            names = [name for name, _ in steps]
            command = next(cmd for name, cmd in steps if name == 'backend')
            self.assertIn('-race', command)
            self.assertEqual('-run' in command, profile == 'smoke')
            self.assertEqual('vet' in names, profile != 'smoke')
            self.assertEqual('production' in names, profile == 'release')
            if profile == 'release':
                command = next(cmd for name, cmd in steps if name == 'production')
                self.assertEqual(command[command.index('--duration') + 1], '1800')
        for args in [['--soak-seconds', '1800'], ['--profile', 'release', '--soak-seconds', '-1']]:
            with self.subTest(args=args), contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                regression.arguments(args)


if __name__ == '__main__':
    unittest.main()
