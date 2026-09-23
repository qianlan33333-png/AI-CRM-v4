import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { gzipSync } from "node:zlib";

const [distArgument, expectedSHA] = process.argv.slice(2);
const fail = (message) => {
  throw new Error(`validate-web-release: ${message}`);
};
if (!distArgument || !path.isAbsolute(distArgument) || !/^[0-9a-f]{40}$/.test(expectedSHA || "")) {
  fail("usage: node validate-release.mjs <absolute-dist-directory> <40-char-source-sha>");
}
const dist = fs.realpathSync(distArgument);
const manifestPath = path.join(dist, "asset-manifest.json");
if (!fs.statSync(manifestPath, { throwIfNoEntry: false })?.isFile()) fail("asset-manifest.json is missing");
const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
if (manifest.source_sha !== expectedSHA || manifest.version !== 1) fail("manifest source SHA or version does not match");
const rootIndex = fs.readFileSync(path.join(dist, "index.html"), "utf8");
if (!rootIndex.includes('content="0; url=/login?next=%2Fadmin"') || !rootIndex.includes('href="/login?next=%2Fadmin"')) {
  fail("root entry must authenticate before entering the admin application");
}
if (rootIndex.includes("AI-CRM 全新前端") || rootIndex.includes("管理后台 · 全部页面")) {
  fail("development page index must not be published as the application root");
}
for (const tool of ["node", "npm", "esbuild", "orval"]) {
  if (typeof manifest.tools?.[tool] !== "string" || !manifest.tools[tool]) fail(`tool version is missing: ${tool}`);
}
const known = new Set(Object.keys(manifest.files || {}));
const actualAssets = new Set();
const actualReleaseFiles = new Set();
const collectAssets = (directory) => {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const target = path.join(directory, entry.name);
    if (entry.isSymbolicLink()) fail(`release contains a symbolic link: ${path.relative(dist, target)}`);
    if (entry.isDirectory()) collectAssets(target);
    else if (entry.isFile()) {
      const relative = path.relative(dist, target).split(path.sep).join("/");
      if (relative !== "asset-manifest.json") actualReleaseFiles.add(relative);
      if (relative.startsWith("assets/")) actualAssets.add(relative);
    }
  }
};
collectAssets(dist);
if (actualAssets.size !== known.size || [...actualAssets].some((relative) => !known.has(relative))) {
  fail("assets directory does not exactly match the manifest");
}
const knownReleaseFiles = new Set(Object.keys(manifest.release_files || {}));
if (actualReleaseFiles.size !== knownReleaseFiles.size || [...actualReleaseFiles].some((relative) => !knownReleaseFiles.has(relative))) {
  fail("release directory does not exactly match the manifest");
}
for (const [relative, metadata] of Object.entries(manifest.release_files || {})) {
  if (!relative || relative.startsWith("/") || relative.includes("..") || relative === "asset-manifest.json") fail(`unsafe release path: ${relative}`);
  const contents = fs.readFileSync(path.join(dist, relative));
  const digest = crypto.createHash("sha256").update(contents).digest("hex");
  if (digest !== metadata.sha256 || contents.byteLength !== metadata.bytes || gzipSync(contents, { level: 9 }).byteLength !== metadata.gzip_bytes) {
    fail(`release file metadata mismatch: ${relative}`);
  }
}
for (const [relative, metadata] of Object.entries(manifest.files || {})) {
  if (!relative.startsWith("assets/") || relative.includes("..")) fail(`unsafe asset path: ${relative}`);
  const file = path.join(dist, relative);
  const contents = fs.readFileSync(file);
  const digest = crypto.createHash("sha256").update(contents).digest("hex");
  if (digest !== metadata.sha256 || contents.byteLength !== metadata.bytes || gzipSync(contents, { level: 9 }).byteLength !== metadata.gzip_bytes) {
    fail(`asset metadata mismatch: ${relative}`);
  }
  for (const imported of metadata.imports || []) {
    if (!known.has(imported.path)) fail(`asset import is missing: ${relative} -> ${imported.path}`);
  }
}
for (const entry of ["admin", "h5", "sidebar", "memberGridShare", "tokens", "labs"]) {
  if (!known.has(manifest.entries?.[entry])) fail(`entry is missing: ${entry}`);
}

const htmlFiles = [];
const walk = (directory) => {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const target = path.join(directory, entry.name);
    if (entry.isDirectory() && entry.name !== "assets") walk(target);
    else if (entry.isFile() && entry.name.endsWith(".html")) htmlFiles.push(target);
  }
};
walk(dist);
for (const file of htmlFiles) {
  const html = fs.readFileSync(file, "utf8");
  for (const match of html.matchAll(/(?:src|href)="([^"?#]*assets\/[^"?#]+)"/g)) {
    const relative = path.relative(dist, path.resolve(path.dirname(file), match[1])).split(path.sep).join("/");
    if (!known.has(relative)) fail(`${path.relative(dist, file)} references missing asset ${relative}`);
  }
}
console.log("validate-web-release: PASS");
