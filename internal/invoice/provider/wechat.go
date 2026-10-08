package provider

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/subtle"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/emmansun/gmsm/sm3"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/cipher/decryptors"
	"github.com/wechatpay-apiv3/wechatpay-go/core/cipher/encryptors"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type WeChat struct {
	Client         *core.Client
	FileClient     *http.Client
	Notify         *notify.Handler
	MerchantID     string
	PlatformSerial string
	Material       p.MaterialReader
	Payments       p.PaymentVerifier
	OA             *OfficialAccount
}

func New(ctx context.Context, merchant, serial, apiKey string, key *rsa.PrivateKey, certificate *x509.Certificate, oa *OfficialAccount, material p.MaterialReader, payments p.PaymentVerifier) (*WeChat, error) {
	if merchant == "" || serial == "" || len(apiKey) != 32 || key == nil || certificate == nil || oa == nil || material == nil || payments == nil || time.Now().Before(certificate.NotBefore) || time.Now().After(certificate.NotAfter) {
		return nil, p.ErrInvalid
	}
	visitor := core.NewCertificateMapWithList([]*x509.Certificate{certificate})
	verifier := verifiers.NewSHA256WithRSAVerifier(visitor)
	c, e := core.NewClient(ctx, option.WithMerchantCredential(merchant, serial, key), option.WithVerifier(verifier), option.WithWechatPayCipher(encryptors.NewWechatPayEncryptor(visitor), decryptors.NewWechatPayDecryptor(key)))
	if e != nil {
		return nil, p.ErrInvalid
	}
	return &WeChat{Client: c, Notify: notify.NewNotifyHandler(apiKey, verifier), MerchantID: merchant, PlatformSerial: strings.ToUpper(certificate.SerialNumber.Text(16)), OA: oa, Material: material, Payments: payments}, nil
}

const base = "https://api.mch.weixin.qq.com/v3/new-tax-control-fapiao"

