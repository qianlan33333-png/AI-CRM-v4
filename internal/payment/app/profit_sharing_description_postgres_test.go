package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type frozenPurchaseDescription struct {
	value            string
	calls            int
	orderID, buyerID int64
}

func (s *frozenPurchaseDescription) ReadProfitSharingDescriptionWithin(ctx context.Context, orderID, buyerID int64) (string, error) {
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return "", err
	}
	s.calls++
	s.orderID, s.buyerID = orderID, buyerID
	return s.value, nil
}

func TestPostgreSQLProfitSharingDescriptionFreezeReplayAndLegacyMigration(t *testing.T) {
	for _, scenario := range []string{"freeze-replay", "rollback", "invalid-text", "legacy-migration"} {
		t.Run(scenario, func(t *testing.T) {
			legacy := scenario == "legacy-migration"
			pool, cleanup := paymentAppIntegrationPoolWithDescription(t, !legacy)
			defer cleanup()
			ctx := context.Background()
			wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			uow, err := platformpostgres.NewUnitOfWork(wrapped)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Add(-time.Minute)
			var orderID, paymentID, receiverID, legacyID int64
			if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','description-test','description-test','M-description',11,11,680,'CNY','paid','native',true,$1,$1) RETURNING id`, now).Scan(&orderID); err != nil {
				t.Fatal(err)
			}
			if err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,provider_transaction_reference,provider_transaction_digest,paid_confirmed_at,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program','M-description',4,11,11,680,'CNY','paid',true,'trusted-transaction',$2,$3,1,$3,$3) RETURNING id`, orderID, effectport.Hash("wechatpay.transaction", "trusted-transaction"), now).Scan(&paymentID); err != nil {
				t.Fatal(err)
			}
			account := effectport.Hash("payment.profit-sharing.receiver.account.v1", "wx-test", "wechat-app:wx-test", "trusted-openid")
			if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,version,created_at,updated_at) VALUES(19,29,'wx-test','wechat-app:wx-test','mini_program',$1,'ready',1,$2,$2) RETURNING id`, account, now).Scan(&receiverID); err != nil {
				t.Fatal(err)
			}
			request := paymentport.ProfitSharingRequest{SettlementRef: "dstl_description_fixture", OriginalPaymentRef: paymentport.PaymentReference(paymentID), RecipientCustomerID: 19, AmountMinor: 204, Currency: "CNY", IdempotencyKey: "description-fixture-idempotency-0001", SourceDigest: effectport.Hash("description-source"), PayloadDigest: effectport.Hash("description-payload"), PolicyDigest: effectport.Hash("description-policy")}
			if legacy {
				if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_instructions(settlement_ref,payment_id,receiver_id,provider_order_no,idempotency_key_digest,source_ref_digest,payload_digest,policy_version_hash,amount_minor,currency,state,deadline_at,version,created_at,updated_at) VALUES($1,$2,$3,'v3ps_ABCDEFGHIJKLMNOPQRST',$4,$5,$6,$7,204,'CNY','accepted',$8,1,$9,$9) RETURNING id`, request.SettlementRef, paymentID, receiverID, effectport.Hash("payment.profit-sharing.idempotency.v1", request.IdempotencyKey), request.SourceDigest, request.PayloadDigest, request.PolicyDigest, now.Add(24*time.Hour), now).Scan(&legacyID); err != nil {
					t.Fatal(err)
				}
				_, file, _, _ := runtime.Caller(0)
				raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../migrations/0233_payment_profit_sharing_description.sql"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = pool.Exec(ctx, string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			description := &frozenPurchaseDescription{value: "崔晓玲购买裂变商品分账"}
			if scenario == "invalid-text" {
				description.value = "bad\ntext"
			}
			s := NewService(uow, paymentstore.NewPostgreSQL(), orderStub{}, sessionStub{}, postgresRefundEffects{}, nil)
			if err = s.SetPaymentChannelAppIDs("wx-test", ""); err != nil {
				t.Fatal(err)
			}
			if err = s.SetProfitSharingEnabled(true); err != nil {
				t.Fatal(err)
			}
			s.SetCanonicalLineageReader(lineageStub{19})
			if err = s.SetProfitSharingIdentityReader(receiverRecoveryIdentityStub{value: identityport.VerifiedCommerceIdentity{IdentityID: 29, CustomerID: customerdomain.CustomerID(19), Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:wx-test", Value: "trusted-openid"}}); err != nil {
				t.Fatal(err)
			}
			if err = s.SetProfitSharingDescriptionReader(description); err != nil {
				t.Fatal(err)
			}
			var accepted paymentport.ProfitSharingInstruction
			abort := errors.New("abort description acceptance")
			err = uow.Within(ctx, func(tx context.Context) error {
				var err error
				accepted, err = s.AcceptProfitSharingWithin(tx, request)
				if err != nil {
					return err
				}
				if scenario == "rollback" {
					return abort
				}
				return nil
			})
			if scenario == "rollback" || scenario == "invalid-text" {
				if err == nil || scenario == "rollback" && !errors.Is(err, abort) || scenario == "invalid-text" && !errors.Is(err, paymentport.ErrInvalid) {
					t.Fatalf("expected rejection/rollback: %v", err)
				}
				for _, table := range []string{"payment_profit_sharing_instructions", "payment_profit_sharing_reserves", "payment_profit_sharing_provider_intents", "external_effects"} {
					var count int
					if err = pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
						t.Fatalf("partial acceptance leaked %s: %d %v", table, count, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected, expectedDigest := "崔晓玲购买裂变商品分账", effectport.Hash("payment.profit-sharing.description.v1", string(request.PayloadDigest), "崔晓玲购买裂变商品分账")
			if legacy {
				expected, expectedDigest = paymentport.LegacyProfitSharingDescription, request.PayloadDigest
				if accepted.Reference != fmt.Sprintf("psinst_%d", legacyID) || description.calls != 0 {
					t.Fatal("legacy replay read live description")
				}
			} else if description.orderID != orderID || description.buyerID != 11 || description.calls != 1 {
				t.Fatal("Payment supplied different buyer/order")
			}
			description.value = "另一昵称购买后来商品分账"
			err = uow.Within(ctx, func(tx context.Context) error {
				replay, err := s.AcceptProfitSharingWithin(tx, request)
				if err == nil && replay.Reference != accepted.Reference {
					return errors.New("replay created new instruction")
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			kind, material, err := s.LoadProfitSharingReferenceMaterial(ctx, accepted.Reference)
			if err != nil || kind != effectport.KindWeChatPayProfitSharing || material.Reason != expected || material.PayloadDigest != expectedDigest || material.AmountMinor != 204 || material.ReceiverAccount != "trusted-openid" {
				t.Fatalf("query material lost frozen data: err=%v", err)
			}
			if !legacy {
				material, err = s.LoadProfitSharingEffectMaterial(ctx, effectport.KindWeChatPayProfitSharing, request.SourceDigest)
				if err != nil || material.Reason != expected || material.PayloadDigest != expectedDigest {
					t.Fatalf("dispatch material lost frozen description: %v", err)
				}
				if description.calls != 1 {
					t.Fatal("replay or loader re-read changed nickname")
				}
				var count int
				if err = pool.QueryRow(ctx, `SELECT count(*) FROM external_effects WHERE payload_digest=$1`, expectedDigest).Scan(&count); err != nil || count != 1 {
					t.Fatal("effect fingerprint did not include description")
				}
			}
			changed := request
			changed.AmountMinor++
			if err = uow.Within(ctx, func(tx context.Context) error { _, err := s.AcceptProfitSharingWithin(tx, changed); return err }); err == nil {
				t.Fatal("mutated money command replay accepted")
			}
			if _, err = pool.Exec(ctx, `UPDATE payment_profit_sharing_instructions SET description_snapshot='changed' WHERE settlement_ref=$1`, request.SettlementRef); err == nil {
				t.Fatal("frozen description mutable")
			}
			if _, err = pool.Exec(ctx, `UPDATE payment_profit_sharing_instructions SET state='outcome_unknown',version=version+1 WHERE settlement_ref=$1`, request.SettlementRef); err != nil {
				t.Fatal("migration blocked ordinary outcome updates")
			}
			var amount, count int64
			if err = pool.QueryRow(ctx, `SELECT count(*),sum(amount_minor) FROM payment_profit_sharing_instructions`).Scan(&count, &amount); err != nil || count != 1 || amount != 204 {
				t.Fatal("migration/replay modified original amount/count")
			}
		})
	}
}
