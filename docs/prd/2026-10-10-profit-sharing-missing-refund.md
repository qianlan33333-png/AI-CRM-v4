# Missing original profit-sharing order and full refund

User authorized a full RMB 6.80 refund for native order 5626 and related bug fixes. Existing production 7514476 already enforces paid plus one minute for new splits. Its historical instruction 6 remains outcome_unknown with a 204-minor reserve: a signed WeChat GET returned RESOURCE_NOT_EXISTS, while control instruction 5 returned exact receiver SUCCESS. No refund exists. One missing-order query does not authorize a new split or immediate reserve release.

```mermaid
flowchart TD
 A[Refund requested or original due reconciliation] --> B[Read original immutable split reference]
 B --> C[Official SDK GET with configured response verifier]
 C -->|Exact receiver success or failure| D[Existing settlement path]
 C -->|Signed 404 RESOURCE_NOT_EXISTS| E[Existing Payment audit records absence]
 C -->|Unsigned error or transport failure| F[Preserve reserve and unknown state]
 E --> G{Three observations at least ten minutes apart and thirty minutes since last write; same effect generation and attempt}
 G -->|No| F
 G -->|Yes| H[Under original UoW confirm not paid and release split reserve]
 H --> I[Existing idempotent refund request and External Effects]
 I --> J[Verified refund query or callback settles order and reverses campaign credit and commission]
```

