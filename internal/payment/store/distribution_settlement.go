package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func (r *Repository) FindProfitSharingReceiver(ctx context.Context, customerID int64, appID string, lock bool) (domain.ProfitSharingReceiver, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingReceiver{}, false, err
	}
	query := `SELECT id,customer_id,identity_id,app_id,app_scope,channel,account_digest,failure_class,state,COALESCE('eer_'||external_effect_id::text,''),version,created_at,updated_at FROM payment_profit_sharing_receivers WHERE customer_id=$1 AND app_id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.ProfitSharingReceiver
	err = t.QueryRow(ctx, query, customerID, appID).Scan(&value.ID, &value.CustomerID, &value.IdentityID, &value.AppID, &value.AppScope, &value.Channel, &value.AccountDigest, &value.FailureClass, &value.State, &value.EffectID, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingReceiver{}, false, nil
	}
	return value, err == nil, mapError(err)
}

func (r *Repository) GetProfitSharingReceiver(ctx context.Context, id int64, lock bool) (domain.ProfitSharingReceiver, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingReceiver{}, err
	}
	query := `SELECT id,customer_id,identity_id,app_id,app_scope,channel,account_digest,failure_class,state,COALESCE('eer_'||external_effect_id::text,''),version,created_at,updated_at FROM payment_profit_sharing_receivers WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.ProfitSharingReceiver
	err = t.QueryRow(ctx, query, id).Scan(&value.ID, &value.CustomerID, &value.IdentityID, &value.AppID, &value.AppScope, &value.Channel, &value.AccountDigest, &value.FailureClass, &value.State, &value.EffectID, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingReceiver{}, paymentport.ErrNotFound
	}
	return value, mapError(err)
}

