#!/usr/bin/env node
// Verifies the exact frozen Group Ops history dynamic module survives staging.
// It deliberately uses the real current web build, then reads the staged
// manifest through the same authenticated private-asset boundary used by v3.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';

const [sourceArg = 'web/dist', stageArg = 'release/web/dist'] = process.argv.slice(2);
const source = path.resolve(sourceArg);
const stage = path.resolve(stageArg);
const readManifest = (root) => JSON.parse(fs.readFileSync(path.join(root, 'asset-manifest.json'), 'utf8'));
const sourceManifest = readManifest(source);
const stagedManifest = readManifest(stage);

const adminEntry = sourceManifest.entries?.admin;
const dynamicOutputForInput = (manifest, from, input) => (manifest.files[from]?.imports || []).find((item) =>
  item.kind === 'dynamic-import' && manifest.files[item.path]?.inputs?.includes(input)
)?.path;
const legacyEntry = adminEntry && dynamicOutputForInput(sourceManifest, adminEntry, 'web/src/admin/legacy.ts');
const historyEntry = legacyEntry && dynamicOutputForInput(sourceManifest, legacyEntry, 'web/src/admin/sections/groupOpsHistory.ts');
assert.ok(adminEntry && legacyEntry && historyEntry, 'the real donor build must contain the admin, legacy, and Group Ops history modules');

const legacyImports = sourceManifest.files[legacyEntry]?.imports || [];
assert.ok(legacyImports.some((item) => item.kind === 'dynamic-import' && item.path === historyEntry), 'the real frozen legacy module must dynamically import Group Ops history');

const required = new Set();
const includeStatic = (relative) => {
  if (required.has(relative)) return;
  required.add(relative);
  for (const imported of sourceManifest.files[relative]?.imports || []) {
    if (imported.kind !== 'dynamic-import') includeStatic(imported.path);
  }
};
for (const entry of [adminEntry, legacyEntry, historyEntry]) includeStatic(entry);
for (const relative of required) {
  assert.ok(stagedManifest.files?.[relative], `staged manifest omits required Group Ops runtime asset: ${relative}`);
  assert.ok(fs.statSync(path.join(stage, relative)).isFile(), `staged Group Ops runtime asset is absent: ${relative}`);
}

const contentType = (relative) => relative.endsWith('.js') ? 'text/javascript; charset=utf-8' : relative.endsWith('.css') ? 'text/css; charset=utf-8' : 'application/octet-stream';
const server = http.createServer((request, response) => {
  if (!request.url?.startsWith('/groupops-assets/')) {
    response.writeHead(404).end();
    return;
  }
  if (!/(?:^|;)\s*aicrm_admin_session=valid(?:;|$)/.test(request.headers.cookie || '')) {
    response.writeHead(303, { Location: '/login?next=' + encodeURIComponent(request.url) }).end();
    return;
  }
  const relative = decodeURIComponent(request.url.slice('/groupops-assets/'.length));
  if (!stagedManifest.files?.[relative] || relative.includes('..') || relative.startsWith('/')) {
    response.writeHead(404).end();
    return;
  }
  response.writeHead(200, { 'Content-Type': contentType(relative), 'X-Content-Type-Options': 'nosniff' });
  response.end(fs.readFileSync(path.join(stage, relative)));
});

await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
try {
  const address = server.address();
  assert.ok(address && typeof address === 'object');
  const base = `http://127.0.0.1:${address.port}/groupops-assets/`;
  const anonymous = await fetch(base + historyEntry, { redirect: 'manual' });
  assert.equal(anonymous.status, 303, 'private history module must retain the v3 admin-session gate');
  for (const relative of [adminEntry, legacyEntry, historyEntry]) {
    const response = await fetch(base + relative, { headers: { Cookie: 'aicrm_admin_session=valid' } });
    assert.equal(response.status, 200, `authenticated loader failed for ${relative}`);
    assert.equal(response.headers.get('content-type'), contentType(relative), `dynamic module MIME drifted for ${relative}`);
    assert.ok((await response.text()).length > 0, `dynamic module body is empty for ${relative}`);
  }
} finally {
  await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
}

console.log('Group Ops frozen history release asset contract passed');
