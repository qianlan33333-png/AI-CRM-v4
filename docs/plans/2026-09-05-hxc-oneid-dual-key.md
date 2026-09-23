# HXC OneID Dual-Key Implementation Plan

> **For implementation:** Use the repository `aicrm-v3-development` decision manual and execute this plan task by task.

**Goal:** Match and persist HXC UnionID plus phone through OneID, retain unmatched observations, and ship an audited conflict workflow to production without provisioning customers.

**Architecture:** HXC remains the read-only source and read-model owner. Identity owns all matching, encrypted source observations, receipts, conflicts, identity attachment, and merge candidates behind a stable Port. Existing River refresh execution and PostgreSQL Unit of Work boundaries are reused.

**Tech Stack:** Go, PostgreSQL 16, River, MySQL read adapter, TypeScript admin UI, OpenAPI.

---

1. Add the approved PRD and architecture classification.
2. Add forward-only Identity and HXC v2 projection migrations.
3. Implement encrypted HXC source observations and dual-key Identity coordination with unit and PostgreSQL tests.
4. Carry phone through the HXC MySQL snapshot and integrate inspect/apply modes into the existing refresh job.
5. Add safe summary/detail fields and the SuperAdmin conflict closure APIs/UI.
6. Run Go, web, OpenAPI, migration, PII and release gates; open one complete capability PR.
7. Deploy write-disabled, run full inspect, enable writes only after invariant checks, apply, replay, and verify the next incremental refresh.

Completion requires merged code, production SHA proof, applied migrations, reconciled full apply, replay zero-delta, an operable conflict workflow, and an active timer/worker.
