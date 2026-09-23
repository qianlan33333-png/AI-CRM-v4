# Duplicate-source governance

This document is the repository-maintained execution contract for
`PRD-ENG-DEDUP-001`. It is deliberately shorter than the approved attachment:
it records only the mechanics, invariants, gates, and rollback rules that later
changes must preserve.

## Scope and provenance

- Approved PRD attachment SHA-256: `f210ceb92e2ff711b95a7cf937f482dd6ae7b392fa1ed76e72b3522b416f3be7` (`/Users/qianlan/Downloads/2.md` when this contract was adopted).
- Approved seed-audit attachment SHA-256: `e0886cde7ab7deb7a7508beb8d10aff753f9afb482bb63128821ba4aedabf9ee` (`/Users/qianlan/Downloads/3.md`).
- P0 object baseline: `05045c645f95d269b624771ceb215713e3300f59`.
- P0 complete-tree audit target: `5291366b9742030957f48ebf7464a040a3ab46db`; see `docs/engineering/dedup/pr1-baseline/`.
- This work concerns Git-tracked source and build metadata only. It does not change OneID, persistence, jobs, Provider effects, API behavior, database migrations, or legacy runtime dependencies.

## Source and view invariants

1. A canonical payload has one declared authority. `authority_kind=frozen_donor` payloads live beneath `web/donor-sources/`; `authority_kind=active_v3_contract` may remain at its reviewed V3 source path when it is the authority (currently `api/openapi.yaml`). `immutable=true` means the currently reviewed repository/commit/blob/hash tuple is locked, not that an active V3 contract can never change. Its library records repository, reviewed commit, path, Git blob SHA-1, SHA-256, bytes, and mode. Verification recomputes both hashes from the canonical bytes; a syntactically valid but different source commit/blob is rejected.
2. `web/donor-sources/source-index.json` binds every logical path to a canonical content ID. Bindings retain their module, logical path, source repository/commit/path/blob, usage, freeze gate, ledger, and mode.
3. `web/donor-sources/source-lock.json` repeats the immutable content identities. The verifier rejects an index/lock mismatch. P5 compares pinned Git base/head objects for every index, lock and canonical payload path; a coordinated edit cannot be treated as an ordinary consumer change. Any nonempty authority diff requires one complete base-bound SHA-256 record in `p5-authority-change-approvals.json`, which is then subject to ordinary PR review. Its `review_reference` is only an auditable declaration, never independent proof of human approval.
4. Frozen consumers may never depend on mutable `web/src` content as their authority. If an active V3 behavior diverges, it must become an explicit V3 adapter or derived source with its own Owner and tests; it cannot edit a generated compatibility view.
5. Compatibility views are normal untracked files, copied only from a whitelist in the index. They are never symlinks or hard links. The materializer rejects `..`, absolute/backslash paths, symlink ancestors, duplicate targets, canonical targets, tracked targets, unknown targets, dirty targets, a missing index-tracked target, missing sources, stale receipts, and a held lock.
6. A receipt at `.aicrm-dedup/donor-views-receipt.json` records only files created by this tool. It is keyed by the source-index digest and canonical payload hashes. Cleanup verifies every recorded target before deleting only those paths; it never traverses a directory or cleans an unlisted file.
7. Writes use a same-directory temporary file and atomic publication. A view is published exclusively without overwriting a target that appeared after validation; the tool-owned receipt is atomically renamed while the lock is held. If the receipt write fails, the tool removes only views created in that invocation after rechecking their bytes and mode; it preserves any target that changed concurrently and reports rollback failure rather than deleting it. The prior receipt remains unchanged.
8. The lock directory contains a private owner record with host, PID, random lock ID, and creation time. Normal `apply` and `clean` never remove a pre-existing lock. `recover-lock` is an explicit operator action: it only removes a lock whose well-formed owner record is on this host and whose PID returns `ESRCH`; active, permission-denied, remote-host, missing, malformed, or changed owner records require manual inspection. It never performs automatic stale-lock cleanup.

PR-2 used `health.schemas.ts` as its mechanism pilot. PR-3 declared every P0-derived build view: 229 paths backed by 74 canonical contents and proved their consumers in a disposable worktree. PR-4 removes those 229 declared paths from Git and changes exactly their bindings to `untracked_post_p4`; the sole retained binding is the active authority `api/openapi.yaml`, which is never a view. The root `.gitignore` block enumerates every generated target and the two tool-owned receipt/lock paths explicitly. It contains no wildcard or directory-level `web/**` rule. `scripts/check-donor-source-view-ignore.mjs` compares that list to the source index and rejects an ignored unlisted source path.

## Commands and phase rules

Read-only source validation:

```sh
node scripts/verify-donor-sources.mjs
node scripts/materialize-donor-views.mjs --mode plan
node scripts/materialize-donor-views.mjs --mode recover-lock  # only after local dead-PID proof
```

The materializer has `plan`, `apply`, `verify`, `clean`, `clean-stale`, and
explicit `recover-lock` modes. `scripts/recover-donor-source-views.mjs` performs
receipt-scoped interrupted-clean recovery. PR-3 also added `prepare-disposable`
and `restore-disposable`, which require `AICRM_DEDUP_DISPOSABLE_WORKTREE=1`;
they remain historical transition-proof tools and are not part of normal P4
builds or package-script hooks.

