import assert from "node:assert/strict";
import test from "node:test";
import {
  chromiumBinaryCandidates,
  resolveChromiumBinary,
} from "./chromium_binary.mjs";

test("Chromium candidates put explicit overrides before platform defaults", () => {
  assert.deepEqual(
    chromiumBinaryCandidates({ AICRM_CHROMIUM_BINARY: "/opt/chrome", CHROME_BIN: "/usr/bin/chrome" }, "linux"),
    ["/opt/chrome", "/usr/bin/chrome", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"],
  );
  assert.deepEqual(chromiumBinaryCandidates({ AICRM_CHROMIUM_BINARY: "/opt/chrome", CHROME_BIN: "/opt/chrome" }, "linux"), [
    "/opt/chrome", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
  ]);
  const attempted = [];
  assert.equal(
    resolveChromiumBinary({
      env: { AICRM_CHROMIUM_BINARY: "/opt/chrome", CHROME_BIN: "/usr/bin/chrome" },
      platform: "linux",
      spawn: candidate => {
        attempted.push(candidate);
        return { status: candidate === "/opt/chrome" ? 0 : 1 };
      },
    }),
    "/opt/chrome",
  );
  assert.deepEqual(attempted, ["/opt/chrome"]);
});

test("Chromium honors CHROME_BIN when the AICRM override is unset", () => {
  const attempted = [];
  assert.equal(
    resolveChromiumBinary({
      env: {},
      platform: "linux",
      spawn: (candidate, args) => {
        attempted.push([candidate, args]);
        return { status: candidate === "google-chrome" ? 0 : 1 };
      },
    }),
    "google-chrome",
  );
  assert.deepEqual(attempted, [["google-chrome", ["--version"]]]);
  attempted.length = 0;
  assert.equal(
    resolveChromiumBinary({
      env: { CHROME_BIN: "/usr/bin/google-chrome" },
      platform: "linux",
      spawn: (candidate, args) => {
        attempted.push([candidate, args]);
        return { status: candidate === "/usr/bin/google-chrome" ? 0 : 1 };
      },
    }),
    "/usr/bin/google-chrome",
  );
  assert.deepEqual(attempted, [["/usr/bin/google-chrome", ["--version"]]]);
});

test("Chromium keeps the macOS fallback and fails clearly when none is available", () => {
  assert.equal(
    chromiumBinaryCandidates({}, "darwin")[0],
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  );
  assert.throws(
    () => resolveChromiumBinary({ env: {}, platform: "linux", spawn: () => ({ status: 1 }) }),
    /Chromium binary is unavailable/,
  );
});
