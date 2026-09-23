package coupon

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	couponhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/http"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// ModuleRegistration is Coupon's stable composition contract. It owns only
// rule-management HTTP/UI/readiness; it registers no provider or worker.
type ModuleRegistration struct{}
type HTTPBindings struct{ Coupons http.Handler }

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

// Bind owns the frozen browser-compatible rule routes. Product facts are
// injected by the composition root through Product's read-only port.
func (m *ModuleRegistration) Bind(rules couponport.RuleApplication, products productport.ProductOptionReader, security couponhttp.RequestSecurity) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("coupon module is required")
	}
	h, err := couponhttp.NewHandler(rules, products, security)
	if err != nil {
		return HTTPBindings{}, err
	}
	return HTTPBindings{Coupons: h}, nil
}

// BindWithClaims is the additive couponData journey. It keeps the original
// rule-only Bind stable for staged composition while giving the root a single
// explicit switch to mount claims once the new migration is present.
func (m *ModuleRegistration) BindWithClaims(rules couponport.RuleApplication, products productport.ProductOptionReader, claims couponport.CouponClaimAdminReader, security couponhttp.RequestSecurity) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("coupon module is required")
	}
	h, err := couponhttp.NewHandlerWithClaims(rules, products, claims, security)
	if err != nil {
		return HTTPBindings{}, err
	}
	return HTTPBindings{Coupons: h}, nil
}

// BindWithClaimsAndPublic wires the frozen couponData share action to the
// Coupon-owned immutable-slug application without broadening any Product,
// Payment, or Identity dependency into the admin adapter.
func (m *ModuleRegistration) BindWithClaimsAndPublic(rules couponport.RuleApplication, products productport.ProductOptionReader, claims couponport.CouponClaimAdminReader, public couponport.PublicCouponApplication, security couponhttp.RequestSecurity) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("coupon module is required")
	}
	h, err := couponhttp.NewHandlerWithClaimsAndPublic(rules, products, claims, public, security)
	if err != nil {
		return HTTPBindings{}, err
	}
	return HTTPBindings{Coupons: h}, nil
}

func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("coupon module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['coupon_rules','coupon_rule_targets','coupon_operation_receipts','coupon_audit_events','coupon_outbox','coupon_customer_claims','coupon_claim_operation_receipts','coupon_order_redemptions','coupon_redemption_operation_receipts','coupon_claim_audit_events','coupon_claim_outbox']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("coupon schema is not ready")
	}
	return nil
}
