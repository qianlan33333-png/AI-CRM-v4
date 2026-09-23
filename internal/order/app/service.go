// Package app coordinates Order transactions. It does not resolve external
// identities or call payment providers.
package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	addresscatalog "github.com/qianlan33333-png/AI-CRM-v3/internal/address/port"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/presentationtime"
)

const (
	DefaultLimit = int32(50)
	MaximumLimit = int32(100)
)

type Receipt struct {
	ID                       int64
	Operation                string
	ActorScope               string
	KeyDigest, PayloadDigest [32]byte
	State                    string
	ResultSnapshot           json.RawMessage
}

type Reservation struct {
	Operation                string
	ActorScope               string
	KeyDigest, PayloadDigest [32]byte
	CreatedAt                time.Time
}

type ImportReceipt struct {
	RunID        string
	SourceDigest [32]byte
	OrderID      int64
}

type Cursor struct {
	CreatedAt time.Time
	ID        int64
}

type ListFilter struct {
	Offset          int32
	Provider        domain.Provider
	Status          domain.Status
	OrderRef        string
	CustomerID      int64
	Product         string
	CreatedFrom     *time.Time
	CreatedTo       *time.Time
	CreatedThrough  *time.Time
	NoCustomerMatch bool
}

type ExportReceipt struct {
	ID            int64
	Actor         int64
	KeyDigest     [32]byte
	FilterDigest  [32]byte
	RowCount      int
	ByteCount     int
	ContentDigest [32]byte
	CreatedAt     time.Time
}

