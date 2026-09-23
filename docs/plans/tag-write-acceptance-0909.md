# Tag catalog write acceptance

OneID: not involved; catalog metadata and its Provider identifiers do not identify a customer.
Persistence: Tag-owned local transaction, Provider reads and outbound/EER Provider writes. Preserve the shared UoW, original effect identity and frozen local IDs.

- Expose Provider tag bindings separately from local command IDs; copy only the verified binding.
- Recover failed writes explicitly through the original effect only after EER proves no unknown/executed attempt. Never auto-replay unknown outcomes or create replacement local tags.
- Keep the frozen donor unchanged; adapt its clipboard/status surface in the V3 Host.
- Verify owner transactions, queue generation/replay, failed/unknown guards and browser interactions. Deployment gates and actual temporary-tag acceptance remain separate.
