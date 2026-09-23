# AI Assistant OneID Full Migration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Deliver donor-equivalent AI Assistant list/detail pages backed by canonical OneID, PostgreSQL persistence, atomic whole-plan approval, Outbound/EER execution and honest outcome projection.

**Architecture:** AI Assistant owns review state; Customer/Identity/Staff/Media are stable-Port dependencies; Outbound owns the only WeCom write; EER/River owns durable execution. Donor UI files stay byte-frozen behind a deterministic Host Adapter.

**Tech Stack:** Go, PostgreSQL 16, pgx v5, River, OpenAPI, TypeScript, esbuild and Orval.

---

## Classification

```text
OneID: involved — canonical reads and verified scoped Resolve; no Provision/Bind/Merge
Persistence: local UoW + River job + Provider read + Provider write
External Effects: involved — approval and effect acceptance share one UoW
```

## Delivery batches

- **AA00:** Freeze donor hashes/behavior, add stable Ports, OpenAPI, journey skeleton, domain and migrations `0036`/`0037` after rebasing onto the v3 migration sequence through `0035`.
- **AA01:** Implement intake, Identity Resolve, canonical roots, list/detail/recipient queries and local receipts/audit/outbox. No pre-approval effects.
- **AA02:** Build deterministic donor Adapter and real list/detail/pagination/drawer API integration without Mock/sessionStorage.
- **AA03:** Add immutable content/material versions, individual review, whole-plan preview/reject. Individual approval never sends.
- **AA04:** Add Outbound private messages and atomically create intents/effects/River rows/bindings/receipt/audit/outbox on whole-plan approval.
- **AA05:** Add projections, unknown reconciliation, truthful UI, runbooks, race/integration/migration/OpenAPI/donor/visual/E2E/capacity/PII checks and controlled feature-flag rollout.

## Locked scope

- List and detail pages only; no Campaign, Observability, Automation UI or model orchestration.
- No donor history migration.
- Whole-plan dispatch gate.
- Provider disabled by default.
