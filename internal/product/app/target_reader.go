package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// TargetReader is the Product-owned implementation of the narrow product
// applicability port. It composes the two Product application services; the
// Coupon domain imports only product/port.
type TargetReader struct {
	ordinary *Service
	period   *ServicePeriodService
}

var _ productport.ProductTargetReader = (*TargetReader)(nil)
var _ productport.SidebarProductShareReader = (*TargetReader)(nil)

func NewTargetReader(ordinary *Service, period *ServicePeriodService) (*TargetReader, error) {
	if ordinary == nil || period == nil {
		return nil, ErrUnavailable
	}
	return &TargetReader{ordinary: ordinary, period: period}, nil
}

func (reader *TargetReader) ReadProductTarget(ctx context.Context, kind productport.ProductOptionType, id productport.ID) (productport.ProductOption, error) {
	if reader == nil || id < 1 {
		return productport.ProductOption{}, ErrNotFound
	}
	switch kind {
	case productport.ProductOptionStandard:
		item, err := reader.ordinary.Get(ctx, id)
		if err != nil {
			return productport.ProductOption{}, err
		}
		local, err := projectLocalProduct(item)
		if err != nil || local.Lifecycle == productport.LocalProductArchived {
			return productport.ProductOption{}, ErrNotFound
		}
		return productport.ProductOption{ID: item.ID, Code: item.ProductCode, ProductType: productport.ProductOptionStandard, Name: item.Name, PriceMinor: item.PriceMinor, Currency: item.Currency, CoverURL: publicProductCardCover(item)}, nil
	case productport.ProductOptionServicePeriod:
		item, err := reader.period.GetServicePeriodProduct(ctx, id)
		if err != nil {
			return productport.ProductOption{}, err
		}
		if item.Archived {
			return productport.ProductOption{}, ErrNotFound
		}
		return productport.ProductOption{ID: item.ServiceProductID, Code: item.ProductCode, ProductType: productport.ProductOptionServicePeriod, Name: item.Name, PriceMinor: item.PriceMinor, Currency: item.Currency, CoverURL: servicePeriodCardCover(item)}, nil
	default:
		return productport.ProductOption{}, ErrInvalidProduct
	}
}

// productCardCover accepts only an HTTPS image supplied as a public asset.
// Admin image-library preview URLs are relative /api/admin paths and require
// an administrator session, so they must never become a WeCom news imgUrl.
func productCardCover(images []string) string {
	for _, raw := range images {
		if image, ok := publicHTTPSImage(raw); ok {
			return image
		}
	}
	return ""
}

// servicePeriodCardCover prefers the Product-owned public detail-media route.
// It is derived only from an enabled service-period item's canonical admin
// projection; the public handler independently verifies both its lifecycle
// and image permission before returning bytes.  A raw images[] entry may be
// an authenticated image-library preview, so it is used only when it is an
// explicit HTTPS public asset.
func servicePeriodCardCover(item productport.ServicePeriodProduct) string {
	presentation, err := publicServicePeriodPresentation(item.AdminProjection)
	if err == nil {
		for _, media := range presentation.Media {
			if media.ImageID > 0 {
				return "/api/h5/service-period-products/" + url.PathEscape(item.ProductCode) + "/images/" + strconv.FormatInt(media.ImageID, 10) + "/variants/original"
			}
		}
	}
	return productCardCover(item.Images)
}

// ReadSidebarShareProduct rechecks the authoritative local lifecycle exactly
// when the sidebar creates its send intent. A generic Product target can be
// suitable for configuration while still being a draft or disabled item that
// must not be shared.
func (reader *TargetReader) ReadSidebarShareProduct(ctx context.Context, kind productport.ProductOptionType, id productport.ID) (productport.SidebarShareProduct, error) {
	if reader == nil || id < 1 {
		return productport.SidebarShareProduct{}, productport.ErrSaleableProductNotFound
	}
	switch kind {
	case productport.ProductOptionStandard:
		item, err := reader.ordinary.Get(ctx, id)
		if err != nil {
			return productport.SidebarShareProduct{}, saleableProductReadError(err)
		}
		projected, projectionErr := projectLocalProduct(item)
		if projectionErr != nil || !projected.Enabled || projected.Lifecycle != productport.LocalProductEnabled {
			if projectionErr != nil {
				return productport.SidebarShareProduct{}, productport.ErrSaleableProductUnavailable
			}
			return productport.SidebarShareProduct{}, productport.ErrSaleableProductNotFound
		}
		return productport.SidebarShareProduct{ID: item.ID, Code: item.ProductCode, ProductType: kind, Name: item.Name, CoverURL: publicProductCardCover(item)}, nil
	case productport.ProductOptionServicePeriod:
		item, err := reader.period.GetServicePeriodProduct(ctx, id)
		if err != nil {
			return productport.SidebarShareProduct{}, saleableProductReadError(err)
		}
		if !item.Enabled || item.Archived || item.Lifecycle != productport.ServicePeriodEnabled {
			return productport.SidebarShareProduct{}, productport.ErrSaleableProductNotFound
		}
		return productport.SidebarShareProduct{ID: item.ServiceProductID, Code: item.ProductCode, ProductType: kind, Name: item.Name, CoverURL: servicePeriodCardCover(item)}, nil
	default:
		return productport.SidebarShareProduct{}, productport.ErrSaleableProductNotFound
	}
}

