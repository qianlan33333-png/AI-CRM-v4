import assert from 'node:assert/strict';
import test from 'node:test';
import SwaggerParser from '@apidevtools/swagger-parser';
import { assertOperationIds } from './check-operation-ids.mjs';
import { assertModuleRegistry, loadModuleRegistry } from './check-module-registry.mjs';

test('all actual module owners, entries, table ownership and source contracts are discoverable', () => {
  assert.equal(assertModuleRegistry(loadModuleRegistry()), 38);
});
for (const [name, mutate] of [
  ['omitted module', registry => registry.modules.splice(registry.modules.findIndex(row => row.data_owner === 'invoice'), 1)],
  ['duplicate source', registry => registry.modules.push(structuredClone(registry.modules.find(row => row.data_owner === 'invoice')))],
  ['owner omitted', registry => delete registry.modules.find(row => row.data_owner === 'invoice').owner],
  ['foreign table owner', registry => registry.modules.find(row => row.data_owner === 'invoice').owned_tables.push('customers')],
  ['out-of-owner entry', registry => registry.modules.find(row => row.data_owner === 'invoice').entrypoints.push('internal/invoice/../access/http/handler.go')],
  ['aggregate disguised as actual module', registry => registry.modules.find(row => row.data_owner === 'invoice').kind = 'aggregate'],
]) {
  test(`reject ${name}`, () => {
    const registry = loadModuleRegistry(); mutate(registry);
    assert.throws(() => assertModuleRegistry(registry));
  });
}
test('current OpenAPI operations have globally unique stable IDs and customer permissions stay scoped', async () => {
  const spec = await SwaggerParser.bundle('api/openapi.yaml');
  assert.equal(assertOperationIds(spec), 616);
  assert.equal(spec.paths['/my/orders'].get.operationId, 'getCustomerOrderCenterPage');
  for (const path of ['/api/h5/me/orders', '/api/h5/me/invoices', '/api/h5/me/invoices/{id}/file']) {
    assert.deepEqual(spec.paths[path].get.security, [{ paymentSession: [] }], `${path} customer authorization drift`);
  }
  assert.deepEqual(spec.paths['/api/callbacks/wechat-invoice'].post.security, [{ wechatPaySignature: [] }]);
  for (const mutation of [spec => delete spec.paths['/my/orders'].get.operationId,
    spec => spec.paths['/my/orders'].get.operationId = spec.paths['/api/h5/me/orders'].get.operationId,
    spec => spec.paths['/my/orders'].get.operationId = '  ']) {
    const copy = structuredClone(spec); mutation(copy);
    assert.throws(() => assertOperationIds(copy));
  }
});
