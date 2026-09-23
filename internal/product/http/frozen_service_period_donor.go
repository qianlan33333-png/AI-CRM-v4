package http

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// frozenServicePeriodPublicRenderer is the immutable donor renderer from
// frozen Commerce service-period renderer source. The Go Host owns trusted
// session lookup and routes, then substitutes its public facts into this exact
// DOM/style/script source; it never invokes Python or donor business code.
//
//go:embed donor/service_period_public.py
var frozenServicePeriodPublicRenderer string

//go:embed donor/public_product_service.py
var frozenPublicProductService string

const frozenServicePeriodPublicRendererSHA256 = "1f145da6e4559ce6956d92ba499f2e332cc57a27537fab499c6039ed9132050e"
const frozenPublicProductServiceSHA256 = "8fc6cb21cd49531b0c14ca25a955ad1fdfe90ca1f8c9a7d138038002b05ff875"

func servicePeriodStateJSON(state servicePeriodPublicState, status string) string {
	entitlement := map[string]any{"status": status}
	if !state.EndAt.IsZero() {
		entitlement["end_at"] = state.EndAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	if state.RemainingDays > 0 {
		entitlement["remaining_days"] = state.RemainingDays
	}
	payload, _ := json.Marshal(map[string]any{
		"ok":           true,
		"available":    state.Available,
		"entitlement":  entitlement,
		"lead_qr":      map[string]any{"qr_url": state.LeadQRURL, "title": state.LeadQRTitle, "subtitle": state.LeadQRSubtitle},
		"cta_text":     state.CTA,
		"checkout_url": state.Product.PaymentPath,
	})
	return strings.ReplaceAll(string(payload), "</", "<\\/")
}

func frozenTripleQuoted(marker, source string) string {
	at := strings.Index(source, marker)
	if at < 0 {
		return ""
	}
	begin := strings.Index(source[at:], `return """`)
	if begin < 0 {
		return ""
	}
	begin += at + len(`return """`)
	end := strings.Index(source[begin:], `"""`)
	if end < 0 {
		return ""
	}
	return source[begin : begin+end]
}

func renderedDonorFragment(marker string) string {
	fragment := frozenTripleQuoted(marker, frozenPublicProductService)
	decoded, err := decodeFrozenPythonLiteral(fragment, false)
	if err != nil {
		return ""
	}
	return decoded
}

func servicePeriodLeadQRStyles() string {
	return renderedDonorFragment("def lead_qr_modal_styles")
}

func servicePeriodLeadQRModal() string {
	return renderedDonorFragment("def render_lead_qr_modal")
}

func servicePeriodLeadQRController() string {
	return renderedDonorFragment("def lead_qr_modal_controller_script")
}
