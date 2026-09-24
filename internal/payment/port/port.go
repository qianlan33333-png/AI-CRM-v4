package port

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
)

var ErrInvalid = errors.New("invalid payment command")
var ErrConflict = errors.New("payment conflict")
var ErrNotFound = errors.New("payment not found")
var ErrUnavailable = errors.New("payment unavailable")
var ErrCanonicalPayerUnavailable = errors.New("payment canonical payer unavailable")

// ErrSettlementCapabilityDisabled is returned before a distribution receiver
// effect is accepted when the merchant has not enabled the separate
// profit-sharing settlement capability.
var ErrSettlementCapabilityDisabled = errors.New("payment settlement capability disabled")

// ErrNothingToUnfreeze is the explicit terminal result for a transaction that
// was never marked for profit sharing.  It is intentionally distinct from a
// funding conflict, which can mean a refund or split reserve is still active
// and therefore must be retried/reconciled.
var ErrNothingToUnfreeze = errors.New("payment has no profit sharing balance to unfreeze")
var ErrSessionRequired = errors.New("trusted payment session required")
var ErrSessionMismatch = errors.New("payment checkout session mismatch")

// PaymentReconciliationPreview is an administrator-only, read-only result for
// the narrow native-payment confirmation repair.  It intentionally reveals no
// Provider transaction, merchant configuration, or customer identity.
type PaymentReconciliationPreview struct {
	PaymentID                    int64
	WouldRestorePaidConfirmation bool
	Reason                       string
}

// TrustedSessionCookieName is shared by public Host adapters. Its opaque
// value is resolved only by Payment's SessionReader inside a PostgreSQL UoW;
// no adapter may treat it as a customer or identity claim.
const TrustedSessionCookieName = "aicrm_payment_session"

// ReferralActivityCookieName is written by the trusted product-activity entry
// handler. Checkout never accepts campaign IDs or promoter IDs in JSON; an
// unknown/invalid cookie simply produces an ordinary purchase.
const ReferralActivityCookieName = "aicrm_referral_activity_context"

