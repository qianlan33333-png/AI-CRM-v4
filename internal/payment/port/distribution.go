package port

import (
	"context"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
)

// DistributionSettlementPort is the sole cross-domain contract for first-level
// distribution settlement. The caller provides only server-frozen facts. It
// cannot supply an OpenID, a Provider transaction number, or a Provider result.
//
// AcceptProfitSharingWithin joins the caller's PostgreSQL Unit of Work. The
// Payment implementation atomically records the reserve, immutable instruction,
// audit facts and EER acceptance. It never makes a Provider call in that UoW.
type DistributionSettlementPort interface {
	// SettlementCapability is the safe, runtime capability projection used by
	// Distribution to distinguish a merchant-level disabled settlement feature
	// from a distributor's receiver readiness. It never exposes configuration,
	// keys, accounts, or Provider responses.
	SettlementCapability(context.Context) (SettlementCapability, error)
	PrepareProfitSharingReceiverWithin(context.Context, ReceiverPreparation) (ReceiverReadiness, error)
	ReceiverReadiness(context.Context, int64, string) (ReceiverReadiness, error)
	// ReceiverReadinessWithin joins an existing business UoW and locks the
	// Payment-owned receiver row. Settlement uses it immediately before the
	// atomic reserve/instruction acceptance, so readiness cannot change between
	// the check and the accepted split command.
	ReceiverReadinessWithin(context.Context, int64, string) (ReceiverReadiness, error)
	DistributionPaymentState(context.Context, int64) (DistributionPaymentState, error)
	// DistributionPaymentStateWithin reads the same projection while joining an
	// already-open PostgreSQL UoW. History import and other atomic consumers
	// must use this form rather than nesting a new Payment transaction.
	DistributionPaymentStateWithin(context.Context, int64) (DistributionPaymentState, error)
	AcceptProfitSharingWithin(context.Context, ProfitSharingRequest) (ProfitSharingInstruction, error)
	GetProfitSharing(context.Context, string) (ProfitSharingInstruction, error)
	ReconcileProfitSharing(context.Context, string) (ProfitSharingInstruction, error)
	ReconcileProfitSharingUnfreeze(context.Context, string) (ProfitSharingUnfreeze, error)
	CancelUnsubmittedProfitSharingWithin(context.Context, string, string, ProfitSharingCancellationActor) (ProfitSharingInstruction, error)
	UnfreezeProfitSharingRemainingWithin(context.Context, ProfitSharingUnfreezeRequest) (ProfitSharingUnfreeze, error)
}

// SettlementCapability is intentionally separate from ReceiverReadiness:
// registering a distributor and preparing their receiver may remain valid
// facts while the merchant has not enabled money-moving settlement.
type SettlementCapability struct {
	Enabled bool
	Reason  string
}

// ReceiverPreparation may only be made from a server-trusted Payment session
// actor. IdentityID is verified through identity/port with the exact
// Mini-Program or Official-Account OpenID kind and configured App scope before
// any receiver effect is accepted. It must never be populated from HTTP JSON.
type ReceiverPreparation struct {
	CustomerID, IdentityID int64
	AppID, AppScope        string
	Channel                domain.Channel
	IdempotencyKey         string
	SourceDigest           effectport.Digest
	PayloadDigest          effectport.Digest
}

func (v ReceiverPreparation) Valid() bool {
	return v.CustomerID > 0 && v.IdentityID > 0 && validTrimmed(v.AppID, 1, 160) && validTrimmed(v.AppScope, 1, 240) && (v.Channel == domain.ChannelMiniProgram || v.Channel == domain.ChannelH5Official) && validTrimmed(v.IdempotencyKey, 12, 240) && effectport.ValidDigest(v.SourceDigest) && effectport.ValidDigest(v.PayloadDigest)
}

// ReceiverReadiness is a safe Payment projection. Reference and EffectRef are
// opaque; OpenID and Provider request/response data are deliberately absent.
type ReceiverReadiness struct {
	Reference, AppID, State, EffectRef string
	// FailureClass is a bounded Payment-owned terminal receiver rejection. It
	// is never a Provider response code or text.
	FailureClass        string
	CustomerID          int64
	Ready, OutcomeKnown bool
	Version             int64
	UpdatedAt           time.Time
}

// ProfitSharingReceiverStatusObserver is an optional same-UoW projection
// bridge. Payment remains the authority for receiver state; Distribution may
// persist only this safe readiness snapshot for its own profile and admin
// reads after Payment has transitioned the owned receiver record.
type ProfitSharingReceiverStatusObserver interface {
	SyncProfitSharingReceiverStatusWithin(context.Context, ReceiverReadiness) error
}

