# Raw PR07 Automation Agent donor

Files under `src/` are byte-exact snapshots from the frozen v2 donor. They
are not part of the v3 frontend build and must not be edited. The list/editor
pages preserve the donor Agent and fixed-script UI, including raw references
to audience binding and activate/precheck operations; PR07 backend routes do
not expose those excluded capabilities.

The donor's effective local contract is paused-only configuration: Agent and
fixed-script types, draft/published role and task prompts, fixed-script text,
opaque legacy configuration, copy/publish/archive, and idempotent local
receipts. The generated fixed-content media arrays are `maxItems: 0`, and the
v3 preparation validator rejects non-empty media references fail-closed. The
precheck operation is read-only; activation, execution, recipient selection,
audience binding, and provider/LLM effects are excluded.

Terra/Web must mount a narrow v3-owned adapter through PR10's
`internal/webshell/admin_base` and primary sidebar. Do not deploy this donor
as a complete v2 shell, introduce a second `.side` navigation, or modify the
donor files to fit the shell.
