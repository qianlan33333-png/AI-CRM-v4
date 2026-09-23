# PR-4 exact compatibility-view removal

Status: implementation contract for the approved P4 deletion. This document
covers source topology only; it does not change any legacy behavior contract,
API, persistence, identity boundary, Provider integration, installer input, or
release artifact format.

## Classification

- OneID / external identity: not involved. The change reads canonical source
  bytes and materializes compatibility views only.
- Persistence / jobs / Provider effects: not involved. It creates no database
  record, job, effect, runtime dependency, or external request.

## Exact removal set

`web/donor-sources/source-index.json` remains the sole machine-readable list.
It declares 74 canonical contents, 230 logical bindings, and 229 enabled view
targets. P4 removes precisely the 229 enabled targets from Git, including the
Go `//go:embed` OpenAPI and webshell CSS compatibility files. It retains only
`api/openapi.yaml` as a tracked, active V3 canonical binding. Every removed
binding has `current_path_state=untracked_post_p4`; the retained OpenAPI
binding alone remains `tracked_pre_p4_removal` because it is not a view.

The root `.gitignore` has one marked block. Its 229 root-relative view entries
are sorted byte-for-byte from the source index, followed only by the exact
receipt and lock locations owned by the materializer. It contains no glob and
no broad `web/**`, donor-directory, or canonical-source exclusion.
`scripts/check-donor-source-view-ignore.mjs` fails if the index state, ignore
block, or Git ignore behavior differ, or if an unlisted `web/src` path is
ignored.

## Consumer transition

P3's disposable removal was transition evidence. P4 makes its result the
normal build state:

1. Validate index, lock, source commit/blob/hash/mode and exact ignore list.
2. Materialize only absent, untracked enabled targets and publish the receipt
   atomically under the existing lock.
3. Run the pre-existing freeze gates, frontend/Host builds, Go consumers and
   release staging against those untracked files.
4. `verify` compares the receipt to the complete enabled untracked target set.
   `clean` or recovery only deletes receipt-listed files after bytes, mode and
   Git-index status are revalidated.

CI's early consumer step now invokes `scripts/run-donor-view-consumers.sh
check` directly; it no longer stages a disposable Git deletion. The Make Go
entrypoints and direct Go/release scripts run the same safe preparation. The
installer remains artifact-only and does not need Node, Git, a donor checkout,
or a source materializer.

A developer uses unchanged frozen npm commands only after the explicit safe
preparation described in `source-governance.md`. This preserves the PR01
freeze on `package.json` and `web/scripts/build.mjs`.

## Required evidence and rollback

P4 approval requires: the exact 229 staged deletions; no tracked enabled view;
all 74 canonical authorities and the sole active OpenAPI authority verified;
exact-ignore validation; materialization/receipt verification; the frozen
consumer closures; direct Go embed preparation; frontend/Host/release staging;
and a final clean that removes only generated views and the receipt.

If rollback is needed, clean only a verified receipt, then restore the same
source-index-listed targets and their matching pre-P4 binding states in one
reviewed change. Never restore a broad donor directory, remove a source
library, or suppress an index/receipt error. PR-5 remains separate: it
recomputes the exact-duplicate audit after these removals and adds prevention
against new duplicates or unauthorized authority changes.