// ProfitSharingReceiverRecoveryCommand is an administrator-only Payment
// command. The caller never supplies an OpenID, current account digest,
// Provider result, or replacement effect reference; Payment re-verifies all
// of those facts from its own records before accepting a new intent.
type ProfitSharingReceiverRecoveryCommand struct {
	ReceiverReference string
	ActorAdminUserID  int64
	IdempotencyKey    string
	EvidenceReference string
}

func (v ProfitSharingReceiverRecoveryCommand) Valid() bool {
	return validProfitSharingReceiverReference(v.ReceiverReference) && v.ActorAdminUserID > 0 && validTrimmed(v.IdempotencyKey, 12, 240) && validTrimmed(v.EvidenceReference, 1, 200)
}

// DistributionPaymentState is the original-transaction projection used for
// qualification and due checks. Amounts are integer CNY minor units and are
// never accepted from a browser.
type DistributionPaymentState struct {
	OriginalPaymentRef                          string
	ConfirmedPaid, SplitCapable, RefundExposure bool
	DeadlineAt                                  time.Time
	OriginalMinor, AvailableMinor               int64
	PayerCustomerID, BeneficiaryCustomerID      int64
	SuccessfulRefundMinor                       int64
	RequestedRefundMinor, ProcessingRefundMinor int64
	OutcomeUnknownRefundMinor                   int64
	// ConfirmedPaidAt is immutable once a Payment reaches paid. A zero value
	// means the payment cannot be used as historical qualification evidence.
	ConfirmedPaidAt time.Time
	UpdatedAt       time.Time
}

// ProfitSharingRequest is created only from a Distribution settlement fact.
// IdempotencyKey must stay bound to SettlementRef for every retry/recovery.
type ProfitSharingRequest struct {
	SettlementRef, OriginalPaymentRef string
	RecipientCustomerID               int64
	AmountMinor                       int64
	Currency                          string
	IdempotencyKey                    string
	SourceDigest, PayloadDigest       effectport.Digest
	PolicyDigest                      effectport.Digest
}

func (v ProfitSharingRequest) Valid() bool {
	return validTrimmed(v.SettlementRef, 8, 200) && validPaymentReference(v.OriginalPaymentRef) && v.RecipientCustomerID > 0 && v.AmountMinor > 0 && v.Currency == "CNY" && validTrimmed(v.IdempotencyKey, 12, 240) && effectport.ValidDigest(v.SourceDigest) && effectport.ValidDigest(v.PayloadDigest) && effectport.ValidDigest(v.PolicyDigest)
}

// ProfitSharingInstruction is a Payment-owned, immutable money instruction.
// ReceiverConfirmedSuccess is true only after an original-order provider query
// confirms SUCCESS for this exact receiver and amount.
type ProfitSharingInstruction struct {
	Reference, SettlementRef, OriginalPaymentRef string
	State, EffectRef                             string
	// FailureClass is Payment's bounded exact-receiver CLOSED diagnostic. It
	// never carries a Provider response, account, or transaction identifier.
	FailureClass                           string
	AmountMinor                            int64
	Currency                               string
	ReceiverConfirmedSuccess, OutcomeKnown bool
	DeadlineAt                             time.Time
	Version                                int64
	UpdatedAt                              time.Time
}

// ProfitSharingCancellationActor preserves who caused a queued split to be
// cancelled. Construct it only from a trusted admin principal or the Payment
// due runner; HTTP and Distribution browser payloads never carry either form.
// The system variant is deliberately one named subject, not a sentinel user.
type ProfitSharingCancellationActor struct {
	actor effectport.ControlActor
}

func AdminProfitSharingCancellationActor(adminUserID int64) (ProfitSharingCancellationActor, error) {
	actor := effectport.ControlActor{AdminUserID: adminUserID}
	if !actor.Valid() {
		return ProfitSharingCancellationActor{}, ErrInvalid
	}
	return ProfitSharingCancellationActor{actor: actor}, nil
}

func SystemDueProfitSharingCancellationActor() ProfitSharingCancellationActor {
	return ProfitSharingCancellationActor{actor: effectport.ControlActor{SystemRef: effectport.SystemActorPaymentProfitSharingDue}}
}

func (actor ProfitSharingCancellationActor) Valid() bool { return actor.actor.Valid() }

