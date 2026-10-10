package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

const promotionCredentialTTL = 30 * 24 * time.Hour
const paidCampaignReferralProvenance = "paid_campaign_referral_v1"

// promotionProductScanMaximum bounds one public request while still allowing
// the server-side eligibility gate to advance past a Product-owned source page
// that contains no promotable rows. The browser never receives an ineligible
// candidate as a substitute for an empty-state explanation.
const promotionProductScanMaximum = int32(500)

type promotionStore interface {
	ReadDistributorByCustomerWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	ReadDistributorWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	ReadProductPolicyWithin(context.Context, int64, distributiondomain.ProductType) (distributiondomain.Policy, error)
	ReadProductPolicyForUpdateWithin(context.Context, int64, distributiondomain.ProductType) (distributiondomain.Policy, error)
	ProductPolicyIDWithin(context.Context, int64, distributiondomain.ProductType) (int64, error)
	InsertPromotionCredentialWithin(context.Context, distributiondomain.PromotionCredential) (distributiondomain.PromotionCredential, error)
	ReadPromotionCredentialByDigestWithin(context.Context, [32]byte, bool) (distributiondomain.PromotionCredential, error)
	ReadPromotionCredentialWithin(context.Context, int64, bool) (distributiondomain.PromotionCredential, error)
	ExpirePromotionCredentialWithin(context.Context, distributiondomain.PromotionCredential, time.Time) error
	InsertAttributionWithin(context.Context, distributiondomain.Attribution, int64) (distributiondomain.Attribution, bool, error)
	LockOperationReceiptWithin(context.Context, string, string, string) error
	ReadOperationReceiptWithin(context.Context, string, string, string) (distributionstore.OperationReceipt, bool, error)
	AppendOperationReceiptWithin(context.Context, string, string, string, [sha256.Size]byte, string, int64, time.Time) error
	AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error
	AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error
}

// paidReferralPromotionStore is used only by the paid campaign invitation
// checkout path. It remains a narrow Distribution-owned persistence seam.
type paidReferralPromotionStore interface {
	ReadDistributorByCustomerWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	InsertDistributorWithin(context.Context, int64, string, string, time.Time) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	UpdateReceiverReadinessWithin(context.Context, int64, int64, distributionport.ReceiverReadiness, time.Time) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	GetOrInsertPromotionCredentialWithin(context.Context, distributiondomain.PromotionCredential) (distributiondomain.PromotionCredential, bool, error)
}

// PromotionService owns opaque credential issuance and Order's checkout-time
// attribution callback. The browser never chooses commission, policy, seller,
// payment recipient, or qualification evidence.
type PromotionService struct {
	uow               platformport.UnitOfWork
	store             promotionStore
	qualification     *QualificationService
	products          productport.ProductOptionReader
	saleableProduct   productport.SidebarProductShareReader
	lineage           identityport.LockedCanonicalLineageReader
	subjects          distributionIdentitySubjects
	paidInvitations   referralport.PaidInvitationCheckoutReader
	settlement        paymentport.DistributionSettlementPort
	promotionOrigin   string
	tokens            promotionTokenIssuer
	settlementEnabled bool
	now               func() time.Time
}

func (s *PromotionService) BindPaidInvitationCheckoutReader(reader referralport.PaidInvitationCheckoutReader) error {
	if s == nil || reader == nil {
		return distributionport.ErrUnavailable
	}
	s.paidInvitations = reader
	return nil
}

func (s *PromotionService) BindProfitSharingSettlement(settlement paymentport.DistributionSettlementPort) error {
	if s == nil || settlement == nil {
		return distributionport.ErrUnavailable
	}
	s.settlement = settlement
	return nil
}

func (s *PromotionService) BindIdentitySubjects(reader identityport.VerifiedIdentitySubjectReader) error {
	if s == nil {
		return distributionport.ErrUnavailable
	}
	value, err := distributionSubjects(reader)
	if err != nil {
		return err
	}
	s.subjects = value
	return nil
}

// SetSettlementEnabled controls whether a valid checkout becomes a financial
// Distribution attribution. Public registration and policy reads remain
// available while the Payment profit-sharing capability is closed, but Order
// then treats every checkout as ordinary and cannot freeze funds.
func (s *PromotionService) SetSettlementEnabled(enabled bool) {
	if s != nil {
		s.settlementEnabled = enabled
	}
}

func NewPromotionService(uow platformport.UnitOfWork, store promotionStore, qualification *QualificationService, products productport.ProductOptionReader, saleable productport.SidebarProductShareReader, lineage identityport.LockedCanonicalLineageReader, promotionOrigin, tokenDataKey string) (*PromotionService, error) {
	if uow == nil || store == nil || qualification == nil || products == nil || saleable == nil || lineage == nil || !validPromotionOrigin(promotionOrigin) {
		return nil, distributionport.ErrUnavailable
	}
	tokens, err := newPromotionTokenIssuer(tokenDataKey)
	if err != nil {
		return nil, distributionport.ErrUnavailable
	}
	return &PromotionService{uow: uow, store: store, qualification: qualification, products: products, saleableProduct: saleable, lineage: lineage, promotionOrigin: strings.TrimRight(promotionOrigin, "/"), tokens: tokens, now: time.Now}, nil
}