func (r *Repository) GetProfitSharingReceiverByEffect(ctx context.Context, effectRef string, lock bool) (domain.ProfitSharingReceiver, error) {
	effectID, err := effectNumeric(effectRef)
	if err != nil {
		return domain.ProfitSharingReceiver{}, paymentport.ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingReceiver{}, err
	}
	query := `SELECT id,customer_id,identity_id,app_id,app_scope,channel,account_digest,failure_class,state,COALESCE('eer_'||external_effect_id::text,''),version,created_at,updated_at FROM payment_profit_sharing_receivers WHERE external_effect_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.ProfitSharingReceiver
	err = t.QueryRow(ctx, query, effectID).Scan(&value.ID, &value.CustomerID, &value.IdentityID, &value.AppID, &value.AppScope, &value.Channel, &value.AccountDigest, &value.FailureClass, &value.State, &value.EffectID, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingReceiver{}, paymentport.ErrNotFound
	}
	return value, mapError(err)
}

func (r *Repository) UpdateProfitSharingReceiver(ctx context.Context, value domain.ProfitSharingReceiver, receipt string) (domain.ProfitSharingReceiver, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingReceiver{}, err
	}
	if !domain.ValidProfitSharingReceiverFailureClass(value.FailureClass) || (value.State != domain.ProfitSharingReceiverFinalFailed && value.FailureClass != "") {
		return domain.ProfitSharingReceiver{}, paymentport.ErrConflict
	}
	updated, err := t.Exec(ctx, `UPDATE payment_profit_sharing_receivers SET state=$2,failure_class=$3,version=$4,updated_at=$5 WHERE id=$1 AND version=$6`, value.ID, value.State, value.FailureClass, value.Version, value.UpdatedAt, value.Version-1)
	if err != nil || updated.RowsAffected() != 1 {
		if err != nil {
			return domain.ProfitSharingReceiver{}, mapError(err)
		}
		return domain.ProfitSharingReceiver{}, paymentport.ErrConflict
	}
	if err = profitSharingAudit(ctx, t, "receiver", value.ID, "payment.profit_sharing.receiver_"+string(value.State), "payment", value.UpdatedAt, map[string]any{"receipt": receipt, "failure_class": value.FailureClass}); err != nil {
		return domain.ProfitSharingReceiver{}, err
	}
	return value, nil
}

func (r *Repository) CreateProfitSharingReceiver(ctx context.Context, value domain.ProfitSharingReceiver) (domain.ProfitSharingReceiver, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingReceiver{}, false, err
	}
	if !domain.ValidProfitSharingReceiverFailureClass(value.FailureClass) || value.FailureClass != "" {
		return domain.ProfitSharingReceiver{}, false, paymentport.ErrConflict
	}
	err = t.QueryRow(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,failure_class,state,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, value.CustomerID, value.IdentityID, value.AppID, value.AppScope, value.Channel, value.AccountDigest, value.FailureClass, value.State, value.Version, value.CreatedAt, value.UpdatedAt).Scan(&value.ID)
	if err != nil {
		return domain.ProfitSharingReceiver{}, false, mapError(err)
	}
	if err = profitSharingAudit(ctx, t, "receiver", value.ID, "payment.profit_sharing.receiver_accepted", "distribution", value.CreatedAt, map[string]any{"customer_id": value.CustomerID, "app_id": value.AppID}); err != nil {
		return domain.ProfitSharingReceiver{}, false, err
	}
	return value, true, nil
}

func (r *Repository) BindProfitSharingReceiverEffect(ctx context.Context, value domain.ProfitSharingReceiver, intent effectport.PaymentV1Intent) (domain.ProfitSharingReceiver, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingReceiver{}, err
	}
	effectID, err := effectNumeric(value.EffectID)
	if err != nil {
		return domain.ProfitSharingReceiver{}, paymentport.ErrConflict
	}
	updated, err := t.Exec(ctx, `UPDATE payment_profit_sharing_receivers SET external_effect_id=$2,version=$3,updated_at=$4 WHERE id=$1 AND external_effect_id IS NULL AND version=$5`, value.ID, effectID, value.Version, value.UpdatedAt, value.Version-1)
	if err != nil || updated.RowsAffected() != 1 {
		if err != nil {
			return domain.ProfitSharingReceiver{}, mapError(err)
		}
		return domain.ProfitSharingReceiver{}, paymentport.ErrConflict
	}
	if err = insertProfitSharingIntent(ctx, t, value.ID, 0, 0, intent, value.UpdatedAt); err != nil {
		return domain.ProfitSharingReceiver{}, err
	}
	if err = profitSharingAudit(ctx, t, "receiver", value.ID, "payment.profit_sharing.receiver_effect_accepted", "payment", value.UpdatedAt, map[string]any{"effect_id": value.EffectID}); err != nil {
		return domain.ProfitSharingReceiver{}, err
	}
	return value, nil
}

// FindProfitSharingReceiverRecoveryWithin replays an immutable administrator
// recovery receipt.  It deliberately loads the receiver from Payment storage
// instead of trusting a caller-supplied result ID.
func (r *Repository) FindProfitSharingReceiverRecoveryWithin(ctx context.Context, actorScope string, key, payload [32]byte) (domain.ProfitSharingReceiver, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingReceiver{}, false, err
	}
	var (
		resultID   int64
		oldPayload []byte
		resultKind string
	)
	err = t.QueryRow(ctx, `SELECT result_id,payload_digest,result_kind FROM payment_operation_receipts WHERE operation='receiver_recovery' AND actor_scope=$1 AND key_digest=$2 FOR UPDATE`, actorScope, key[:]).Scan(&resultID, &oldPayload, &resultKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingReceiver{}, false, nil
	}
	if err != nil {
		return domain.ProfitSharingReceiver{}, false, mapError(err)
	}
	if len(oldPayload) != len(payload) || string(oldPayload) != string(payload[:]) || resultKind != "receiver" || resultID < 1 {
		return domain.ProfitSharingReceiver{}, false, paymentport.ErrConflict
	}
	value, err := r.GetProfitSharingReceiver(ctx, resultID, false)
	if err != nil {
		return domain.ProfitSharingReceiver{}, false, err
	}
	return value, true, nil
}

// RecoverProfitSharingReceiverEffectWithin records a reviewed replacement
// receiver-add intent.  The old final-failed effect is only used as a CAS
// precondition and remains immutable.  The caller proves its full attempt
// history and accepts the new EER intent in this same Unit of Work first.
func (r *Repository) RecoverProfitSharingReceiverEffectWithin(ctx context.Context, value domain.ProfitSharingReceiver, oldEffect string, intent effectport.PaymentV1Intent, actorScope, evidenceReference string, key, payload [32]byte) (domain.ProfitSharingReceiver, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingReceiver{}, false, err
	}
	if value.ID < 1 || value.State != domain.ProfitSharingReceiverAccepted || actorScope == "" || evidenceReference == "" {
		return domain.ProfitSharingReceiver{}, false, paymentport.ErrInvalid
	}
	oldEffectID, err := effectNumeric(oldEffect)
	if err != nil {
		return domain.ProfitSharingReceiver{}, false, paymentport.ErrConflict
	}
	newEffectID, err := effectNumeric(value.EffectID)
	if err != nil || newEffectID == oldEffectID {
		return domain.ProfitSharingReceiver{}, false, paymentport.ErrConflict
	}
	value.FailureClass = ""

	// The payment receipt is inserted before changing the receiver.  An
	// identical concurrent command cannot create a second effect because the
	// app uses the same actor/key-derived EER receipt key; a cross-receiver key
	// reuse reaches this conflict before this UoW can commit.
	var receiptResultID int64
	err = t.QueryRow(ctx, `INSERT INTO payment_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at) VALUES('receiver_recovery',$1,$2,$3,'receiver',$4,$5) ON CONFLICT(operation,actor_scope,key_digest) DO NOTHING RETURNING result_id`, actorScope, key[:], payload[:], value.ID, value.UpdatedAt).Scan(&receiptResultID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingReceiver{}, false, mapError(err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		replay, found, inner := r.FindProfitSharingReceiverRecoveryWithin(ctx, actorScope, key, payload)
		if inner != nil {
			return domain.ProfitSharingReceiver{}, false, inner
		}
		if !found || replay.ID != value.ID {
			return domain.ProfitSharingReceiver{}, false, paymentport.ErrConflict
		}
		return replay, true, nil
	}
	if receiptResultID != value.ID {
		return domain.ProfitSharingReceiver{}, false, paymentport.ErrConflict
	}

	updated, err := t.Exec(ctx, `UPDATE payment_profit_sharing_receivers SET state=$2,failure_class='',external_effect_id=$3,version=$4,updated_at=$5 WHERE id=$1 AND state='final_failed' AND external_effect_id=$6 AND version=$7`, value.ID, value.State, newEffectID, value.Version, value.UpdatedAt, oldEffectID, value.Version-1)
	if err != nil {
		return domain.ProfitSharingReceiver{}, false, mapError(err)
	}
	if updated.RowsAffected() != 1 {
		return domain.ProfitSharingReceiver{}, false, paymentport.ErrConflict
	}
	if err = insertProfitSharingIntent(ctx, t, value.ID, 0, 0, intent, value.UpdatedAt); err != nil {
		return domain.ProfitSharingReceiver{}, false, err
	}
	if err = profitSharingAudit(ctx, t, "receiver", value.ID, "payment.profit_sharing.receiver_recovery_accepted", actorScope, value.UpdatedAt, map[string]any{
		"old_effect_id":      oldEffect,
		"new_effect_id":      value.EffectID,
		"evidence_reference": evidenceReference,
		"review":             "controlled_review_all_attempts_unexecuted_current_configuration_verified",
	}); err != nil {
		return domain.ProfitSharingReceiver{}, false, err
	}
	return value, false, nil
}

func (r *Repository) PaymentForDistributionOrder(ctx context.Context, orderID int64, lock bool) (domain.Payment, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.Payment{}, err
	}
	query := `SELECT id FROM payments WHERE order_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var id int64
	err = t.QueryRow(ctx, query, orderID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Payment{}, paymentport.ErrNotFound
	}
	if err != nil {
		return domain.Payment{}, mapError(err)
	}
	return r.GetPayment(ctx, id, false)
}

