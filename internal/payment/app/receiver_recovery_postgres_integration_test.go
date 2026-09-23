package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type receiverRecoveryIdentityStub struct {
	value identityport.VerifiedCommerceIdentity
}

func (stub receiverRecoveryIdentityStub) VerifiedPaymentIdentity(_ context.Context, id int64, kind identitydomain.Kind, scope string) (identityport.VerifiedCommerceIdentity, bool, error) {
	return stub.value, id == stub.value.IdentityID && kind == stub.value.Kind && scope == stub.value.Scope, nil
}

type receiverRecoveryEffects struct {
	postgresRefundEffects
	proofErr error
}

func (stub receiverRecoveryEffects) Get(_ context.Context, id string) (effectport.Projection, error) {
	return effectport.Projection{ID: id, Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayReceiverAdd, State: effectport.StateFinalFailed, AttemptCount: 1, UpdatedAt: time.Now().UTC()}, nil
}

func (stub receiverRecoveryEffects) FinalFailureWithoutExternalCallWithin(_ context.Context, effectID string, owner effectport.Owner, kind effectport.Kind) (effectport.FinalFailureWithoutExternalCallEvidence, error) {
	if stub.proofErr != nil {
		return effectport.FinalFailureWithoutExternalCallEvidence{}, stub.proofErr
	}
	if effectID == "" || owner != effectport.OwnerPayment || kind != effectport.KindWeChatPayReceiverAdd {
		return effectport.FinalFailureWithoutExternalCallEvidence{}, effectport.ErrReconciliationConflict
	}
	return effectport.FinalFailureWithoutExternalCallEvidence{Projection: effectport.Projection{ID: effectID, Owner: owner, Kind: kind, State: effectport.StateFinalFailed, AttemptCount: 1, UpdatedAt: time.Now().UTC()}, CompletedAt: time.Now().UTC()}, nil
}

type receiverRecoveryReconciler struct{}

func (receiverRecoveryReconciler) QueryProfitSharing(context.Context, string) (paymentport.ProfitSharingProviderResult, error) {
	return paymentport.ProfitSharingProviderResult{}, errors.New("unused")
}
func (receiverRecoveryReconciler) QueryProfitSharingUnfreeze(context.Context, string) (paymentport.ProfitSharingProviderResult, error) {
	return paymentport.ProfitSharingProviderResult{}, errors.New("unused")
}