// EffectControlActor is consumed only by Payment's EER adapter. It exposes no
// browser or Provider value; callers still cannot manufacture a valid actor
// without one of the constructors above.
func (actor ProfitSharingCancellationActor) EffectControlActor() effectport.ControlActor {
	return actor.actor
}

// ProfitSharingUnfreezeRequest releases the remaining retained balance of one
// original transaction only after Payment's refund/split coordinator has
// established that no refund outcome is in flight and no split reserve remains.
// It is an internal settlement follow-up, never a browser command.
type ProfitSharingUnfreezeRequest struct {
	OriginalPaymentRef string
	Reason             string
	IdempotencyKey     string
	SourceDigest       effectport.Digest
	PayloadDigest      effectport.Digest
	PolicyDigest       effectport.Digest
}

func (v ProfitSharingUnfreezeRequest) Valid() bool {
	return validPaymentReference(v.OriginalPaymentRef) && validTrimmed(v.Reason, 1, 500) && validTrimmed(v.IdempotencyKey, 12, 240) && effectport.ValidDigest(v.SourceDigest) && effectport.ValidDigest(v.PayloadDigest) && effectport.ValidDigest(v.PolicyDigest)
}

type ProfitSharingUnfreeze struct {
	Reference, OriginalPaymentRef, State, EffectRef string
	OutcomeKnown                                    bool
	Version                                         int64
	UpdatedAt                                       time.Time
}

// ProfitSharingProviderResult is a Payment-internal, provider-verified query
// result. `ReceiverConfirmedSuccess` is false for HTTP success, processing,
// and terminal order completion unless the exact intended receiver succeeded.
type ProfitSharingProviderResult struct {
	State                                              string
	ReceiverConfirmedSuccess, ReceiverConfirmedFailure bool
	// FailureClass is set only for an exact receiver/amount/type CLOSED detail
	// whose official fail_reason maps to Payment's finite safe vocabulary.
	FailureClass string
	// OutcomeKnown is true only when the exact instructed receiver and amount
	// have a verified terminal result. An aggregate FINISHED order with a
	// missing/mismatched receiver remains an exceptional, queryable unknown.
	OutcomeKnown   bool
	EvidenceDigest effectport.Digest
	OccurredAt     time.Time
}

type ProfitSharingReconciler interface {
	QueryProfitSharing(context.Context, string) (ProfitSharingProviderResult, error)
	QueryProfitSharingUnfreeze(context.Context, string) (ProfitSharingProviderResult, error)
}

// ProfitSharingProviderMaterial is a transient, Payment-to-provider capability.
// It is never serialized in EER, returned to Distribution, or logged. Account
// and TransactionReference are scoped Provider secrets, not API inputs.
type ProfitSharingProviderMaterial struct {
	PayloadDigest                                effectport.Digest
	AppID, ReceiverAccount, TransactionReference string
	ProviderOrderNo, Reason                      string
	AmountMinor                                  int64
}

// ProfitSharingMaterialReader is implemented by Payment Service and consumed
// only by its Provider adapter. The reference form is used for reconciliation
// and preserves the original instruction number rather than minting a retry.
type ProfitSharingMaterialReader interface {
	LoadProfitSharingEffectMaterial(context.Context, effectport.Kind, effectport.Digest) (ProfitSharingProviderMaterial, error)
	LoadProfitSharingReferenceMaterial(context.Context, string) (effectport.Kind, ProfitSharingProviderMaterial, error)
}

// PaymentReference creates the opaque Payment reference stored by a foreign
// domain. It is not a Provider merchant order number and contains no customer
// or account identity.
func PaymentReference(paymentID int64) string {
	if paymentID < 1 {
		return ""
	}
	return "payref_" + decimal(paymentID)
}

func validProfitSharingReceiverReference(value string) bool {
	if !strings.HasPrefix(value, "psrecv_") {
		return false
	}
	id := strings.TrimPrefix(value, "psrecv_")
	if id == "" || id[0] == '0' {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validPaymentReference(value string) bool {
	if !strings.HasPrefix(value, "payref_") || len(value) < len("payref_")+1 || len(value) > 40 {
		return false
	}
	for _, c := range value[len("payref_"):] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return value[len("payref_")] != '0'
}

func validTrimmed(value string, min, max int) bool {
	return len(value) >= min && len(value) <= max && strings.TrimSpace(value) == value
}

func decimal(value int64) string {
	const digits = "0123456789"
	var out [20]byte
	i := len(out)
	for value > 0 {
		i--
		out[i] = digits[value%10]
		value /= 10
	}
	return string(out[i:])
}
