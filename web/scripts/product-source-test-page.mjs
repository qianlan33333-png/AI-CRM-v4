import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// Adapter contracts use the tracked V4 template binding, never a previous
// web/dist build. Final packaged HTML is checked by the installed journeys.
export function productSourceTestPage(name) {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
  const index = JSON.parse(fs.readFileSync(path.join(root, 'web/donor-sources/source-index.json'), 'utf8'));
  const binding = index.bindings.find(item => item.logical_path === `web/src/admin/templates/${name}`);
  const content = index.contents.find(item => item.id === binding?.content_id);
  if (!content) throw new Error(`Product source template binding missing: ${name}`);
  const template = fs.readFileSync(path.join(root, content.canonical_path), 'utf8')
    .replace(/<sc-for\s+([^>]*?)list="([^"]*)"([^>]*?)as="([^"]*)"([^>]*)>/g, (_match, _a, list, _b, as) => `<template data-sc-for="${list}" data-as="${as}">`)
    .replace(/<\/sc-for>/g, '</template>')
    .replace(/<sc-if\s+([^>]*?)value="([^"]*)"([^>]*)>/g, (_match, _a, value) => `<template data-sc-if="${value}">`)
    .replace(/<\/sc-if>/g, '</template>');
  return `<!doctype html><html><body data-page="${path.basename(name, '.html')}"><main id="stage"></main><template id="tpl">${template}</template></body></html>`;
}
