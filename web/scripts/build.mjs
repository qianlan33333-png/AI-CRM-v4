/** Build all browser surfaces as deterministic, content-hashed ESM assets. */
import { build } from 'esbuild';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

const ROOT = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const REPOSITORY = path.dirname(ROOT);
const SRC = path.join(ROOT, 'src');
const DIST = path.join(ROOT, 'dist');
const ASSETS = path.join(DIST, 'assets');

const read = (file) => fs.readFileSync(file, 'utf8');
const write = (file, contents) => {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, contents);
};

function transform(html) {
  let output = html
    .replace(/<sc-for\s+([^>]*?)list="([^"]*)"([^>]*?)as="([^"]*)"([^>]*)>/g, (_match, _a, list, _b, as) => `<template data-sc-for="${list}" data-as="${as}">`)
    .replace(/<\/sc-for>/g, '</template>')
    .replace(/<sc-if\s+([^>]*?)value="([^"]*)"([^>]*)>/g, (_match, _a, value) => `<template data-sc-if="${value}">`)
    .replace(/<\/sc-if>/g, '</template>');
  if (output.includes('<sc-for') || output.includes('<sc-if')) {
    output = output
      .replace(/<sc-for([^>]*)>/g, (_match, attributes) => {
        const list = (attributes.match(/list="([^"]*)"/) || [])[1] || '';
        const as = (attributes.match(/as="([^"]*)"/) || [])[1] || 'item';
        return `<template data-sc-for="${list}" data-as="${as}">`;
      })
      .replace(/<sc-if([^>]*)>/g, (_match, attributes) => {
        const value = (attributes.match(/value="([^"]*)"/) || [])[1] || '';
        return `<template data-sc-if="${value}">`;
      });
  }
  return output;
}

const registry = JSON.parse(read(path.join(SRC, 'admin/registry.json')));
const navItems = JSON.parse(read(path.join(SRC, 'admin/nav.json')));
const h5Registry = JSON.parse(read(path.join(SRC, 'h5/registry.json')));
const packageJson = JSON.parse(read(path.join(REPOSITORY, 'package.json')));
const richPages = new Set(['radar', 'radarDetail', 'radarForm', 'ai', 'aiDetail', 'funnel', 'campaigns']);
const adminPaths = { tags: '/admin/wecom-tags' };

function sourceSHA() {
  if (process.env.AICRM_SOURCE_SHA) return process.env.AICRM_SOURCE_SHA;
  return execFileSync('git', ['rev-parse', 'HEAD'], { cwd: REPOSITORY, encoding: 'utf8' }).trim();
}

function navHtml(activeNav) {
  let html = '';
  let lastGroup = null;
  for (const item of navItems) {
    if (item.group !== lastGroup) {
      html += `<div class="side-grp">${item.group}</div>\n`;
      lastGroup = item.group;
    }
    html += `<a class="nav-item${item.key === activeNav ? ' on' : ''}" href="${adminPaths[item.key] || `${item.key}.html`}">${item.svg}<span>${item.label}</span></a>\n`;
  }
  return html;
}

function moduleScript(relative, entry) {
  return `<script type="module" src="${relative}${entry}"></script>`;
}

function stylesheet(relative, entry) {
  return `<link rel="stylesheet" href="${relative}${entry}">`;
}

function adminShell(screen, assets, rich) {
  return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${screen.label} · AI-CRM 管理后台</title>
${stylesheet('../', assets.tokens)}
${rich ? stylesheet('../', assets.labs) : ''}
</head>
<body data-page="${screen.key}">
<div class="shell">
  <aside class="side">
    <div class="side-brand"><div class="mark">CRM</div><div><div class="name">客户管理后台</div><div class="en">ADMIN CONSOLE</div></div></div>
    <nav class="side-nav">${navHtml(screen.nav)}</nav>
    <div class="side-user"><div class="avatar">运</div><div><div class="n">运营管理员</div><div class="s">退出登录</div></div></div>
  </aside>
  <main id="stage" class="stage${rich ? ' rich' : ''}"></main>
</div>
${moduleScript('../', assets.admin)}
</body>
</html>
`;
}

function adminPage(screen, assets) {
  if (screen.key === 'questionnaireDetail') {
    const template = read(path.join(SRC, 'admin/templates/questionnaireEditorStandalone.html'));
    return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${screen.label} · AI-CRM 管理后台</title>
${stylesheet('../', assets.questionnaireEditorStyles)}
</head>
<body data-page="${screen.key}">
${template}
${moduleScript('../', assets.questionnaireEditor)}
</body>
</html>
`;
  }
  const rich = richPages.has(screen.key);
  const shell = adminShell(screen, assets, rich);
  if (rich) return shell;
  const template = transform(read(path.join(SRC, 'admin/templates', `${screen.key}.html`)));
  return shell.replace(moduleScript('../', assets.admin), `<template id="tpl">\n${template}\n</template>\n${moduleScript('../', assets.admin)}`);
}

