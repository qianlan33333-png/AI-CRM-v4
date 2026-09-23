package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type productSalesStore interface {
	ReadProductSaleContextWithin(context.Context, [sha256.Size]byte, int64, string, time.Time) (referralport.ProductSaleContext, error)
	InsertProductSaleCheckoutWithin(context.Context, referralport.ProductSaleCheckoutSnapshot) error
	ReadProductSaleCheckoutWithin(context.Context, int64, bool) (referralport.ProductSaleCheckoutSnapshot, error)
	ReadProductSaleCreditWithin(context.Context, int64, int64, bool) (referraldomain.ProductSaleEvent, error)
	ReadProductSaleReversalByReceiptWithin(context.Context, int64, string) (referraldomain.ProductSaleEvent, error)
	SumProductSaleReversalsWithin(context.Context, int64, bool) (int64, int64, error)
	InsertProductSaleEventWithin(context.Context, referraldomain.ProductSaleEvent) (referraldomain.ProductSaleEvent, error)
}

var _ referralport.ProductSaleCheckoutCoordinator = (*Service)(nil)

// RecordProductSaleCheckoutAttributionWithin freezes the activity context in
// Referral-owned storage. It is deliberately called in the same Order UoW as
// the Distribution promotion attribution; no current relationship is read.
func (s *Service) RecordProductSaleCheckoutAttributionWithin(ctx context.Context, command orderport.ProductSaleCheckoutContextCommand) error {
	if s == nil || s.uow == nil || s.store == nil || command.OrderID < 1 || command.OrderVersion < 1 || command.ProductID < 1 || command.ProductCode == "" || len(command.ProductCode) > 200 || command.ProductName == "" || len(command.ProductName) > 500 || command.ProductVersion < 1 || command.BuyerCustomerID < 1 || command.BeneficiaryCustomerID < 1 || command.ReferralActivityContext == "" || command.OccurredAt.IsZero() || (command.ProductType != "standard_product" && command.ProductType != "service_period") || command.CommissionRateBasisPoints < 0 || command.CommissionRateBasisPoints > 3000 || command.WaitDays < 0 || command.WaitDays > 29 || (command.PromotionCustomerID == 0 && (command.PromotionCredentialRef != "" || command.PolicyVersion != 0 || command.CommissionRateBasisPoints != 0 || command.WaitDays != 0)) || (command.PromotionCustomerID > 0 && (command.PromotionCredentialRef == "" || command.PolicyVersion < 1)) {
		return referralport.ErrConflict
	}
	store, ok := s.store.(productSalesStore)
	if !ok {
		return referralport.ErrUnavailable
	}
	activityDigest := sha256.Sum256([]byte(command.ReferralActivityContext))
	promotionDigest := [sha256.Size]byte{}
	if command.PromotionContext != "" {
		promotionDigest = sha256.Sum256([]byte(command.PromotionContext))
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return referralport.ErrUnavailable
	}
	activity, err := store.ReadProductSaleContextWithin(ctx, activityDigest, command.ProductID, command.ProductType, command.OccurredAt.UTC())
	if errors.Is(err, referralport.ErrNotFound) {
		return nil // an invalid/expired activity is an ordinary purchase
	}
	if err != nil {
		return err
	}
	campaign, err := s.store.ReadCampaignWithin(ctx, activity.CampaignID, true)
	if err != nil {
		return err
	}
	if !campaign.AcceptingAt(command.OccurredAt.UTC()) {
		return nil
	}
	snapshot := referralport.ProductSaleCheckoutSnapshot{OrderID: command.OrderID, OrderVersion: command.OrderVersion, CampaignID: activity.CampaignID, ProductID: command.ProductID, ProductType: command.ProductType, ProductCode: command.ProductCode, ProductName: command.ProductName, ProductVersion: command.ProductVersion, PromotionCustomerID: command.PromotionCustomerID, PromotionCredentialRef: command.PromotionCredentialRef, PolicyVersion: command.PolicyVersion, CommissionRateBasisPoints: command.CommissionRateBasisPoints, WaitDays: command.WaitDays, BuyerCustomerID: command.BuyerCustomerID, BeneficiaryCustomerID: command.BeneficiaryCustomerID, ActivityContextDigest: activityDigest, PromotionContextDigest: promotionDigest, CreatedAt: command.OccurredAt.UTC()}
	if err = store.InsertProductSaleCheckoutWithin(ctx, snapshot); err != nil {
		return err
	}
	stored, err := store.ReadProductSaleCheckoutWithin(ctx, command.OrderID, true)
	if err != nil {
		return err
	}
	if stored.OrderVersion != snapshot.OrderVersion || stored.CampaignID != snapshot.CampaignID || stored.ProductID != snapshot.ProductID || stored.ProductType != snapshot.ProductType || stored.ProductCode != snapshot.ProductCode || stored.ProductName != snapshot.ProductName || stored.ProductVersion != snapshot.ProductVersion || stored.PromotionCustomerID != snapshot.PromotionCustomerID || stored.PromotionCredentialRef != snapshot.PromotionCredentialRef || stored.PolicyVersion != snapshot.PolicyVersion || stored.CommissionRateBasisPoints != snapshot.CommissionRateBasisPoints || stored.WaitDays != snapshot.WaitDays || stored.BuyerCustomerID != snapshot.BuyerCustomerID || stored.BeneficiaryCustomerID != snapshot.BeneficiaryCustomerID || stored.ActivityContextDigest != snapshot.ActivityContextDigest || stored.PromotionContextDigest != snapshot.PromotionContextDigest {
		return referralport.ErrConflict
	}
	return nil
}

