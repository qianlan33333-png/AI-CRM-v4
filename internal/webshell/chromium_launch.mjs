// Browser startup has a separate bounded budget from journey assertions. These
// diagnostics never include test credentials, request bodies, or provider data.
export const chromiumStartupTimeoutMS = 30_000;

const safeErrorCategory = error => String(error?.code || error?.name || "unknown_error")
  .replace(/[^A-Za-z0-9_.-]/g, "_").slice(0, 96);

const boundedStderr = (stderr, profile) => {
  const rendered = String(stderr || "").replaceAll(String(profile || ""), "<profile>")
    .replace(/https?:\/\/\S+/g, "<url>").replace(/[\u0000-\u001f\u007f]+/g, " ")
    .replace(/\s+/g, " ").trim();
  return rendered ? rendered.slice(-320) : "none";
};

// chromiumStartupDiagnostic distinguishes a failed child process from a live
// process whose DevTools endpoint exceeded the startup budget. It is exported
// for a no-browser contract test so CI diagnosis cannot silently regress.
export const chromiumStartupDiagnostic = ({ profile, exitCode = null, signalCode = null, launchError, stderr, timeoutMS = chromiumStartupTimeoutMS } = {}) => {
  const safeStderr = boundedStderr(stderr, profile);
  const safeTimeoutMS = Number.isSafeInteger(timeoutMS) && timeoutMS > 0 ? timeoutMS : chromiumStartupTimeoutMS;
  if (launchError) return `Chromium launch failed before remote debugging (category=${safeErrorCategory(launchError)} stderr=${safeStderr})`;
  if (exitCode !== null || signalCode) return `Chromium exited before remote debugging (exit_code=${exitCode ?? "none"} signal=${signalCode || "none"} stderr=${safeStderr})`;
  return `Chromium remote debugging did not become ready within ${safeTimeoutMS}ms (process=still_running stderr=${safeStderr})`;
};