func saleableProductReadError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return productport.ErrSaleableProductNotFound
	}
	return productport.ErrSaleableProductUnavailable
}

func (reader *TargetReader) ReadCheckoutProductWithin(ctx context.Context, kind productport.ProductOptionType, id productport.ID) (productport.CheckoutProduct, error) {
	if reader == nil || reader.ordinary == nil || reader.period == nil || id < 1 {
		return productport.CheckoutProduct{}, ErrNotFound
	}
	switch kind {
	case productport.ProductOptionStandard:
		item, err := reader.ordinary.store.GetForUpdate(ctx, id)
		if err != nil {
			return productport.CheckoutProduct{}, classify(err)
		}
		projected, projectionErr := projectLocalProduct(item)
		if !validOrdinaryProduct(item) || projectionErr != nil || !projected.Enabled || projected.Lifecycle != productport.LocalProductEnabled {
			return productport.CheckoutProduct{}, ErrNotFound
		}
		var projection struct {
			RequireMobile          bool   `json:"require_mobile"`
			ContactCollectionLevel string `json:"contact_collection_level"`
		}
		if json.Unmarshal(item.LegacyAdminProjection, &projection) != nil {
			return productport.CheckoutProduct{}, ErrUnavailable
		}
		action, actionErr := checkoutPaidPurchaseAction(item.LegacyAdminProjection)
		if actionErr != nil {
			return productport.CheckoutProduct{}, actionErr
		}
		level := projection.ContactCollectionLevel
		if level == "" {
			if projection.RequireMobile {
				level = "mobile"
			} else {
				level = "none"
			}
		}
		return productport.CheckoutProduct{ID: item.ID, ProductType: kind, Code: item.ProductCode, Name: item.Name, PriceMinor: item.PriceMinor, Currency: item.Currency, Version: item.Version, RequireMobile: level != "none", ContactCollectionLevel: level, Images: append([]string(nil), item.Images...), PostPurchaseAction: action}, nil
	case productport.ProductOptionServicePeriod:
		item, err := reader.period.store.GetServicePeriodProductForUpdate(ctx, id)
		if err != nil {
			return productport.CheckoutProduct{}, classify(err)
		}
		duration, err := reader.period.store.ReadServicePeriodDuration(ctx, id)
		if err != nil || duration < 1 {
			return productport.CheckoutProduct{}, ErrUnavailable
		}
		projected, err := projectServicePeriodProduct(item, duration)
		if err != nil || !projected.Enabled || projected.Lifecycle != productport.ServicePeriodEnabled {
			return productport.CheckoutProduct{}, ErrNotFound
		}
		action, actionErr := checkoutPaidPurchaseAction(projected.AdminProjection)
		if actionErr != nil {
			return productport.CheckoutProduct{}, actionErr
		}
		return productport.CheckoutProduct{ID: projected.ServiceProductID, ProductType: kind, Code: projected.ProductCode, Name: projected.Name, PriceMinor: projected.PriceMinor, Currency: projected.Currency, Version: projected.Version, Images: append([]string(nil), projected.Images...), PostPurchaseAction: action, ServicePeriodDurationDays: duration}, nil
	default:
		return productport.CheckoutProduct{}, ErrInvalidProduct
	}
}

var _ productport.CheckoutProductReader = (*TargetReader)(nil)

func checkoutPaidPurchaseAction(raw json.RawMessage) (json.RawMessage, error) {
	canonical, err := CanonicalLegacyAdminProjection(raw)
	if err != nil {
		return nil, ErrUnavailable
	}
	var projection map[string]json.RawMessage
	if json.Unmarshal(canonical, &projection) != nil {
		return nil, ErrUnavailable
	}
	keys := []string{"schema_version", "purchase_action_enabled", "purchase_action_mode", "lead_channel_id", "lead_qr_title", "lead_qr_subtitle", "completion_redirect_url", "completion_target"}
	snapshot := make(map[string]json.RawMessage, len(keys))
	for _, key := range keys {
		value, found := projection[key]
		if !found {
			return nil, ErrUnavailable
		}
		snapshot[key] = value
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || len(encoded) == 0 {
		return nil, ErrUnavailable
	}
	return encoded, nil
}
