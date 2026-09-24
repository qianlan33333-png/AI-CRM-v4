package app

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	addresscatalog "github.com/qianlan33333-png/AI-CRM-v3/internal/address/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func emptyShippingAddress(value paymentport.ShippingAddress) bool {
	return value == (paymentport.ShippingAddress{})
}

func validShippingAddress(value paymentport.ShippingAddress) bool {
	return value.RecipientName != "" && value.DetailAddress != "" && addresscatalog.Validate(value.ProvinceCode, value.ProvinceName, value.CityCode, value.CityName, value.DistrictCode, value.DistrictName) == nil
}

type Store interface {
	CreatePayment(context.Context, domain.Payment, [32]byte, [32]byte, string) (domain.Payment, bool, error)
	ReplayPayment(context.Context, [32]byte, [32]byte, string) (domain.Payment, bool, error)
	BindPaymentEffect(context.Context, domain.Payment, effectport.PaymentV1Intent, map[string]any) (domain.Payment, error)
	GetPayment(context.Context, int64, bool) (domain.Payment, error)
	GetHandoff(context.Context, int64) (paymentport.Handoff, error)
	ReservedRefundMinor(context.Context, int64) (int64, error)
	ReservedProfitSharingMinor(context.Context, int64) (int64, error)
	HasNonTerminalRefund(context.Context, int64) (bool, error)
	CreateRefund(context.Context, domain.Refund, [32]byte, [32]byte, string) (domain.Refund, bool, error)
	ReplayRefund(context.Context, [32]byte, [32]byte, string) (domain.Refund, bool, error)
	FindRefundByIdempotencyKey(context.Context, [32]byte, string) (domain.Refund, bool, error)
	BindRefundEffect(context.Context, domain.Refund, effectport.PaymentV1Intent, map[string]any) (domain.Refund, error)
	GetRefund(context.Context, int64, bool) (domain.Refund, error)
	GetRefundByProviderReference(context.Context, domain.Provider, string, bool) (domain.Refund, error)
	GetShopRefundMaterial(context.Context, int64) (paymentport.ShopRefundMaterial, error)
	RecordReconciliation(context.Context, int64, effectport.Digest, string, time.Time) (bool, error)
	RecordPaymentReconciliation(context.Context, int64, effectport.Digest, string, time.Time) (bool, error)
	GetPaymentByMerchantProvider(context.Context, domain.Provider, string, bool) (domain.Payment, error)
	ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error)
	ListRefundsForPayment(context.Context, domain.Provider, string, int32, int32) ([]paymentport.RefundProjection, int64, error)
	ListEffectBindings(context.Context, domain.Provider, string) ([]paymentport.EffectProjection, error)
	UpdatePaymentSettlement(context.Context, domain.Payment, string, string) (domain.Payment, error)
	UpdateRefundSettlement(context.Context, domain.Refund, string, string) (domain.Refund, error)
	GetPaymentByMerchant(context.Context, string, bool) (domain.Payment, error)
	GetRefundByNumber(context.Context, string, bool) (domain.Refund, error)
	ClaimCallback(context.Context, string, [32]byte, [32]byte, string, string, int64) (bool, error)
	ImportTerminalPayment(context.Context, domain.Payment, [32]byte, string) (domain.Payment, error)
	ImportTerminalRefund(context.Context, domain.Refund, [32]byte, string) (domain.Refund, error)
}

type paidConfirmationRestorer interface {
	RestorePaidConfirmation(context.Context, domain.Payment, string, string) (domain.Payment, error)
}

type Service struct {
	lineage                 identityport.CanonicalLineageReader
	receiverIDs             identityport.PaymentIdentityReader
	uow                     platformport.UnitOfWork
	store                   Store
	orders                  orderport.PaymentCoordinator
	sessions                paymentport.SessionLifecycle
	effects                 effectport.TransactionalAccepter
	effectReader            effectport.Reader
	shopReconciler          paymentport.ShopRefundReconciler
	payReconciler           paymentport.WeChatPayReconciler
	alipayReconciler        paymentport.AlipayReconciler
	profitSharingReconciler paymentport.ProfitSharingReconciler
	receiverStatusObserver  paymentport.ProfitSharingReceiverStatusObserver
	reconcileJobs           paymentport.ReconciliationEnqueuer
	products                productport.CheckoutProductReader
	refundExposureConsumer  paymentport.RefundExposureConsumer
	miniAppID               string
	h5AppID                 string
	alipayAppID             string
	profitSharingEnabled    bool
	now                     func() time.Time
}

func (s *Service) SetAlipayAppID(appID string) error {
	if s == nil || !validScope(appID) {
		return paymentport.ErrInvalid
	}
	s.alipayAppID = appID
	return nil
}

func (s *Service) SetPaymentChannelAppIDs(miniProgramAppID, h5OfficialAccountAppID string) error {
	if s == nil || !validScope(miniProgramAppID) || h5OfficialAccountAppID != "" && !validScope(h5OfficialAccountAppID) {
		return paymentport.ErrInvalid
	}
	s.miniAppID, s.h5AppID = miniProgramAppID, h5OfficialAccountAppID
	return nil
}

// SetProfitSharingEnabled records Composition's explicit merchant capability
// decision. It is not a browser setting: Payment checks it before accepting a
// receiver-add effect or any money-moving distribution instruction.
func (s *Service) SetProfitSharingEnabled(enabled bool) error {
	if s == nil {
		return paymentport.ErrInvalid
	}
	s.profitSharingEnabled = enabled
	return nil
}

// SetRefundExposureConsumer registers the in-UoW observer for refund state
// changes. It is optional until Distribution composition is enabled.
func (s *Service) SetRefundExposureConsumer(consumer paymentport.RefundExposureConsumer) error {
	if s == nil || consumer == nil || s.refundExposureConsumer != nil {
		return paymentport.ErrInvalid
	}
	s.refundExposureConsumer = consumer
	return nil
}

func (s *Service) SetReconciliationEnqueuer(enqueuer paymentport.ReconciliationEnqueuer) error {
	if s == nil || enqueuer == nil {
		return paymentport.ErrInvalid
	}
	s.reconcileJobs = enqueuer
	return nil
}

func (s *Service) SetAlipayReconciler(reconciler paymentport.AlipayReconciler) error {
	if s == nil || reconciler == nil || s.alipayReconciler != nil {
		return paymentport.ErrInvalid
	}
	s.alipayReconciler = reconciler
	return nil
}

func (s *Service) SetCheckoutProductReader(reader productport.CheckoutProductReader) error {
	if s == nil || reader == nil {
		return paymentport.ErrInvalid
	}
	s.products = reader
	return nil
}

func (s *Service) SetShopReconciler(reconciler paymentport.ShopRefundReconciler) error {
	if s == nil || reconciler == nil {
		return paymentport.ErrInvalid
	}
	s.shopReconciler = reconciler
	return nil
}

func (s *Service) SetWeChatPayReconciler(reconciler paymentport.WeChatPayReconciler) error {
	if s == nil || reconciler == nil {
		return paymentport.ErrInvalid
	}
	s.payReconciler = reconciler
	return nil
}

// SetProfitSharingIdentityReader wires the restricted Identity adapter used to
// verify an existing, scoped OpenID before a distribution receiver is accepted.
// It does not resolve, create, merge, or expose customer identity data.
func (s *Service) SetProfitSharingIdentityReader(reader identityport.PaymentIdentityReader) error {
	if s == nil || reader == nil || s.receiverIDs != nil {
		return paymentport.ErrInvalid
	}
	s.receiverIDs = reader
	return nil
}

func (s *Service) SetProfitSharingReconciler(reconciler paymentport.ProfitSharingReconciler) error {
	if s == nil || reconciler == nil || s.profitSharingReconciler != nil {
		return paymentport.ErrInvalid
	}
	s.profitSharingReconciler = reconciler
	return nil
}

// SetProfitSharingReceiverStatusObserver is wired by Composition to the
// Distribution-owned snapshot projector. It runs only inside an existing
// Payment/UoW transition; it never reads Provider state or creates a queue.
func (s *Service) SetProfitSharingReceiverStatusObserver(observer paymentport.ProfitSharingReceiverStatusObserver) error {
	if s == nil || observer == nil || s.receiverStatusObserver != nil {
		return paymentport.ErrInvalid
	}
	s.receiverStatusObserver = observer
	return nil
}

func NewService(uow platformport.UnitOfWork, store Store, orders orderport.PaymentCoordinator, sessions paymentport.SessionLifecycle, effects effectport.TransactionalAccepter, readers ...effectport.Reader) *Service {
	service := &Service{uow: uow, store: store, orders: orders, sessions: sessions, effects: effects, now: time.Now}
	if len(readers) > 0 {
		service.effectReader = readers[0]
	}
	return service
}

