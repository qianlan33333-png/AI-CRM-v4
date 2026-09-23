import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import SwaggerParser from '@apidevtools/swagger-parser';

const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const explicitServeMuxRoute = /\bmux\.Handle(?:Func)?\(\s*"(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) (\/[^" ]*)"/g;
const serverSourceRoots = ['internal', 'cmd'];

const routeContracts = [
  ['post', '/oauth/token', 'none', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("POST /oauth/token", handler.token)'],
  ['get', '/mcp', 'machineBearer', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("GET /mcp", handler.mcpMetadata)'],
  ['post', '/mcp', 'machineBearer', 'optional-header', 'internal/openplatform/http/handler.go', 'mux.HandleFunc("POST /mcp", handler.mcp)'],
  ['get', '/open/v1/capabilities', 'machineBearer', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("GET /open/v1/capabilities", handler.v1Capabilities)'],
  ['post', '/open/v1/customers:resolve', 'machineBearer', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("POST /open/v1/customers:resolve", handler.v1ResolveCustomer)'],
  ['get', '/open/v1/customers/{customer_id}', 'machineBearer', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("GET /open/v1/customers/{customer_id}", handler.v1CustomerContext)'],
  ['get', '/open/v1/customers/{customer_id}/activities', 'machineBearer', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("GET /open/v1/customers/{customer_id}/activities", handler.v1CustomerActivities)'],
  ['post', '/open/v1/ai/review-plans', 'machineBearer', 'required-header', 'internal/openplatform/http/handler.go', 'mux.HandleFunc("POST /open/v1/ai/review-plans", handler.v1AIReviewPlan)'],
  ['get', '/open/v1/operations/{operation_id}', 'machineBearer', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("GET /open/v1/operations/{operation_id}", handler.v1OperationStatus)'],
  ['get', '/api/admin/open-platform/clients', 'adminSession', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("GET /api/admin/open-platform/clients", handler.listClients)'],
  ['post', '/api/admin/open-platform/clients', 'adminSession+csrfHeader', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("POST /api/admin/open-platform/clients", handler.createClient)'],
  ['get', '/api/admin/open-platform/clients/{client_id}', 'adminSession', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("GET /api/admin/open-platform/clients/{client_id}", handler.getClient)'],
  ['patch', '/api/admin/open-platform/clients/{client_id}', 'adminSession+csrfHeader', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("PATCH /api/admin/open-platform/clients/{client_id}", handler.patchClient)'],
  ['get', '/api/admin/open-platform/clients/{client_id}/audit', 'adminSession', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("GET /api/admin/open-platform/clients/{client_id}/audit", handler.listClientAudit)'],
  ['post', '/api/admin/open-platform/clients/{client_id}/activate', 'adminSession+csrfHeader', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("POST /api/admin/open-platform/clients/{client_id}/activate", handler.activateClient)'],
  ['post', '/api/admin/open-platform/clients/{client_id}/rotate', 'adminSession+csrfHeader', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("POST /api/admin/open-platform/clients/{client_id}/rotate", handler.rotateClient)'],
  ['post', '/api/admin/open-platform/clients/{client_id}/enable', 'adminSession+csrfHeader', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("POST /api/admin/open-platform/clients/{client_id}/enable", handler.enableClient)'],
  ['post', '/api/admin/open-platform/clients/{client_id}/disable', 'adminSession+csrfHeader', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("POST /api/admin/open-platform/clients/{client_id}/disable", handler.disableClient)'],
  ['get', '/api/admin/open-platform/routes', 'adminSession', null, 'internal/openplatform/http/handler.go', 'mux.HandleFunc("GET /api/admin/open-platform/routes", handler.routes)'],
  ['get', '/api/admin/customers/owner-handoffs/context', 'adminSession', null, 'internal/customer/http/handler.go', 'mux.HandleFunc("GET /api/admin/customers/owner-handoffs/context", handler.ownerHandoffContext)'],
  ['post', '/api/admin/customers/owner-handoffs/previews', 'adminSession+csrfHeader', null, 'internal/customer/http/handler.go', 'mux.HandleFunc("POST /api/admin/customers/owner-handoffs/previews", handler.ownerHandoffPreview)'],
  ['get', '/api/admin/customers/owner-handoffs/previews/{preview_id}', 'adminSession', null, 'internal/customer/http/handler.go', 'mux.HandleFunc("GET /api/admin/customers/owner-handoffs/previews/{preview_id}", handler.ownerHandoffPreviewRead)'],
  ['post', '/api/admin/customers/owner-handoffs/confirm', 'adminSession+csrfHeader', 'body-key', 'internal/customer/http/handler.go', 'mux.HandleFunc("POST /api/admin/customers/owner-handoffs/confirm", handler.ownerHandoffConfirm)'],
  ['get', '/api/admin/customers/owner-handoffs/batches/{batch_id}', 'adminSession', null, 'internal/customer/http/handler.go', 'mux.HandleFunc("GET /api/admin/customers/owner-handoffs/batches/{batch_id}", handler.ownerHandoffBatchRead)'],
  ['post', '/api/admin/customers/owner-handoffs/batches/{batch_id}/transfer-result', 'adminSession+csrfHeader', 'body-key', 'internal/customer/http/handler.go', 'mux.HandleFunc("POST /api/admin/customers/owner-handoffs/batches/{batch_id}/transfer-result", handler.ownerHandoffTransferResult)'],
  ['get', '/api/admin/message-archive/customers/{customer_id}', 'adminSession', null, 'internal/messagearchive/http/handler.go', 'mux.HandleFunc("GET /api/admin/message-archive/customers/{customer_id}", h.customerMessages)'],
  ['get', '/api/admin/message-archive/customers/{customer_id}/staff', 'adminSession', null, 'internal/messagearchive/http/handler.go', 'mux.HandleFunc("GET /api/admin/message-archive/customers/{customer_id}/staff", h.customerStaff)'],
  ['get', '/api/admin/message-archive/customers/{customer_id}/media/{media_id}', 'adminSession', null, 'internal/messagearchive/http/handler.go', 'mux.HandleFunc("GET /api/admin/message-archive/customers/{customer_id}/media/{media_id}", h.customerMedia)'],
  ['post', '/api/sidebar/v2/bootstrap', 'sidebarSession', null, 'internal/sidebar/handler.go', 'mux.HandleFunc("POST /api/sidebar/v2/bootstrap", h.bootstrap)'],
  ['get', '/api/sidebar/v2/materials/{image_id}/variants/{variant_key}', 'contextToken', null, 'internal/sidebar/handler.go', 'mux.HandleFunc("GET /api/sidebar/v2/materials/{image_id}/variants/{variant_key}", h.materialVariant)'],
  ['post', '/api/admin/wecom/group-membership/refresh', 'adminSession+csrfHeader', null, 'internal/wecom/admin_http.go', 'mux.HandleFunc("POST /api/admin/wecom/group-membership/refresh", handler.refreshGroupMembership)'],
  ['get', '/api/admin/media-preparations', 'adminSession', null, 'internal/media/http/handler.go', 'tail == "" && r.Method == http.MethodGet'],
  ['post', '/api/admin/media-preparations/refresh-rounds', 'adminSession+csrfHeader', 'optional-header', 'internal/media/http/handler.go', 'tail == "refresh-rounds" && r.Method == http.MethodPost'],
  ['get', '/api/admin/media-preparations/refresh-rounds/{refresh_round_id}', 'adminSession', null, 'internal/media/http/handler.go', 'strings.HasPrefix(tail, "refresh-rounds/") && r.Method == http.MethodGet'],
  ['post', '/api/admin/media-preparations/{source_ref}/prepare', 'adminSession+csrfHeader', 'optional-header', 'internal/media/http/handler.go', 'strings.HasSuffix(tail, "/prepare") && r.Method == http.MethodPost'],
  ['post', '/api/admin/common/operation-members/sync', 'adminSession+csrfHeader', 'required-header', 'internal/groupops/http/handler.go', 'r.URL.Path == OperationMembersPath+"/sync"'],
  ['get', '/api/admin/payments/history', 'adminSession', null, 'internal/payment/http/handler.go', 'case path == "/api/admin/payments/history":'],
  ['get', '/api/admin/refunds/recovery', 'adminSession', 'required-header', 'internal/payment/http/handler.go', 'case path == "/api/admin/refunds/recovery":'],
  ['get', '/api/admin/operation-batches/strategy-summaries', 'adminSession', null, 'internal/aiassistant/excel/bridge.go', 'case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "strategy-summaries":'],
];

const retiredRouteContracts = [
  [
    'post',
    '/api/admin/operation-batches/imports',
    'internal/aiassistant/excel/bridge.go',
    'case r.Method == "POST" && len(parts) == 1 && parts[0] == "imports":',
    'POST /api/admin/operation-batches/strategies/{strategy_key}/imports',
    '410 legacy_write_disabled',
  ],
];

// These handlers dispatch below a prefix registration, so the literal ServeMux
// extractor cannot infer their method/path pairs. Their active routes stay in
// routeContracts, while this evidence makes the exception and its retired-path
// policy explicit for review.
const customDispatcherEvidence = [
  ['cmd/aicrm/composition.go', 'case "owner_migration":', 'shared operation-members scope dispatch'],
  ['cmd/aicrm/composition.go', 'case "channel_code":', 'shared operation-members scope dispatch'],
  ['cmd/aicrm/composition.go', 'mux.Handle("/api/admin/common/operation-members/", groupOpsHandler)', 'GroupOps prefix dispatch'],
  ['internal/groupops/http/handler.go', 'r.URL.Path == OperationMembersPath+"/sync"', 'GroupOps operation-member sync'],
  ['internal/media/http/handler.go', 'tail == "refresh-rounds" && r.Method == http.MethodPost', 'Media preparation refresh round'],
  ['internal/payment/http/handler.go', 'case path == "/api/admin/payments/history":', 'Payment history read projection'],
  ['internal/payment/http/handler.go', 'case path == "/api/admin/refunds/recovery":', 'Payment refund receipt recovery read'],
  ['cmd/aicrm/composition.go', 'adminAPIs.Handle("/api/admin/operation-batches", excelBridge)', 'Excel batch prefix dispatch'],
  ['internal/openplatform/http/handler.go', 'mux.Handle(retired.Method+" "+retired.Path, http.NotFoundHandler())', 'retired Open Platform inventory routes are explicit 404 handlers and are intentionally excluded from OpenAPI'],
];

function fail(message) {
  throw new Error(`OpenAPI route parity failed: ${message}`);
}

function securityRequirementGroups(operation) {
  return (operation.security ?? [])
    .map(requirement => Object.keys(requirement).sort())
    .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
}

function expectedSecurityGroups(kind) {
  return kind === 'none' ? [] : [kind.split('+').sort()];
}

function parametersFor(specification, route, operation) {
  return [...(specification.paths[route].parameters ?? []), ...(operation.parameters ?? [])];
}

function hasIdempotencyHeader(parameters, required) {
  return parameters.some(parameter => parameter.name === 'Idempotency-Key' && parameter.in === 'header' && parameter.required === required);
}

function hasBodyIdempotencyKey(operation) {
  const schema = operation.requestBody?.content?.['application/json']?.schema;
  return Array.isArray(schema?.required) && schema.required.includes('idempotency_key');
}

function sourceText(relativePath, cache) {
  if (!cache.has(relativePath)) {
    cache.set(relativePath, fs.readFileSync(path.join(repository, relativePath), 'utf8'));
  }
  return cache.get(relativePath);
}

export function extractExplicitServeMuxRoutes(root = repository) {
  const routes = new Map();
  function walk(relative) {
    const directory = path.join(root, relative);
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const child = path.join(relative, entry.name);
      if (entry.isDirectory()) {
        walk(child);
        continue;
      }
      if (!entry.isFile() || !child.endsWith('.go') || child.endsWith('_test.go')) continue;
      const source = fs.readFileSync(path.join(root, child), 'utf8');
      for (const match of source.matchAll(explicitServeMuxRoute)) {
        const method = match[1].toLowerCase();
        const route = match[2];
        routes.set(`${method} ${route}`, { method, route, source: child });
      }
    }
  }
  for (const sourceRoot of serverSourceRoots) walk(sourceRoot);
  return [...routes.values()].sort((left, right) => `${left.method} ${left.route}`.localeCompare(`${right.method} ${right.route}`));
}

export function assertExplicitRoutesDocumented(specification, registrations = extractExplicitServeMuxRoutes()) {
  for (const { method, route, source } of registrations) {
    if (!specification.paths?.[route]?.[method]) {
      fail(`explicit ServeMux route ${method.toUpperCase()} ${route} from ${source} is missing from OpenAPI`);
    }
  }
}

export function assertSecurityRequirementGroups(operation, expectedGroups, label) {
  const actualGroups = securityRequirementGroups(operation);
  if (JSON.stringify(actualGroups) !== JSON.stringify(expectedGroups)) {
    fail(`${label} security must be ${JSON.stringify(expectedGroups)}`);
  }
}

export function assertRetiredRoutesAbsent(specification, retiredRoutes = retiredRouteContracts) {
  const cache = new Map();
  for (const [method, route, source, registration, replacement, behavior] of retiredRoutes) {
    if (specification.paths?.[route]?.[method]) {
      fail(`retired ${method.toUpperCase()} ${route} must remain absent from OpenAPI; runtime returns ${behavior} and callers must use ${replacement}`);
    }
    if (!sourceText(source, cache).includes(registration)) {
      fail(`retired ${method.toUpperCase()} ${route} no longer has observable ${behavior} evidence in ${source}`);
    }
  }
}

export function assertOpenAPIRouteParity(specification) {
  const cache = new Map();
  assertExplicitRoutesDocumented(specification);
  for (const [method, route, security, idempotency, source, registration] of routeContracts) {
    const operation = specification.paths?.[route]?.[method];
    const label = `${method.toUpperCase()} ${route}`;
    if (!operation) fail(`missing active ${label}`);
    assertSecurityRequirementGroups(operation, expectedSecurityGroups(security), label);
    const parameters = parametersFor(specification, route, operation);
    if (idempotency === 'required-header' && !hasIdempotencyHeader(parameters, true)) {
      fail(`${label} must require Idempotency-Key`);
    }
    if (idempotency === 'optional-header' && !hasIdempotencyHeader(parameters, false)) {
      fail(`${label} must document optional Idempotency-Key compatibility`);
    }
    if (idempotency === 'body-key' && !hasBodyIdempotencyKey(operation)) {
      fail(`${label} must require the application-body idempotency_key`);
    }
    if (!sourceText(source, cache).includes(registration)) {
      fail(`${label} is no longer registered by ${source}`);
    }
  }
  assertRetiredRoutesAbsent(specification);
  for (const [source, requirement, reason] of customDispatcherEvidence) {
    if (!sourceText(source, cache).includes(requirement)) {
      fail(`custom dispatcher evidence is missing for ${reason}: ${requirement}`);
    }
  }
  console.log(`OpenAPI route parity: ${routeContracts.length} active operations and ${extractExplicitServeMuxRoutes().length} explicit ServeMux registrations verified`);
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  try {
    const specification = await SwaggerParser.validate(path.join(repository, 'api/openapi.yaml'));
    assertOpenAPIRouteParity(specification);
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
