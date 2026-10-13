import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const require = createRequire(import.meta.url);
const yaml = require('js-yaml');
export function loadModuleRegistry(root = '.') {
  return yaml.load(readFileSync(resolve(root, 'modules/registry.yaml'), 'utf8'));
}
function files(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap(item => {
    const path = resolve(directory, item.name);
    return item.isDirectory() ? files(path) : [path];
  });
}
export function assertModuleRegistry(registry, root = '.') {
  const names = new Set(), sources = new Set();
  const tables = JSON.parse(readFileSync(resolve(root, 'docs/governance/retention-registry.json'), 'utf8')).tables;
  const actual = readdirSync(resolve(root, 'internal'), { withFileTypes: true })
    .filter(item => item.isDirectory() && files(resolve(root, 'internal', item.name)).some(path => path.endsWith('.go') && !path.endsWith('_test.go')))
    .map(item => `internal/${item.name}`);
  for (const row of registry.modules) {
    assert.ok(row.name && !names.has(row.name), `duplicate/empty module ${row.name}`);
    names.add(row.name);
    assert.ok(row.owner && typeof row.owner === 'string', `missing owner ${row.name}`);
    assert.ok(registry.states.includes(row.status), `invalid status ${row.name}`);
    assert.ok(['module', 'aggregate'].includes(row.kind), `unknown registry kind ${row.name}`);
    if (row.kind === 'aggregate') {
      assert.equal(row.source, undefined, 'aggregate cannot stand in for a source module');
      continue;
    }
    assert.match(row.data_owner, /^[a-z]+$/, `invalid data owner ${row.name}`);
    assert.equal(row.source, `internal/${row.data_owner}`, `unsafe or mismatched source ${row.name}`);
    assert.ok(actual.includes(row.source) && !sources.has(row.source), `duplicate/unknown source ${row.source}`);
    sources.add(row.source);
    assert.ok(Array.isArray(row.entrypoints) && row.entrypoints.length, `missing entrypoints ${row.name}`);
    for (const path of [...row.entrypoints, ...row.validation_contracts]) {
      assert.ok(path.startsWith(row.source + '/') && !path.split('/').includes('..'), `out-of-owner path ${path}`);
      assert.ok(statSync(resolve(root, path)).isFile(), `missing source contract ${path}`);
    }
    const owned = Object.entries(tables).filter(([, table]) => table.owner === row.data_owner).map(([name]) => name).sort();
    assert.deepEqual(row.owned_tables, owned, `data ownership drift ${row.name}`);
    assert.equal(row.data_registry, 'docs/governance/retention-registry.json#tables');
    assert.ok(Array.isArray(row.dependencies) && new Set(row.dependencies).size === row.dependencies.length, `invalid dependencies ${row.name}`);
    for (const dependency of row.dependencies) assert.ok(actual.includes(`internal/${dependency}`), `unknown dependency ${dependency}`);
  }
  assert.deepEqual([...sources].sort(), actual.sort(), 'actual module coverage incomplete');
  return sources.size;
}
if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  console.log(`module-registry: ${assertModuleRegistry(loadModuleRegistry())} actual modules; ownership/entry/data/contracts verified`);
}
