package paymenthttp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymenth5oauth "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/h5oauth"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const SessionCookieName = paymentport.TrustedSessionCookieName
const h5OAuthReturnCookieName = "__Secure-aicrm_payment_oauth_return"
const maxBody = 64 << 10

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Application interface {
	Create(context.Context, paymentport.CreateCommand) (domain.Payment, error)
	CheckoutSessionBinding(context.Context, string) (string, error)
	GetCheckout(context.Context, domain.Provider, string, string) (paymentport.Handoff, error)
	RequestRefund(context.Context, paymentport.RefundCommand) (domain.Refund, error)
	GetPayment(context.Context, int64) (domain.Payment, error)
	ApplyVerifiedCallback(context.Context, paymentprovider.CallbackResult) error
	ApplyVerifiedShopCallback(context.Context, paymentport.ShopRefundCallback) error
	ReconcileShopRefund(context.Context, int64) (domain.Refund, error)
	ReconcileWeChatPayPayment(context.Context, int64) (domain.Payment, error)
	ReconcileWeChatPayRefund(context.Context, int64) (domain.Refund, error)
	FindPayment(context.Context, domain.Provider, string) (domain.Payment, error)
	ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error)
	ListRefundsForPayment(context.Context, domain.Provider, string, int32, int32) ([]paymentport.RefundProjection, int64, error)
	ListOrderEffects(context.Context, domain.Provider, string) ([]paymentport.EffectProjection, error)
}

// RefundRecoveryApplication is intentionally a narrow optional read Port:
// checkout-only adapters retain their existing Payment surface while the
// composed admin handler can recover only a receipt scoped by its original key.
type RefundRecoveryApplication interface {
	FindRefundRecoveryReceipt(context.Context, domain.Provider, string, string, string) (domain.Refund, bool, error)
}

// PaymentReconciliationPreviewApplication is a narrow administrator-only
// read seam. It lets the existing reconcile endpoint prove the specific
// paid-confirmation repair before an operator chooses the real mutation.
type PaymentReconciliationPreviewApplication interface {
	PreviewReconcileWeChatPayPayment(context.Context, int64) (paymentport.PaymentReconciliationPreview, error)
}

type SessionIdentityVerifier interface {
	VerifyCode(context.Context, string) (identitydomain.VerifiedFact, error)
}

type TrustedSessionIssuer interface {
	IssueTrusted(context.Context, paymentsession.IssueCommand) (paymentsession.Issued, error)
}

type H5OAuthApplication interface {
	Enabled() bool
	Start(context.Context, string) (string, error)
	Complete(context.Context, string, string) (paymentsession.Issued, string, error)
	RecoverReturnPath(context.Context, string) (string, error)
}

type AlipayCallbackVerifier interface {
	VerifyValues(context.Context, url.Values) (paymentprovider.CallbackResult, error)
}

type Handler struct {
	app                 Application
	verifier            *paymentprovider.CallbackVerifier
	alipayVerifier      AlipayCallbackVerifier
	security            RequestSecurity
	writesEnabled       bool
	alipayWritesEnabled bool
	shopWritesEnabled   bool
	shopVerifier        paymentport.ShopCallbackVerifier
	sessionVerifier     SessionIdentityVerifier
	sessionIssuer       TrustedSessionIssuer
	h5OAuth             H5OAuthApplication
	commerceOrders      orderport.CommercePushDeliveryReferenceReader
	commerceDeliveries  outboundport.CommercePushDeliveryReader
	purchaseActions     productport.PaidPurchaseActionReader
	leadQR              channelport.PublicLeadQRCodeReader
}

func (handler *Handler) SetAlipayCallbackVerifier(verifier AlipayCallbackVerifier) error {
	if handler == nil || verifier == nil {
		return paymentport.ErrInvalid
	}
	handler.alipayVerifier = verifier
	return nil
}

func (handler *Handler) SetH5OAuth(application H5OAuthApplication) error {
	if handler == nil || application == nil {
		return paymentport.ErrInvalid
	}
	handler.h5OAuth = application
	return nil
}

func (handler *Handler) SetTrustedSessionIssuer(verifier SessionIdentityVerifier, issuer TrustedSessionIssuer) error {
	if handler == nil || verifier == nil || issuer == nil {
		return paymentport.ErrInvalid
	}
	handler.sessionVerifier, handler.sessionIssuer = verifier, issuer
	return nil
}

func (handler *Handler) SetShopCallbackVerifier(verifier paymentport.ShopCallbackVerifier) error {
	if handler == nil || verifier == nil {
		return paymentport.ErrInvalid
	}
	handler.shopVerifier = verifier
	return nil
}

// SetCommercePushDeliveryReaders binds the two stable read Ports used only by
// the legacy order-delivery compatibility route. Payment owns neither Order
// references nor Outbound delivery rows.
func (handler *Handler) SetCommercePushDeliveryReaders(orders orderport.CommercePushDeliveryReferenceReader, deliveries outboundport.CommercePushDeliveryReader) error {
	if handler == nil || orders == nil || deliveries == nil {
		return paymentport.ErrInvalid
	}
	handler.commerceOrders, handler.commerceDeliveries = orders, deliveries
	return nil
}

// SetPaidPurchaseActionReader wires Product's immutable paid-action snapshot
// and Channel's persisted public QR reader into the already session-authorized
// checkout-status route. Neither reader may resolve identity, write Provider
// state, or broaden checkout access.
func (handler *Handler) SetPaidPurchaseActionReader(actions productport.PaidPurchaseActionReader, leadQR channelport.PublicLeadQRCodeReader) error {
	if handler == nil || actions == nil || leadQR == nil {
		return paymentport.ErrInvalid
	}
	handler.purchaseActions, handler.leadQR = actions, leadQR
	return nil
}

