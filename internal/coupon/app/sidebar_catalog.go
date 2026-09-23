package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// SidebarClaimableRecord stays inside Coupon's application/store boundary. It
// carries the durable claim count required to render a scoped directory but
// never exposes claim rows to the sidebar.
type SidebarClaimableRecord struct {
	Coupon     couponport.Coupon
	PublicSlug string
	ClaimCount int64
}

type SidebarClaimableRecordPage struct {
	Items  []SidebarClaimableRecord
	Total  int64
	Limit  int32
	Offset int32
}

// SidebarClaimableCatalogStore is implemented by Coupon's PostgreSQL store.
// It is deliberately separate from CustomerCouponStore: a directory of rule
// definitions cannot be substituted with a customer's already-issued claims.
type SidebarClaimableCatalogStore interface {
	ListSidebarClaimable(context.Context, int64, int32, int32) (SidebarClaimableRecordPage, error)
	ReadSidebarClaimable(context.Context, int64, couponport.ID) (SidebarClaimableRecord, error)
}

// SidebarClaimableCatalogApplication assembles Coupon's rule/claim facts with
// the stable Product target reader. Product targets are read after the Coupon
// transaction closes: the Product reader owns its own UoW and must not be
// invoked inside Coupon's transaction.
type SidebarClaimableCatalogApplication struct {
	uow      platformport.UnitOfWork
	store    SidebarClaimableCatalogStore
	products couponport.ProductReader
	now      func() time.Time
}

func NewSidebarClaimableCatalog(uow platformport.UnitOfWork, store SidebarClaimableCatalogStore, products couponport.ProductReader) (*SidebarClaimableCatalogApplication, error) {
	if uow == nil || store == nil || products == nil {
		return nil, errors.New("coupon sidebar catalog dependencies are required")
	}
	return &SidebarClaimableCatalogApplication{uow: uow, store: store, products: products, now: time.Now}, nil
}

func (s *SidebarClaimableCatalogApplication) ListSidebarClaimable(ctx context.Context, customerID int64, query couponport.SidebarClaimableQuery) (couponport.SidebarClaimablePage, error) {
	if s == nil || s.uow == nil || s.store == nil || s.products == nil || customerID < 1 {
		return couponport.SidebarClaimablePage{}, ErrInvalidCoupon
	}
	if query.Limit == 0 {
		query.Limit = couponport.SidebarClaimableDefaultLimit
	}
	if query.Limit < 1 || query.Limit > couponport.SidebarClaimableMaximumLimit || query.Offset < 0 || query.Offset > couponport.SidebarClaimableMaximumOffset {
		return couponport.SidebarClaimablePage{}, ErrInvalidCoupon
	}

	var source SidebarClaimableRecordPage
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var readErr error
		source, readErr = s.store.ListSidebarClaimable(txctx, customerID, query.Limit, query.Offset)
		return readErr
	})
	if err != nil {
		return couponport.SidebarClaimablePage{}, classify(err)
	}
	if source.Total < 0 || source.Limit != query.Limit || source.Offset != query.Offset || len(source.Items) > int(query.Limit) {
		return couponport.SidebarClaimablePage{}, ErrUnavailable
	}

	page := couponport.SidebarClaimablePage{Items: make([]couponport.SidebarClaimableItem, 0, len(source.Items)), Total: source.Total, Limit: source.Limit, Offset: source.Offset}
	at := s.now().UTC()
	for _, record := range source.Items {
		item, itemErr := s.projectSidebarClaimable(ctx, record, at)
		if itemErr != nil {
			return couponport.SidebarClaimablePage{}, itemErr
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

func (s *SidebarClaimableCatalogApplication) ReadSidebarClaimable(ctx context.Context, customerID int64, couponID couponport.ID) (couponport.SidebarClaimableItem, error) {
	if s == nil || s.uow == nil || s.store == nil || s.products == nil || customerID < 1 || couponID < 1 {
		return couponport.SidebarClaimableItem{}, ErrInvalidCoupon
	}
	var record SidebarClaimableRecord
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var readErr error
		record, readErr = s.store.ReadSidebarClaimable(txctx, customerID, couponID)
		return readErr
	})
	if err != nil {
		return couponport.SidebarClaimableItem{}, classify(err)
	}
	return s.projectSidebarClaimable(ctx, record, s.now().UTC())
}

func (s *SidebarClaimableCatalogApplication) projectSidebarClaimable(ctx context.Context, record SidebarClaimableRecord, at time.Time) (couponport.SidebarClaimableItem, error) {
	if !validStored(record.Coupon) || record.ClaimCount < 0 || (record.PublicSlug != "" && !validPublicSlug(record.PublicSlug)) {
		return couponport.SidebarClaimableItem{}, ErrUnavailable
	}
	coupon := withAvailability(record.Coupon, at)
	targets, err := s.sidebarTargets(ctx, coupon.TargetRefs)
	if err != nil {
		return couponport.SidebarClaimableItem{}, err
	}
	return couponport.SidebarClaimableItem{
		CouponID:           coupon.ID,
		Name:               coupon.Name,
		DiscountMinor:      coupon.DiscountAmountTotal,
		Currency:           coupon.Currency,
		Targets:            targets,
		ClaimEndsAt:        coupon.ClaimEndsAt,
		PublicSlug:         record.PublicSlug,
		AvailabilityStatus: coupon.AvailabilityStatus,
		UserLimitReached:   record.ClaimCount >= coupon.PerUserIssueLimit,
	}, nil
}

func (s *SidebarClaimableCatalogApplication) sidebarTargets(ctx context.Context, refs []string) ([]couponport.SidebarClaimableTarget, error) {
	targets := make([]couponport.SidebarClaimableTarget, 0, len(refs))
	for _, ref := range refs {
		kind, id, ok := parseSidebarTargetRef(ref)
		if !ok {
			return nil, ErrUnavailable
		}
		product, err := s.products.ReadProductTarget(ctx, kind, id)
		if err != nil || product.ID != id || product.ProductType != kind || strings.TrimSpace(product.Name) == "" {
			return nil, ErrUnavailable
		}
		targets = append(targets, couponport.SidebarClaimableTarget{Title: product.Name, ProductType: product.ProductType})
	}
	return targets, nil
}

func parseSidebarTargetRef(value string) (productport.ProductOptionType, productport.ID, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || (parts[0] != "standard_product" && parts[0] != "service_period") || parts[1] == "" {
		return "", 0, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != parts[1] {
		return "", 0, false
	}
	kind := productport.ProductOptionStandard
	if parts[0] == "service_period" {
		kind = productport.ProductOptionServicePeriod
	}
	return kind, productport.ID(id), true
}

var _ couponport.SidebarClaimableCatalog = (*SidebarClaimableCatalogApplication)(nil)
