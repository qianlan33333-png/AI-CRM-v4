# Audience rule activation on target, 2026-09-11

User scope: move the previously active audience rules and enable native calculation now; do not copy old audience member associations, and defer traffic cutover.

## Boundaries

Identity: use canonical Customer/Identity Owner Ports and current trustworthy facts. Missing or ambiguous identity evidence does not become a negative membership match. Persistence: native Segment configuration, CAS, idempotency receipts and existing jobqueue. No automation bindings, sender sets or outbound messages were created.

## Production evidence

Target 124.220.53.183 runs `75708d027e95affa2f9595fc7e3b9d4ae5a0a0fd`; release archive SHA256 `d32dbd3eb69e0d8f053712a6702649ef762cea21b1965f5e3668f1c269505ae6`. Protected target backup completed before migrations through 0139. API/effects/excel service states preserved; readiness confirmed. DNS, traffic and source writes unchanged.

Normal authenticated API readback at about 14:23 Asia/Shanghai:

| Source rule | New package | State | Schedule | Published count |
|---|---|---|---|---|
|30|15|active|every 3 minutes|0|
|37|16|active|every 3 minutes|0|
|14|17|active|every 3 minutes|221|
|38|18|active|every 3 minutes|1|
|28|19|active|daily 02:00 Asia/Shanghai|0|
|31|20|active|daily 02:00 Asia/Shanghai|0|

All six have actual published member snapshots and no sending bindings. Rule 28 corrected refresh 220 published snapshot 43; original failed/retrying refresh 203 remains audit evidence and is not used as completion proof. Rule 31 refresh 221 published snapshot 44. Counts reflect current target facts, not source population equivalence or full cutover readiness. Paid audiences still depend on target active follow relationships; never fabricate these or copy old members to force nonzero counts.

HXC full refresh 74 succeeded for 2,925 records. Full group Provider read succeeded with 122 external members, 10 lacking canonical mapping. Provider completeness is independent of canonical completeness: candidate membership compares unique active verified same-corporation identities against the complete group identity hash set. Unknown candidates remain excluded. No implicit customers were created.

The six detail pages `/admin/automation-conversion/packages/{15..20}` returned HTTP 200 under normal admin authentication and included the independent rule activation host. Runtime configuration was read back via the normal Owner API. This is not a claim of manual browser testing.

## Validation

`make test arch fmt-check vet` passed (1,157 Go files checked by architecture gate). Real PostgreSQL/race coverage verified group candidate membership and specified-owner transaction reuse. The activation host test exercised the frozen list controller, detail activation, CAS/CSRF/replay, and independent send restrictions. Release frontend staging and 364 file checksums passed.

Protected operational journals remain under `/var/backups/aicrm/cutover-prep-20260911/` on target. Preserve them for idempotent recovery. Full business data migration, final delta synchronization and domain cutover are separate pending work.
