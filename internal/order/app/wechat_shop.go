package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"sort"
	"strings"
	"time"
)

type shopStore interface {
	UpsertShopOrderWithin(context.Context, orderport.ShopOrder, domain.Snapshot, [32]byte, time.Time, []byte, int16) (orderport.ShopSaved, error)
	MarkShopDispatchWithin(context.Context, int64) error
	PendingShopOrdersWithin(context.Context, string, string, string) ([]string, error)
	UpsertShopBuyerWithin(context.Context, int64, orderport.ShopBuyerMaterial) error
	ReadShopBuyerCiphertexts(context.Context, []int64) ([]orderport.ShopBuyerCiphertext, error)
	PendingShopBuyerCiphertextsWithin(context.Context, string, [32]byte) ([]orderport.ShopBuyerCiphertext, error)
	AssociateShopBuyerWithin(context.Context, int64, int64, string, []int64) error
}
type ShopOrders struct {
	Store      shopStore
	Products   productport.ShopCatalog
	Consumer   orderport.ShopPaidConsumer
	Cipher     orderport.ShopContactCipher
	Identity   identityport.Resolver
	Subjects   identityport.WriteLockedVerifiedIdentitySubjectReader
	BuyerKeys  identityport.VerifiedIdentityKeyLocker
	UnionScope string
}

