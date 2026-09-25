import assert from "node:assert/strict";
import test from "node:test";
import {
  dataWorkspaceChromiumCandidates,
  resolveDataWorkspaceChromiumBinary,
} from "./data_workspace_chromium_binary.mjs";

test("data workspace Chromium honors explicit binary overrides first", () => {
  assert.deepEqual(
    dataWorkspaceChromiumCandidates({ AICRM_CHROMIUM_BINARY: "/opt/chrome", CHROME_BIN: "/usr/bin/chrome" }, "linux"),
    ["/opt/chrome", "/usr/bin/chrome", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"],
  );
  assert.equal(
    resolveDataWorkspaceChromiumBinary({
      env: { AICRM_CHROMIUM_BINARY: "/opt/chrome", CHROME_BIN: "/usr/bin/chrome" },
      platform: "linux",
      spawn: candidate => ({ status: candidate === "/opt/chrome" ? 0 : 1 }),
    }),
    "/opt/chrome",
  );
});

test("data workspace Chromium selects the Chrome binary installed by CI", () => {
  const attempted = [];
  assert.equal(
    resolveDataWorkspaceChromiumBinary({
      env: {},
      platform: "linux",
      spawn: candidate => {
        attempted.push(candidate);
        return { status: candidate === "google-chrome" ? 0 : 1 };
      },
    }),
    "google-chrome",
  );
  assert.deepEqual(attempted, ["google-chrome"]);
});

test("data workspace Chromium retains the macOS fallback and fails clearly", () => {
  assert.equal(
    dataWorkspaceChromiumCandidates({}, "darwin")[0],
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  );
  assert.throws(
    () => resolveDataWorkspaceChromiumBinary({ env: {}, platform: "linux", spawn: () => ({ status: 1 }) }),
    /Chromium binary is unavailable/,
  );
});
