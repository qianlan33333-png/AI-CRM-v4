package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type adminTestUOW struct{}

func (adminTestUOW) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type adminTestStore struct {
	distributor                         distributiondomain.Distributor
	exception                           distributionstore.AdminExceptionDetail
	commission                          distributiondomain.Commission
	receipts                            map[string]distributionstore.OperationReceipt
	writes, audits, outbox, adjustments int
	handledMinor                        int64
	locks                               []string
}

func (s *adminTestStore) ReadDistributorWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	return s.distributor, distributionport.ReceiverReadiness{}, nil
}
func (s *adminTestStore) SetDistributorEnabledWithin(_ context.Context, id, expected int64, enabled bool, at time.Time) (distributiondomain.Distributor, error) {
	if id != s.distributor.ID || expected != s.distributor.Version {
		return distributiondomain.Distributor{}, distributionport.ErrConflict
	}
	s.distributor.Enabled = enabled
	s.distributor.Version++
	return s.distributor, nil
}
func (s *adminTestStore) ReadAdminExceptionWithin(_ context.Context, _ int64, lock bool) (distributionstore.AdminExceptionDetail, error) {
	if lock {
		s.locks = append(s.locks, "exception")
	}
	return s.exception, nil
}
func (s *adminTestStore) UpdateAdminExceptionWithin(_ context.Context, value distributionstore.AdminExceptionDetail, expected int64, at time.Time) (distributionstore.AdminExceptionDetail, error) {
	if expected != s.exception.Version || value.Version != expected+1 {
		return distributionstore.AdminExceptionDetail{}, distributionport.ErrConflict
	}
	s.writes++
	s.exception = value
	return value, nil
}
func (s *adminTestStore) ReadCommissionWithin(_ context.Context, _ int64, lock bool) (distributiondomain.Commission, error) {
	if lock {
		s.locks = append(s.locks, "commission")
	}
	return s.commission, nil
}
func (s *adminTestStore) SumRecordedAfterSalesHandlingWithin(context.Context, int64) (int64, error) {
	return s.handledMinor, nil
}
func (s *adminTestStore) AppendCommissionAdjustmentWithin(context.Context, distributionstore.CommissionAdjustment) error {
	s.adjustments++
	return nil
}
func (s *adminTestStore) ReadOperationReceiptWithin(_ context.Context, operation, actor, key string) (distributionstore.OperationReceipt, bool, error) {
	value, ok := s.receipts[operation+actor+key]
	return value, ok, nil
}
func (s *adminTestStore) AppendOperationReceiptWithin(_ context.Context, operation, actor, key string, digest [sha256.Size]byte, _ string, id int64, _ time.Time) error {
	s.receipts[operation+actor+key] = distributionstore.OperationReceipt{PayloadDigest: digest, ResultID: id}
	return nil
}
func (s *adminTestStore) AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error {
	s.audits++
	return nil
}
func (s *adminTestStore) AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error {
	s.outbox++
	return nil
}

type adminTestPayment struct {
	calls, unfreezeCalls int
	instruction          paymentport.ProfitSharingInstruction
	unfreeze             paymentport.ProfitSharingUnfreeze
	err                  error
}

func (p *adminTestPayment) ReconcileProfitSharing(_ context.Context, ref string) (paymentport.ProfitSharingInstruction, error) {
	p.calls++
	if p.err != nil {
		return paymentport.ProfitSharingInstruction{}, p.err
	}
	if ref != p.instruction.Reference {
		return paymentport.ProfitSharingInstruction{}, errors.New("unexpected payment reference")
	}
	return p.instruction, nil
}

func (p *adminTestPayment) ReconcileProfitSharingUnfreeze(_ context.Context, ref string) (paymentport.ProfitSharingUnfreeze, error) {
	p.unfreezeCalls++
	if p.err != nil {
		return paymentport.ProfitSharingUnfreeze{}, p.err
	}
	if ref != p.unfreeze.Reference {
		return paymentport.ProfitSharingUnfreeze{}, errors.New("unexpected unfreeze reference")
	}
	return p.unfreeze, nil
}