References: official [WeChat FAQ](https://pay.wechatpay.cn/doc/v3/merchant/4014547102) documents three queries ten minutes apart over thirty minutes without intervening writes; [original-order query](https://pay.wechatpay.cn/doc/v3/merchant/4012525210) and [official SDK](https://github.com/wechatpay-apiv3/wechatpay-go/blob/main/core/client.go) show request/query and response verification. The installed SDK v0.2.21 checks HTTP error status before its signature validator, so an error code alone is insufficient evidence. Explicitly verify the original signed 404 with the same configured trust material.

Reuse Payment instruction/reserve/audit, original query maintenance role, existing Distribution due job, normal refund command, existing EER and refund callback/query. Do not create a split retry, new merchant split number, queue, migration, or frontend flow. Three accepted observations span at least twenty minutes, with the last no earlier than thirty minutes after the original effect's last write; intervening non-absence observations reset the sequence. A still running, queued, changed-generation or changed-attempt effect cannot provide absence proof. Final known pre-submit failures also release their retained reserve in the same Payment UoW.

OneID: existing identity/receiver validation through current ports only; no ownership change. Persistence: Payment-owned audit and reserve transition under existing UoW and instruction lock. External Effects: signed Provider reads, followed only by the authorized original refund effect; no split Provider write. No second execution or identity kernel.

Five impacts: query Port adds a verified absence signal; refund may reconcile original split before checking available funds; Payment/provider/store and direct Distribution settlement consumer are affected; existing admin refund page consumes the same API, with no UI edit; targeted tests cover signed/unsigned/tampered 404, spacing, stable attempt/generation, concurrent replay, rollback, known-paid preservation, pending protection, full-refund acceptance and commission reversal. Production verification separately records exact binary, signed absence observations, refund effect, verified WeChat refund result and final order/commission/campaign facts.

Minimum necessary constraint: retain reserve until the official missing-order observation window proves non-payment; this prevents simultaneous split/refund or duplicate money effects. Existing one-minute and idempotency constraints remain. No additional approval. Candidate is committed and pushed from an independent codex worktree, handed to the routed workbench, tested on Linux/PG and installed staging, then same-artifact production. No migration or database backup. Rollback restores the prior application; existing audit facts and truthful released reserves are preserved.

## Refund first (user scope clarification after standards review)

The user explicitly requires a distribution order to refund in full instead of protecting its future commission, and rejects split queries as a prerequisite. Standards review confirms that a fully refunded WeChat order cannot subsequently split and that unsplit funds can refund directly. The existing application incorrectly subtracts a future commission reserve from the refund budget and performs a split query before refund acceptance. Remove both prerequisites. Refund budget is original paid amount minus successful and non-terminal refunds, serialized by the original payment lock. WeChat remains authoritative for actual available/frozen funds and concurrent money processing; a refund request is not presented as success before a verified SUCCESS callback/query.

Reuse Payment/EER transactional cancellation for still-queued, zero-attempt splits in the refund acceptance UoW. If dispatch already won, leave that immutable split effect/reserve truthful and accept the refund anyway; never forge a failed split, replay it, or discard a confirmed paid amount. A requested refund cancels any eligible queued split, creates the refund receipt/effect and opens Distribution refund exposure atomically. On full refund success the existing consumer cancels the unpaid commission and reverses referral credit; during a pending/unknown refund, no new settlement is accepted. Original attempted split reconciliation may run later for accounting, independently of refund acceptance.

```mermaid
flowchart TD
 R[Authorized refund request] --> P[Lock original payment; replay existing receipt first]
 P --> B[Refund budget excludes future commission reserve]
 B --> S{Original split dispatch state under effect lock}
 S -->|Queued with zero attempts| C[Cancel original effect and instruction; release reserve]
 S -->|Already attempted or unknown| Q[Preserve original split facts; no query prerequisite]
 S -->|No split or already paid| F[Validate remaining refund budget]
 C --> F
 Q --> F
 F -->|Amount available| A[Accept original refund effect and refund exposure in same UoW]
 A --> W[Normal refund worker and verified Provider result]
 W -->|Full refund succeeded| Z[Cancel unpaid commission and reverse referral credit; WeChat refuses later split]
 W -->|Processing or unknown| H[Keep original refund key; hold future settlement]
 W -->|Verified rejection| E[Report actual Provider failure; preserve money facts]
```

References actually inspected before application editing: [official GitHub Go SDK refund API/example](https://github.com/wechatpay-apiv3/wechatpay-go/blob/main/docs/refunddomestic/RefundsApi.md), [refund source](https://github.com/wechatpay-apiv3/wechatpay-go/blob/main/services/refunddomestic/api_refunds.go), [official split API/example](https://github.com/wechatpay-apiv3/wechatpay-go/blob/main/docs/profitsharing/OrdersApi.md), [WeChat order refund rules](https://pay.wechatpay.cn/doc/v3/merchant/4013071001) and [merchant split FAQ](https://pay.wechatpay.cn/doc/v3/merchant/4014547102). The SDK exposes independent Create/Query refund operations and does not make a split query mandatory. The provider rules, rather than the example alone, establish direct unsplit refunds, the impossibility of splitting a fully refunded order, and refund/split totals not exceeding original frozen money. Orders that already split partially may require the standard unfreeze step and sufficient merchant available balance; already-paid commission facts remain history and are never relabelled unpaid by this fix. This candidate targets acceptance of refunds without the local future-commission gate; it does not fabricate Provider success or auto-recover personal-account payouts.

No new approval, UI, Provider writer, queue, migration or identity mechanism. OneID uses the original canonical payment/customer facts. Persistence uses existing Payment and EER UoW with payment/instruction/effect locks. Normal admin cancellation records the authenticated administrator already frozen in the refund actor scope. Root refund operations keep their distinct root authorization in the refund receipt; their queued cancellation is owned by the existing due worker with its existing named system actor. Both paths refuse fresh split material whenever the payment has non-terminal refund exposure or a completed full refund. No made-up administrator or new system subject is introduced. An optional conditional guard on the existing cancellation Port cancels only queued zero-attempt effects under the EER lock and otherwise returns the unchanged projection. This is the minimum protection against falsely cancelling an attempted money write, and it never blocks the refund request. The old strict cancellation contract and receipts remain compatible.

| State | Refund acceptance | Commission/split handling |
| --- | --- | --- |
| No split / awaiting due date | Accept full remaining amount directly | Refund exposure prevents new split acceptance |
| Queued with zero attempts | Cancel locally and accept full refund in one UoW | Cancel original split and release reserve |
| Running / outcome unknown | Accept refund without split query or local reserve deduction | Preserve original attempt; hold future splitting; reconcile accounting later |
| Provider confirms full refund | Verified successful refund fact only | Cancel unpaid commission, reverse ranking; never dispatch new split |
| Split already paid | Normal refund request, Provider fund rules apply | Preserve paid commission; existing refund-after-paid liability logic |
| Refund response unknown | Original-key replay/query only | No second refund or split intent |

Five impacts: unchanged refund API accepts eligible full refunds without split reads; no deduction of future split reserves, and conditional local cancellation shares refund UoW; Payment/EER and Distribution refund/due consumers; existing admin page receives the normal acceptance without UI edits; PostgreSQL tests cover full/partial/replay, concurrent refunds, dispatch wins, rollback after cancellation, commission preservation until verified refund and cancellation after success. Candidate base is domestic main a018e6858eaf75ecfcf37435c9953eb8065d1770; targeted checks and installed smoke precede same-artifact production through the routed workbench under existing user authorization. Rollback reinstalls the preceding application, preserving already committed refund/split/cancellation facts.

## Authorized operator entry

The production administrator session has expired. Add a one-shot `payment-refund` runtime role restricted to effective OS UID 0 before composition. It uses the same Payment service, funding checks, operation receipt, audit, refund queue and verified query; it introduces no HTTP route, daemon, direct Provider writer or authentication bypass. Normal unauthenticated HTTP refunds remain forbidden. A payment/order mismatch, historical payment, non-WeChat payment, non-paid payment, missing authorization reference or invalid amount fails closed. The root command may report accepted/unknown before the normal worker and verified refund query prove completion.

Run the exact installed binary as root with its existing protected production configuration and the following explicit overrides. Do not print configuration secrets. The one immutable key and authorization reference are reused on every safe replay; never create another key after an unknown result.

```text
AICRM_ROLE=payment-refund
AICRM_PAYMENT_REFUND_ID=1244
AICRM_PAYMENT_REFUND_ORDER_ID=5626
AICRM_PAYMENT_REFUND_AMOUNT_MINOR=680
AICRM_PAYMENT_REFUND_KEY=order-5626-full-refund-680-20261010-v1
AICRM_PAYMENT_REFUND_AUTHORIZATION=user:01a12520-cccb-7bf3-8995-76a0f2c30782:refund-680
```

The user additionally supplied WeChat transaction `4500000511202610107247146771`. Production read-only mapping confirmed payment 1244 / native order 5626 / amount 680. Before the command, independently repeat that mapping and verify absence observations and released reserve. The original `profit-sharing-reconcile` role with instruction ID 6 can collect real-time signed absence observations; do not backdate receipts. After acceptance, read the same refund receipt, normal worker result, signed WeChat refund query, order refunded amount, commission cancellation and paid-referral reversal. No further split write is authorized for order 5626.
