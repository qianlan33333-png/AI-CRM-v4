# PR-1 duplicate-source baseline coverage

Target commit: `5291366b9742030957f48ebf7464a040a3ab46db`
Target tree: `f7fb3e57accfa4c4afce08e6546fdd10661dbd7c`
Historical seed baseline: `05045c645f95d269b624771ceb215713e3300f59`
Historical tree: `0747d8a4165283fe15e5b7caa5663015eec2c41d`

## Classification

- OneID / external identity: not involved; this audit reads only Git objects.
- Persistence / internal tasks / provider effects: not involved; no database, queue, provider or build command ran.
- PR-1 action: audit only. No tracked source was removed, linked, imported, generated or rewritten.

## Required PR sequence

| Stage | Status in this branch | Gate before the next stage |
|---|---|---|
| P0 / PR-1 baseline audit | This branch only | Full object coverage, seed verification, candidate/consumer/owner records; no unknown deletion target |
| P1 / PR-2 source mechanism and pilot | Not started | Owner-approved immutable source, bindings and materialization negative cases |
| P2 / PR-3 consumer/build wiring | Not started | Clean-checkout relevant entrypoints and frozen logical-file verification |
| P3 / PR-4 approved payload removal | Not started | Explicit per-group approval plus pre/post behavior and freeze evidence |
| P4 / PR-5 prevention/final audit | Not started | Injection gate, exception review and final before/after ledger |

## Layer A: exact Git-object inventory

- Target tracked entries: **1937**; blob paths verified: **1937/1937**.
- Target unique blob objects: **1781/1781**.
- Target exact groups: **74**; paths in groups: **230**; additional logical path bytes: **10,234,871**.
- Baseline tracked entries: **1817**; blob paths verified: **1817/1817**.
- Baseline exact groups: **74**; paths in groups: **230**; additional logical path bytes: **10,234,871**.
- Read errors: target `0`, baseline `0`. Target LFS pointers `0`, submodules `0`.

### Target top-level coverage

| Root | Entries | Blobs | Verified blobs |
|---|---:|---:|---:|
| `(root files)` | 12 | 12 | 12 |
| `.github` | 5 | 5 | 5 |
| `acceptance` | 1 | 1 | 1 |
| `api` | 1 | 1 | 1 |
| `cmd` | 155 | 155 | 155 |
| `deploy` | 22 | 22 | 22 |
| `docs` | 129 | 129 | 129 |
| `internal` | 939 | 939 | 939 |
| `journeys` | 5 | 5 | 5 |
| `migrations` | 101 | 101 | 101 |
| `modules` | 1 | 1 | 1 |
| `scripts` | 57 | 57 | 57 |
| `skills` | 1 | 1 | 1 |
| `web` | 508 | 508 | 508 |

## Seed evidence

- 05045 seed groups verified exactly: **39/39**.
- Those seed groups unchanged at target: **39/39**.
- Each group/path/size result is in `exact-duplicates.json`; this is a revalidation, not deletion authorization.

## Layers B and C: candidate-only analysis

- Mechanical candidates: **0**. Only line-ending and trailing-space/tab normalization were applied; comments, headers, encoding, modes and behavior remain meaningful.
- Lexical shared-block candidates: **411** across **1533** bounded unique UTF-8 source objects.
- C-layer exclusions: {"binary_or_non_utf8": 6, "fewer_than_window_lines": 25, "over_bounded_bytes": 4, "unsupported_suffix": 213}. This is not parser, AST, type, data-flow or semantic analysis.

## Consumer and decision closure

- Static target paths mapped: **497**; readable text blobs scanned: **1931**.
- Exact decisions: **74**, all retain every path in PR-1. Each has an immutable-source or active-contract canonical plan, a source/version/hash binding per current path, a reusable consumer closure and explicit P2 entrypoints; no deletion is approved.
- Consumer closures: **3** reusable families. The static map retains both resolved literals and ambiguous lexical evidence; dynamic imports, shell expansion, runtime routing, generated assets and release staging are specifically deferred to the named P2 clean-checkout entrypoints, never treated as absent consumers.

## Artifacts

- `inventory.json`: all target tracked entries, including binary, empty files, modes and special-entry status.
- `exact-duplicates.json`: all exact groups and 39-seed verification across both pinned commits.
- `near-duplicate-candidates.json`: B/C candidate methods, results and explicit limits.
- `dependency-map.json`: per-target resolved and ambiguous static consumer observations, plus the bounded scanner boundary.
- `dedup-decisions.json`: every exact group has a source-governance owner, concrete canonical/source-version binding, reusable consumer closure, P2 entrypoints, temporary transition constraint and retain-only action. No group has a permanent exception in PR-1.
- `provenance.json`: direct GitHub clone, pinned commits/trees, object-scan inputs, tool hashes and pre-output checkout state.

**Closure state: PR-1 records a concrete non-destructive source and consumer route for every exact group. It does not materialize a view, rewire a consumer, run a repository build or approve deletion. P1/P2 must prove those declared gates from a clean checkout before P3 can remove any tracked payload.**
