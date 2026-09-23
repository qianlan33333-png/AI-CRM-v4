# H5 authorization and prepay incident

OneID: resolves and explicitly provisions Provider-verified OA OpenID plus scoped UnionID through Identity Port. Existing cross-customer conflicts remain audited candidates, never automatically merged.
Persistence: shared PostgreSQL transaction for identity/session; prepay remains a Payment-owned External Effect, with original idempotency and unknown-result fencing.

## Observed production evidence

- Payment 924 / effect 21 at 16:10:28: awaiting_prepay, outcome_unknown, one attempted call; no usable handoff.
- Generated merchant order length was 38, exceeding WeChat Pay's 32-character limit. Original-order read-only query returned HTTP 400 PARAM_ERROR (invalid merchant order number), without response signature headers. The unsigned rejection is not treated as a signed terminal reconciliation receipt.
- Protected evidence remains on target under /var/backups/aicrm/payment-debug-20260911. No payment, refund, or replacement prepay was sent by this investigation.

## Changes

- New merchant order numbers encode the existing 128-bit deterministic digest in base32, yielding 32 characters including prefix. Existing 38-character idempotent replays retain their original reference.
- Provider adapter rejects invalid merchant numbers before network dispatch.
- Checkout reports prepay state through External Effects Port, stops indefinite waiting on unknown results, and adds bounded WeChat bridge waits.
- H5 requests snsapi_userinfo and validates same-subject userinfo with nonempty UnionID. Old OpenID-only sessions must reauthorize. No artificial promise that WeChat will repeat its consent screen.

## Acceptance boundary

Unit and PostgreSQL integration checks plus production route readback are required. Native WeChat authorization and cashier display require a WeChat client. An online release alone does not prove an actual payment.

## Production readback

Release 7a88435cdca19372757b0895d072d8638c58fd16 is active on 124.220.53.183. Platform migrations 0140/0141 applied. Payment OAuth returned 302 and survey OAuth 303 to open.weixin.qq.com with snsapi_userinfo, both callbacks on www.youcangogogo.com. API/effects/Excel active; Caddy hash unchanged. Effect 21 remains outcome_unknown with attempt_count=1.

Full Go tests and architecture/format checks passed. Targeted PostgreSQL checkout, session, identity and survey migration tests passed. Broad PostgreSQL run initially included invalid socket-URL fixture parsing and a River timing failure; the failed cases passed with the TCP URL on rerun. Release installer first rolled back because mktemp left the release root 0700, blocking the separate Excel service user; root mode restored to 0755 and validated installation succeeded. Installer now preserves that traversal permission for future releases.

Patch reused verified unchanged 75708 web/component/tool artifacts, replacing the application binary and adding the two owner migrations. Protected pre-release database backup 49,239,840 bytes passed pg_restore listing. Real user OAuth receipt and native cashier UI remain phone-client acceptance, not claimed by HTTP checks.

## 16:38 identity conflict and checkout follow-up

The real userinfo callback supplied verified UnionID, but OA and UnionID belonged to different Customer roots. The strong merge candidate was confirmed through the authenticated OneID admin API after explicit user approval. The survivor retains 55 orders (20 paid), one membership, ten coupons and eight submissions. Verified OA, UnionID and WeCom identities now resolve to the survivor. Protected confirmation receipt: `/var/backups/aicrm/oauth-merge-confirm-20260911/confirm-response.json`. Payment 924 and effect 21 were not modified or retried by the merge.

Release `8a436d20660af149144c1fd4b497aec061ad0beb` deployed with migration 0142. `/p/subscription_trial_month` and `/pay/subscription_trial_month` return the direct checkout, amount summary and fixed pay button, without a hero image or beneficiary checkbox. OAuth returns to the same product page; conflict responses are human-readable and do not loop. Purchase attribution continues to use the verified payer_self contract. Head image removal was explicitly requested after the initial layout implementation.

Production readyz returns this SHA; OAuth for /p returns 302 with snsapi_userinfo and the formal callback host. API, effects and Excel services are active. Full Go suite passed serially; parallel suite initially raced a web/dist regeneration, and the isolated affected webshell test also passed. Architecture and format checks passed. Browser DOM at 390px reports 390px scroll width and no hero/confirmation control. Native WeChat cashier remains unverified.

The user reports no debit for the original 16:10 payment. This is manual evidence, not a signed Provider terminal receipt. Original unknown payment recovery is being handled separately; it must not be silently retried or reassigned during identity merging.
