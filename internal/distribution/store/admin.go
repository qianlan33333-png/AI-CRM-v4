package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

// AdminExceptionDetail stays inside the Distribution store. It contains only
// Distribution-owned frozen facts and opaque Payment instruction references.
type AdminExceptionDetail struct {
	ID, CommissionID, SettlementID                                int64
	Kind, Status, Reason, EvidenceReference, InstructionReference string
	ReconcileTarget                                               distributionport.AdminReconcileTarget
	UnpaidDueMinor, AlreadyPaidMinor, AmountMinor                 int64
	Version                                                       int64
}

type OperationReceipt struct {
	PayloadDigest [sha256.Size]byte
	ResultKind    string
	ResultID      int64
}

func (r *Repository) SetDistributorEnabledWithin(ctx context.Context, distributorID, expectedVersion int64, enabled bool, at time.Time) (distributiondomain.Distributor, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Distributor{}, err
	}
	if distributorID < 1 || expectedVersion < 1 || at.IsZero() {
		return distributiondomain.Distributor{}, ErrInvalid
	}
	var d distributiondomain.Distributor
	err = tx.QueryRow(ctx, `UPDATE distribution_distributors SET enabled=$2,version=version+1,updated_at=$3 WHERE id=$1 AND version=$4 RETURNING id,customer_id,public_no,agreement_version,enabled,registered_at,version`, distributorID, enabled, at.UTC(), expectedVersion).Scan(&d.ID, &d.CustomerID, &d.PublicNo, &d.AgreementVersion, &d.Enabled, &d.RegisteredAt, &d.Version)
	if err != nil {
		return distributiondomain.Distributor{}, distributionport.ErrConflict
	}
	if !d.Valid() {
		return distributiondomain.Distributor{}, distributionport.ErrUnavailable
	}
	return d, nil
}

func (r *Repository) ReadAdminExceptionWithin(ctx context.Context, exceptionID int64, lock bool) (AdminExceptionDetail, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return AdminExceptionDetail{}, err
	}
	if exceptionID < 1 {
		return AdminExceptionDetail{}, ErrInvalid
	}
	query := `SELECT e.id,e.commission_id,COALESCE(e.settlement_id,0),e.kind,e.status,e.reason,e.evidence_reference,COALESCE(s.payment_instruction_reference,''),e.unpaid_due_minor,e.already_paid_minor,e.amount_minor,e.version FROM distribution_exceptions e LEFT JOIN distribution_settlements s ON s.id=e.settlement_id WHERE e.id=$1`
	if lock {
		// The optional settlement join must not be locked: PostgreSQL rejects a
		// bare FOR UPDATE over its nullable side, and the caller has already
		// locked the owning commission before taking this exception lock.
		query += " FOR UPDATE OF e"
	}
	return scanAdminException(tx.QueryRow(ctx, query, exceptionID))
}

func scanAdminException(row rowScanner) (AdminExceptionDetail, error) {
	var value AdminExceptionDetail
	err := row.Scan(&value.ID, &value.CommissionID, &value.SettlementID, &value.Kind, &value.Status, &value.Reason, &value.EvidenceReference, &value.InstructionReference, &value.UnpaidDueMinor, &value.AlreadyPaidMinor, &value.AmountMinor, &value.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminExceptionDetail{}, distributionport.ErrNotFound
	}
	if err != nil {
		return AdminExceptionDetail{}, mapError(err)
	}
	if value.ID < 1 || value.CommissionID < 1 || value.SettlementID < 0 || !validExceptionKind(value.Kind) || !validExceptionStatus(value.Status) || value.Reason != strings.TrimSpace(value.Reason) || value.Reason == "" || len(value.Reason) > 500 || value.EvidenceReference != strings.TrimSpace(value.EvidenceReference) || len(value.EvidenceReference) > 500 || value.InstructionReference != strings.TrimSpace(value.InstructionReference) || len(value.InstructionReference) > 200 || value.Version < 1 || value.AmountMinor < 0 || value.UnpaidDueMinor < 0 || value.AlreadyPaidMinor < 0 {
		return AdminExceptionDetail{}, distributionport.ErrUnavailable
	}
	value.ReconcileTarget = adminReconcileTarget(value.Kind, value.EvidenceReference, value.InstructionReference)
	return value, nil
}

func adminReconcileTarget(kind, evidenceReference, instructionReference string) distributionport.AdminReconcileTarget {
	if kind == "unfreeze_final_failed" && validUnfreezeReference(evidenceReference) {
		return distributionport.AdminReconcileTargetUnfreeze
	}
	if validInstructionReference(instructionReference) {
		return distributionport.AdminReconcileTargetSplit
	}
	return distributionport.AdminReconcileTargetNone
}

func validUnfreezeReference(value string) bool {
	if !strings.HasPrefix(value, "psunfreeze_") {
		return false
	}
	for _, character := range value[len("psunfreeze_"):] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return len(value) > len("psunfreeze_")
}

func validInstructionReference(value string) bool {
	if !strings.HasPrefix(value, "psinst_") {
		return false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(value, "psinst_"), 10, 64)
	return err == nil && id > 0 && value == "psinst_"+strconv.FormatInt(id, 10)
}

