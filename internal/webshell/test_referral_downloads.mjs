import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

export const RETENTION_MS = 72 * 60 * 60 * 1000;
export const DOWNLOAD_ROOT_NAME = 'aicrm-referral-test-downloads';
export const defaultDownloadRoot = () => path.join(os.tmpdir(), DOWNLOAD_ROOT_NAME);
const markerName = '.aicrm-test-download.json';

async function ownedRoot(root, create = false) {
  root = path.resolve(root);
  if (path.basename(root) !== DOWNLOAD_ROOT_NAME) throw new Error('Unexpected test download root');
  if (create) await fs.mkdir(root, { recursive: true, mode: 0o700 });
  let stat;
  try { stat = await fs.lstat(root); }
  catch (error) { if (error.code === 'ENOENT' && !create) return null; throw error; }
  if (!stat.isDirectory() || stat.isSymbolicLink()) throw new Error('Test download root must be a real directory');
  return root;
}

export async function cleanupExpiredDownloads({ root = defaultDownloadRoot(), now = Date.now() } = {}) {
  root = await ownedRoot(root);
  const removed = [];
  if (!root) return removed;
  for (const entry of await fs.readdir(root, { withFileTypes: true })) {
    if (!entry.isDirectory() || !/^run-[A-Za-z0-9]{6}$/.test(entry.name)) continue;
    const directory = path.join(root, entry.name);
    try {
      // Never follow a substituted run directory or ownership marker.
      const directoryStat = await fs.lstat(directory);
      if (!directoryStat.isDirectory() || directoryStat.isSymbolicLink()) continue;
      const markerPath = path.join(directory, markerName);
      const markerStat = await fs.lstat(markerPath);
      if (!markerStat.isFile() || markerStat.isSymbolicLink()) continue;
      let marker;
      try { marker = JSON.parse(await fs.readFile(markerPath, 'utf8')); } catch { continue; }
      if (!marker || typeof marker !== 'object' || marker.owner !== DOWNLOAD_ROOT_NAME || marker.schema !== 1 ||
          !Number.isFinite(marker.createdAtMs) || marker.createdAtMs <= 0) continue;
      let lastActivity = Math.max(marker.createdAtMs, Math.floor(directoryStat.mtimeMs), Math.floor(markerStat.mtimeMs));
      for (const child of await fs.readdir(directory)) {
        lastActivity = Math.max(lastActivity, Math.floor((await fs.lstat(path.join(directory, child))).mtimeMs));
      }
      if (now - lastActivity < RETENTION_MS) continue;
      await fs.rm(directory, { recursive: true, force: true });
      removed.push(entry.name);
    } catch (error) {
      // Another cleanup invocation may have removed this run already.
      if (error.code !== 'ENOENT') throw error;
    }
  }
  return removed;
}

export async function createDownloadDirectory({ root = defaultDownloadRoot() } = {}) {
  root = await ownedRoot(root, true);
  await cleanupExpiredDownloads({ root });
  const directory = await fs.mkdtemp(path.join(root, 'run-'));
  await fs.writeFile(path.join(directory, markerName), JSON.stringify({
    schema: 1, owner: DOWNLOAD_ROOT_NAME, createdAtMs: Date.now(),
  }), { flag: 'wx', mode: 0o600 });
  return directory;
}

export async function configureTestDownloads(call, directory) {
  await call('Browser.setDownloadBehavior', {
    behavior: 'allow', downloadPath: directory, eventsEnabled: true,
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const args = process.argv.slice(2);
  if (args[0] !== '--cleanup' || (args.length !== 1 && (args.length !== 3 || args[1] !== '--root'))) {
    throw new Error('Usage: node referral_test_downloads.mjs --cleanup [--root ROOT]');
  }
  const removed = await cleanupExpiredDownloads({ root: args[2] || defaultDownloadRoot() });
  console.log(JSON.stringify({ removed: removed.length, retention_hours: 72 }));
}