func (s *Service) Create(ctx context.Context, c paymentport.CreateCommand) (domain.Payment, error) {
	fromExistingOrder := c.OrderID > 0 && c.ProductID == 0 && c.ProductType == ""
	fromProduct := c.OrderID == 0 && c.ProductID > 0 && (c.ProductType == string(productport.ProductOptionStandard) || c.ProductType == string(productport.ProductOptionServicePeriod))
	if !s.ready() || (!fromExistingOrder && !fromProduct) || fromProduct && s.products == nil || c.CouponClaimID < 0 || fromExistingOrder && c.CouponClaimID != 0 || len(c.SessionToken) < 20 || len(c.SessionToken) > 100 || !validScope(c.ActorScope) || !validKey(c.IdempotencyKey) || len(c.PromotionContext) > 512 || strings.TrimSpace(c.PromotionContext) != c.PromotionContext || !validOpaqueActivityContext(c.ReferralActivityContext) || (c.Provider != "" && c.Provider != string(domain.ProviderWeChatPay) && c.Provider != string(domain.ProviderAlipay)) || (c.Provider == string(domain.ProviderAlipay) && c.Channel != domain.ChannelAlipayWap && c.Channel != domain.ChannelAlipayPage) {
		return domain.Payment{}, paymentport.ErrInvalid
	}
	// A response-lost browser recovery checkpoint is valid for precisely the
	// trusted session that created it. Check before opening the mutation UoW so
	// a renewed OAuth session cannot turn the old idempotency key into a second
	// merchant order.
	if !paymentport.MatchesCheckoutSessionBinding(c.SessionToken, c.CheckoutSessionBinding) {
		return domain.Payment{}, paymentport.ErrSessionMismatch
	}
	now := s.now().UTC()
	sessionDigest := sha256.Sum256([]byte(c.SessionToken))
	merchantDigest := sha256.Sum256([]byte("payment.checkout.v1\x00" + c.SessionToken + "\x00" + c.IdempotencyKey))
	merchantOrderNo := "v3pay_" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(merchantDigest[:16])
	// Keep exact legacy replay stable; never change an accepted external intent.
	legacyMerchantOrderNo := "v3pay_" + hex.EncodeToString(merchantDigest[:16])
	payload, _ := json.Marshal(c)
	keyDigest := sha256.Sum256([]byte(c.IdempotencyKey))
	payloadDigest := sha256.Sum256(payload)
	var result domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error {
		actor, err := s.sessions.LookupWithin(tx, c.SessionToken, now)
		if err != nil {
			return err
		}
		if fromProduct {
			switch actor.BeneficiarySelection {
			case paymentport.BeneficiarySelectionUnresolved:
				if c.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf {
					return paymentport.ErrConflict
				}
				actor, err = s.sessions.SelectPayerSelfWithin(tx, c.SessionToken, now)
				if err != nil {
					return err
				}
			case paymentport.BeneficiarySelectionPayerSelf:
				if c.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf || actor.BeneficiaryCustomerID != actor.PayerCustomerID {
					return paymentport.ErrConflict
				}
			case paymentport.BeneficiarySelectionAdminAssisted:
				// This state is set only by the trusted server-side session issuer.
				// A public browser cannot select or replace its prebound recipient.
				if c.BeneficiarySelection != "" || actor.BeneficiaryCustomerID < 1 {
					return paymentport.ErrConflict
				}
			default:
				// Legacy rows retain their original value but are never re-labelled as
				// a fresh user confirmation by the public purchase path.
				return paymentport.ErrConflict
			}
		}
		channel := actor.Channel
		provider := orderdomain.ProviderWeChatPay
		if c.Provider == string(domain.ProviderAlipay) {
			provider = orderdomain.ProviderAlipay
			channel = c.Channel
		}
		if channel == "" {
			channel = domain.ChannelMiniProgram
		}
		if channel != domain.ChannelMiniProgram && channel != domain.ChannelH5Official && channel != domain.ChannelAlipayWap && channel != domain.ChannelAlipayPage {
			return paymentport.ErrConflict
		}
		replay, found, err := s.store.ReplayPayment(tx, keyDigest, payloadDigest, c.ActorScope)
		if err != nil {
			return err
		}
		if found {
			if (fromExistingOrder && replay.OrderID != c.OrderID) || (fromProduct && replay.MerchantOrderNo != merchantOrderNo && replay.MerchantOrderNo != legacyMerchantOrderNo) || replay.PayerIdentityID != actor.PayerIdentityID || replay.PayerCustomerID != actor.PayerCustomerID || replay.BeneficiaryCustomerID != actor.BeneficiaryCustomerID || replay.Channel != channel {
				return paymentport.ErrConflict
			}
			result = replay
			return nil
		}
		if !selectedBeneficiary(actor) {
			// Pre-migration sessions retain their recipient value only to authorize
			// an exact payment replay above. They cannot create a new command or
			// silently turn an inferred recipient into a fresh confirmation.
			return paymentport.ErrConflict
		}
		var order orderdomain.Snapshot
		if fromExistingOrder {
			order, err = s.orders.ReservePaymentWithin(tx, c.OrderID)
			if err == nil {
				for _, item := range order.Items {
					if item.ProductID == nil {
						return paymentport.ErrConflict
					}
					state, checkErr := s.standardPurchaseWithin(tx, actor.BeneficiaryCustomerID, *item.ProductID, item.ProductCode, order.ID, true)
					if checkErr != nil {
						return checkErr
					}
					if !state.CanPurchase {
						return purchaseBlocked(state)
					}
				}
			}
		} else {
			product, productErr := s.products.ReadCheckoutProductWithin(tx, productport.ProductOptionType(c.ProductType), productport.ID(c.ProductID))
			if productErr != nil || int64(product.ID) != c.ProductID || product.ProductType != productport.ProductOptionType(c.ProductType) || product.PriceMinor < 1 || product.Currency != "CNY" || product.Version < 1 || product.ProductType == productport.ProductOptionServicePeriod && product.ServicePeriodDurationDays < 1 {
				if productErr != nil {
					return productErr
				}
				return paymentport.ErrConflict
			}
			level := product.ContactCollectionLevel
			if level == "" {
				if product.RequireMobile {
					level = "mobile"
				} else {
					level = "none"
				}
			}
			if channel != domain.ChannelH5Official && channel != domain.ChannelAlipayWap && channel != domain.ChannelAlipayPage {
				level = "none"
			}
			if channel == domain.ChannelH5Official || channel == domain.ChannelAlipayWap || channel == domain.ChannelAlipayPage {
				if level == "shipping_address" && !validShippingAddress(c.ShippingAddress) {
					return paymentport.ErrConflict
				}
				if (level != "none") != validMainlandMobileE164(c.MobileE164) {
					return paymentport.ErrConflict
				}
			} else if c.MobileE164 != "" || c.ContactCollectionLevel != "" && c.ContactCollectionLevel != "none" || !emptyShippingAddress(c.ShippingAddress) {
				return paymentport.ErrConflict
			}
			c.ContactCollectionLevel = level
			if product.ProductType == productport.ProductOptionStandard {
				state, checkErr := s.standardPurchaseWithin(tx, actor.BeneficiaryCustomerID, int64(product.ID), product.Code, 0, true)
				if checkErr != nil {
					return checkErr
				}
				if !state.CanPurchase {
					return purchaseBlocked(state)
				}
			}
			order, err = s.orders.CreatePaymentOrderWithin(tx, orderport.PaymentOrderCommand{
				Provider: provider, MerchantOrderNo: merchantOrderNo,
				PayerCustomerID: actor.PayerCustomerID, BeneficiaryCustomerID: actor.BeneficiaryCustomerID,
				ProductID: int64(product.ID), CouponClaimID: c.CouponClaimID, ProductCode: product.Code, ProductName: product.Name,
				ProductVersion: product.Version, ProductType: orderCheckoutProductType(product.ProductType), ServicePeriodDurationDays: product.ServicePeriodDurationDays, UnitAmountMinor: product.PriceMinor, Currency: product.Currency,
				PostPurchaseAction: product.PostPurchaseAction,
				MobileE164:         c.MobileE164, ContactCollectionLevel: c.ContactCollectionLevel, ShippingAddress: orderport.ShippingAddress{RecipientName: c.ShippingAddress.RecipientName, ProvinceCode: c.ShippingAddress.ProvinceCode, ProvinceName: c.ShippingAddress.ProvinceName, CityCode: c.ShippingAddress.CityCode, CityName: c.ShippingAddress.CityName, DistrictCode: c.ShippingAddress.DistrictCode, DistrictName: c.ShippingAddress.DistrictName, DetailAddress: c.ShippingAddress.DetailAddress}, PromotionContext: c.PromotionContext, ReferralActivityContext: c.ReferralActivityContext,
				ActorScope: "payment-session:" + hex.EncodeToString(sessionDigest[:]), IdempotencyKey: c.IdempotencyKey,
			})
		}
		if err != nil {
			return err
		}
		if order.PayerCustomerID == nil || order.BeneficiaryCustomerID == nil || int64(*order.PayerCustomerID) != actor.PayerCustomerID || int64(*order.BeneficiaryCustomerID) != actor.BeneficiaryCustomerID {
			return paymentport.ErrConflict
		}
		// Distribution is the only coordinator that may decide whether this
		// checkout needs a profit-sharing reservation. Its result was frozen by
		// Order in this same Unit of Work. Never accept a caller supplied boolean:
		// that would let an HTTP request mark an invalid, self, direct, or zero
		// commission sale as shareable.
		payment, err := domain.NewPaymentWithProfitSharing(order, actor.PayerIdentityID, order.ProfitSharingRequired, now, channel)
		if err != nil {
			return err
		}
		var created bool
		payment, created, err = s.store.CreatePayment(tx, payment, keyDigest, payloadDigest, c.ActorScope)
		if err != nil {
			return err
		}
		if created {
			consumed, consumeErr := s.sessions.ConsumeWithin(tx, c.SessionToken, now)
			if consumeErr != nil {
				return consumeErr
			}
			if consumed != actor {
				return paymentport.ErrConflict
			}
		}
		kind := effectport.KindWeChatPayPrepay
		if payment.Provider == domain.ProviderAlipay {
			if payment.Channel == domain.ChannelAlipayWap {
				kind = effectport.KindAlipayWapPay
			} else {
				kind = effectport.KindAlipayPagePay
			}
		}
		if payment.Provider == domain.ProviderWeChatShop {
			kind = effectport.KindWeChatShopRefund
			return paymentport.ErrConflict
		}
		intent := effectport.PaymentV1Intent{Kind: kind, ReceiptKey: effectport.Hash("payment.create.v1", c.IdempotencyKey), SourceRefDigest: effectport.Hash("payment", strconv.FormatInt(payment.ID, 10)), TargetRefDigest: effectport.Hash("payment.identity", strconv.FormatInt(payment.PayerIdentityID, 10)), PayloadDigest: effectport.Hash("payment.payload", payment.MerchantOrderNo, strconv.FormatInt(payment.AmountMinor, 10), payment.Currency), PolicyVersionHash: effectport.Hash("payment.policy", "v1")}
		accept, ok := intent.AcceptCommand()
		if !ok {
			return paymentport.ErrInvalid
		}
		projection, _, err := s.effects.AcceptAndQueueWithin(tx, accept)
		if err != nil {
			return err
		}
		if payment.EffectID == "" {
			payment, err = payment.BindEffect(payment.Version, projection.ID, now)
			if err != nil {
				return err
			}
			snapshot := map[string]any{"payment_id": payment.ID, "order_id": payment.OrderID, "amount_minor": payment.AmountMinor, "currency": payment.Currency}
			if payment.Provider == domain.ProviderAlipay {
				subject, ok := alipaySubjectFromOrder(order)
				if !ok {
					return paymentport.ErrConflict
				}
				snapshot["subject"] = subject
			}
			payment, err = s.store.BindPaymentEffect(tx, payment, intent, snapshot)
			if err != nil {
				return err
			}
		} else if payment.EffectID != projection.ID {
			return paymentport.ErrConflict
		}
		result = payment
		return nil
	})
	if err != nil {
		return domain.Payment{}, classify(err)
	}
	return result, nil
}