func (w *WeChat) request(ctx context.Context, method, path string, q url.Values, body, out any) error {
	if w == nil || w.Client == nil {
		return p.ErrDisabled
	}
	headers := http.Header{}
	headers.Set("Wechatpay-Serial", w.PlatformSerial)
	if method == http.MethodPost {
		headers.Set("Content-Type", "application/json")
	}
	result, e := w.Client.Request(ctx, method, base+path, headers, q, body, "application/json")
	if e != nil {
		var api *core.APIError
		if errors.As(e, &api) && api.StatusCode == 404 {
			return p.ErrPending
		}
		return p.ErrUnavailable
	}
	if result == nil || result.Response == nil {
		return p.ErrUnavailable
	}
	defer result.Response.Body.Close()
	if out == nil {
		return nil
	}
	if e = json.NewDecoder(io.LimitReader(result.Response.Body, 256<<10)).Decode(out); e != nil {
		return p.ErrUnavailable
	}
	return nil
}
func (w *WeChat) Title(ctx context.Context, a p.Application, openID string) (p.Launch, error) {
	q := url.Values{"sub_mchid": {a.Snapshot.MerchantID}, "fapiao_apply_id": {a.ID}, "source": {"WEB"}, "appid": {a.Snapshot.AppID}, "openid": {openID}, "total_amount": {strconv.FormatInt(a.Snapshot.Amount, 10)}, "seller_name": {a.Snapshot.SellerName}}
	var link p.Launch
	e := w.request(ctx, http.MethodGet, "/user-title/title-url", q, nil, &link)
	if e != nil {
		return link, e
	}
	if link.AppID == "" || link.Path == "" || link.UserName == "" {
		return link, p.ErrUnavailable
	}
	link.ExpiresAt = time.Now().Add(30 * time.Minute)
	return link, nil
}
func (w *WeChat) ReadTitle(ctx context.Context, a p.Application) (p.Buyer, error) {
	var b p.Buyer
	e := w.request(ctx, http.MethodGet, "/user-title", url.Values{"sub_mchid": {a.Snapshot.MerchantID}, "fapiao_apply_id": {a.ID}, "scene": {"WITHOUT_WECHATPAY"}}, nil, &b)
	return b, e
}
func (w *WeChat) Query(ctx context.Context, a p.Application) (p.Result, error) {
	var list struct {
		Information []p.Result `json:"fapiao_information"`
	}
	e := w.request(ctx, http.MethodGet, "/fapiao-applications/"+url.PathEscape(a.ID), url.Values{"sub_mchid": {a.Snapshot.MerchantID}, "fapiao_id": {a.InvoiceID}}, nil, &list)
	if e != nil {
		return p.Result{}, e
	}
	if len(list.Information) != 1 || list.Information[0].InvoiceID != a.InvoiceID {
		return p.Result{}, p.ErrConflict
	}
	return list.Information[0], nil
}
func (w *WeChat) Write(ctx context.Context, a p.Application, reverse bool) error {
	if !reverse {
		if e := w.Payments.VerifyInvoicePayment(ctx, a.Snapshot); e != nil {
			return e
		}
	}
	return w.writeVerified(ctx, a, reverse)
}
func (w *WeChat) writeVerified(ctx context.Context, a p.Application, reverse bool) error {
	if reverse {
		body := map[string]any{"sub_mchid": a.Snapshot.MerchantID, "reverse_reason": "SALES_RETURN", "fapiao_information": []map[string]string{{"fapiao_id": a.InvoiceID, "fapiao_number": a.BlueNumber}}}
		return w.request(ctx, http.MethodPost, "/fapiao-applications/"+url.PathEscape(a.ID)+"/reverse", nil, body, nil)
	}
	body := map[string]any{"sub_mchid": a.Snapshot.MerchantID, "fapiao_apply_id": a.ID, "buyer_information": a.Buyer, "fapiao_information": map[string]any{"fapiao_id": a.InvoiceID, "total_amount": a.Snapshot.Amount, "items": a.Snapshot.Lines, "billing_person_id": a.Snapshot.BillingPersonID, "fapiao_bill_type": "COMM_FAPIAO", "transaction_information": []map[string]any{{"pay_channel": "WECHAT_PAY", "transaction_id": a.Snapshot.TransactionID, "out_trade_no": a.Snapshot.OrderNo, "amount": a.Snapshot.Amount}}}}
	return w.request(ctx, http.MethodPost, "/fapiao-applications/issue-general", nil, body, nil)
}
func (w *WeChat) Execute(ctx context.Context, envelope effects.Envelope, attempt effects.Attempt) (effects.AdapterResult, error) {
	if envelope.Owner != effects.OwnerInvoice || (envelope.Kind != effects.KindInvoiceIssue && envelope.Kind != effects.KindInvoiceReverse) {
		return effects.AdapterResult{}, p.ErrInvalid
	}
	reverse := envelope.Kind == effects.KindInvoiceReverse
	a, e := w.Material.PrepareInvoiceEffect(ctx, attempt.EffectID, reverse)
	if errors.Is(e, p.ErrIneligible) {
		return effects.AdapterResult{Completion: effects.StateFinalFailed, ReceiptDigest: effects.Hash("invoice.stopped", attempt.EffectID), FailureCode: "invoice_not_eligible"}, nil
	}
	if e != nil {
		return effects.AdapterResult{}, p.ErrUnavailable
	}
	if !reverse {
		if proof := w.Payments.VerifyInvoicePayment(ctx, a.Snapshot); proof != nil {
			code := "invoice_payment_check_unavailable"
			if errors.Is(proof, p.ErrIneligible) || errors.Is(proof, p.ErrConflict) {
				code = "invoice_payment_unverified"
			}
			return effects.AdapterResult{Completion: effects.StateFinalFailed, ReceiptDigest: effects.Hash("invoice.payment.unverified", attempt.EffectID), FailureCode: code}, nil
		}
	}
	e = w.writeVerified(ctx, a, reverse)
	state := effects.StateExecuted
	if e != nil {
		state = effects.StateUnknown
	}
	return effects.AdapterResult{Completion: state, ReceiptDigest: effects.Hash("invoice.dispatch", attempt.EffectID, string(state)), CallAttempted: true, RealExternalCallExecuted: e == nil, FailureCode: func() string {
		if e != nil {
			return "invoice_query_required"
		}
		return ""
	}()}, nil
}
func (w *WeChat) File(ctx context.Context, a p.Application) ([]byte, error) {
	var data struct {
		Files []struct {
			InvoiceID string `json:"fapiao_id"`
			URL       string `json:"download_url"`
		} `json:"fapiao_download_info_list"`
	}
	e := w.request(ctx, http.MethodGet, "/fapiao-applications/"+url.PathEscape(a.ID)+"/fapiao-files", url.Values{"sub_mchid": {a.Snapshot.MerchantID}, "fapiao_id": {a.InvoiceID}}, nil, &data)
	if e != nil {
		return nil, e
	}
	if len(data.Files) != 1 || data.Files[0].InvoiceID != a.InvoiceID {
		return nil, p.ErrConflict
	}
	u, e := url.Parse(data.Files[0].URL)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Host != "pay.wechatpay.cn" || u.Path != "/invoicing/fapiao/fapiao-file" || u.Fragment != "" || u.Query().Get("token") == "" {
		return nil, p.ErrConflict
	}
	// Downloads deliberately carry no merchant Authorization header: the official
	// 30-second URL is unsigned. Verify its SM3 digest and never follow redirects.
	invoice, e := w.Query(ctx, a)
	if e != nil {
		return nil, e
	}
	if invoice.Status != "ISSUED" || invoice.InvoiceID != a.InvoiceID || invoice.TotalAmount != a.Snapshot.Amount || invoice.Seller.TaxpayerID != a.Snapshot.SellerTaxID || invoice.Blue.Number != a.BlueNumber || invoice.Card.OpenID == "" {
		return nil, p.ErrConflict
	}
	q := u.Query()
	q.Set("mchid", w.MerchantID)
	q.Set("sub_mchid", a.Snapshot.MerchantID)
	q.Set("openid", invoice.Card.OpenID)
	q.Set("invoice_no", a.BlueNumber)
	q.Set("fapiao_id", a.InvoiceID)
	q.Set("file_format", "PDF")
	u.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return nil, p.ErrUnavailable
	}
	client := http.Client{Timeout: 15 * time.Second}
	if w.FileClient != nil {
		client = *w.FileClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, e := client.Do(req)
	if e != nil {
		return nil, p.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, p.ErrUnavailable
	}
	b, e := io.ReadAll(io.LimitReader(response.Body, (10<<20)+1))
	if e != nil || len(b) > 10<<20 || len(b) < 5 || string(b[:5]) != "%PDF-" {
		return nil, p.ErrUnavailable
	}
	digest := fmt.Sprintf("%x", sm3.Sum(b))
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(response.Header.Get("SM3-Digest"))), []byte(digest)) != 1 {
		return nil, p.ErrConflict
	}
	return b, nil
}
func (w *WeChat) Verify(ctx context.Context, r *http.Request) (p.Notice, error) {
	var out p.Notice
	if w == nil || w.Notify == nil {
		return out, p.ErrDisabled
	}
	ts, e := strconv.ParseInt(r.Header.Get("Wechatpay-Timestamp"), 10, 64)
	if e != nil || time.Since(time.Unix(ts, 0)) > 5*time.Minute || time.Until(time.Unix(ts, 0)) > 5*time.Minute {
		return out, p.ErrInvalid
	}
	var resource struct {
		Merchant      string `json:"mchid"`
		SubMerchant   string `json:"sub_mchid"`
		ApplicationID string `json:"fapiao_apply_id"`
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, (128<<10)+1))
	if e != nil || len(raw) > 128<<10 {
		return out, p.ErrInvalid
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	request, e := w.Notify.ParseNotifyRequest(ctx, r, &resource)
	if e != nil {
		return out, p.ErrInvalid
	}
	if resource.Merchant != w.MerchantID || resource.SubMerchant == "" || resource.ApplicationID == "" {
		return out, p.ErrInvalid
	}
	return p.Notice{ID: request.ID, EventType: request.EventType, ApplicationID: resource.ApplicationID, MerchantID: resource.Merchant, SubMerchantID: resource.SubMerchant, BodyDigest: string(effects.Hash("invoice.notify", string(raw)))}, nil
}
func (w *WeChat) SignPage(ctx context.Context, page string) (p.JSConfig, error) {
	return w.OA.SignPage(ctx, page)
}

var _ p.Provider = (*WeChat)(nil)
var _ effects.ProviderAdapter = (*WeChat)(nil)
