package app

import (
	"context"
	"errors"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type CustomerCouponStore interface {
	ListCustomerCoupons(context.Context, int64, int32) (couponport.CustomerCouponPage, error)
	ImportHistoricalCustomerCoupon(context.Context, couponport.HistoricalCustomerCoupon) (couponport.CustomerCoupon, bool, error)
}

type CouponClaimAdminStore interface {
	ListCouponClaims(context.Context, couponport.ID, int32, int32) (couponport.AdminCouponClaimPage, error)
}

type CustomerCouponApplication struct {
	uow   platformport.UnitOfWork
	store CustomerCouponStore
}

func NewCustomerCouponApplication(uow platformport.UnitOfWork, store CustomerCouponStore) (*CustomerCouponApplication, error) {
	if uow == nil || store == nil {
		return nil, errors.New("coupon customer projection dependencies are required")
	}
	return &CustomerCouponApplication{uow: uow, store: store}, nil
}

func (s *CustomerCouponApplication) ListCustomerCoupons(ctx context.Context, customerID int64, limit int32) (couponport.CustomerCouponPage, error) {
	if customerID < 1 || limit < 1 || limit > 100 {
		return couponport.CustomerCouponPage{}, ErrInvalidCoupon
	}
	var page couponport.CustomerCouponPage
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var err error
		page, err = s.store.ListCustomerCoupons(txctx, customerID, limit)
		return err
	})
	return page, err
}

func (s *CustomerCouponApplication) ImportHistoricalCustomerCoupon(ctx context.Context, input couponport.HistoricalCustomerCoupon) (couponport.CustomerCoupon, bool, error) {
	if input.SourceSystem == "" || input.SourceKey == "" || input.CustomerID < 1 || input.CouponID < 1 || input.SourceDigest == ([32]byte{}) || input.ClaimedAt.IsZero() {
		return couponport.CustomerCoupon{}, false, ErrInvalidCoupon
	}
	var item couponport.CustomerCoupon
	var created bool
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var err error
		item, created, err = s.store.ImportHistoricalCustomerCoupon(txctx, input)
		return err
	})
	return item, created, err
}

var _ couponport.CustomerCouponReader = (*CustomerCouponApplication)(nil)
var _ couponport.HistoricalCustomerCouponImporter = (*CustomerCouponApplication)(nil)

// CouponClaimAdminApplication owns bounded admin reads for the legacy coupon
// data page. It does not resolve or expose external identities.
type CouponClaimAdminApplication struct {
	uow   platformport.UnitOfWork
	store CouponClaimAdminStore
}

func NewCouponClaimAdminApplication(uow platformport.UnitOfWork, store CouponClaimAdminStore) (*CouponClaimAdminApplication, error) {
	if uow == nil || store == nil {
		return nil, errors.New("coupon claim admin dependencies are required")
	}
	return &CouponClaimAdminApplication{uow: uow, store: store}, nil
}

func (s *CouponClaimAdminApplication) ListCouponClaims(ctx context.Context, couponID couponport.ID, limit, offset int32) (couponport.AdminCouponClaimPage, error) {
	if s == nil || s.uow == nil || s.store == nil || couponID < 1 || limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		return couponport.AdminCouponClaimPage{}, ErrInvalidCoupon
	}
	var page couponport.AdminCouponClaimPage
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var err error
		page, err = s.store.ListCouponClaims(txctx, couponID, limit, offset)
		return err
	})
	if err != nil {
		return couponport.AdminCouponClaimPage{}, err
	}
	if page.Items == nil {
		page.Items = []couponport.AdminCouponClaim{}
	}
	if page.Total < 0 || page.Limit != limit || page.Offset != offset || len(page.Items) > int(limit) {
		return couponport.AdminCouponClaimPage{}, ErrUnavailable
	}
	return page, nil
}

var _ couponport.CouponClaimAdminReader = (*CouponClaimAdminApplication)(nil)
