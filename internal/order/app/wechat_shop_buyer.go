package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

type shopBuyerDetails struct{ Phone, Account, Nickname, OpenID, UnionID, UnionScope string }

func shopBuyerAAD(app, order string) []byte {
	return []byte("order.shop.buyer.v1:" + app + ":" + order)
}
func shopBuyerUnionDigest(ref identitydomain.NormalizedReference) [32]byte {
	return sha256.Sum256([]byte("order.shop.union.v1\x00" + ref.Scope + "\x00" + ref.NormalizedValue))
}
func identityReferenceForDigest(scope, value string) identitydomain.NormalizedReference {
	return identitydomain.NormalizedReference{Scope: scope, NormalizedValue: value}
}
func shopBuyerReference(ref identitydomain.NormalizedReference) identitydomain.Reference {
	return identitydomain.Reference{Kind: ref.Kind, Scope: ref.Scope, Value: ref.NormalizedValue, Assurance: identitydomain.AssuranceVerified, Source: ref.Source}
}

// Resolve does not create or merge customers. Two already-owned identities
// must agree before a buyer can be attributed.
func (s ShopOrders) resolveShopBuyer(ctx context.Context, source orderport.ShopOrder) (*int64, string, []int64, error) {
	facts := []identitydomain.VerifiedFact{source.BuyerUnion, source.Buyer}
	if source.BuyerUnion.Valid() && s.BuyerKeys != nil {
		if err := s.BuyerKeys.LockVerifiedIdentityWithin(ctx, source.BuyerUnion); err != nil {
			return nil, "pending", nil, err
		}
	}
	type hit struct {
		fact   identitydomain.VerifiedFact
		result identityport.ResolveResult
	}
	hits := []hit{}
	for _, fact := range facts {
		if !fact.Valid() {
			continue
		}
		ref := fact.Reference()
		if ref.Kind == identitydomain.KindUnionID {
			if s.UnionScope == "" || ref.Scope != s.UnionScope {
				return nil, "conflict", nil, orderport.ErrConflict
			}
		} else if ref.Kind != identitydomain.KindOAOpenID || ref.Scope != "wechat-app:"+source.AppID {
			return nil, "conflict", nil, orderport.ErrConflict
		}
		if s.Identity == nil || s.Subjects == nil {
			continue
		}
		result, err := s.Identity.Resolve(ctx, shopBuyerReference(ref))
		if err != nil {
			return nil, "pending", nil, err
		}
		if result.Status == identityport.ResolveConflict {
			return nil, "conflict", nil, nil
		}
		if result.Status == identityport.ResolveFound {
			hits = append(hits, hit{fact, result})
		}
	}
	if len(hits) == 0 {
		return nil, "pending", nil, nil
	}
	root := hits[0].result.CustomerID
	for _, h := range hits {
		if h.result.CustomerID != root {
			return nil, "conflict", nil, nil
		}
	}
	subject, found, err := s.Subjects.WriteLockedVerifiedIdentitySubject(ctx, hits[0].result.IdentityID)
	if err != nil {
		return nil, "pending", nil, err
	}
	if !found || subject.CustomerID != root {
		return nil, "pending", nil, nil
	}
	// Recheck both handles after the shared root is pinned.
	for _, fact := range facts {
		if !fact.Valid() {
			continue
		}
		result, err := s.Identity.Resolve(ctx, shopBuyerReference(fact.Reference()))
		if err != nil {
			return nil, "pending", nil, err
		}
		if result.Status == identityport.ResolveConflict || result.Status == identityport.ResolveFound && result.CustomerID != root {
			return nil, "conflict", nil, nil
		}
	}
	id := int64(root)
	return &id, "linked", shopBuyerLineage(subject), nil
}

func shopBuyerLineage(subject identityport.VerifiedIdentitySubject) []int64 {
	ids := make([]int64, len(subject.Lineage))
	for i, id := range subject.Lineage {
		ids[i] = int64(id)
	}
	return ids
}

func (s ShopOrders) ReadShopBuyerDisplays(ctx context.Context, ids []int64) (map[int64]orderport.ShopBuyerDisplay, error) {
	rows, err := s.Store.ReadShopBuyerCiphertexts(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := map[int64]orderport.ShopBuyerDisplay{}
	for _, row := range rows {
		buyer, err := s.decodeShopBuyer(row)
		if err != nil {
			return nil, err
		}
		phone := strings.TrimPrefix(strings.TrimSpace(buyer.Phone), "+86")
		masked := ""
		if len(phone) == 11 {
			masked = phone[:3] + "****" + phone[7:]
		} else if phone != "" {
			masked = "****"
		}
		result[row.OrderID] = orderport.ShopBuyerDisplay{Nickname: buyer.Nickname, PhoneMasked: masked}
	}
	return result, nil
}

func (s ShopOrders) decodeShopBuyer(row orderport.ShopBuyerCiphertext) (shopBuyerDetails, error) {
	var buyer shopBuyerDetails
	if s.Cipher == nil {
		return buyer, orderport.ErrUnavailable
	}
	plain, err := s.Cipher.DecryptCommercePayload(row.Ciphertext, row.KeyVersion, shopBuyerAAD(row.AppID, row.SourceOrderID))
	if err != nil || json.Unmarshal(plain, &buyer) != nil {
		return buyer, orderport.ErrUnavailable
	}
	return buyer, nil
}

// This observer is called after Identity accepts a verified UnionID in the
// same UoW. It updates only current attribution and never consumes a paid event.
func (s ShopOrders) ObserveShopBuyerIdentityWithin(ctx context.Context, customer int64, fact identitydomain.VerifiedFact) error {
	if !fact.Valid() || fact.Reference().Kind != identitydomain.KindUnionID || s.UnionScope == "" || fact.Reference().Scope != s.UnionScope {
		return nil
	}
	if s.Identity == nil || s.Subjects == nil || customer < 1 {
		return orderport.ErrUnavailable
	}
	ref := fact.Reference()
	result, err := s.Identity.Resolve(ctx, shopBuyerReference(ref))
	if err != nil {
		return err
	}
	if result.Status != identityport.ResolveFound || int64(result.CustomerID) != customer {
		return orderport.ErrConflict
	}
	subject, found, err := s.Subjects.WriteLockedVerifiedIdentitySubject(ctx, result.IdentityID)
	if err != nil {
		return err
	}
	if !found || int64(subject.CustomerID) != customer {
		return orderport.ErrConflict
	}
	rows, err := s.Store.PendingShopBuyerCiphertextsWithin(ctx, ref.Scope, shopBuyerUnionDigest(ref))
	if err != nil {
		return err
	}
	for _, row := range rows {
		buyer, err := s.decodeShopBuyer(row)
		if err != nil {
			return err
		}
		if buyer.UnionScope != ref.Scope || buyer.UnionID != ref.NormalizedValue {
			return orderport.ErrConflict
		}
		state := "linked"
		if buyer.OpenID != "" {
			peer, err := s.Identity.Resolve(ctx, identitydomain.Reference{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:" + row.AppID, Value: buyer.OpenID, Assurance: identitydomain.AssuranceVerified, Source: "wechat-shop.order-api"})
			if err != nil {
				return err
			}
			if peer.Status == identityport.ResolveConflict || peer.Status == identityport.ResolveFound && int64(peer.CustomerID) != customer {
				state = "conflict"
			}
		}
		if err = s.Store.AssociateShopBuyerWithin(ctx, row.OrderID, customer, state, shopBuyerLineage(subject)); err != nil {
			return err
		}
	}
	return nil
}
