import assert from 'node:assert/strict';

const methods = new Set(['get', 'put', 'post', 'delete', 'patch', 'head', 'options', 'trace']);
export function assertOperationIds(specification) {
  const seen = new Map();
  let count = 0;
  for (const [path, item] of Object.entries(specification.paths)) {
    for (const [method, operation] of Object.entries(item)) {
      if (!methods.has(method)) continue;
      const name = operation.operationId;
      assert.equal(typeof name, 'string', `${method.toUpperCase()} ${path} has no operationId`);
      assert.match(name, /^[A-Za-z][A-Za-z0-9_]*$/, `${method.toUpperCase()} ${path} has an invalid operationId`);
      assert.ok(!seen.has(name), `${name} reused by ${seen.get(name)} and ${method.toUpperCase()} ${path}`);
      seen.set(name, `${method.toUpperCase()} ${path}`);
      count++;
    }
  }
  assert.ok(count > 0, 'operation inventory empty');
  return count;
}
