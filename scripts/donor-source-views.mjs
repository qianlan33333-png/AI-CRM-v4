import crypto from 'node:crypto';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

export const INDEX_SCHEMA_VERSION = 1;
export const RECEIPT_SCHEMA_VERSION = 1;
export const RECEIPT_PATH = '.aicrm-dedup/donor-views-receipt.json';
export const LOCK_PATH = '.aicrm-dedup/donor-views.lock';

export class DonorViewError extends Error {
  constructor(code, message) {
    super(message);
    this.name = 'DonorViewError';
    this.code = code;
  }
}

function fail(code, message) {
  throw new DonorViewError(code, message);
}

function isPlainObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function requireString(value, label) {
  if (typeof value !== 'string' || value.length === 0) fail('INVALID_INDEX', `${label} must be a non-empty string`);
  return value;
}

function requireArray(value, label) {
  if (!Array.isArray(value)) fail('INVALID_INDEX', `${label} must be an array`);
  return value;
}

function isHex(value, length) {
  return typeof value === 'string' && new RegExp(`^[0-9a-f]{${length}}$`).test(value);
}

export function sha256(bytes) {
  return crypto.createHash('sha256').update(bytes).digest('hex');
}

export function gitBlobSHA1(bytes) {
  return crypto.createHash('sha1').update(`blob ${bytes.byteLength}\0`).update(bytes).digest('hex');
}

function normalizeLogicalPath(value, label) {
  requireString(value, label);
  if (value.includes('\\') || path.posix.isAbsolute(value) || value.startsWith('./') || value.includes('\0')) {
    fail('PATH_ESCAPE', `${label} must be a clean repository-relative POSIX path`);
  }
  const normalized = path.posix.normalize(value);
  if (normalized !== value || normalized === '.' || normalized === '..' || normalized.startsWith('../')) {
    fail('PATH_ESCAPE', `${label} escapes or normalizes outside its declared repository path`);
  }
  return normalized;
}

function assertNoSymlinkAncestor(root, absolute, label) {
  const relative = path.relative(root, absolute);
  if (relative === '' || relative === '..' || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
    fail('PATH_ESCAPE', `${label} is outside the declared root`);
  }
  let current = root;
  for (const segment of relative.split(path.sep)) {
    current = path.join(current, segment);
    if (!fs.existsSync(current)) break;
    if (fs.lstatSync(current).isSymbolicLink()) fail('SYMLINK_PATH', `${label} traverses a symbolic link: ${relative}`);
  }
}

export function resolveLogicalPath(root, logicalPath, label = 'path') {
  const normalized = normalizeLogicalPath(logicalPath, label);
  const absolute = path.resolve(root, ...normalized.split('/'));
  assertNoSymlinkAncestor(root, absolute, label);
  return absolute;
}

function modeString(stat) {
  return `100${(stat.mode & 0o777).toString(8).padStart(3, '0')}`;
}

function readRegularFile(absolute, label) {
  if (!fs.existsSync(absolute)) fail('MISSING_FILE', `${label} is missing`);
  const stat = fs.lstatSync(absolute);
  if (!stat.isFile()) fail('NOT_REGULAR_FILE', `${label} must be a regular file`);
  return { bytes: fs.readFileSync(absolute), mode: modeString(stat) };
}

function parseJSON(absolute, label) {
  let parsed;
  try {
    parsed = JSON.parse(readRegularFile(absolute, label).bytes.toString('utf8'));
  } catch (error) {
    fail('INVALID_JSON', `${label} is not valid JSON: ${error.message}`);
  }
  if (!isPlainObject(parsed)) fail('INVALID_INDEX', `${label} must contain a JSON object`);
  return parsed;
}

function unique(items, selector, label) {
  const seen = new Set();
  for (const item of items) {
    const key = selector(item);
    if (seen.has(key)) fail('DUPLICATE_DECLARATION', `${label} repeats ${key}`);
    seen.add(key);
  }
}

function requireMode(value, label) {
  if (value !== '100644') fail('INVALID_INDEX', `${label} must be 100644 for the pilot`);
  return value;
}

function requireObservedSource(value, label) {
  if (!isPlainObject(value)) fail('INVALID_INDEX', `${label} must be an object`);
  requireString(value.repository, `${label}.repository`);
  if (!isHex(value.commit, 40)) fail('INVALID_INDEX', `${label}.commit must be a 40-hex commit`);
  value.path = normalizeLogicalPath(value.path, `${label}.path`);
  if (!isHex(value.git_blob_sha, 40)) fail('INVALID_INDEX', `${label}.git_blob_sha must be a 40-hex Git blob`);
  return value;
}