func adminServiceFixture(t *testing.T) (*AdminService, *adminTestStore, *adminTestPayment) {
	t.Helper()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	store := &adminTestStore{distributor: distributiondomain.Distributor{ID: 9, CustomerID: 11, PublicNo: "DABC123", AgreementVersion: "v1", Enabled: true, RegisteredAt: now, Version: 3}, exception: distributionstore.AdminExceptionDetail{ID: 8, CommissionID: 7, SettlementID: 6, Kind: "settlement_unknown", Status: "open", Reason: "settlement_unknown", EvidenceReference: "payment:original:7", InstructionReference: "psinst_123", ReconcileTarget: distributionport.AdminReconcileTargetSplit, UnpaidDueMinor: 12, AlreadyPaidMinor: 33, AmountMinor: 12, Version: 4}, commission: distributiondomain.Commission{ID: 7, CurrentPayableMinor: 21}, receipts: map[string]distributionstore.OperationReceipt{}}
	payment := &adminTestPayment{instruction: paymentport.ProfitSharingInstruction{Reference: "psinst_123", State: "receiver_succeeded", ReceiverConfirmedSuccess: true, OutcomeKnown: true, Version: 5}}
	service, err := NewAdminService(adminTestUOW{}, store, payment)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	return service, store, payment
}

func TestAdminReconcileQueriesOriginalPaymentOutsideDistributionWriteAndNeverMarksPaid(t *testing.T) {
	service, store, payment := adminServiceFixture(t)
	command := distributionport.AdminExceptionCommand{ExceptionID: 8, ExpectedVersion: 4, ActorScope: "access:9", IdempotencyKey: "admin-reconcile-key"}
	if err := service.ReconcileException(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if payment.calls != 1 || store.exception.Status != "resolved" || store.exception.Version != 5 || store.audits != 1 || store.outbox != 1 {
		t.Fatalf("payment/status/version/audit/outbox=%d/%s/%d/%d/%d", payment.calls, store.exception.Status, store.exception.Version, store.audits, store.outbox)
	}
	if store.exception.AlreadyPaidMinor != 33 || store.exception.UnpaidDueMinor != 12 {
		t.Fatalf("admin query must not manufacture payment facts: %+v", store.exception)
	}
	if store.exception.Reason != "settlement_unknown" || store.exception.EvidenceReference != "payment:original:7" || store.exception.AmountMinor != 12 {
		t.Fatalf("admin query must preserve immutable source fact: %+v", store.exception)
	}
	if err := service.ReconcileException(context.Background(), command); err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	if payment.calls != 1 || store.writes != 1 {
		t.Fatalf("receipt must prevent a duplicate Provider query or distribution write: payment=%d writes=%d", payment.calls, store.writes)
	}
}

func TestPaymentObservationReasonAcceptsOnlyPaymentSafeClasses(t *testing.T) {
	known := paymentport.ProfitSharingInstruction{State: "exception", FailureClass: "merchant_permission_revoked"}
	if got := paymentObservationReason(known); got != "payment_merchant_permission_revoked" {
		t.Fatalf("known observation reason=%q", got)
	}
	unknown := paymentport.ProfitSharingInstruction{State: "exception", FailureClass: "provider raw response"}
	if got := paymentObservationReason(unknown); got != "payment_exception" {
		t.Fatalf("unknown observation reason=%q", got)
	}
}

func TestAdminRecoveryRequiresEvidenceAndCannotExceedHandleableAmount(t *testing.T) {
	service, store, _ := adminServiceFixture(t)
	store.exception.Kind, store.exception.Reason = "buyer_refund_after_paid", "buyer_refund_after_paid"
	store.exception.EvidenceReference, store.exception.AmountMinor = "refund:source", 5
	store.commission.Status, store.commission.PaidMinor = distributiondomain.CommissionException, 33
	tooLarge := distributionport.AdminExceptionCommand{ExceptionID: 8, ExpectedVersion: 4, AmountMinor: 13, ActorScope: "access:9", Reason: "manual_recovery", EvidenceReference: "receipt:1", IdempotencyKey: "admin-recovery-too-large"}
	if err := service.RecordRecovery(context.Background(), tooLarge); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("too-large recovery error=%v", err)
	}
	if store.writes != 0 {
		t.Fatal("too-large recovery wrote a record")
	}
	missingEvidence := tooLarge
	missingEvidence.AmountMinor, missingEvidence.EvidenceReference, missingEvidence.IdempotencyKey = 12, "", "admin-recovery-no-evidence"
	if err := service.RecordRecovery(context.Background(), missingEvidence); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("missing evidence error=%v", err)
	}
	accepted := tooLarge
	accepted.AmountMinor, accepted.IdempotencyKey = 12, "admin-recovery-accepted"
	if err := service.RecordRecovery(context.Background(), accepted); err != nil {
		t.Fatal(err)
	}
	if store.exception.Status != "recovery_recorded" || store.exception.Reason != "buyer_refund_after_paid" || store.exception.EvidenceReference != "refund:source" || store.exception.AmountMinor != 5 || store.exception.AlreadyPaidMinor != 33 || store.adjustments != 1 {
		t.Fatalf("recovery must be audited without overwriting paid fact: %+v adjustments=%d", store.exception, store.adjustments)
	}
}

