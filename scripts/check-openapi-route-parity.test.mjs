import assert from 'node:assert/strict';
import SwaggerParser from '@apidevtools/swagger-parser';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { assertExplicitRoutesDocumented, assertOpenAPIRouteParity, assertRetiredRoutesAbsent, assertSecurityRequirementGroups } from './check-openapi-route-parity.mjs';

const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

test('rejects a newly registered explicit route absent from OpenAPI', () => {
  assert.throws(
    () => assertExplicitRoutesDocumented({ paths: {} }, [{ method: 'post', route: '/api/admin/new-route', source: 'internal/example/http.go' }]),
    /explicit ServeMux route POST \/api\/admin\/new-route.*missing from OpenAPI/,
  );
});

test('rejects OR security where admin session and CSRF must be one requirement', () => {
  assert.throws(
    () => assertSecurityRequirementGroups({ security: [{ adminSession: [] }, { csrfHeader: [] }] }, [['adminSession', 'csrfHeader']], 'POST /api/admin/example'),
    /security must be/,
  );
});

test('rejects a retired Excel import operation restored to OpenAPI', () => {
  assert.throws(
    () => assertRetiredRoutesAbsent({ paths: { '/api/admin/operation-batches/imports': { post: {} } } }),
    /retired POST \/api\/admin\/operation-batches\/imports must remain absent from OpenAPI/,
  );
});

test('keeps the Alipay callback public at the session layer and documents its form boundary', async () => {
  const specification = await SwaggerParser.validate(path.join(repository, 'api/openapi.yaml'));
  const operation = specification.paths['/api/public/alipay/callback'].post;
  assert.deepEqual(operation.security, []);
  assert.match(operation.description, /matching Origin is authoritative even if Sec-Fetch-Site reports cross-site/);
  assert.match(operation.description, /Origin is absent.*marked cross-site by Sec-Fetch-Site/);
  assert.match(operation.requestBody.description, /64 KiB/);
  assert.match(operation.responses['400'].description, /Malformed form.*over 64 KiB/);
  assert.match(operation.responses['403'].description, /mismatched nonempty Origin.*cross-site Sec-Fetch-Site value when Origin is absent/);
  const schema = operation.requestBody.content['application/x-www-form-urlencoded'].schema;
  assert.equal(schema.type, 'object');
  assert.equal(schema.required, undefined);
  assert.equal(schema.additionalProperties.type, 'string');
  assert.equal(schema.properties.app_id.type, 'string');
  assert.equal(schema.properties.out_trade_no.type, 'string');
  assert.equal(schema.properties.out_request_no.type, 'string');
  assert.equal(schema.properties.refund_amount.type, 'string');
  assert.match(schema.properties.refund_status.description, /does not use it to select or confirm/);
  assert.match(schema.properties.refund_fee.description, /does not use it as the normalized refund amount/);
  assert.match(schema.properties.refund_reason.description, /does not normalize it/);
  assert.match(schema.properties.gmt_refund.description, /does not select or confirm a refund result/);

  const missing = structuredClone(specification);
  delete missing.paths['/api/public/alipay/callback'].post;
  assert.throws(() => assertOpenAPIRouteParity(missing), /missing active POST \/api\/public\/alipay\/callback/);
});