func alipaySubjectFromOrder(order orderdomain.Snapshot) (string, bool) {
	if len(order.Items) != 1 {
		return "", false
	}
	item := order.Items[0]
	subject := strings.TrimSpace(item.ProductName)
	if subject == "" {
		subject = strings.TrimSpace(item.ProductCode)
	}
	if subject == "" || strings.IndexFunc(subject, unicode.IsControl) >= 0 {
		return "", false
	}
	runes := []rune(subject)
	if len(runes) > paymentport.AlipayMaxSubjectRunes {
		subject = strings.TrimSpace(string(runes[:paymentport.AlipayMaxSubjectRunes]))
	}
	return subject, subject != ""
}

func validOpaqueActivityContext(value string) bool {
	return len(value) <= 512 && value == strings.TrimSpace(value) && strings.IndexFunc(value, func(r rune) bool { return r < 0x21 || r > 0x7e }) < 0
}

func orderCheckoutProductType(kind productport.ProductOptionType) string {
	if kind == productport.ProductOptionServicePeriod {
		return "service_period"
	}
	return "standard_product"
}

func (s *Service) RequestRefund(ctx context.Context, c paymentport.RefundCommand) (domain.Refund, error) {
	if !s.ready() || c.PaymentID < 1 || c.AmountMinor < 1 || !validScope(c.ActorScope) || !validKey(c.IdempotencyKey) {
		return domain.Refund{}, paymentport.ErrInvalid
	}
	payload, _ := json.Marshal(c)
	keyDigest := sha256.Sum256([]byte(c.IdempotencyKey))
	payloadDigest := sha256.Sum256(payload)
	var result domain.Refund
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var found bool
		var replayErr error
		result, found, replayErr = s.store.ReplayRefund(tx, keyDigest, payloadDigest, c.ActorScope)
		if replayErr != nil || found {
			return replayErr
		}
		result = domain.Refund{}
		return nil
	})
	if err != nil {
		return domain.Refund{}, classify(err)
	}
	if result.ID > 0 {
		return result, nil
	}
	if c.ProviderOrderID != "" {
		if s.shopReconciler == nil || !validShopRefundCommand(c) {
			return domain.Refund{}, paymentport.ErrInvalid
		}
		var candidate domain.Payment
		if err := s.uow.Within(ctx, func(tx context.Context) error {
			var readErr error
			candidate, readErr = s.store.GetPayment(tx, c.PaymentID, false)
			return readErr
		}); err != nil {
			return domain.Refund{}, classify(err)
		}
		if candidate.Historical || candidate.Provider != domain.ProviderWeChatShop || candidate.MerchantOrderNo != c.ProviderOrderID || candidate.Status != domain.StatusPaid {
			return domain.Refund{}, paymentport.ErrConflict
		}
		if err := s.shopReconciler.ValidateRefundMaterial(ctx, paymentport.ShopRefundMaterial{AmountMinor: c.AmountMinor, ProviderOrderID: c.ProviderOrderID, ProductID: c.ProductID, SKUID: c.SKUID, RefundCount: c.RefundCount, ReasonCode: c.ReasonCode, Currency: "CNY"}); err != nil {
			return domain.Refund{}, paymentport.ErrUnavailable
		}
	}
	now := s.now().UTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		payment, err := s.store.GetPayment(tx, c.PaymentID, true)
		if err != nil {
			return err
		}
		if payment.Provider == domain.ProviderWeChatShop && !validShopRefundCommand(c) {
			return paymentport.ErrInvalid
		}
		replay, found, err := s.store.ReplayRefund(tx, keyDigest, payloadDigest, c.ActorScope)
		if err != nil {
			return err
		}
		if found {
			if replay.PaymentID != payment.ID || replay.Provider != payment.Provider {
				return paymentport.ErrConflict
			}
			result = replay
			return nil
		}
		// The Payment row is locked above. For a live WeChat Pay Payment, do
		// not allow another idempotency key to create a second in-flight refund
		// while the first still awaits an external outcome. Original-key replay
		// remains ahead of this check; a terminal refund permits an explicit
		// later partial-refund request. WeChat Shop retains its SKU contract.
		if payment.Provider == domain.ProviderWeChatPay || payment.Provider == domain.ProviderAlipay {
			pending, pendingErr := s.store.HasNonTerminalRefund(tx, payment.ID)
			if pendingErr != nil {
				return pendingErr
			}
			if pending {
				return paymentport.ErrConflict
			}
		}
		reserved, err := s.store.ReservedRefundMinor(tx, payment.ID)
		if err != nil {
			return err
		}
		profitSharingReserved, err := s.store.ReservedProfitSharingMinor(tx, payment.ID)
		if err != nil {
			return err
		}
		// The payment row is the shared coordinator lock. Once a split reserve
		// exists, an ensuing refund may not consume those retained funds. This
		// is deliberately checked before creating any refund effect.
		if reserved < 0 || profitSharingReserved < 0 || c.AmountMinor > payment.AmountMinor-reserved-profitSharingReserved {
			return paymentport.ErrConflict
		}
		refund, err := domain.NewRefund(payment, c.RefundNo, c.AmountMinor, c.Reason, now)
		if err != nil {
			return err
		}
		refund, _, err = s.store.CreateRefund(tx, refund, keyDigest, payloadDigest, c.ActorScope)
		if err != nil {
			return err
		}
		kind := effectport.KindWeChatPayRefund
		if refund.Provider == domain.ProviderWeChatShop {
			kind = effectport.KindWeChatShopRefund
		} else if refund.Provider == domain.ProviderAlipay {
			kind = effectport.KindAlipayRefund
		}
		intent := effectport.PaymentV1Intent{Kind: kind, ReceiptKey: effectport.Hash("payment.refund.v1", c.IdempotencyKey), SourceRefDigest: effectport.Hash("payment.refund", strconv.FormatInt(refund.ID, 10)), TargetRefDigest: effectport.Hash("payment", strconv.FormatInt(payment.ID, 10)), PayloadDigest: effectport.Hash("payment.refund.payload", refund.RefundNo, strconv.FormatInt(refund.AmountMinor, 10), refund.Reason, c.ProviderOrderID, c.ProductID, c.SKUID, strconv.FormatInt(c.RefundCount, 10), c.ReasonCode), PolicyVersionHash: effectport.Hash("payment.refund.policy", "v1")}
		accept, ok := intent.AcceptCommand()
		if !ok {
			return paymentport.ErrInvalid
		}
		projection, _, err := s.effects.AcceptAndQueueWithin(tx, accept)
		if err != nil {
			return err
		}
		if refund.EffectID == "" {
			refund, err = refund.BindEffect(refund.Version, projection.ID, now)
			if err != nil {
				return err
			}
			refund, err = s.store.BindRefundEffect(tx, refund, intent, map[string]any{"refund_id": refund.ID, "payment_id": payment.ID, "amount_minor": refund.AmountMinor, "currency": payment.Currency, "provider_order_id": c.ProviderOrderID, "product_id": c.ProductID, "sku_id": c.SKUID, "refund_count": c.RefundCount, "reason_code": c.ReasonCode})
			if err != nil {
				return err
			}
		} else if refund.EffectID != projection.ID {
			return paymentport.ErrConflict
		}
		if err = s.notifyRefundExposureWithin(tx, payment.OrderID, refund.ID, paymentport.RefundExposureOpened, now, "refund-open:"+c.IdempotencyKey); err != nil {
			return err
		}
		result = refund
		return nil
	})
	if err != nil {
		return domain.Refund{}, classify(err)
	}
	return result, nil
}