func (s ShopOrders) PendingShopOrdersWithin(ctx context.Context, app, product, sku string) ([]string, error) {
	return s.Store.PendingShopOrdersWithin(ctx, app, product, sku)
}
func (s ShopOrders) ImportShopOrderWithin(ctx context.Context, source orderport.ShopOrder, cutoff time.Time) error {
	if source.PaidAt.IsZero() {
		return nil
	}
	if s.Store == nil || s.Products == nil || s.Consumer == nil || s.Cipher == nil || source.AppID == "" || source.ID == "" || cutoff.IsZero() || source.PaidMinor < 0 || source.RefundedMinor < 0 || source.RefundedMinor > source.PaidMinor || len(source.Lines) == 0 || len(source.Lines) > 100 {
		return orderport.ErrConflict
	}
	// Canonical line ordering survives a Provider response array reorder. Identical
	// repeated SKU rows retain separate occurrence/line slots; quantity is unchanged.
	source.Lines = append([]orderport.ShopOrderLine(nil), source.Lines...)
	sort.SliceStable(source.Lines, func(i, j int) bool {
		a, _ := json.Marshal(source.Lines[i])
		b, _ := json.Marshal(source.Lines[j])
		return string(a) < string(b)
	})
	if source.CreatedAt.IsZero() {
		source.CreatedAt = source.PaidAt
	}
	if source.UpdatedAt.Before(source.PaidAt) {
		source.UpdatedAt = source.PaidAt
	}
	frozen := struct {
		App, ID, Transaction string
		Created, Paid        time.Time
		Amount               int64
		Lines                []orderport.ShopOrderLine
	}{source.AppID, source.ID, source.TransactionID, source.CreatedAt, source.PaidAt, source.PaidMinor, source.Lines}
	raw, e := json.Marshal(frozen)
	if e != nil {
		return e
	}
	digest := sha256.Sum256(raw)
	snap := domain.Snapshot{Provider: domain.ProviderWeChatShop, SourceSystem: "wechat-shop:" + source.AppID, SourceKey: source.ID, MerchantOrderNo: source.ID, ProviderTransactionNo: source.TransactionID, Amount: domain.Money{AmountMinor: source.PaidMinor, Currency: "CNY"}, Status: domain.StatusPaid, RecordOrigin: domain.RecordOriginExternal, Version: 2, CreatedAt: source.CreatedAt, UpdatedAt: source.PaidAt}
	if snap.UpdatedAt.Before(snap.CreatedAt) {
		snap.UpdatedAt = snap.CreatedAt
	}
	buyerID, buyerState, buyerLineage, e := s.resolveShopBuyer(ctx, source)
	if e != nil {
		return e
	}
	snap.PayerCustomerID, snap.BeneficiaryCustomerID = buyerID, buyerID
	for index, line := range source.Lines {
		if strings.TrimSpace(line.Name) == "" || line.ProductID == "" || line.SKUID == "" {
			return orderport.ErrConflict
		}
		id, found, e := s.Products.ResolveShopSKUWithin(ctx, source.AppID, line.ProductID, line.SKUID)
		if e != nil {
			return e
		}
		var productID *int64
		if found {
			v := int64(id)
			productID = &v
		}
		code := fmt.Sprintf("wxshop-%x", sha256.Sum256([]byte(source.AppID+"\x00"+line.ProductID+"\x00"+line.SKUID)))
		snap.Items = append(snap.Items, domain.ItemSnapshot{LineNo: int32(index + 1), ProductID: productID, ProductCode: code, ProductName: line.Name, UnitAmountMinor: line.UnitMinor, Quantity: line.Quantity, LineAmountMinor: line.PaidMinor})
	}
	if _, e = domain.Restore(snap); e != nil {
		return e
	}
	contact, _ := json.Marshal(struct{ Phone, Account string }{source.Phone, source.Account})
	aad := []byte("order.shop.contact.v1:" + source.AppID + ":" + source.ID)
	encrypted, version, e := s.Cipher.EncryptCommercePayload(contact, aad)
	if e != nil {
		return orderport.ErrUnavailable
	}
	saved, e := s.Store.UpsertShopOrderWithin(ctx, source, snap, digest, cutoff, encrypted, version)
	if e != nil {
		return e
	}
	if saved.Event.OrderID > 0 {
		buyer := shopBuyerDetails{Phone: source.Phone, Nickname: source.Nickname}
		if source.Buyer.Valid() {
			buyer.OpenID = source.Buyer.Reference().NormalizedValue
		}
		material := orderport.ShopBuyerMaterial{CustomerID: buyerID, State: buyerState, CanonicalLineage: buyerLineage}
		if source.BuyerUnion.Valid() {
			ref := source.BuyerUnion.Reference()
			buyer.UnionID, buyer.UnionScope = ref.NormalizedValue, ref.Scope
			material.UnionScope, material.UnionDigest = ref.Scope, shopBuyerUnionDigest(ref)
		}
		// Later refund responses may omit optional contacts. Keep previously
		// verified lookup material and display facts for this exact source.
		previous, err := s.Store.ReadShopBuyerCiphertexts(ctx, []int64{saved.Event.OrderID})
		if err != nil {
			return err
		}
		if len(previous) != 0 {
			prior, err := s.decodeShopBuyer(previous[0])
			if err != nil {
				return err
			}
			if buyer.Phone == "" {
				buyer.Phone = prior.Phone
			}
			if buyer.Nickname == "" {
				buyer.Nickname = prior.Nickname
			}
			if buyer.OpenID == "" {
				buyer.OpenID = prior.OpenID
			}
			if buyer.UnionID == "" && prior.UnionScope != "" {
				buyer.UnionID, buyer.UnionScope = prior.UnionID, prior.UnionScope
				material.UnionScope = prior.UnionScope
				material.UnionDigest = shopBuyerUnionDigest(identityReferenceForDigest(prior.UnionScope, prior.UnionID))
			}
		}
		body, err := json.Marshal(buyer)
		if err != nil {
			return orderport.ErrUnavailable
		}
		material.Ciphertext, material.KeyVersion, err = s.Cipher.EncryptCommercePayload(body, shopBuyerAAD(source.AppID, source.ID))
		if err != nil {
			return orderport.ErrUnavailable
		}
		if err = s.Store.UpsertShopBuyerWithin(ctx, saved.Event.OrderID, material); err != nil {
			return err
		}
	}
	if !saved.Eligible || saved.Event.ID == 0 {
		return nil
	}
	// Reuse frozen paid/contact facts when recovery follows Product configuration.
	contact, e = s.Cipher.DecryptCommercePayload(saved.Ciphertext, saved.KeyVersion, aad)
	if e != nil {
		return orderport.ErrUnavailable
	}
	var values struct{ Phone, Account string }
	if json.Unmarshal(contact, &values) != nil {
		return orderport.ErrUnavailable
	}
	saved.Event.ShopPhone, saved.Event.ShopAccount = values.Phone, values.Account
	for i, line := range saved.Lines {
		if i >= len(saved.Event.Order.Items) {
			return orderport.ErrConflict
		}
		id, found, e := s.Products.ResolveShopSKUWithin(ctx, source.AppID, line.ProductID, line.SKUID)
		if e != nil {
			return e
		}
		if found {
			v := int64(id)

			saved.Event.Order.Items[i].ProductID = &v
		}
	}
	accepted, e := s.Consumer.ConsumeShopPaidEventWithin(ctx, saved.Event)
	if e != nil {
		return e
	}
	if accepted {
		return s.Store.MarkShopDispatchWithin(ctx, saved.Event.OrderID)
	}
	return nil
}
