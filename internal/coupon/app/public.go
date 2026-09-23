package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type PublicStore interface {
	GetPublicCouponBySlug(context.Context, string) (couponport.PublicCoupon, error)
	PublicCouponClaimState(context.Context, int64, couponport.ID) (couponport.PublicCouponClaimState, error)
	ListAvailableCouponClaims(context.Context, int64, string, time.Time) ([]couponport.CustomerCoupon, error)
	EnsurePublicCouponSlug(context.Context, couponport.ID, string, int64, time.Time) (couponport.PublicCoupon, bool, error)
}

// PublicCouponService is a Coupon-owned public read/share application. The
// host provides only a canonical holder Customer from Payment's opaque session.
type PublicCouponService struct {
	uow   platformport.UnitOfWork
	store PublicStore
	now   func() time.Time
}

func NewPublicCouponService(uow platformport.UnitOfWork, store PublicStore) (*PublicCouponService, error) {
	if uow == nil || store == nil {
		return nil, errors.New("coupon public dependencies are required")
	}
	return &PublicCouponService{uow: uow, store: store, now: time.Now}, nil
}

func (s *PublicCouponService) GetPublicCoupon(ctx context.Context, slug string) (couponport.PublicCoupon, error) {
	slug = strings.TrimSpace(slug)
	if s == nil || s.uow == nil || s.store == nil || !validPublicSlug(slug) {
		return couponport.PublicCoupon{}, ErrNotFound
	}
	var result couponport.PublicCoupon
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result, err = s.store.GetPublicCouponBySlug(tx, slug)
		return err
	})
	if err != nil {
		return couponport.PublicCoupon{}, classifyCheckout(err)
	}
	return result, nil
}

func (s *PublicCouponService) PublicClaimState(ctx context.Context, holderCustomerID int64, couponID couponport.ID) (couponport.PublicCouponClaimState, error) {
	if s == nil || s.uow == nil || s.store == nil || holderCustomerID < 1 || couponID < 1 {
		return couponport.PublicCouponClaimState{}, ErrInvalidCoupon
	}
	var result couponport.PublicCouponClaimState
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var err error
		result, err = s.store.PublicCouponClaimState(txctx, holderCustomerID, couponID)
		return err
	})
	if err != nil {
		return couponport.PublicCouponClaimState{}, classify(err)
	}
	return result, nil
}

func (s *PublicCouponService) ListAvailableClaims(ctx context.Context, holderCustomerID int64, targetRef string, at time.Time) ([]couponport.CustomerCoupon, error) {
	if s == nil || s.uow == nil || s.store == nil || holderCustomerID < 1 || !validTargetRef(targetRef) || at.IsZero() {
		return nil, ErrInvalidCoupon
	}
	var result []couponport.CustomerCoupon
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result, err = s.store.ListAvailableCouponClaims(tx, holderCustomerID, targetRef, at.UTC())
		return err
	})
	if err != nil {
		return nil, classifyCheckout(err)
	}
	if result == nil {
		return []couponport.CustomerCoupon{}, nil
	}
	return result, nil
}

func (s *PublicCouponService) EnsurePublicShare(ctx context.Context, couponID couponport.ID, actorID int64) (couponport.PublicCouponShare, error) {
	if s == nil || s.uow == nil || s.store == nil || couponID < 1 || actorID < 1 {
		return couponport.PublicCouponShare{}, ErrInvalidCoupon
	}
	var coupon couponport.PublicCoupon
	err := s.uow.Within(ctx, func(tx context.Context) error {
		for attempt := 0; attempt < 4; attempt++ {
			slug, slugErr := newPublicSlug()
			if slugErr != nil {
				return ErrUnavailable
			}
			var created bool
			coupon, created, slugErr = s.store.EnsurePublicCouponSlug(tx, couponID, slug, actorID, s.now().UTC())
			if slugErr == nil {
				_ = created
				return nil
			}
			if !errors.Is(slugErr, ErrConflict) {
				return slugErr
			}
		}
		return ErrUnavailable
	})
	if err != nil {
		return couponport.PublicCouponShare{}, classifyCheckout(err)
	}
	if coupon.ID != couponID || !validPublicSlug(coupon.PublicSlug) || coupon.Status != "published" {
		return couponport.PublicCouponShare{}, ErrConflict
	}
	return couponport.PublicCouponShare{CouponID: coupon.ID, PublicSlug: coupon.PublicSlug, URL: "/c/" + coupon.PublicSlug}, nil
}

func newPublicSlug() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "cp-" + hex.EncodeToString(raw), nil
}

func validPublicSlug(value string) bool {
	if len(value) < 6 || len(value) > 120 || value != strings.TrimSpace(value) {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// validTargetRef is intentionally local to Coupon's public read model. The
// browser can choose a product target, but it never supplies a Customer or a
// price. Keep the target grammar aligned with the Coupon-owned targets table
// and reject aliases such as a bare numeric product ID.
func validTargetRef(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || (parts[0] != "standard_product" && parts[0] != "service_period") || parts[1] == "" {
		return false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == parts[1]
}

var _ couponport.PublicCouponApplication = (*PublicCouponService)(nil)