func (s *Service) notifyRefundExposureWithin(ctx context.Context, orderID, refundID int64, state paymentport.RefundExposureState, at time.Time, receiptKey string) error {
	if s.refundExposureConsumer == nil {
		return nil
	}
	return s.refundExposureConsumer.ConsumeRefundExposureWithin(ctx, paymentport.RefundExposureEvent{OrderID: orderID, RefundID: refundID, State: state, OccurredAt: at.UTC(), ReceiptKey: receiptKey})
}

func (s *Service) GetPayment(ctx context.Context, id int64) (domain.Payment, error) {
	var out domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.store.GetPayment(tx, id, false); return e })
	return out, classify(err)
}

func (s *Service) GetCheckout(ctx context.Context, provider domain.Provider, merchantOrderNo, sessionToken string) (paymentport.Handoff, error) {
	if s == nil || s.uow == nil || s.store == nil || s.sessions == nil || (provider != domain.ProviderWeChatPay && provider != domain.ProviderAlipay) || !validScope(merchantOrderNo) || len(sessionToken) < 20 || len(sessionToken) > 100 {
		return paymentport.Handoff{}, paymentport.ErrInvalid
	}
	now := s.now().UTC()
	var out paymentport.Handoff
	var prepayEffectID string
	var prepayKind effectport.Kind
	err := s.uow.Within(ctx, func(tx context.Context) error {
		actor, err := s.sessions.LookupWithin(tx, sessionToken, now)
		if err != nil {
			return err
		}
		payment, err := s.store.GetPaymentByMerchantProvider(tx, provider, merchantOrderNo, false)
		if err != nil {
			return err
		}
		authorized := checkoutReadAuthorized(payment, actor)
		if !authorized && s.lineage != nil && payment.PayerIdentityID == actor.PayerIdentityID && checkoutSessionChannelMatches(payment, actor) {
			roots, readErr := s.lineage.CanonicalLineage(tx, customerdomain.CustomerID(actor.PayerCustomerID))
			if readErr != nil {
				return readErr
			}
			for _, root := range roots {
				if int64(root) == payment.PayerCustomerID {
					original := payment
					original.PayerCustomerID = actor.PayerCustomerID
					if original.BeneficiaryCustomerID == payment.PayerCustomerID {
						original.BeneficiaryCustomerID = actor.PayerCustomerID
					}
					authorized = checkoutReadAuthorized(original, actor)
					break
				}
			}
		}
		if !authorized {
			return paymentport.ErrConflict
		}
		out = paymentport.Handoff{PaymentID: payment.ID, OrderID: payment.OrderID, MerchantOrder: payment.MerchantOrderNo, Provider: payment.Provider, Channel: payment.Channel, Status: payment.Status, AmountMinor: payment.AmountMinor, Currency: payment.Currency}
		// A terminal outcome is an immutable Payment fact. It remains readable to
		// the original trusted payer after the short-lived JSAPI handoff expires;
		// handoff material is neither needed nor safe to revive at this point.
		if payment.Status == domain.StatusPaid || payment.Status == domain.StatusFailed || payment.Status == domain.StatusCancelled {
			return nil
		}
		if payment.Status == domain.StatusAwaitingPrepay {
			if local, ok := s.store.(checkoutAbandonmentStore); ok {
				out.CheckoutAbandoned, err = local.CheckoutAbandoned(tx, payment.ID)
				if err != nil {
					return err
				}
			}
			if review, ok := s.store.(checkoutRestartStore); ok {
				out.CheckoutRestartAllowed, err = review.CheckoutRestartAllowed(tx, payment.ID)
				if err != nil {
					return err
				}
			}
			prepayEffectID = payment.EffectID
			switch payment.Provider {
			case domain.ProviderWeChatPay:
				prepayKind = effectport.KindWeChatPayPrepay
			case domain.ProviderAlipay:
				if payment.Channel == domain.ChannelAlipayWap {
					prepayKind = effectport.KindAlipayWapPay
				} else if payment.Channel == domain.ChannelAlipayPage {
					prepayKind = effectport.KindAlipayPagePay
				} else {
					return paymentport.ErrConflict
				}
			default:
				return paymentport.ErrConflict
			}
			return nil
		}
		handoff, err := s.store.GetHandoff(tx, payment.ID)
		if errors.Is(err, paymentport.ErrNotFound) && payment.Status != domain.StatusAwaitingPayment {
			return nil
		}
		if err != nil {
			return err
		}
		if !handoff.ExpiresAt.After(now) {
			return paymentport.ErrConflict
		}
		out.Payload, out.ExpiresAt = handoff.Payload, handoff.ExpiresAt
		return nil
	})
	if err != nil {
		return out, classify(err)
	}
	// Read through the effect owner after the authorization transaction. Expose
	// only its state; never return effect identifiers or provider response data.
	if prepayEffectID != "" && s.effectReader != nil {
		projection, readErr := s.effectReader.Get(ctx, prepayEffectID)
		if readErr != nil || projection.ID != prepayEffectID || projection.Owner != effectport.OwnerPayment || projection.Kind != prepayKind {
			return paymentport.Handoff{}, paymentport.ErrUnavailable
		}
		out.PrepayState = projection.State
	}
	return out, nil
}

// CheckoutSessionBinding proves only that the caller still holds a valid
// trusted Payment session. It exposes an opaque marker, never an identity or
// session cookie value, for the public Host's browser recovery checkpoint.
func (s *Service) CheckoutSessionBinding(ctx context.Context, sessionToken string) (string, error) {
	if s == nil || s.uow == nil || s.sessions == nil || len(sessionToken) < 20 || len(sessionToken) > 100 {
		return "", paymentport.ErrInvalid
	}
	binding := paymentport.CheckoutSessionBinding(sessionToken)
	if binding == "" {
		return "", paymentport.ErrInvalid
	}
	now := s.now().UTC()
	err := s.uow.Within(ctx, func(tx context.Context) error {
		_, err := s.sessions.LookupWithin(tx, sessionToken, now)
		return err
	})
	if err != nil {
		return "", classify(err)
	}
	return binding, nil
}
func (s *Service) GetRefund(ctx context.Context, id int64) (domain.Refund, error) {
	var out domain.Refund
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.store.GetRefund(tx, id, false); return e })
	return out, classify(err)
}
func (s *Service) FindPayment(ctx context.Context, provider domain.Provider, merchantOrderNo string) (domain.Payment, error) {
	if s == nil || s.uow == nil || s.store == nil || (provider != domain.ProviderWeChatPay && provider != domain.ProviderWeChatShop) || !validScope(merchantOrderNo) {
		return domain.Payment{}, paymentport.ErrInvalid
	}
	var out domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		out, inner = s.store.GetPaymentByMerchantProvider(tx, provider, merchantOrderNo, false)
		return inner
	})
	return out, classify(err)
}
func (s *Service) ListRefunds(ctx context.Context, limit, offset int32) ([]paymentport.RefundProjection, int64, error) {
	if s == nil || s.uow == nil || s.store == nil || limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		return nil, 0, paymentport.ErrInvalid
	}
	var out []paymentport.RefundProjection
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		out, total, inner = s.store.ListRefunds(tx, limit, offset)
		return inner
	})
	return out, total, classify(err)
}

