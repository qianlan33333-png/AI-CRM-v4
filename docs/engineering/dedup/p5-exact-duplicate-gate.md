# P5 exact-duplicate injection gate

`python3 scripts/audit/check_new_exact_duplicates.py REPO --base BASE_COMMIT --head HEAD_COMMIT` compares two pinned Git commits through `scan_exact_duplicates.py`. `BASE_COMMIT` must be an ancestor of `HEAD_COMMIT`.

The gate reads all tracked blob paths without extension or directory exclusions. It rejects every head path that newly makes an exact duplicate group or joins an existing group, even if the change removes a larger old duplicate group. Regular files and symlinks are grouped separately; binary blobs, empty files and nonstandard paths stay in scope. Incomplete blob reads, LFS pointers and submodules fail closed.

A temporary exception is optional and must be a JSON file with `schema_version: 1`. Every entry contains exactly one repository-relative `path`, its lowercase `content_sha256`, and a nonempty `reason`. Directory rules, globs and stale exceptions are rejected. The command prints the pinned commits and any explicitly exempted paths as JSON; it does not alter the repository.

CI invokes this gate together with the source-authority base-diff gate through `scripts/audit/check-dedup-base-diff.sh`. The wrapper resolves pinned base/head commits, including a parent fallback for a manually dispatched main run; it does not inspect working-tree bytes.
