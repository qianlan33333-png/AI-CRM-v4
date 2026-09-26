"""Comparison must retain backend skips separately from real browser passes."""
import unittest
from check_benchmark import validate_comparison


class BenchmarkTest(unittest.TestCase):
    def test_four_journeys_can_pass_without_mixing_backend_chromium_skips(self):
        full = {('backend','crm/cmd/aicrm',''):'pass',
                ('backend','crm/cmd/aicrm','TestRequiredChromiumJourney'):'skip',
                ('browser','crm/cmd/aicrm','TestRequiredChromiumJourney'):'pass',
                ('browser','crm/cmd/aicrm','TestUnrelatedChromiumJourney'):'pass'}
        affected = {key:value for key,value in full.items() if key[2]!='TestUnrelatedChromiumJourney'}
        validate_comparison(full,affected,['crm/cmd/aicrm'],['TestRequiredChromiumJourney'])
        affected[('browser','crm/cmd/aicrm','TestRequiredChromiumJourney')]='skip'
        with self.assertRaises(ValueError):
            validate_comparison(full,affected,['crm/cmd/aicrm'],['TestRequiredChromiumJourney'])

    def test_missing_package_and_new_result_cannot_certify_profile(self):
        full={('backend','crm/pkg','TestBehavior'):'pass'}
        for affected in ({}, {('backend','crm/pkg',''):'pass'}, {('backend','crm/pkg','TestBehavior'):'fail'}):
            with self.assertRaises(ValueError):
                validate_comparison(full,affected,['crm/pkg'],[])