function validateIndex(index) {
  if (index.schema_version !== INDEX_SCHEMA_VERSION) fail('INVALID_INDEX', 'unsupported source-index schema_version');
  index.lock_path = normalizeLogicalPath(index.lock_path, 'lock_path');
  const libraries = requireArray(index.libraries, 'libraries');
  const contents = requireArray(index.contents, 'contents');
  const bindings = requireArray(index.bindings, 'bindings');
  const views = requireArray(index.views, 'views');
  unique(libraries, (item) => requireString(item.id, 'library.id'), 'library id');
  unique(contents, (item) => requireString(item.id, 'content.id'), 'content id');
  unique(bindings, (item) => `${requireString(item.module, 'binding.module')}\0${normalizeLogicalPath(item.logical_path, 'binding.logical_path')}`, 'binding');
  unique(views, (item) => normalizeLogicalPath(item.target_path, 'view.target_path'), 'view target');

  const libraryByID = new Map();
  for (const library of libraries) {
    if (!isPlainObject(library) || library.immutable !== true) fail('INVALID_INDEX', 'every source library must be an immutable object');
    if (!['frozen_donor', 'active_v3_contract'].includes(library.authority_kind)) {
      fail('INVALID_INDEX', 'library authority_kind must be frozen_donor or active_v3_contract');
    }
    requireString(library.source_repository, 'library.source_repository');
    if (!isHex(library.source_commit, 40)) fail('INVALID_INDEX', 'library.source_commit must be a 40-hex commit');
    library.root = normalizeLogicalPath(library.root, 'library.root');
    libraryByID.set(library.id, library);
  }

  const contentByID = new Map();
  const canonicalHashes = new Set();
  const canonicalPaths = new Set();
  for (const content of contents) {
    if (!isPlainObject(content) || !libraryByID.has(content.library_id)) fail('INVALID_INDEX', 'content must name an existing immutable library');
    content.canonical_path = normalizeLogicalPath(content.canonical_path, 'content.canonical_path');
    content.source_path = normalizeLogicalPath(content.source_path, 'content.source_path');
    if (!content.canonical_path.startsWith(`${libraryByID.get(content.library_id).root}/`)) {
      fail('INVALID_INDEX', `canonical path is outside immutable library: ${content.canonical_path}`);
    }
    if (!isHex(content.source_git_blob_sha, 40) || !isHex(content.content_sha256, 64)) fail('INVALID_INDEX', 'content hashes must be fixed SHA-1/SHA-256 values');
    if (!Number.isSafeInteger(content.bytes) || content.bytes < 0) fail('INVALID_INDEX', 'content.bytes must be a non-negative integer');
    requireMode(content.mode, 'content.mode');
    if (canonicalHashes.has(content.content_sha256) || canonicalPaths.has(content.canonical_path)) {
      fail('DUPLICATE_CANONICAL_CONTENT', 'one payload may have only one canonical entry');
    }
    canonicalHashes.add(content.content_sha256);
    canonicalPaths.add(content.canonical_path);
    contentByID.set(content.id, content);
  }

  for (const binding of bindings) {
    if (!isPlainObject(binding) || !contentByID.has(binding.content_id)) fail('INVALID_INDEX', 'binding must name an existing canonical content id');
    binding.logical_path = normalizeLogicalPath(binding.logical_path, 'binding.logical_path');
    binding.source_path = normalizeLogicalPath(binding.source_path, 'binding.source_path');
    requireString(binding.source_repository, 'binding.source_repository');
    if (!isHex(binding.source_commit, 40) || !isHex(binding.source_git_blob_sha, 40)) fail('INVALID_INDEX', 'binding must record exact source commit/blob');
    requireMode(binding.mode, 'binding.mode');
    requireString(binding.usage, 'binding.usage');
    requireString(binding.freeze_gate, 'binding.freeze_gate');
    requireString(binding.freeze_ledger, 'binding.freeze_ledger');
    if (binding.observed_source !== undefined) requireObservedSource(binding.observed_source, 'binding.observed_source');
    if (!['tracked_pre_p4_removal', 'untracked_post_p4'].includes(binding.current_path_state)) {
      fail('INVALID_INDEX', 'binding current_path_state must describe the PR-3/P4 transition');
    }
    const content = contentByID.get(binding.content_id);
    const library = libraryByID.get(content.library_id);
    if (
      binding.source_repository !== library.source_repository
      || binding.source_commit !== library.source_commit
      || binding.source_path !== content.source_path
      || binding.source_git_blob_sha !== content.source_git_blob_sha
    ) {
      fail('SOURCE_VERSION_MISMATCH', `binding source identity differs from immutable canonical source: ${binding.logical_path}`);
    }
  }

  for (const view of views) {
    if (!isPlainObject(view) || !contentByID.has(view.content_id)) fail('INVALID_INDEX', 'view must name an existing canonical content id');
    view.target_path = normalizeLogicalPath(view.target_path, 'view.target_path');
    if (canonicalPaths.has(view.target_path)) fail('INVALID_INDEX', 'view target cannot be a canonical source path');
    if (typeof view.enabled !== 'boolean') fail('INVALID_INDEX', 'view.enabled must be boolean');
  }
  return { index, libraryByID, contentByID };
}

export function loadSourceIndex(root, indexPath = 'web/donor-sources/source-index.json') {
  const normalizedRoot = path.resolve(root);
  if (!fs.existsSync(normalizedRoot) || !fs.lstatSync(normalizedRoot).isDirectory()) fail('INVALID_ROOT', 'root must be an existing directory');
  const normalizedIndex = normalizeLogicalPath(indexPath, 'index_path');
  const absoluteIndex = resolveLogicalPath(normalizedRoot, normalizedIndex, 'index_path');
  const index = parseJSON(absoluteIndex, 'source-index');
  const validated = validateIndex(index);
  const absoluteLock = resolveLogicalPath(normalizedRoot, validated.index.lock_path, 'lock_path');
  return { root: normalizedRoot, indexPath: normalizedIndex, absoluteIndex, absoluteLock, ...validated };
}

function stableJSON(value) {
  if (Array.isArray(value)) return `[${value.map(stableJSON).join(',')}]`;
  if (isPlainObject(value)) return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${stableJSON(value[key])}`).join(',')}}`;
  return JSON.stringify(value);
}

function lockedEntries(loaded) {
  return [...loaded.contentByID.values()].map((content) => {
    const library = loaded.libraryByID.get(content.library_id);
    return {
      id: content.id,
      library_id: content.library_id,
      source_repository: library.source_repository,
      source_commit: library.source_commit,
      canonical_path: content.canonical_path,
      source_path: content.source_path,
      source_git_blob_sha: content.source_git_blob_sha,
      content_sha256: content.content_sha256,
      bytes: content.bytes,
      mode: content.mode,
    };
  }).sort((left, right) => left.id.localeCompare(right.id));
}

