package app

import (
	"encoding/json"
	"testing"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestProductCardCoverUsesOnlyAProductPublicImageReference(t *testing.T) {
	images := []string{
		"  ",
		"http://assets.example.test/product.png",
		"https://user@assets.example.test/product.png",
		"https://assets.example.test/product.png#fragment",
		"https://assets.example.test/product.png",
	}
	if got := productCardCover(images); got != "https://assets.example.test/product.png" {
		t.Fatalf("cover=%q", got)
	}
	if got := productCardCover([]string{"http://assets.example.test/product.png"}); got != "" {
		t.Fatalf("non-public cover=%q", got)
	}
	if got := productCardCover([]string{"/api/admin/image-library/8/variants/original"}); got != "" {
		t.Fatalf("authenticated preview cover=%q", got)
	}
	if got := productCardCover([]string{"//other.example/product.png", "/product-assets/product.png#fragment"}); got != "" {
		t.Fatalf("unsafe relative cover=%q", got)
	}
}

func TestServicePeriodCardCoverUsesStablePublicDetailMedia(t *testing.T) {
	item := productport.ServicePeriodProduct{ProductCode: "term-31", Images: []string{"/api/admin/image-library/8/variants/original"}, AdminProjection: json.RawMessage(`{"schema_version":1,"status":"service_period_enabled","enabled":true,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[{"image_id":88}]}`)}
	if got := servicePeriodCardCover(item); got != "/api/h5/service-period-products/term-31/images/88/variants/original" {
		t.Fatalf("cover=%q", got)
	}
}
