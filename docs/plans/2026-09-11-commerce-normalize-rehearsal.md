# Protected commerce normalization and rehearsal

Classification: OneID resolves existing canonical customers only. Persistence: source encrypted snapshot plus target repeatable-read read-only normalization; apply uses existing Order/Payment Owner ports and the shared transaction. No Provider reads or writes occur in these commands.

`migrate-commerce-capture --mode normalize --directory CAPTURE --key-file KEY --output-directory NEW_PRIVATE_DIRECTORY --run-key RUN` reads `AICRM_DATABASE_URL` for target evidence. Use an isolated rehearsal database first. Source tables remain encrypted in the unchanged capture. The output manifest includes every order/refund, original source status, explicit missing-refund evidence, and unresolved identity quarantine; missing or unsupported source tables/states fail closed.

The normalizer uses explicitly scoped external-contact source mappings and OneID Resolve to reuse existing roots. Historical payer attribution is preserved from audited target evidence. It does not provision customers, infer a beneficiary, or infer an Open Platform scope from UnionID. `--existing-identities-only` on the commerce importer enforces resolution-only behavior even during apply. Unresolved financial facts remain historical and effect-ineligible; they cannot grant benefits.

The protected preconditions file binds the exact manifest SHA to each existing order's source digest and target version. Frozen commercial fields are compared before output and again by the transactional Owner delta port. Old import receipts are immutable. Use full import, never order-only, so failed/closed/processing/requested refund facts remain visible without entering completed refund sums.

Rehearsal sequence: apply schema migrations 0127, 0129, 0131 to the isolated database; dry-run with manifest and preconditions; apply with exact SHA, preconditions, existing-identities-only and confirm-apply; full reconcile with the same SHA. Read through the isolated admin Order/Payment API afterwards. Do not apply these steps to production until the final source freeze and fresh complete capture have been reviewed.

Legacy raw envelopes may contain Go JSON HTML escaping introduced after source table hashes were computed. Validation reverses only the known escaping and accepts it only when the recovered bytes exactly match the original frozen SHA. No digest is changed or bypassed.