function verifySourceLock(loaded) {
  const lock = parseJSON(loaded.absoluteLock, 'source lock');
  if (lock.schema_version !== INDEX_SCHEMA_VERSION || !Array.isArray(lock.entries)) fail('INVALID_LOCK', 'source lock schema is invalid');
  const expected = lockedEntries(loaded);
  if (stableJSON(lock.entries) !== stableJSON(expected)) fail('SOURCE_LOCK_MISMATCH', 'source lock differs from immutable source-index entries');
  return lock;
}

function checkContent(root, content) {
  const absolute = resolveLogicalPath(root, content.canonical_path, `canonical:${content.id}`);
  const file = readRegularFile(absolute, `canonical:${content.id}`);
  if (file.bytes.byteLength !== content.bytes) fail('CANONICAL_SIZE_MISMATCH', `canonical:${content.id} has unexpected byte count`);
  if (sha256(file.bytes) !== content.content_sha256) fail('CANONICAL_HASH_MISMATCH', `canonical:${content.id} SHA-256 differs from the immutable index`);
  if (gitBlobSHA1(file.bytes) !== content.source_git_blob_sha) fail('CANONICAL_GIT_BLOB_MISMATCH', `canonical:${content.id} Git blob SHA-1 differs from the immutable index`);
  if (file.mode !== content.mode) fail('CANONICAL_MODE_MISMATCH', `canonical:${content.id} mode differs from the immutable index`);
  return { ...content, absolute, file_bytes: file.bytes };
}

function checkBinding(root, binding, content) {
  const absolute = resolveLogicalPath(root, binding.logical_path, `binding:${binding.logical_path}`);
  const file = readRegularFile(absolute, `binding:${binding.logical_path}`);
  if (sha256(file.bytes) !== content.content_sha256 || file.bytes.byteLength !== content.bytes || file.mode !== content.mode) {
    fail('BINDING_DRIFT', `binding:${binding.logical_path} differs from canonical ${content.id}`);
  }
}

function sourceSummary(loaded, checked) {
  return {
    index_path: loaded.indexPath,
    canonical_contents: checked.map((content) => ({ id: content.id, canonical_path: content.canonical_path, content_sha256: content.content_sha256, bytes: content.bytes })),
    bindings_verified: loaded.index.bindings.length,
    planned_views: loaded.index.views.filter((view) => !view.enabled).length,
    enabled_views: loaded.index.views.filter((view) => view.enabled).length,
  };
}

export function verifySourceIndex(root, indexPath) {
  // A reviewed P4 view is deliberately absent from the checkout until the
  // safe materializer creates it. A tracked absence remains an error.
  const { loaded, checked } = loadValidated(root, indexPath, { allowMissingEnabledViewBindings: true });
  return sourceSummary(loaded, checked);
}

function receiptAbsolute(root) {
  return resolveLogicalPath(root, RECEIPT_PATH, 'receipt_path');
}

function loadReceipt(root) {
  const absolute = receiptAbsolute(root);
  if (!fs.existsSync(absolute)) return { absolute, receipt: { schema_version: RECEIPT_SCHEMA_VERSION, targets: [] } };
  const receipt = parseJSON(absolute, 'materialization receipt');
  if (receipt.schema_version !== RECEIPT_SCHEMA_VERSION || !Array.isArray(receipt.targets)) fail('INVALID_RECEIPT', 'materialization receipt schema is invalid');
  unique(receipt.targets, (item) => normalizeLogicalPath(item.target_path, 'receipt.target_path'), 'receipt target');
  return { absolute, receipt };
}

function writeAtomically(root, absolute, bytes, mode, { replaceOwnedFile = false } = {}) {
  assertNoSymlinkAncestor(root, absolute, 'atomic target');
  const parent = path.dirname(absolute);
  fs.mkdirSync(parent, { recursive: true, mode: 0o755 });
  assertNoSymlinkAncestor(root, absolute, 'atomic target');
  const temporary = path.join(parent, `.${path.basename(absolute)}.aicrm-dedup-${process.pid}-${crypto.randomUUID()}`);
  try {
    fs.writeFileSync(temporary, bytes, { mode: parseInt(mode.slice(-3), 8), flag: 'wx' });
    fs.chmodSync(temporary, parseInt(mode.slice(-3), 8));
    if (replaceOwnedFile) {
      fs.renameSync(temporary, absolute);
    } else {
      try {
        // An exclusive same-directory link publishes a normal file only when
        // the target remains absent. Removing the temporary name leaves one
        // link, so views are never persistent hard links to their source.
        fs.linkSync(temporary, absolute);
        fs.rmSync(temporary);
      } catch (error) {
        if (error?.code === 'EEXIST') fail('DIRTY_TARGET', `target appeared during materialization: ${path.basename(absolute)}`);
        throw error;
      }
    }
  } finally {
    if (fs.existsSync(temporary)) fs.rmSync(temporary, { force: true });
  }
}

const trackedIndexCache = new Map();

function gitDirectory(root) {
  const dotGit = path.join(root, '.git');
  if (!fs.existsSync(dotGit)) fail('GIT_INDEX_UNAVAILABLE', 'source-view preparation requires a Git worktree');
  const stat = fs.lstatSync(dotGit);
  if (stat.isDirectory()) return dotGit;
  if (!stat.isFile()) fail('GIT_INDEX_UNAVAILABLE', 'Git metadata is neither a directory nor a gitdir file');
  const declaration = fs.readFileSync(dotGit, 'utf8').trim();
  const match = /^gitdir:[ \t]*(.+)$/.exec(declaration);
  if (!match) fail('GIT_INDEX_UNAVAILABLE', 'Git worktree metadata does not name its gitdir');
  const gitDirectoryPath = path.resolve(path.dirname(dotGit), match[1]);
  if (!fs.existsSync(gitDirectoryPath) || !fs.lstatSync(gitDirectoryPath).isDirectory()) {
    fail('GIT_INDEX_UNAVAILABLE', 'Git worktree gitdir is unavailable');
  }
  return gitDirectoryPath;
}