func (s *Service) ListRefundsForPayment(ctx context.Context, provider domain.Provider, merchantOrderNo string, limit, offset int32) ([]paymentport.RefundProjection, int64, error) {
	if s == nil || s.uow == nil || s.store == nil || (provider != domain.ProviderWeChatPay && provider != domain.ProviderWeChatShop) || !validScope(merchantOrderNo) || limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		return nil, 0, paymentport.ErrInvalid
	}
	var out []paymentport.RefundProjection
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		out, total, inner = s.store.ListRefundsForPayment(tx, provider, merchantOrderNo, limit, offset)
		return inner
	})
	return out, total, classify(err)
}

// FindRefundRecoveryReceipt resolves only an existing refund receipt for the
// same Payment, operator scope, and original idempotency key. It never
// replays a command or exposes the command payload/digests to an HTTP caller.
func (s *Service) FindRefundRecoveryReceipt(ctx context.Context, provider domain.Provider, merchantOrderNo, actorScope, idempotencyKey string) (domain.Refund, bool, error) {
	if s == nil || s.uow == nil || s.store == nil || (provider != domain.ProviderWeChatPay && provider != domain.ProviderWeChatShop) || !validScope(merchantOrderNo) || !validScope(actorScope) || !validKey(idempotencyKey) {
		return domain.Refund{}, false, paymentport.ErrInvalid
	}
	keyDigest := sha256.Sum256([]byte(idempotencyKey))
	var out domain.Refund
	var found bool
	err := s.uow.Within(ctx, func(tx context.Context) error {
		payment, err := s.store.GetPaymentByMerchantProvider(tx, provider, merchantOrderNo, false)
		if err != nil {
			return err
		}
		out, found, err = s.store.FindRefundByIdempotencyKey(tx, keyDigest, actorScope)
		if err != nil || !found {
			return err
		}
		// A key belonging to another Payment must look absent to this scoped
		// recovery read; it must never release a different order's lock.
		if out.PaymentID != payment.ID || out.Provider != provider {
			out, found = domain.Refund{}, false
		}
		return nil
	})
	if err != nil {
		return domain.Refund{}, false, classify(err)
	}
	return out, found, nil
}

func (s *Service) ListOrderEffects(ctx context.Context, provider domain.Provider, merchantOrderNo string) ([]paymentport.EffectProjection, error) {
	if s == nil || s.uow == nil || s.store == nil || s.effectReader == nil ||
		(provider != domain.ProviderWeChatPay && provider != domain.ProviderWeChatShop) || !validScope(merchantOrderNo) {
		return nil, paymentport.ErrInvalid
	}
	var bindings []paymentport.EffectProjection
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		bindings, inner = s.store.ListEffectBindings(tx, provider, merchantOrderNo)
		return inner
	})
	if err != nil {
		return nil, classify(err)
	}
	for index := range bindings {
		projection, readErr := s.effectReader.Get(ctx, bindings[index].EffectID)
		if readErr != nil || projection.Owner != effectport.OwnerPayment || projection.Kind != bindings[index].Kind {
			return nil, paymentport.ErrConflict
		}
		bindings[index].State = projection.State
		bindings[index].AttemptCount = projection.AttemptCount
		bindings[index].UpdatedAt = projection.UpdatedAt
	}
	return bindings, nil
}

func (s *Service) ReconcileShopRefund(ctx context.Context, refundID int64) (domain.Refund, error) {
	if s == nil || s.uow == nil || s.store == nil || s.shopReconciler == nil || refundID < 1 {
		return domain.Refund{}, paymentport.ErrInvalid
	}
	var current domain.Refund
	var material paymentport.ShopRefundMaterial
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		current, inner = s.store.GetRefund(tx, refundID, false)
		if inner != nil {
			return inner
		}
		if current.Provider != domain.ProviderWeChatShop || current.ProviderRefundReference == "" || (current.Status != domain.RefundEffectAccepted && current.Status != domain.RefundOutcomeUnknown && current.Status != domain.RefundCompleted) {
			return paymentport.ErrConflict
		}
		material, inner = s.store.GetShopRefundMaterial(tx, current.ID)
		return inner
	})
	if err != nil {
		return domain.Refund{}, classify(err)
	}
	if current.Status == domain.RefundCompleted {
		return current, nil
	}
	query, err := s.shopReconciler.QueryRefund(ctx, current.ProviderRefundReference)
	if err != nil {
		return domain.Refund{}, paymentport.ErrUnavailable
	}
	if query.AfterSaleID != current.ProviderRefundReference || query.ProviderOrderID != material.ProviderOrderID || query.ProductID != material.ProductID || query.SKUID != material.SKUID || query.Count != material.RefundCount || query.AmountMinor != current.AmountMinor || query.Currency != "CNY" || !effectport.ValidDigest(query.EvidenceDigest) || !effectport.ValidDigest(query.ProviderRefundDigest) || query.OccurredAt.IsZero() {
		return domain.Refund{}, paymentport.ErrConflict
	}
	outcome := "pending"
	if query.Status == "MERCHANT_REFUND_SUCCESS" {
		outcome = "refunded"
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := s.store.GetRefund(tx, refundID, true)
		if inner != nil {
			return inner
		}
		if locked.Status == domain.RefundCompleted {
			current = locked
			return nil
		}
		if locked.ProviderRefundReference != query.AfterSaleID || (locked.Status != domain.RefundEffectAccepted && locked.Status != domain.RefundOutcomeUnknown) {
			return paymentport.ErrConflict
		}
		_, inner = s.store.RecordReconciliation(tx, locked.ID, query.EvidenceDigest, outcome, s.now().UTC())
		if inner != nil || outcome != "refunded" {
			current = locked
			return inner
		}
		locked, inner = locked.Complete(locked.Version, domain.RefundCompleted, query.OccurredAt)
		if inner != nil {
			return inner
		}
		receiptKey := "reconcile:" + string(query.EvidenceDigest)
		current, inner = s.store.UpdateRefundSettlement(tx, locked, string(query.ProviderRefundDigest), receiptKey)
		if inner != nil {
			return inner
		}
		payment, inner := s.store.GetPayment(tx, current.PaymentID, false)
		if inner != nil {
			return inner
		}
		_, inner = s.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: payment.OrderID, RefundedDelta: current.AmountMinor, OccurredAt: query.OccurredAt, ReceiptKey: receiptKey})
		return inner
	})
	return current, classify(err)
}

func (s *Service) ApplyVerifiedShopCallback(ctx context.Context, callback paymentport.ShopRefundCallback) error {
	if s == nil || s.uow == nil || s.store == nil || s.shopReconciler == nil || s.reconcileJobs == nil || callback.AfterSaleID == "" || callback.ProviderOrderID == "" || callback.Status == "" || callback.EventDigest == ([32]byte{}) || callback.PayloadDigest == ([32]byte{}) || callback.OccurredAt.IsZero() {
		return paymentport.ErrInvalid
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		refund, inner := s.store.GetRefundByProviderReference(tx, domain.ProviderWeChatShop, callback.AfterSaleID, true)
		if inner != nil {
			return inner
		}
		material, inner := s.store.GetShopRefundMaterial(tx, refund.ID)
		if inner != nil || material.ProviderOrderID != callback.ProviderOrderID {
			if inner != nil {
				return inner
			}
			return paymentport.ErrConflict
		}
		replay, inner := s.store.ClaimCallback(tx, "wechat_shop", callback.EventDigest, callback.PayloadDigest, "refund", "query_required", refund.ID)
		if inner != nil || replay {
			return inner
		}
		return s.reconcileJobs.EnqueueWithin(tx, paymentport.ReconciliationTarget{Provider: domain.ProviderWeChatShop, RefundID: refund.ID})
	})
	return classify(err)
}

