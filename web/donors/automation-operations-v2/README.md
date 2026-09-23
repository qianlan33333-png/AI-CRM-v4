# Raw Automation Operations donor

Files under `src/` are byte-exact snapshots from the frozen v2 donor at
`6bfbe5816bb89913c70adaca87d6a486260e016e`. They are evidence inputs, not a
runtime dependency and not a second deployable admin shell.

The active donor surface is the audience-package workspace: groups, package
lifecycle, fixed templates, closed segment configuration, preview,
materialization, Agent binding, sender selection, and member display. The
visible broadcast control is deliberately blocked in the donor because that
API has no safe run/effect contract.

Do not edit these files. Mount their business templates through a v3-owned
adapter in the existing webshell. Do not import the donor `legacy.ts` or build
its full navigation shell. Production data must come from v3 APIs; the frozen
Mock/sessionStorage paths are characterization evidence only.