function gitIndexStamp(root) {
  const gitDirectoryPath = gitDirectory(root);
  const index = path.join(gitDirectoryPath, 'index');
  try {
    const stat = fs.statSync(index);
    if (!stat.isFile()) fail('GIT_INDEX_UNAVAILABLE', 'Git index is not a regular file');
    return `${index}\0${stat.dev}\0${stat.ino}\0${stat.size}\0${stat.mtimeMs}`;
  } catch (error) {
    if (error instanceof DonorViewError) throw error;
    fail('GIT_INDEX_UNAVAILABLE', 'Git index is unavailable');
  }
}

function trackedPaths(root) {
  const stamp = gitIndexStamp(root);
  const cached = trackedIndexCache.get(root);
  if (cached?.stamp === stamp) return cached.paths;
  const result = spawnSync('git', ['-C', root, 'ls-files', '-z'], { encoding: 'buffer' });
  if (result.error || result.status !== 0 || !Buffer.isBuffer(result.stdout)) {
    fail('GIT_INDEX_UNAVAILABLE', 'unable to inspect Git index');
  }
  const paths = new Set(result.stdout.toString('utf8').split('\0').filter(Boolean));
  trackedIndexCache.set(root, { stamp, paths });
  return paths;
}

function invalidateTrackedPaths(root) {
  trackedIndexCache.delete(root);
}

function isTracked(root, logicalPath) {
  return trackedPaths(root).has(logicalPath);
}

function indexDigest(loaded) {
  return sha256(readRegularFile(loaded.absoluteIndex, 'source-index').bytes);
}

function receiptRecord(view, content) {
  return {
    target_path: view.target_path,
    content_id: content.id,
    canonical_path: content.canonical_path,
    content_sha256: content.content_sha256,
    bytes: content.bytes,
    mode: content.mode,
  };
}

function matchesReceipt(record, content) {
  return isPlainObject(record)
    && record.content_id === content.id
    && record.canonical_path === content.canonical_path
    && record.content_sha256 === content.content_sha256
    && record.bytes === content.bytes
    && record.mode === content.mode;
}

function validateReceipt(loaded, receipt) {
  if (!isHex(receipt.index_sha256, 64) && receipt.targets.length !== 0) {
    fail('INVALID_RECEIPT', 'materialization receipt lacks an index SHA-256');
  }
  if (receipt.targets.length !== 0 && receipt.index_sha256 !== indexDigest(loaded)) {
    fail('STALE_RECEIPT', 'source index changed since materialized views were created');
  }
  const viewByTarget = new Map(loaded.index.views.map((view) => [view.target_path, view]));
  for (const record of receipt.targets) {
    if (!isPlainObject(record)) fail('INVALID_RECEIPT', 'materialization receipt target must be an object');
    const view = viewByTarget.get(normalizeLogicalPath(record.target_path, 'receipt.target_path'));
    if (!view || !view.enabled) fail('INVALID_RECEIPT', `receipt refers to an unknown or disabled view: ${record.target_path}`);
    const content = loaded.contentByID.get(view.content_id);
    if (!matchesReceipt(record, content)) fail('INVALID_RECEIPT', `receipt content does not match source index: ${record.target_path}`);
  }
}

function validateStaleReceiptForCleanup(loaded, receipt) {
  if (receipt.targets.length === 0) return [];
  if (!isHex(receipt.index_sha256, 64)) {
    fail('INVALID_RECEIPT', 'stale materialization receipt lacks an index SHA-256');
  }
  const currentViews = new Map(loaded.index.views.map((view) => [view.target_path, view]));
  return receipt.targets.map((record) => {
    if (!isPlainObject(record)) fail('INVALID_RECEIPT', 'stale materialization receipt target must be an object');
    const targetPath = normalizeLogicalPath(record.target_path, 'receipt.target_path');
    const current = currentViews.get(targetPath);
    if (!current || !current.enabled) {
      fail('STALE_RECEIPT_RECOVERY_REQUIRED', `current source index no longer declares the stale receipt target: ${targetPath}`);
    }
    requireString(record.content_id, `receipt:${targetPath}.content_id`);
    normalizeLogicalPath(record.canonical_path, `receipt:${targetPath}.canonical_path`);
    if (!isHex(record.content_sha256, 64) || !Number.isSafeInteger(record.bytes) || record.bytes < 0) {
      fail('INVALID_RECEIPT', `stale materialization receipt has an invalid content identity: ${targetPath}`);
    }
    requireMode(record.mode, `receipt:${targetPath}.mode`);
    return { ...record, target_path: targetPath };
  });
}

function materializedViewTargets(loaded) {
  return loaded.index.views
    .filter((view) => view.enabled && !isTracked(loaded.root, view.target_path))
    .map((view) => view.target_path)
    .sort();
}

function trackedViewTargets(loaded) {
  return loaded.index.views
    .filter((view) => view.enabled && isTracked(loaded.root, view.target_path))
    .map((view) => view.target_path)
    .sort();
}

function assertReceiptCoversMaterializedViews(loaded, receipt, { allowEmpty = false } = {}) {
  for (const record of receipt.targets) {
    if (isTracked(loaded.root, record.target_path)) {
      fail('TRACKED_TARGET', `materialized receipt target became tracked: ${record.target_path}`);
    }
  }
  const expected = materializedViewTargets(loaded);
  const actual = receipt.targets.map((record) => record.target_path).sort();
  if (allowEmpty && actual.length === 0) return;
  if (stableJSON(actual) !== stableJSON(expected)) {
    fail('RECEIPT_INCOMPLETE', 'materialization receipt does not cover exactly the currently untracked enabled view targets');
  }
}

