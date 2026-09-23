# OperationCycle local Runner installation contract

This package is not enabled by the CRM web process and does not select a machine,
workspace, Codex socket, strategy, or action. An operator installs it only after
the designated machine, HTTPS CRM endpoint, exact Codex version, managed app-server
socket, and local binding directories have been reviewed.

The normal release contains these two reviewed artifacts:

```sh
bin/aicrm-operation-cycle-runner
bin/aicrm-operation-cycle-result
```

CI currently builds the release for Linux amd64. The generated
`release-files.sha256` covers both files before they enter the release tar. Do
not install these Linux artifacts on a machine with another operating system or
architecture. Once the actual target is known, use the same reviewed Go build
flow to produce and checksum artifacts for that target.

Extract the exact approved release into a SHA-versioned staging directory and
verify its complete manifest before selecting any binary:

```sh
reviewed_release=/absolute/path/to/extracted/release-SHA
(cd "$reviewed_release" && sha256sum --strict --check release-files.sha256)
test -x "$reviewed_release/bin/aicrm-operation-cycle-runner"
test -x "$reviewed_release/bin/aicrm-operation-cycle-result"
```

Record the release SHA and the current installed-version link. Stop the old
client through its existing supervisor, copy both verified files into a new
SHA-versioned directory on the target machine, and verify their manifest hashes
again. Atomically replace the installed-version link only after both files are
present. Put that link's `bin` directory on the local task PATH; the artifact
names are referenced verbatim in generated prompts. Start the client through
the same existing supervisor and verify its reported Codex version, socket, and
heartbeat before treating the version as active.

If startup or heartbeat validation fails, stop that client, atomically restore
the recorded previous-version link, and start the previous client through the
same supervisor. Keep the failed release directory for digest inspection until
the change is reconciled. These steps version and roll back files only; they do
not choose a target machine or introduce a service manager. Until a target and
its existing supervisor are explicitly approved, the release artifacts are
packaged but not installed or enabled.

A service manager starts `aicrm-operation-cycle-runner` with explicit absolute
paths, a registered runner id, an HTTPS CRM URL, and each reviewed binding:

```sh
exec aicrm-operation-cycle-runner \
  --crm-url "$AICRM_OPERATION_RUNNER_URL" \
  --service-token-env AICRM_OPERATION_RUNNER_SERVICE_TOKEN \
  --runner-id "$AICRM_OPERATION_RUNNER_ID" \
  --codex-binary "$AICRM_CODEX_BINARY" \
  --codex-socket "$AICRM_CODEX_APP_SERVER_SOCKET" \
  --codex-version "$AICRM_CODEX_EXPECTED_VERSION" \
  --control-socket "$AICRM_OPERATION_RUNNER_CONTROL_SOCKET" \
  --renewal-interval 25s \
  --binding excel_workspace="$AICRM_EXCEL_WORKSPACE"
```

The service token is read only from the named protected service environment. The
command does not print the token, endpoint credentials, local bindings, prompts,
or completion payload. Never put a token in arguments, a unit file, a result
file, or a runner heartbeat.

At startup, the connector verifies the absolute Codex binary's exact version,
that the app-server path is a connectable Unix socket, then completes the
`initialize` request followed by its `initialized` notification before it
heartbeats or claims. It then heartbeats, claims at most one action, and renews its
fenced 60-second lease every 25 seconds while the local control socket is alive.
`SIGTERM` or `SIGINT` closes that socket and stops the process; a restart can
reclaim only its own expired action. A recovered action missing either thread or
turn binding records `start_outcome_unknown` for manual verification and never
starts a replacement task.

The generated task prompt names the frozen objective, instructions, context
hashes, and approved local bindings. After human review of a sanitized aggregate
result, the local task invokes:

```sh
aicrm-operation-cycle-result --socket "/the-reviewed/control/socket" \
  --request-id REQUEST_ID --result-file /absolute/path/to/safe-result.json
```

The result command talks only to the local socket. It does not contact CRM
directly. The socket accepts only the action currently held by this process and
records a fenced terminal event through the runner. A missing 0104 execution
snapshot is terminally marked `missing_execution_snapshot` with manual review;
it is never reconstructed from a later strategy or run version.

No deployment action, real Codex thread/turn, or customer effect is part of this
runbook. Validate a target installation first with the controlled HTTP and Unix
socket tests in this repository, then use the explicit production change process.