function h5Page(screen, assets) {
  const template = transform(read(path.join(SRC, 'h5/templates', `${screen.key}.html`)));
  return `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>${screen.title} · AI-CRM 用户端</title>${stylesheet('../', assets.tokens)}</head><body data-page="${screen.key}"><div class="h5-backdrop"><div><div class="phone"><div id="screen" class="phone-screen"></div></div><div style="text-align:center;margin-top:14px;font-size:12px;color:#8F959E"><a href="index.html">← 全部屏幕</a></div></div></div><template id="tpl">${template}</template>${moduleScript('../', assets.h5)}</body></html>\n`;
}

function sidebarPage(assets) {
  return `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>企微侧边栏 · AI-CRM</title>${stylesheet('../', assets.tokens)}${stylesheet('../', assets.sidebarStyles)}</head><body>${read(path.join(SRC, 'sidebar/templates/index.html'))}${moduleScript('../', assets.sidebar)}</body></html>\n`;
}

function memberGridSharePage(assets) {
  return `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="referrer" content="no-referrer"><title>Member Grid 公开会员网格</title>${stylesheet('../', assets.tokens)}</head><body><div id="stage" class="ix-wrap"></div>${moduleScript('../', assets.memberGridShare)}</body></html>\n`;
}

function rootEntry() {
  const login = '/login?next=%2Fadmin';
  return `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta http-equiv="refresh" content="0; url=${login}"><title>正在进入 AI-CRM</title></head><body>正在验证登录状态… <a href="${login}">手动进入管理后台</a></body></html>\n`;
}

function h5Index(assets) {
  let sections = '';
  for (const [group, label] of Object.entries({ Q: '问卷作答流程', S: '报名 / 续费落地' })) {
    const links = h5Registry.filter((screen) => screen.group === group).map((screen) => `<a href="${screen.key}.html">${screen.title}</a>`).join('\n');
    sections += `<div class="ix-sec">${label}</div><div class="ix-list">${links}</div>`;
  }
  return `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>用户端 H5 · AI-CRM</title>${stylesheet('../', assets.tokens)}</head><body><div class="ix-wrap"><h1 class="ix-title">用户端 H5</h1><p class="ix-sub">12 屏 · <a href="../index.html">← 返回总索引</a></p>${sections}</div></body></html>\n`;
}

function outputPath(output) {
  return path.relative(DIST, path.resolve(REPOSITORY, output)).split(path.sep).join('/');
}

function fileMetadata(contents) {
  return {
    bytes: contents.byteLength,
    gzip_bytes: gzipSync(contents, { level: 9 }).byteLength,
    sha256: crypto.createHash('sha256').update(contents).digest('hex'),
  };
}

function createManifest(metafile, entries) {
  const files = {};
  for (const [output, metadata] of Object.entries(metafile.outputs)) {
    const relative = outputPath(output);
    const absolute = path.join(DIST, relative);
    const contents = fs.readFileSync(absolute);
    files[relative] = {
      ...fileMetadata(contents),
      entry_point: metadata.entryPoint ? path.relative(REPOSITORY, path.resolve(REPOSITORY, metadata.entryPoint)).split(path.sep).join('/') : undefined,
      imports: metadata.imports.map((item) => ({ path: outputPath(item.path), kind: item.kind })),
      inputs: Object.keys(metadata.inputs).map((input) => path.relative(REPOSITORY, path.resolve(REPOSITORY, input)).split(path.sep).join('/')).sort(),
    };
  }
  return {
    version: 1,
    source_sha: sourceSHA(),
    tools: {
      node: packageJson.engines.node,
      npm: packageJson.engines.npm,
      esbuild: packageJson.devDependencies.esbuild,
      orval: packageJson.devDependencies.orval,
    },
    entries,
    files: Object.fromEntries(Object.entries(files).sort(([left], [right]) => left.localeCompare(right))),
  };
}