`Makefile`, release builders, and direct build scripts automatically run the
non-destructive preparation command. It verifies the tracked derived bytes in
PR-3 and, after an approved PR-4 deletion, materializes only the declared
untracked views. It does not require a clean working tree and it never stages,
removes, or restores a tracked path. A bare `go` command remains an explicit
opt-in through this safe helper:

```sh
make check
scripts/build-linux.sh amd64
scripts/run-go-with-donor-views.sh go test ./cmd/aicrm
```

For a normal front-end command in a fresh P4 checkout, first prepare the exact
views. The root npm execution toolchain is V3-owned since the security migration
recorded in `docs/donor-manifests/v3-toolchain-ownership.json`. Its original two
donor files remain preserved under `toolchain-v2` and checked against the original
PR01/PR03 hashes. Only these two execution files leave the donor byte contract;
all donor source, including `web/scripts/build.mjs`, remains frozen. Do not create
implicit source-materialization hooks in the package or build commands:

```sh
node scripts/check-donor-source-view-ignore.mjs
node scripts/prepare-donor-source-views.mjs
npm run typecheck  # or: npm test, npm run build
```

`make`, `scripts/run-go-with-donor-views.sh`, `scripts/build-linux.sh`,
`scripts/build-wecom-archive-sdk-runner-linux.sh`, CI and the release builder
perform the same safe preparation themselves. The production installer still
receives only a built release.

For an approved canonical-source update after PR-4, ordinary `clean` correctly
rejects the stale receipt. Run `clean-stale` only when the current index still
declares every old target and each target still exactly matches the old receipt;
it removes no user edit or tracked file. Then run `apply` with the reviewed new
source identity. If a process is interrupted after `clean` deleted one or more
views but before it removed the receipt, run `node scripts/recover-donor-source-views.mjs`.
It recreates only receipt-listed *missing* paths after every surviving path is
proven byte/mode-identical; it refuses a tracked or changed target, then lets
normal `clean` finish. Before switching branches, restore a disposable
worktree. If a normal worktree contains a stale receipt, use `clean-stale` only
under those same exact-byte conditions; otherwise preserve the files and
recover manually.

The installer continues to consume only validated built binaries, `web/dist`,
and the release manifest; it must not require Node, Git, a donor checkout, or
source-view materialization.

| Phase | Permitted result | Required proof before advancing |
|---|---|---|
| P0 / PR-1 | Inventory, source/consumer decisions, no deletion | Full object scan, seed verification, no unknown target |
| P1 / PR-2 | Canonical library, lock/index, materializer, isolated pilot | Tamper, missing source, wrong source version, path escape, duplicate target, dirty target, lock, idempotence, cleanup and original-byte tests |
| P2 / PR-3 | Consumer/build/release wiring | Clean checkout runs each affected freeze gate, frontend/Host build, Go embed preparation, direct Go paths, CI artifact and release staging without a tracked-copy fallback |
| P3 / PR-4 | Only explicitly approved tracked payload removal | Pre/post hash, freeze and behavior evidence per group; required test contexts remain independent |
| P4 / PR-5 | Prevention and final ledger | Git-object base-diff/injection gates reject a new duplicate, new duplicate path, tracked generated view, unknown canonical payload, or an authority diff lacking a complete base-bound declaration reviewed in the PR |

The original PR07 20-logical-file contract, every donor SHA comparison, test
context, template URL/MIME contract, and OpenAPI/Go-embed preparation remain
mandatory. No later phase may lower a count, replace a hash comparison with a
directory check, or add a broad `web/**` ignore rule.

## P5 audit evidence

After P4 commit `c20eaa03e7c87a990dafa9dd7e1eaa1698c6b1b9`, the complete Git-object rescan read 1,815 paths and blobs with zero exact duplicate groups; its JSON SHA-256 is `4e634edb41a553f8a5083c4f0964f785b24871c16fe4f1402a4e6e657781cd7f`. The bounded lexical candidate report has SHA-256 `a1efccb1e544f2da175490990996fbba7c70c885b74e02f18ef8325b16a8e83e`; its 418 candidate IDs are retained in [p5-near-duplicate-review.md](p5-near-duplicate-review.md), SHA-256 `14432293204d0c4b6de0319281f3e46640288d5826495c0f763d0cbdc28db282`. It authorizes no extraction, deletion, contract merger, or exception. It records bounded lexical evidence only; AST, type, data-flow, runtime and protocol equivalence remain outside its method.

P5 continuously enforces both `scripts/audit/check_new_exact_duplicates.py` and `scripts/audit/check_source_authority_changes.py` through `scripts/audit/check-dedup-base-diff.sh` before CI builds and release-artifact staging. The latter validates the full base/head source snapshots before it evaluates an authority-diff record. The production installer remains source independent.

## Rollback

Each phase is independently revertible. Before reverting a view-producing
change, run `clean` against its receipt; if any generated view is dirty, stop
and preserve the developer change. Reverting a source migration must restore
its canonical entry, source-index binding, lock entry, view manifest, consumer
wiring, and the old logical path together. Do not delete a source library or
receipt by broad glob. After rollback, rerun the prior freeze checks and the
relevant source/view verification.