function lockOwnerAbsolute(root) {
  return resolveLogicalPath(root, `${LOCK_PATH}/owner.json`, 'lock_owner_path');
}

function readLockOwner(root) {
  const absolute = lockOwnerAbsolute(root);
  let owner;
  try {
    owner = JSON.parse(readRegularFile(absolute, 'materialization lock owner').bytes.toString('utf8'));
  } catch (error) {
    if (error instanceof DonorViewError) {
      fail('LOCK_RECOVERY_REQUIRED', 'materialization lock has no readable owner metadata; inspect it manually');
    }
    fail('LOCK_RECOVERY_REQUIRED', `materialization lock owner metadata is invalid: ${error.message}`);
  }
  if (
    !isPlainObject(owner)
    || owner.schema_version !== 1
    || typeof owner.host !== 'string'
    || owner.host.length === 0
    || !Number.isSafeInteger(owner.pid)
    || owner.pid <= 0
    || typeof owner.lock_id !== 'string'
    || owner.lock_id.length === 0
    || typeof owner.created_at !== 'string'
    || Number.isNaN(Date.parse(owner.created_at))
  ) {
    fail('LOCK_RECOVERY_REQUIRED', 'materialization lock owner metadata is malformed; inspect it manually');
  }
  return owner;
}

function acquireLock(root) {
  const absolute = resolveLogicalPath(root, LOCK_PATH, 'lock_path');
  fs.mkdirSync(path.dirname(absolute), { recursive: true, mode: 0o755 });
  assertNoSymlinkAncestor(root, absolute, 'lock_path');
  try {
    fs.mkdirSync(absolute, { mode: 0o700 });
  } catch (error) {
    if (error?.code === 'EEXIST') {
      fail('CONCURRENT_MATERIALIZATION', 'another donor-view materialization or cleanup holds the lock; use explicit recover-lock only after confirming its owner is dead');
    }
    throw error;
  }
  const owner = {
    schema_version: 1,
    host: os.hostname(),
    pid: process.pid,
    lock_id: crypto.randomUUID(),
    created_at: new Date().toISOString(),
  };
  try {
    const ownerAbsolute = lockOwnerAbsolute(root);
    fs.writeFileSync(ownerAbsolute, `${JSON.stringify(owner)}\n`, { mode: 0o600, flag: 'wx' });
  } catch (error) {
    fs.rmSync(absolute, { recursive: true, force: true });
    throw error;
  }
  return () => {
    const current = readLockOwner(root);
    if (current.lock_id !== owner.lock_id) {
      fail('LOCK_OWNER_MISMATCH', 'materialization lock ownership changed; leave it for manual inspection');
    }
    fs.rmSync(absolute, { recursive: true, force: false });
  };
}

export function recoverMaterializationLock(root) {
  const normalizedRoot = path.resolve(root);
  const absolute = resolveLogicalPath(normalizedRoot, LOCK_PATH, 'lock_path');
  if (!fs.existsSync(absolute)) return { action: 'recover-lock', recovered: false, reason: 'lock_absent' };
  if (!fs.lstatSync(absolute).isDirectory()) fail('LOCK_RECOVERY_REQUIRED', 'materialization lock is not a directory; inspect it manually');
  const owner = readLockOwner(normalizedRoot);
  if (owner.host !== os.hostname()) {
    fail('LOCK_RECOVERY_REQUIRED', 'materialization lock belongs to another host; inspect it manually');
  }
  try {
    process.kill(owner.pid, 0);
  } catch (error) {
    if (error?.code !== 'ESRCH') {
      fail('LOCK_RECOVERY_REQUIRED', 'materialization lock owner cannot be proven dead; inspect it manually');
    }
    const rechecked = readLockOwner(normalizedRoot);
    if (stableJSON(rechecked) !== stableJSON(owner)) {
      fail('LOCK_RECOVERY_REQUIRED', 'materialization lock owner changed during recovery; inspect it manually');
    }
    fs.rmSync(absolute, { recursive: true, force: false });
    return { action: 'recover-lock', recovered: true, lock_id: owner.lock_id };
  }
  fail('LOCK_RECOVERY_REQUIRED', 'materialization lock owner is still active; inspect it manually');
}

function checkedContents(loaded, { allowMissingEnabledViewBindings = false } = {}) {
  const checked = [...loaded.contentByID.values()].map((content) => checkContent(loaded.root, content));
  const byID = new Map(checked.map((content) => [content.id, content]));
  const enabledTargets = new Set(loaded.index.views.filter((view) => view.enabled).map((view) => view.target_path));
  for (const binding of loaded.index.bindings) {
    try {
      checkBinding(loaded.root, binding, byID.get(binding.content_id));
    } catch (error) {
      // A missing source-view binding is permissible only after it has left
      // Git's index (the disposable proof or a reviewed P4 removal). A
      // tracked-but-missing working-tree file is a developer/worktree error,
      // not a signal to silently materialize a replacement.
      if (
        allowMissingEnabledViewBindings
        && error instanceof DonorViewError
        && error.code === 'MISSING_FILE'
        && enabledTargets.has(binding.logical_path)
        && binding.current_path_state === 'untracked_post_p4'
        && !isTracked(loaded.root, binding.logical_path)
      ) {
        continue;
      }
      throw error;
    }
  }
  return { checked, byID };
}

function loadValidated(root, indexPath, options = {}) {
  const loaded = loadSourceIndex(root, indexPath);
  verifySourceLock(loaded);
  const { checked, byID } = checkedContents(loaded, options);
  return { loaded, checked, byID };
}

function loadCanonicalIndex(root, indexPath) {
  const loaded = loadSourceIndex(root, indexPath);
  verifySourceLock(loaded);
  const checked = [...loaded.contentByID.values()].map((content) => checkContent(loaded.root, content));
  return { loaded, checked };
}