var _ referralport.ProductSalePaidConsumer = (*Service)(nil)

func (s *Service) ConsumeProductSalePaidWithin(ctx context.Context, event referralport.ProductSalePaidEvent) error {
	if s == nil || s.uow == nil {
		return referralport.ErrUnavailable
	}
	return s.uow.Within(ctx, func(tx context.Context) error {
		return s.consumeProductSalePaidWithin(tx, event)
	})
}

// ConsumeProductSalePaidWithin appends one sale credit after Order confirms
// payment. A duplicate paid event is a no-op only when all frozen facts match.
func (s *Service) consumeProductSalePaidWithin(ctx context.Context, event referralport.ProductSalePaidEvent) error {
	if s == nil || s.store == nil || !event.Valid() {
		return referralport.ErrConflict
	}
	store, ok := s.store.(productSalesStore)
	if !ok {
		return referralport.ErrUnavailable
	}
	{
		checkout, err := store.ReadProductSaleCheckoutWithin(ctx, event.OrderID, true)
		if errors.Is(err, referralport.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// Checkout order_version is the immutable version when the sale was
		// created; the paid event carries the later settlement version.
		if !checkoutOrderVersionCompatible(checkout.OrderVersion, event.OrderVersion) || checkout.CampaignID != event.CampaignID || checkout.ProductID != event.ProductID || checkout.ProductType != event.ProductType || checkout.BuyerCustomerID != event.BuyerCustomerID || checkout.BeneficiaryCustomerID != event.BeneficiaryCustomerID || checkout.PromotionCustomerID != event.PromotionCustomerID || checkout.ActivityContextDigest != event.ActivityContextDigest || checkout.PromotionContextDigest != event.PromotionContextDigest {
			return referralport.ErrConflict
		}
		campaign, err := s.store.ReadCampaignWithin(ctx, checkout.CampaignID, true)
		if err != nil {
			return err
		}
		if checkout.CreatedAt.After(event.OccurredAt.UTC()) || event.OccurredAt.After(campaign.EndsAt) {
			return nil
		}
		if err = s.store.LockParticipationWithin(ctx, checkout.CampaignID, checkout.BeneficiaryCustomerID); err != nil {
			return err
		}
		participation, err := s.store.ReadParticipationWithin(ctx, checkout.CampaignID, checkout.BeneficiaryCustomerID, true)
		if errors.Is(err, referralport.ErrNotFound) {
			participation, err = s.store.InsertParticipationWithin(ctx, referraldomain.Participation{CampaignID: checkout.CampaignID, CustomerID: checkout.BeneficiaryCustomerID, TeamID: 0, State: referraldomain.ParticipationActive, JoinedAt: event.OccurredAt.UTC()})
		}
		if err != nil {
			return err
		}
		// A self-purchase never credits a promoter. The buyer still becomes an
		// activity participant, which keeps organic and self-purchase membership
		// behavior explicit and auditable.
		if checkout.PromotionCustomerID > 0 && (checkout.PromotionCustomerID == checkout.BuyerCustomerID || checkout.PromotionCustomerID == checkout.BeneficiaryCustomerID) {
			return nil
		}
		// A bound product purchase without a frozen promoter is still a valid
		// activity entry, but it is not a sale attributed to anyone.  Keep the
		// participant fact above and leave the sales ledger untouched; creating a
		// zero-promoter sale would pollute the sales leaderboard and could later
		// be mistaken for a payable distribution.
		if checkout.PromotionCustomerID == 0 {
			return nil
		}
		credit := referraldomain.ProductSaleEvent{CampaignID: checkout.CampaignID, OrderID: event.OrderID, OrderVersion: event.OrderVersion, ProductID: event.ProductID, ProductType: event.ProductType, ProductCode: checkout.ProductCode, ProductName: checkout.ProductName, ProductVersion: checkout.ProductVersion, ParticipationID: participation.ID, TeamID: participation.TeamID, PromoterCustomerID: checkout.PromotionCustomerID, PromotionCredentialRef: checkout.PromotionCredentialRef, PolicyVersion: checkout.PolicyVersion, CommissionRateBasisPoints: checkout.CommissionRateBasisPoints, WaitDays: checkout.WaitDays, BuyerCustomerID: event.BuyerCustomerID, BeneficiaryCustomerID: event.BeneficiaryCustomerID, Kind: referraldomain.ProductSaleCredit, AmountDeltaMinor: event.PaidAmountMinor, OrderCountDelta: 1, SourcePaidEventID: event.PaidEventID, Currency: event.Currency, SourceDigest: event.SourceDigest, OccurredAt: event.OccurredAt.UTC()}
		if _, err = store.InsertProductSaleEventWithin(ctx, credit); err != nil {
			if errors.Is(err, referralport.ErrConflict) {
				return s.verifyExistingProductCredit(ctx, store, credit)
			}
			return err
		}
		return s.appendEvent(ctx, "referral.product_sale.credited", "product_sale", credit.OrderID, "order", credit.OrderID, "order.paid:"+itoa(credit.SourcePaidEventID), map[string]any{"campaign_id": credit.CampaignID, "order_id": credit.OrderID, "amount_minor": credit.AmountDeltaMinor, "order_count": credit.OrderCountDelta}, event.OccurredAt.UTC())
	}
}

func (s *Service) verifyExistingProductCredit(ctx context.Context, store productSalesStore, expected referraldomain.ProductSaleEvent) error {
	actual, err := store.ReadProductSaleCreditWithin(ctx, expected.OrderID, expected.ProductID, true)
	if err != nil {
		return err
	}
	if actual.CampaignID != expected.CampaignID || actual.SourcePaidEventID != expected.SourcePaidEventID || actual.AmountDeltaMinor != expected.AmountDeltaMinor || actual.PromoterCustomerID != expected.PromoterCustomerID || actual.ParticipationID != expected.ParticipationID || actual.BuyerCustomerID != expected.BuyerCustomerID || actual.BeneficiaryCustomerID != expected.BeneficiaryCustomerID || actual.SourceDigest != expected.SourceDigest {
		return referralport.ErrConflict
	}
	return nil
}

var _ referralport.ProductSaleRefundConsumer = (*Service)(nil)

func (s *Service) ConsumeProductSaleRefundWithin(ctx context.Context, event referralport.ProductSaleRefundEvent) error {
	if s == nil || s.uow == nil {
		return referralport.ErrUnavailable
	}
	return s.uow.Within(ctx, func(tx context.Context) error {
		return s.consumeProductSaleRefundWithin(tx, event)
	})
}

// ConsumeProductSaleRefundWithin appends a capped compensation event. It is
// called only for a confirmed refund; in-flight or unknown refunds stay out of
// the sales ledger and are retried by the existing Order/Payment flow.
func (s *Service) consumeProductSaleRefundWithin(ctx context.Context, event referralport.ProductSaleRefundEvent) error {
	if s == nil || s.store == nil || !event.Valid() {
		return referralport.ErrConflict
	}
	store, ok := s.store.(productSalesStore)
	if !ok {
		return referralport.ErrUnavailable
	}
	{
		credit, err := store.ReadProductSaleCreditWithin(ctx, event.OrderID, event.ProductID, true)
		if errors.Is(err, referralport.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if (event.PaidEventID > 0 && credit.SourcePaidEventID != event.PaidEventID) || credit.BuyerCustomerID != event.BuyerCustomerID || credit.BeneficiaryCustomerID != event.BeneficiaryCustomerID {
			return referralport.ErrConflict
		}
		if _, readErr := store.ReadProductSaleReversalByReceiptWithin(ctx, credit.ID, event.ReceiptKey); readErr == nil {
			return nil
		} else if !errors.Is(readErr, referralport.ErrNotFound) {
			return readErr
		}
		already, _, err := store.SumProductSaleReversalsWithin(ctx, credit.ID, false)
		if err != nil {
			return err
		}
		remaining := credit.AmountDeltaMinor - already
		if remaining <= 0 {
			return nil
		}
		amount := int64(math.Min(float64(remaining), float64(event.RefundedAmountMinor)))
		if amount <= 0 {
			return nil
		}
		countDelta := int64(0)
		if amount == remaining {
			countDelta = -1
		}
		reversal := referraldomain.ProductSaleEvent{CampaignID: credit.CampaignID, OrderID: credit.OrderID, OrderVersion: credit.OrderVersion, ProductID: credit.ProductID, ProductType: credit.ProductType, ProductCode: credit.ProductCode, ProductName: credit.ProductName, ProductVersion: credit.ProductVersion, ParticipationID: credit.ParticipationID, TeamID: credit.TeamID, PromoterCustomerID: credit.PromoterCustomerID, PromotionCredentialRef: credit.PromotionCredentialRef, PolicyVersion: credit.PolicyVersion, CommissionRateBasisPoints: credit.CommissionRateBasisPoints, WaitDays: credit.WaitDays, BuyerCustomerID: credit.BuyerCustomerID, BeneficiaryCustomerID: credit.BeneficiaryCustomerID, Kind: referraldomain.ProductSaleReversal, AmountDeltaMinor: -amount, OrderCountDelta: countDelta, SourcePaidEventID: credit.SourcePaidEventID, ReversesSaleEventID: credit.ID, RefundReceiptKey: event.ReceiptKey, Currency: credit.Currency, SourceDigest: event.SourceDigest, OccurredAt: event.OccurredAt.UTC()}
		if _, err = store.InsertProductSaleEventWithin(ctx, reversal); err != nil {
			if errors.Is(err, referralport.ErrConflict) {
				if _, readErr := store.ReadProductSaleReversalByReceiptWithin(ctx, credit.ID, event.ReceiptKey); readErr == nil {
					return nil
				}
			}
			return err
		}
		return s.appendEvent(ctx, "referral.product_sale.reversed", "product_sale", reversal.OrderID, "order", reversal.OrderID, "refund:"+event.ReceiptKey, map[string]any{"campaign_id": reversal.CampaignID, "order_id": reversal.OrderID, "amount_minor": reversal.AmountDeltaMinor, "order_count": reversal.OrderCountDelta}, event.OccurredAt.UTC())
	}
}

func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buf := [32]byte{}
	pos := len(buf)
	for value > 0 {
		pos--
		buf[pos] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