func (r *Repository) ProfitSharingFunding(ctx context.Context, paymentID int64) (domain.ProfitSharingFunding, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingFunding{}, err
	}
	var value domain.ProfitSharingFunding
	err = t.QueryRow(ctx, `SELECT
 COALESCE(sum(amount_minor) FILTER(WHERE status='completed'),0)::bigint,
 COALESCE(sum(amount_minor) FILTER(WHERE status IN ('requested','history_requested')),0)::bigint,
 COALESCE(sum(amount_minor) FILTER(WHERE status IN ('effect_accepted','history_processing')),0)::bigint,
 COALESCE(sum(amount_minor) FILTER(WHERE status='outcome_unknown'),0)::bigint
 FROM payment_refunds WHERE payment_id=$1`, paymentID).Scan(&value.SuccessfulRefundMinor, &value.RequestedRefundMinor, &value.ProcessingRefundMinor, &value.OutcomeUnknownRefundMinor)
	if err != nil {
		return domain.ProfitSharingFunding{}, mapError(err)
	}
	err = t.QueryRow(ctx, `SELECT COALESCE(sum(amount_minor),0)::bigint FROM payment_profit_sharing_reserves WHERE payment_id=$1 AND state='reserved'`, paymentID).Scan(&value.ReservedSplitMinor)
	return value, mapError(err)
}