export function planMaterialization(root, indexPath) {
  const { loaded, checked } = loadValidated(root, indexPath, { allowMissingEnabledViewBindings: true });
  return {
    ...sourceSummary(loaded, checked),
    action: 'plan',
    materialized_view_targets: loaded.index.views.filter((view) => view.enabled).map((view) => view.target_path).sort(),
  };
}

function runGit(root, args, label) {
  const result = spawnSync('git', ['-C', root, ...args], { encoding: 'utf8' });
  if (result.error || result.status !== 0) {
    fail('GIT_COMMAND_FAILED', `${label} failed`);
  }
  if (['rm', 'restore', 'add', 'reset', 'read-tree', 'update-index'].includes(args[0])) invalidateTrackedPaths(root);
  return result.stdout;
}

function enabledViewTargets(loaded) {
  return loaded.index.views.filter((view) => view.enabled).map((view) => view.target_path).sort();
}

function assertDisposableWorktree(root, environment, { requireClean = true } = {}) {
  if (environment.AICRM_DEDUP_DISPOSABLE_WORKTREE !== '1') {
    fail('DISPOSABLE_WORKTREE_REQUIRED', 'preparing tracked compatibility views requires AICRM_DEDUP_DISPOSABLE_WORKTREE=1');
  }
  if (runGit(root, ['rev-parse', '--is-inside-work-tree'], 'Git worktree check').trim() !== 'true') {
    fail('DISPOSABLE_WORKTREE_REQUIRED', 'preparing tracked compatibility views requires a Git worktree');
  }
  if (requireClean && runGit(root, ['status', '--porcelain', '--untracked-files=no'], 'Git worktree status').trim() !== '') {
    fail('DIRTY_WORKTREE', 'preparing tracked compatibility views requires a clean tracked worktree');
  }
}

function assertExactStagedRemovals(root, targets) {
  for (const target of targets) {
    if (isTracked(root, target)) fail('TRACKED_TARGET', `disposable materialized view became tracked: ${target}`);
  }
  const expected = targets.map((target) => `D\t${target}`).sort();
  const actual = runGit(root, ['diff', '--cached', '--name-status', '--'], 'Git staged-removal check')
    .trim()
    .split('\n')
    .filter(Boolean)
    .sort();
  if (stableJSON(actual) !== stableJSON(expected)) {
    fail('UNEXPECTED_INDEX_CHANGES', 'disposable worktree index does not contain exactly the declared view removals');
  }
  if (runGit(root, ['diff', '--name-only', '--'], 'Git worktree-diff check').trim() !== '') {
    fail('DIRTY_WORKTREE', 'disposable worktree has unstaged changes while compatibility views are prepared');
  }
}

function restoreTrackedTargets(root, targets) {
  runGit(root, ['restore', '--source=HEAD', '--staged', '--worktree', '--', ...targets], 'restore declared tracked view targets');
}

function restoreAfterSafePrepareFailure(root, targets, originalError) {
  try {
    assertExactStagedRemovals(root, targets);
    for (const target of targets) {
      const absolute = resolveLogicalPath(root, target, `prepare rollback:${target}`);
      if (fs.existsSync(absolute)) {
        fail('PREPARE_RECOVERY_REQUIRED', `preparation left an untracked target at ${target}; preserve it and restore manually`);
      }
    }
    restoreTrackedTargets(root, targets);
  } catch (rollbackError) {
    const detail = rollbackError instanceof Error ? rollbackError.message : String(rollbackError);
    fail('PREPARE_RECOVERY_REQUIRED', `preparation failed (${originalError.message}); no tracked target was restored because safe rollback could not be proven: ${detail}`);
  }
}

export function prepareDisposableMaterialization(root, indexPath, { environment = process.env, faults = {} } = {}) {
  const { loaded, checked, byID } = loadValidated(root, indexPath);
  const targets = enabledViewTargets(loaded);
  if (targets.length === 0) fail('NO_ENABLED_VIEWS', 'disposable preparation requires at least one enabled view');
  assertDisposableWorktree(loaded.root, environment);
  const release = acquireLock(loaded.root);
  try {
    for (const target of targets) {
      if (!isTracked(loaded.root, target)) fail('EXPECTED_TRACKED_TARGET', `disposable preparation expected a tracked source path: ${target}`);
    }
    runGit(loaded.root, ['rm', '--force', '--', ...targets], 'remove declared tracked view targets');
    try {
      const applied = applyLoadedMaterialization(loaded, checked, byID, faults);
      return { ...applied, action: 'prepare-disposable', materialized_view_targets: targets };
    } catch (error) {
      restoreAfterSafePrepareFailure(loaded.root, targets, error);
      throw error;
    }
  } finally {
    release();
  }
}

export function restoreDisposableMaterialization(root, indexPath, { environment = process.env } = {}) {
  const { loaded, checked, byID } = loadValidated(root, indexPath, { allowMissingEnabledViewBindings: true });
  const targets = enabledViewTargets(loaded);
  if (targets.length === 0) fail('NO_ENABLED_VIEWS', 'disposable restoration requires at least one enabled view');
  assertDisposableWorktree(loaded.root, environment, { requireClean: false });
  const release = acquireLock(loaded.root);
  try {
    assertExactStagedRemovals(loaded.root, targets);
    const cleaned = cleanLoadedMaterialization(loaded, checked, byID);
    restoreTrackedTargets(loaded.root, targets);
    return { ...cleaned, action: 'restore-disposable', restored_view_targets: targets };
  } finally {
    release();
  }
}