function createReleaseFiles() {
  const files = {};
  const walk = (directory) => {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const absolute = path.join(directory, entry.name);
      if (entry.isDirectory()) walk(absolute);
      else if (entry.isFile() && entry.name !== 'asset-manifest.json') {
        const relative = path.relative(DIST, absolute).split(path.sep).join('/');
        files[relative] = fileMetadata(fs.readFileSync(absolute));
      }
    }
  };
  walk(DIST);
  return Object.fromEntries(Object.entries(files).sort(([left], [right]) => left.localeCompare(right)));
}

async function main() {
  fs.rmSync(DIST, { recursive: true, force: true });
  const result = await build({
    entryPoints: {
      admin: path.join(SRC, 'admin/main.ts'),
      h5: path.join(SRC, 'h5/main.ts'),
      sidebar: path.join(SRC, 'sidebar/main.ts'),
      sidebarStyles: path.join(SRC, 'sidebar/sidebar.css'),
      memberGridShare: path.join(SRC, 'public/main.ts'),
      questionnaireEditor: path.join(SRC, 'admin/sections/questionnaireEditor.ts'),
      questionnaireEditorStyles: path.join(SRC, 'admin/sections/questionnaireEditorStyles.css'),
      tokens: path.join(SRC, 'shared/ui/tokens.css'),
      labs: path.join(SRC, 'admin/sections/labs.css'),
    },
    bundle: true,
    format: 'esm',
    splitting: true,
    target: 'es2020',
    outdir: ASSETS,
    entryNames: '[name]-[hash]',
    chunkNames: 'chunks/[name]-[hash]',
    assetNames: 'files/[name]-[hash]',
    minify: true,
    metafile: true,
    logLevel: 'warning',
  });

  const entries = {};
  for (const [output, metadata] of Object.entries(result.metafile.outputs)) {
    if (!metadata.entryPoint) continue;
    const name = Object.entries({
      admin: path.join(SRC, 'admin/main.ts'), h5: path.join(SRC, 'h5/main.ts'),
      sidebar: path.join(SRC, 'sidebar/main.ts'), memberGridShare: path.join(SRC, 'public/main.ts'),
      questionnaireEditor: path.join(SRC, 'admin/sections/questionnaireEditor.ts'),
      questionnaireEditorStyles: path.join(SRC, 'admin/sections/questionnaireEditorStyles.css'),
      sidebarStyles: path.join(SRC, 'sidebar/sidebar.css'),
      tokens: path.join(SRC, 'shared/ui/tokens.css'), labs: path.join(SRC, 'admin/sections/labs.css'),
    }).find(([, entry]) => path.resolve(REPOSITORY, metadata.entryPoint) === entry)?.[0];
    if (name) entries[name] = outputPath(output);
  }
  for (const required of ['admin', 'h5', 'sidebar', 'sidebarStyles', 'memberGridShare', 'questionnaireEditor', 'questionnaireEditorStyles', 'tokens', 'labs']) {
    if (!entries[required]) throw new Error(`missing build entry: ${required}`);
  }

  const manifest = createManifest(result.metafile, entries);

  for (const screen of registry.screens) {
    const filename = screen.key === 'tags' ? 'wecom-tags.html' : `${screen.key}.html`;
    write(path.join(DIST, 'admin', filename), adminPage(screen, entries));
  }
  write(path.join(DIST, 'admin/index.html'), '<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><meta http-equiv="refresh" content="0; url=customers.html"><title>AI-CRM 管理后台</title></head><body>正在进入管理后台… <a href="customers.html">手动进入</a></body></html>\n');
  for (const screen of h5Registry) write(path.join(DIST, 'h5', `${screen.key}.html`), h5Page(screen, entries));
  write(path.join(DIST, 'h5/index.html'), h5Index(entries));
  write(path.join(DIST, 'sidebar/index.html'), sidebarPage(entries));
  write(path.join(DIST, 'member-grid-share/index.html'), memberGridSharePage(entries));
  write(path.join(DIST, 'index.html'), rootEntry());
  manifest.release_files = createReleaseFiles();
  write(path.join(DIST, 'asset-manifest.json'), `${JSON.stringify(manifest, null, 2)}\n`);

  console.log(`✓ build done: ${registry.screens.length + h5Registry.length + 4} pages + ${Object.keys(entries).length} hashed entries → dist/`);
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