func (r *Repository) ReservedProfitSharingMinor(ctx context.Context, paymentID int64) (int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return 0, err
	}
	var amount int64
	err = t.QueryRow(ctx, `SELECT COALESCE(sum(amount_minor),0)::bigint FROM payment_profit_sharing_reserves WHERE payment_id=$1 AND state='reserved'`, paymentID).Scan(&amount)
	return amount, mapError(err)
}

func (r *Repository) FindProfitSharingBySettlement(ctx context.Context, settlement string, lock bool) (domain.ProfitSharingInstruction, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingInstruction{}, false, err
	}
	query := `SELECT id FROM payment_profit_sharing_instructions WHERE settlement_ref=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var id int64
	err = t.QueryRow(ctx, query, settlement).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingInstruction{}, false, nil
	}
	if err != nil {
		return domain.ProfitSharingInstruction{}, false, mapError(err)
	}
	value, err := r.getProfitSharingInstructionByID(ctx, id, false)
	return value, err == nil, err
}

func (r *Repository) GetProfitSharingInstruction(ctx context.Context, reference string, lock bool) (domain.ProfitSharingInstruction, error) {
	id, ok := instructionID(reference)
	if !ok {
		return domain.ProfitSharingInstruction{}, paymentport.ErrInvalid
	}
	return r.getProfitSharingInstructionByID(ctx, id, lock)
}

func (r *Repository) GetProfitSharingInstructionByEffect(ctx context.Context, effectRef string, lock bool) (domain.ProfitSharingInstruction, error) {
	effectID, err := effectNumeric(effectRef)
	if err != nil {
		return domain.ProfitSharingInstruction{}, paymentport.ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingInstruction{}, err
	}
	query := `SELECT id FROM payment_profit_sharing_instructions WHERE external_effect_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var id int64
	if err = t.QueryRow(ctx, query, effectID).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingInstruction{}, paymentport.ErrNotFound
	} else if err != nil {
		return domain.ProfitSharingInstruction{}, mapError(err)
	}
	return r.getProfitSharingInstructionByID(ctx, id, false)
}