// The disposable proof is allowed to have exactly the selected source views
// staged as deletions. This verifier is intentionally separate from normal
// prepare: freeze gates can call it to prove that those are the only index
// changes and that every replacement is receipted and byte-identical.
export function verifyDisposableMaterialization(root, indexPath, { environment = process.env } = {}) {
  const { loaded } = loadValidated(root, indexPath, { allowMissingEnabledViewBindings: true });
  assertDisposableWorktree(loaded.root, environment, { requireClean: false });
  const release = acquireLock(loaded.root);
  try {
    const targets = enabledViewTargets(loaded);
    assertExactStagedRemovals(loaded.root, targets);
    const verified = verifyMaterialization(loaded.root, indexPath);
    return { ...verified, action: 'verify-disposable', materialized_view_targets: targets };
  } finally {
    release();
  }
}

function assertWritableTarget(root, loaded, receipt, view, content) {
  const absolute = resolveLogicalPath(root, view.target_path, `view:${view.target_path}`);
  if (isTracked(root, view.target_path)) fail('TRACKED_TARGET', `refusing to overwrite tracked path: ${view.target_path}`);
  const known = receipt.targets.find((record) => record.target_path === view.target_path);
  if (!fs.existsSync(absolute)) {
    if (known) fail('MISSING_GENERATED_TARGET', `receipt target is missing: ${view.target_path}`);
    return { absolute, existing: false };
  }
  const actual = readRegularFile(absolute, `view:${view.target_path}`);
  if (!known || !matchesReceipt(known, content)) fail('DIRTY_TARGET', `target exists but is not a generated view: ${view.target_path}`);
  if (actual.bytes.byteLength !== content.bytes || sha256(actual.bytes) !== content.content_sha256 || actual.mode !== content.mode) {
    fail('DIRTY_TARGET', `generated target was modified: ${view.target_path}`);
  }
  return { absolute, existing: true };
}

function writeReceipt(root, loaded, absolute, targets) {
  const receipt = {
    schema_version: RECEIPT_SCHEMA_VERSION,
    index_sha256: indexDigest(loaded),
    targets: [...targets].sort((left, right) => left.target_path.localeCompare(right.target_path)),
  };
  writeAtomically(root, absolute, Buffer.from(`${JSON.stringify(receipt, null, 2)}\n`), '100644', { replaceOwnedFile: true });
}

function rollbackCreatedViews(created) {
  const failures = [];
  for (const item of [...created].reverse()) {
    try {
      const actual = readRegularFile(item.absolute, `rollback:${item.view.target_path}`);
      if (actual.bytes.byteLength !== item.content.bytes || sha256(actual.bytes) !== item.content.content_sha256 || actual.mode !== item.content.mode) {
        throw new Error('target changed after materialization');
      }
      fs.rmSync(item.absolute);
    } catch (error) {
      failures.push(`${item.view.target_path}: ${error.message}`);
    }
  }
  if (failures.length > 0) {
    fail('ROLLBACK_FAILED', `unable to remove every view created by the failed materialization: ${failures.join('; ')}`);
  }
}

function applyLoadedMaterialization(loaded, checked, byID, faults = {}) {
  const enabled = loaded.index.views.filter((view) => view.enabled);
  if (enabled.length === 0) return { ...sourceSummary(loaded, checked), action: 'apply', created: [], reused: [], tracked_views: [], no_enabled_views: true };
  const created = [];
  const receiptState = loadReceipt(loaded.root);
  validateReceipt(loaded, receiptState.receipt);
  assertReceiptCoversMaterializedViews(loaded, receiptState.receipt, { allowEmpty: true });
  const materialized = enabled.filter((view) => !isTracked(loaded.root, view.target_path));
  const prepared = materialized.map((view) => ({ view, content: byID.get(view.content_id), ...assertWritableTarget(loaded.root, loaded, receiptState.receipt, view, byID.get(view.content_id)) }));
  try {
    for (const item of prepared) {
      if (item.existing) continue;
      writeAtomically(loaded.root, item.absolute, item.content.file_bytes, item.content.mode);
      created.push(item);
    }
    if (typeof faults.beforeReceiptWrite === 'function') faults.beforeReceiptWrite();
    const receiptWriter = faults.writeReceipt ?? writeReceipt;
    if (materialized.length > 0) {
      receiptWriter(loaded.root, loaded, receiptState.absolute, materialized.map((view) => receiptRecord(view, byID.get(view.content_id))));
    }
  } catch (error) {
    rollbackCreatedViews(created);
    throw error;
  }
  return {
    ...sourceSummary(loaded, checked), action: 'apply',
    created: created.map((item) => item.view.target_path).sort(),
    reused: prepared.filter((item) => item.existing).map((item) => item.view.target_path).sort(),
    tracked_views: trackedViewTargets(loaded),
  };
}

export function applyMaterialization(root, indexPath, faults = {}) {
  const { loaded, checked, byID } = loadValidated(root, indexPath, { allowMissingEnabledViewBindings: true });
  const release = acquireLock(loaded.root);
  try {
    return applyLoadedMaterialization(loaded, checked, byID, faults);
  } finally {
    release();
  }
}

export function verifyMaterialization(root, indexPath) {
  const { loaded, checked, byID } = loadValidated(root, indexPath, { allowMissingEnabledViewBindings: true });
  const receiptState = loadReceipt(loaded.root);
  validateReceipt(loaded, receiptState.receipt);
  assertReceiptCoversMaterializedViews(loaded, receiptState.receipt);
  for (const record of receiptState.receipt.targets) {
    if (isTracked(loaded.root, record.target_path)) fail('TRACKED_TARGET', `materialized view became tracked: ${record.target_path}`);
    const content = byID.get(record.content_id);
    const absolute = resolveLogicalPath(loaded.root, record.target_path, `receipt:${record.target_path}`);
    const actual = readRegularFile(absolute, `receipt:${record.target_path}`);
    if (actual.bytes.byteLength !== content.bytes || sha256(actual.bytes) !== content.content_sha256 || actual.mode !== content.mode) {
      fail('DIRTY_TARGET', `materialized view drifted: ${record.target_path}`);
    }
  }
  return {
    ...sourceSummary(loaded, checked), action: 'verify',
    materialized_views_verified: receiptState.receipt.targets.map((record) => record.target_path).sort(),
    tracked_views_verified: trackedViewTargets(loaded),
  };
}