func (s *PromotionService) ListPromotionProducts(ctx context.Context, actor distributionport.TrustedSessionActor, cursor string, limit int32) (distributionport.PromotionPage, error) {
	if s == nil || !actor.Valid() || limit < 1 || limit > productport.ProductOptionMaximumLimit {
		return distributionport.PromotionPage{}, distributionport.ErrConflict
	}
	offset, err := promotionProductOffset(cursor)
	if err != nil {
		return distributionport.PromotionPage{}, distributionport.ErrConflict
	}
	// Product owns discoverability/pagination. Distribution deliberately does
	// not query a Product table; its current saleable reader below is the
	// authoritative lifecycle check before a row is shown as promotable. The
	// cursor is Product's bounded offset, never a Distribution table key.
	var distributor distributiondomain.Distributor
	var receiver distributionport.ReceiverReadiness
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		if s.subjects != nil {
			actor, err = distributionActorWithin(tx, s.subjects, s.store, actor, false)
			if err != nil {
				return err
			}
		}
		distributor, receiver, err = s.store.ReadDistributorByCustomerWithin(tx, actor.CustomerID, false)
		return err
	})
	if errors.Is(err, distributionport.ErrNotFound) {
		return distributionport.PromotionPage{}, distributionport.ErrUnauthorized
	}
	if err != nil {
		return distributionport.PromotionPage{}, err
	}
	page := distributionport.PromotionPage{Items: make([]distributionport.PromotionProduct, 0, limit)}
	if !distributor.Enabled {
		page.EmptyReason = "distributor_disabled"
		return page, nil
	}
	scanned := int32(0)
	emptyReason := "no_saleable_policy_products"
	for int32(len(page.Items)) < limit && scanned < promotionProductScanMaximum {
		remaining := promotionProductScanMaximum - scanned
		// The cursor is a raw Product offset. Fetch no more than this client
		// page can return, otherwise accepting an early option would skip later
		// eligible rows from the same Product source page on the next cursor.
		sourceLimit := limit - int32(len(page.Items))
		if remaining < sourceLimit {
			sourceLimit = remaining
		}
		options, readErr := s.products.ListProductOptions(ctx, productport.ProductOptionQuery{ProductType: productport.ProductOptionAll, Limit: sourceLimit, Offset: offset})
		if readErr != nil {
			return distributionport.PromotionPage{}, readErr
		}
		if options.Offset != offset || options.Total < 0 || len(options.Items) > int(sourceLimit) {
			return distributionport.PromotionPage{}, distributionport.ErrUnavailable
		}
		next := int64(offset) + int64(len(options.Items))
		if next > int64(productport.ProductOptionMaximumOffset) || next > options.Total {
			return distributionport.PromotionPage{}, distributionport.ErrUnavailable
		}
		if len(options.Items) == 0 {
			if int64(offset) != options.Total {
				return distributionport.PromotionPage{}, distributionport.ErrUnavailable
			}
			if len(page.Items) == 0 {
				page.EmptyReason = "no_saleable_policy_products"
			}
			return page, nil
		}
		offset = int32(next)
		scanned += int32(len(options.Items))
		for _, option := range options.Items {
			kind, ok := distributionProductType(option.ProductType)
			if !ok || option.ID < 1 || option.PriceMinor < 0 || option.Currency != "CNY" {
				continue
			}
			// This current Product read excludes draft/disabled/archived products.
			saleable, saleableErr := s.saleableProduct.ReadSidebarShareProduct(ctx, option.ProductType, option.ID)
			if errors.Is(saleableErr, productport.ErrSaleableProductNotFound) {
				continue
			}
			if saleableErr != nil {
				return distributionport.PromotionPage{}, distributionport.ErrUnavailable
			}
			var policy distributiondomain.Policy
			var qualification distributiondomain.Qualification
			readErr := s.uow.Within(ctx, func(tx context.Context) error {
				var err error
				if s.subjects != nil {
					currentActor, subjectErr := distributionActorWithin(tx, s.subjects, s.store, actor, false)
					if subjectErr != nil {
						return subjectErr
					}
					if currentActor.CustomerID != distributor.CustomerID {
						return distributionport.ErrUnauthorized
					}
				}
				policy, err = s.store.ReadProductPolicyWithin(tx, int64(option.ID), kind)
				if errors.Is(err, distributionport.ErrNotFound) {
					return nil
				}
				if err != nil {
					return err
				}
				if !policy.Enabled {
					return nil
				}
				qualification, err = s.qualification.CheckWithin(tx, distributor.CustomerID, int64(option.ID), kind)
				return err
			})
			if readErr != nil {
				return distributionport.PromotionPage{}, readErr
			}
			if !policy.Enabled {
				continue
			}
			if !qualification.AllowsPromotion() {
				emptyReason = preferredPromotionEmptyReason(emptyReason, promotionQualificationBlock(qualification))
				continue
			}
			estimated, calcErr := distributiondomain.CalculateCommission(option.PriceMinor, policy.CommissionRateBasisPoints)
			if calcErr != nil {
				return distributionport.PromotionPage{}, distributionport.ErrUnavailable
			}
			purchaseURL := "/p/" + saleable.Code
			if kind == distributiondomain.ProductTypeServicePeriod {
				purchaseURL = "/s/" + saleable.Code
			}
			// Qualification is a strict server-side inclusion gate. A qualified
			// product may still report a Payment-owned settlement block, but it
			// never turns an unqualified product into a visible promotion card.
			ready := s.settlementEnabled && receiver.Ready && receiver.AppID == actor.AppID
			block := ""
			if !s.settlementEnabled {
				block = "merchant_settlement_disabled"
			}
			if block == "" && !ready {
				block = receiver.Reason
				if block == "" {
					block = "receiver_not_ready"
				}
			}
			page.Items = append(page.Items, distributionport.PromotionProduct{ProductID: int64(option.ID), ProductType: kind, CoverURL: saleable.CoverURL, Name: saleable.Name, PurchaseURL: purchaseURL, PriceMinor: option.PriceMinor, Currency: option.Currency, CommissionRateBasisPoints: policy.CommissionRateBasisPoints, EstimatedCommissionMinor: estimated, WaitDays: policy.WaitDays, PromotionReady: ready, PromotionBlockReason: block})
			if int32(len(page.Items)) == limit {
				break
			}
		}
		if int64(offset) >= options.Total {
			if len(page.Items) == 0 {
				page.EmptyReason = emptyReason
			}
			return page, nil
		}
		if int32(len(page.Items)) == limit || scanned == promotionProductScanMaximum {
			page.NextCursor = strconv.FormatInt(int64(offset), 10)
			return page, nil
		}
	}
	if scanned > 0 {
		page.NextCursor = strconv.FormatInt(int64(offset), 10)
	}
	return page, nil
}