func (r *Repository) getProfitSharingInstructionByID(ctx context.Context, id int64, lock bool) (domain.ProfitSharingInstruction, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingInstruction{}, err
	}
	query := `SELECT id,payment_id,receiver_id,settlement_ref,provider_order_no,idempotency_key_digest,source_ref_digest,payload_digest,policy_version_hash,amount_minor,currency,state,failure_class,COALESCE('eer_'||external_effect_id::text,''),deadline_at,receiver_confirmed_success,outcome_known,version,created_at,updated_at FROM payment_profit_sharing_instructions WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.ProfitSharingInstruction
	err = t.QueryRow(ctx, query, id).Scan(&value.ID, &value.PaymentID, &value.ReceiverID, &value.SettlementRef, &value.ProviderOrderNo, &value.IdempotencyKeyDigest, &value.SourceRefDigest, &value.PayloadDigest, &value.PolicyVersionHash, &value.AmountMinor, &value.Currency, &value.State, &value.FailureClass, &value.EffectID, &value.DeadlineAt, &value.ReceiverConfirmedSuccess, &value.OutcomeKnown, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingInstruction{}, paymentport.ErrNotFound
	}
	return value, mapError(err)
}

func (r *Repository) CreateProfitSharingInstruction(ctx context.Context, value domain.ProfitSharingInstruction) (domain.ProfitSharingInstruction, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingInstruction{}, false, err
	}
	err = t.QueryRow(ctx, `INSERT INTO payment_profit_sharing_instructions(settlement_ref,payment_id,receiver_id,provider_order_no,idempotency_key_digest,source_ref_digest,payload_digest,policy_version_hash,amount_minor,currency,state,deadline_at,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING id`, value.SettlementRef, value.PaymentID, value.ReceiverID, value.ProviderOrderNo, value.IdempotencyKeyDigest, value.SourceRefDigest, value.PayloadDigest, value.PolicyVersionHash, value.AmountMinor, value.Currency, value.State, value.DeadlineAt, value.Version, value.CreatedAt, value.UpdatedAt).Scan(&value.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingInstruction{}, false, paymentport.ErrConflict
	}
	if err != nil {
		// The caller locks matching settlement references before this insert. A
		// unique violation is therefore a cross-settlement receiver/payment race.
		return domain.ProfitSharingInstruction{}, false, mapError(err)
	}
	if _, err = t.Exec(ctx, `INSERT INTO payment_profit_sharing_reserves(instruction_id,payment_id,amount_minor,state,created_at) VALUES($1,$2,$3,'reserved',$4)`, value.ID, value.PaymentID, value.AmountMinor, value.CreatedAt); err != nil {
		return domain.ProfitSharingInstruction{}, false, mapError(err)
	}
	if err = profitSharingAudit(ctx, t, "instruction", value.ID, "payment.profit_sharing.instruction_reserved", "distribution", value.CreatedAt, map[string]any{"payment_id": value.PaymentID, "amount_minor": value.AmountMinor}); err != nil {
		return domain.ProfitSharingInstruction{}, false, err
	}
	return value, true, nil
}

func (r *Repository) BindProfitSharingInstructionEffect(ctx context.Context, value domain.ProfitSharingInstruction, intent effectport.PaymentV1Intent) (domain.ProfitSharingInstruction, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingInstruction{}, err
	}
	effectID, err := effectNumeric(value.EffectID)
	if err != nil {
		return domain.ProfitSharingInstruction{}, paymentport.ErrConflict
	}
	updated, err := t.Exec(ctx, `UPDATE payment_profit_sharing_instructions SET external_effect_id=$2,version=$3,updated_at=$4 WHERE id=$1 AND external_effect_id IS NULL AND version=$5`, value.ID, effectID, value.Version, value.UpdatedAt, value.Version-1)
	if err != nil || updated.RowsAffected() != 1 {
		if err != nil {
			return domain.ProfitSharingInstruction{}, mapError(err)
		}
		return domain.ProfitSharingInstruction{}, paymentport.ErrConflict
	}
	if err = insertProfitSharingIntent(ctx, t, 0, value.ID, 0, intent, value.UpdatedAt); err != nil {
		return domain.ProfitSharingInstruction{}, err
	}
	if err = profitSharingAudit(ctx, t, "instruction", value.ID, "payment.profit_sharing.instruction_effect_accepted", "payment", value.UpdatedAt, map[string]any{"effect_id": value.EffectID}); err != nil {
		return domain.ProfitSharingInstruction{}, err
	}
	return value, nil
}

func (r *Repository) UpdateProfitSharingInstruction(ctx context.Context, value domain.ProfitSharingInstruction, receipt string) (domain.ProfitSharingInstruction, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingInstruction{}, err
	}
	if !domain.ValidProfitSharingInstructionFailureClass(value.FailureClass) || (value.State != domain.ProfitSharingException && value.FailureClass != "") {
		return domain.ProfitSharingInstruction{}, paymentport.ErrConflict
	}
	updated, err := t.Exec(ctx, `UPDATE payment_profit_sharing_instructions SET state=$2,failure_class=$3,receiver_confirmed_success=$4,outcome_known=$5,version=$6,updated_at=$7 WHERE id=$1 AND version=$8`, value.ID, value.State, value.FailureClass, value.ReceiverConfirmedSuccess, value.OutcomeKnown, value.Version, value.UpdatedAt, value.Version-1)
	if err != nil || updated.RowsAffected() != 1 {
		if err != nil {
			return domain.ProfitSharingInstruction{}, mapError(err)
		}
		return domain.ProfitSharingInstruction{}, paymentport.ErrConflict
	}
	if err = profitSharingAudit(ctx, t, "instruction", value.ID, "payment.profit_sharing.instruction_"+string(value.State), "payment", value.UpdatedAt, map[string]any{"receipt": receipt, "failure_class": value.FailureClass}); err != nil {
		return domain.ProfitSharingInstruction{}, err
	}
	return value, nil
}

func (r *Repository) ReleaseProfitSharingReserve(ctx context.Context, instructionID int64, reason string, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	result, err := t.Exec(ctx, `UPDATE payment_profit_sharing_reserves SET state='released',released_reason=$2,released_at=$3 WHERE instruction_id=$1 AND state='reserved'`, instructionID, reason, now)
	if err != nil {
		return mapError(err)
	}
	if result.RowsAffected() == 0 {
		var state string
		err = t.QueryRow(ctx, `SELECT state FROM payment_profit_sharing_reserves WHERE instruction_id=$1`, instructionID).Scan(&state)
		if err != nil {
			return mapError(err)
		}
		if state != "released" {
			return paymentport.ErrConflict
		}
	}
	return nil
}

func (r *Repository) FindProfitSharingUnfreeze(ctx context.Context, paymentID int64, lock bool) (domain.ProfitSharingUnfreeze, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingUnfreeze{}, false, err
	}
	query := `SELECT id,payment_id,provider_order_no,reason,idempotency_key_digest,source_ref_digest,payload_digest,policy_version_hash,state,COALESCE('eer_'||external_effect_id::text,''),outcome_known,version,created_at,updated_at FROM payment_profit_sharing_unfreezes WHERE payment_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.ProfitSharingUnfreeze
	err = t.QueryRow(ctx, query, paymentID).Scan(&value.ID, &value.PaymentID, &value.ProviderOrderNo, &value.Reason, &value.IdempotencyKeyDigest, &value.SourceRefDigest, &value.PayloadDigest, &value.PolicyVersionHash, &value.State, &value.EffectID, &value.OutcomeKnown, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingUnfreeze{}, false, nil
	}
	return value, err == nil, mapError(err)
}

