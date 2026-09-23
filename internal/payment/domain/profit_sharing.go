package domain

import (
	"errors"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

var ErrProfitSharing = errors.New("invalid profit sharing transition")

type ProfitSharingState string

type ProfitSharingReceiverState string

const (
	ProfitSharingReceiverAccepted       ProfitSharingReceiverState = "accepted"
	ProfitSharingReceiverReady          ProfitSharingReceiverState = "ready"
	ProfitSharingReceiverOutcomeUnknown ProfitSharingReceiverState = "outcome_unknown"
	ProfitSharingReceiverFinalFailed    ProfitSharingReceiverState = "final_failed"
)

func (s ProfitSharingReceiverState) Valid() bool {
	return s == ProfitSharingReceiverAccepted || s == ProfitSharingReceiverReady || s == ProfitSharingReceiverOutcomeUnknown || s == ProfitSharingReceiverFinalFailed
}

type ProfitSharingReceiver struct {
	ID, CustomerID, IdentityID int64
	AppID, AppScope            string
	Channel                    Channel
	AccountDigest              string
	// FailureClass is a bounded Payment-owned explanation for a terminal
	// receiver-add rejection. It never contains a provider response, account,
	// credential, or arbitrary error text.
	FailureClass         string
	State                ProfitSharingReceiverState
	EffectID             string
	Version              int64
	CreatedAt, UpdatedAt time.Time
}

const ProfitSharingReceiverFailureProviderPermissionDenied = "provider_permission_denied"

func ValidProfitSharingReceiverFailureClass(value string) bool {
	return value == "" || value == ProfitSharingReceiverFailureProviderPermissionDenied
}

const (
	// ProfitSharingInstructionFailureReceiverAccountAbnormal is a bounded
	// projection of the official CLOSED detail reason. It is not an account
	// identifier or Provider response body.
	ProfitSharingInstructionFailureReceiverAccountAbnormal = "receiver_account_abnormal"
	ProfitSharingInstructionFailureReceiverRelationRemoved = "receiver_relation_removed"
	ProfitSharingInstructionFailureReceiverHighRisk        = "receiver_high_risk"
	ProfitSharingInstructionFailureReceiverRealNameMissing = "receiver_real_name_unverified"
	ProfitSharingInstructionFailureMerchantPermissionLost  = "merchant_permission_revoked"
	ProfitSharingInstructionFailureReceiverReceiptLimit    = "receiver_receipt_limit"
	ProfitSharingInstructionFailurePayerAccountAbnormal    = "payer_account_abnormal"
	ProfitSharingInstructionFailureInvalidRequest          = "invalid_split_request"
)

// ValidProfitSharingInstructionFailureClass only accepts the finite, official
// CLOSED-detail categories that Payment has reviewed for persistence. Empty
// retains an unknown or absent Provider reason without inventing one.
func ValidProfitSharingInstructionFailureClass(value string) bool {
	switch value {
	case "", ProfitSharingInstructionFailureReceiverAccountAbnormal,
		ProfitSharingInstructionFailureReceiverRelationRemoved,
		ProfitSharingInstructionFailureReceiverHighRisk,
		ProfitSharingInstructionFailureReceiverRealNameMissing,
		ProfitSharingInstructionFailureMerchantPermissionLost,
		ProfitSharingInstructionFailureReceiverReceiptLimit,
		ProfitSharingInstructionFailurePayerAccountAbnormal,
		ProfitSharingInstructionFailureInvalidRequest:
		return true
	default:
		return false
	}
}

const (
	ProfitSharingAccepted       ProfitSharingState = "accepted"
	ProfitSharingSettling       ProfitSharingState = "settling"
	ProfitSharingOutcomeUnknown ProfitSharingState = "outcome_unknown"
	ProfitSharingPaid           ProfitSharingState = "paid"
	ProfitSharingCancelled      ProfitSharingState = "cancelled"
	ProfitSharingException      ProfitSharingState = "exception"
)

func (s ProfitSharingState) Valid() bool {
	switch s {
	case ProfitSharingAccepted, ProfitSharingSettling, ProfitSharingOutcomeUnknown, ProfitSharingPaid, ProfitSharingCancelled, ProfitSharingException:
		return true
	default:
		return false
	}
}

func (s ProfitSharingState) Reserving() bool {
	return s == ProfitSharingAccepted || s == ProfitSharingSettling || s == ProfitSharingOutcomeUnknown
}

type ProfitSharingInstruction struct {
	ID, PaymentID, ReceiverID      int64
	SettlementRef, ProviderOrderNo string
	// These digests are immutable command fingerprints.  A settlement replay
	// must match all of them; returning an old instruction for a mutated
	// recipient or frozen Distribution fact would otherwise hide a money
	// command conflict.
	IdempotencyKeyDigest, SourceRefDigest, PayloadDigest, PolicyVersionHash string
	AmountMinor                                                             int64
	Currency                                                                string
	State                                                                   ProfitSharingState
	// FailureClass is a bounded exact-receiver CLOSED diagnostic. It never
	// stores the raw Provider response or any receiver/payment identifier.
	FailureClass                           string
	EffectID                               string
	DeadlineAt                             time.Time
	ReceiverConfirmedSuccess, OutcomeKnown bool
	Version                                int64
	CreatedAt, UpdatedAt                   time.Time
}

type ProfitSharingFunding struct {
	SuccessfulRefundMinor, RequestedRefundMinor, ProcessingRefundMinor, OutcomeUnknownRefundMinor int64
	ReservedSplitMinor                                                                            int64
}

func (v ProfitSharingFunding) RefundExposure() bool {
	return v.RequestedRefundMinor > 0 || v.ProcessingRefundMinor > 0 || v.OutcomeUnknownRefundMinor > 0
}

type ProfitSharingUnfreeze struct {
	ID, PaymentID                                                           int64
	ProviderOrderNo                                                         string
	Reason                                                                  string
	IdempotencyKeyDigest, SourceRefDigest, PayloadDigest, PolicyVersionHash string
	State, EffectID                                                         string
	OutcomeKnown                                                            bool
	Version                                                                 int64
	CreatedAt, UpdatedAt                                                    time.Time
}

type ProfitSharingProviderIntent struct {
	ReceiverID, InstructionID, UnfreezeID int64
	PayloadDigest                         effectport.Digest
}

func (v ProfitSharingInstruction) Cancel(expected int64, reason string, now time.Time) (ProfitSharingInstruction, error) {
	if expected != v.Version || !v.State.Reserving() || strings.TrimSpace(reason) != reason || reason == "" || len(reason) > 500 || now.Before(v.UpdatedAt) {
		return ProfitSharingInstruction{}, ErrProfitSharing
	}
	v.State = ProfitSharingCancelled
	v.OutcomeKnown = true
	v.Version++
	v.UpdatedAt = now.UTC()
	return v, nil
}
