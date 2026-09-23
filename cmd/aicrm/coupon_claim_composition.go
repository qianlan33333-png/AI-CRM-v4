package main

import (
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// HTTP requests have no repository transaction until the application opens it.
func composeCouponClaimAdmin(uow platformport.UnitOfWork, store couponapp.CouponClaimAdminStore) (couponport.CouponClaimAdminReader, error) {
	return couponapp.NewCouponClaimAdminApplication(uow, store)
}