func (r *Repository) GetProfitSharingUnfreezeByReference(ctx context.Context, reference string, lock bool) (domain.ProfitSharingUnfreeze, error) {
	if !strings.HasPrefix(reference, "psunfreeze_") {
		return domain.ProfitSharingUnfreeze{}, paymentport.ErrInvalid
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(reference, "psunfreeze_"), 10, 64)
	if err != nil || id < 1 || reference != "psunfreeze_"+strconv.FormatInt(id, 10) {
		return domain.ProfitSharingUnfreeze{}, paymentport.ErrInvalid
	}
	return r.getProfitSharingUnfreezeByID(ctx, id, lock)
}

func (r *Repository) GetProfitSharingUnfreezeByEffect(ctx context.Context, effectRef string, lock bool) (domain.ProfitSharingUnfreeze, error) {
	effectID, err := effectNumeric(effectRef)
	if err != nil {
		return domain.ProfitSharingUnfreeze{}, paymentport.ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingUnfreeze{}, err
	}
	query := `SELECT id FROM payment_profit_sharing_unfreezes WHERE external_effect_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var id int64
	if err = t.QueryRow(ctx, query, effectID).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingUnfreeze{}, paymentport.ErrNotFound
	} else if err != nil {
		return domain.ProfitSharingUnfreeze{}, mapError(err)
	}
	return r.getProfitSharingUnfreezeByID(ctx, id, false)
}

func (r *Repository) getProfitSharingUnfreezeByID(ctx context.Context, id int64, lock bool) (domain.ProfitSharingUnfreeze, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingUnfreeze{}, err
	}
	query := `SELECT id,payment_id,provider_order_no,reason,idempotency_key_digest,source_ref_digest,payload_digest,policy_version_hash,state,COALESCE('eer_'||external_effect_id::text,''),outcome_known,version,created_at,updated_at FROM payment_profit_sharing_unfreezes WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.ProfitSharingUnfreeze
	err = t.QueryRow(ctx, query, id).Scan(&value.ID, &value.PaymentID, &value.ProviderOrderNo, &value.Reason, &value.IdempotencyKeyDigest, &value.SourceRefDigest, &value.PayloadDigest, &value.PolicyVersionHash, &value.State, &value.EffectID, &value.OutcomeKnown, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingUnfreeze{}, paymentport.ErrNotFound
	}
	return value, mapError(err)
}

