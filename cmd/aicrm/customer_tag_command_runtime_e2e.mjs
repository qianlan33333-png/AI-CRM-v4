import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const origin = process.argv[2];
if (!origin) throw new Error('missing runtime origin');
const repository = path.dirname(path.dirname(path.dirname(fileURLToPath(import.meta.url))));
const source = fs.readFileSync(path.join(repository, 'internal/webshell/templates/admin_customers.html'), 'utf8');
const script = fs.readFileSync(path.join(repository, 'internal/webshell/static/admin_console/admin_customers.js'), 'utf8');
// Execute the production V3 picker in this composition journey. The host must
// retain its existing preview/command transport after a real picker commit;
// assigning the hidden select directly would not exercise that integration.
const tagPickerBundle = (await build({
  stdin: {
    contents: "import { installTagPickerAdapter } from './web/v3/shared/ui/tagPickerAdapter'; installTagPickerAdapter();",
    resolveDir: repository,
    sourcefile: 'customer-tag-command-runtime-picker.ts',
  },
  bundle: true,
  format: 'iife',
  platform: 'browser',
  target: 'es2020',
  write: false,
})).outputFiles[0].text.replace(/<\/script/gi, '<\\/script');
const template = source
  .replace('{{define "admin_customers"}}', '')
  .replace(/{{if eq \.RequestPath "\/admin\/customers"}}([\s\S]*?){{else}}([\s\S]*?){{end}}\s*<\/div>\s*{{end}}\s*$/, '$1\n</div>');
const calls = [];
const standardReadyForCalls = [];
const pickerOpenCalls = [];
const dom = new JSDOM(`<!doctype html><html><body>${template}<script>${tagPickerBundle}</script><script>${script}</script></body></html>`, {
  url: `${origin}/admin/customers`, runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = Headers;
    window.AdminDateTime = {};
    window.AdminFmt = { localTime: (value) => value === '2026-09-06T00:00:00Z' ? '2026-09-06 08:00:00' : '时间暂不可用', whenAdminDateTimeReady: (ready) => ready(window.AdminDateTime) };
    window.confirm = () => true;
    // This composition fixture executes the customer script without the release
    // asset renderer. Supply only the standard-component Port that script needs;
    // the real tag catalog, preview, command, and receipt routes remain HTTP-backed.
    window.AICRMStandardComponents = {
      readyFor: async (capabilities) => {
        if (!Array.isArray(capabilities) || capabilities.length !== 1 || capabilities[0] !== 'tags') throw new Error(`unexpected standard component request: ${JSON.stringify(capabilities)}`);
        standardReadyForCalls.push([...capabilities]);
      },
    };
    window.AICRMWeComTagPicker = { open: (options) => { pickerOpenCalls.push(options); } };
    window.fetch = async (input, options = {}) => {
      const url = new URL(String(input), window.location.origin);
      if (url.pathname === '/api/admin/customers') {
        return new Response(JSON.stringify({ items: [{ customer_id: 1, display_name: '运行时客户', oneid: 'CID-1', phone_masked: '138****0000', updated_at: '2026-09-06T00:00:00Z' }], total: 1, total_is_estimate: false }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
      if (url.pathname.startsWith('/api/v1/customer-tag-commands') && String(options.method || 'GET').toUpperCase() !== 'GET') calls.push(url.pathname);
      // JSDOM's AbortSignal is browser-correct for the V3 loader but cannot
      // be handed to Node's HTTP client. The bridge keeps the real HTTP
      // request and all command headers/body intact; cancellation remains
      // owned by the picker session inside this DOM fixture.
      const { signal: _domSignal, ...requestOptions } = options;
      return globalThis.fetch(url, requestOptions);
    };
  },
});
try {
  dom.window.document.cookie = 'aicrm_admin_csrf=runtime-csrf; path=/';
  // The real catalog request includes a PostgreSQL read. Wait for its visible
  // result instead of assuming it finishes within a fixed 30 ms startup gap.
  const deadline = Date.now() + 3000;
  while (Date.now() < deadline) {
    const ready = dom.window.document.querySelector('#customer-tag-batch [name="add_tag_ids"] option')?.textContent === '运行时分组 / 运行时标签';
    if (ready && dom.window.document.querySelector('input[aria-label="选择用户 1"]')) break;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  const checkbox = dom.window.document.querySelector('input[aria-label="选择用户 1"]');
  if (!checkbox) throw new Error('actual Host list did not render selection');
  checkbox.checked = true;
  checkbox.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  const form = dom.window.document.querySelector('#customer-tag-batch');
  if (!form || form.querySelector('[name="add_tag_ids"] option')?.textContent !== '运行时分组 / 运行时标签') throw new Error('actual catalog route did not render the local tag name');
  if (standardReadyForCalls.length !== 1 || standardReadyForCalls[0].join(',') !== 'tags') throw new Error(`customer tag selectors did not request the tags capability: ${JSON.stringify(standardReadyForCalls)}`);
  if (pickerOpenCalls.length !== 0) throw new Error('runtime command fixture unexpectedly opened the picker UI');
  const tagOptions = [...form.querySelector('[name="add_tag_ids"]').options];
  if (tagOptions.length !== 1 || tagOptions[0].textContent !== '运行时分组 / 运行时标签') throw new Error('actual catalog route did not render the local tag name');
  const addDraft = form.querySelector('[name="add_tag_ids"]');
  const openPicker = addDraft?.parentElement?.querySelector('button');
  if (!openPicker) throw new Error('actual V3 tag picker was not attached to the runtime add-tag draft');
  openPicker.click();
  let picker;
  for (let remaining = 100; remaining > 0; remaining--) {
    picker = dom.window.document.querySelector('[data-v3-selection-session="tag"]');
    if (picker?.querySelector('[data-v3-tag-key]')) break;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  const row = picker?.querySelector('[data-v3-tag-key]');
  if (!row) throw new Error('actual V3 tag picker did not render the runtime catalog tag');
  row.click();
  picker.querySelector('[data-v3-tag-confirm]')?.click();
  for (let remaining = 100; remaining > 0 && dom.window.document.querySelector('[data-v3-selection-session="tag"]'); remaining--)
    await new Promise((resolve) => setTimeout(resolve, 10));
  if (dom.window.document.querySelector('[data-v3-selection-session="tag"]') || [...addDraft.selectedOptions].map((option) => option.value).join(',') !== '1')
    throw new Error('actual V3 tag picker did not commit the runtime tag into the existing add-tag draft');
  form.dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  let result = '';
  for (let remaining = 40; remaining > 0; remaining--) {
    await new Promise((resolve) => setTimeout(resolve, 10));
    result = dom.window.document.querySelector('#customer-tag-batch-result')?.textContent || '';
    if (calls.length === 2 && result.includes('已刷新执行结果：用户 #1：排队中；观察标签：已观察标签（已生效）')) break;
  }
  if (calls.join(',') !== '/api/v1/customer-tag-commands/preview,/api/v1/customer-tag-commands' ||
      !result.includes('已刷新执行结果：用户 #1：排队中；观察标签：已观察标签（已生效）') ||
      result.includes('：queued') || result.includes('（active）')) {
    throw new Error(`Host tag interaction calls=${calls.join(',')} result=${result}`);
  }
  console.log('customer-tag-command-runtime-host: PASS');
} finally { dom.window.close(); }