func TestAdminAfterSalesHandlingRequiresPaidDeltaAndCapsCombinedHistory(t *testing.T) {
	service, store, _ := adminServiceFixture(t)
	store.exception.Kind, store.exception.Reason = "buyer_refund_after_paid", "buyer_refund_after_paid"
	store.commission.Status, store.commission.PaidMinor = distributiondomain.CommissionException, 0
	command := distributionport.AdminExceptionCommand{ExceptionID: 8, ExpectedVersion: 4, AmountMinor: 1, ActorScope: "access:9", Reason: "manual_recovery", EvidenceReference: "receipt:unpaid", IdempotencyKey: "admin-unpaid-no-recovery"}
	if err := service.RecordRecovery(context.Background(), command); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("unpaid recovery=%v", err)
	}
	command.Reason, command.EvidenceReference, command.IdempotencyKey = "merchant accepts split", "", "admin-unpaid-no-liability"
	if err := service.RecordMerchantLiability(context.Background(), command); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("unpaid liability=%v", err)
	}

	store.commission.PaidMinor = 33
	store.handledMinor = 10
	command.AmountMinor, command.Reason, command.EvidenceReference, command.IdempotencyKey = 12, "manual_recovery", "receipt:remaining", "admin-recovery-over-combined"
	if err := service.RecordRecovery(context.Background(), command); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("combined cap recovery=%v", err)
	}
	command.AmountMinor, command.IdempotencyKey = 2, "admin-recovery-remaining"
	if err := service.RecordRecovery(context.Background(), command); err != nil {
		t.Fatalf("remaining paid delta recovery=%v", err)
	}
	if store.exception.AmountMinor != 12 || store.exception.Reason != "buyer_refund_after_paid" || store.exception.EvidenceReference != "payment:original:7" || store.adjustments != 1 {
		t.Fatalf("recovery must preserve the source snapshot while recording its action separately: %+v adjustments=%d", store.exception, store.adjustments)
	}
}