func (r *Repository) CreateProfitSharingUnfreeze(ctx context.Context, value domain.ProfitSharingUnfreeze) (domain.ProfitSharingUnfreeze, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingUnfreeze{}, false, err
	}
	err = t.QueryRow(ctx, `INSERT INTO payment_profit_sharing_unfreezes(payment_id,provider_order_no,reason,idempotency_key_digest,source_ref_digest,payload_digest,policy_version_hash,state,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, value.PaymentID, value.ProviderOrderNo, value.Reason, value.IdempotencyKeyDigest, value.SourceRefDigest, value.PayloadDigest, value.PolicyVersionHash, value.State, value.Version, value.CreatedAt, value.UpdatedAt).Scan(&value.ID)
	if err != nil {
		return domain.ProfitSharingUnfreeze{}, false, mapError(err)
	}
	return value, true, profitSharingAudit(ctx, t, "instruction", value.ID, "payment.profit_sharing.unfreeze_accepted", "payment", value.CreatedAt, map[string]any{"payment_id": value.PaymentID})
}

func (r *Repository) BindProfitSharingUnfreezeEffect(ctx context.Context, value domain.ProfitSharingUnfreeze, intent effectport.PaymentV1Intent) (domain.ProfitSharingUnfreeze, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingUnfreeze{}, err
	}
	effectID, err := effectNumeric(value.EffectID)
	if err != nil {
		return domain.ProfitSharingUnfreeze{}, paymentport.ErrConflict
	}
	updated, err := t.Exec(ctx, `UPDATE payment_profit_sharing_unfreezes SET external_effect_id=$2,version=$3,updated_at=$4 WHERE id=$1 AND external_effect_id IS NULL AND version=$5`, value.ID, effectID, value.Version, value.UpdatedAt, value.Version-1)
	if err != nil || updated.RowsAffected() != 1 {
		if err != nil {
			return domain.ProfitSharingUnfreeze{}, mapError(err)
		}
		return domain.ProfitSharingUnfreeze{}, paymentport.ErrConflict
	}
	if err = insertProfitSharingIntent(ctx, t, 0, 0, value.ID, intent, value.UpdatedAt); err != nil {
		return domain.ProfitSharingUnfreeze{}, err
	}
	return value, profitSharingAudit(ctx, t, "instruction", value.ID, "payment.profit_sharing.unfreeze_effect_accepted", "payment", value.UpdatedAt, map[string]any{"effect_id": value.EffectID})
}

func (r *Repository) UpdateProfitSharingUnfreeze(ctx context.Context, value domain.ProfitSharingUnfreeze, receipt string) (domain.ProfitSharingUnfreeze, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingUnfreeze{}, err
	}
	updated, err := t.Exec(ctx, `UPDATE payment_profit_sharing_unfreezes SET state=$2,outcome_known=$3,version=$4,updated_at=$5 WHERE id=$1 AND version=$6`, value.ID, value.State, value.OutcomeKnown, value.Version, value.UpdatedAt, value.Version-1)
	if err != nil || updated.RowsAffected() != 1 {
		if err != nil {
			return domain.ProfitSharingUnfreeze{}, mapError(err)
		}
		return domain.ProfitSharingUnfreeze{}, paymentport.ErrConflict
	}
	if err = profitSharingAudit(ctx, t, "instruction", value.ID, "payment.profit_sharing.unfreeze_"+value.State, "payment", value.UpdatedAt, map[string]any{"receipt": receipt}); err != nil {
		return domain.ProfitSharingUnfreeze{}, err
	}
	return value, nil
}

func (r *Repository) FindProfitSharingProviderIntent(ctx context.Context, kind effectport.Kind, source effectport.Digest) (domain.ProfitSharingProviderIntent, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.ProfitSharingProviderIntent{}, err
	}
	var value domain.ProfitSharingProviderIntent
	err = t.QueryRow(ctx, `SELECT COALESCE(receiver_id,0),COALESCE(instruction_id,0),COALESCE(unfreeze_id,0),payload_digest FROM payment_profit_sharing_provider_intents WHERE effect_kind=$1 AND source_ref_digest=$2`, kind, source).Scan(&value.ReceiverID, &value.InstructionID, &value.UnfreezeID, &value.PayloadDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfitSharingProviderIntent{}, paymentport.ErrNotFound
	}
	if err != nil {
		return domain.ProfitSharingProviderIntent{}, mapError(err)
	}
	if !hasExactlyOneProfitSharingIntentOwner(value.ReceiverID, value.InstructionID, value.UnfreezeID) {
		return domain.ProfitSharingProviderIntent{}, paymentport.ErrConflict
	}
	return value, nil
}

// hasExactlyOneProfitSharingIntentOwner enforces the table's polymorphic
// owner invariant. A recovered receiver effect legitimately leaves both other
// owner columns NULL, so this must count populated columns rather than compare
// each pair for equality.
func hasExactlyOneProfitSharingIntentOwner(receiverID, instructionID, unfreezeID int64) bool {
	owners := 0
	for _, id := range []int64{receiverID, instructionID, unfreezeID} {
		if id > 0 {
			owners++
		}
	}
	return owners == 1
}

func insertProfitSharingIntent(ctx context.Context, t pgx.Tx, receiverID, instructionID, unfreezeID int64, intent effectport.PaymentV1Intent, now time.Time) error {
	raw, _ := json.Marshal(map[string]any{"receiver_id": receiverID, "instruction_id": instructionID, "unfreeze_id": unfreezeID})
	_, err := t.Exec(ctx, `INSERT INTO payment_profit_sharing_provider_intents(receiver_id,instruction_id,unfreeze_id,effect_kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,request_snapshot,created_at) VALUES(NULLIF($1,0),NULLIF($2,0),NULLIF($3,0),$4,$5,$6,$7,$8,$9,$10)`, receiverID, instructionID, unfreezeID, intent.Kind, intent.SourceRefDigest, intent.TargetRefDigest, intent.PayloadDigest, intent.PolicyVersionHash, raw, now)
	return mapError(err)
}

func profitSharingAudit(ctx context.Context, t pgx.Tx, kind string, id int64, event, actor string, at time.Time, payload map[string]any) error {
	raw, _ := json.Marshal(payload)
	_, err := t.Exec(ctx, `INSERT INTO payment_profit_sharing_audit_events(aggregate_kind,aggregate_id,event_type,actor_scope,payload,occurred_at) VALUES($1,$2,$3,$4,$5,$6)`, kind, id, event, actor, raw, at)
	return mapError(err)
}

func instructionID(reference string) (int64, bool) {
	if !strings.HasPrefix(reference, "psinst_") {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(reference, "psinst_"), 10, 64)
	return id, err == nil && id > 0 && reference == "psinst_"+strconv.FormatInt(id, 10)
}