func (s *Service) ReconcileWeChatPayPayment(ctx context.Context, paymentID int64) (domain.Payment, error) {
	current, query, outcome, err := s.queriedWeChatPayPayment(ctx, paymentID)
	if err != nil {
		return domain.Payment{}, err
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := s.store.GetPayment(tx, paymentID, true)
		if inner != nil {
			return inner
		}
		if locked.Status == domain.StatusPaid {
			current = locked
			// A historical native payment missing its confirmation time is the
			// narrow recovery case.  Do not even persist a reconciliation read
			// until the signed Provider SUCCESS fact and immutable Order paid
			// evidence agree exactly; unknown or mismatched queries change
			// nothing.
			if locked.PaidConfirmedAt == nil {
				if outcome != "paid" {
					return nil
				}
				verifier, ok := s.orders.(orderport.PaymentConfirmationEvidenceVerifier)
				if !ok {
					return paymentport.ErrUnavailable
				}
				if _, inner = verifier.VerifyPaymentConfirmationEvidenceWithin(tx, orderport.PaymentConfirmationEvidence{OrderID: locked.OrderID, ProviderTransactionNo: query.TransactionReference, OccurredAt: query.OccurredAt}); inner != nil {
					return inner
				}
				// Pre-0161 native rows may have neither field.  A verified query may
				// fill those missing immutable transaction facts, but it must never
				// replace a prior non-empty value with a different Provider result.
				if locked.ProviderTransactionReference != "" && locked.ProviderTransactionReference != query.TransactionReference || locked.ProviderTransactionDigest != "" && locked.ProviderTransactionDigest != string(query.TransactionDigest) {
					return paymentport.ErrConflict
				}
				if _, inner = s.store.RecordPaymentReconciliation(tx, locked.ID, query.EvidenceDigest, outcome, s.now().UTC()); inner != nil {
					return inner
				}
				reconciledAt := s.now().UTC()
				locked, inner = locked.RestorePaidConfirmation(locked.Version, query.OccurredAt, reconciledAt)
				if inner != nil {
					return inner
				}
				locked.ProviderTransactionReference = query.TransactionReference
				locked.ProviderTransactionDigest = string(query.TransactionDigest)
				restorer, ok := s.store.(paidConfirmationRestorer)
				if !ok {
					return paymentport.ErrUnavailable
				}
				current, inner = restorer.RestorePaidConfirmation(tx, locked, string(query.TransactionDigest), "reconcile:"+string(query.EvidenceDigest))
				return inner
			}
			if _, inner = s.store.RecordPaymentReconciliation(tx, locked.ID, query.EvidenceDigest, outcome, s.now().UTC()); inner != nil {
				return inner
			}
			if outcome != "paid" {
				return nil
			}
			return nil
		}
		_, inner = s.store.RecordPaymentReconciliation(tx, locked.ID, query.EvidenceDigest, outcome, s.now().UTC())
		if inner != nil || outcome == "pending" || locked.Status == domain.StatusFailed {
			current = locked
			return inner
		}
		next := domain.StatusPaid
		if outcome == "final_failed" {
			next = domain.StatusFailed
		}
		locked, inner = locked.Settle(locked.Version, next, query.OccurredAt)
		if inner != nil {
			return inner
		}
		locked.ProviderTransactionReference = query.TransactionReference
		receiptKey := "reconcile:" + string(query.EvidenceDigest)
		current, inner = s.store.UpdatePaymentSettlement(tx, locked, string(query.TransactionDigest), receiptKey)
		if inner != nil {
			return inner
		}
		_, inner = s.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: current.OrderID, Failed: outcome == "final_failed", ProviderTransactionNo: query.TransactionReference, OccurredAt: query.OccurredAt, ReceiptKey: receiptKey})
		return inner
	})
	return current, classify(err)
}

// PreviewReconcileWeChatPayPayment repeats the same signed Provider and Order
// fact validation as the paid-confirmation repair without writing a receipt,
// audit, Payment, Order, or external effect.  It is intentionally limited to
// the already-paid-but-unconfirmed native Payment case.
func (s *Service) PreviewReconcileWeChatPayPayment(ctx context.Context, paymentID int64) (paymentport.PaymentReconciliationPreview, error) {
	current, query, outcome, err := s.queriedWeChatPayPayment(ctx, paymentID)
	if err != nil {
		return paymentport.PaymentReconciliationPreview{}, err
	}
	preview := paymentport.PaymentReconciliationPreview{PaymentID: current.ID}
	if current.Status != domain.StatusPaid {
		preview.Reason = "payment_not_paid"
		return preview, nil
	}
	if current.PaidConfirmedAt != nil {
		preview.Reason = "already_confirmed"
		return preview, nil
	}
	if outcome != "paid" {
		preview.Reason = "provider_not_success"
		return preview, nil
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := s.store.GetPayment(tx, paymentID, false)
		if inner != nil {
			return inner
		}
		if locked.Status != domain.StatusPaid || locked.PaidConfirmedAt != nil {
			return paymentport.ErrConflict
		}
		// Keep preview semantics identical to the mutating repair: a previously
		// stored immutable transaction fact can only agree with the signed query,
		// never be silently replaced after an operator sees a dry-run result.
		if locked.ProviderTransactionReference != "" && locked.ProviderTransactionReference != query.TransactionReference || locked.ProviderTransactionDigest != "" && locked.ProviderTransactionDigest != string(query.TransactionDigest) {
			return paymentport.ErrConflict
		}
		verifier, ok := s.orders.(orderport.PaymentConfirmationEvidenceVerifier)
		if !ok {
			return paymentport.ErrUnavailable
		}
		_, inner = verifier.VerifyPaymentConfirmationEvidenceWithin(tx, orderport.PaymentConfirmationEvidence{OrderID: locked.OrderID, ProviderTransactionNo: query.TransactionReference, OccurredAt: query.OccurredAt})
		return inner
	})
	if err != nil {
		if errors.Is(classify(err), paymentport.ErrConflict) {
			preview.Reason = "payment_evidence_mismatch"
			return preview, nil
		}
		return paymentport.PaymentReconciliationPreview{}, classify(err)
	}
	preview.WouldRestorePaidConfirmation = true
	preview.Reason = "payment_confirmation_missing"
	return preview, nil
}

func (s *Service) queriedWeChatPayPayment(ctx context.Context, paymentID int64) (domain.Payment, paymentport.WeChatPayPaymentQuery, string, error) {
	if s == nil || s.uow == nil || s.store == nil || s.payReconciler == nil || paymentID < 1 {
		return domain.Payment{}, paymentport.WeChatPayPaymentQuery{}, "", paymentport.ErrInvalid
	}
	var current domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		current, inner = s.store.GetPayment(tx, paymentID, false)
		if inner != nil {
			return inner
		}
		if current.Historical || current.Provider != domain.ProviderWeChatPay || (current.Status != domain.StatusAwaitingPayment && current.Status != domain.StatusAwaitingPrepay && current.Status != domain.StatusPaid) {
			return paymentport.ErrConflict
		}
		return nil
	})
	if err != nil {
		return domain.Payment{}, paymentport.WeChatPayPaymentQuery{}, "", classify(err)
	}
	query, err := s.payReconciler.QueryPayment(ctx, current.MerchantOrderNo)
	if err != nil {
		return domain.Payment{}, paymentport.WeChatPayPaymentQuery{}, "", paymentport.ErrUnavailable
	}
	query.OccurredAt = canonicalProviderOccurredAt(query.OccurredAt)
	if query.MerchantOrderNo != current.MerchantOrderNo || query.AmountMinor != current.AmountMinor || query.Currency != current.Currency || !s.callbackAppIDMatches(current, query.AppID) || !effectport.ValidDigest(query.EvidenceDigest) || query.OccurredAt.IsZero() {
		return domain.Payment{}, paymentport.WeChatPayPaymentQuery{}, "", paymentport.ErrConflict
	}
	outcome := "pending"
	switch query.Status {
	case "SUCCESS":
		if !effectport.ValidDigest(query.TransactionDigest) || !validProviderTransactionReference(query.TransactionReference) || query.TransactionDigest != effectport.Hash("wechatpay.transaction", query.TransactionReference) {
			return domain.Payment{}, paymentport.WeChatPayPaymentQuery{}, "", paymentport.ErrConflict
		}
		outcome = "paid"
	case "CLOSED", "REVOKED", "PAYERROR":
		outcome = "final_failed"
	}
	return current, query, outcome, nil
}

func (s *Service) ReconcileWeChatPayRefund(ctx context.Context, refundID int64) (domain.Refund, error) {
	if s == nil || s.uow == nil || s.store == nil || s.payReconciler == nil || refundID < 1 {
		return domain.Refund{}, paymentport.ErrInvalid
	}
	var current domain.Refund
	var payment domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		current, inner = s.store.GetRefund(tx, refundID, false)
		if inner != nil {
			return inner
		}
		if current.Provider != domain.ProviderWeChatPay || (current.Status != domain.RefundEffectAccepted && current.Status != domain.RefundOutcomeUnknown && current.Status != domain.RefundCompleted) {
			return paymentport.ErrConflict
		}
		payment, inner = s.store.GetPayment(tx, current.PaymentID, false)
		if inner == nil && payment.Historical {
			return paymentport.ErrConflict
		}
		return inner
	})
	if err != nil {
		return domain.Refund{}, classify(err)
	}
	query, err := s.payReconciler.QueryRefund(ctx, current.RefundNo)
	if err != nil {
		return domain.Refund{}, paymentport.ErrUnavailable
	}
	if query.RefundNo != current.RefundNo || query.AmountMinor != current.AmountMinor || query.TotalMinor != payment.AmountMinor || query.Currency != payment.Currency || !effectport.ValidDigest(query.EvidenceDigest) || query.OccurredAt.IsZero() {
		return domain.Refund{}, paymentport.ErrConflict
	}
	outcome := "pending"
	switch query.Status {
	case "SUCCESS":
		if !effectport.ValidDigest(query.RefundDigest) {
			return domain.Refund{}, paymentport.ErrConflict
		}
		outcome = "refunded"
	case "CLOSED":
		outcome = "final_failed"
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := s.store.GetRefund(tx, refundID, true)
		if inner != nil {
			return inner
		}
		_, inner = s.store.RecordReconciliation(tx, locked.ID, query.EvidenceDigest, outcome, s.now().UTC())
		if inner != nil || outcome == "pending" || locked.Status == domain.RefundCompleted || locked.Status == domain.RefundFinalFailed {
			current = locked
			return inner
		}
		next := domain.RefundCompleted
		if outcome == "final_failed" {
			next = domain.RefundFinalFailed
		}
		locked, inner = locked.Complete(locked.Version, next, query.OccurredAt)
		if inner != nil {
			return inner
		}
		receiptKey := "reconcile:" + string(query.EvidenceDigest)
		current, inner = s.store.UpdateRefundSettlement(tx, locked, string(query.RefundDigest), receiptKey)
		if inner != nil {
			return inner
		}
		if outcome == "final_failed" {
			return s.notifyRefundExposureWithin(tx, payment.OrderID, current.ID, paymentport.RefundExposureFinalFailed, query.OccurredAt, receiptKey)
		}
		payment, inner = s.store.GetPayment(tx, current.PaymentID, false)
		if inner != nil {
			return inner
		}
		_, inner = s.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: payment.OrderID, RefundedDelta: current.AmountMinor, OccurredAt: query.OccurredAt, ReceiptKey: receiptKey})
		return inner
	})
	return current, classify(err)
}