func TestAdminQualificationRevocationUsesPaidAmountAndLocksCommissionFirst(t *testing.T) {
	service, store, _ := adminServiceFixture(t)
	store.exception.Kind, store.exception.Reason, store.exception.EvidenceReference, store.exception.AmountMinor = "qualification_revoked_after_paid", "qualification_revoked_after_paid", "qualification:source", 3
	store.commission.Status, store.commission.PaidMinor = distributiondomain.CommissionException, 33
	command := distributionport.AdminExceptionCommand{ExceptionID: 8, ExpectedVersion: 4, AmountMinor: 33, ActorScope: "access:9", Reason: "merchant accepts qualification reversal", IdempotencyKey: "admin-qualification-liability"}
	if err := service.RecordMerchantLiability(context.Background(), command); err != nil {
		t.Fatalf("paid qualification liability=%v", err)
	}
	if store.exception.Status != "merchant_liability_recorded" || store.exception.AmountMinor != 3 || store.exception.Reason != "qualification_revoked_after_paid" || store.exception.EvidenceReference != "qualification:source" || len(store.locks) < 2 || store.locks[len(store.locks)-2] != "commission" || store.locks[len(store.locks)-1] != "exception" {
		t.Fatalf("qualification handling/lock order=%+v locks=%v", store.exception, store.locks)
	}
}

func TestAdminDeadlineWarningCannotTriggerProviderQueryOrFinancialEntry(t *testing.T) {
	service, store, payment := adminServiceFixture(t)
	store.exception.Kind = "settlement_deadline_imminent"
	store.exception.InstructionReference = ""
	query := distributionport.AdminExceptionCommand{ExceptionID: 8, ExpectedVersion: 4, ActorScope: "access:9", IdempotencyKey: "admin-warning-query-key"}
	if err := service.ReconcileException(context.Background(), query); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("warning reconcile=%v", err)
	}
	money := distributionport.AdminExceptionCommand{ExceptionID: 8, ExpectedVersion: 4, AmountMinor: 12, ActorScope: "access:9", Reason: "manual_recovery", EvidenceReference: "receipt:1", IdempotencyKey: "admin-warning-recovery-key"}
	if err := service.RecordRecovery(context.Background(), money); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("warning recovery=%v", err)
	}
	money.Reason, money.EvidenceReference, money.IdempotencyKey = "merchant absorbs split deadline", "", "admin-warning-liability-key"
	if err := service.RecordMerchantLiability(context.Background(), money); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("warning liability=%v", err)
	}
	if payment.calls != 0 || store.writes != 0 || store.adjustments != 0 || len(store.receipts) != 0 {
		t.Fatalf("informational warning wrote/queried payment=%d writes=%d adjustments=%d receipts=%d", payment.calls, store.writes, store.adjustments, len(store.receipts))
	}
}