func NewHandler(app Application, verifier *paymentprovider.CallbackVerifier, security RequestSecurity, writesEnabled bool, shopEnabled ...bool) (*Handler, error) {
	if app == nil || security == nil {
		return nil, errors.New("payment HTTP dependencies are required")
	}
	handler := &Handler{app: app, verifier: verifier, security: security, writesEnabled: writesEnabled}
	if len(shopEnabled) > 0 {
		handler.shopWritesEnabled = shopEnabled[0]
	}
	if len(shopEnabled) > 1 {
		handler.alipayWritesEnabled = shopEnabled[1]
	}
	return handler, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	path := strings.TrimSuffix(request.URL.Path, "/")
	switch {
	case path == "/api/h5/wechat-pay/oauth/start":
		handler.startH5OAuth(writer, request)
	case path == "/api/h5/wechat-pay/oauth/callback":
		handler.completeH5OAuth(writer, request)
	case path == "/api/v1/wechat-pay/sessions":
		handler.issueSession(writer, request)
	case path == "/api/v1/wechat-pay/purchase-status":
		handler.purchaseStatus(writer, request)
	case path == "/api/v1/wechat-pay/checkout-session":
		handler.checkoutSession(writer, request)
	case path == "/api/v1/wechat-pay/checkouts":
		handler.checkout(writer, request, domain.ProviderWeChatPay)
	case path == "/api/v1/alipay/checkouts":
		handler.checkout(writer, request, domain.ProviderAlipay)
	case strings.HasPrefix(path, "/api/v1/wechat-pay/checkouts/"):
		checkoutPath := strings.TrimPrefix(path, "/api/v1/wechat-pay/checkouts/")
		if strings.HasSuffix(checkoutPath, "/completion-target") {
			handler.resolveCompletionTarget(writer, request, domain.ProviderWeChatPay, strings.TrimSuffix(checkoutPath, "/completion-target"))
			return
		}
		handler.checkoutStatus(writer, request, domain.ProviderWeChatPay, checkoutPath)
	case strings.HasPrefix(path, "/api/v1/alipay/checkouts/"):
		checkoutPath := strings.TrimPrefix(path, "/api/v1/alipay/checkouts/")
		if strings.HasSuffix(checkoutPath, "/completion-target") {
			handler.resolveCompletionTarget(writer, request, domain.ProviderAlipay, strings.TrimSuffix(checkoutPath, "/completion-target"))
			return
		}
		handler.checkoutStatus(writer, request, domain.ProviderAlipay, checkoutPath)
	case strings.HasPrefix(path, "/api/admin/wechat-pay/payments/") && strings.HasSuffix(path, "/abandon-checkout"):
		handler.abandonCheckout(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/wechat-pay/payments/"), "/abandon-checkout"))
	case strings.HasPrefix(path, "/api/admin/wechat-pay/payments/") && strings.HasSuffix(path, "/allow-checkout-restart"):
		handler.allowCheckoutRestart(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/wechat-pay/payments/"), "/allow-checkout-restart"))
	case path == "/api/admin/payments/history":
		handler.historyPayment(writer, request)
	case path == "/api/admin/refunds":
		handler.refunds(writer, request)
	case path == "/api/admin/refunds/recovery":
		handler.refundRecoveryReceipt(writer, request)
	case path == "/api/public/wechat-pay/callbacks/payment" || path == "/api/public/wechat-pay/callbacks/refund":
		handler.callback(writer, request)
	case path == "/api/public/alipay/callback":
		handler.alipayCallback(writer, request)
	case path == "/api/public/wechat-shop/callbacks/refund":
		handler.shopCallback(writer, request)
	case strings.HasPrefix(path, "/api/admin/wechat-pay/profit-sharing/receivers/") && strings.HasSuffix(path, "/recover"):
		handler.recoverProfitSharingReceiver(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/wechat-pay/profit-sharing/receivers/"), "/recover"))
	case strings.HasPrefix(path, "/api/admin/wechat-shop/refunds/") && strings.HasSuffix(path, "/reconcile"):
		handler.reconcileShopRefund(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/wechat-shop/refunds/"), "/reconcile"))
	case strings.HasPrefix(path, "/api/admin/wechat-pay/payments/") && strings.HasSuffix(path, "/reconcile"):
		handler.reconcileWeChatPay(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/wechat-pay/payments/"), "/reconcile"), false)
	case strings.HasPrefix(path, "/api/admin/wechat-pay/refunds/") && strings.HasSuffix(path, "/reconcile"):
		handler.reconcileWeChatPay(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/wechat-pay/refunds/"), "/reconcile"), true)
	case strings.HasPrefix(path, "/api/admin/wechat-pay/orders/") && strings.HasSuffix(path, "/refunds"):
		handler.compatRefund(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/wechat-pay/orders/"), "/refunds"))
	case strings.HasPrefix(path, "/api/admin/wechat-pay/orders/") && strings.HasSuffix(path, "/external-push-deliveries"):
		handler.orderEffects(writer, request)
	case strings.HasPrefix(path, "/api/admin/payments/") && strings.HasSuffix(path, "/refunds"):
		handler.refund(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/payments/"), "/refunds"))
	default:
		writeError(writer, http.StatusNotFound, "not_found")
	}
}

// checkoutSession returns an opaque current-session marker for a browser
// recovery checkpoint. It deliberately verifies the HttpOnly cookie through
// Payment before returning anything, and contains no OneID/customer facts.
func (handler *Handler) checkoutSession(writer http.ResponseWriter, request *http.Request) {
	if !handler.writesEnabled {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		writeError(writer, http.StatusUnauthorized, "payment_session_required")
		return
	}
	binding, err := handler.app.CheckoutSessionBinding(request.Context(), cookie.Value)
	if err != nil {
		resultError(writer, err)
		return
	}
	canCreate := false
	if ready, ok := handler.app.(paymentport.CheckoutSessionReadiness); ok {
		canCreate, err = ready.CanCreateCheckout(request.Context(), cookie.Value)
		if err != nil {
			resultError(writer, err)
			return
		}
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, map[string]any{"checkout_session_binding": binding, "can_create_checkout": canCreate})
}

func (handler *Handler) startH5OAuth(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || handler.h5OAuth == nil || !handler.h5OAuth.Enabled() {
		writeError(writer, http.StatusServiceUnavailable, "payment_h5_oauth_disabled")
		return
	}
	query, ok := exactH5OAuthQuery(request, "return_url")
	if !strings.Contains(strings.ToLower(request.UserAgent()), "micromessenger") || !ok {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	location, err := handler.h5OAuth.Start(request.Context(), query["return_url"])
	if err != nil {
		if errors.Is(err, paymenth5oauth.ErrInvalid) {
			writeError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
		// A valid canonical return can still fail while durably reserving its
		// one-time OAuth state. Do not misreport that server-side condition as a
		// caller error or expose its database/provider detail.
		writeError(writer, http.StatusServiceUnavailable, "payment_h5_oauth_unavailable")
		return
	}
	// The OAuth state remains one-time. This short-lived, path-scoped cookie
	// carries only the already validated same-origin return path so a rejected
	// or stale callback can return to the login gate without replaying state.
	http.SetCookie(writer, &http.Cookie{Name: h5OAuthReturnCookieName, Value: base64.RawURLEncoding.EncodeToString([]byte(query["return_url"])), Path: "/api/h5/wechat-pay/oauth/callback", MaxAge: 600, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(writer, request, location, http.StatusFound)
}

func (handler *Handler) completeH5OAuth(writer http.ResponseWriter, request *http.Request) {
	query, ok := exactH5OAuthQuery(request, "state", "code")
	denied := false
	if !ok {
		if refusal, valid := exactH5OAuthQuery(request, "state", "error"); valid && (refusal["error"] == "access_denied" || refusal["error"] == "authdeny") {
			query, ok, denied = refusal, true, true
		} else if refusal, valid := exactH5OAuthQuery(request, "state"); valid {
			query, ok, denied = refusal, true, true
		}
	}
	if request.Method != http.MethodGet || handler.h5OAuth == nil || !handler.h5OAuth.Enabled() || !ok {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	denied = denied || query["code"] == "authdeny"
	var issued paymentsession.Issued
	var returnPath string
	var err error
	if denied {
		err = paymenth5oauth.ErrInvalid
	} else {
		issued, returnPath, err = handler.h5OAuth.Complete(request.Context(), query["state"], query["code"])
	}
	returnCookie, cookieErr := request.Cookie(h5OAuthReturnCookieName)
	http.SetCookie(writer, &http.Cookie{Name: h5OAuthReturnCookieName, Path: "/api/h5/wechat-pay/oauth/callback", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	if err != nil {
		if !errors.Is(err, paymenth5oauth.ErrIdentityConflict) {
			recoveryPath, recoveryErr := handler.h5OAuth.RecoverReturnPath(request.Context(), query["state"])
			if recoveryErr != nil && cookieErr == nil {
				if decoded, decodeErr := base64.RawURLEncoding.DecodeString(returnCookie.Value); decodeErr == nil && paymenth5oauth.ValidReturnPath(string(decoded)) {
					recoveryPath = string(decoded)
				}
			}
			if paymenth5oauth.ValidReturnPath(recoveryPath) {
				writer.Header().Set("Cache-Control", "no-store")
				http.Redirect(writer, request, recoveryPath, http.StatusSeeOther)
				return
			}
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		message := "微信授权未完成，请关闭页面后从原链接重新进入。"
		if errors.Is(err, paymenth5oauth.ErrIdentityConflict) {
			message = "微信授权已完成，但您的历史账号资料需要核对。请联系客服处理后再继续支付。"
		}
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(writer, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>授权提示</title></head><body style="font:17px/1.7 -apple-system,sans-serif;padding:32px;color:#263238"><h2>暂时无法继续支付</h2><p>%s</p><p>当前未发起新的支付，请勿反复提交。</p></body></html>`, message)
		return
	}
	if err = WriteTrustedSessionCookie(writer, issued); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	http.Redirect(writer, request, returnPath, http.StatusFound)
}

// exactH5OAuthQuery rejects duplicate values and unknown parameter names at
// the HTTP boundary, before the OAuth application can persist or consume
// state, or call its Provider adapter.
func exactH5OAuthQuery(request *http.Request, names ...string) (map[string]string, bool) {
	if request == nil || request.URL == nil {
		return nil, false
	}
	values := request.URL.Query()
	if len(values) != len(names) {
		return nil, false
	}
	result := make(map[string]string, len(names))
	for _, name := range names {
		items, found := values[name]
		if !found || len(items) != 1 {
			return nil, false
		}
		result[name] = items[0]
	}
	return result, true
}

func (handler *Handler) issueSession(writer http.ResponseWriter, request *http.Request) {
	if !handler.writesEnabled || handler.sessionVerifier == nil || handler.sessionIssuer == nil {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	fact, err := handler.sessionVerifier.VerifyCode(request.Context(), body.Code)
	if err != nil || !fact.Valid() {
		writeError(writer, http.StatusUnauthorized, "identity_verification_failed")
		return
	}
	codeDigest := sha256.Sum256([]byte(body.Code))
	issued, err := handler.sessionIssuer.IssueTrusted(request.Context(), paymentsession.IssueCommand{Fact: fact, IdempotencyKey: "payment-session:" + fmt.Sprintf("%x", codeDigest[:])})
	if err != nil {
		resultError(writer, err)
		return
	}
	if err = WriteTrustedSessionCookie(writer, issued); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"expires_at": issued.ExpiresAt, "verified": true})
}

func (handler *Handler) refunds(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodPost {
		handler.shopRefund(writer, request)
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet, http.MethodPost)
		return
	}
	if _, err := handler.security.Authenticate(request.Context(), request); err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return
	}
	provider, merchantOrderNo, limit, offset, filtered, ok := parseRefundListQuery(request)
	if !ok {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	var (
		rows  []paymentport.RefundProjection
		total int64
		err   error
	)
	if filtered {
		rows, total, err = handler.app.ListRefundsForPayment(request.Context(), provider, merchantOrderNo, int32(limit), int32(offset))
	} else {
		rows, total, err = handler.app.ListRefunds(request.Context(), int32(limit), int32(offset))
	}
	if err != nil {
		resultError(writer, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		effectID := int64(0)
		if strings.HasPrefix(row.Refund.EffectID, "eer_") {
			effectID, _ = strconv.ParseInt(strings.TrimPrefix(row.Refund.EffectID, "eer_"), 10, 64)
		}
		provider := string(row.Refund.Provider)
		if provider == "wechat_pay" {
			provider = "wechat"
		}
		items = append(items, map[string]any{
			"id": row.Refund.ID, "order_id": row.OrderID, "provider": provider,
			// The legacy list has no verified Order transaction projection and no
			// effect-state read. Keep the old fields empty rather than mislabeling
			// a digest or a refund business state as either fact.
			"order_no": row.MerchantOrder, "transaction_id": "",
			"refund_id": row.Refund.RefundNo, "out_refund_no": row.Refund.RefundNo,
			"refund_amount_total": row.Refund.AmountMinor, "order_amount_minor": row.OrderAmount,
			"currency": row.Currency, "reason": row.Refund.Reason, "status": compatRefundStatus(row.Refund.Status),
			"external_effect_id": effectID, "external_effect_state": "",
			"auto_retry_allowed": false, "created_at": row.Refund.CreatedAt,
		})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items, "refunds": items, "total": total, "limit": limit, "offset": offset, "has_more": offset+int64(len(items)) < total})
}

// parseRefundListQuery leaves the unscoped administrative page available, but
// makes a detail filter an all-or-nothing Payment identity. An order number on
// its own is not globally unique across providers and must never select a
// refund timeline by coincidence.
func parseRefundListQuery(request *http.Request) (domain.Provider, string, int64, int64, bool, bool) {
	query := request.URL.Query()
	for key, values := range query {
		if (key != "provider" && key != "order_no" && key != "limit" && key != "offset") || len(values) != 1 {
			return "", "", 0, 0, false, false
		}
	}
	limit, offset := int64(50), int64(0)
	var err error
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 32)
	}
	if err == nil {
		if raw := query.Get("offset"); raw != "" {
			offset, err = strconv.ParseInt(raw, 10, 32)
		}
	}
	if err != nil || limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		return "", "", 0, 0, false, false
	}
	rawProvider, merchantOrderNo := query.Get("provider"), query.Get("order_no")
	if merchantOrderNo == "" && (rawProvider == "" || rawProvider == "all") {
		return "", "", limit, offset, false, true
	}
	if merchantOrderNo == "" || len(merchantOrderNo) > 200 {
		return "", "", 0, 0, false, false
	}
	var provider domain.Provider
	switch rawProvider {
	case "wechat", "wechat_pay":
		provider = domain.ProviderWeChatPay
	case "wechat_shop":
		provider = domain.ProviderWeChatShop
	default:
		return "", "", 0, 0, false, false
	}
	return provider, merchantOrderNo, limit, offset, true, true
}

func parseRefundRecoveryQuery(request *http.Request) (domain.Provider, string, bool) {
	query := request.URL.Query()
	for key, values := range query {
		if (key != "provider" && key != "order_no") || len(values) != 1 {
			return "", "", false
		}
	}
	merchantOrderNo := query.Get("order_no")
	if merchantOrderNo == "" || len(merchantOrderNo) > 200 {
		return "", "", false
	}
	switch query.Get("provider") {
	case "wechat", "wechat_pay":
		return domain.ProviderWeChatPay, merchantOrderNo, true
	case "wechat_shop":
		return domain.ProviderWeChatShop, merchantOrderNo, true
	default:
		return "", "", false
	}
}

// refundRecoveryReceipt is a Payment-owned, non-mutating recovery read. The
// original idempotency key stays in a request header so it is neither logged
// in ordinary URL telemetry nor copied into browser history.
func (handler *Handler) refundRecoveryReceipt(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	principal, err := handler.security.Authenticate(request.Context(), request)
	if err != nil || principal.Kind != accessdomain.KindAdmin || principal.InternalID < 1 {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return
	}
	provider, merchantOrderNo, ok := parseRefundRecoveryQuery(request)
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if !ok || key == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	recovery, ok := handler.app.(RefundRecoveryApplication)
	if !ok {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	actorScope := "admin:" + strconv.FormatInt(principal.InternalID, 10)
	// actor_binding is only a local-browser partition marker. It is neither an
	// authorization credential nor a substitute for the principal checked on
	// every request, and it intentionally never exposes the raw admin ID.
	actorBindingDigest := sha256.Sum256([]byte("payment.refund.recovery.actor|" + actorScope))
	actorBinding := fmt.Sprintf("%x", actorBindingDigest)
	refund, found, err := recovery.FindRefundRecoveryReceipt(request.Context(), provider, merchantOrderNo, actorScope, key)
	if err != nil {
		resultError(writer, err)
		return
	}
	if !found {
		writeJSON(writer, http.StatusOK, map[string]any{"found": false, "actor_binding": actorBinding})
		return
	}
	status := compatRefundStatus(refund.Status)
	writeJSON(writer, http.StatusOK, map[string]any{
		"found": true, "receipt_id": refund.ID, "refund_no": refund.RefundNo,
		"status": status, "actor_binding": actorBinding,
	})
}

func (handler *Handler) shopRefund(writer http.ResponseWriter, request *http.Request) {
	if !handler.shopWritesEnabled {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	principal, err := handler.security.AuthorizeCSRF(request.Context(), request)
	if err != nil || !paymentBusinessWriteRole(principal) {
		writeError(writer, http.StatusForbidden, "forbidden")
		return
	}
	var body struct {
		Provider                  string `json:"provider"`
		OrderNo                   string `json:"order_no"`
		ProductID                 string `json:"product_id"`
		SKUID                     string `json:"sku_id"`
		RefundCount               int64  `json:"refund_count"`
		AmountMinor               int64  `json:"refund_amount_total"`
		ReasonCode                string `json:"reason_code"`
		Reason                    string `json:"reason"`
		TransactionIDConfirmation string `json:"transaction_id_confirmation"`
		Checked                   bool   `json:"checked"`
		Operator                  string `json:"operator,omitempty"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	// WeChat Shop has its own existing confirmation contract: the named field
	// confirms this Shop order reference. Shop callbacks do not produce a
	// verified WeChat Pay transaction_id fact, so applying the Pay guard here
	// would reject every valid Shop refund.
	if body.Provider != "wechat_shop" || body.OrderNo == "" || body.TransactionIDConfirmation != body.OrderNo || !body.Checked || key == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	payment, err := handler.app.FindPayment(request.Context(), domain.ProviderWeChatShop, body.OrderNo)
	if err != nil {
		resultError(writer, err)
		return
	}
	digest := sha256.Sum256([]byte(strconv.FormatInt(principal.InternalID, 10) + "\x00" + key))
	refund, err := handler.app.RequestRefund(request.Context(), paymentport.RefundCommand{
		PaymentID: payment.ID, AmountMinor: body.AmountMinor, RefundNo: "SRF-" + fmt.Sprintf("%x", digest[:12]), Reason: body.Reason,
		ActorScope: "admin:" + strconv.FormatInt(principal.InternalID, 10), IdempotencyKey: key,
		ProviderOrderID: body.OrderNo, ProductID: body.ProductID, SKUID: body.SKUID, RefundCount: body.RefundCount, ReasonCode: body.ReasonCode,
	})
	if err != nil {
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"id": refund.ID, "refund_id": refund.RefundNo, "out_refund_no": refund.RefundNo, "provider": "wechat_shop", "state": compatRefundStatus(refund.Status), "status": compatRefundStatus(refund.Status), "external_effect_id": strings.TrimPrefix(refund.EffectID, "eer_"), "real_external_call_executed": false, "delivery_proven": false})
}

func (handler *Handler) compatRefund(writer http.ResponseWriter, request *http.Request, orderRef string) {
	if !handler.writesEnabled {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	principal, err := handler.security.AuthorizeCSRF(request.Context(), request)
	if err != nil || !paymentBusinessWriteRole(principal) {
		writeError(writer, http.StatusForbidden, "forbidden")
		return
	}
	var body struct {
		Provider                  string `json:"provider"`
		OrderNo                   string `json:"order_no"`
		AmountMinor               int64  `json:"refund_amount_total"`
		Reason                    string `json:"reason"`
		TransactionIDConfirmation string `json:"transaction_id_confirmation"`
		Checked                   bool   `json:"checked"`
		Operator                  string `json:"operator,omitempty"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	if orderRef == "" || (body.OrderNo != "" && body.OrderNo != orderRef) || !body.Checked || !validTransactionConfirmation(body.TransactionIDConfirmation) {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	payment, err := handler.app.FindPayment(request.Context(), domain.ProviderWeChatPay, orderRef)
	if err != nil {
		resultError(writer, err)
		return
	}
	if !matchesVerifiedWeChatTransaction(payment, body.TransactionIDConfirmation) {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	digest := sha256.Sum256([]byte(key))
	refund, err := handler.app.RequestRefund(request.Context(), paymentport.RefundCommand{PaymentID: payment.ID, AmountMinor: body.AmountMinor, RefundNo: "RF-" + fmt.Sprintf("%x", digest[:12]), Reason: body.Reason, ActorScope: "admin:" + strconv.FormatInt(principal.InternalID, 10), IdempotencyKey: key})
	if err != nil {
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"id": refund.ID, "refund_id": refund.RefundNo, "out_refund_no": refund.RefundNo, "status": compatRefundStatus(refund.Status), "external_effect_id": strings.TrimPrefix(refund.EffectID, "eer_"), "auto_retry_allowed": false})
}

func (handler *Handler) orderEffects(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	if _, err := handler.security.Authenticate(request.Context(), request); err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return
	}
	if handler.commerceOrders == nil || handler.commerceDeliveries == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	orderRef := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(request.URL.Path, "/external-push-deliveries"), "/api/admin/wechat-pay/orders/"), "/")
	if orderRef == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	reference, err := handler.commerceOrders.CommercePushDeliveryReference(request.Context(), orderdomain.ProviderWeChatPay, orderRef)
	if err != nil {
		commerceDeliveryError(writer, err)
		return
	}
	if reference.HistoricalMappingState == "pending" {
		writeJSON(writer, http.StatusOK, map[string]any{"items": []any{}, "effects": []any{}, "total": 0, "history_mapping_state": "pending"})
		return
	}
	// A native checkout is a valid order-detail record before its first paid
	// fact exists. No paid event means no eligible commerce delivery, rather
	// than a failed Outbound read or a fabricated event ID.
	if reference.HistoricalMappingState == "current" && reference.PaidEventID == 0 {
		writeJSON(writer, http.StatusOK, map[string]any{"items": []any{}, "effects": []any{}, "total": 0, "history_mapping_state": "current"})
		return
	}
	query := outboundport.CommercePushDeliveryQuery{PaidEventID: reference.PaidEventID}
	if reference.HistoricalMappingState == "mapped" {
		query = outboundport.CommercePushDeliveryQuery{HistoricalSourceKind: reference.HistoricalSourceKind, HistoricalSourceSystem: reference.HistoricalSourceSystem, HistoricalSourceKey: reference.HistoricalSourceKey}
	}
	items, err := handler.commerceDeliveries.ListCommercePushDeliveries(request.Context(), query)
	if err != nil {
		commerceDeliveryError(writer, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		var externalEffectID, legacyDeliveryID, legacyEffectJobID any
		if item.Source == "current" {
			externalEffectID = item.EffectID
		} else {
			legacyDeliveryID, legacyEffectJobID = item.HistoricalDeliveryID, item.LegacyEffectJobID
		}
		out = append(out, map[string]any{
			"id": item.ID, "external_effect_id": externalEffectID, "legacy_delivery_id": legacyDeliveryID, "legacy_effect_job_id": legacyEffectJobID, "source": item.Source,
			"kind": "commerce_product_push", "status": item.State, "state": item.State,
			"attempt_count": item.AttemptCount, "provider_call_attempted": item.ProviderCallAttempted,
			"real_external_call_executed": item.RealExternalCallExecuted, "provider_result_received": item.ProviderResultReceived,
			"response_status": item.ResponseStatus, "result_code": item.ResultCode, "error_message": item.ErrorMessage,
			"response_body_protected": item.ResponseBodyProtected, "created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
		})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": out, "effects": out, "total": len(out), "history_mapping_state": reference.HistoricalMappingState})
}

func commerceDeliveryError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orderport.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found")
	case errors.Is(err, orderport.ErrConflict):
		writeError(writer, http.StatusConflict, "conflict")
	case errors.Is(err, paymentport.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found")
	case errors.Is(err, paymentport.ErrConflict):
		writeError(writer, http.StatusConflict, "conflict")
	default:
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
	}
}

func compatRefundStatus(status domain.RefundStatus) string {
	switch status {
	case domain.RefundRequested, domain.RefundEffectAccepted:
		return "pending_external_gate"
	case domain.RefundOutcomeUnknown:
		return "outcome_unknown"
	case domain.RefundCompleted:
		return "completed"
	case domain.RefundFinalFailed:
		return "final_failed"
	case domain.RefundHistoryRequested:
		return "history_requested"
	case domain.RefundHistoryProcessing:
		return "history_processing"
	case domain.RefundHistoryFailed:
		return "history_failed"
	case domain.RefundHistoryClosed:
		return "history_closed"
	default:
		// A legacy value with no documented meaning is neither a final failure
		// nor evidence of a completed Provider effect.
		return "unknown"
	}
}

func (handler *Handler) providerEnabled(provider domain.Provider) bool {
	switch provider {
	case domain.ProviderWeChatPay:
		return handler != nil && handler.writesEnabled
	case domain.ProviderAlipay:
		return handler != nil && handler.alipayWritesEnabled
	default:
		return false
	}
}

func checkoutStatusPath(provider domain.Provider) string {
	if provider == domain.ProviderAlipay {
		return "/api/v1/alipay/checkouts/"
	}
	return "/api/v1/wechat-pay/checkouts/"
}

func (handler *Handler) checkout(writer http.ResponseWriter, request *http.Request, routeProvider domain.Provider) {
	if !handler.providerEnabled(routeProvider) {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		writeError(writer, http.StatusUnauthorized, "payment_session_required")
		return
	}
	var body struct {
		ProductID              int64                            `json:"product_id,omitempty"`
		CouponClaimID          int64                            `json:"coupon_claim_id,omitempty"`
		ProductType            string                           `json:"product_kind,omitempty"`
		Provider               string                           `json:"provider,omitempty"`
		Channel                domain.Channel                   `json:"channel,omitempty"`
		MobileE164             string                           `json:"mobile,omitempty"`
		ContactCollectionLevel string                           `json:"contact_collection_level,omitempty"`
		RecipientName          string                           `json:"recipient_name,omitempty"`
		ProvinceCode           string                           `json:"province_code,omitempty"`
		ProvinceName           string                           `json:"province_name,omitempty"`
		CityCode               string                           `json:"city_code,omitempty"`
		CityName               string                           `json:"city_name,omitempty"`
		DistrictCode           string                           `json:"district_code,omitempty"`
		DistrictName           string                           `json:"district_name,omitempty"`
		DetailAddress          string                           `json:"detail_address,omitempty"`
		BeneficiarySelection   paymentport.BeneficiarySelection `json:"beneficiary_selection,omitempty"`
		CheckoutSessionBinding string                           `json:"checkout_session_binding"`
		// PromotionContext is an opaque /d credential. Order validates its
		// target, trusted participants and qualification in its checkout UoW;
		// the browser cannot submit a commission, amount or receiver.
		PromotionContext string `json:"promotion_context,omitempty"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	provider := body.Provider
	if provider == "" {
		provider = string(routeProvider)
	}
	if provider != string(routeProvider) {
		writeError(writer, http.StatusConflict, "payment_provider_mismatch")
		return
	}
	if !paymentport.MatchesCheckoutSessionBinding(cookie.Value, body.CheckoutSessionBinding) {
		writeError(writer, http.StatusConflict, "session_mismatch")
		return
	}
	idempotency := request.Header.Get("Idempotency-Key")
	if !validPromotionContext(body.PromotionContext) {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	activityContext := ""
	if activityCookie, cookieErr := request.Cookie(paymentport.ReferralActivityCookieName); cookieErr == nil && validReferralActivityContext(activityCookie.Value) {
		activityContext = activityCookie.Value
	}
	payment, err := handler.app.Create(request.Context(), paymentport.CreateCommand{ProductID: body.ProductID, CouponClaimID: body.CouponClaimID, ProductType: body.ProductType, Provider: provider, Channel: body.Channel, MobileE164: body.MobileE164, ContactCollectionLevel: body.ContactCollectionLevel, ShippingAddress: paymentport.ShippingAddress{RecipientName: body.RecipientName, ProvinceCode: body.ProvinceCode, ProvinceName: body.ProvinceName, CityCode: body.CityCode, CityName: body.CityName, DistrictCode: body.DistrictCode, DistrictName: body.DistrictName, DetailAddress: body.DetailAddress}, BeneficiarySelection: body.BeneficiarySelection, SessionToken: cookie.Value, CheckoutSessionBinding: body.CheckoutSessionBinding, PromotionContext: body.PromotionContext, ReferralActivityContext: activityContext, ActorScope: "public-checkout", IdempotencyKey: idempotency})
	if err != nil {
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"order_id": payment.OrderID, "merchant_order_no": payment.MerchantOrderNo, "payment_id": payment.ID, "status": payment.Status, "effect_id": payment.EffectID})
}

func validPromotionContext(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 47 || !strings.HasPrefix(value, "dpc_") {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value[4:])
	return err == nil && len(raw) == 32
}

func validReferralActivityContext(value string) bool {
	if len(value) != 47 || !strings.HasPrefix(value, "rpa_") {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value[4:])
	return err == nil && len(raw) == 32
}

func (handler *Handler) checkoutStatus(writer http.ResponseWriter, request *http.Request, provider domain.Provider, merchantOrderNo string) {
	if !handler.providerEnabled(provider) {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" || merchantOrderNo == "" {
		writeError(writer, http.StatusUnauthorized, "payment_session_required")
		return
	}
	handoff, err := handler.app.GetCheckout(request.Context(), provider, merchantOrderNo, cookie.Value)
	if err != nil {
		resultError(writer, err)
		return
	}
	result := map[string]any{"payment_id": handoff.PaymentID, "merchant_order_no": handoff.MerchantOrder, "provider": handoff.Provider, "channel": handoff.Channel, "status": handoff.Status, "ready": len(handoff.Payload) > 0, "amount_minor": handoff.AmountMinor, "currency": handoff.Currency}
	if handoff.CheckoutRestartAllowed {
		result["checkout_restart_allowed"] = true
	}
	if handoff.CheckoutAbandoned {
		result["checkout_abandoned"] = true
	}
	if handoff.PrepayState != "" {
		result["prepay_state"] = handoff.PrepayState
	}
	status := http.StatusAccepted
	if len(handoff.Payload) > 0 {
		var providerPayload map[string]any
		if json.Unmarshal(handoff.Payload, &providerPayload) != nil {
			writeError(writer, http.StatusServiceUnavailable, "unavailable")
			return
		}
		result["handoff"] = providerPayload
		result["expires_at"] = handoff.ExpiresAt
		status = http.StatusOK
	}
	if handoff.Status == domain.StatusPaid {
		// Keep the already-authorized, short-lived payer session through the
		// terminal paid state. A refresh can therefore re-read the same frozen
		// completion action, while GetCheckout still binds it to this exact
		// merchant order and cannot create a new payment or action.
		result["completion_action"] = handler.paidPurchaseAction(request.Context(), provider, handoff.OrderID, handoff.MerchantOrder)
	} else if handoff.Status == domain.StatusFailed || handoff.Status == domain.StatusCancelled {
		clearSessionCookie(writer)
	}
	writeJSON(writer, status, result)
}

func (handler *Handler) paidPurchaseAction(ctx context.Context, provider domain.Provider, orderID int64, merchantOrderNo string) map[string]any {
	if handler == nil || handler.purchaseActions == nil || handler.leadQR == nil || orderID < 1 {
		return map[string]any{"state": "unavailable"}
	}
	action, err := handler.readPaidPurchaseAction(ctx, orderID)
	if err != nil || action.OrderID != orderID {
		return map[string]any{"state": "unavailable"}
	}
	switch action.Mode {
	case productport.PaidPurchaseActionNone:
		return map[string]any{"state": "none"}
	case productport.PaidPurchaseActionRedirect:
		if !action.Enabled {
			return map[string]any{"state": "unavailable"}
		}
		if len(action.CompletionTarget) > 0 {
			if merchantOrderNo == "" {
				return map[string]any{"state": "unavailable"}
			}
			return map[string]any{"state": "available", "mode": "redirect", "redirect_url": checkoutStatusPath(provider) + url.PathEscape(merchantOrderNo) + "/completion-target"}
		}
		if action.RedirectURL == "" {
			return map[string]any{"state": "unavailable"}
		}
		return map[string]any{"state": "available", "mode": "redirect", "redirect_url": action.RedirectURL}
	case productport.PaidPurchaseActionQR:
		if !action.Enabled || action.LeadChannelID < 1 {
			return map[string]any{"state": "unavailable"}
		}
		lead, readErr := handler.leadQR.ReadPublicLeadQRCode(ctx, action.LeadChannelID)
		if readErr != nil || lead.URL == "" {
			return map[string]any{"state": "unavailable"}
		}
		return map[string]any{"state": "available", "mode": "qr", "lead_qr": map[string]string{"url": lead.URL, "title": action.LeadQRTitle, "subtitle": action.LeadQRSubtitle}}
	default:
		return map[string]any{"state": "unavailable"}
	}
}

func (handler *Handler) resolveCompletionTarget(writer http.ResponseWriter, request *http.Request, provider domain.Provider, merchantOrderNo string) {
	if !handler.providerEnabled(provider) {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" || merchantOrderNo == "" || strings.Contains(merchantOrderNo, "/") {
		writeError(writer, http.StatusUnauthorized, "payment_session_required")
		return
	}
	handoff, err := handler.app.GetCheckout(request.Context(), provider, merchantOrderNo, cookie.Value)
	if err != nil {
		resultError(writer, err)
		return
	}
	if handoff.Status != domain.StatusPaid || handoff.OrderID < 1 {
		writeError(writer, http.StatusConflict, "payment_not_paid")
		return
	}
	action, err := handler.readPaidPurchaseAction(request.Context(), handoff.OrderID)
	if err != nil || action.OrderID != handoff.OrderID || !action.Enabled || action.Mode != productport.PaidPurchaseActionRedirect || len(action.CompletionTarget) == 0 {
		writeError(writer, http.StatusNotFound, "completion_target_unavailable")
		return
	}
	resolver, ok := handler.purchaseActions.(productport.PaidPurchaseURLLinkResolver)
	if !ok {
		writeError(writer, http.StatusServiceUnavailable, "completion_target_unavailable")
		return
	}
	destination, err := resolver.ResolvePaidPurchaseURLLink(request.Context(), action)
	if err != nil || destination == "" {
		writeError(writer, http.StatusBadGateway, "completion_target_unavailable")
		return
	}
	http.Redirect(writer, request, destination, http.StatusFound)
}

func (handler *Handler) readPaidPurchaseAction(ctx context.Context, orderID int64) (productport.PaidPurchaseAction, error) {
	if handler == nil || handler.purchaseActions == nil || orderID < 1 {
		return productport.PaidPurchaseAction{}, paymentport.ErrUnavailable
	}
	if guidance, ok := handler.purchaseActions.(productport.PaidPurchaseGuidanceReader); ok {
		return guidance.ReadPaidPurchaseGuidance(ctx, orderID)
	}
	return handler.purchaseActions.ReadPaidPurchaseAction(ctx, orderID)
}

func (handler *Handler) refund(writer http.ResponseWriter, request *http.Request, rawPaymentID string) {
	if !handler.writesEnabled {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	principal, err := handler.security.AuthorizeCSRF(request.Context(), request)
	if err != nil || !paymentBusinessWriteRole(principal) {
		writeError(writer, http.StatusForbidden, "forbidden")
		return
	}
	paymentID, err := strconv.ParseInt(rawPaymentID, 10, 64)
	if err != nil || paymentID < 1 {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	var body struct {
		AmountMinor               int64  `json:"amount_minor"`
		RefundNo                  string `json:"refund_no"`
		Reason                    string `json:"reason"`
		TransactionIDConfirmation string `json:"transaction_id_confirmation"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	payment, err := handler.app.GetPayment(request.Context(), paymentID)
	if err != nil {
		resultError(writer, err)
		return
	}
	// This route spans providers. Only WeChat Pay has the verified callback
	// transaction fact this confirmation checks; preserve every other provider's
	// established contract instead of inventing a cross-provider substitute.
	if payment.Provider == domain.ProviderWeChatPay && !matchesVerifiedWeChatTransaction(payment, body.TransactionIDConfirmation) {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	refund, err := handler.app.RequestRefund(request.Context(), paymentport.RefundCommand{PaymentID: paymentID, AmountMinor: body.AmountMinor, RefundNo: body.RefundNo, Reason: body.Reason, ActorScope: "admin:" + strconv.FormatInt(principal.InternalID, 10), IdempotencyKey: request.Header.Get("Idempotency-Key")})
	if err != nil {
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"refund_id": refund.ID, "out_refund_no": refund.RefundNo, "status": refund.Status, "effect_id": refund.EffectID})
}

func validTransactionConfirmation(value string) bool {
	return value != "" && len(value) <= 200 && value == strings.TrimSpace(value)
}

// matchesVerifiedWeChatTransaction compares the operator-entered provider
// transaction_id to the digest stored from a verified payment callback or
// reconciliation. It never treats the merchant order number as a substitute.
func matchesVerifiedWeChatTransaction(payment domain.Payment, confirmation string) bool {
	if !validTransactionConfirmation(confirmation) || payment.ProviderTransactionDigest == "" {
		return false
	}
	if payment.Provider != domain.ProviderWeChatPay {
		return false
	}
	expected := effectport.Hash("wechatpay.transaction", confirmation)
	return len(payment.ProviderTransactionDigest) == len(expected) && subtle.ConstantTimeCompare([]byte(payment.ProviderTransactionDigest), []byte(expected)) == 1
}

func (handler *Handler) callback(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || handler.verifier == nil {
		callbackDiagnostic("route", request, nil)
		writeError(writer, http.StatusNotFound, "not_found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxBody))
	if err != nil {
		callbackDiagnostic("body", request, nil)
		writeError(writer, http.StatusBadRequest, "invalid_callback")
		return
	}
	headers, err := paymentprovider.CallbackHeaders(request)
	if err != nil {
		callbackDiagnostic("headers", request, body)
		writeError(writer, http.StatusUnauthorized, "invalid_signature")
		return
	}
	callback, err := handler.verifier.Verify(request.Context(), body, headers)
	if err != nil {
		callbackDiagnostic(paymentprovider.CallbackFailureStage(err), request, body)
		writeError(writer, http.StatusUnauthorized, "invalid_signature")
		return
	}
	if strings.HasSuffix(request.URL.Path, "/payment") != (callback.Kind == "payment") {
		callbackDiagnostic("route_kind", request, body)
		writeError(writer, http.StatusUnauthorized, "invalid_signature")
		return
	}
	if err = handler.app.ApplyVerifiedCallback(request.Context(), callback); err != nil {
		callbackDiagnostic(callbackApplicationFailureStage(err), request, body)
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"code": "SUCCESS", "message": "成功"})
}

func (handler *Handler) alipayCallback(writer http.ResponseWriter, request *http.Request) {
	alipayCallbackDiagnostic("arrived")
	if request.Method != http.MethodPost || handler.alipayVerifier == nil {
		alipayCallbackDiagnostic("route_unavailable")
		writeError(writer, http.StatusNotFound, "not_found")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxBody)
	if err := request.ParseForm(); err != nil {
		alipayCallbackDiagnostic("parse_failed")
		writeError(writer, http.StatusBadRequest, "invalid_callback")
		return
	}
	callback, err := handler.alipayVerifier.VerifyValues(request.Context(), request.PostForm)
	if err != nil {
		alipayCallbackDiagnostic("verification_failed")
		writeError(writer, http.StatusUnauthorized, "invalid_signature")
		return
	}
	alipayCallbackDiagnostic("verified")
	if err = handler.app.ApplyVerifiedCallback(request.Context(), callback); err != nil {
		stage := "transaction_failed"
		if errors.Is(err, paymentport.ErrNotFound) {
			stage = "order_not_found"
		}
		if errors.Is(err, paymentport.ErrConflict) || errors.Is(err, paymentport.ErrInvalid) {
			stage = "facts_rejected"
		}
		alipayCallbackDiagnostic(stage)
		resultError(writer, err)
		return
	}
	alipayCallbackDiagnostic("success")
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte("success"))
}

// Only fixed operational categories leave this boundary. No request fields,
// provider errors, signatures, merchant references or customer data are logged.
func alipayCallbackDiagnostic(stage string) {
	slog.Info("alipay_callback", "stage", stage, "route", "alipay_payment_callback")
}

// callbackDiagnostic allows production operators to distinguish ingress,
// verification, decryption, routing, and business-transaction failures
// without putting callback credentials or payment/customer identifiers in logs.
func callbackDiagnostic(stage string, request *http.Request, body []byte) {
	digest := sha256.Sum256(body)
	// The callback handler is mounted only for the two exact WeChat Pay routes.
	// Do not place request.URL.Path (an inbound value) in operational logs: the
	// stable route label is enough to correlate an ingress failure safely.
	slog.Warn("wechat_pay_callback_rejected", "stage", stage, "route", "wechat_pay_callback", "body_bytes", len(body), "body_sha256", fmt.Sprintf("%x", digest[:]))
}

// callbackApplicationFailureStage intentionally records an operationally
// actionable, fixed category rather than an error string.  Callback errors can
// wrap provider values, merchant references, or downstream consumer details.
func callbackApplicationFailureStage(err error) string {
	switch {
	case errors.Is(err, paymentport.ErrInvalid):
		return "application_invalid"
	case errors.Is(err, paymentport.ErrNotFound):
		return "application_not_found"
	case errors.Is(err, paymentport.ErrConflict):
		return "application_conflict"
	default:
		return "application_unavailable"
	}
}

func (handler *Handler) shopCallback(writer http.ResponseWriter, request *http.Request) {
	if !handler.shopWritesEnabled || handler.shopVerifier == nil {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	query, err := exactShopCallbackQuery(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_callback")
		return
	}
	if request.Method == http.MethodGet {
		echo, verifyErr := handler.shopVerifier.VerifyURL(request.Context(), query)
		if verifyErr != nil {
			writeError(writer, http.StatusUnauthorized, "invalid_signature")
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(echo))
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodGet, http.MethodPost)
		return
	}
	body, readErr := io.ReadAll(http.MaxBytesReader(writer, request.Body, 128<<10))
	if readErr != nil {
		writeError(writer, http.StatusBadRequest, "invalid_callback")
		return
	}
	callback, verifyErr := handler.shopVerifier.VerifyRefund(request.Context(), body, query)
	if verifyErr != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_signature")
		return
	}
	if applyErr := handler.app.ApplyVerifiedShopCallback(request.Context(), callback); applyErr != nil {
		resultError(writer, applyErr)
		return
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte("success"))
}

func (handler *Handler) reconcileShopRefund(writer http.ResponseWriter, request *http.Request, rawID string) {
	if !handler.shopWritesEnabled {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	principal, err := handler.security.AuthorizeCSRF(request.Context(), request)
	if err != nil || !paymentBusinessWriteRole(principal) {
		writeError(writer, http.StatusForbidden, "forbidden")
		return
	}
	refundID, err := strconv.ParseInt(rawID, 10, 64)
	var empty struct{}
	if err != nil || refundID < 1 || strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" || !decodeJSON(writer, request, &empty) {
		if err != nil || refundID < 1 || strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" {
			writeError(writer, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	refund, err := handler.app.ReconcileShopRefund(request.Context(), refundID)
	if err != nil {
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"id": refund.ID, "refund_id": refund.RefundNo, "provider": "wechat_shop", "status": compatRefundStatus(refund.Status), "state": compatRefundStatus(refund.Status), "delivery_proven": refund.Status == domain.RefundCompleted, "real_external_call_executed": true, "updated_at": refund.UpdatedAt})
}

func (handler *Handler) reconcileWeChatPay(writer http.ResponseWriter, request *http.Request, rawID string, refund bool) {
	if !handler.writesEnabled {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	principal, err := handler.security.AuthorizeCSRF(request.Context(), request)
	if err != nil || !paymentBusinessWriteRole(principal) {
		writeError(writer, http.StatusForbidden, "forbidden")
		return
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	var body struct {
		DryRun bool `json:"dry_run"`
	}
	if err != nil || id < 1 || strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" || !decodeJSON(writer, request, &body) {
		if err != nil || id < 1 || strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" {
			writeError(writer, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	if refund {
		if body.DryRun {
			writeError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
		value, callErr := handler.app.ReconcileWeChatPayRefund(request.Context(), id)
		if callErr != nil {
			resultError(writer, callErr)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"id": value.ID, "refund_id": value.RefundNo, "provider": "wechat", "status": compatRefundStatus(value.Status), "state": compatRefundStatus(value.Status), "delivery_proven": value.Status == domain.RefundCompleted, "real_external_call_executed": true, "updated_at": value.UpdatedAt})
		return
	}
	if body.DryRun {
		previewApplication, ok := handler.app.(PaymentReconciliationPreviewApplication)
		if !ok {
			writeError(writer, http.StatusServiceUnavailable, "unavailable")
			return
		}
		preview, callErr := previewApplication.PreviewReconcileWeChatPayPayment(request.Context(), id)
		if callErr != nil {
			resultError(writer, callErr)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"id": preview.PaymentID, "dry_run": true, "would_restore_paid_confirmation": preview.WouldRestorePaidConfirmation, "reason": preview.Reason})
		return
	}
	value, callErr := handler.app.ReconcileWeChatPayPayment(request.Context(), id)
	if callErr != nil {
		resultError(writer, callErr)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"id": value.ID, "merchant_order_no": value.MerchantOrderNo, "provider": "wechat", "status": value.Status, "delivery_proven": value.Status == domain.StatusPaid, "provider_query_performed": true, "real_external_call_executed": false, "updated_at": value.UpdatedAt})
}

func exactShopCallbackQuery(request *http.Request) (map[string]string, error) {
	if request == nil || request.URL == nil {
		return nil, paymentport.ErrInvalid
	}
	names := []string{"timestamp", "nonce"}
	if request.Method == http.MethodGet {
		names = append(names, "signature", "echostr")
	} else if request.Method == http.MethodPost {
		names = append(names, "msg_signature")
	} else {
		return nil, paymentport.ErrInvalid
	}
	values := request.URL.Query()
	result := make(map[string]string, len(names))
	for _, name := range names {
		items := values[name]
		if len(items) != 1 || items[0] == "" || strings.TrimSpace(items[0]) != items[0] {
			return nil, paymentport.ErrInvalid
		}
		result[name] = items[0]
	}
	return result, nil
}

func WriteTrustedSessionCookie(writer http.ResponseWriter, issued paymentsession.Issued) error {
	if issued.Token == "" || issued.ExpiresAt.IsZero() {
		return paymentsession.ErrInvalid
	}
	// Coupon and service-period public pages reuse the same opaque OAuth
	// session through their own same-origin H5 endpoints. Root scope preserves
	// that trusted session without exposing a customer or external identity.
	http.SetCookie(writer, &http.Cookie{Name: SessionCookieName, Value: issued.Token, Path: "/", Expires: issued.ExpiresAt, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	return nil
}

func clearSessionCookie(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{Name: SessionCookieName, Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) bool {
	if request.Header.Get("Content-Type") != "application/json" {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func resultError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, paymentport.ErrAlreadyPurchased):
		writeError(writer, http.StatusConflict, "already_purchased")
	case errors.Is(err, paymentport.ErrPurchasePending):
		writeError(writer, http.StatusConflict, "purchase_pending")
	case errors.Is(err, paymentport.ErrInvalid):
		writeError(writer, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, paymentport.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found")
	case errors.Is(err, paymentport.ErrSessionRequired):
		writeError(writer, http.StatusUnauthorized, "payment_session_required")
	case errors.Is(err, paymentport.ErrSessionMismatch):
		writeError(writer, http.StatusConflict, "session_mismatch")
	case errors.Is(err, paymentport.ErrConflict):
		writeError(writer, http.StatusConflict, "conflict")
	default:
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
	}
}

func methodNotAllowed(writer http.ResponseWriter, methods ...string) {
	writer.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"code": code})
}

func hasRole(roles []accessdomain.Role, expected accessdomain.Role) bool {
	for _, role := range roles {
		if role == expected {
			return true
		}
	}
	return false
}

// paymentBusinessWriteRole preserves the payment HTTP surface's existing
// administrator principal kind while treating the two daily-business roles
// alike. CSRF, idempotency, confirmation and provider safeguards remain at
// each command boundary.
func paymentBusinessWriteRole(principal accessdomain.Principal) bool {
	return principal.Kind == accessdomain.KindAdmin &&
		(hasRole(principal.Roles, accessdomain.RoleAdmin) || hasRole(principal.Roles, accessdomain.RoleSuperAdmin))
}

// paymentSuperAdminWriteRole is reserved for payment-configuration exception
// handling. It intentionally does not broaden the existing payment business
// write surface used by ordinary reconciliation and checkout operations.
func paymentSuperAdminWriteRole(principal accessdomain.Principal) bool {
	return principal.IsSuperAdmin()
}

var _ Application = (*paymentapp.Service)(nil)

// historyPayment is an authenticated native read path for inert money facts.
func (handler *Handler) historyPayment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	principal, err := handler.security.Authenticate(r.Context(), r)
	if err != nil || principal.Kind != accessdomain.KindAdmin {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	provider := domain.Provider(r.URL.Query().Get("provider"))
	merchant := r.URL.Query().Get("merchant_order_no")
	if (provider != domain.ProviderWeChatPay && provider != domain.ProviderWeChatShop) || merchant == "" || len(merchant) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	p, err := handler.app.FindPayment(r.Context(), provider, merchant)
	if err != nil {
		resultError(w, err)
		return
	}
	if !p.Historical {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	nullable := func(v int64) any {
		if v < 1 {
			return nil
		}
		return v
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": p.ID, "order_id": p.OrderID, "merchant_order_no": p.MerchantOrderNo, "provider": p.Provider, "status": p.Status, "amount_minor": p.AmountMinor, "currency": p.Currency, "record_origin": "history", "source_status": p.SourceStatus, "history_reason": p.HistoryReason, "payer_customer_id": nullable(p.PayerCustomerID), "beneficiary_customer_id": nullable(p.BeneficiaryCustomerID), "effect_eligible": false, "updated_at": p.UpdatedAt})
}
