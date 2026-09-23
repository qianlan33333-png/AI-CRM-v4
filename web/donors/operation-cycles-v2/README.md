# Raw PR08 operation-cycle donor

Files under `src/` are byte-exact snapshots from the frozen v2 donor. They
are evidence only and are not part of a v3 frontend build. The templates are
kept unchanged, including the run dossier's raw audience, target-count,
send-time, delivery, and recipient-oriented labels; those fields must not be
bound to customer, OneID, order, or entitlement data by a v3 adapter.

There is no standalone cycles-specific TypeScript or CSS module in the frozen
donor. The shared controller/API/runtime files are mixed with excluded
customer, audience, campaign, and recipient branches, so they remain deferred
to Terra for a narrowed adapter.

Terra/Web must mount the allowlisted cycle templates through the v3-owned
PR10 webshell and primary sidebar. Do not deploy this directory as a complete
v2 shell, introduce a second navigation sidebar, or modify donor files to fit
the shell.
