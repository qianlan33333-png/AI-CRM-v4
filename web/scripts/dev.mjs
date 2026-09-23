/**
 * 开发预览服务器：构建 dist/ 后启动静态服务，支持 --port / --host / --watch。
 * 用法：npm run dev [-- --port 7100 --watch]
 */
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const DIST = path.join(ROOT, 'dist');

const args = process.argv.slice(2);
const opt = (name, dflt) => {
  const i = args.indexOf('--' + name);
  return i >= 0 ? args[i + 1] : dflt;
};
const PORT = Number(opt('port', '7100'));
const HOST = opt('host', '127.0.0.1');
const WATCH = args.includes('--watch');

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.woff2': 'font/woff2',
};

function runBuild() {
  return new Promise((resolve, reject) => {
    const p = spawn(process.execPath, [path.join(ROOT, 'scripts/build.mjs')], {
      cwd: ROOT,
      stdio: 'inherit',
    });
    p.on('exit', (code) => (code === 0 ? resolve() : reject(new Error('build failed: ' + code))));
  });
}

function serve() {
  const server = http.createServer((req, res) => {
    const url = decodeURIComponent((req.url || '/').split('?')[0]);
    let file = path.join(DIST, url === '/' ? 'index.html' : url);
    if (!file.startsWith(DIST)) {
      res.writeHead(403).end('Forbidden');
      return;
    }
    if (fs.existsSync(file) && fs.statSync(file).isDirectory()) {
      file = path.join(file, 'index.html');
    }
    if (!fs.existsSync(file)) {
      res.writeHead(404).end('Not Found: ' + url);
      return;
    }
    res.writeHead(200, { 'Content-Type': MIME[path.extname(file)] || 'application/octet-stream' });
    fs.createReadStream(file).pipe(res);
  });
  server.listen(PORT, HOST, () => {
    console.log(`\n▶ AI-CRM 预览服务  http://${HOST}:${PORT}/`);
    console.log(`  管理后台  http://${HOST}:${PORT}/admin/customers.html`);
    console.log(`  企微侧边栏 http://${HOST}:${PORT}/sidebar/`);
    console.log(`  用户端 H5 http://${HOST}:${PORT}/h5/\n`);
  });
}

async function main() {
  await runBuild();
  serve();
  if (WATCH) {
    let timer = null;
    fs.watch(path.join(ROOT, 'src'), { recursive: true }, () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        console.log('· src 变动，重新构建…');
        runBuild().then(() => console.log('✓ 已重建')).catch((e) => console.error(e.message));
      }, 200);
    });
    console.log('watch 模式已开启（监听 src/）');
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
