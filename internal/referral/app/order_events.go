package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"time"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

// ConsumePaidEventWithin is the composition adapter for Order's existing
// durable PaidEvent. It resolves the Referral-owned checkout snapshot in the
// caller's transaction, then invokes the product sale consumer without a
// nested Unit of Work.
func (s *Service) ConsumePaidEventWithin(ctx context.Context, event orderport.PaidEvent) error {
	if s == nil || s.store == nil || !event.Valid() {
		return referralport.ErrConflict
	}
	store, ok := s.store.(productSalesStore)
	if !ok {
		return referralport.ErrUnavailable
	}
	if event.Order.PayerCustomerID == nil || event.Order.BeneficiaryCustomerID == nil || *event.Order.PayerCustomerID < 1 || *event.Order.BeneficiaryCustomerID < 1 {
		return referralport.ErrConflict
	}
	// An explicit activity entry can still admit its buyer. Sales attribution is
	// independent and is resolved only from Distribution's frozen order fact.
	checkout, err := store.ReadProductSaleCheckoutWithin(ctx, event.OrderID, true)
	if err == nil {
		if !checkoutOrderVersionCompatible(checkout.OrderVersion, event.OrderVersion) || checkout.ProductID != event.CheckoutProductID || checkout.ProductType != event.CheckoutProductType || checkout.ActivityContextDigest != event.ReferralActivityContextDigest || checkout.PromotionContextDigest != event.PromotionContextDigest {
			return referralport.ErrConflict
		}
		if err = s.ensureCheckoutBuyerParticipationWithin(ctx, checkout, event); err != nil {
			return err
		}
	} else if !errors.Is(err, referralport.ErrNotFound) {
		return err
	}
	return s.recordAttributedSaleWithin(ctx, store, event)
}

func (s *Service) ensureCheckoutBuyerParticipationWithin(ctx context.Context, checkout referralport.ProductSaleCheckoutSnapshot, event orderport.PaidEvent) error {
	if checkout.CreatedAt.After(event.OccurredAt) {
		return referralport.ErrConflict
	}
	campaign, err := s.store.ReadCampaignWithin(ctx, checkout.CampaignID, false)
	if err != nil {
		return err
	}
	if event.OccurredAt.After(campaign.EndsAt) {
		return nil
	}
	buyer := *event.Order.BeneficiaryCustomerID
	if err = s.store.LockParticipationWithin(ctx, checkout.CampaignID, buyer); err != nil {
		return err
	}
	_, err = s.store.ReadParticipationWithin(ctx, checkout.CampaignID, buyer, true)
	if errors.Is(err, referralport.ErrNotFound) {
		_, err = s.store.InsertParticipationWithin(ctx, referraldomain.Participation{
			CampaignID: checkout.CampaignID, CustomerID: buyer,
			State: referraldomain.ParticipationActive, JoinedAt: event.OccurredAt.UTC(),
		})
	}
	return err
}

func (s *Service) recordAttributedSaleWithin(ctx context.Context, store productSalesStore, event orderport.PaidEvent) error {
	credit, eligible, existing, err := s.prepareAttributedSaleWithin(ctx, store, event)
	if err != nil || !eligible || existing {
		return err
	}
	if _, err = store.InsertProductSaleEventWithin(ctx, credit); err != nil {
		if errors.Is(err, referralport.ErrConflict) {
			stored, readErr := store.ReadProductSaleCreditWithin(ctx, credit.OrderID, credit.ProductID, false)
			if readErr != nil {
				return readErr
			}
			return verifyFrozenSaleCredit(stored, credit.CampaignID, credit.SourcePaidEventID, credit.PromoterCustomerID, credit.AmountDeltaMinor, credit.SourceDigest)
		}
		return err
	}
	return s.appendEvent(ctx, "referral.product_sale.credited", "product_sale", credit.OrderID, "order", credit.OrderID, "order.paid:"+itoa(credit.SourcePaidEventID), map[string]any{"campaign_id": credit.CampaignID, "order_id": credit.OrderID, "amount_minor": credit.AmountDeltaMinor, "order_count": credit.OrderCountDelta}, event.OccurredAt.UTC())
}

// BackfillSalePreview describes one read-only historical decision. The same
// preparation method is used for live scoring and apply, so previews cannot
// silently drift from the write predicate.
type BackfillSalePreview struct {
	Eligible           bool
	AlreadyCredited    bool
	CampaignID         int64
	PromoterCustomerID int64
	GrossAmountMinor   int64
}

