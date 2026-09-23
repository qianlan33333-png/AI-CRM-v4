/**
 * 绑定冒烟测试：
 * 在 Node 中实例化 AdminController / H5Controller（stub 浏览器全局），
 * 对每屏模板校验：
 *  1. 所有 {{ path }} 的绑定根存在于 renderVals scope
 *  2. 所有 sc-for 列表解析为数组
 *  3. 所有 onClick 绑定解析为函数
 */
import { build } from 'esbuild';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const ROOT = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const SRC = path.join(ROOT, 'src');
const TMP = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-smoke-'));

// 浏览器全局 stub（在动态 import 前装好）
const store = new Map();
globalThis.sessionStorage = {
  getItem: (k) => (store.has(k) ? store.get(k) : null),
  setItem: (k, v) => store.set(k, String(v)),
  removeItem: (k) => store.delete(k),
};
globalThis.location = { search: '', href: '' };
globalThis.__AICRM_TEST_MOCK__ = true;
// navigator 在 Node 中只读，用 defineProperty 覆盖
Object.defineProperty(globalThis, 'navigator', { value: {}, configurable: true });

await build({
  entryPoints: {
    admin: path.join(SRC, 'admin/controller.ts'),
    api: path.join(SRC, 'shared/api/client.ts'),
    h5: path.join(SRC, 'h5/controller.ts'),
  },
  bundle: true,
  format: 'esm',
  platform: 'node',
  outdir: TMP,
  logLevel: 'warning',
});

const adminMod = await import(pathToFileURL(path.join(TMP, 'admin.js')).href);
const apiMod = await import(pathToFileURL(path.join(TMP, 'api.js')).href);
const h5Mod = await import(pathToFileURL(path.join(TMP, 'h5.js')).href);

function extractRefs(html) {
  const roots = new Set();
  const fors = [];
  const handlers = [];
  const aliases = new Set(['true', 'false', 'null']);
  const aliasList = new Map(); // 别名 → 列表路径
  for (const m of html.matchAll(/list="\{\{\s*([\w.]+?)\s*\}\}"[^>]*?as="(\w+)"/g)) {
    fors.push(m[1]);
    aliases.add(m[2]);
    aliasList.set(m[2], m[1]);
  }
  // as 在 list 之前的写法兜底
  for (const m of html.matchAll(/as="(\w+)"[^>]*?list="\{\{\s*([\w.]+?)\s*\}\}"/g)) {
    if (!aliasList.has(m[1])) {
      fors.push(m[2]);
      aliases.add(m[1]);
      aliasList.set(m[1], m[2]);
    }
  }
  for (const m of html.matchAll(/\{\{\s*([a-zA-Z_$][\w$]*)((?:\.[\w$]+)*)\s*\}\}/g)) roots.add(m[1]);
  for (const m of html.matchAll(/onClick="\{\{\s*([\w.]+?)\s*\}\}"/g)) handlers.push(m[1]);
  return { roots, fors, handlers, aliases, aliasList };
}

function resolvePath(p, scope) {
  let v = scope;
  for (const part of p.split('.')) {
    if (v === null || v === undefined) return undefined;
    v = v[part];
  }
  return v;
}

let failures = 0;

async function checkPage(name, html, vals) {
  const { roots, fors, handlers, aliases, aliasList } = extractRefs(html);
  const bad = [];
  for (const r of roots) if (!(r in vals) && !aliases.has(r)) bad.push('缺绑定根: ' + r);
  for (const f of fors) {
    const v = resolvePath(f, vals);
    // null/undefined 视为被 sc-if 守卫（如 rcCur.tasks 仅在抽屉打开时渲染）
    if (v !== null && v !== undefined && !Array.isArray(v)) bad.push('sc-for 非数组: ' + f);
  }
  for (const h of handlers) {
    const [head, ...rest] = h.split('.');
    let target = vals;
    if (aliases.has(head) && aliasList.has(head)) {
      // 行内处理器：对列表首行求解（空列表跳过）
      const list = resolvePath(aliasList.get(head), vals);
      if (!Array.isArray(list) || list.length === 0) continue;
      target = { [head]: list[0] };
    }
    const v = resolvePath(h, target);
    if (typeof v !== 'function') bad.push('处理器非函数: ' + h);
  }
  if (bad.length) {
    failures += bad.length;
    console.log(`✗ ${name}`);
    bad.forEach((b) => console.log('    ' + b));
  } else {
    console.log(`✓ ${name}`);
  }
}

/* ---- 后台全屏（富交互页 radar/ai/funnel 由 sections/* 渲染，不走模板，跳过） ---- */
const RICH = new Set(['radar', 'radarDetail', 'radarForm', 'ai', 'aiDetail', 'funnel', 'campaigns']);
const adminRegistry = JSON.parse(fs.readFileSync(path.join(SRC, 'admin/registry.json'), 'utf8'));
for (const s of adminRegistry.screens) {
  if (RICH.has(s.key)) {
    console.log('- admin/' + s.key + '（富交互页，跳过模板绑定检查）');
    continue;
  }
  const c = new adminMod.AdminController(new apiMod.MockApi(), s.key);
  await c.init();
  const vals = c.renderVals();
  const html = fs.readFileSync(path.join(SRC, 'admin/templates', s.key + '.html'), 'utf8');
  await checkPage('admin/' + s.key, html, vals);
}

/* ---- H5 全屏 ---- */
const h5Registry = JSON.parse(fs.readFileSync(path.join(SRC, 'h5/registry.json'), 'utf8'));
for (const s of h5Registry) {
  const c = new h5Mod.H5Controller(s.key);
  await c.init();
  const vals = c.renderVals();
  const html = fs.readFileSync(path.join(SRC, 'h5/templates', s.key + '.html'), 'utf8');
  await checkPage('h5/' + s.key, html, vals);
}

console.log(failures === 0 ? '\n全部通过' : `\n${failures} 处失败`);
process.exit(failures === 0 ? 0 : 1);