func TestAdminReconcileUnfreezeWithoutSettlementPreservesCancelledCommission(t *testing.T) {
	service, store, payment := adminServiceFixture(t)
	store.exception = distributionstore.AdminExceptionDetail{ID: 8, CommissionID: 7, Kind: "unfreeze_final_failed", Status: "open", Reason: "unfreeze_final_failed", EvidenceReference: "psunfreeze_42", ReconcileTarget: distributionport.AdminReconcileTargetUnfreeze, AmountMinor: 12, Version: 4}
	store.commission.Status = distributiondomain.CommissionCancelled
	beforeCommission := store.commission
	payment.unfreeze = paymentport.ProfitSharingUnfreeze{Reference: "psunfreeze_42", State: "succeeded", OutcomeKnown: true, Version: 3}
	command := distributionport.AdminExceptionCommand{ExceptionID: 8, ExpectedVersion: 4, ActorScope: "access:9", IdempotencyKey: "admin-unfreeze-cancelled"}
	if err := service.ReconcileException(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if payment.unfreezeCalls != 1 || payment.calls != 0 || store.exception.Status != "resolved" || store.exception.EvidenceReference != "psunfreeze_42" {
		t.Fatalf("wrong unfreeze reconciliation: split=%d unfreeze=%d exception=%+v", payment.calls, payment.unfreezeCalls, store.exception)
	}
	if store.commission != beforeCommission {
		t.Fatalf("admin reconciliation changed cancelled commission fact: before=%+v after=%+v", beforeCommission, store.commission)
	}
}

func TestAdminReconcileUnfreezeWinsOverSettlementForPaidCommission(t *testing.T) {
	service, store, payment := adminServiceFixture(t)
	store.exception = distributionstore.AdminExceptionDetail{ID: 8, CommissionID: 7, SettlementID: 6, Kind: "unfreeze_final_failed", Status: "open", Reason: "unfreeze_final_failed", EvidenceReference: "psunfreeze_43", InstructionReference: "psinst_123", ReconcileTarget: distributionport.AdminReconcileTargetUnfreeze, AmountMinor: 12, Version: 4}
	store.commission.Status = distributiondomain.CommissionPaid
	beforeCommission := store.commission
	payment.unfreeze = paymentport.ProfitSharingUnfreeze{Reference: "psunfreeze_43", State: "exception", OutcomeKnown: true, Version: 3}
	command := distributionport.AdminExceptionCommand{ExceptionID: 8, ExpectedVersion: 4, ActorScope: "access:9", IdempotencyKey: "admin-unfreeze-paid"}
	if err := service.ReconcileException(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if payment.unfreezeCalls != 1 || payment.calls != 0 || store.exception.Status != "open" || store.exception.EvidenceReference != "psunfreeze_43" {
		t.Fatalf("must query unfreeze and retain failed exception: split=%d unfreeze=%d exception=%+v", payment.calls, payment.unfreezeCalls, store.exception)
	}
	if store.commission != beforeCommission {
		t.Fatalf("admin reconciliation changed paid commission fact: before=%+v after=%+v", beforeCommission, store.commission)
	}
}

func TestAdminCancelledCommissionRejectsSplitAndMoneyButKeepsUnfreezeQueryable(t *testing.T) {
	service, store, payment := adminServiceFixture(t)
	store.commission.Status, store.commission.CurrentPayableMinor, store.commission.PaidMinor = distributiondomain.CommissionCancelled, 0, 0
	command := distributionport.AdminExceptionCommand{ExceptionID: 8, ExpectedVersion: 4, ActorScope: "access:9", IdempotencyKey: "admin-cancelled-split"}
	if err := service.ReconcileException(context.Background(), command); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("cancelled split reconcile=%v", err)
	}
	money := distributionport.AdminExceptionCommand{ExceptionID: 8, ExpectedVersion: 4, AmountMinor: 12, ActorScope: "access:9", Reason: "manual_recovery", EvidenceReference: "receipt:cancelled", IdempotencyKey: "admin-cancelled-recovery"}
	if err := service.RecordRecovery(context.Background(), money); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("cancelled recovery=%v", err)
	}
	money.Reason, money.EvidenceReference, money.IdempotencyKey = "merchant accepts liability", "", "admin-cancelled-liability"
	if err := service.RecordMerchantLiability(context.Background(), money); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("cancelled liability=%v", err)
	}
	if payment.calls != 0 || store.writes != 0 || store.adjustments != 0 || len(store.receipts) != 0 {
		t.Fatalf("cancelled commission accepted admin financial action: payment=%d writes=%d adjustments=%d receipts=%d", payment.calls, store.writes, store.adjustments, len(store.receipts))
	}

	store.exception = distributionstore.AdminExceptionDetail{ID: 8, CommissionID: 7, Kind: "unfreeze_final_failed", Status: "open", Reason: "unfreeze_final_failed", EvidenceReference: "psunfreeze_42", ReconcileTarget: distributionport.AdminReconcileTargetUnfreeze, Version: 4}
	payment.unfreeze = paymentport.ProfitSharingUnfreeze{Reference: "psunfreeze_42", State: "succeeded", OutcomeKnown: true, Version: 1}
	command.IdempotencyKey = "admin-cancelled-unfreeze"
	if err := service.ReconcileException(context.Background(), command); err != nil {
		t.Fatalf("cancelled unfreeze remains queryable: %v", err)
	}
	if payment.unfreezeCalls != 1 || payment.calls != 0 || store.exception.Status != "resolved" {
		t.Fatalf("cancelled unfreeze=%+v split=%d unfreeze=%d", store.exception, payment.calls, payment.unfreezeCalls)
	}
}
