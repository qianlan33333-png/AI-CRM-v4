package domain

import (
	"crypto/sha256"
	"time"
)

type ProductSaleEventKind string

const (
	ProductSaleCredit   ProductSaleEventKind = "credit"
	ProductSaleReversal ProductSaleEventKind = "reversal"
)

func (k ProductSaleEventKind) Valid() bool { return k == ProductSaleCredit || k == ProductSaleReversal }

// ProductSaleEvent is immutable evidence used by both amount and order-count
// projections. A reversal retains the original credit and its receipt key.
type ProductSaleEvent struct {
	ID, CampaignID, OrderID, OrderVersion, ProductID int64
	ProductType                                      string
	ProductCode, ProductName                         string
	ProductVersion                                   int64
	ParticipationID, TeamID, PromoterCustomerID      int64
	PromotionCredentialRef                           string
	PolicyVersion                                    int64
	CommissionRateBasisPoints                        int32
	WaitDays                                         int32
	BuyerCustomerID, BeneficiaryCustomerID           int64
	Kind                                             ProductSaleEventKind
	AmountDeltaMinor, OrderCountDelta                int64
	SourcePaidEventID, ReversesSaleEventID           int64
	RefundReceiptKey, Currency                       string
	SourceDigest                                     [sha256.Size]byte
	OccurredAt                                       time.Time
}

func (e ProductSaleEvent) Valid() bool {
	if e.ID < 1 || e.CampaignID < 1 || e.OrderID < 1 || e.OrderVersion < 1 || e.ProductID < 1 ||
		(e.ProductType != "standard_product" && e.ProductType != "service_period") ||
		e.ProductCode == "" || len(e.ProductCode) > 200 || e.ProductName == "" || len(e.ProductName) > 500 || e.ProductVersion < 1 ||
		e.ParticipationID < 0 || e.BuyerCustomerID < 1 || e.BeneficiaryCustomerID < 1 || !e.Kind.Valid() ||
		e.SourcePaidEventID < 1 || e.Currency != "CNY" || e.SourceDigest == ([sha256.Size]byte{}) || e.OccurredAt.IsZero() {
		return false
	}
	if e.Kind == ProductSaleCredit {
		return e.AmountDeltaMinor > 0 && e.OrderCountDelta == 1 && e.ReversesSaleEventID == 0 && e.RefundReceiptKey == ""
	}
	return e.AmountDeltaMinor < 0 && (e.OrderCountDelta == 0 || e.OrderCountDelta == -1) && e.ReversesSaleEventID > 0 && e.RefundReceiptKey != ""
}