function cleanLoadedMaterialization(loaded, checked, byID) {
  const receiptState = loadReceipt(loaded.root);
  validateReceipt(loaded, receiptState.receipt);
  assertReceiptCoversMaterializedViews(loaded, receiptState.receipt);
  for (const record of receiptState.receipt.targets) {
    if (isTracked(loaded.root, record.target_path)) fail('TRACKED_TARGET', `refusing to clean a materialized view that became tracked: ${record.target_path}`);
    const content = byID.get(record.content_id);
    const absolute = resolveLogicalPath(loaded.root, record.target_path, `receipt:${record.target_path}`);
    const actual = readRegularFile(absolute, `receipt:${record.target_path}`);
    if (actual.bytes.byteLength !== content.bytes || sha256(actual.bytes) !== content.content_sha256 || actual.mode !== content.mode) {
      fail('DIRTY_TARGET', `refusing to clean a modified generated view: ${record.target_path}`);
    }
  }
  const removed = [];
  for (const record of receiptState.receipt.targets) {
    const absolute = resolveLogicalPath(loaded.root, record.target_path, `receipt:${record.target_path}`);
    fs.rmSync(absolute);
    removed.push(record.target_path);
  }
  if (fs.existsSync(receiptState.absolute)) fs.rmSync(receiptState.absolute);
  return { ...sourceSummary(loaded, checked), action: 'clean', removed: removed.sort(), tracked_views: trackedViewTargets(loaded) };
}

export function cleanMaterialization(root, indexPath) {
  const { loaded, checked, byID } = loadValidated(root, indexPath, { allowMissingEnabledViewBindings: true });
  const release = acquireLock(loaded.root);
  try {
    return cleanLoadedMaterialization(loaded, checked, byID);
  } finally {
    release();
  }
}

// If a process dies after clean removed some views but before it removes the
// receipt, this explicit repair recreates only receipt-listed files that are
// absent. It never overwrites a present file: every present target must still
// match the receipt exactly before any missing target is restored.
export function recoverPartialMaterialization(root, indexPath) {
  const { loaded, checked, byID } = loadValidated(root, indexPath, { allowMissingEnabledViewBindings: true });
  const release = acquireLock(loaded.root);
  try {
    const receiptState = loadReceipt(loaded.root);
    validateReceipt(loaded, receiptState.receipt);
    assertReceiptCoversMaterializedViews(loaded, receiptState.receipt);
    const missing = [];
    for (const record of receiptState.receipt.targets) {
      if (isTracked(loaded.root, record.target_path)) {
        fail('TRACKED_TARGET', `refusing to recover a materialized view that became tracked: ${record.target_path}`);
      }
      const content = byID.get(record.content_id);
      const absolute = resolveLogicalPath(loaded.root, record.target_path, `receipt:${record.target_path}`);
      if (!fs.existsSync(absolute)) {
        missing.push({ record, content, absolute });
        continue;
      }
      const actual = readRegularFile(absolute, `receipt:${record.target_path}`);
      if (actual.bytes.byteLength !== content.bytes || sha256(actual.bytes) !== content.content_sha256 || actual.mode !== content.mode) {
        fail('DIRTY_TARGET', `refusing to recover over a modified generated view: ${record.target_path}`);
      }
    }
    const restored = [];
    try {
      for (const item of missing) {
        writeAtomically(loaded.root, item.absolute, item.content.file_bytes, item.content.mode);
        restored.push({ view: { target_path: item.record.target_path }, content: item.content, absolute: item.absolute });
      }
    } catch (error) {
      rollbackCreatedViews(restored);
      throw error;
    }
    return {
      ...sourceSummary(loaded, checked),
      action: 'recover-partial',
      restored: restored.map((item) => item.view.target_path).sort(),
    };
  } finally {
    release();
  }
}

// A reviewed source update can replace a canonical content identity while its
// declared logical targets remain the same. Normal clean rejects that receipt.
// This explicit recovery validates the current index/library and deletes only
// exact old receipt bytes; user changes and later-tracked paths remain intact.
export function cleanStaleMaterialization(root, indexPath) {
  const { loaded, checked } = loadCanonicalIndex(root, indexPath);
  const release = acquireLock(loaded.root);
  try {
    const receiptState = loadReceipt(loaded.root);
    const staleTargets = validateStaleReceiptForCleanup(loaded, receiptState.receipt);
    const prepared = staleTargets.map((record) => {
      if (isTracked(loaded.root, record.target_path)) {
        fail('TRACKED_TARGET', `refusing to clean a stale materialized view that became tracked: ${record.target_path}`);
      }
      const absolute = resolveLogicalPath(loaded.root, record.target_path, `receipt:${record.target_path}`);
      const actual = readRegularFile(absolute, `receipt:${record.target_path}`);
      if (actual.bytes.byteLength !== record.bytes || sha256(actual.bytes) !== record.content_sha256 || actual.mode !== record.mode) {
        fail('DIRTY_TARGET', `refusing to clean a modified stale generated view: ${record.target_path}`);
      }
      return { record, absolute };
    });
    for (const item of prepared) fs.rmSync(item.absolute);
    if (fs.existsSync(receiptState.absolute)) fs.rmSync(receiptState.absolute);
    return {
      ...sourceSummary(loaded, checked),
      action: 'clean-stale',
      prior_index_sha256: receiptState.receipt.index_sha256 ?? null,
      removed: prepared.map((item) => item.record.target_path).sort(),
    };
  } finally {
    release();
  }
}