func (s *Service) ReconcileAlipayPayment(ctx context.Context, paymentID int64) (domain.Payment, error) {
	if s == nil || s.alipayReconciler == nil || paymentID < 1 {
		return domain.Payment{}, paymentport.ErrInvalid
	}
	var current domain.Payment
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		current, err = s.store.GetPayment(tx, paymentID, false)
		return err
	}); err != nil {
		return domain.Payment{}, classify(err)
	}
	if current.Provider != domain.ProviderAlipay || current.Historical {
		return domain.Payment{}, paymentport.ErrConflict
	}
	query, err := s.alipayReconciler.QueryPayment(ctx, current.MerchantOrderNo)
	if err != nil {
		return domain.Payment{}, paymentport.ErrUnavailable
	}
	if query.MerchantOrderNo != current.MerchantOrderNo || query.AmountMinor != current.AmountMinor || query.Currency != current.Currency || !effectport.ValidDigest(query.EvidenceDigest) || query.OccurredAt.IsZero() {
		return domain.Payment{}, paymentport.ErrConflict
	}
	outcome := "pending"
	if query.TradeStatus == "TRADE_SUCCESS" || query.TradeStatus == "TRADE_FINISHED" {
		outcome = "paid"
	} else if query.TradeStatus == "TRADE_CLOSED" {
		outcome = "final_failed"
	}
	if outcome == "paid" && (!effectport.ValidDigest(query.TransactionDigest) || !validProviderTransactionReference(query.TradeNo)) {
		return domain.Payment{}, paymentport.ErrConflict
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := s.store.GetPayment(tx, paymentID, true)
		if inner != nil {
			return inner
		}
		if _, inner = s.store.RecordPaymentReconciliation(tx, locked.ID, query.EvidenceDigest, outcome, s.now().UTC()); inner != nil || outcome == "pending" || locked.Status == domain.StatusPaid && outcome == "paid" {
			current = locked
			return inner
		}
		next := domain.StatusPaid
		if outcome == "final_failed" {
			next = domain.StatusFailed
		}
		locked, inner = locked.Settle(locked.Version, next, query.OccurredAt)
		if inner != nil {
			return inner
		}
		locked.ProviderTransactionReference = query.TradeNo
		receipt := "reconcile:" + string(query.EvidenceDigest)
		current, inner = s.store.UpdatePaymentSettlement(tx, locked, string(query.TransactionDigest), receipt)
		if inner != nil {
			return inner
		}
		_, inner = s.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: current.OrderID, Failed: outcome == "final_failed", ProviderTransactionNo: query.TradeNo, OccurredAt: query.OccurredAt, ReceiptKey: receipt})
		return inner
	})
	return current, classify(err)
}

func (s *Service) ReconcileAlipayRefund(ctx context.Context, refundID int64) (domain.Refund, error) {
	if s == nil || s.alipayReconciler == nil || refundID < 1 {
		return domain.Refund{}, paymentport.ErrInvalid
	}
	var current domain.Refund
	var payment domain.Payment
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		current, err = s.store.GetRefund(tx, refundID, false)
		if err != nil {
			return err
		}
		payment, err = s.store.GetPayment(tx, current.PaymentID, false)
		return err
	}); err != nil {
		return domain.Refund{}, classify(err)
	}
	if current.Provider != domain.ProviderAlipay || payment.Historical {
		return domain.Refund{}, paymentport.ErrConflict
	}
	query, err := s.alipayReconciler.QueryRefund(ctx, current.RefundNo)
	if err != nil {
		return domain.Refund{}, paymentport.ErrUnavailable
	}
	if query.RefundNo != current.RefundNo || query.AmountMinor != current.AmountMinor || query.TotalMinor != payment.AmountMinor || query.Currency != payment.Currency || !effectport.ValidDigest(query.EvidenceDigest) || query.OccurredAt.IsZero() {
		return domain.Refund{}, paymentport.ErrConflict
	}
	outcome := "pending"
	if query.Status == "REFUND_SUCCESS" {
		outcome = "refunded"
	} else if query.Status == "REFUND_CLOSED" {
		outcome = "final_failed"
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := s.store.GetRefund(tx, refundID, true)
		if inner != nil {
			return inner
		}
		if _, inner = s.store.RecordReconciliation(tx, locked.ID, query.EvidenceDigest, outcome, s.now().UTC()); inner != nil || outcome == "pending" || locked.Status == domain.RefundCompleted || locked.Status == domain.RefundFinalFailed {
			current = locked
			return inner
		}
		next := domain.RefundCompleted
		if outcome == "final_failed" {
			next = domain.RefundFinalFailed
		}
		locked, inner = locked.Complete(locked.Version, next, query.OccurredAt)
		if inner != nil {
			return inner
		}
		receipt := "reconcile:" + string(query.EvidenceDigest)
		current, inner = s.store.UpdateRefundSettlement(tx, locked, string(query.RefundDigest), receipt)
		if inner != nil {
			return inner
		}
		if outcome == "final_failed" {
			return s.notifyRefundExposureWithin(tx, payment.OrderID, current.ID, paymentport.RefundExposureFinalFailed, query.OccurredAt, receipt)
		}
		_, inner = s.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: payment.OrderID, RefundedDelta: current.AmountMinor, OccurredAt: query.OccurredAt, ReceiptKey: receipt})
		return inner
	})
	return current, classify(err)
}
func validProviderTransactionReference(value string) bool {
	return value != "" && len(value) <= 200 && value == strings.TrimSpace(value)
}

func (s *Service) ApplyVerifiedCallback(ctx context.Context, callback paymentprovider.CallbackResult) error {
	if callback.Provider == "" {
		callback.Provider = domain.ProviderWeChatPay
	}
	providerDigestNamespace := "wechatpay.transaction"
	callbackProvider := "wechat_pay"
	if callback.Provider == domain.ProviderAlipay {
		providerDigestNamespace = "alipay.transaction"
		callbackProvider = "alipay"
	}
	if !s.ready() || (callback.Kind != "payment" && callback.Kind != "refund") || callback.AmountMinor < 1 || callback.Currency != "CNY" || callback.OccurredAt.IsZero() ||
		(callback.Kind == "payment" && (!validProviderTransactionReference(callback.ProviderTransactionReference) || callback.ProviderTransactionDigest != string(effectport.Hash(providerDigestNamespace, callback.ProviderTransactionReference)))) {
		return paymentport.ErrInvalid
	}
	if callback.Kind == "payment" {
		callback.OccurredAt = canonicalProviderOccurredAt(callback.OccurredAt)
	}
	return classify(s.uow.Within(ctx, func(tx context.Context) error {
		if callback.Kind == "payment" {
			payment, err := s.store.GetPaymentByMerchant(tx, callback.MerchantOrderNo, true)
			if err != nil {
				return err
			}
			if payment.Provider != callback.Provider || payment.AmountMinor != callback.AmountMinor || payment.Currency != callback.Currency || !s.callbackAppIDMatches(payment, callback.AppID) {
				return paymentport.ErrConflict
			}
			// Reconciliation may have already settled this exact Provider fact
			// before the original notification arrives.  The callback still gets
			// an immutable receipt, but it must not settle the Order a second time
			// or rerun its paid-event consumers.
			if payment.Status == domain.StatusPaid {
				if payment.ProviderTransactionReference != callback.ProviderTransactionReference || payment.ProviderTransactionDigest != callback.ProviderTransactionDigest || !samePaymentConfirmationTime(payment.PaidConfirmedAt, callback.OccurredAt) {
					return paymentport.ErrConflict
				}
				_, err = s.store.ClaimCallback(tx, callbackProvider, callback.EventDigest, callback.BodyDigest, "payment", "replayed", payment.ID)
				return err
			}
			replay, err := s.store.ClaimCallback(tx, callbackProvider, callback.EventDigest, callback.BodyDigest, "payment", "settled", payment.ID)
			if err != nil || replay {
				return err
			}
			payment, err = payment.Settle(payment.Version, domain.StatusPaid, callback.OccurredAt)
			if err != nil {
				return err
			}
			payment.ProviderTransactionReference = callback.ProviderTransactionReference
			receiptKey := "callback:" + hexDigest(callback.EventDigest)
			if _, err = s.store.UpdatePaymentSettlement(tx, payment, callback.ProviderTransactionDigest, receiptKey); err != nil {
				return err
			}
			if !validProviderTransactionReference(callback.ProviderTransactionReference) {
				return paymentport.ErrConflict
			}
			_, err = s.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: payment.OrderID, ProviderTransactionNo: callback.ProviderTransactionReference, OccurredAt: callback.OccurredAt, ReceiptKey: receiptKey})
			return err
		}
		refund, err := s.store.GetRefundByNumber(tx, callback.RefundNo, true)
		if err != nil {
			return err
		}
		if refund.AmountMinor != callback.AmountMinor {
			return paymentport.ErrConflict
		}
		payment, err := s.store.GetPayment(tx, refund.PaymentID, false)
		if err != nil || payment.Provider != callback.Provider || !s.callbackAppIDMatches(payment, callback.AppID) {
			return paymentport.ErrConflict
		}
		replay, err := s.store.ClaimCallback(tx, callbackProvider, callback.EventDigest, callback.BodyDigest, "refund", "settled", refund.ID)
		if err != nil || replay {
			return err
		}
		refund, err = refund.Complete(refund.Version, domain.RefundCompleted, callback.OccurredAt)
		if err != nil {
			return err
		}
		receiptKey := "callback:" + hexDigest(callback.EventDigest)
		if _, err = s.store.UpdateRefundSettlement(tx, refund, callback.ProviderRefundDigest, receiptKey); err != nil {
			return err
		}
		_, err = s.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: payment.OrderID, RefundedDelta: refund.AmountMinor, OccurredAt: callback.OccurredAt, ReceiptKey: receiptKey})
		return err
	}))
}

