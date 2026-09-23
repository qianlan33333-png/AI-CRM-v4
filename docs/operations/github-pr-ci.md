# GitHub PR CI

GitHub-hosted PR checks keep the required `check` job name stable. PR runs test code and retain CI evidence; they do not create or transfer a production release package.

The main CI workflow has no production deployment job. Existing manual data-import and governance workflows remain separate from PR checks.

## Test selection

- Documentation-only Markdown changes skip application lanes; the governance check still runs.
- A registered medium-risk change runs the quick preflight plus only the lanes and tests listed for affected capabilities in `docs/governance/capability-impact.json`. Go checks run the named test in its package, browser checks run the named Chromium journey, and frontend checks run the mapped script.
- Unknown mappings, high-risk changes, shared infrastructure, CI/deploy rules, API contracts, dependencies, composition, and migrations run every verification lane.
- Changed migration SQL also passes a static safety check before the workflow plan runs. It rejects common destructive statements: dropping persisted objects or columns, renaming a table or column, truncating a table, deleting rows, and an `UPDATE` without `WHERE`. Replace these with a forward-compatible expand/backfill/contract migration. This check catches common hazards; it does not prove that every schema or application change is backward compatible.

The `check` job fails if a selected lane fails, is cancelled, or runs when it was not selected. A missing or invalid registry mapping selects every lane; if a valid check cannot be narrowed safely, that lane runs in full. Capability registry edits are protected and always use full verification.
