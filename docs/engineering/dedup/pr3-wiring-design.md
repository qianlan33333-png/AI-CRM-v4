# PR-3 build-time source-view wiring design

Status: PR-3 implementation contract. Every derived path remains tracked until
PR-4 approves exact removal. PR-3 has two deliberately different modes: normal
safe preparation verifies the tracked bytes without modifying Git, while a
separate disposable transition proof stages exactly the declared deletions and
materializes them. The index declares 229 derived paths from 74 canonical
contents; it does not alter installer inputs.

## Scope and invariant

Every preparation command first runs the PR-2 index/lock/canonical-byte
verification. Normal preparation verifies a derived target while it remains
tracked in PR-3, and after an approved PR-4 removal materializes only the
corresponding declared untracked target before verifying its receipt. It never
removes or restores a tracked path. A build fails for a missing index-tracked
target, dirty generated target, stale receipt, path outside the whitelist, or
different source identity. The dedicated disposable proof alone stages exactly
the declared targets as deletions, materializes their receipts, and proves no
consumer can fall back to a tracked copy.

The three P0 consumer closures are separate because their language and source
contracts differ.

| Closure | Canonical authority | Derived build-time views | Required consumers and proof |
| --- | --- | --- | --- |
| `v2_frozen_web_views` | Exact paths under `web/donor-sources/v2-6bfbe5816bb89913c70adaca87d6a486260e016e/` are bound to frozen V2 commit `6bfbe5816bb89913c70adaca87d6a486260e016e`. | All 226 P0-audited V2 logical paths under `web/src/**` and `web/donors/<module>-v2/src/**`. | The seven module freeze gates, PR01 full source-set gate, `web/scripts/build.mjs`, `scripts/build-v3-host-adapters.mjs`, CI release staging, and their current behavior/Host checks. |
| `openapi_go_embed_view` | `api/openapi.yaml` remains the active V3 contract source. | `internal/config/http/openapi.yaml`, strictly package-local for `//go:embed`. | Generator keeps reading `api/openapi.yaml`; `Makefile`, direct CI/release Go commands, the config HTTP byte-comparison test, authenticated download, and the documented bare-Go preparation command. |
| `ai_webshell_static_view` | The frozen DD8 CSS in `web/donor-sources/production-dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/static/send_content_readonly_detail.css`. | The declared AI donor view and `internal/webshell/static/admin_console/send_content_readonly_detail.css`. | Direct Go embed paths, `internal/webshell/renderer.go`, public URL/MIME test, `scripts/build-v3-host-adapters.mjs`, and release staging. |

The source index must use the exact source commits already audited in P0; a
same-looking payload from a different valid commit is not an acceptable
substitute. Each migration keeps the original donor ledger, file count, hash
comparison, and independent test context.

## Transition proof before PR-4 removal

PR-3 will add a dedicated disposable-worktree proof command, not a broad copy
or ignore rule. For each selected closure it will:

1. Start from a clean checkout and verify the immutable source index.
2. Remove only the selected logical targets from that disposable worktree's
   Git index and working tree. The exact removal set must be supplied by the
   source index and asserted before materialization.
3. Materialize the corresponding targets at their original logical paths,
   verify the receipt, and prove that `git ls-files` does not contain any of
   those paths.
4. Run every consumer listed in the table against that worktree. No command may
   read a remaining tracked copy as a fallback.
5. Validate the staged release manifest and built `web/dist`; then clean only
   the receipted views and discard the disposable worktree.

This gives PR-3 a clean-checkout no-fallback proof while PR-4 still owns the
reviewed committed deletions. PR-3 must not delete the tracked payloads or
change a frozen file's bytes.

## Entry-point order

The PR-3 implementation will add one explicit preparation command before each
actual consumer family, not only `npm run build`:

- Module freeze gates and frontend/Host builds first run normal safe preparation for the V2 closure. The early CI proof additionally runs those same gates after exact disposable removal.
- `make vet`, `make test`, `make build`, `make run`, and `make radar-check`,
  direct CI Go commands, and the release binary build prepare the OpenAPI and
  AI webshell closures before any `go` invocation. A bare `go test` or
  `go build` remains supported only after the documented explicit preparation
  command; `go:generate` is not an implicit prerequisite.
- The release workflow prepares views inside its build workspace before
  compiling or generating `web/dist`, then checks the release manifest. The
  production installer receives only the finished binaries, migrations,
  `web/dist`, and release manifest. It will not run Node, Git, a donor checkout,
  or any materializer.

## Evidence required to approve PR-3

- A clean disposable-worktree run for each selected closure, with the exact
  target list removed from the Git index before materialization.
- Original SHA-256, Git blob identity, mode, ledger count, and freeze gate
  results for every logical path.
- Frontend typecheck/build, Host adapter build, relevant shell/asset assertions,
  direct Go vet/test/race/build where the two Go closures are selected, and
  release-stage manifest verification.
- Negative cases for tracked target fallback, missing target, stale receipt,
  wrong source version, and a build invoked without required preparation.
- No production installer or deployment change.

After PR-4 removes a group, P5 must rerun exact duplicate scanning before
reviewing remaining near-duplicate candidates. It will exclude the eliminated
whole-file-copy groups from the C-layer queue, then assess only independent
residual candidates by domain ownership, protocol, and test context; matching
blocks alone do not authorize a merge.
