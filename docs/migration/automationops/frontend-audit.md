# Automation Operations frozen frontend audit

## Result

Nineteen files required to characterize the active `automation` and
`audienceEdit` pages are archived byte-for-byte from v2 commit
`6bfbe5816bb89913c70adaca87d6a486260e016e`. The same nineteen paths in the
v3 base are also currently byte-identical, but their presence is not an
implemented v3 capability because no v3 Segment owner/API exists yet.

The archive is evidence-only. The active v3 build must use a narrow adapter
outside `web/donors/automation-operations-v2/`; it must not import the broad
donor controller, mock database, or legacy shell.

## Active page observations

- `automation.html`: group filter, package cards, create group, edit group,
  copy, activate/pause, delete/archive, open detail, and a visible broadcast
  action whose active controller deliberately blocks execution.
- `audienceEdit.html`: basics/template, Agent binding, senders, members, and a
  sending-boundary explanation. Configuration preview/materialization are
  local audience operations, not external sends.
- `controller.ts`: active page orchestration and the authoritative blocked
  broadcast message.
- `api/admin.ts` plus generated `p4-ai-audience`: donor API/DTO evidence. These
  mixed files contain unrelated domains and are never imported wholesale.
- `mockData.ts` and `client.ts`: characterization only; production must not use
  their sessionStorage or hard-coded audience members.

## Shell and extension rule

The templates contain business content only. Donor `legacy.ts` and
`build.mjs` are deliberately excluded because they create the complete v2
shell. New preview-confirm-run and result/reconciliation controls are v3
extensions and must be mounted in documented extension points without editing
the frozen templates.

Run:

```sh
AICRM_AUTOMATIONOPS_DONOR_DIR=/path/to/AI-CRM-v2 \
  bash scripts/check-automationops-donor-manifest.sh
```
