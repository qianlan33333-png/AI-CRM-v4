# Paid audience cutover: packages 30 and 37

Scope: rules only, recompute through native Segment; never import old members or trigger outbound. Source evidence is `audience-rule-audit-complete.json`, current versions 30/83 and 37/81. Archived versions are not migration inputs.

OneID: reads canonical customer only. Order payer attribution is owned by Order; active contact facts come from WeCom Owner Port. No identity lookup by phone/hash, implicit provision, or merges.
Persistence: existing Segment configuration/refresh uses its Owner Port and PostgreSQL UoW/jobqueue. These changes add no production mutations or Provider calls.

## 37: supported rule, contact facts currently missing

`fixtures/cutover-audience-37-definition.json` is the native definition, with the original timestamp normalized to UTC and null end normalized to the native empty unbounded value. Product names are presentation, not matching keys. Owner all becomes an empty native staff selection. `paid_order` selects payer, not beneficiary; unknown payer remains excluded even if beneficiary exists. Both WeChat Pay and WeChat Shop participate through Order.

Original published SQL selects paid product 202608121337, paid_at >= 2026-08-12 19:30 +08, and an active WeCom contact. Native matching preserves these business conditions using established canonical attribution. When attribution is unresolved it fails closed rather than cloning the old external-ID join.

Actual isolated read-only funnel on 2026-09-11: 67 product order lines, 2 paid, 2 attributed payers, 2 with recorded payment time, 2 in the original lower-bound window, 0 with active WeCom contact facts. A successful preview response with zero members does not demonstrate source-equivalent population. Provider-backed contact data must be refreshed through WeCom before declaring the recomputed population complete. Do not copy old audience membership as a workaround.

Native configuration must be submitted through normal authenticated Segment API and existing configuration/version CAS; use the fixture as the `definition`, not as an unversioned database patch. Preserve original 180-second evaluation cadence through the supported scheduler. Production activation is performed only by the coordinating operator with the approved native API script below.

## 30: reviewed native business rule

The user explicitly requested recomputation from new-system canonical business data, not literal replication of legacy SQL or legacy memberships. The approved native definition is `fixtures/cutover-audience-30-definition.json`: the following three exact product codes, paid status, all owners, and an active WeCom relationship:

- subscription_trial_month
- prd_20260518095708_9f77db
- prd_20260707050545_291025

A full native refresh generates a new snapshot. It does not import the old incremental watermark, old event receipts or mobile-hash identity joins. Historical unknown payment times may match this intentionally unbounded business rule; this is different from version 83's incremental SQL and is explicit rather than silently dropping conditions. Payer ownership remains the authoritative Order fact. Unresolved payer roots stay excluded. The rule cannot substitute an arbitrary profile or another customer's beneficiary to recover an old list.

Package 37 currently has two paid buyers with active profiles but **no follow relationships**. Profiles alone do not establish an active relationship. Refresh current WeCom facts through its Owner before accepting the recomputed population; never manufacture follow rows or import the old list to force a nonzero count.

## Reviewable API configuration plan

Run `python3 scripts/cutover_paid_audience_plan.py --target-30 TARGET_ID --version-30 CURRENT_VERSION --target-37 TARGET_ID --version-37 CURRENT_VERSION --reference-time RFC3339` to emit four reviewable API requests: versioned native configuration followed by full refresh for each package. IDs are explicit target IDs; source IDs are never assumed equal. The script does not authenticate or write. Submit these requests through the normal authenticated admin client with its CSRF protection and the included stable Idempotency-Key. Existing Worker processes the accepted refresh through jobqueue; verify the refresh run and published members afterward. HTTP 202 alone is not completion. `scripts/cutover_paid_audience_apply.py` also exposes `apply(request, journal_path, target_label, reference_time)` for the coordinating authenticated admin client. It creates only two new packages, saves the reviewed definitions with `refresh_mode=every_3m`, activates rule computation, and accepts full refreshes. It uses stable idempotency keys and a protected write-ahead journal; interrupted requests replay the same body/key. It creates no automation bindings or sender sets, and rejects unexpected ones. Preserve the journal and verify final refresh-run/member results separately; the returned `population_verified` remains false.

## Validation

Real PostgreSQL regression covers historical payer-only orders with NULL beneficiary for both commerce providers, and excludes an unknown payer with a known beneficiary. Segment regression covers active-contact requirement, unrelated products, missing payment time, deduplication, and both half-open interval boundaries. Existing runtime Owner implementation was already correct; no substitute business read path was added.
