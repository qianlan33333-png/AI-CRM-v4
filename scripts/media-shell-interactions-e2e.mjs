/**
 * PR02 DOM regression for the three frozen Media workspaces.
 *
 * The Go webshell extracts the release page's outer #tpl and mounts it inside
 * the v3 sidebar. These assertions execute the same admin bundle against the
 * generated donor pages, then click each real toolbar action. The matching Go
 * test covers the extraction boundary itself; this test guards the resulting
 * browser behaviour without modifying donor business files.
 */
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from '../web/scripts/test-browser-bundle.mjs';

const REPOSITORY = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const WEB = path.join(REPOSITORY, 'web');
const DIST = path.join(WEB, 'dist');
const ADMIN = await buildTestBrowserBundle(path.join(WEB, 'src/admin/main.ts'));
const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

function fail(message) {
  throw new Error(`media shell DOM regression: ${message}`);
}

function clickButton(document, label) {
  const button = Array.from(document.querySelectorAll('button')).find((candidate) => candidate.textContent?.trim() === label);
  if (!button) fail(`missing toolbar button ${label}`);
  button.click();
}

async function load(page) {
  const file = path.join(DIST, 'admin', `${page}.html`);
  let html = fs.readFileSync(file, 'utf8');
  html = html.replace(
    /<script type="module" src="[^\"]*assets\/admin-[^\"]+\.js"><\/script>/,
    () => `<script>${ADMIN}</script>`,
  );
  const dom = new JSDOM(html, {
    url: `http://localhost/admin/${page}.html`,
    runScripts: 'dangerously',
    pretendToBeVisual: true,
    beforeParse(window) {
      // Mock is deliberately explicit and only supplies the controller's
      // read data. The assertion is DOM lifecycle + real event binding.
      window.__AICRM_TEST_MOCK__ = true;
      window.confirm = () => true;
    },
  });
  // MockApi intentionally delays its initial aggregate read by 200ms.
  await sleep(500);
  return dom;
}

for (const scenario of [
  { page: 'images', button: '上传图片', control: '#fImgUpFile', title: '上传图片' },
  { page: 'mpLib', button: '新建小程序卡片', control: '#fMpAppid', title: '新建小程序卡片' },
  { page: 'attach', button: '上传附件', control: '#fAttUpFile', title: '上传附件' },
]) {
  const dom = await load(scenario.page);
  try {
    const { document } = dom.window;
    if (!document.querySelector('#stage')?.textContent?.trim()) fail(`${scenario.page} did not mount the donor workspace`);
    clickButton(document, scenario.button);
    await sleep(0);
    if (!document.querySelector(scenario.control)) fail(`${scenario.page} click did not reveal ${scenario.control}`);
    if (!document.body.textContent?.includes(scenario.title)) fail(`${scenario.page} modal title is missing after click`);
    console.log(`  ✓ ${scenario.page} toolbar opens frozen donor modal`);
  } finally {
    dom.window.close();
  }
}

console.log('media shell DOM interactions: ok');
