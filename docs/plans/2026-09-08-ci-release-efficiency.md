# CI and release efficiency

OneID: not involved; no customer or external identity behavior changes.
Persistence: CI artifacts only; no business transaction or durable job changes.
External Effects: no Provider calls added or changed. HXC activation and business
acceptance use the existing deployment owner only when explicitly requested by
manual dispatch. Ordinary deployment does not run HXC state checks. No new identity
matching, provisioning, business table writes or execution kernel.

## Execution

- Fast formatting, architecture and frozen-source gates run first. Remove the
  separate compile-only full suite: the mandatory backend job compiles all tests.
- Run backend, frontend, mandatory Chromium and SDK jobs concurrently.
  Each database job owns a separate PostgreSQL 16 service; packages stay serial.
- Run the entire Go suite once with race detection and `-count=1`, plus full vet.
  This replaces normal full tests, the hidden radar/composition suite inside the
  frontend builder, and the manually enumerated second race suite.
- Keep frontend regression, source contracts, package-layout checks and automatic
  discovery of mandatory browser cases. A skipped mandatory browser case fails.
- Cache Go dependencies/build products per toolchain, lane, dependency hash and
  commit with a lane restore prefix. No database or mandatory test-result reuse.

## Main to server

After successful full PR verification, `check` records the tested merge commit,
complete Git tree, PR head, run/attempt and all mandatory phase results.
On main, use GitHub APIs to locate the merged PR and successful run from this
repository/workflow. Read only the small JSON evidence artifact, verify its
tested commit/tree and PR parent via GitHub, and require successful jobs in the
same attempt. Only an exactly identical complete tree reuses PR verification.
Squash commit IDs can differ; source contents, including workflow and deploy
scripts, cannot differ. No PR binaries or executable artifacts are imported.

Missing/expired evidence, changed merged content, API errors, direct pushes or
manual `force_full` requests run all verification before deployment. The stable
required `check` rejects missing, failed, cancelled or unexpectedly skipped jobs.

Deployment builds binaries and the final three-stage frontend from the actual
main checkout, verifies the file manifest and package structure, then uses the
existing chunk uploader and installer. It does not repeat PR regression, vet,
typechecks or governance self-tests. Keep host locking, stale release protection,
migration failure rollback, worker readiness, bootstrap and config activation.
The deploy job allows up to three hours for a cross-region link that continues
to upload chunks. Per-chunk timeouts, checksums, installation locking and all
post-upload health gates remain unchanged, so the larger ceiling does not turn
a stalled transfer into an unbounded deployment.
Routine deployment does not invoke HXC business acceptance or automatically
fall back to inspect/apply/scheduled refresh. Keep the existing HXC source
configuration step, but run the full rollout only on an explicit manual
`workflow_dispatch` with `hxc_full_rollout` enabled. First activation and an
incompatible HXC projection rule change require this explicit rollout; ordinary
deployment success establishes the installed release, not HXC business acceptance.
Any incompatible HXC projection semantics must update the owner's RuleVersion,
as required by versioned projections. No new file-based deployment routing or
separate script upload path is introduced.
Replace redundant public health requests with a final bounded readiness check
that must report the expected release SHA. A healthy older version cannot pass.

## Acceptance

Run policy tests (tree drift, wrong run/attempt/repository, missing or skipped
phases, unsafe evidence ZIP, wrong deployed SHA), existing preflight and installer
contracts, workflow syntax validation, then actual PR CI. Observe main selecting
the verified tree path and the final public release SHA. Compare cold and warm
wall times separately; do not claim speedups from local mocks or skipped gates.
