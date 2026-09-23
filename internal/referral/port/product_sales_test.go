package port

import (
	"crypto/sha256"
	"testing"
	"time"
)

func TestProductSalePaidEventAllowsOrganicSaleWithoutPromoter(t *testing.T) {
	event := ProductSalePaidEvent{PaidEventID: 8, OrderID: 10, OrderVersion: 2, ProductID: 77, ProductType: "standard_product", CampaignID: 3, BuyerCustomerID: 21, BeneficiaryCustomerID: 21, PaidAmountMinor: 9900, Currency: "CNY", ActivityContextDigest: sha256.Sum256([]byte("activity")), SourceDigest: sha256.Sum256([]byte("paid")), OccurredAt: time.Now().UTC()}
	if !event.Valid() {
		t.Fatal("organic activity sale should be valid without a promoter")
	}
}

func TestProductSaleRefundEventAllowsAdapterWithoutPaidEventID(t *testing.T) {
	event := ProductSaleRefundEvent{OrderID: 10, ProductID: 77, ProductType: "service_period", RefundedAmountMinor: 100, BuyerCustomerID: 21, BeneficiaryCustomerID: 21, ReceiptKey: "refund-receipt-1", SourceDigest: sha256.Sum256([]byte("refund")), OccurredAt: time.Now().UTC()}
	if !event.Valid() {
		t.Fatal("confirmed refund adapter may resolve paid event id from the frozen sale row")
	}
}

func TestSalesMetricRejectsUnknownConfiguration(t *testing.T) {
	if SalesMetric("relationship").Valid() {
		t.Fatal("relationship must not be a product sales metric")
	}
}