func (s *Service) PreviewAttributedSaleWithin(ctx context.Context, event orderport.PaidEvent) (BackfillSalePreview, error) {
	if s == nil || !event.Valid() {
		return BackfillSalePreview{}, referralport.ErrConflict
	}
	store, ok := s.store.(productSalesStore)
	if !ok {
		return BackfillSalePreview{}, referralport.ErrUnavailable
	}
	credit, eligible, existing, err := s.prepareAttributedSaleWithin(ctx, store, event)
	if err != nil || !eligible {
		return BackfillSalePreview{}, err
	}
	return BackfillSalePreview{Eligible: true, AlreadyCredited: existing, CampaignID: credit.CampaignID, PromoterCustomerID: credit.PromoterCustomerID, GrossAmountMinor: credit.AmountDeltaMinor}, nil
}

// BackfillAttributedSaleWithin only appends Referral sales evidence. It never
// replays Order settlement, Distribution commission, or buyer participation.
func (s *Service) BackfillAttributedSaleWithin(ctx context.Context, event orderport.PaidEvent) error {
	if s == nil || !event.Valid() {
		return referralport.ErrConflict
	}
	store, ok := s.store.(productSalesStore)
	if !ok {
		return referralport.ErrUnavailable
	}
	return s.recordAttributedSaleWithin(ctx, store, event)
}

func (s *Service) prepareAttributedSaleWithin(ctx context.Context, store productSalesStore, event orderport.PaidEvent) (referraldomain.ProductSaleEvent, bool, bool, error) {
	if s.attributions == nil || !event.Valid() {
		return referraldomain.ProductSaleEvent{}, false, false, referralport.ErrUnavailable
	}
	if event.Order.PayerCustomerID == nil || event.Order.BeneficiaryCustomerID == nil || *event.Order.PayerCustomerID < 1 || *event.Order.BeneficiaryCustomerID < 1 {
		return referraldomain.ProductSaleEvent{}, false, false, referralport.ErrConflict
	}
	attr, err := s.attributions.ReadFrozenOrderAttributionWithin(ctx, event.OrderID, 1)
	if errors.Is(err, distributionport.ErrNotFound) {
		return referraldomain.ProductSaleEvent{}, false, false, nil
	}
	if err != nil {
		return referraldomain.ProductSaleEvent{}, false, false, err
	}
	if attr.OrderID != event.OrderID || attr.OrderItemLine != 1 || attr.ProductID != event.CheckoutProductID || string(attr.ProductType) != event.CheckoutProductType || attr.PromoterCustomerID < 1 || event.OccurredAt.Before(attr.AttributedAt) {
		return referraldomain.ProductSaleEvent{}, false, false, referralport.ErrConflict
	}
	if attr.PromoterCustomerID == *event.Order.PayerCustomerID || attr.PromoterCustomerID == *event.Order.BeneficiaryCustomerID {
		return referraldomain.ProductSaleEvent{}, false, false, nil
	}
	campaign, found, err := s.saleCampaignAt(ctx, attr.ProductID, string(attr.ProductType), event.OccurredAt)
	if err != nil || !found {
		return referraldomain.ProductSaleEvent{}, false, false, err
	}
	var item *orderdomain.ItemSnapshot
	for i := range event.Order.Items {
		if event.Order.Items[i].LineNo == attr.OrderItemLine {
			item = &event.Order.Items[i]
			break
		}
	}
	if item == nil || item.ProductID == nil || *item.ProductID != attr.ProductID || item.ProductVersion == nil || *item.ProductVersion < 1 || item.LineAmountMinor < 1 || item.LineAmountMinor > event.Order.Amount.AmountMinor {
		return referraldomain.ProductSaleEvent{}, false, false, referralport.ErrConflict
	}
	var alreadyCredited bool
	if existing, readErr := store.ReadProductSaleCreditWithin(ctx, event.OrderID, attr.ProductID, false); readErr == nil {
		if err = verifyFrozenSaleCredit(existing, campaign.ID, event.ID, attr.PromoterCustomerID, item.LineAmountMinor, event.SourceDigest); err != nil {
			return referraldomain.ProductSaleEvent{}, false, false, err
		}
		alreadyCredited = true
		return existing, true, alreadyCredited, nil
	} else if !errors.Is(readErr, referralport.ErrNotFound) {
		return referraldomain.ProductSaleEvent{}, false, false, readErr
	}
	var participationID, teamID int64
	participation, participationErr := s.store.ReadParticipationWithin(ctx, campaign.ID, attr.PromoterCustomerID, false)
	if participationErr == nil {
		activeAtPayment, stateErr := s.participationActiveAt(ctx, participation, event.OccurredAt)
		if stateErr != nil {
			return referraldomain.ProductSaleEvent{}, false, false, stateErr
		}
		if activeAtPayment {
			participationID = participation.ID
			if campaign.Config().TeamMode == referraldomain.TeamModeTeam {
				teamID = participation.TeamID
			}
		}
	} else if participationErr != nil && !errors.Is(participationErr, referralport.ErrNotFound) {
		return referraldomain.ProductSaleEvent{}, false, false, participationErr
	}
	credit := referraldomain.ProductSaleEvent{
		CampaignID: campaign.ID, OrderID: event.OrderID, OrderVersion: event.OrderVersion,
		ProductID: attr.ProductID, ProductType: string(attr.ProductType),
		ProductCode: item.ProductCode, ProductName: item.ProductName, ProductVersion: *item.ProductVersion,
		ParticipationID: participationID, TeamID: teamID, PromoterCustomerID: attr.PromoterCustomerID,
		PromotionCredentialRef: "distribution.credential:" + strconv.FormatInt(attr.CredentialID, 10),
		PolicyVersion:          attr.PolicyVersion, CommissionRateBasisPoints: attr.CommissionRateBasisPoints, WaitDays: attr.WaitDays,
		BuyerCustomerID: *event.Order.PayerCustomerID, BeneficiaryCustomerID: *event.Order.BeneficiaryCustomerID,
		Kind: referraldomain.ProductSaleCredit, AmountDeltaMinor: item.LineAmountMinor, OrderCountDelta: 1,
		SourcePaidEventID: event.ID, Currency: event.Order.Amount.Currency,
		SourceDigest: event.SourceDigest, OccurredAt: event.OccurredAt.UTC(),
	}
	return credit, true, alreadyCredited, nil
}

