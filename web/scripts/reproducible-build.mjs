import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = path.dirname(path.dirname(path.dirname(fileURLToPath(import.meta.url))));
const manifest = path.join(root, "web/dist/asset-manifest.json");
const first = fs.readFileSync(manifest, "utf8");
const result = spawnSync(process.execPath, ["web/scripts/build.mjs"], { cwd: root, encoding: "utf8" });
if (result.status !== 0) throw new Error(result.stderr || result.stdout || "second build failed");
const second = fs.readFileSync(manifest, "utf8");
if (first !== second) throw new Error("reproducible-build: identical source produced a different manifest");
console.log("reproducible-build: PASS");
