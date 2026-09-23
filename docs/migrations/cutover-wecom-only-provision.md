# Explicit cutover WeCom-only provisioning

Classification: OneID verified provisioning through `identity/port.VerifiedProvisioner`; Provider directory read through the existing WeCom adapter; local Owner transaction for customer/identity/audit. No UnionID facts, linking, automatic merging, or Provider writes.

This command is intentionally separate from resolve-only sidebar/audience history. A v2 `existing_wecom_only` proof can only be used to select and verify candidates. It cannot be applied as a provisioning plan.

1. Freeze a 0600 JSON object containing `opaque_source_refs` for only the reviewed source subjects. Supply its exact file SHA-256, the encrypted v2 proof SHA, and independently confirm source/target Corp configuration. Do not infer an Open Platform scope.
2. `migrate-cutover-wecom-provision --mode=verify` resolves existing target references without creating them, then calls the existing external-contact reader for missing subjects. Success requires matching external ID and source opaque Union reference plus a follow relationship. Provider errors and mismatches are retained in the encrypted v3 output. Network calls do not hold a database transaction.
3. Freeze the returned v3 SHA. `--mode=dry-run` accepts only this separate live-verified format. `--mode=apply --confirm-provision` imports verified missing WeCom roots through the Owner Port in one Unit of Work; existing roots are reused, conflicts roll back. It never constructs a UnionID fact. All input paths and Corp are explicit; credentials are read privately from platform configuration.
4. Replay the exact v3 proof and verify zero new roots. Rerun the unchanged business manifest; previously quarantined rows may now map through the same existing-WeCom resolver. Business history Owner receipts prevent duplicate grants or claims across independent runs.

Do not broaden candidate scope to every directory row. Missing provider relationships remain quarantined and require separate evidence. Rehearsal acceptance does not authorize production data writes; final source deltas and API readbacks remain separate gates.
