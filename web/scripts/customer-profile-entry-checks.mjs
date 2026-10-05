// Focused release entry: these browser contracts consume actual built HTML
// and tag-picker assets, which a clean candidate checkout does not contain.
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const repository = fileURLToPath(new URL("../../", import.meta.url));
const run = (command, args) => execFileSync(command, args, { cwd: repository, stdio: "inherit" });
run(process.execPath, ["web/scripts/build.mjs"]);
run(process.execPath, ["scripts/build-v3-host-adapters.mjs"]);
for (const contract of [
  "internal/webshell/static/admin_console/admin_customers.test.mjs",
  "web/v3/customerAdapter.test.mjs",
  "web/v3/orderAdapter.test.mjs",
  "web/v3/navigationHost.test.mjs",
  "web/scripts/hxc-history-e2e.mjs",
  "web/scripts/e2e.mjs",
]) run(process.execPath, [contract, ...(contract === 'web/scripts/e2e.mjs' ? ['--customer-profile-links'] : [])]);
console.log("customer-profile-entry-checks: PASS");
