package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type absenceReader struct{ effectport.Projection }

func (r *absenceReader) Get(context.Context, string) (effectport.Projection, error) {
	return r.Projection, nil
}

type absenceProvider struct {
	now  func() time.Time
	fail bool
}

func (p *absenceProvider) QueryProfitSharing(context.Context, string) (paymentport.ProfitSharingProviderResult, error) {
	if p.fail {
		return paymentport.ProfitSharingProviderResult{}, paymentport.NewProfitSharingQueryError("RESOURCE_NOT_EXISTS")
	}
	return paymentport.ProfitSharingProviderResult{State: "NOT_FOUND", OrderNotFound: true, OccurredAt: p.now(), EvidenceDigest: effectport.Hash("signed-absence", p.now().Format(time.RFC3339Nano))}, nil
}
func (*absenceProvider) QueryProfitSharingUnfreeze(context.Context, string) (paymentport.ProfitSharingProviderResult, error) {
	return paymentport.ProfitSharingProviderResult{}, errors.New("unexpected unfreeze")
}

func TestPostgreSQLMissingProfitSharingProofAndFullRefund(t *testing.T) {
	for _, scenario := range []string{"confirmed", "too-fast", "too-soon-after-write", "unsigned-query", "interrupted", "generation-changed", "attempt-changed", "running", "rollback", "concurrent", "final-failed"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentAppIntegrationPool(t)
			defer cleanup()
			ctx := context.Background()
			wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer wrapped.Close()
			uow, err := platformpostgres.NewUnitOfWork(wrapped)
			if err != nil {
				t.Fatal(err)
			}
			origin := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
			now := origin.Add(time.Hour)
			var orderID, paymentID, receiverID, instructionID, effectID int64
			if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','absence-test','absence-test','M-absence',11,11,680,'CNY','paid','native',true,$1,$1) RETURNING id`, origin).Scan(&orderID); err != nil {
				t.Fatal(err)
			}
			if err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,provider_transaction_reference,provider_transaction_digest,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program','M-absence',4,11,11,680,'CNY','paid',true,'trusted-transaction',$2,1,$3,$3) RETURNING id`, orderID, effectport.Hash("wechatpay.transaction", "trusted-transaction"), origin).Scan(&paymentID); err != nil {
				t.Fatal(err)
			}
			if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,version,created_at,updated_at) VALUES(19,29,'wx-test','wechat-app:wx-test','mini_program',$1,'ready',1,$2,$2) RETURNING id`, effectport.Hash("receiver"), origin).Scan(&receiverID); err != nil {
				t.Fatal(err)
			}
			if err = pool.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state,attempt_count,generation,created_at,updated_at) VALUES('payment',$1,$2,$2,$2,$2,$2,'outcome_unknown',1,1,$3,$3) RETURNING id`, effectport.KindWeChatPayProfitSharing, effectport.Hash("split"), origin).Scan(&effectID); err != nil {
				t.Fatal(err)
			}
			if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_instructions(settlement_ref,payment_id,receiver_id,provider_order_no,idempotency_key_digest,source_ref_digest,payload_digest,policy_version_hash,amount_minor,currency,state,external_effect_id,deadline_at,version,created_at,updated_at) VALUES('dstl_absence_test',$1,$2,'v3ps_ABCDEFGHIJKLMNOPQRST',$3,$3,$3,$3,204,'CNY','outcome_unknown',$4,$5,1,$6,$6) RETURNING id`, paymentID, receiverID, effectport.Hash("split"), effectID, origin.Add(24*time.Hour), origin).Scan(&instructionID); err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `INSERT INTO payment_profit_sharing_reserves(instruction_id,payment_id,amount_minor,state,created_at) VALUES($1,$2,204,'reserved',$3)`, instructionID, paymentID, origin); err != nil {
				t.Fatal(err)
			}
			reader := &absenceReader{effectport.Projection{ID: fmt.Sprintf("eer_%d", effectID), Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayProfitSharing, State: effectport.StateUnknown, AttemptCount: 1, Generation: 1, UpdatedAt: origin}}
			service := NewService(uow, paymentstore.NewPostgreSQL(), orderStub{}, sessionStub{}, postgresRefundEffects{}, reader)
			service.now = func() time.Time { return now }
			provider := &absenceProvider{now: service.now}
			if err = service.SetProfitSharingReconciler(provider); err != nil {
				t.Fatal(err)
			}
			ref := fmt.Sprintf("psinst_%d", instructionID)
			command := paymentport.RefundCommand{PaymentID: paymentID, AmountMinor: 680, RefundNo: "RF-absence-test", Reason: "full refund", ActorScope: "admin:17", IdempotencyKey: "absence-full-refund-0001"}
			if _, err = service.ReconcileProfitSharing(ctx, ref); err != nil {
				t.Fatalf("initial independent accounting observation=%v", err)
			}
			if scenario == "too-soon-after-write" {
				reader.UpdatedAt = now
			}
			if scenario == "unsigned-query" {
				provider.fail = true
			}
			if scenario == "running" {
				reader.State = effectport.StateAttempted
			}
			if scenario == "final-failed" {
				err = uow.Within(ctx, func(tx context.Context) error {
					return service.CompleteEffect(tx, reader.ID, effectport.Envelope{Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayProfitSharing}, effectport.Attempt{}, effectport.AdapterResult{Completion: effectport.StateFinalFailed})
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 3; i++ {
				now = origin.Add(time.Hour + time.Duration(i)*10*time.Minute)
				if scenario == "too-fast" {
					now = origin.Add(time.Hour + time.Duration(i)*time.Minute)
				}
				if scenario == "interrupted" && i == 1 {
					provider.fail = true
				} else if scenario != "unsigned-query" {
					provider.fail = false
				}
				if scenario == "generation-changed" && i == 2 {
					reader.Generation++
				}
				if scenario == "attempt-changed" && i == 2 {
					reader.AttemptCount++
				}
				if scenario == "rollback" && i == 2 {
					if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_absence_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture rollback'; END $$; CREATE TRIGGER reject_absence_update BEFORE UPDATE ON payment_profit_sharing_instructions FOR EACH ROW EXECUTE FUNCTION reject_absence_update()`); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "concurrent" && i == 2 {
					var wg sync.WaitGroup
					failures := make(chan error, 2)
					for n := 0; n < 2; n++ {
						wg.Add(1)
						go func() { defer wg.Done(); _, inner := service.ReconcileProfitSharing(ctx, ref); failures <- inner }()
					}
					wg.Wait()
					close(failures)
					for inner := range failures {
						if inner != nil {
							t.Fatal(inner)
						}
					}
				} else {
					_, err = service.ReconcileProfitSharing(ctx, ref)
					if err != nil && scenario != "unsigned-query" && scenario != "running" && scenario != "interrupted" && scenario != "rollback" {
						t.Fatal(err)
					}
				}
			}
			var reserve, state string
			var known bool
			var obs, refunds, splits int
			if err = pool.QueryRow(ctx, `SELECT r.state,i.state,i.outcome_known FROM payment_profit_sharing_reserves r JOIN payment_profit_sharing_instructions i ON i.id=r.instruction_id WHERE i.id=$1`, instructionID).Scan(&reserve, &state, &known); err != nil {
				t.Fatal(err)
			}
			confirmed := scenario == "confirmed" || scenario == "concurrent" || scenario == "final-failed"
			wantState := "cancelled"
			if scenario == "final-failed" {
				wantState = "exception"
			}
			if confirmed != (reserve == "released" && state == wantState && known) {
				t.Fatalf("reserve=%s state=%s known=%v", reserve, state, known)
			}
			if confirmed {
				first, inner := service.RequestRefund(ctx, command)
				if inner != nil || first.AmountMinor != 680 {
					t.Fatalf("full refund=%+v err=%v", first, inner)
				}
				replay, inner := service.RequestRefund(ctx, command)
				if inner != nil || replay.ID != first.ID {
					t.Fatalf("replay=%+v err=%v", replay, inner)
				}
			}
			if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payment_profit_sharing_audit_events WHERE event_type='payment.profit_sharing.order_absent'),(SELECT count(*) FROM payment_refunds),(SELECT count(*) FROM external_effects WHERE kind=$1)`, effectport.KindWeChatPayProfitSharing).Scan(&obs, &refunds, &splits); err != nil {
				t.Fatal(err)
			}
			wantObs := 3
			if scenario == "final-failed" {
				wantObs = 1
			}
			if splits != 1 || (!confirmed && refunds != 0) || (confirmed && (refunds != 1 || obs != wantObs)) {
				t.Fatalf("observations=%d refunds=%d splits=%d", obs, refunds, splits)
			}
		})
	}
}