func promotionQualificationBlock(qualification distributiondomain.Qualification) string {
	if qualification.AllowsPromotion() {
		return ""
	}
	switch qualification.State {
	case distributiondomain.QualificationIneligible:
		return "qualification_purchase_required"
	case distributiondomain.QualificationSuspended:
		return "qualification_refund_pending"
	case distributiondomain.QualificationUnavailable:
		if qualification.Reason == "payment_confirmation_missing" {
			return "qualification_payment_confirmation_missing"
		}
		return "qualification_check_unavailable"
	default:
		return "qualification_check_unavailable"
	}
}

// preferredPromotionEmptyReason returns only a server-derived state. It is
// used when every policy-enabled saleable product was filtered by eligibility;
// no product card is emitted for any of these states. Confirmation evidence
// has priority because buying again cannot repair an already-paid order.
func preferredPromotionEmptyReason(current, candidate string) string {
	priority := map[string]int{
		"no_saleable_policy_products":                0,
		"qualification_purchase_required":            1,
		"qualification_refund_pending":               2,
		"qualification_check_unavailable":            3,
		"qualification_payment_confirmation_missing": 4,
	}
	if priority[candidate] > priority[current] {
		return candidate
	}
	return current
}

// ApplicationTarget resolves a shared application link with current Product
// lifecycle facts. It is intentionally independent of a distributor session:
// being able to view a saleable, enabled policy is not evidence of identity,
// registration, qualification, receiver readiness, settlement availability or
// a right to issue a promotion credential.
func (s *PromotionService) ApplicationTarget(ctx context.Context, productID int64, productType distributiondomain.ProductType) (distributionport.ApplicationTarget, error) {
	if s == nil || s.uow == nil || s.store == nil || s.saleableProduct == nil || productID < 1 || !productType.Valid() {
		return distributionport.ApplicationTarget{}, distributionport.ErrConflict
	}
	var product productport.SidebarShareProduct
	err := s.uow.Within(ctx, func(tx context.Context) error {
		policy, err := s.store.ReadProductPolicyWithin(tx, productID, productType)
		if err != nil {
			return err
		}
		if !policy.Enabled {
			return distributionport.ErrNotFound
		}
		product, err = s.saleableProduct.ReadSidebarShareProduct(tx, optionType(productType), productport.ID(productID))
		if err != nil {
			if errors.Is(err, productport.ErrSaleableProductNotFound) {
				return distributionport.ErrNotFound
			}
			return distributionport.ErrUnavailable
		}
		if product.ID != productport.ID(productID) || product.ProductType != optionType(productType) || strings.TrimSpace(product.Code) == "" || strings.TrimSpace(product.Name) == "" {
			return distributionport.ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return distributionport.ApplicationTarget{}, err
	}
	purchaseURL := "/p/" + product.Code
	if productType == distributiondomain.ProductTypeServicePeriod {
		purchaseURL = "/s/" + product.Code
	}
	return distributionport.ApplicationTarget{ProductID: productID, ProductType: productType, PolicyEnabled: true, ProductName: product.Name, PurchaseURL: purchaseURL}, nil
}

func promotionProductOffset(cursor string) (int32, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.ParseInt(cursor, 10, 32)
	if err != nil || offset < 1 || offset > int64(productport.ProductOptionMaximumOffset) || cursor != strconv.FormatInt(offset, 10) {
		return 0, distributionport.ErrConflict
	}
	return int32(offset), nil
}

func (s *PromotionService) IssuePromotionLink(ctx context.Context, command distributionport.IssuePromotionCommand) (distributionport.PromotionLink, error) {
	if s == nil || !s.settlementEnabled {
		return distributionport.PromotionLink{}, distributionport.ErrUnavailable
	}
	if !command.Actor.Valid() || command.ProductID < 1 || !command.ProductType.Valid() || !validPromotionIdempotencyKey(command.IdempotencyKey) {
		return distributionport.PromotionLink{}, distributionport.ErrConflict
	}
	payloadDigest := promotionCredentialPayloadDigest(command.ProductID, command.ProductType)
	// PostgreSQL timestamps have microsecond precision. Normalize the service
	// clock before persistence and response so an exact receipt replay returns
	// the same expiry instant as the first call.
	now := s.now().UTC().Truncate(time.Microsecond)
	var token string
	var expiresAt time.Time
	err := s.uow.Within(ctx, func(tx context.Context) error {
		if s.subjects != nil {
			var err error
			command.Actor, err = distributionActorWithin(tx, s.subjects, s.store, command.Actor, true)
			if err != nil {
				return err
			}
		}
		actorScope := promotionActorScope(command.Actor)
		var err error
		token, err = s.tokens.issue(actorScope, command.ProductID, command.ProductType, command.IdempotencyKey)
		if err != nil {
			return distributionport.ErrUnavailable
		}
		digest := sha256.Sum256([]byte(token))
		if err := s.store.LockOperationReceiptWithin(tx, "credential", actorScope, command.IdempotencyKey); err != nil {
			return err
		}
		if replay, found, replayErr := s.promotionCredentialReplay(tx, command, actorScope, payloadDigest, digest); replayErr != nil {
			return replayErr
		} else if found {
			expiresAt = replay.ExpiresAt
			return nil
		}
		distributor, readiness, err := s.store.ReadDistributorByCustomerWithin(tx, command.Actor.CustomerID, true)
		if err != nil {
			return err
		}
		if !distributor.Enabled || !readiness.Ready || readiness.AppID != command.Actor.AppID {
			return distributionport.ErrQualification
		}
		policy, err := s.store.ReadProductPolicyForUpdateWithin(tx, command.ProductID, command.ProductType)
		if err != nil || !policy.Enabled {
			return distributionport.ErrQualification
		}
		if _, err = s.saleableProduct.ReadSidebarShareProduct(tx, optionType(command.ProductType), productport.ID(command.ProductID)); err != nil {
			return distributionport.ErrQualification
		}
		qualification, err := s.qualification.CheckWithin(tx, distributor.CustomerID, command.ProductID, command.ProductType)
		if err != nil || !qualification.AllowsPromotion() {
			return distributionport.ErrQualification
		}
		expiresAt = now.Add(promotionCredentialTTL)
		credential, err := s.store.InsertPromotionCredentialWithin(tx, distributiondomain.PromotionCredential{DistributorID: distributor.ID, ProductID: command.ProductID, ProductType: command.ProductType, TokenDigest: digest, Status: distributiondomain.CredentialActive, CreatedAt: now, ExpiresAt: expiresAt})
		if err != nil {
			return err
		}
		payload := map[string]any{"credential_id": credential.ID, "product_id": command.ProductID, "expires_at": expiresAt}
		if err = s.store.AppendOperationReceiptWithin(tx, "credential", actorScope, command.IdempotencyKey, payloadDigest, "credential", credential.ID, now); err != nil {
			return err
		}
		if err = s.store.AppendAuditWithin(tx, "distribution.credential_issued.v1", "credential", credential.ID, "distributor:"+decimal(distributor.ID), payload, now); err != nil {
			return err
		}
		return s.store.AppendOutboxWithin(tx, "distribution.credential_issued.v1", "distribution.credential:"+base64.RawURLEncoding.EncodeToString(digest[:]), credential.ID, payload, now)
	})
	if err != nil {
		return distributionport.PromotionLink{}, err
	}
	return distributionport.PromotionLink{URL: s.promotionOrigin + "/d/" + token, ExpiresAt: expiresAt}, nil
}

func (s *PromotionService) promotionCredentialReplay(ctx context.Context, command distributionport.IssuePromotionCommand, actorScope string, payloadDigest, tokenDigest [sha256.Size]byte) (distributiondomain.PromotionCredential, bool, error) {
	receipt, found, err := s.store.ReadOperationReceiptWithin(ctx, "credential", actorScope, command.IdempotencyKey)
	if err != nil || !found {
		return distributiondomain.PromotionCredential{}, found, err
	}
	if receipt.PayloadDigest != payloadDigest || receipt.ResultKind != "credential" {
		return distributiondomain.PromotionCredential{}, true, distributionport.ErrConflict
	}
	credential, err := s.store.ReadPromotionCredentialWithin(ctx, receipt.ResultID, false)
	if err != nil {
		return distributiondomain.PromotionCredential{}, true, err
	}
	distributor, _, err := s.store.ReadDistributorWithin(ctx, credential.DistributorID, false)
	if err != nil {
		return distributiondomain.PromotionCredential{}, true, err
	}
	if distributor.CustomerID != command.Actor.CustomerID || credential.ProductID != command.ProductID || credential.ProductType != command.ProductType || credential.TokenDigest != tokenDigest {
		return distributiondomain.PromotionCredential{}, true, distributionport.ErrConflict
	}
	return credential, true, nil
}

func promotionActorScope(actor distributionport.TrustedSessionActor) string {
	return "customer:" + decimal(actor.CustomerID)
}

func promotionCredentialPayloadDigest(productID int64, productType distributiondomain.ProductType) [sha256.Size]byte {
	return sha256.Sum256([]byte("distribution.credential.v1:product_id=" + decimal(productID) + "&product_type=" + string(productType)))
}

func validPromotionIdempotencyKey(value string) bool {
	return value == strings.TrimSpace(value) && len(value) >= 16 && len(value) <= 200
}

// ResolvePromotionTarget is the public entry-point handoff. It validates only
// the opaque credential and current Product availability, then lets the HTTP
// adapter place the token into a server-controlled cookie. Qualification and
// policy are deliberately checked again inside Order's checkout UoW.
func (s *PromotionService) ResolvePromotionTarget(ctx context.Context, token string) (string, error) {
	if s == nil || !validPromotionToken(token) {
		return "", distributionport.ErrNotFound
	}
	digest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	var credential distributiondomain.PromotionCredential
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		credential, err = s.store.ReadPromotionCredentialByDigestWithin(tx, digest, true)
		if err != nil {
			return err
		}
		if credential.Status != distributiondomain.CredentialActive {
			return distributionport.ErrNotFound
		}
		if !credential.ExpiresAt.After(now) {
			if err = s.store.ExpirePromotionCredentialWithin(tx, credential, now); err != nil {
				return err
			}
			return distributionport.ErrNotFound
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	product, err := s.saleableProduct.ReadSidebarShareProduct(ctx, optionType(credential.ProductType), productport.ID(credential.ProductID))
	if err != nil {
		return "", distributionport.ErrNotFound
	}
	if credential.ProductType == distributiondomain.ProductTypeServicePeriod {
		return "/s/" + product.Code, nil
	}
	return "/p/" + product.Code, nil
}

// RecordCheckoutAttributionWithin is injected into Order. Invalid, revoked,
// expired, self, disabled, and ineligible credentials deliberately produce an
// ordinary purchase. A real persistence failure remains an error so an order
// cannot commit while pretending that a partial attribution was saved.
func (s *PromotionService) RecordCheckoutAttributionWithin(ctx context.Context, command orderport.CheckoutAttributionCommand) (orderport.CheckoutAttributionResult, error) {
	if s == nil || !s.settlementEnabled || s.store == nil || s.qualification == nil || s.lineage == nil || command.OrderID < 1 || command.OrderItemLine != 1 || command.ProductID < 1 || command.PayerCustomerID < 1 || command.BeneficiaryCustomerID < 1 || command.ItemPaidMinor < 1 || command.OccurredAt.IsZero() {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return orderport.CheckoutAttributionResult{}, distributionport.ErrUnavailable
	}
	// An RFI link is its own immutable campaign source. Never let a stale or
	// invalid RFI cookie fall through to an unrelated dpc_ cookie on the same
	// checkout, which could otherwise silently change the promoter.
	if strings.HasPrefix(command.ReferralActivityContext, "rfi_") {
		return s.recordPaidInvitationAttributionWithin(ctx, command)
	}
	if !validPromotionToken(command.PromotionContext) {
		return orderport.CheckoutAttributionResult{}, nil
	}
	productType := distributiondomain.ProductType(command.ProductType)
	if !productType.Valid() {
		return orderport.CheckoutAttributionResult{}, nil
	}
	digest := sha256.Sum256([]byte(command.PromotionContext))
	credential, err := s.store.ReadPromotionCredentialByDigestWithin(ctx, digest, true)
	if errors.Is(err, distributionport.ErrNotFound) {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if credential.Status != distributiondomain.CredentialActive || credential.ProductID != command.ProductID || credential.ProductType != productType {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if !credential.ExpiresAt.After(command.OccurredAt) {
		if err = s.store.ExpirePromotionCredentialWithin(ctx, credential, command.OccurredAt); err != nil {
			return orderport.CheckoutAttributionResult{}, err
		}
		return orderport.CheckoutAttributionResult{}, nil
	}
	distributor, _, err := s.store.ReadDistributorWithin(ctx, credential.DistributorID, true)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if !distributor.Enabled {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if s.isSelfPurchaseWithin(ctx, distributor.CustomerID, command.PayerCustomerID, command.BeneficiaryCustomerID) {
		return orderport.CheckoutAttributionResult{}, nil
	}
	policy, err := s.store.ReadProductPolicyForUpdateWithin(ctx, command.ProductID, productType)
	if errors.Is(err, distributionport.ErrNotFound) || (err == nil && !policy.Enabled) {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	qualification, err := s.qualification.CheckWithin(ctx, distributor.CustomerID, command.ProductID, productType)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if !qualification.AllowsPromotion() {
		return orderport.CheckoutAttributionResult{}, nil
	}
	policyID, err := s.store.ProductPolicyIDWithin(ctx, command.ProductID, productType)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	attribution := distributiondomain.Attribution{OrderID: command.OrderID, OrderItemLine: command.OrderItemLine, ProductCode: command.ProductCode, ProductName: command.ProductName, DistributorID: distributor.ID, PromotionCredentialID: credential.ID, QualificationEvidenceRef: qualification.EvidenceRef, QualificationState: qualification.State, PolicyVersion: policy.Version, CommissionRateBasisPoints: policy.CommissionRateBasisPoints, WaitDays: policy.WaitDays, AttributedAt: command.OccurredAt.UTC()}
	saved, created, err := s.store.InsertAttributionWithin(ctx, attribution, policyID)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if !created {
		return orderport.CheckoutAttributionResult{}, distributionport.ErrConflict
	}
	payload := map[string]any{"attribution_id": saved.ID, "order_id": command.OrderID, "policy_version": policy.Version}
	if err = s.store.AppendAuditWithin(ctx, "distribution.attribution_created.v1", "attribution", saved.ID, "order:"+decimal(command.OrderID), payload, command.OccurredAt); err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if err = s.store.AppendOutboxWithin(ctx, "distribution.attribution_created.v1", "distribution.attribution:order:"+decimal(command.OrderID)+":item:"+decimal(int64(command.OrderItemLine)), saved.ID, payload, command.OccurredAt); err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	commission, err := distributiondomain.CalculateCommission(command.ItemPaidMinor, policy.CommissionRateBasisPoints)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	return orderport.CheckoutAttributionResult{Attributed: true, ProfitSharingRequired: commission > 0, PromoterCustomerID: distributor.CustomerID, PromotionCredentialRef: "distribution.credential:" + decimal(credential.ID), PolicyVersion: policy.Version, CommissionRateBasisPoints: policy.CommissionRateBasisPoints, WaitDays: policy.WaitDays}, nil
}

func (s *PromotionService) recordPaidInvitationAttributionWithin(ctx context.Context, command orderport.CheckoutAttributionCommand) (orderport.CheckoutAttributionResult, error) {
	if !validReferralInvitationToken(command.ReferralActivityContext) {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if s.paidInvitations == nil || s.settlement == nil || s.subjects == nil {
		return orderport.CheckoutAttributionResult{}, distributionport.ErrUnavailable
	}
	digest := sha256.Sum256([]byte(command.ReferralActivityContext))
	invitation, err := s.paidInvitations.ReadPaidInvitationCheckoutWithin(ctx, digest, command.ProductID, command.ProductType, command.OccurredAt.UTC())
	if errors.Is(err, referralport.ErrNotFound) {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	inviter := invitation.Inviter
	if invitation.CampaignID < 1 || invitation.InvitationID < 1 || invitation.ProductID != command.ProductID || invitation.ProductType != command.ProductType || !invitation.ExpiresAt.After(command.OccurredAt.UTC()) || !inviter.Valid() {
		return orderport.CheckoutAttributionResult{}, distributionport.ErrUnavailable
	}
	// A current merge can change the inviter's canonical root after the
	// invitation was issued. Do not guess which historical participation or
	// receiver should own money; merged identities remain activity-only.
	roots, err := s.lineage.LockedCanonicalLineage(ctx, customerdomain.CustomerID(inviter.CustomerID))
	if err != nil || len(roots) != 1 || int64(roots[0]) != inviter.CustomerID {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if s.isSelfPurchaseWithin(ctx, inviter.CustomerID, command.PayerCustomerID, command.BeneficiaryCustomerID) {
		return orderport.CheckoutAttributionResult{}, nil
	}
	productType := distributiondomain.ProductType(command.ProductType)
	if !productType.Valid() {
		return orderport.CheckoutAttributionResult{}, nil
	}
	policy, err := s.store.ReadProductPolicyForUpdateWithin(ctx, command.ProductID, productType)
	if errors.Is(err, distributionport.ErrNotFound) || err == nil && (!policy.Enabled || policy.CommissionRateBasisPoints == 0) {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	qualification, err := s.qualification.CheckWithin(ctx, inviter.CustomerID, command.ProductID, productType)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if !qualification.AllowsPromotion() {
		return orderport.CheckoutAttributionResult{}, nil
	}
	commission, err := distributiondomain.CalculateCommission(command.ItemPaidMinor, policy.CommissionRateBasisPoints)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if commission < 1 {
		return orderport.CheckoutAttributionResult{}, nil
	}
	store, ok := s.store.(paidReferralPromotionStore)
	if !ok {
		return orderport.CheckoutAttributionResult{}, distributionport.ErrUnavailable
	}
	distributor, readiness, err := store.ReadDistributorByCustomerWithin(ctx, inviter.CustomerID, false)
	if errors.Is(err, distributionport.ErrNotFound) {
		distributor, readiness, err = s.ensurePaidReferralDistributorWithin(ctx, store, invitation, inviter.CustomerID, command.OccurredAt.UTC())
	}
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if !distributor.Enabled || distributor.CustomerID != inviter.CustomerID {
		return orderport.CheckoutAttributionResult{}, nil
	}
	credential, created, err := store.GetOrInsertPromotionCredentialWithin(ctx, distributiondomain.PromotionCredential{
		DistributorID: distributor.ID, ProductID: command.ProductID, ProductType: productType,
		TokenDigest: digest, Status: distributiondomain.CredentialActive, CreatedAt: command.OccurredAt.UTC(), ExpiresAt: invitation.ExpiresAt.UTC(),
	})
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if credential.DistributorID != distributor.ID || credential.ProductID != command.ProductID || credential.ProductType != productType || credential.TokenDigest != digest || credential.Status != distributiondomain.CredentialActive || !credential.ExpiresAt.Equal(invitation.ExpiresAt.UTC()) || !credential.ExpiresAt.After(command.OccurredAt.UTC()) {
		return orderport.CheckoutAttributionResult{}, distributionport.ErrConflict
	}
	if created {
		payload := map[string]any{"source_kind": "referral_invitation", "campaign_id": invitation.CampaignID, "invitation_id": invitation.InvitationID, "distributor_id": distributor.ID, "credential_id": credential.ID, "enrollment_provenance": paidCampaignReferralProvenance}
		if err = s.store.AppendAuditWithin(ctx, "distribution.referral_invitation_credential_created.v1", "credential", credential.ID, "referral:campaign:"+decimal(invitation.CampaignID), payload, command.OccurredAt.UTC()); err != nil {
			return orderport.CheckoutAttributionResult{}, err
		}
		if err = s.store.AppendOutboxWithin(ctx, "distribution.referral_invitation_credential_created.v1", paidReferralCredentialOutboxKey(invitation, command.ProductID), credential.ID, payload, command.OccurredAt.UTC()); err != nil {
			return orderport.CheckoutAttributionResult{}, err
		}
	}
	policyID, err := s.store.ProductPolicyIDWithin(ctx, command.ProductID, productType)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	attribution := distributiondomain.Attribution{OrderID: command.OrderID, OrderItemLine: command.OrderItemLine, ProductCode: command.ProductCode, ProductName: command.ProductName, DistributorID: distributor.ID, PromotionCredentialID: credential.ID, QualificationEvidenceRef: qualification.EvidenceRef, QualificationState: qualification.State, PolicyVersion: policy.Version, CommissionRateBasisPoints: policy.CommissionRateBasisPoints, WaitDays: policy.WaitDays, AttributedAt: command.OccurredAt.UTC()}
	saved, created, err := s.store.InsertAttributionWithin(ctx, attribution, policyID)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if !created {
		return orderport.CheckoutAttributionResult{}, distributionport.ErrConflict
	}
	payload := map[string]any{"attribution_id": saved.ID, "order_id": command.OrderID, "policy_version": policy.Version, "source_kind": "referral_invitation", "campaign_id": invitation.CampaignID, "invitation_id": invitation.InvitationID}
	if err = s.store.AppendAuditWithin(ctx, "distribution.attribution_created.v1", "attribution", saved.ID, "order:"+decimal(command.OrderID), payload, command.OccurredAt.UTC()); err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if err = s.store.AppendOutboxWithin(ctx, "distribution.attribution_created.v1", "distribution.attribution:order:"+decimal(command.OrderID)+":item:"+decimal(int64(command.OrderItemLine)), saved.ID, payload, command.OccurredAt.UTC()); err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}

	channel := paymentdomain.ChannelMiniProgram
	if inviter.Channel == "h5_official_account" {
		channel = paymentdomain.ChannelH5Official
	}
	key := "distribution.receiver:" + decimal(distributor.ID) + ":" + inviter.AppID
	prepared, err := s.settlement.PrepareProfitSharingReceiverWithin(ctx, paymentport.ReceiverPreparation{
		CustomerID: inviter.CustomerID, IdentityID: inviter.IdentityID, AppID: inviter.AppID, AppScope: inviter.AppScope,
		Channel: channel, IdempotencyKey: key,
		SourceDigest:  effectport.Hash("distribution.receiver.v1", decimal(distributor.ID), inviter.AppID),
		PayloadDigest: effectport.Hash("distribution.receiver.payload.v1", decimal(distributor.ID), inviter.AppID, inviter.Channel),
	})
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if prepared.CustomerID != inviter.CustomerID || prepared.AppID != inviter.AppID || prepared.Reference == "" {
		return orderport.CheckoutAttributionResult{}, distributionport.ErrUnavailable
	}
	// Keep lock ordering consistent with RegistrationService: Payment first,
	// then refresh/lock the Distribution row and persist its safe projection.
	distributor, readiness, err = store.ReadDistributorByCustomerWithin(ctx, inviter.CustomerID, true)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	next := distributionport.ReceiverReadiness{Reference: prepared.Reference, AppID: inviter.AppID, Ready: prepared.Ready, CheckedAt: command.OccurredAt.UTC()}
	if !next.Ready {
		next.Reason = safeReceiverReason(prepared)
	}
	if readiness.Reference != next.Reference || readiness.AppID != next.AppID || readiness.Ready != next.Ready || readiness.Reason != next.Reason {
		updated, persisted, updateErr := store.UpdateReceiverReadinessWithin(ctx, distributor.ID, distributor.Version, next, next.CheckedAt)
		if updateErr != nil {
			return orderport.CheckoutAttributionResult{}, updateErr
		}
		distributor, readiness = updated, persisted
	}
	return orderport.CheckoutAttributionResult{Attributed: true, ProfitSharingRequired: true, PromoterCustomerID: distributor.CustomerID, PromotionCredentialRef: "distribution.credential:" + decimal(credential.ID), PolicyVersion: policy.Version, CommissionRateBasisPoints: policy.CommissionRateBasisPoints, WaitDays: policy.WaitDays}, nil
}

func (s *PromotionService) ensurePaidReferralDistributorWithin(ctx context.Context, store paidReferralPromotionStore, invitation referralport.PaidInvitationCheckout, customerID int64, at time.Time) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	for attempt := 0; attempt < 3; attempt++ {
		publicNo, err := newPublicNumber()
		if err != nil {
			return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, distributionport.ErrUnavailable
		}
		distributor, readiness, err := store.InsertDistributorWithin(ctx, customerID, publicNo, paidCampaignReferralProvenance, at)
		if err == nil {
			payload := map[string]any{"source_kind": "referral_invitation", "campaign_id": invitation.CampaignID, "invitation_id": invitation.InvitationID, "distributor_id": distributor.ID, "enrollment_provenance": paidCampaignReferralProvenance}
			if err = s.store.AppendAuditWithin(ctx, "distribution.paid_campaign_enrollment_created.v1", "distributor", distributor.ID, "referral:campaign:"+decimal(invitation.CampaignID), payload, at); err != nil {
				return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, err
			}
			if err = s.store.AppendOutboxWithin(ctx, "distribution.paid_campaign_enrollment_created.v1", paidReferralEnrollmentOutboxKey(invitation, customerID), distributor.ID, payload, at); err != nil {
				return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, err
			}
			return distributor, readiness, nil
		}
		if !errors.Is(err, distributionport.ErrConflict) {
			return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, err
		}
		// The insert store returns an expected conflict without aborting the
		// caller's transaction. Re-read the customer owner before considering a
		// generated public-number collision.
		distributor, readiness, err = store.ReadDistributorByCustomerWithin(ctx, customerID, false)
		if err == nil {
			return distributor, readiness, nil
		}
		if !errors.Is(err, distributionport.ErrNotFound) {
			return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, err
		}
	}
	return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, distributionport.ErrUnavailable
}

func validReferralInvitationToken(value string) bool {
	if len(value) != 47 || !strings.HasPrefix(value, "rfi_") {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value[4:])
	return err == nil && len(decoded) == sha256.Size
}

func paidReferralEnrollmentOutboxKey(invitation referralport.PaidInvitationCheckout, customerID int64) string {
	return "distribution.paid-campaign-enrollment:" + decimal(invitation.CampaignID) + ":" + decimal(customerID)
}

func paidReferralCredentialOutboxKey(invitation referralport.PaidInvitationCheckout, productID int64) string {
	return "distribution.paid-referral-credential:" + decimal(invitation.CampaignID) + ":" + decimal(invitation.InvitationID) + ":" + decimal(productID)
}

func (s *PromotionService) isSelfPurchaseWithin(ctx context.Context, distributorCustomerID, payerCustomerID, beneficiaryCustomerID int64) bool {
	roots, err := s.lineage.LockedCanonicalLineage(ctx, customerdomain.CustomerID(distributorCustomerID))
	if err != nil || len(roots) == 0 || len(roots) > 100 {
		// Failing closed means no attribution, never a guessed relationship.
		return true
	}
	for _, root := range roots {
		if int64(root) == payerCustomerID || int64(root) == beneficiaryCustomerID {
			return true
		}
	}
	return false
}

func distributionProductType(value productport.ProductOptionType) (distributiondomain.ProductType, bool) {
	switch value {
	case productport.ProductOptionStandard:
		return distributiondomain.ProductTypeStandard, true
	case productport.ProductOptionServicePeriod:
		return distributiondomain.ProductTypeServicePeriod, true
	default:
		return "", false
	}
}

func optionType(value distributiondomain.ProductType) productport.ProductOptionType {
	if value == distributiondomain.ProductTypeServicePeriod {
		return productport.ProductOptionServicePeriod
	}
	return productport.ProductOptionStandard
}

func validPromotionOrigin(value string) bool {
	return strings.HasPrefix(value, "https://") && strings.TrimRight(value, "/") == value && len(value) <= 500
}

func validPromotionToken(value string) bool {
	if len(value) != 47 || !strings.HasPrefix(value, "dpc_") {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(value[4:])
	return err == nil
}

var _ orderport.CheckoutAttributionCoordinator = (*PromotionService)(nil)