// canonicalProviderOccurredAt keeps Provider success facts at PostgreSQL
// timestamptz precision. The signed callback/query payload can contain
// RFC3339 nanoseconds, while PostgreSQL persists microseconds. Normalizing
// only this representation preserves exact comparison of one Provider fact
// across callback, reconciliation and Order evidence reads.
func canonicalProviderOccurredAt(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func samePaymentConfirmationTime(stored *time.Time, received time.Time) bool {
	return stored != nil && !received.IsZero() && canonicalProviderOccurredAt(*stored).Equal(canonicalProviderOccurredAt(received))
}

func (s *Service) callbackAppIDMatches(payment domain.Payment, appID string) bool {
	if payment.Provider == domain.ProviderAlipay {
		return s.alipayAppID == "" || appID == s.alipayAppID
	}
	if s.miniAppID == "" { // Tests and provider-disabled compositions have no accepted callback surface.
		return true
	}
	if payment.Channel == domain.ChannelH5Official {
		return s.h5AppID != "" && appID == s.h5AppID
	}
	return payment.Channel == domain.ChannelMiniProgram && appID == s.miniAppID
}

func hexDigest(value [32]byte) string { return fmt.Sprintf("%x", value[:]) }

func (s *Service) ImportTerminalPayment(ctx context.Context, payment domain.Payment, digest [32]byte, runID string) (domain.Payment, error) {
	if s == nil || s.uow == nil || s.store == nil || digest == ([32]byte{}) || !validScope(runID) || payment.ID != 0 || payment.OrderID < 1 || payment.PayerIdentityID < 0 || payment.PayerCustomerID < 0 || payment.BeneficiaryCustomerID < 0 || ((payment.PayerIdentityID == 0) != (payment.PayerCustomerID == 0)) || (payment.PayerCustomerID == 0 && payment.BeneficiaryCustomerID != 0) || payment.AmountMinor < 1 || payment.Currency != "CNY" || (payment.Status != domain.StatusPaid && payment.Status != domain.StatusFailed && payment.Status != domain.StatusCancelled) || payment.EffectID != "" {
		return domain.Payment{}, paymentport.ErrInvalid
	}
	var out domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		out, err = s.store.ImportTerminalPayment(tx, payment, digest, runID)
		return err
	})
	return out, classify(err)
}

func (s *Service) ImportTerminalRefund(ctx context.Context, refund domain.Refund, digest [32]byte, runID string) (domain.Refund, error) {
	if s == nil || s.uow == nil || s.store == nil || digest == ([32]byte{}) || !validScope(runID) || refund.ID != 0 || refund.PaymentID < 1 || refund.AmountMinor < 1 || !refund.Status.HistoricalImportable() || refund.EffectID != "" {
		return domain.Refund{}, paymentport.ErrInvalid
	}
	var out domain.Refund
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		out, err = s.store.ImportTerminalRefund(tx, refund, digest, runID)
		return err
	})
	return out, classify(err)
}
func (s *Service) ready() bool {
	return s != nil && s.uow != nil && s.store != nil && s.orders != nil && s.sessions != nil && s.effects != nil && s.now != nil
}

func validShopRefundCommand(command paymentport.RefundCommand) bool {
	if !validScope(command.ProviderOrderID) || !validScope(command.ProductID) || !validScope(command.SKUID) || command.RefundCount < 1 || command.RefundCount > 1_000_000 {
		return false
	}
	switch command.ReasonCode {
	case "10000000", "10000001", "10000002", "10000006", "10000007", "10000008", "10000014", "10000015", "10000017", "10000021":
		return true
	default:
		return false
	}
}
func validKey(v string) bool   { return v == strings.TrimSpace(v) && len(v) >= 16 && len(v) <= 200 }
func validScope(v string) bool { return v == strings.TrimSpace(v) && len(v) > 0 && len(v) <= 200 }

// checkoutReadAuthorized permits a fresh trusted session to read a prior order
// only when the same Payment-owned payer identity and canonical customer match.
// A newly issued OAuth session is intentionally unresolved until a new checkout
// selects a recipient, but that browser state must not hide an already persisted
// payment whose beneficiary was selected in the original transaction.
func checkoutReadAuthorized(payment domain.Payment, actor paymentport.SessionActor) bool {
	if payment.PayerIdentityID != actor.PayerIdentityID || payment.PayerCustomerID != actor.PayerCustomerID || !checkoutSessionChannelMatches(payment, actor) {
		return false
	}
	switch actor.BeneficiarySelection {
	case paymentport.BeneficiarySelectionUnresolved:
		return actor.BeneficiaryCustomerID == 0
	case paymentport.BeneficiarySelectionPayerSelf:
		return actor.BeneficiaryCustomerID == actor.PayerCustomerID && payment.BeneficiaryCustomerID == actor.BeneficiaryCustomerID
	case paymentport.BeneficiarySelectionAdminAssisted:
		return actor.BeneficiaryCustomerID > 0 && payment.BeneficiaryCustomerID == actor.BeneficiaryCustomerID
	default:
		return false
	}
}

func checkoutSessionChannelMatches(payment domain.Payment, actor paymentport.SessionActor) bool {
	switch payment.Provider {
	case domain.ProviderWeChatPay:
		return payment.Channel == actor.Channel
	case domain.ProviderAlipay:
		return actor.Channel == domain.ChannelH5Official && (payment.Channel == domain.ChannelAlipayWap || payment.Channel == domain.ChannelAlipayPage)
	default:
		return false
	}
}

func selectedBeneficiary(actor paymentport.SessionActor) bool {
	switch actor.BeneficiarySelection {
	case paymentport.BeneficiarySelectionPayerSelf:
		return actor.PayerCustomerID > 0 && actor.BeneficiaryCustomerID == actor.PayerCustomerID
	case paymentport.BeneficiarySelectionAdminAssisted:
		return actor.BeneficiaryCustomerID > 0
	default:
		return false
	}
}

func validMainlandMobileE164(v string) bool {
	if len(v) != 14 || !strings.HasPrefix(v, "+861") || v[4] < '3' || v[4] > '9' {
		return false
	}
	return strings.IndexFunc(v[1:], func(r rune) bool { return r < '0' || r > '9' }) < 0
}
func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, paymentport.ErrAlreadyPurchased), errors.Is(err, paymentport.ErrPurchasePending):
		return err
	case errors.Is(err, paymentport.ErrNotFound), errors.Is(err, orderport.ErrNotFound):
		return paymentport.ErrNotFound
	case errors.Is(err, paymentport.ErrSessionRequired):
		return paymentport.ErrSessionRequired
	case errors.Is(err, paymentport.ErrSessionMismatch):
		return paymentport.ErrSessionMismatch
	case errors.Is(err, paymentport.ErrConflict), errors.Is(err, orderport.ErrConflict), errors.Is(err, domain.ErrInvalid), errors.Is(err, domain.ErrTransition), errors.Is(err, domain.ErrVersion), errors.Is(err, effectport.ErrReconciliationConflict), errors.Is(err, effectport.ErrPayloadMismatch):
		return paymentport.ErrConflict
	default:
		return paymentport.ErrUnavailable
	}
}
