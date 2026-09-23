import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const DIST = path.join(ROOT, "dist");
const manifest = JSON.parse(
  fs.readFileSync(path.join(DIST, "asset-manifest.json"), "utf8"),
);

function fail(message) {
  throw new Error(`performance-budget: ${message}`);
}

function staticGraph(root) {
  const visited = new Set();
  const visit = (file) => {
    if (visited.has(file)) return;
    const metadata = manifest.files[file];
    if (!metadata) fail(`manifest import missing: ${file}`);
    visited.add(file);
    for (const item of metadata.imports) {
      if (item.kind !== "dynamic-import") visit(item.path);
    }
  };
  visit(root);
  return visited;
}

function entryForSource(source) {
  const match = Object.entries(manifest.files).find(
    ([, metadata]) => metadata.entry_point === source,
  );
  if (!match) fail(`entry source missing: ${source}`);
  return match[0];
}

function assertHTMLAssets() {
  const known = new Set(Object.keys(manifest.files));
  const htmlFiles = [];
  const walk = (directory) => {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const target = path.join(directory, entry.name);
      if (entry.isDirectory()) walk(target);
      else if (entry.name.endsWith(".html")) htmlFiles.push(target);
    }
  };
  walk(DIST);
  for (const file of htmlFiles) {
    const html = fs.readFileSync(file, "utf8");
    if (/<script(?![^>]*type="module")[^>]+src="[^"#]*assets\//i.test(html))
      fail(`${path.relative(DIST, file)} has a non-module local asset`);
    for (const match of html.matchAll(
      /(?:src|href)="([^"?#]*assets\/[^"?#]+)"/g,
    )) {
      const resolved = path
        .relative(DIST, path.resolve(path.dirname(file), match[1]))
        .split(path.sep)
        .join("/");
      if (!known.has(resolved))
        fail(
          `${path.relative(DIST, file)} references unknown asset ${resolved}`,
        );
    }
  }
}

function assertBudget(label, files, maximum) {
  const total = [...files].reduce(
    (sum, file) => sum + manifest.files[file].gzip_bytes,
    0,
  );
  if (total > maximum) fail(`${label} gzip chain ${total} exceeds ${maximum}`);
  console.log(`${label}: ${total} / ${maximum} gzip bytes`);
}

assertHTMLAssets();

const customerGraph = new Set([
  ...staticGraph(manifest.entries.admin),
  ...staticGraph(entryForSource("web/src/admin/pages/customers.ts")),
  ...staticGraph(manifest.entries.tokens),
]);
const customerInputs = [...customerGraph].flatMap(
  (file) => manifest.files[file].inputs,
);
for (const forbidden of [
  "qrcode-generator",
  "read-excel-file",
  "@xmldom",
  "fflate",
  "/sections/qr.ts",
  "History.ts",
]) {
  if (customerInputs.some((input) => input.includes(forbidden)))
    fail(`customer initial graph includes ${forbidden}`);
}
assertBudget("crm-customers", customerGraph, 150 * 1024);

const legacyGraph = new Set([
  ...staticGraph(manifest.entries.admin),
  ...staticGraph(entryForSource("web/src/admin/legacy.ts")),
  ...staticGraph(manifest.entries.tokens),
]);
const legacyInputs = [...legacyGraph].flatMap(
  (file) => manifest.files[file].inputs,
);
for (const deferred of [
  "qrcode-generator",
  "read-excel-file",
  "@xmldom",
  "fflate",
  "/sections/qr.ts",
  "/ownerReassignmentFile.ts",
]) {
  if (legacyInputs.some((input) => input.includes(deferred)))
    fail(`CRM legacy initial graph eagerly includes ${deferred}`);
}
assertBudget("crm-legacy-shell", legacyGraph, 150 * 1024);

const sidebarGraph = new Set([
  ...staticGraph(manifest.entries.sidebar),
  ...staticGraph(manifest.entries.sidebarStyles),
  ...staticGraph(manifest.entries.tokens),
]);
const sidebarInputs = [...sidebarGraph].flatMap(
  (file) => manifest.files[file].inputs,
);
for (const deferred of [
  "p4-sidebar-activity",
  "p4-sidebar-questionnaires",
  "p4-sidebar-orders",
  "p4-sidebar-materials",
  "p4-sidebar-send",
]) {
  if (sidebarInputs.some((input) => input.includes(deferred)))
    fail(`sidebar profile graph eagerly includes ${deferred}`);
}
for (const tabModule of [
  "questionnaires",
  "timeline",
  "chat",
  "orders",
  "periodic-orders",
  "products",
  "materials",
]) {
  const entry = entryForSource(`web/src/sidebar/tabs/${tabModule}.ts`);
  if (sidebarGraph.has(entry))
    fail(`sidebar profile graph eagerly includes tab module ${tabModule}`);
}
assertBudget("sidebar-profile", sidebarGraph, 100 * 1024);

console.log("performance-budget: PASS");
