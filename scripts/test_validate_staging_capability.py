import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('capability', Path(__file__).with_name('validate-staging-capability.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class CapabilityTests(unittest.TestCase):
    def setUp(self):
        self.cap = {'required_routes': ['/checkout'], 'required_provider_dependencies': []}
        self.ok = {'route': '/checkout', 'status': 200, 'business_verified': True, 'effect_mode': 'virtual'}
        self.disabled = {'route': '/api/v1/distribution/products', 'provider': 'distribution', 'status': 503, 'error': 'distribution_unavailable'}

    def test_unrelated_disabled_distribution(self):
        result = module.validate(self.cap, {'observations': [self.ok, self.disabled]})
        self.assertEqual(result['unrelated_observations'][0]['classification'], 'external_config_unavailable')


    def test_virtual_payment_requires_virtual_evidence(self):
        cap = {**self.cap, 'acceptance_mode': 'virtual'}
        with self.assertRaises(ValueError):
            module.validate(cap, {'observations': [{k: v for k, v in self.ok.items() if k != 'effect_mode'}]})
        result = module.validate(cap, {'observations': [{**self.ok, 'effect_mode': 'virtual'}]})
        self.assertEqual(result['acceptance_mode'], 'virtual')

    def test_invalid_acceptance_mode_fails(self):
        with self.assertRaises(ValueError):
            module.validate({**self.cap, 'acceptance_mode': 'provider'}, {'observations': [self.ok]})


    def test_all_unconnected_external_schedulers_are_nonblocking_in_virtual_mode(self):
        result = module.validate(self.cap, {'observations': [self.ok, {'route': '/api/v1/audience/push', 'provider': 'outbound', 'status': 503, 'error': 'outbound_unavailable'}, {'route': '/api/v1/automation/run', 'provider': 'automation', 'status': 503, 'error': 'automation_unavailable'}]})
        self.assertEqual(len(result['unrelated_observations']), 2)

    def test_declared_route_cannot_be_ignored(self):
        self.cap['required_routes'].append(self.disabled['route'])
        with self.assertRaises(ValueError):
            module.validate(self.cap, {'observations': [self.ok, self.disabled]})

    def test_declared_provider_cannot_be_ignored(self):
        self.cap['required_provider_dependencies'] = ['distribution']
        with self.assertRaises(ValueError):
            module.validate(self.cap, {'observations': [self.ok, self.disabled]})

    def test_missing_and_empty_evidence(self):
        for observations in ([], [self.disabled]):
            with self.assertRaises(ValueError):
                module.validate(self.cap, {'observations': observations})

    def test_http_success_is_not_business_acceptance(self):
        with self.assertRaises(ValueError):
            module.validate(self.cap, {'observations': [{'route': '/checkout', 'status': 200}]})

    def test_payment_failure_cannot_use_distribution_exemption(self):
        for error in ('service_unavailable', 'distribution_unavailable'):
            with self.assertRaises(ValueError):
                module.validate(self.cap, {'observations': [{'route': '/checkout', 'status': 503, 'error': error}]})

    def test_malformed_and_unknown_failures(self):
        for extra in ({'route': '/other', 'status': 500}, {'route': '/other', 'status': 503, 'error': 'distribution_unavailable'}):
            with self.assertRaises(ValueError):
                module.validate(self.cap, {'observations': [self.ok, extra]})
        for value in (None, 'distribution', [{}]):
            with self.assertRaises(ValueError):
                module.validate({**self.cap, 'required_provider_dependencies': value}, {'observations': [self.ok]})


if __name__ == '__main__':
    unittest.main()
