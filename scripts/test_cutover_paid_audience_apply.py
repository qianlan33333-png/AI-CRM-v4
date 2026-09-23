import copy
import json
import tempfile
import unittest
from pathlib import Path
from cutover_paid_audience_apply import apply


class ReplayTest(unittest.TestCase):
    def test_lost_config_reply_reuses_key_and_never_duplicates_packages(self):
        self.exercise()

    def test_optional_rule_bundle_reuses_same_engine(self):
        rules = {i: {"name": "Survey rule " + str(i), "definition": {"schema_version": 1, "template_key": "questionnaire_submitted", "parameters": {"questionnaire_ids": [str(i)]}}, "refresh_mode": "every_3m", "refresh_cron_utc": ""} for i in (14, 38)}
        self.exercise(rules)

    def exercise(self, rules=None):
        packages, configs, receipts = {}, {}, {}
        calls = []
        lost = [False]
        def request(method, path, body, headers):
            key = headers.get('Idempotency-Key')
            calls.append((method, path, copy.deepcopy(body), key))
            if key in receipts:
                self.assertEqual(receipts[key][0], body)
                return receipts[key][1:]
            if method == 'POST' and path.endswith('/packages'):
                identifier = len(packages) + 1
                packages[identifier] = {'id': identifier, 'version': 1, 'lifecycle': 'draft'}
                status, value = 201, {'package': copy.deepcopy(packages[identifier])}
            else:
                identifier = int(path.split('/')[5])
                if method == 'GET':
                    return 200, {'configuration': copy.deepcopy(configs[identifier])} if path.endswith('/configuration') else {'package': copy.deepcopy(packages[identifier])}
                if path.endswith('/configuration'):
                    self.assertEqual(body['expected_package_version'], packages[identifier]['version'])
                    configs[identifier] = {'definition': body['definition'], 'refresh_mode': body['refresh_mode']}
                    packages[identifier]['version'] += 1
                    status, value = 200, {'configuration': configs[identifier]}
                elif path.endswith('/activate'):
                    self.assertEqual(body['expected_version'], packages[identifier]['version'])
                    packages[identifier]['version'] += 1
                    packages[identifier]['lifecycle'] = 'active'
                    status, value = 200, {'package': copy.deepcopy(packages[identifier])}
                elif path.endswith('/refresh'):
                    status, value = 202, {'refresh_run': {'id': identifier, 'status': 'accepted'}}
                else:
                    self.fail('unexpected endpoint')
            receipts[key] = (copy.deepcopy(body), status, copy.deepcopy(value))
            if path.endswith('/configuration') and not lost[0]:
                lost[0] = True
                raise ConnectionError('reply lost after commit')
            return status, value
        with tempfile.TemporaryDirectory() as d:
            journal = Path(d)/'journal.json'
            with self.assertRaises(ConnectionError):
                apply(request, journal, 'isolated-test', '2026-09-11T05:00:00Z', rules=rules)
            result = apply(request, journal, 'isolated-test', '2026-09-11T05:00:00Z', rules=rules)
            apply(request, journal, 'isolated-test', '2026-09-11T05:00:00Z', rules=rules)
            self.assertEqual(len(packages), 2)
            self.assertTrue(all(not r['population_verified'] for r in result))
            self.assertEqual(journal.stat().st_mode & 0o777, 0o600)
            self.assertEqual(len(receipts), 8)
            with self.assertRaises(ValueError):
                apply(request, journal, 'different-target', '2026-09-11T05:00:00Z', rules=rules)
        self.assertFalse(any('sender' in c[1] or 'binding' in c[1] for c in calls))

if __name__ == '__main__':
    unittest.main()