// CheckoutSessionBinding is an opaque, non-identity marker derived only from
// the HttpOnly Payment session. Public pages retain it with a recovery
// checkpoint, never the session token itself. A marker is useful only when
// presented together with the current HttpOnly cookie, so it is not a second
// credential or a browser-provided identity claim.
func CheckoutSessionBinding(token string) string {
	if len(token) < 20 || len(token) > 100 {
		return ""
	}
	digest := sha256.Sum256([]byte("payment.checkout.session-binding.v1\x00" + token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// MatchesCheckoutSessionBinding uses a constant-time comparison so the
// checkout mutation can reject a checkpoint from another trusted session
// before it reaches the payment/order Unit of Work.
func MatchesCheckoutSessionBinding(token, binding string) bool {
	expected := CheckoutSessionBinding(token)
	return expected != "" && len(binding) == len(expected) && subtle.ConstantTimeCompare([]byte(expected), []byte(binding)) == 1
}

type CreateCommand struct {
	OrderID, ProductID int64
	CouponClaimID      int64
	ProductType        string
	// Provider and Channel are a constrained checkout choice. They are
	// validated against the server-side enabled capability before any order is
	// created; the browser cannot select an unsupported Provider.
	Provider string
	Channel  domain.Channel
	// PromotionContext is an opaque, server-issued checkout context. Payment
	// does not parse it; it is frozen into idempotent checkout facts and passed
	// to Order's same-UoW attribution coordinator by composition.
	PromotionContext string
	// ReferralActivityContext is a separate opaque activity token. Payment
	// carries it unchanged to Order; Referral resolves it in its own store.
	ReferralActivityContext string
	// ProfitSharingRequired is an internal-only result from that coordinator's
	// authoritative attribution preparation. Public HTTP handlers must not bind
	// it directly from browser input.
	ProfitSharingRequired      bool
	SessionToken               string
	CheckoutSessionBinding     string
	MobileE164                 string
	ContactCollectionLevel     string
	ShippingAddress            ShippingAddress
	BeneficiarySelection       BeneficiarySelection
	ActorScope, IdempotencyKey string
}

type ShippingAddress struct {
	RecipientName string `json:"recipient_name,omitempty"`
	ProvinceCode  string `json:"province_code,omitempty"`
	ProvinceName  string `json:"province_name,omitempty"`
	CityCode      string `json:"city_code,omitempty"`
	CityName      string `json:"city_name,omitempty"`
	DistrictCode  string `json:"district_code,omitempty"`
	DistrictName  string `json:"district_name,omitempty"`
	DetailAddress string `json:"detail_address,omitempty"`
}
type RefundCommand struct {
	PaymentID, AmountMinor                        int64
	RefundNo, Reason, ActorScope, IdempotencyKey  string
	ProviderOrderID, ProductID, SKUID, ReasonCode string
	RefundCount                                   int64
}
type Application interface {
	Create(context.Context, CreateCommand) (domain.Payment, error)
	RequestRefund(context.Context, RefundCommand) (domain.Refund, error)
}
type Query interface {
	GetPayment(context.Context, int64) (domain.Payment, error)
	GetRefund(context.Context, int64) (domain.Refund, error)
}

// RefundExposureReader returns order IDs with a requested, in-flight,
// outcome-unknown, or completed refund. Final failures are excluded.
type RefundExposureReader interface {
	RefundRelatedOrderIDsWithin(context.Context, []int64) (map[int64]struct{}, error)
}

// ExternalOrderRefundSummary is Payment's read-only public-safe projection.
// Successful money is never combined with in-flight or unknown outcomes.
type ExternalOrderRefundSummary struct {
	Available           bool
	HasRefund           bool
	CompletedMinor      int64
	RequestedMinor      int64
	ProcessingMinor     int64
	OutcomeUnknownMinor int64
	FinalFailedMinor    int64
}

type ExternalOrderRefundReader interface {
	ExternalOrderRefundSummaries(context.Context, []int64) (map[int64]ExternalOrderRefundSummary, error)
	ExternalOrderRefundDetails(context.Context, []int64) (map[int64][]ExternalOrderRefundDetail, error)
	ExternalRefundedOrderIDs(context.Context, []int64) ([]int64, error)
}

type ExternalOrderRefundDetail struct {
	RefundID    int64
	Status      string
	AmountMinor int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type RefundProjection struct {
	Refund        domain.Refund
	OrderID       int64
	MerchantOrder string
	OrderAmount   int64
	Currency      string
}

type EffectProjection struct {
	EffectID     string
	Kind         effectport.Kind
	State        effectport.State
	AttemptCount int32
	UpdatedAt    time.Time
}

// AbandonCheckoutCommand records a human-reviewed local checkout decision.
// It is not a Provider cancellation or proof that prepay never executed.
type AbandonCheckoutCommand struct {
	PaymentID        int64
	ActorScope       string
	EvidenceDigest   string
	ConfirmedNoDebit bool
}
type CheckoutRestartReviewer interface {
	AllowCheckoutRestart(context.Context, AbandonCheckoutCommand) error
}
type CheckoutAbandoner interface {
	AbandonCheckout(context.Context, AbandonCheckoutCommand) error
}

type Handoff struct {
	// AmountMinor and Currency are frozen Payment facts, not current catalog/coupon prices.
	AmountMinor            int64
	Currency               string
	Provider               domain.Provider
	Channel                domain.Channel
	CheckoutRestartAllowed bool
	CheckoutAbandoned      bool
	PrepayState            effectport.State
	PaymentID              int64
	OrderID                int64
	MerchantOrder          string
	Status                 domain.Status
	Payload                []byte
	ExpiresAt              time.Time
}

type AdminQuery interface {
	FindPayment(context.Context, domain.Provider, string) (domain.Payment, error)
	ListRefunds(context.Context, int32, int32) ([]RefundProjection, int64, error)
	// ListRefundsForPayment is the scoped read used by an order detail. Provider
	// and merchant order number together identify one persisted Payment; callers
	// must not derive a detail timeline from the unscoped refund list.
	ListRefundsForPayment(context.Context, domain.Provider, string, int32, int32) ([]RefundProjection, int64, error)
	ListOrderEffects(context.Context, domain.Provider, string) ([]EffectProjection, error)
}
type HistoricalImporter interface {
	ImportTerminalPayment(context.Context, domain.Payment, [32]byte, string) (domain.Payment, error)
	ImportTerminalRefund(context.Context, domain.Refund, [32]byte, string) (domain.Refund, error)
}

// BeneficiarySelection records how a payment-session recipient was established.
// The public checkout exposes only PayerSelf; AdminAssisted is a server-only
// prebound session fact.
type BeneficiarySelection string

const (
	BeneficiarySelectionLegacyPrebound BeneficiarySelection = "legacy_prebound"
	BeneficiarySelectionUnresolved     BeneficiarySelection = "unresolved"
	BeneficiarySelectionPayerSelf      BeneficiarySelection = "payer_self"
	BeneficiarySelectionAdminAssisted  BeneficiarySelection = "admin_assisted"
)

type SessionActor struct {
	PayerIdentityID       int64
	PayerCustomerID       int64
	BeneficiaryCustomerID int64
	BeneficiarySelection  BeneficiarySelection
	Channel               domain.Channel
}

// SessionConsumer consumes a trusted, opaque payment handoff inside the
// caller's existing transaction. It never accepts raw identity claims.
type SessionConsumer interface {
	ConsumeWithin(context.Context, string, time.Time) (SessionActor, error)
}

// SessionReader authorizes polling after the one-shot checkout mutation has
// consumed the token. It never renews or mutates the session.
type SessionReader interface {
	LookupWithin(context.Context, string, time.Time) (SessionActor, error)
}

// SessionBeneficiarySelector records the only public recipient choice within
// the caller's existing transaction. It derives the recipient from the trusted
// payer; callers cannot submit a customer ID.
type SessionBeneficiarySelector interface {
	SelectPayerSelfWithin(context.Context, string, time.Time) (SessionActor, error)
}

// SessionLifecycle supports idempotent checkout replay: callers first read the
// still-valid actor, then consume only when a new mutation is persisted in the
// same transaction.
type SessionLifecycle interface {
	SessionConsumer
	SessionReader
	SessionBeneficiarySelector
}

// ProviderIntent is a Payment-owned, immutable request projection. It is read
// before a Provider call and outside the command transaction.
type ProviderIntent struct {
	Kind                    effectport.Kind
	PaymentID, RefundID     int64
	PayerIdentityID         int64
	Channel                 domain.Channel
	MerchantOrderNo         string
	RefundNo, RefundReason  string
	ProviderOrderID         string
	ProductID, SKUID        string
	Subject                 string
	RefundCount             int64
	ReasonCode              string
	AmountMinor, TotalMinor int64
	ProfitSharingMarked     bool
	Currency                string
	SourceRefDigest         effectport.Digest
	PayloadDigest           effectport.Digest
}

// AlipayMaxSubjectRunes follows the provider checkout title budget while
// counting Unicode code points rather than UTF-8 bytes.
const AlipayMaxSubjectRunes = 256

type ShopRefundQuery struct {
	AfterSaleID, ProviderOrderID, ProductID, SKUID string
	Count, AmountMinor                             int64
	Currency, Status                               string
	OccurredAt                                     time.Time
	EvidenceDigest, ProviderRefundDigest           effectport.Digest
}

type ShopRefundMaterial struct {
	RefundID, PaymentID, AmountMinor int64
	RefundNo, ProviderOrderID        string
	ProductID, SKUID                 string
	RefundCount                      int64
	ReasonCode, Currency             string
}

type ShopRefundCallback struct {
	AfterSaleID, ProviderOrderID, Status string
	EventDigest, PayloadDigest           [32]byte
	OccurredAt                           time.Time
}

type ShopCallbackVerifier interface {
	VerifyURL(context.Context, map[string]string) (string, error)
	VerifyRefund(context.Context, []byte, map[string]string) (ShopRefundCallback, error)
}

type ShopRefundReconciler interface {
	ValidateRefundMaterial(context.Context, ShopRefundMaterial) error
	QueryRefund(context.Context, string) (ShopRefundQuery, error)
}

type ReconciliationEnqueuer interface {
	EnqueueWithin(context.Context, ReconciliationTarget) error
}

type ReconciliationTarget struct {
	Provider            domain.Provider
	OrderID             int64
	PaymentID, RefundID int64
}

type WeChatPayPaymentQuery struct {
	// Verified response identity; never log or return through public APIs.
	AppID           string `json:"-"`
	PayerOpenID     string `json:"-"`
	MerchantOrderNo string
	Currency        string
	Status          string
	// TransactionReference is the provider-verified query fact passed only to
	// Order's same-transaction paid settlement; Payment persists its digest.
	TransactionReference              string
	AmountMinor                       int64
	OccurredAt                        time.Time
	EvidenceDigest, TransactionDigest effectport.Digest
}

func (WeChatPayPaymentQuery) String() string   { return "WeChatPayPaymentQuery{identity:[REDACTED]}" }
func (WeChatPayPaymentQuery) GoString() string { return "WeChatPayPaymentQuery{identity:[REDACTED]}" }

type WeChatPayRefundQuery struct {
	RefundNo, Currency, Status   string
	AmountMinor, TotalMinor      int64
	OccurredAt                   time.Time
	EvidenceDigest, RefundDigest effectport.Digest
}

type WeChatPayReconciler interface {
	QueryPayment(context.Context, string) (WeChatPayPaymentQuery, error)
	QueryRefund(context.Context, string) (WeChatPayRefundQuery, error)
}

type AlipayPaymentQuery struct {
	MerchantOrderNo, TradeNo, TradeStatus, Currency string
	AmountMinor                                     int64
	OccurredAt                                      time.Time
	EvidenceDigest, TransactionDigest               effectport.Digest
}
type AlipayRefundQuery struct {
	RefundNo, Currency, Status   string
	AmountMinor, TotalMinor      int64
	OccurredAt                   time.Time
	EvidenceDigest, RefundDigest effectport.Digest
}
type AlipayReconciler interface {
	QueryPayment(context.Context, string) (AlipayPaymentQuery, error)
	QueryRefund(context.Context, string) (AlipayRefundQuery, error)
}

type ProviderIntentReader interface {
	ProviderIntent(context.Context, effectport.Kind, effectport.Digest) (ProviderIntent, error)
}

// H5OAuthFacts is minted after one userinfo read verifies both subject IDs.
type H5OAuthFacts struct {
	OpenID      identitydomain.VerifiedFact
	UnionID     identitydomain.VerifiedFact
	DisplayName string
	AvatarURL   string
}

// RefundExposureState is a Payment-owned refund-finality transition delivered
// inside the existing Payment UoW. It carries no provider payload or amount;
// Distribution uses it only to hold or re-evaluate its own frozen facts.
type RefundExposureState string

const (
	RefundExposureOpened      RefundExposureState = "opened"
	RefundExposureFinalFailed RefundExposureState = "final_failed"
)

type RefundExposureEvent struct {
	OrderID, RefundID int64
	State             RefundExposureState
	OccurredAt        time.Time
	ReceiptKey        string
}

func (v RefundExposureEvent) Valid() bool {
	return v.OrderID > 0 && v.RefundID > 0 && (v.State == RefundExposureOpened || v.State == RefundExposureFinalFailed) && !v.OccurredAt.IsZero() && v.ReceiptKey != "" && len(v.ReceiptKey) <= 240
}

// RefundExposureConsumer joins Payment's current transaction. It must perform
// no provider calls and must not mutate Payment/Order data.
type RefundExposureConsumer interface {
	ConsumeRefundExposureWithin(context.Context, RefundExposureEvent) error
}
