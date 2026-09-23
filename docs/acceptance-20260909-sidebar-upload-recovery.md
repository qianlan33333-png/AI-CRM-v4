# Sidebar image upload recovery — 2026-09-09

OneID: reads canonical customer through existing sidebar context; no identity changes. Diagnostic image uploads do not involve customer identity.
Persistence: Provider write through Outbound and existing External Effects. Owner completion, reconciliation receipt, audit, and outbox share the PostgreSQL Unit of Work. No new queue, worker, identity matcher, or provider writer.

## Defect

An attempted upload with an explicit provider rejection was treated as outcome_unknown and discarded its safe error code, permanently blocking the image. Upload uses the application credential that signs the sidebar SDK; token and application read checks succeeded in production. The original attempt has no recoverable provider response, so its outcome is not retroactively claimed to be failure or success.

## Change

- Distinguish official numeric rejection from interrupted or invalid responses; retain only closed failure categories, numeric provider code and HTTP status.
- Encode multipart media filename and file length explicitly; never follow upload redirects or automatically repeat the request.
- Allow an operator to explicitly abandon an unconfirmed temporary upload with evidence and actor audit via the existing EER reconciliation port, atomically updating its owner. Preserve the original unknown event and never mark it sent.
- A new preparation is permitted only after that explicit reconciliation. Cached successful receipts remain reused.

## Acceptance

Pending: isolated diagnostic upload, original material preparation recovery and provider media receipt. Chat delivery still requires the sidebar SDK and an employee send action; preparing media alone is not sending.
