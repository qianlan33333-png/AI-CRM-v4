# CRM v4 Three-member Aggregate Candidate Implementation Plan

**Goal:** Build one auditable draft PR containing the exact runtime source commits from PRs #15, #3, and #13 plus the final PR #19 bridge governance commit, then pass protected `check` and complete CI on the aggregate head.

**Architecture:** Start from the live protected `main` SHA in a clean worktree and merge each immutable source head with ordinary `--no-ff` merge commits. Keep the three runtime members distinct from the PR #19 governance source; the PR19 bridge binds both sets of provenance without changing any source branch.

**Tech Stack:** Git, GitHub protected branch checks, Go, Node.js, PostgreSQL 16-backed CI, Chromium browser journeys, existing `release_batch.py` and PR19 bridge manifests.

---

## Step 1: Freeze source identities

**Output:** Current main and four source PR head/tree pairs match GitHub live refs.

**Verify:** `git ls-remote origin refs/heads/main refs/heads/codex/v4-ci-baseline-consolidated refs/heads/codex/alipay-checkout-options-v4 refs/heads/codex/v4-group-invite-direct-qr refs/heads/codex/first-v4-batch-bridge`; verify PR #15/#3/#13/#19 `headRefOid` and base SHA through `gh pr view`.

## Step 2: Assemble the aggregate history

**Output:** Four normal merge commits on `codex/v4-three-member-aggregate-candidate`; all four exact source heads remain ancestors and source refs stay unchanged.

**Verify:** Run `git merge-base --is-ancestor <source-head> HEAD` for each source, compare `git show <source-head>^{tree}` with its frozen tree, then inspect `git status --short --branch` and all merge commit parents. Resolve any conflict only in the aggregate worktree and record it in the PRD.

## Step 3: Run local preflight

**Output:** Fast preflight and applicable compile/focused checks match the clean aggregate tree.

**Verify:** Run `python3 scripts/dev_preflight.py fast`, `python3 scripts/dev_preflight.py compile`, the affected Go package tests, and `git diff --check`. Any failed case is fixed in a new aggregate commit and requires repeating the affected checks.

## Step 4: Publish a draft review unit

**Output:** One GitHub Draft PR from the aggregate branch to `main`; it lists exact source provenance, bridge fields, and remaining staging/production gates.

**Verify:** Confirm the PR is draft, current head/tree match the worktree, live main is unchanged, the aggregate contains all required source ancestors, and PR #15/#3/#13/#19 remote heads are unchanged.

## Step 5: Verify the protected check and complete CI

**Output:** The aggregate PR's required `check` succeeds and one `workflow_dispatch` full run is bound to the same exact head.

**Verify:** Run `gh workflow run ci.yml --ref codex/v4-three-member-aggregate-candidate -f force_full=true`; verify `plan`, `governance`, `preflight`, `backend`, `frontend`, `browser`, `archive-sdk`, and `check` all conclude `success` on the aggregate SHA. Skipped, cancelled, stale-head, or unknown lanes fail this step. The `deploy` job remains skipped because deployment is outside this task.

## Step 6: Persist and deliver the development checkpoint

**Output:** A durable event is stored before notifying the release coordinator; the coordinator receives the Draft PR, exact head/tree, member list, full CI run ID, protected check result, and the still-open staging/observation gates.

**Verify:** Use the unique release-control `state.json` and explicit `--state` on every `release_events.py` command. Do not submit/adopt the batch, consume the first-v4 bridge, merge, install, or deploy.