func TestPostgreSQLProfitSharingReceiverRecoveryCreatesOneNewReviewedIntent(t *testing.T) {
	pool, cleanup := paymentAppIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 13, 0, 0, 0, time.UTC)
	var oldEffectID, receiverID int64
	if err = pool.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state,attempt_count,generation,updated_at) VALUES('payment',$1,$2,$3,$4,$5,$6,'final_failed',1,1,$7) RETURNING id`, effectport.KindWeChatPayReceiverAdd, effectport.Hash("recovery-old-source"), effectport.Hash("recovery-old-target"), effectport.Hash("recovery-old-payload"), effectport.Hash("recovery-old-policy"), effectport.Hash("recovery-old-envelope"), now).Scan(&oldEffectID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state,call_attempted,real_external_call_executed,completed_at) VALUES($1,1,1,1,'final_failed',false,false,$2)`, oldEffectID, now); err != nil {
		t.Fatal(err)
	}
	account := effectport.Hash("payment.profit-sharing.receiver.account.v1", "wx-recovery", "wechat-app:wx-recovery", "trusted-openid")
	if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,external_effect_id,version,created_at,updated_at) VALUES(81,91,'wx-recovery','wechat-app:wx-recovery','h5_official_account',$1,'final_failed',$2,2,$3,$3) RETURNING id`, account, oldEffectID, now).Scan(&receiverID); err != nil {
		t.Fatal(err)
	}
	effects := receiverRecoveryEffects{}
	service := NewService(uow, paymentstore.NewPostgreSQL(), orderStub{}, sessionStub{}, effects, effects)
	service.now = func() time.Time { return now.Add(time.Minute) }
	if err = service.SetProfitSharingEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err = service.SetPaymentChannelAppIDs("wx-mini-recovery", "wx-recovery"); err != nil {
		t.Fatal(err)
	}
	if err = service.SetProfitSharingIdentityReader(receiverRecoveryIdentityStub{value: identityport.VerifiedCommerceIdentity{IdentityID: 91, CustomerID: customerdomain.CustomerID(81), Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:wx-recovery", Value: "trusted-openid"}}); err != nil {
		t.Fatal(err)
	}
	service.SetCanonicalLineageReader(lineageStub{81})
	if err = service.SetProfitSharingReconciler(receiverRecoveryReconciler{}); err != nil {
		t.Fatal(err)
	}
	command := paymentport.ProfitSharingReceiverRecoveryCommand{ReceiverReference: fmt.Sprintf("psrecv_%d", receiverID), ActorAdminUserID: 17, IdempotencyKey: "receiver-recovery-key-0001", EvidenceReference: "review-2026-09-14"}
	first, err := service.RecoverProfitSharingReceiver(ctx, command)
	if err != nil || first.State != string(domain.ProfitSharingReceiverAccepted) || first.EffectRef == fmt.Sprintf("eer_%d", oldEffectID) || first.EffectRef == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := service.RecoverProfitSharingReceiver(ctx, command)
	if err != nil || second != first {
		t.Fatalf("replay=%+v first=%+v err=%v", second, first, err)
	}
	var state, oldState, actor, review string
	var effectsCount, receipts, intents, audits int
	if err = pool.QueryRow(ctx, `SELECT r.state,e.state,(SELECT count(*) FROM external_effects WHERE owner='payment' AND kind='wechat_pay_profit_sharing_receiver_v1'),(SELECT count(*) FROM payment_operation_receipts WHERE operation='receiver_recovery' AND result_kind='receiver' AND result_id=r.id),(SELECT count(*) FROM payment_profit_sharing_provider_intents WHERE receiver_id=r.id),(SELECT count(*) FROM payment_profit_sharing_audit_events WHERE aggregate_kind='receiver' AND aggregate_id=r.id AND event_type='payment.profit_sharing.receiver_recovery_accepted'),(SELECT actor_scope FROM payment_profit_sharing_audit_events WHERE aggregate_kind='receiver' AND aggregate_id=r.id AND event_type='payment.profit_sharing.receiver_recovery_accepted' ORDER BY id DESC),(SELECT payload->>'review' FROM payment_profit_sharing_audit_events WHERE aggregate_kind='receiver' AND aggregate_id=r.id AND event_type='payment.profit_sharing.receiver_recovery_accepted' ORDER BY id DESC) FROM payment_profit_sharing_receivers r JOIN external_effects e ON e.id=$2 WHERE r.id=$1`, receiverID, oldEffectID).Scan(&state, &oldState, &effectsCount, &receipts, &intents, &audits, &actor, &review); err != nil {
		t.Fatal(err)
	}
	if state != "accepted" || oldState != "final_failed" || effectsCount != 2 || receipts != 1 || intents != 1 || audits != 1 || actor != "admin:17" || review != "controlled_review_all_attempts_unexecuted_current_configuration_verified" {
		t.Fatalf("state=%q old=%q effects=%d receipts=%d intents=%d audits=%d actor=%q review=%q", state, oldState, effectsCount, receipts, intents, audits, actor, review)
	}
	// The recovery is not complete merely because its intent was accepted. The
	// independently executed EER worker must be able to resolve exactly that
	// persisted source/payload back to the current trusted receiver material.
	var source, payload string
	if err = pool.QueryRow(ctx, `SELECT source_ref_digest,payload_digest FROM payment_profit_sharing_provider_intents WHERE receiver_id=$1`, receiverID).Scan(&source, &payload); err != nil {
		t.Fatal(err)
	}
	material, err := service.LoadProfitSharingEffectMaterial(ctx, effectport.KindWeChatPayReceiverAdd, effectport.Digest(source))
	if err != nil {
		t.Fatalf("recovered receiver material source=%q: %v", source, err)
	}
	if string(material.PayloadDigest) != payload || material.AppID != "wx-recovery" || material.ReceiverAccount != "trusted-openid" {
		t.Fatalf("recovered material=%+v payload=%q", material, payload)
	}
	if _, err = service.RecoverProfitSharingReceiver(ctx, paymentport.ProfitSharingReceiverRecoveryCommand{ReceiverReference: command.ReceiverReference, ActorAdminUserID: command.ActorAdminUserID, IdempotencyKey: command.IdempotencyKey, EvidenceReference: "changed-review"}); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("evidence drift err=%v", err)
	}
	// A persisted receiver is bound to the configured channel AppID. A later
	// configuration change must be reviewed as a new identity/material path,
	// never reuse the old receiver account under a different application.
	if err = service.SetPaymentChannelAppIDs("wx-mini-recovery", "wx-recovery-replaced"); err != nil {
		t.Fatal(err)
	}
	changedKey := command
	changedKey.IdempotencyKey = "receiver-recovery-key-0002"
	if _, err = service.RecoverProfitSharingReceiver(ctx, changedKey); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("changed channel app id err=%v", err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM external_effects WHERE owner='payment' AND kind='wechat_pay_profit_sharing_receiver_v1'`).Scan(&effectsCount); err != nil || effectsCount != 2 {
		t.Fatalf("changed channel app id accepted an effect count=%d err=%v", effectsCount, err)
	}
}

func TestPostgreSQLProfitSharingReceiverRecoveryRejectsAnyUnprovenHistory(t *testing.T) {
	// The EER-owned proof has its own PostgreSQL coverage.  Payment must still
	// refuse before accepting a new effect when that proof reports any unknown
	// or externally attempted history.
	if !errors.Is(classify(effectport.ErrReconciliationConflict), paymentport.ErrConflict) {
		t.Fatal("recovery evidence conflict must remain a payment conflict")
	}
}
