package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/validators"
)

// SetupClient supplies signed configuration calls for operator setup and the
// queued card-template adapter. It owns no business state or retries; runtime
// writes are accepted and executed through External Effects.
type SetupClient struct {
	Client   *core.Client
	Verifier auth.Verifier
}
type SetupReceipt struct {
	CapturedAt        time.Time       `json:"captured_at"`
	Method            string          `json:"method"`
	Path              string          `json:"path"`
	HTTPStatus        int             `json:"http_status"`
	SignatureVerified bool            `json:"signature_verified"`
	Outcome           string          `json:"outcome"`
	Body              json.RawMessage `json:"body,omitempty"`
}
type DevelopmentConfig struct {
	CallbackURL     string `json:"callback_url"`
	SubMerchant     string `json:"sub_mch_code,omitempty"`
	ShowInvoiceCell bool   `json:"show_fapiao_cell"`
	SupportVAT      bool   `json:"support_vat_fapiao"`
}
type CardTemplate struct {
	SubMerchant string `json:"sub_mchid"`
	AppID       string `json:"card_appid"`
	Information struct {
		PayeeName  string `json:"payee_name,omitempty"`
		LogoURL    string `json:"logo_url"`
		CustomCell struct {
			Words       string `json:"words"`
			Description string `json:"description"`
			JumpURL     string `json:"jump_url"`
		} `json:"custom_cell"`
	} `json:"card_template_information"`
}

// Call saves only response material, never signed request headers or credentials.
// API errors must be verified too, because the SDK only validates 2xx responses.
// There are deliberately no retries; uncertain configuration writes require readback.
func (s SetupClient) call(ctx context.Context, method, path string, q url.Values, body any) SetupReceipt {
	receipt := SetupReceipt{CapturedAt: time.Now().UTC(), Method: method, Path: path, Outcome: "unavailable"}
	if method != http.MethodGet {
		receipt.Outcome = "outcome_unknown"
	}
	if s.Client == nil || s.Verifier == nil {
		return receipt
	}
	result, err := s.Client.Request(ctx, method, base+path, http.Header{}, q, body, "application/json")
	if result == nil || result.Response == nil {
		return receipt
	}
	defer result.Response.Body.Close()
	receipt.HTTPStatus = result.Response.StatusCode
	verified := err == nil
	var api *core.APIError
	if errors.As(err, &api) {
		verified = validators.NewWechatPayResponseValidator(s.Verifier).Validate(ctx, result.Response) == nil
	}
	if !verified {
		return receipt
	}
	raw, e := io.ReadAll(io.LimitReader(result.Response.Body, (256<<10)+1))
	if e != nil || len(raw) > 256<<10 || !json.Valid(raw) {
		return receipt
	}
	receipt.SignatureVerified = true
	receipt.Body = raw
	if receipt.HTTPStatus >= 200 && receipt.HTTPStatus < 300 {
		receipt.Outcome = "confirmed"
	} else if method == http.MethodGet || receipt.HTTPStatus >= 400 && receipt.HTTPStatus < 500 {
		receipt.Outcome = "rejected"
	}
	return receipt
}
func (s SetupClient) Status(ctx context.Context, sub string) SetupReceipt {
	return s.call(ctx, http.MethodGet, "/merchant/"+url.PathEscape(sub)+"/check-status", nil, nil)
}
func (s SetupClient) BaseInformation(ctx context.Context, sub string) SetupReceipt {
	return s.call(ctx, http.MethodGet, "/merchant/base-information", url.Values{"sub_mchid": {sub}}, nil)
}
func (s SetupClient) TaxCodes(ctx context.Context, sub string) SetupReceipt {
	return s.call(ctx, http.MethodGet, "/merchant/tax-codes", url.Values{"sub_mchid": {sub}, "offset": {"0"}, "limit": {"20"}}, nil)
}

func (s SetupClient) InvitedMerchants(ctx context.Context, now time.Time) SetupReceipt {
	return s.call(ctx, http.MethodGet, "/fapiaomerchant/listspinvitemchinfo", url.Values{"query_time_start": {now.Add(-6 * 24 * time.Hour).Format(time.RFC3339)}, "query_time_end": {now.Format(time.RFC3339)}, "offset": {"0"}, "limit": {"20"}, "mch_invite_status": {"MCH_INVITE_SUCC"}}, nil)
}
func (s SetupClient) Development(ctx context.Context, sub string) SetupReceipt {
	q := url.Values{}
	if sub != "" {
		q.Set("sub_mch_code", sub)
	}
	return s.call(ctx, http.MethodGet, "/merchant/development-config", q, nil)
}
func (s SetupClient) ConfigureDevelopment(ctx context.Context, c DevelopmentConfig) SetupReceipt {
	return s.call(ctx, http.MethodPatch, "/merchant/development-config", nil, c)
}
func (s SetupClient) ConfigureCard(ctx context.Context, c CardTemplate) SetupReceipt {
	return s.call(ctx, http.MethodPost, "/card-template", nil, c)
}
