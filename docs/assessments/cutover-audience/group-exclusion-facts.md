# Source rule31: complete group facts and closed exclusion

OneID: resolves existing scoped WeCom identities only; no Provision, links, merges or UnionID facts. Persistence: WeCom-owned local projection and audit in a shared UoW; Provider read outside any transaction. No external write, send, retry queue or old SQL.

Migration0136 precedes use. `GroupMembershipRefresh` invalidates the old result before network I/O; process interruption, Provider failure, unresolved identity or conflict cannot preserve a usable old result. The completion uses observedAt CAS; an older in-flight request cannot overwrite a later refresh. Missing member_list, null, wrong chat, untyped/duplicate IDs fail; an explicitly empty complete member_list is valid. Raw member IDs stay in connector memory; storage contains canonical customer IDs, scope/chat reference and safe summary. Partial facts remain visible as counts but cannot be used for exclusion.

Authenticated manual endpoint: `POST /api/admin/wecom/group-membership/refresh`, JSON `{"chat_reference":"PROTECTED_CONFIGURED_GROUP"}`. Requires normal superadmin session and CSRF, plus existing ChannelProviderReadEnabled; no permission bypass or query-string identity. Returns only completion, observation time and counts. It reads Provider and writes local facts; does not send anything.

Template `member_excluding_group_paid` parameters:
- owner_scope: all; owner_staff_ids: []
- exclude_group_chat: the exact reviewed same-Corp source group reference
- excluded_product_codes: SOAK-FDE-BUNDLE-S1, FDE-CAMP-S1, SOAK-QTR

The closed evaluator selects current proven HXC active memberships with a recognized WeCom contact; excludes canonical IDs in the complete group snapshot and canonical payers of the three paid products. Uses existing HXC, WeCom and Order ports only. No arbitrary boolean DSL or SQL. The group snapshot must be complete, no unresolved members, observed not in the future and <=15 minutes old. Missing data fails refresh rather than broadening the audience.

Automatic refresh now reuses the existing River AudienceScheduleScan job (one-minute periodic scan). Segment Owner discovers only active closed-rule group dependencies (deduplicated, maximum100 groups). Composition reads local freshness in a UoW, then refreshes outside it if missing or older than5 minutes; the original scheduled scan runs afterwards. Failures are persisted by WeCom and keep the dependent rule unavailable without stopping unrelated rule dispatch. There is no second timer, queue or retry state machine. Provider reads remain disabled unless ChannelProviderReadEnabled is configured.

Migration0137 retains immutable complete observations. Scheduled runs use a frozen occurrence timestamp, so a later successful read must not erase the most recent observation at or before that timestamp. The current snapshot must still be complete/healthy; a failed or in-flight refresh blocks use of every historical observation. A first scheduled run with no preceding observation fails closed instead of inventing history. No observations are silently deleted. Real Provider read and production availability remain unverified; run the normal admin manual refresh and isolated Preview before enabling the rule.

Validation: local real PostgreSQL complete-empty, scope mismatch, missing/future/stale, inflight invalidation, partial identity, old writer CAS; Provider protocol malformed responses; no network inside UoW; admin/CSRF boundary; group and paid exclusions; all targeted race tests and architecture gate passed. No production writes.
