package port

import (
	"context"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	"time"
)

type ShopOrderLine struct {
	ProductID, SKUID, Name string
	UnitMinor, PaidMinor   int64
	Quantity               int32
}
type ShopOrder struct {
	AppID, ID, TransactionID     string
	CreatedAt, UpdatedAt, PaidAt time.Time
	PaidMinor, RefundedMinor     int64
	Lines                        []ShopOrderLine
	Phone, Account               string // transient Provider facts; encrypted before persistence
	Buyer                        identitydomain.VerifiedFact
}

// Never allow Provider data or contact values into fmt-based diagnostics.
func (ShopOrder) String() string   { return "wechat-shop-order[redacted]" }
func (ShopOrder) GoString() string { return "wechat-shop-order[redacted]" }

type ShopContactCipher interface {
	EncryptCommercePayload([]byte, []byte) ([]byte, int16, error)
	DecryptCommercePayload([]byte, int16, []byte) ([]byte, error)
}
type ShopPaidConsumer interface {
	ConsumeShopPaidEventWithin(context.Context, PaidEvent) (bool, error)
}
type ShopOrderImporter interface {
	ImportShopOrderWithin(context.Context, ShopOrder, time.Time) error
}
type ShopPendingReader interface {
	PendingShopOrdersWithin(context.Context, string, string, string) ([]string, error)
}
type ShopSaved struct {
	Event      PaidEvent
	Eligible   bool
	Ciphertext []byte
	KeyVersion int16
	Lines      []ShopOrderLine
}