// Store is private to Order app/store. Cross-domain callers use port only.
type Store interface {
	Reserve(context.Context, Reservation) (Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
	Insert(context.Context, domain.Order, int64, time.Time) (domain.Order, error)
	InsertScoped(context.Context, domain.Order, string, time.Time) (domain.Order, error)
	Get(context.Context, int64, bool) (domain.Order, error)
	List(context.Context, *Cursor, int32, ListFilter) ([]domain.Order, error)
	Count(context.Context, ListFilter) (int64, error)
	FindByReference(context.Context, string) ([]domain.Order, error)
	Export(context.Context, ListFilter, int32) ([]domain.Order, error)
	RecordExport(context.Context, ExportReceipt) (ExportReceipt, bool, error)
	UpdateSettlement(context.Context, domain.Order, domain.StatusEvent, string) (domain.Order, error)
	AppendPaidEvent(context.Context, domain.Snapshot) (orderport.PaidEvent, bool, error)
	Import(context.Context, string, [32]byte, domain.Order) (domain.Order, bool, error)
	InsertCheckoutSnapshot(context.Context, orderport.CheckoutSnapshot) error
	ReadCheckoutSnapshot(context.Context, int64) (orderport.CheckoutSnapshot, error)
	CommercePushDeliveryReference(context.Context, domain.Provider, string) (orderport.CommercePushDeliveryReference, error)
}

// providerReferenceStore is an additive private store seam. Existing test
// stores and legacy read consumers keep the ambiguity-safe reference lookup.
type providerReferenceStore interface {
	FindByReferenceForProvider(context.Context, domain.Provider, string) ([]domain.Order, error)
}

// paymentConfirmationEvidenceStore is deliberately a private read seam. A
// Provider reconciliation may prove an existing paid event, but it must never
// manufacture one for an already-paid order whose immutable event is absent.
type paymentConfirmationEvidenceStore interface {
	PaymentConfirmationOccurredAtWithin(context.Context, int64) (time.Time, error)
}

type Service struct {
	uow           platformport.UnitOfWork
	store         Store
	contactCipher interface {
		Encrypt(string) ([]byte, error)
		KeyVersion() int16
	}
	coupons                    couponport.OrderCouponCoordinator
	entitlements               orderport.ServicePeriodEntitlementCoordinator
	paidEvents                 orderport.PaidEventConsumer
	refundEvents               orderport.RefundSettlementConsumer
	attribution                orderport.CheckoutAttributionCoordinator
	productSaleAttribution     orderport.ProductSaleCheckoutCoordinator
	historicalEvidenceVerifier historicalQualificationEvidenceVerifier
	now                        func() time.Time
}

// SetCheckoutCouponCoordinator injects Coupon's transaction-bound reserve /
// consume / release seam. It never grants Order access to Coupon tables.
func (s *Service) SetCheckoutCouponCoordinator(coordinator couponport.OrderCouponCoordinator) error {
	if s == nil || coordinator == nil {
		return orderport.ErrConflict
	}
	s.coupons = coordinator
	return nil
}

// SetServicePeriodEntitlementCoordinator injects the Order-owned fulfillment
// leaf for paid service periods. The coordinator receives only frozen Order
// facts after the authoritative payment transition.
func (s *Service) SetServicePeriodEntitlementCoordinator(coordinator orderport.ServicePeriodEntitlementCoordinator) error {
	if s == nil || coordinator == nil {
		return orderport.ErrConflict
	}
	s.entitlements = coordinator
	return nil
}

// SetPaidEventConsumer binds the sole composition-owned consumer for the
// Order-owned first-paid event. It receives the existing transaction so Order
// settlement, dispatch intent, EER acceptance and River enqueue are atomic.
func (s *Service) SetPaidEventConsumer(consumer orderport.PaidEventConsumer) error {
	if s == nil || consumer == nil {
		return orderport.ErrConflict
	}
	s.paidEvents = consumer
	return nil
}

// SetRefundSettlementConsumer binds the payment-final refund observer. It
// runs only after Order's durable refund status transition was written in the
// same UoW and is optional while Distribution is disabled.
func (s *Service) SetRefundSettlementConsumer(consumer orderport.RefundSettlementConsumer) error {
	if s == nil || consumer == nil {
		return orderport.ErrConflict
	}
	s.refundEvents = consumer
	return nil
}

// SetCheckoutAttributionCoordinator binds Distribution's checkout-time
// promotion recorder. It operates within the Order/Payment UoW, so an order
// can never become payable before an accepted attribution is frozen. The
// coordinator is optional while the Distribution module is disabled.
func (s *Service) SetCheckoutAttributionCoordinator(coordinator orderport.CheckoutAttributionCoordinator) error {
	if s == nil || coordinator == nil {
		return orderport.ErrConflict
	}
	s.attribution = coordinator
	return nil
}

// SetProductSaleCheckoutCoordinator binds the product-activity recorder. It
// receives the same transaction as Distribution attribution and persists only
// its own domain facts through the injected port.
func (s *Service) SetProductSaleCheckoutCoordinator(coordinator orderport.ProductSaleCheckoutCoordinator) error {
	if s == nil || coordinator == nil {
		return orderport.ErrConflict
	}
	s.productSaleAttribution = coordinator
	return nil
}

func NewService(uow platformport.UnitOfWork, store Store) *Service {
	return &Service{uow: uow, store: store, now: time.Now}
}

func (s *Service) SetContactCipher(cipher interface {
	Encrypt(string) ([]byte, error)
	KeyVersion() int16
}) error {
	if s == nil || cipher == nil {
		return orderport.ErrConflict
	}
	s.contactCipher = cipher
	return nil
}

func (s *Service) ReservePaymentWithin(ctx context.Context, id int64) (domain.Snapshot, error) {
	if !ready(s) || id < 1 {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	order, err := s.store.Get(ctx, id, true)
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	snapshot := order.Snapshot()
	if snapshot.RecordOrigin != domain.RecordOriginNative || !snapshot.EffectEligible || snapshot.Status != domain.StatusPendingPayment {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	return snapshot, nil
}

// ReadCheckoutSnapshotWithin exposes only the immutable native-checkout fact
// to an in-transaction consumer. It does not create a second Unit of Work.
func (s *Service) ReadCheckoutSnapshotWithin(ctx context.Context, orderID int64) (orderport.CheckoutSnapshot, error) {
	if !ready(s) || orderID < 1 {
		return orderport.CheckoutSnapshot{}, orderport.ErrNotFound
	}
	return s.store.ReadCheckoutSnapshot(ctx, orderID)
}

func (s *Service) CreatePaymentOrderWithin(ctx context.Context, command orderport.PaymentOrderCommand) (domain.Snapshot, error) {
	// Empty means the legacy no-contact contract for callers created before
	// contact collection levels were introduced.
	if command.ContactCollectionLevel == "" {
		if command.MobileE164 != "" {
			command.ContactCollectionLevel = "mobile"
		} else {
			command.ContactCollectionLevel = "none"
		}
	}
	if !ready(s) || command.Provider != domain.ProviderWeChatPay || command.PayerCustomerID < 1 || command.BeneficiaryCustomerID < 1 || command.ProductID < 1 || command.ProductVersion < 1 || command.UnitAmountMinor < 1 || command.Currency != "CNY" || command.CouponClaimID < 0 || !validPaymentProductType(command.ProductType, command.ServicePeriodDurationDays) || !validPostPurchaseAction(command.PostPurchaseAction) || !validPromotionContext(command.PromotionContext) || !validReferralActivityContext(command.ReferralActivityContext) || !validKey(command.IdempotencyKey) || !validKey(command.ActorScope) || !validContactCollection(command.ContactCollectionLevel, command.MobileE164, command.ShippingAddress) {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	productID := command.ProductID
	productVersion := command.ProductVersion
	var phoneDigest [32]byte
	var phoneCiphertext []byte
	var phoneKeyVersion int16
	var err error
	if command.MobileE164 != "" {
		if len(command.MobileE164) != 14 || !strings.HasPrefix(command.MobileE164, "+861") || strings.IndexFunc(command.MobileE164[1:], func(r rune) bool { return r < '0' || r > '9' }) >= 0 || command.MobileE164[4] < '3' || command.MobileE164[4] > '9' || s.contactCipher == nil {
			return domain.Snapshot{}, orderport.ErrConflict
		}
		phoneDigest = sha256.Sum256([]byte(command.MobileE164))
		phoneCiphertext, err = s.contactCipher.Encrypt(command.MobileE164)
		if err != nil {
			return domain.Snapshot{}, orderport.ErrUnavailable
		}
		phoneKeyVersion = s.contactCipher.KeyVersion()
	}
	if command.ContactCollectionLevel == "shipping_address" && addresscatalog.Validate(command.ShippingAddress.ProvinceCode, command.ShippingAddress.ProvinceName, command.ShippingAddress.CityCode, command.ShippingAddress.CityName, command.ShippingAddress.DistrictCode, command.ShippingAddress.DistrictName) != nil {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	createdAt := s.now().UTC()
	digestInput := struct {
		Command     orderport.PaymentOrderCommand
		PhoneDigest [32]byte
	}{Command: command, PhoneDigest: phoneDigest}
	payload, err := json.Marshal(digestInput)
	if err != nil {
		return domain.Snapshot{}, orderport.ErrUnavailable
	}
	reservation := Reservation{Operation: "create", ActorScope: command.ActorScope, KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: sha256.Sum256(payload), CreatedAt: createdAt}
	receipt, owned, err := s.store.Reserve(ctx, reservation)
	if err != nil || !validReceipt(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
		if err != nil {
			return domain.Snapshot{}, classify(err)
		}
		return domain.Snapshot{}, orderport.ErrConflict
	}
	if !owned {
		var result domain.Snapshot
		if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &result) != nil {
			return domain.Snapshot{}, orderport.ErrUnavailable
		}
		if _, err = domain.Restore(result); err != nil {
			return domain.Snapshot{}, orderport.ErrConflict
		}
		return result, nil
	}
	checkout, err := s.reserveCheckout(ctx, command, createdAt)
	if err != nil {
		return domain.Snapshot{}, err
	}
	input := domain.NewOrderInput{
		Provider: command.Provider, SourceSystem: "v3-checkout", SourceKey: command.MerchantOrderNo,
		MerchantOrderNo: command.MerchantOrderNo, PayerCustomerID: &command.PayerCustomerID,
		BeneficiaryCustomerID: &command.BeneficiaryCustomerID,
		Amount:                domain.Money{AmountMinor: checkout.PayableAmountMinor, Currency: command.Currency},
		Items:                 []domain.ItemSnapshot{{LineNo: 1, ProductID: &productID, ProductVersion: &productVersion, ProductCode: command.ProductCode, ProductName: command.ProductName, UnitAmountMinor: checkout.PayableAmountMinor, Quantity: 1, LineAmountMinor: checkout.PayableAmountMinor}},
		RecordOrigin:          domain.RecordOriginNative, EffectEligible: true, CreatedAt: createdAt,
	}
	order, err := domain.NewOrder(input)
	if err != nil {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	persisted, err := s.store.InsertScoped(ctx, order, command.ActorScope, input.CreatedAt)
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	checkout.OrderID = persisted.Snapshot().ID
	var attribution orderport.CheckoutAttributionResult
	if (command.PromotionContext != "" || command.ReferralActivityContext != "") && s.attribution != nil {
		var attributionErr error
		attribution, attributionErr = s.attribution.RecordCheckoutAttributionWithin(ctx, orderport.CheckoutAttributionCommand{
			OrderID: persisted.Snapshot().ID, OrderItemLine: 1, ProductID: command.ProductID,
			ProductType: command.ProductType, ProductCode: command.ProductCode, ProductName: command.ProductName, PayerCustomerID: command.PayerCustomerID,
			BeneficiaryCustomerID: command.BeneficiaryCustomerID, PromotionContext: command.PromotionContext,
			ReferralActivityContext: command.ReferralActivityContext,
			ItemPaidMinor:           checkout.PayableAmountMinor, OccurredAt: input.CreatedAt,
		})
		if attributionErr != nil {
			return domain.Snapshot{}, classify(attributionErr)
		}
		if attribution.ProfitSharingRequired {
			checkout.ProfitSharingRequired = true
		}
	}
	if command.ReferralActivityContext != "" && s.productSaleAttribution != nil {
		if err = s.productSaleAttribution.RecordProductSaleCheckoutAttributionWithin(ctx, orderport.ProductSaleCheckoutContextCommand{
			OrderID: persisted.Snapshot().ID, OrderVersion: persisted.Snapshot().Version, ProductID: command.ProductID,
			ProductType: command.ProductType, ProductCode: command.ProductCode, ProductName: command.ProductName, ProductVersion: command.ProductVersion,
			PromotionCustomerID: attribution.PromoterCustomerID, PromotionCredentialRef: attribution.PromotionCredentialRef, PolicyVersion: attribution.PolicyVersion,
			CommissionRateBasisPoints: attribution.CommissionRateBasisPoints, WaitDays: attribution.WaitDays, BuyerCustomerID: command.PayerCustomerID,
			BeneficiaryCustomerID: command.BeneficiaryCustomerID, PromotionContext: command.PromotionContext, ReferralActivityContext: command.ReferralActivityContext,
			OccurredAt: input.CreatedAt,
		}); err != nil {
			return domain.Snapshot{}, classify(err)
		}
	}
	// The checkout snapshot is an immutable sale fact. Attribution is evaluated
	// after the Order has an ID but before that fact is inserted, so the positive
	// profit-sharing decision is frozen atomically with every other checkout
	// field; later refund or reconciliation flows cannot rewrite it.
	if err = s.store.InsertCheckoutSnapshot(ctx, checkout); err != nil {
		return domain.Snapshot{}, classify(err)
	}
	if len(phoneCiphertext) > 0 {
		contactStore, ok := s.store.(interface {
			InsertContactSnapshot(context.Context, int64, []byte, int16, time.Time) error
		})
		if !ok || contactStore.InsertContactSnapshot(ctx, persisted.Snapshot().ID, phoneCiphertext, phoneKeyVersion, input.CreatedAt) != nil {
			return domain.Snapshot{}, orderport.ErrUnavailable
		}
	}
	if command.ContactCollectionLevel == "shipping_address" {
		shippingStore, ok := s.store.(interface {
			InsertShippingAddressSnapshot(context.Context, int64, orderport.ShippingAddress, time.Time) error
		})
		if !ok || shippingStore.InsertShippingAddressSnapshot(ctx, persisted.Snapshot().ID, command.ShippingAddress, input.CreatedAt) != nil {
			return domain.Snapshot{}, orderport.ErrUnavailable
		}
	}
	result := persisted.Snapshot()
	result.ProfitSharingRequired = checkout.ProfitSharingRequired
	snapshot, err := json.Marshal(result)
	if err != nil {
		return domain.Snapshot{}, orderport.ErrUnavailable
	}
	completed, err := s.store.Complete(ctx, receipt.ID, snapshot, input.CreatedAt)
	if err != nil || completed.State != "completed" {
		return domain.Snapshot{}, orderport.ErrUnavailable
	}
	return result, nil
}

func validContactCollection(level, mobile string, address orderport.ShippingAddress) bool {
	validMobile := mobile == "" || (len(mobile) == 14 && strings.HasPrefix(mobile, "+861") && mobile[4] >= '3' && mobile[4] <= '9' && strings.IndexFunc(mobile[1:], func(r rune) bool { return r < '0' || r > '9' }) < 0)
	if !validMobile {
		return false
	}
	emptyAddress := address == (orderport.ShippingAddress{})
	switch level {
	case "none":
		return mobile == "" && emptyAddress
	case "mobile":
		return validMobile && mobile != "" && emptyAddress
	case "shipping_address":
		return validMobile && mobile != "" && address.RecipientName != "" && address.ProvinceCode != "" && address.ProvinceName != "" && address.CityCode != "" && address.CityName != "" && address.DistrictCode != "" && address.DistrictName != "" && address.DetailAddress != ""
	default:
		return false
	}
}

func (s *Service) SettlePaymentWithin(ctx context.Context, command orderport.PaymentSettlementCommand) (domain.Snapshot, error) {
	if !ready(s) || command.OrderID < 1 || command.RefundedDelta < 0 || (command.Failed && command.RefundedDelta != 0) || !validKey(command.ReceiptKey) || command.OccurredAt.IsZero() {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	current, err := s.store.Get(ctx, command.OrderID, true)
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	next, refunded := domain.StatusPaid, int64(0)
	if command.Failed {
		if current.Status != domain.StatusPendingPayment {
			return domain.Snapshot{}, orderport.ErrConflict
		}
		next = domain.StatusPaymentFailed
	} else if command.RefundedDelta > 0 {
		if current.Status != domain.StatusPaid && current.Status != domain.StatusPartiallyRefunded {
			return domain.Snapshot{}, orderport.ErrConflict
		}
		refunded = current.RefundedMinor + command.RefundedDelta
		if refunded < current.RefundedMinor || refunded > current.Amount.AmountMinor {
			return domain.Snapshot{}, orderport.ErrConflict
		}
		next = domain.StatusPartiallyRefunded
		if refunded == current.Amount.AmountMinor {
			next = domain.StatusRefunded
		}
	} else if current.Status != domain.StatusPendingPayment {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	updated, event, err := current.ApplySettlement(current.Version, next, refunded, command.OccurredAt.UTC())
	if err != nil {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	if !command.Failed && command.RefundedDelta == 0 {
		updated, err = updated.WithVerifiedProviderTransaction(command.ProviderTransactionNo)
		if err != nil {
			return domain.Snapshot{}, orderport.ErrConflict
		}
	}
	updated, err = s.store.UpdateSettlement(ctx, updated, event, "payment:"+command.ReceiptKey)
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	if err = s.applyCheckoutSettlement(ctx, updated.Snapshot(), command); err != nil {
		return domain.Snapshot{}, err
	}
	if err = s.consumeFirstNativePaidEvent(ctx, current.Snapshot(), updated.Snapshot()); err != nil {
		return domain.Snapshot{}, err
	}
	if command.RefundedDelta > 0 && s.refundEvents != nil {
		// Checkout is an immutable Order-owned snapshot. Its ID/type only
		// enrich the already-persisted refund fact for Distribution; a missing
		// historical snapshot remains usable for downline-order matching but
		// cannot be asserted as a qualification-purchase refund.
		checkout, checkoutErr := s.store.ReadCheckoutSnapshot(ctx, updated.ID)
		if checkoutErr != nil && !errors.Is(checkoutErr, orderport.ErrNotFound) {
			return domain.Snapshot{}, classify(checkoutErr)
		}
		event := orderport.RefundSettlementEvent{Order: updated.Snapshot(), RefundedDelta: command.RefundedDelta, OccurredAt: command.OccurredAt.UTC(), ReceiptKey: command.ReceiptKey}
		if checkoutErr == nil {
			event.CheckoutProductID, event.CheckoutProductType = checkout.ProductID, checkout.ProductType
		}
		if err = s.refundEvents.ConsumeRefundSettlementWithin(ctx, event); err != nil {
			return domain.Snapshot{}, err
		}
	}
	return updated.Snapshot(), nil
}

// VerifyPaymentConfirmationEvidenceWithin proves that an existing native
// Order paid event is exactly the Provider query fact Payment is about to use
// to restore its own missing timestamp. This is read-only and joins the
// caller's UoW; it cannot settle or otherwise alter an Order.
func (s *Service) VerifyPaymentConfirmationEvidenceWithin(ctx context.Context, evidence orderport.PaymentConfirmationEvidence) (domain.Snapshot, error) {
	if !ready(s) || evidence.OrderID < 1 || evidence.ProviderTransactionNo == "" || evidence.ProviderTransactionNo != strings.TrimSpace(evidence.ProviderTransactionNo) || evidence.OccurredAt.IsZero() {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	reader, ok := s.store.(paymentConfirmationEvidenceStore)
	if !ok {
		return domain.Snapshot{}, orderport.ErrUnavailable
	}
	current, err := s.store.Get(ctx, evidence.OrderID, true)
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	if current.RecordOrigin != domain.RecordOriginNative || current.Status != domain.StatusPaid || current.ProviderTransactionNo != evidence.ProviderTransactionNo {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	paidAt, err := reader.PaymentConfirmationOccurredAtWithin(ctx, evidence.OrderID)
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	if !paidAt.UTC().Equal(evidence.OccurredAt.UTC()) {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	return current.Snapshot(), nil
}

func validPaymentProductType(kind string, durationDays int32) bool {
	return (kind == "standard_product" && durationDays == 0) || (kind == "service_period" && durationDays > 0)
}

func validPostPurchaseAction(raw json.RawMessage) bool {
	return len(raw) == 0 || len(raw) <= 8<<10 && json.Valid(raw)
}

func validPromotionContext(value string) bool {
	return len(value) <= 512 && value == strings.TrimSpace(value) && strings.IndexFunc(value, func(r rune) bool { return r < 0x21 || r > 0x7e }) < 0
}

func (s *Service) reserveCheckout(ctx context.Context, command orderport.PaymentOrderCommand, at time.Time) (orderport.CheckoutSnapshot, error) {
	postPurchaseAction := append([]byte(nil), command.PostPurchaseAction...)
	if len(postPurchaseAction) == 0 {
		postPurchaseAction = []byte(`{}`)
	}
	snapshot := orderport.CheckoutSnapshot{ProductType: command.ProductType, ProductID: command.ProductID, ProductCode: command.ProductCode, ProductName: command.ProductName, ProductVersion: command.ProductVersion, ServicePeriodDurationDays: command.ServicePeriodDurationDays, GrossAmountMinor: command.UnitAmountMinor, PayableAmountMinor: command.UnitAmountMinor, Currency: command.Currency, PostPurchaseAction: postPurchaseAction, ReservedAt: at}
	if command.ReferralActivityContext != "" {
		snapshot.ReferralActivityContextDigest = sha256.Sum256([]byte(command.ReferralActivityContext))
	}
	if command.PromotionContext != "" {
		snapshot.PromotionContextDigest = sha256.Sum256([]byte(command.PromotionContext))
	}
	if s.coupons == nil {
		return snapshot, nil
	}
	reserved, err := s.coupons.ReserveWithin(ctx, couponport.ReserveCommand{HolderCustomerID: command.PayerCustomerID, ClaimID: command.CouponClaimID, ProductID: command.ProductID, ProductCode: command.ProductCode, ProductType: command.ProductType, GrossAmountMinor: command.UnitAmountMinor, Currency: command.Currency, OrderReference: command.MerchantOrderNo, ActorScope: command.ActorScope, IdempotencyKey: command.IdempotencyKey, ReservedAt: at})
	if err != nil {
		return orderport.CheckoutSnapshot{}, err
	}
	if reserved.ProductID != command.ProductID || reserved.ProductType != command.ProductType || reserved.ProductCode != command.ProductCode || reserved.GrossAmountMinor != command.UnitAmountMinor || reserved.Currency != command.Currency || reserved.DiscountAmountMinor < 0 || reserved.PayableAmountMinor < 1 || reserved.PayableAmountMinor != reserved.GrossAmountMinor-reserved.DiscountAmountMinor {
		return orderport.CheckoutSnapshot{}, orderport.ErrConflict
	}
	if !reserved.CouponApplied {
		if reserved.ReservationRef != "" || reserved.ClaimID != 0 || reserved.CouponID != 0 || reserved.RuleVersion != 0 || reserved.DiscountAmountMinor != 0 || reserved.PayableAmountMinor != command.UnitAmountMinor {
			return orderport.CheckoutSnapshot{}, orderport.ErrConflict
		}
		return snapshot, nil
	}
	if reserved.ReservationRef == "" || len(reserved.ReservationRef) > 240 || reserved.ClaimID < 1 || reserved.CouponID < 1 || reserved.RuleVersion < 1 || reserved.DiscountAmountMinor < 1 || reserved.DiscountAmountMinor >= reserved.GrossAmountMinor {
		return orderport.CheckoutSnapshot{}, orderport.ErrConflict
	}
	snapshot.CouponApplied, snapshot.CouponReservationRef, snapshot.CouponClaimID, snapshot.CouponID, snapshot.CouponRuleVersion = true, reserved.ReservationRef, reserved.ClaimID, int64(reserved.CouponID), reserved.RuleVersion
	snapshot.DiscountAmountMinor, snapshot.PayableAmountMinor = reserved.DiscountAmountMinor, reserved.PayableAmountMinor
	return snapshot, nil
}

func (s *Service) applyCheckoutSettlement(ctx context.Context, order domain.Snapshot, command orderport.PaymentSettlementCommand) error {
	checkout, err := s.store.ReadCheckoutSnapshot(ctx, order.ID)
	if errors.Is(err, orderport.ErrNotFound) {
		// Existing orders predate the immutable checkout table. Their missing
		// evidence must remain unknown; no coupon or entitlement is inferred.
		return nil
	}
	if err != nil {
		return classify(err)
	}
	if checkout.OrderID != order.ID || checkout.PayableAmountMinor != order.Amount.AmountMinor || checkout.Currency != order.Amount.Currency || !validPaymentProductType(checkout.ProductType, checkout.ServicePeriodDurationDays) {
		return orderport.ErrConflict
	}
	actorScope := "payment:order:" + strconv.FormatInt(order.ID, 10)
	if command.Failed {
		if checkout.CouponApplied && s.coupons != nil {
			_, err = s.coupons.ReleaseWithin(ctx, couponport.ReleaseCommand{ReservationRef: checkout.CouponReservationRef, OrderReference: order.MerchantOrderNo, CloseReason: "payment_final_failed", ActorScope: actorScope, IdempotencyKey: "payment.release:" + command.ReceiptKey, ClosedAt: command.OccurredAt.UTC()})
		}
		return err
	}
	if command.RefundedDelta > 0 {
		if checkout.ProductType == "service_period" && s.entitlements != nil {
			_, err = s.entitlements.ApplyServicePeriodRefundWithin(ctx, orderport.ServicePeriodRefundCommand{SourceOrderID: order.ID, RefundAmountMinor: command.RefundedDelta, ProcessedAt: command.OccurredAt.UTC()})
		}
		return err
	}
	if checkout.CouponApplied && s.coupons != nil {
		_, err = s.coupons.ConsumeWithin(ctx, couponport.ConsumeCommand{ReservationRef: checkout.CouponReservationRef, OrderReference: order.MerchantOrderNo, SettledAmountMinor: checkout.PayableAmountMinor, SettledCurrency: checkout.Currency, ActorScope: actorScope, IdempotencyKey: "payment.consume:" + command.ReceiptKey, SettledAt: command.OccurredAt.UTC()})
		if err != nil {
			return err
		}
	}
	if checkout.ProductType == "service_period" && s.entitlements != nil {
		if order.BeneficiaryCustomerID == nil || *order.BeneficiaryCustomerID < 1 {
			return orderport.ErrConflict
		}
		_, err = s.entitlements.GrantPaidServicePeriodWithin(ctx, orderport.ServicePeriodGrantCommand{SourceOrderID: order.ID, BeneficiaryCustomerID: int64(*order.BeneficiaryCustomerID), ServiceProductID: checkout.ProductID, ProductName: checkout.ProductName, DurationDays: checkout.ServicePeriodDurationDays, PaidAt: command.OccurredAt.UTC(), ProcessedAt: s.now().UTC()})
	}
	return err
}

func (s *Service) Create(ctx context.Context, command orderport.CreateCommand) (domain.Snapshot, error) {
	if !ready(s) || command.Actor < 1 || !validKey(command.IdempotencyKey) || command.Input.RecordOrigin == domain.RecordOriginHistory {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	now := s.now().UTC()
	command.Input.RecordOrigin = domain.RecordOriginNative
	command.Input.CreatedAt = now
	order, err := domain.NewOrder(command.Input)
	if err != nil {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	// Creation time is assigned by the server and must not rotate the logical
	// command digest when the same idempotency key is replayed.
	digestInput := command.Input
	digestInput.CreatedAt = time.Time{}
	payload, err := json.Marshal(digestInput)
	if err != nil {
		return domain.Snapshot{}, orderport.ErrUnavailable
	}
	reservation := Reservation{
		Operation: "create", ActorScope: fmt.Sprintf("admin:%d", command.Actor),
		KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now,
	}
	var result domain.Snapshot
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := s.store.Reserve(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !validReceipt(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return orderport.ErrConflict
		}
		if !owned {
			if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &result) != nil {
				return orderport.ErrUnavailable
			}
			_, restoreErr := domain.Restore(result)
			return restoreErr
		}
		persisted, insertErr := s.store.Insert(tx, order, command.Actor, now)
		if insertErr != nil {
			return insertErr
		}
		result = persisted.Snapshot()
		snapshot, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return marshalErr
		}
		completed, completeErr := s.store.Complete(tx, receipt.ID, snapshot, now)
		if completeErr != nil || completed.State != "completed" {
			return orderport.ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, id int64) (domain.Snapshot, error) {
	if !ready(s) || id < 1 {
		return domain.Snapshot{}, orderport.ErrNotFound
	}
	var result domain.Order
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var getErr error
		result, getErr = s.store.Get(tx, id, false)
		return getErr
	})
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	return result.Snapshot(), nil
}

func (s *Service) List(ctx context.Context, query orderport.ListQuery) (orderport.Page, error) {
	if !ready(s) || !validListQuery(query) {
		return orderport.Page{}, orderport.ErrUnavailable
	}
	if query.Limit == 0 {
		query.Limit = DefaultLimit
	}
	if query.Limit < 1 || query.Limit > MaximumLimit {
		return orderport.Page{}, orderport.ErrConflict
	}
	var before *Cursor
	if query.Cursor != "" {
		decoded, err := decodeCursor(query.Cursor)
		if err != nil {
			return orderport.Page{}, orderport.ErrConflict
		}
		before = &decoded
	}
	var rows []domain.Order
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var listErr error
		total, listErr = s.store.Count(tx, filterFrom(query))
		if listErr != nil {
			return listErr
		}
		rows, listErr = s.store.List(tx, before, query.Limit+1, filterFrom(query))
		return listErr
	})
	if err != nil {
		return orderport.Page{}, classify(err)
	}
	if len(rows) > int(query.Limit)+1 {
		return orderport.Page{}, orderport.ErrUnavailable
	}
	page := orderport.Page{Items: make([]domain.Snapshot, 0, min(len(rows), int(query.Limit))), Total: total}
	for index, order := range rows {
		if index == int(query.Limit) {
			break
		}
		page.Items = append(page.Items, order.Snapshot())
	}
	if len(rows) > int(query.Limit) {
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeCursor(Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

func (s *Service) CustomerActivities(ctx context.Context, query orderport.CustomerActivityQuery) (orderport.CustomerActivityPage, error) {
	if !ready(s) || query.CustomerID < 1 || query.Limit < 1 || query.Limit > MaximumLimit+1 || query.Watermark.IsZero() || query.AfterID < 0 ||
		(query.AfterAt.IsZero() && query.AfterID != 0) || (!query.AfterAt.IsZero() && query.AfterAt.After(query.Watermark)) {
		return orderport.CustomerActivityPage{}, orderport.ErrConflict
	}
	beforeAt, beforeID := query.AfterAt, query.AfterID
	if beforeAt.IsZero() {
		beforeAt, beforeID = query.Watermark.UTC(), math.MaxInt64
	}
	rows := []domain.Order{}
	through := query.Watermark.UTC()
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var listErr error
		rows, listErr = s.store.List(tx, &Cursor{CreatedAt: beforeAt.UTC(), ID: beforeID}, query.Limit, ListFilter{CustomerID: query.CustomerID, CreatedThrough: &through})
		return listErr
	})
	if err != nil {
		return orderport.CustomerActivityPage{}, classify(err)
	}
	page := orderport.CustomerActivityPage{Items: make([]orderport.CustomerActivity, 0, len(rows))}
	for _, row := range rows {
		snapshot := row.Snapshot()
		relationship := customerOrderRelationship(snapshot, query.CustomerID)
		if relationship == "" { // Store predicates must remain defense in depth.
			return orderport.CustomerActivityPage{}, orderport.ErrUnavailable
		}
		names := make([]string, 0, len(snapshot.Items))
		for _, item := range snapshot.Items {
			names = append(names, item.ProductName)
		}
		page.Items = append(page.Items, orderport.CustomerActivity{OrderID: snapshot.ID, Relationship: relationship, ProductNames: names,
			Provider: snapshot.Provider, Status: snapshot.Status, Amount: snapshot.Amount, RefundedMinor: snapshot.RefundedMinor,
			RecordOrigin: snapshot.RecordOrigin, OccurredAt: snapshot.CreatedAt})
	}
	return page, nil
}

func customerOrderRelationship(snapshot domain.Snapshot, customerID int64) string {
	payer := snapshot.PayerCustomerID != nil && *snapshot.PayerCustomerID == customerID
	beneficiary := snapshot.BeneficiaryCustomerID != nil && *snapshot.BeneficiaryCustomerID == customerID
	switch {
	case payer && beneficiary:
		return "payer_and_beneficiary"
	case payer:
		return "payer"
	case beneficiary:
		return "beneficiary"
	default:
		return ""
	}
}

var _ orderport.CustomerActivityReader = (*Service)(nil)

func (s *Service) CustomerOrderSummary(ctx context.Context, customerID int64, recentLimit int32) (orderport.CustomerOrderSummary, error) {
	if !ready(s) || customerID < 1 || recentLimit < 1 || recentLimit > MaximumLimit {
		return orderport.CustomerOrderSummary{}, orderport.ErrConflict
	}
	var out orderport.CustomerOrderSummary
	err := s.uow.Within(ctx, func(tx context.Context) error {
		base := ListFilter{CustomerID: customerID}
		var err error
		if out.Total, err = s.store.Count(tx, base); err != nil {
			return err
		}
		if out.Paid, err = s.store.Count(tx, ListFilter{CustomerID: customerID, Status: domain.StatusPaid}); err != nil {
			return err
		}
		if out.Failed, err = s.store.Count(tx, ListFilter{CustomerID: customerID, Status: domain.StatusPaymentFailed}); err != nil {
			return err
		}
		if out.Refunded, err = s.store.Count(tx, ListFilter{CustomerID: customerID, Status: domain.StatusRefunded}); err != nil {
			return err
		}
		partial, err := s.store.Count(tx, ListFilter{CustomerID: customerID, Status: domain.StatusPartiallyRefunded})
		if err != nil {
			return err
		}
		out.Refunded += partial
		rows, err := s.store.List(tx, nil, recentLimit, base)
		if err != nil {
			return err
		}
		out.Recent = make([]domain.Snapshot, 0, len(rows))
		for _, row := range rows {
			out.Recent = append(out.Recent, row.Snapshot())
		}
		return nil
	})
	if err != nil {
		return orderport.CustomerOrderSummary{}, classify(err)
	}
	return out, nil
}

var _ orderport.CustomerOrderSummaryReader = (*Service)(nil)
var _ orderport.CustomerScopedQuery = (*Service)(nil)

// CommercePushDeliveryReference resolves the existing compatibility order
// reference without exposing Order persistence. Historical snapshots only
// carry their owned source coordinates; callers cannot use a coincidental V3
// numeric primary key to discover a V2 delivery row.
func (s *Service) CommercePushDeliveryReference(ctx context.Context, provider domain.Provider, reference string) (orderport.CommercePushDeliveryReference, error) {
	if !ready(s) || !validScope(reference) {
		return orderport.CommercePushDeliveryReference{}, orderport.ErrNotFound
	}
	var out orderport.CommercePushDeliveryReference
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		out, readErr = s.store.CommercePushDeliveryReference(tx, provider, reference)
		return readErr
	})
	if err != nil {
		return orderport.CommercePushDeliveryReference{}, classify(err)
	}
	return out, nil
}

var _ orderport.CommercePushDeliveryReferenceReader = (*Service)(nil)

func (s *Service) GetByReference(ctx context.Context, reference string) (domain.Snapshot, error) {
	if !ready(s) || !validScope(reference) {
		return domain.Snapshot{}, orderport.ErrNotFound
	}
	var matches []domain.Order
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var findErr error
		matches, findErr = s.store.FindByReference(tx, reference)
		return findErr
	})
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	if len(matches) == 0 {
		return domain.Snapshot{}, orderport.ErrNotFound
	}
	if len(matches) != 1 {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	return matches[0].Snapshot(), nil
}

// GetByReferenceForProvider resolves the legacy merchant reference only in
// the explicitly selected payment provider. This is a read-only detail seam;
// it does not relax the ambiguity-safe behaviour of GetByReference.
func (s *Service) GetByReferenceForProvider(ctx context.Context, provider domain.Provider, reference string) (domain.Snapshot, error) {
	if !ready(s) || !validScope(reference) || !validProvider(provider) {
		return domain.Snapshot{}, orderport.ErrNotFound
	}
	store, ok := s.store.(providerReferenceStore)
	if !ok {
		return domain.Snapshot{}, orderport.ErrUnavailable
	}
	var matches []domain.Order
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var findErr error
		matches, findErr = store.FindByReferenceForProvider(tx, provider, reference)
		return findErr
	})
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	if len(matches) == 0 {
		return domain.Snapshot{}, orderport.ErrNotFound
	}
	if len(matches) != 1 {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	return matches[0].Snapshot(), nil
}

func validProvider(provider domain.Provider) bool {
	return provider == domain.ProviderWeChatPay || provider == domain.ProviderWeChatShop || provider == domain.ProviderAlipay
}

func (s *Service) GetByReferenceForCustomer(ctx context.Context, reference string, customerID int64) (domain.Snapshot, error) {
	if !ready(s) || !validScope(reference) || customerID < 1 {
		return domain.Snapshot{}, orderport.ErrNotFound
	}
	var matches []domain.Order
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var listErr error
		// Store.List compiles CustomerID and OrderRef into the same SQL WHERE
		// clause. The service must never perform an unrestricted detail read
		// and apply a machine-client scope after the fact.
		matches, listErr = s.store.List(tx, nil, 2, ListFilter{CustomerID: customerID, OrderRef: reference})
		return listErr
	})
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	if len(matches) == 0 {
		return domain.Snapshot{}, orderport.ErrNotFound
	}
	if len(matches) != 1 {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	return matches[0].Snapshot(), nil
}

func (s *Service) PreviewExport(ctx context.Context, query orderport.ListQuery) (orderport.ExportPreview, error) {
	if !ready(s) || !validListQuery(query) {
		return orderport.ExportPreview{}, orderport.ErrConflict
	}
	var rows []domain.Order
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var exportErr error
		rows, exportErr = s.store.Export(tx, filterFrom(query), 10001)
		return exportErr
	})
	if err != nil {
		return orderport.ExportPreview{}, classify(err)
	}
	return orderport.ExportPreview{Rows: min(len(rows), 10000), Truncated: len(rows) > 10000}, nil
}

func (s *Service) ExportCSV(ctx context.Context, query orderport.ListQuery, actor int64, idempotencyKey string) (orderport.ExportResult, error) {
	if !ready(s) || actor < 1 || !validKey(idempotencyKey) || !validListQuery(query) {
		return orderport.ExportResult{}, orderport.ErrConflict
	}
	filterPayload, _ := json.Marshal(filterFrom(query))
	receipt := ExportReceipt{Actor: actor, KeyDigest: sha256.Sum256([]byte(idempotencyKey)), FilterDigest: sha256.Sum256(filterPayload), CreatedAt: s.now().UTC()}
	var result orderport.ExportResult
	err := s.uow.Within(ctx, func(tx context.Context) error {
		rows, exportErr := s.store.Export(tx, filterFrom(query), 10001)
		if exportErr != nil {
			return exportErr
		}
		if len(rows) > 10000 {
			return orderport.ErrConflict
		}
		content := encodeCSV(rows)
		if len(content) > 5<<20 {
			return orderport.ErrConflict
		}
		receipt.RowCount, receipt.ByteCount, receipt.ContentDigest = len(rows), len(content), sha256.Sum256(content)
		stored, _, recordErr := s.store.RecordExport(tx, receipt)
		if recordErr != nil {
			return recordErr
		}
		if stored.FilterDigest != receipt.FilterDigest || stored.ContentDigest != receipt.ContentDigest || stored.RowCount != receipt.RowCount || stored.ByteCount != receipt.ByteCount {
			return orderport.ErrConflict
		}
		result = orderport.ExportResult{ReceiptID: stored.ID, Rows: len(rows), Bytes: len(content), Content: content, ContentDigest: receipt.ContentDigest}
		return nil
	})
	if err != nil {
		return orderport.ExportResult{}, classify(err)
	}
	return result, nil
}

func validListQuery(query orderport.ListQuery) bool {
	if query.CustomerID < 0 || query.Offset < 0 || query.Offset > 1000000 || (query.Cursor != "" && query.Offset != 0) || len(query.OrderRef) > 200 || len(query.Product) > 200 {
		return false
	}
	if query.Provider != "" && query.Provider != domain.ProviderWeChatPay && query.Provider != domain.ProviderWeChatShop && query.Provider != domain.ProviderAlipay {
		return false
	}
	if query.Status != "" {
		switch query.Status {
		case domain.StatusPendingPayment, domain.StatusPaid, domain.StatusPartiallyRefunded, domain.StatusRefunded, domain.StatusCancelled, domain.StatusPaymentFailed, domain.StatusClosed:
		default:
			return false
		}
	}
	if query.NoCustomerMatch && query.CustomerID != 0 {
		return false
	}
	return query.CreatedFrom == nil || query.CreatedTo == nil || !query.CreatedFrom.After(*query.CreatedTo)
}

func filterFrom(query orderport.ListQuery) ListFilter {
	return ListFilter{Offset: query.Offset, Provider: query.Provider, Status: query.Status, OrderRef: strings.TrimSpace(query.OrderRef), CustomerID: query.CustomerID, Product: strings.TrimSpace(query.Product), CreatedFrom: query.CreatedFrom, CreatedTo: query.CreatedTo, NoCustomerMatch: query.NoCustomerMatch}
}

func encodeCSV(orders []domain.Order) []byte {
	var builder strings.Builder
	builder.WriteString("created_at,merchant_order_no,provider_transaction_no,provider,payer_customer_id,beneficiary_customer_id,product_code,product_name,amount_minor,currency,status,record_origin\r\n")
	for _, order := range orders {
		snapshot := order.Snapshot()
		productCode, productName := "", ""
		if len(snapshot.Items) > 0 {
			productCode, productName = snapshot.Items[0].ProductCode, snapshot.Items[0].ProductName
		}
		values := []string{presentationtime.FormatShanghaiDateTime(snapshot.CreatedAt), snapshot.MerchantOrderNo, snapshot.ProviderTransactionNo, orderProviderLabel(snapshot.Provider), optionalID(snapshot.PayerCustomerID), optionalID(snapshot.BeneficiaryCustomerID), productCode, productName, strconv.FormatInt(snapshot.Amount.AmountMinor, 10), snapshot.Amount.Currency, orderStatusLabel(snapshot.Status), orderRecordOriginLabel(snapshot.RecordOrigin)}
		for index, value := range values {
			if index > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(csvCell(value))
		}
		builder.WriteString("\r\n")
	}
	return []byte(builder.String())
}

func orderProviderLabel(value domain.Provider) string {
	switch value {
	case domain.ProviderWeChatPay:
		return "微信支付"
	case domain.ProviderWeChatShop:
		return "微信小店"
	case domain.ProviderAlipay:
		return "支付宝"
	default:
		return "支付来源待确认"
	}
}

func orderStatusLabel(value domain.Status) string {
	switch value {
	case domain.StatusPendingPayment:
		return "待支付"
	case domain.StatusPaid:
		return "已支付"
	case domain.StatusPartiallyRefunded:
		return "部分退款"
	case domain.StatusRefunded:
		return "已退款"
	case domain.StatusCancelled:
		return "已取消"
	case domain.StatusPaymentFailed:
		return "支付失败"
	case domain.StatusClosed:
		return "已关闭"
	default:
		return "订单状态待确认"
	}
}

func orderRecordOriginLabel(value domain.RecordOrigin) string {
	switch value {
	case domain.RecordOriginNative:
		return "系统订单"
	case domain.RecordOriginHistory:
		return "历史导入"
	default:
		return "订单来源待确认"
	}
}

func optionalID(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func csvCell(value string) string {
	if value != "" && strings.ContainsRune("=+-@", rune(value[0])) {
		value = "'" + value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func (s *Service) ApplySettlement(ctx context.Context, command orderport.SettlementCommand) (domain.Snapshot, error) {
	if !ready(s) || command.OrderID < 1 || command.ExpectedVersion < 1 || !validKey(command.IdempotencyKey) || !validScope(command.ActorScope) {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	payload, _ := json.Marshal(command)
	reservation := Reservation{Operation: "settlement", ActorScope: command.ActorScope, KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: sha256.Sum256(payload), CreatedAt: command.OccurredAt.UTC()}
	var result domain.Snapshot
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := s.store.Reserve(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !validReceipt(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return orderport.ErrConflict
		}
		if !owned {
			if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &result) != nil {
				return orderport.ErrUnavailable
			}
			_, restoreErr := domain.Restore(result)
			return restoreErr
		}
		current, getErr := s.store.Get(tx, command.OrderID, true)
		if getErr != nil {
			return getErr
		}
		updated, event, settleErr := current.ApplySettlement(command.ExpectedVersion, command.Status, command.RefundedMinor, command.OccurredAt.UTC())
		if settleErr != nil {
			return settleErr
		}
		if updated.Version != current.Version {
			updated, settleErr = s.store.UpdateSettlement(tx, updated, event, command.ActorScope)
			if settleErr != nil {
				return settleErr
			}
			if settleErr = s.consumeFirstNativePaidEvent(tx, current.Snapshot(), updated.Snapshot()); settleErr != nil {
				return settleErr
			}
		}
		result = updated.Snapshot()
		snapshot, _ := json.Marshal(result)
		completed, completeErr := s.store.Complete(tx, receipt.ID, snapshot, command.OccurredAt.UTC())
		if completeErr != nil || completed.State != "completed" {
			return orderport.ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	return result, nil
}

func (s *Service) ImportHistorical(ctx context.Context, command orderport.HistoricalImportCommand) (domain.Snapshot, error) {
	if !ready(s) || !validScope(command.RunID) || command.SourceDigest == ([32]byte{}) || command.Order.RecordOrigin != domain.RecordOriginHistory || command.Order.EffectEligible {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	command.Order.ID = 0
	order, err := domain.Restore(command.Order)
	if err != nil {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	var result domain.Order
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var importErr error
		result, _, importErr = s.store.Import(tx, command.RunID, command.SourceDigest, order)
		return importErr
	})
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	return result.Snapshot(), nil
}

// consumeFirstNativePaidEvent appends exactly one immutable Order event when
// a native, effect-eligible order first transitions into paid. The consumer is
// optional only for old direct-library callers; cmd/aicrm always injects the
// Outbound bridge so a live settlement cannot commit a split intent/effect.
func (s *Service) consumeFirstNativePaidEvent(ctx context.Context, previous, current domain.Snapshot) error {
	if previous.Status == domain.StatusPaid || current.Status != domain.StatusPaid ||
		current.RecordOrigin != domain.RecordOriginNative || !current.EffectEligible || current.Version == previous.Version {
		return nil
	}
	event, created, err := s.store.AppendPaidEvent(ctx, current)
	if err != nil {
		return classify(err)
	}
	if !event.Valid() {
		return orderport.ErrUnavailable
	}
	if s.paidEvents == nil {
		// A pre-composition library caller still preserves the Order event. The
		// deployed composition always provides the bridge before payment routes
		// are exposed, so this cannot claim an outbound dispatch was accepted.
		return nil
	}
	if !created && event.OrderID != current.ID {
		return orderport.ErrConflict
	}
	return s.paidEvents.ConsumePaidEventWithin(ctx, event)
}

func ready(service *Service) bool {
	return service != nil && service.uow != nil && service.store != nil && service.now != nil
}

func validKey(value string) bool {
	return value == strings.TrimSpace(value) && len(value) >= 16 && len(value) <= 200
}

func validReferralActivityContext(value string) bool {
	return len(value) <= 512 && value == strings.TrimSpace(value) && strings.IndexFunc(value, func(r rune) bool { return r < 0x21 || r > 0x7e }) < 0
}
func validScope(value string) bool {
	return value == strings.TrimSpace(value) && len(value) >= 1 && len(value) <= 200
}

func validReceipt(receipt Receipt, reservation Reservation) bool {
	return receipt.ID > 0 && receipt.Operation == reservation.Operation && receipt.ActorScope == reservation.ActorScope &&
		subtle.ConstantTimeCompare(receipt.KeyDigest[:], reservation.KeyDigest[:]) == 1 && (receipt.State == "in_progress" || receipt.State == "completed")
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, orderport.ErrNotFound):
		return orderport.ErrNotFound
	case errors.Is(err, orderport.ErrConflict), errors.Is(err, domain.ErrInvalidOrder), errors.Is(err, domain.ErrInvalidSettlement), errors.Is(err, domain.ErrInvalidTransition), errors.Is(err, domain.ErrVersionConflict):
		return orderport.ErrConflict
	default:
		return orderport.ErrUnavailable
	}
}

func encodeCursor(cursor Cursor) string {
	payload := cursor.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(cursor.ID, 10)
	digest := sha256.Sum256([]byte("aicrm-order-cursor-v1\x00" + payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + hex.EncodeToString(digest[:8])))
}

func decodeCursor(value string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, err
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return Cursor{}, errors.New("invalid cursor")
	}
	payload := parts[0] + "|" + parts[1]
	digest := sha256.Sum256([]byte("aicrm-order-cursor-v1\x00" + payload))
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(hex.EncodeToString(digest[:8]))) != 1 {
		return Cursor{}, errors.New("invalid cursor checksum")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return Cursor{}, err
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 1 {
		return Cursor{}, errors.New("invalid cursor id")
	}
	return Cursor{CreatedAt: createdAt.UTC(), ID: id}, nil
}