func verifyFrozenSaleCredit(existing referraldomain.ProductSaleEvent, campaignID, paidEventID, promoterID, amount int64, source [sha256.Size]byte) error {
	if existing.CampaignID != campaignID || existing.SourcePaidEventID != paidEventID ||
		existing.PromoterCustomerID != promoterID || existing.AmountDeltaMinor != amount ||
		existing.SourceDigest != source {
		return referralport.ErrConflict
	}
	return nil
}

func (s *Service) participationActiveAt(ctx context.Context, participation referraldomain.Participation, paidAt time.Time) (bool, error) {
	if participation.JoinedAt.After(paidAt) {
		return false, nil
	}
	if participation.State == referraldomain.ParticipationActive {
		return true, nil
	}
	if participation.State != referraldomain.ParticipationReversed || s.timeline == nil {
		return false, referralport.ErrUnavailable
	}
	events, err := s.timeline.ResourceTimelineWithin(ctx, "participation", strconv.FormatInt(participation.ID, 10), time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		return false, err
	}
	for _, event := range events {
		if event.Action == "referral.invitation.reversed" {
			return event.OccurredAt.After(paidAt), nil
		}
	}
	return false, referralport.ErrUnavailable
}

func checkoutOrderVersionCompatible(checkoutVersion, paidEventVersion int64) bool {
	return checkoutVersion >= 1 && paidEventVersion >= checkoutVersion
}

// ConsumeRefundSettlementWithin adapts Order's confirmed cumulative refund
// delta. Payment's receipt key is retained as the immutable reversal key.
func (s *Service) ConsumeRefundSettlementWithin(ctx context.Context, event orderport.RefundSettlementEvent) error {
	if s == nil || s.store == nil || !event.Valid() {
		return referralport.ErrConflict
	}
	if event.CheckoutProductID < 1 || event.CheckoutProductType == "" {
		return nil
	}
	var buyer, beneficiary int64
	if event.Order.PayerCustomerID == nil || event.Order.BeneficiaryCustomerID == nil {
		return referralport.ErrConflict
	}
	buyer, beneficiary = *event.Order.PayerCustomerID, *event.Order.BeneficiaryCustomerID
	source := sha256.Sum256([]byte("order.refund.v1:" + event.ReceiptKey))
	return s.consumeProductSaleRefundWithin(ctx, referralport.ProductSaleRefundEvent{OrderID: event.Order.ID, ProductID: event.CheckoutProductID, ProductType: event.CheckoutProductType, RefundedAmountMinor: event.RefundedDelta, BuyerCustomerID: buyer, BeneficiaryCustomerID: beneficiary, ReceiptKey: event.ReceiptKey, SourceDigest: source, OccurredAt: event.OccurredAt})
}

var _ orderport.PaidEventConsumer = (*Service)(nil)
var _ orderport.RefundSettlementConsumer = (*Service)(nil)
