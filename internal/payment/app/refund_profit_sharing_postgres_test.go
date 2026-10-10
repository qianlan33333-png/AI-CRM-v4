package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

// Refund acceptance uses the existing transaction-local fixture. Cancellation
// uses the actual EER repository/lock/receipt, including rollback and races.
type refundCancellationEffects struct{ *externaleffects.Repository }

func (refundCancellationEffects) AcceptAndQueueWithin(ctx context.Context, command effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	return (postgresRefundEffects{}).AcceptAndQueueWithin(ctx, command)
}

type forbiddenRefundSplitQuery struct{ calls int }

func (p *forbiddenRefundSplitQuery) QueryProfitSharing(context.Context, string) (paymentport.ProfitSharingProviderResult, error) {
	p.calls++
	return paymentport.ProfitSharingProviderResult{}, errors.New("split query must not precede refund")
}
func (p *forbiddenRefundSplitQuery) QueryProfitSharingUnfreeze(context.Context, string) (paymentport.ProfitSharingProviderResult, error) {
	p.calls++
	return paymentport.ProfitSharingProviderResult{}, errors.New("unfreeze query must not precede refund")
}

func TestPostgreSQLRefundDoesNotReserveFutureCommission(t *testing.T) {
	for _, scenario := range []string{"queued-full", "queued-partial", "unknown", "dispatch-won", "queued-prior-attempt", "rollback", "concurrent-refunds", "root-refund"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentAppIntegrationPool(t)
			defer cleanup()
			ctx := context.Background()
			_, source, _, _ := runtime.Caller(0)
			migration, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../../migrations/0160_external_effect_system_control_actor.sql"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, string(migration)); err != nil {
				t.Fatal(err)
			}
			wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer wrapped.Close()
			uow, err := platformpostgres.NewUnitOfWork(wrapped)
			if err != nil {
				t.Fatal(err)
			}
			client, err := platformjobqueue.NewInsertClient(pool, river.NewWorkers())
			if err != nil {
				t.Fatal(err)
			}
			effects, err := externaleffects.NewRepository(pool, client)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Add(-time.Minute)
			var orderID, paymentID, receiverID, instructionID, effectID int64
			if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','refund-first','refund-first','M-refund-first',11,11,680,'CNY','paid','native',true,$1,$1) RETURNING id`, now).Scan(&orderID); err != nil {
				t.Fatal(err)
			}
			if err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,provider_transaction_reference,provider_transaction_digest,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program','M-refund-first',4,11,11,680,'CNY','paid',true,'trusted-transaction',$2,1,$3,$3) RETURNING id`, orderID, effectport.Hash("wechatpay.transaction", "trusted-transaction"), now).Scan(&paymentID); err != nil {
				t.Fatal(err)
			}
			if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,version,created_at,updated_at) VALUES(19,29,'wx-test','wechat-app:wx-test','mini_program',$1,'ready',1,$2,$2) RETURNING id`, effectport.Hash("receiver"), now).Scan(&receiverID); err != nil {
				t.Fatal(err)
			}
			effectState, splitState, attempts := "queued", "accepted", 0
			if scenario == "unknown" {
				effectState, splitState, attempts = "outcome_unknown", "outcome_unknown", 1
			}
			if scenario == "dispatch-won" {
				effectState, attempts = "attempted", 1
			}
			if scenario == "queued-prior-attempt" {
				attempts = 1
			}
			digest := effectport.Hash("refund-first-split")
			if err = pool.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state,attempt_count,generation,created_at,updated_at) VALUES('payment',$1,$2,$2,$2,$2,$2,$3,$4,1,$5,$5) RETURNING id`, effectport.KindWeChatPayProfitSharing, digest, effectState, attempts, now).Scan(&effectID); err != nil {
				t.Fatal(err)
			}
			if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_instructions(settlement_ref,payment_id,receiver_id,provider_order_no,idempotency_key_digest,source_ref_digest,payload_digest,policy_version_hash,amount_minor,currency,state,external_effect_id,deadline_at,version,created_at,updated_at) VALUES('dstl_refund_first',$1,$2,'v3ps_ABCDEFGHIJKLMNOPQRST',$3,$3,$3,$3,204,'CNY',$4,$5,$6,1,$7,$7) RETURNING id`, paymentID, receiverID, digest, splitState, effectID, now.Add(24*time.Hour), now).Scan(&instructionID); err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `INSERT INTO payment_profit_sharing_reserves(instruction_id,payment_id,amount_minor,state,created_at) VALUES($1,$2,204,'reserved',$3)`, instructionID, paymentID, now); err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `INSERT INTO payment_profit_sharing_provider_intents(instruction_id,effect_kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,request_snapshot,created_at) VALUES($1,$2,$3,$3,$3,$3,'{}',$4)`, instructionID, effectport.KindWeChatPayProfitSharing, digest, now); err != nil {
				t.Fatal(err)
			}
			service := NewService(uow, paymentstore.NewPostgreSQL(), orderStub{}, sessionStub{}, refundCancellationEffects{effects}, effects)
			provider := &forbiddenRefundSplitQuery{}
			if err = service.SetProfitSharingReconciler(provider); err != nil {
				t.Fatal(err)
			}
			if err = service.SetProfitSharingIdentityReader(receiverRecoveryIdentityStub{}); err != nil {
				t.Fatal(err)
			}
			command := paymentport.RefundCommand{PaymentID: paymentID, AmountMinor: 680, RefundNo: "RF-refund-first", Reason: "customer refund", ActorScope: "admin:17", IdempotencyKey: "refund-first-full-key-0001"}
			if scenario == "queued-partial" {
				command.AmountMinor = 200
			}
			if scenario == "root-refund" {
				command.ActorScope = "ops:root-refund:authorized-test"
			}
			if scenario == "rollback" {
				if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_refund_first() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture refund rollback'; END $$; CREATE TRIGGER reject_refund_first BEFORE INSERT ON payment_refunds FOR EACH ROW EXECUTE FUNCTION reject_refund_first()`); err != nil {
					t.Fatal(err)
				}
			}
			var acceptedID int64
			if scenario == "concurrent-refunds" {
				start, results := make(chan struct{}), make(chan error, 2)
				var wg sync.WaitGroup
				for i := 0; i < 2; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						<-start
						other := command
						other.IdempotencyKey += fmt.Sprint(i)
						other.RefundNo += fmt.Sprint(i)
						_, inner := service.RequestRefund(ctx, other)
						results <- inner
					}(i)
				}
				close(start)
				wg.Wait()
				close(results)
				accepted, rejected := 0, 0
				for inner := range results {
					if inner == nil {
						accepted++
					} else if errors.Is(inner, paymentport.ErrConflict) {
						rejected++
					} else {
						t.Fatal(inner)
					}
				}
				if accepted != 1 || rejected != 1 {
					t.Fatalf("accepted=%d rejected=%d", accepted, rejected)
				}
			} else {
				refund, inner := service.RequestRefund(ctx, command)
				if scenario == "rollback" {
					if inner == nil {
						t.Fatal("refund insert must roll back split cancellation")
					}
				} else {
					if inner != nil || refund.AmountMinor != command.AmountMinor {
						t.Fatalf("refund=%+v err=%v", refund, inner)
					}
					acceptedID = refund.ID
					replay, inner := service.RequestRefund(ctx, command)
					if inner != nil || replay.ID != acceptedID {
						t.Fatalf("replay=%+v err=%v", replay, inner)
					}
				}
			}
			if provider.calls != 0 {
				t.Fatalf("refund queried split %d times", provider.calls)
			}
			cancelled := scenario == "queued-full" || scenario == "queued-partial" || scenario == "concurrent-refunds"
			wantState, wantReserve := splitState, "reserved"
			if cancelled {
				wantState, wantReserve = "cancelled", "released"
			}
			var state, reserve string
			var refundCount, refundEffectCount, cancellationCount, cancelledActor int64
			if err = pool.QueryRow(ctx, `SELECT i.state,r.state,(SELECT count(*) FROM payment_refunds),(SELECT count(*) FROM external_effects WHERE kind=$2),(SELECT count(*) FROM external_effect_operation_receipts WHERE operation='cancel'),COALESCE((SELECT max(actor_admin_user_id) FROM external_effect_operation_receipts WHERE operation='cancel'),0) FROM payment_profit_sharing_instructions i JOIN payment_profit_sharing_reserves r ON r.instruction_id=i.id WHERE i.id=$1`, instructionID, effectport.KindWeChatPayRefund).Scan(&state, &reserve, &refundCount, &refundEffectCount, &cancellationCount, &cancelledActor); err != nil {
				t.Fatal(err)
			}
			wantRefunds := int64(1)
			if scenario == "rollback" {
				wantRefunds = 0
			}
			if state != wantState || reserve != wantReserve || refundCount != wantRefunds || refundEffectCount != wantRefunds || (cancelled && (cancellationCount != 1 || cancelledActor != 17)) || (!cancelled && cancellationCount != 0) {
				t.Fatalf("split=%s reserve=%s refunds=%d effects=%d cancels=%d actor=%d", state, reserve, refundCount, refundEffectCount, cancellationCount, cancelledActor)
			}
			projection, inner := effects.Get(ctx, fmt.Sprintf("eer_%d", effectID))
			wantEffect := effectport.State(effectState)
			if cancelled {
				wantEffect = effectport.StateCancelled
			}
			if inner != nil || projection.State != wantEffect || int(projection.AttemptCount) != attempts {
				t.Fatalf("split effect=%+v err=%v", projection, inner)
			}
			if scenario != "rollback" {
				if _, inner = service.LoadProfitSharingEffectMaterial(ctx, effectport.KindWeChatPayProfitSharing, digest); !errors.Is(inner, paymentport.ErrConflict) {
					t.Fatalf("refund exposure must stop fresh split call: %v", inner)
				}
				if _, err = pool.Exec(ctx, `UPDATE payment_refunds SET status='completed',version=version+1,updated_at=clock_timestamp() WHERE payment_id=$1`, paymentID); err != nil {
					t.Fatal(err)
				}
				if scenario != "queued-partial" {
					if _, inner = service.LoadProfitSharingEffectMaterial(ctx, effectport.KindWeChatPayProfitSharing, digest); !errors.Is(inner, paymentport.ErrConflict) {
						t.Fatalf("full refund must stop future split: %v", inner)
					}
				}
			}
		})
	}
}
