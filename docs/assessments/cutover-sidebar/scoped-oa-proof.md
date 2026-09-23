# Two scoped OA subjects: isolated rehearsal

Classification: OneID Resolve + explicit ProvisionCustomerFromVerifiedIdentity; Provider read through existing signed WeChat Pay QueryPayment, followed by a separate local Identity UoW. No UnionID fact, automatic merge, new payment, redemption, entitlement effect, or message. Source references are opaque migration keys only.

The remaining entitlement source rows 77/83 and coupon claim rows 9/10 refer to two source subjects without verified external-contact evidence. Three source paid orders preserve matching request/notification AppID + payer OpenID; CRM OAuth projection agrees. The AppID matches the source's effective managed MP/payment AppID. Source h5_wechat_pay.py:1446 verifies/decrypts notifications before storing provider payload; its OAuth callback exchanges codes using the configured MP AppID.

`cmd/migrate-sidebar-oa-proof` verifies all three orders through the existing signed Payment query. Every successful signed result must match the exact source merchant number, amount, CNY, SUCCESS, AppID and payer OpenID, with AppID equal to target's configured H5 AppID. Only then is the independent encrypted proof written. Apply requires its exact digest, fresh verification within one hour, matching H5 App scope, and explicit provisioning flag. Existing roots are resolved; missing roots are explicitly provisioned as oa_openid / wechat-app scope. Neither source UnionID nor phone is imported as identity evidence.

Sidebar binding adds an optional independently hashed OA proof to the existing same-Corp proof. A source reference present in both proofs is rejected. The derived manifest binds both proof digests and original source digest. Entitlement/claim source rows and CapturedAt are unchanged; the original source snapshot is not rewritten.

## Actual result, 2026-09-11 (isolated DB only)

- 3 real signed Provider payment queries matched; proof digest `9c4dcb9af48d14458d20222108894d43715ec71b51f6628b8d659048a8df0e30`.
- Explicitly provisioned 2 scoped OA customers in aicrm_cutover_rehearsal_20260911.
- Sidebar preflight: 156 ready, 0 blocked.
- Apply: 4 imported, 152 replayed, 0 quarantined.
- Reconcile: 156 mapped, matched=true; 95 entitlement + 61 claimed-coupon source records.
- Protected target evidence: sidebar-oa-source.json, sidebar-oa-live.enc, sidebar-existing-scoped-oa.json, sidebar-oa-*.log under /var/backups/aicrm/cutover-prep-20260911. Raw identifiers and keys were never printed.

Production was not written. Reuse the verification tool with a new exclusive proof destination and freshly confirmed source/target configuration before any later production provisioning. The protected key file is supplied explicitly; it is never embedded in code or arguments as a value.
