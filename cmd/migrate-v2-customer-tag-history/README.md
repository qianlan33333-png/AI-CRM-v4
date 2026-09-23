# v2 customer-tag history import

`migrate-v2-customer-tag-history` is an operator-run, one-way importer for
already-recorded v2 `external_effect_job` facts. It writes only immutable
Customer-owned history receipts. It never creates a current tag command,
External Effect record, River job, or Provider call.

The importer uses OneID's scoped `wecom_external_userid` resolver and stable
Tag provider-to-local mappings. Resolve never provisions a customer. A source
row therefore has exactly one recorded outcome: `imported`, `pending`,
`conflict`, `excluded`, or `failed`.

## Source contract

The frozen v2 donor is `aicrm_next/crm/customer_tags/live_mutation.py` at
`dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`, and the source table contract is
`migrations/versions/0039_external_effect_queue.py` at the same revision.
Extraction opens the supplied PostgreSQL connection in one `REPEATABLE READ,
READ ONLY` transaction and runs this projection, ordered by source ID:

```sql
SELECT id, effect_type, operation, target_id, actor_id, payload_json, status,
       created_at, executed_at AS completed_at
FROM external_effect_job
WHERE effect_type IN ('wecom.contact.tag.mark', 'wecom.contact.tag.unmark')
  AND operation IN ('tag_mark', 'tag_unmark')
ORDER BY id;
```

`external_effect_job.executed_at` is exported as the receipt's
`completed_at`; `completed_at` on the old schema belongs to
`external_effect_attempt`, not the job. The source `status` is preserved
unchanged. `actor_id` may be empty and is the v2 caller/admin field, not the
tagging employee. The employee is `payload_json.follow_user_userid`; the
extract validates `payload_json.external_userid == target_id`. Missing
follow-user or unresolved staff is `pending`; an inconsistent payload target
is `conflict`. Historical inactive staff is retained as provenance.

## Commands

Put the read-only v2 database URL in a regular, non-symlink file with exactly
mode `0600`. Do not pass it directly on the command line or put it in logs.
The output snapshot path must not already exist; the CLI creates it at `0600`.

```sh
install -m 600 /dev/null /secure/aicrm/v2-readonly-db-url
# Write the read-only v2 PostgreSQL URL into that file outside command history.

go run ./cmd/migrate-v2-customer-tag-history \
  --mode=extract \
  --source-database-url-file=/secure/aicrm/v2-readonly-db-url \
  --snapshot=/secure/aicrm/customer-tag-history-20260906.json \
  --wecom-corp-id=CORP_ID

go run ./cmd/migrate-v2-customer-tag-history \
  --mode=inspect \
  --snapshot=/secure/aicrm/customer-tag-history-20260906.json

go run ./cmd/migrate-v2-customer-tag-history \
  --mode=dry-run \
  --snapshot=/secure/aicrm/customer-tag-history-20260906.json

shasum -a 256 /secure/aicrm/customer-tag-history-20260906.json

go run ./cmd/migrate-v2-customer-tag-history \
  --mode=apply \
  --snapshot=/secure/aicrm/customer-tag-history-20260906.json \
  --manifest-sha256=EXACT_SHA256 \
  --confirm-apply

go run ./cmd/migrate-v2-customer-tag-history \
  --mode=verify \
  --snapshot=/secure/aicrm/customer-tag-history-20260906.json
```

`apply` requires both the exact snapshot digest and `--confirm-apply`.
Overlapping captures replay an existing source ID only when its immutable
source digest is identical; drift is rejected. The command reports only
counts, timestamps, and zero-effect counters. It never prints database URLs,
external user IDs, staff IDs, provider tag IDs, or source payloads.
