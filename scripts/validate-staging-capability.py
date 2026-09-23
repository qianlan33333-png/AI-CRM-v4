#!/usr/bin/env python3
"""Check declared readback evidence; never mint an accepted staging receipt."""
import argparse
import json
from pathlib import Path


def string_list(value, name):
    if not isinstance(value, list) or any(not isinstance(x, str) or not x.strip() for x in value):
        raise ValueError(f'{name} must be a string array')
    return set(value)


def validate(capability, readback):
    mode = capability.get('acceptance_mode', 'virtual')
    if mode not in {'live', 'virtual'}:
        raise ValueError('acceptance_mode must be live or virtual')
    routes = string_list(capability.get('required_routes'), 'required_routes')
    providers = string_list(capability.get('required_provider_dependencies'), 'required_provider_dependencies')
    if not routes:
        raise ValueError('required_routes must not be empty')
    observations = readback.get('observations')
    if not isinstance(observations, list):
        raise ValueError('observations must be an array')
    seen_routes, seen_providers, unrelated = set(), set(), []
    for item in observations:
        if not isinstance(item, dict):
            raise ValueError('observation must be an object')
        route, provider, status = item.get('route'), item.get('provider'), item.get('status')
        if not isinstance(route, str) or type(status) is not int or not 100 <= status <= 599:
            raise ValueError('observation must contain a route and HTTP status')
        required = route in routes or provider in providers
        if required:
            if mode == 'virtual' and item.get('effect_mode') != 'virtual':
                raise ValueError(f'virtual acceptance requires virtual effect evidence: {route}')
            if not 200 <= status < 300 or item.get('business_verified') is not True:
                raise ValueError(f'required business readback failed: {route}')
            seen_routes.add(route)
            seen_providers.add(provider)
        elif mode == 'virtual' and status == 503 and isinstance(item.get('provider'), str) and item.get('provider') and isinstance(item.get('error'), str) and item['error'].endswith('_unavailable'):
            # Staging deliberately has no real external scheduler/provider.
            unrelated.append({'route': route, 'provider': provider, 'classification': 'external_config_unavailable'})
        elif not 200 <= status < 300:
            raise ValueError(f'unclassified failed observation: {route}')
    if routes - seen_routes or providers - seen_providers:
        raise ValueError('required route/provider readback is missing')
    return {'required_readback': 'passed', 'acceptance_mode': mode, 'unrelated_observations': unrelated}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('capability', type=Path)
    parser.add_argument('readback', type=Path)
    args = parser.parse_args()
    try:
        result = validate(json.loads(args.capability.read_text()), json.loads(args.readback.read_text()))
    except (ValueError, TypeError, AttributeError) as exc:
        raise SystemExit(str(exc))
    print(json.dumps(result, sort_keys=True))


if __name__ == '__main__':
    main()