func (r *Repository) UpdateAdminExceptionWithin(ctx context.Context, value AdminExceptionDetail, expectedVersion int64, at time.Time) (AdminExceptionDetail, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return AdminExceptionDetail{}, err
	}
	if value.ID < 1 || expectedVersion < 1 || value.Version != expectedVersion+1 || !validExceptionStatus(value.Status) || value.Reason != strings.TrimSpace(value.Reason) || value.Reason == "" || len(value.Reason) > 500 || value.EvidenceReference != strings.TrimSpace(value.EvidenceReference) || len(value.EvidenceReference) > 500 || value.AmountMinor < 0 || at.IsZero() {
		return AdminExceptionDetail{}, ErrInvalid
	}
	err = tx.QueryRow(ctx, `UPDATE distribution_exceptions SET status=$2,reason=$3,evidence_reference=$4,amount_minor=$5,version=$6,updated_at=$7 WHERE id=$1 AND version=$8 RETURNING id,commission_id,COALESCE(settlement_id,0),status,reason,evidence_reference,amount_minor,version`, value.ID, value.Status, value.Reason, value.EvidenceReference, value.AmountMinor, value.Version, at.UTC(), expectedVersion).Scan(&value.ID, &value.CommissionID, &value.SettlementID, &value.Status, &value.Reason, &value.EvidenceReference, &value.AmountMinor, &value.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminExceptionDetail{}, distributionport.ErrConflict
	}
	if err != nil {
		return AdminExceptionDetail{}, mapError(err)
	}
	return value, nil
}

// SumRecordedAfterSalesHandlingWithin is scoped to one locked commission. It
// reads the append-only adjustment ledger rather than mutable exception
// snapshots, so recovery and merchant-liability records cannot overwrite a
// later business-cancellation proof or evade their shared paid delta cap.
func (r *Repository) SumRecordedAfterSalesHandlingWithin(ctx context.Context, commissionID int64) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	if commissionID < 1 {
		return 0, ErrInvalid
	}
	var total int64
	err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(delta_minor),0) FROM distribution_commission_adjustments WHERE commission_id=$1 AND kind IN ('manual_recovery','merchant_liability')`, commissionID).Scan(&total)
	if err != nil {
		return 0, mapError(err)
	}
	if total < 0 {
		return 0, distributionport.ErrUnavailable
	}
	return total, nil
}

func (r *Repository) ReadOperationReceiptWithin(ctx context.Context, operation, actorScope, idempotencyKey string) (OperationReceipt, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return OperationReceipt{}, false, err
	}
	if operation == "" || actorScope == "" || !validAdminIdempotencyKey(idempotencyKey) {
		return OperationReceipt{}, false, ErrInvalid
	}
	keyDigest := sha256.Sum256([]byte(idempotencyKey))
	result, err := scanOperationReceipt(tx.QueryRow(ctx, `SELECT payload_digest,result_kind,result_id FROM distribution_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3`, operation, actorScope, keyDigest[:]))
	if errors.Is(err, distributionport.ErrNotFound) {
		return OperationReceipt{}, false, nil
	}
	if err != nil {
		return OperationReceipt{}, false, mapError(err)
	}
	return result, true, nil
}

// LockOperationReceiptWithin serializes competing commands for one immutable
// receipt key. It holds only a PostgreSQL transaction advisory lock and never
// stores the raw browser idempotency key.
func (r *Repository) LockOperationReceiptWithin(ctx context.Context, operation, actorScope, idempotencyKey string) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if operation == "" || actorScope == "" || !validAdminIdempotencyKey(idempotencyKey) {
		return ErrInvalid
	}
	keyDigest := sha256.Sum256([]byte(idempotencyKey))
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "distribution.receipt:"+operation+":"+actorScope+":"+hex.EncodeToString(keyDigest[:]))
	return mapError(err)
}

func scanOperationReceipt(row rowScanner) (OperationReceipt, error) {
	var result OperationReceipt
	var raw []byte
	err := row.Scan(&raw, &result.ResultKind, &result.ResultID)
	if errors.Is(err, pgx.ErrNoRows) {
		return OperationReceipt{}, distributionport.ErrNotFound
	}
	if err != nil {
		return OperationReceipt{}, mapError(err)
	}
	if len(raw) != sha256.Size || result.ResultID < 1 || !validOperationReceiptResultKind(result.ResultKind) {
		return OperationReceipt{}, distributionport.ErrUnavailable
	}
	copy(result.PayloadDigest[:], raw)
	return result, nil
}

func (r *Repository) AppendOperationReceiptWithin(ctx context.Context, operation, actorScope, idempotencyKey string, payloadDigest [sha256.Size]byte, resultKind string, resultID int64, at time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if operation == "" || actorScope == "" || !validAdminIdempotencyKey(idempotencyKey) || resultID < 1 || !validOperationReceiptResultKind(resultKind) || at.IsZero() {
		return ErrInvalid
	}
	keyDigest := sha256.Sum256([]byte(idempotencyKey))
	result, err := tx.Exec(ctx, `INSERT INTO distribution_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(operation,actor_scope,key_digest) DO NOTHING`, operation, actorScope, keyDigest[:], payloadDigest[:], resultKind, resultID, at.UTC())
	if err != nil {
		return mapError(err)
	}
	if result.RowsAffected() != 1 {
		return distributionport.ErrConflict
	}
	return nil
}

func validAdminIdempotencyKey(value string) bool {
	return value == strings.TrimSpace(value) && len(value) >= 16 && len(value) <= 200
}
func validOperationReceiptResultKind(value string) bool {
	return value == "distributor" || value == "credential" || value == "exception"
}
func validExceptionStatus(value string) bool {
	switch value {
	case "open", "querying", "resolved", "merchant_liability_recorded", "recovery_recorded":
		return true
	default:
		return false
	}
}
