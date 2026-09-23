#!/usr/bin/env node
import assert from 'node:assert/strict';
import path from 'node:path';
import { createRequire } from 'node:module';
import SwaggerParser from '@apidevtools/swagger-parser';
import { assertOpenAPIRouteParity } from './check-openapi-route-parity.mjs';

const require = createRequire(import.meta.url);

function assertGroupOpsPlanListItemExamples(specification) {
  // Ajv is Swagger Parser's existing transitive validator. Resolve it from the
  // Parser package so this assertion adds no second schema runtime.
  const parserPath = require.resolve('@apidevtools/swagger-parser');
  const Ajv = require(require.resolve('ajv', { paths: [path.dirname(parserPath)] }));
  const validate = new Ajv({ allErrors: true, strict: false }).compile(specification.components.schemas.GroupOpsPlanListItem);
  const item = {
    plan_id: '41', name: '列表计划', status: 'draft', revision: 7,
    created_by: 7, updated_by: 7,
    created_at: '2026-09-15T09:00:00Z', updated_at: '2026-09-15T09:00:00Z',
    owner: { staff_id: 7, sender_userid: 'fixture-owner', display_name: '列表负责人', name_source: 'wecom_profile', profile_read_state: 'ready' },
    queue_count: 0, bound_group_count: 0,
  };
  assert.equal(validate(item), true, `GroupOpsPlanListItem rejected valid zero counters: ${JSON.stringify(validate.errors)}`);
  assert.equal(validate({ ...item, accidental_field: true }), false, 'GroupOpsPlanListItem must reject an undeclared field');
  assert.equal(validate({ ...item, bound_group_count: -1 }), false, 'GroupOpsPlanListItem must reject a negative bound count');
}

function assertSurveyCompletionContracts(specification) {
  const schemas = specification.components.schemas;
  const completion = specification.paths['/api/admin/questionnaires/{questionnaire_id}/operations/completion'].put;
  const externalPush = specification.paths['/api/admin/questionnaires/{questionnaire_id}/operations/external-push'].put;
  const publicSubmit = specification.paths['/api/public/questionnaires/{slug}/submissions'].post;

  const completionRequest = completion.requestBody.content['application/json'].schema;
  const externalRequest = externalPush.requestBody.content['application/json'].schema;
  assert.equal(completionRequest.oneOf?.length, 4, 'completion PUT must declare retained and explicit action payload variants');
  assert.equal(externalRequest.additionalProperties, false, 'external-push request must reject undeclared top-level fields');
  assert.equal(externalRequest.properties.metadata.additionalProperties, true, 'opaque metadata must retain explicit additional-properties semantics');
  for (const field of ['webhook_url', 'type', 'expires_at_ts', 'day', 'frequency', 'remark', 'custom_params', 'configuration_version']) {
    assert.ok(field in externalRequest.properties, `external-push request omitted ${field}`);
  }

  const response = schemas.SurveyPublicSubmissionResponse;
  const publicSuccess = publicSubmit.responses['201'].content['application/json'].schema;
  assert.equal(publicSuccess, response, 'public submit 201 must use the compatible completion response schema');
  assert.deepEqual(response.required, ['receipt', 'result_token', 'completion_action'], 'ordinary public submit must retain the top-level result token alongside its completion action');
  assert.ok('result_token' in response.properties, 'ordinary public submit must retain its top-level result token');
  assert.ok(!('result_token' in schemas.SurveyPublicSubmissionReceipt.properties), 'public submit receipt must not nest a result token');

  const redirect = schemas.CompletionAction.oneOf.find((value) => value.properties?.type?.const === 'redirect');
  assert.equal(redirect?.properties?.redirect_url?.oneOf?.length, 2, 'completion redirect must use the safe relative-or-HTTPS URL contract');
  const parserPath = require.resolve('@apidevtools/swagger-parser');
  const Ajv = require(require.resolve('ajv', { paths: [path.dirname(parserPath)] }));
  const validateRedirect = new Ajv({ allErrors: true, strict: false }).compile(schemas.SurveySafeRedirectURL);
  for (const value of ['/h5/done.html?slug=growth', 'https://approved.example/complete']) {
    assert.equal(validateRedirect(value), true, `safe completion redirect rejected ${value}: ${JSON.stringify(validateRedirect.errors)}`);
  }
  for (const value of ['//evil.example/complete', 'http://approved.example/complete', 'https://user@approved.example/complete', 'https://approved.example/complete#fragment']) {
    assert.equal(validateRedirect(value), false, `unsafe completion redirect accepted ${value}`);
  }
}

function assertGovernanceUnknownContracts(specification) {
  // These are real API states: no fault start, an open episode, or no verified
  // denominator. OpenAPI 3.1 uses JSON Schema null unions, not 3.0 nullable.
  const fields = {
    OpsIncidentEpisode: ['recovered_at', 'caused_by_release_sequence', 'fault_started_at', 'effort_minutes', 'attributed_at'],
    OpsIncidentAttribution: ['caused_by_release_sequence', 'fault_started_at', 'effort_minutes'],
    OpsIncidentAttributionCommand: ['caused_by_release_sequence', 'fault_started_at', 'effort_minutes'],
    OpsGovernanceDurationMetric: ['mean_seconds'],
    OpsGovernanceRatioMetric: ['ratio'],
    OpsGovernanceDeploymentMetrics: ['evidence_at', 'historical_sequence_gaps', 'verified_success_cohort_failure_ratio'],
  };
  const parserPath = require.resolve('@apidevtools/swagger-parser');
  const Ajv = require(require.resolve('ajv', { paths: [path.dirname(parserPath)] }));
  const ajv = new Ajv({ allErrors: true, strict: false });
  for (const [name, keys] of Object.entries(fields)) {
    for (const key of keys) {
      const schema = specification.components.schemas[name].properties[key];
      assert.ok(Array.isArray(schema.type) && schema.type.includes('null'), name + '.' + key + ' must declare JSON Schema null');
      assert.equal(schema.nullable, undefined, '3.0 nullable must not silently substitute 3.1 null');
      const validate = ajv.compile(schema);
      assert.equal(validate(null), true, name + '.' + key + ' must preserve unknown');
      assert.equal(validate(schema.type.includes('string') ? '2026-09-18T13:00:00Z' : 3), true, name + '.' + key + ' must accept a known value');
      assert.equal(validate({unknown: true}), false, name + '.' + key + ' rejects arbitrary objects');
    }
  }
}

try {
  // Keep the repository's normal OpenAPI structural validation.  The
  // dereferenced copy below is only for compiling the local DTO examples.
  const specification = await SwaggerParser.validate('api/openapi.yaml');
  assertOpenAPIRouteParity(specification);
  const dereferenced = await SwaggerParser.dereference('api/openapi.yaml');
  assertGroupOpsPlanListItemExamples(dereferenced);
  assertSurveyCompletionContracts(specification);
  assertGovernanceUnknownContracts(specification);
  console.log(`validated OpenAPI ${specification.openapi}: ${Object.keys(specification.paths).length} paths`);
} catch (error) {
  console.error(`OpenAPI validation failed: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
}
