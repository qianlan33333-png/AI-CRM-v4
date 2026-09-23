import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { chromiumStartupDiagnostic, chromiumStartupTimeoutMS } from "./chromium_launch.mjs";

test("reports a bounded, redacted early Chromium exit", () => {
  const profile="/tmp/aicrm-owner-handoff-chromium-fixture";
  const diagnostic=chromiumStartupDiagnostic({profile, exitCode:23, stderr:`fatal profile=${profile}\n`});
  assert.match(diagnostic, /exited before remote debugging/);
  assert.match(diagnostic, /exit_code=23/);
  assert.match(diagnostic, /<profile>/);
  assert.doesNotMatch(diagnostic, new RegExp(profile.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
});

test("keeps a live Chromium startup failure bounded and distinct", () => {
  const diagnostic=chromiumStartupDiagnostic({stderr:"still initializing"});
  assert.equal(chromiumStartupTimeoutMS, 30_000);
  assert.match(diagnostic, /within 30000ms/);
  assert.match(diagnostic, /process=still_running/);
});

test("keeps a caller-provided startup budget accurate", () => {
  const diagnostic = chromiumStartupDiagnostic({ stderr: "still initializing", timeoutMS: 8_000 });
  assert.match(diagnostic, /within 8000ms/);
  assert.match(diagnostic, /process=still_running/);
});

test("reports spawn failures without raw error data", () => {
  const diagnostic=chromiumStartupDiagnostic({launchError:{code:"EAGAIN", message:"secret should not render"}});
  assert.match(diagnostic, /category=EAGAIN/);
  assert.doesNotMatch(diagnostic, /secret/);
});

test("bounds and redacts stderr for a signaled Chromium exit", () => {
  const profile = "/tmp/aicrm-access-chromium-sensitive-profile";
  const diagnostic = chromiumStartupDiagnostic({
    profile,
    signalCode: "SIGKILL",
    stderr: `${"x".repeat(640)} control=\u0000 profile=${profile} url=https://diagnostic.example/token`,
  });
  const renderedStderr = /stderr=(.*)\)$/.exec(diagnostic)?.[1] || "";
  assert.match(diagnostic, /signal=SIGKILL/);
  assert.match(diagnostic, /<profile>/);
  assert.match(diagnostic, /<url>/);
  assert.equal(diagnostic.includes("\u0000"), false);
  assert.doesNotMatch(diagnostic, new RegExp(profile.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
  assert.doesNotMatch(diagnostic, /diagnostic\.example/);
  assert.ok(renderedStderr.length <= 320);
});

test("Access Chromium journey reports a redacted early browser exit", async () => {
  const fixture = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-access-chromium-launch-"));
  const browser = path.join(fixture, "fake-chromium");
  const screenshots = path.join(fixture, "screenshots");
  const journey = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../cmd/aicrm/access_governance_ui_chromium_journey.mjs");
  await fs.mkdir(screenshots);
  await fs.writeFile(browser, [
    "#!/bin/sh",
    'if [ "$1" = "--version" ]; then exit 0; fi',
    'for argument in "$@"; do',
    '  case "$argument" in --user-data-dir=*) profile=$(printf "%s" "$argument" | sed "s/^--user-data-dir=//") ;; esac',
    "done",
    'printf "fatal profile=%s url=https://diagnostic.example/token\\n" "$profile" >&2',
    "exit 23",
    "",
  ].join("\n"));
  await fs.chmod(browser, 0o700);
  try {
    const outcome = await new Promise((resolve, reject) => {
      let stderr = "";
      const child = spawn(process.execPath, [journey], {
        env: {
          ...process.env,
          AICRM_CHROMIUM_BINARY: browser,
          AICRM_ACCESS_UI_TEST_URL: "https://127.0.0.1:1",
          AICRM_ACCESS_UI_SCREENSHOT_DIR: screenshots,
          AICRM_ACCESS_UI_SUPER_USERNAME: "fixture-super",
          AICRM_ACCESS_UI_SUPER_PASSWORD: "fixture-super-password",
          AICRM_ACCESS_UI_ADMIN_USERNAME: "fixture-admin",
          AICRM_ACCESS_UI_ADMIN_PASSWORD: "fixture-admin-password",
          AICRM_ACCESS_UI_VIEWER_USERNAME: "fixture-viewer",
          AICRM_ACCESS_UI_VIEWER_PASSWORD: "fixture-viewer-password",
        },
        stdio: ["ignore", "ignore", "pipe"],
      });
      const timer = setTimeout(() => { child.kill("SIGKILL"); reject(new Error("Access Chromium launch fixture timed out")); }, 5_000);
      child.stderr.on("data", (chunk) => { stderr += String(chunk); });
      child.once("error", (error) => { clearTimeout(timer); reject(error); });
      child.once("close", (code, signal) => { clearTimeout(timer); resolve({ code, signal, stderr }); });
    });
    assert.equal(outcome.code, 1);
    assert.equal(outcome.signal, null);
    assert.match(outcome.stderr, /Chromium exited before remote debugging/);
    assert.match(outcome.stderr, /exit_code=23/);
    assert.match(outcome.stderr, /<profile>/);
    assert.doesNotMatch(outcome.stderr, /diagnostic\.example/);
    assert.doesNotMatch(outcome.stderr, new RegExp(fixture.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
  } finally {
    await fs.rm(fixture, { recursive: true, force: true });
  }
});
