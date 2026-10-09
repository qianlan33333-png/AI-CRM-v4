package main

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	invoice "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	invoiceprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/provider"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	config "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/riverqueue/river"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
	"net/http"
	"os"
	"strings"
)

type invoiceIdentity interface {
	identityport.LockedCanonicalLineageReader
	identityport.PaymentIdentityReader
	identityport.VerifiedIdentitySubjectReader
}

func composeInvoice(uow platformport.UnitOfWork, sessions paymentport.SessionReader, identities invoiceIdentity, orders orderport.SelfReader, refunds paymentport.RefundExposureReader, effects effectport.TransactionalAccepter, jobs *river.Client[pgx.Tx], cfg config.Runtime) (*invoice.Service, *invoice.Handler, error) {
	c := p.Config{Enabled: cfg.Invoice.Enabled, MerchantID: cfg.WeChatPay.MerchantID, AppID: cfg.WeChatPay.H5AppID, AppScope: cfg.WeChatPay.H5AppScope, SellerName: cfg.Invoice.SellerName, SellerTaxID: cfg.Invoice.SellerTaxID, BillingPersonID: cfg.Invoice.BillingPersonID}
	if c.Enabled {
		var e error
		c.Taxes, e = loadInvoiceTaxes(cfg.Invoice.TaxConfigPath)
		if e != nil {
			return nil, nil, e
		}
	}
	stateReader, ok := effects.(effectport.WithinReader)
	if !ok {
		return nil, nil, p.ErrInvalid
	}
	finality, ok := effects.(effectport.InvoiceFinality)
	if !ok {
		return nil, nil, p.ErrInvalid
	}
	invoicePayments, ok := refunds.(paymentport.InvoicePaymentReader)
	if !ok {
		return nil, nil, p.ErrInvalid
	}
	s := &invoice.Service{Finality: finality, EffectStates: stateReader, UoW: uow, Sessions: sessions, Lineage: identities, Identities: identities, Subjects: identities, Payments: invoicePayments, Orders: orders, Refunds: refunds, Effects: effects, Jobs: invoice.QueryEnqueuer{Client: jobs}, Config: c}
	return s, &invoice.Handler{Service: s, Origins: []string{cfg.PublicOrigin, h5PublicOrigin(cfg)}, Dist: "web/dist"}, nil
}

func loadInvoiceTaxes(path string) (map[string]p.Tax, error) {
	raw, e := os.ReadFile(path)
	var taxes map[string]p.Tax
	if e != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &taxes) != nil || len(taxes) == 0 {
		return nil, errors.New("invoice finance configuration unavailable")
	}
	var values map[string]map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil, p.ErrInvalid
	}
	for code, tax := range taxes {
		if code == "" || !tax.Valid() || values[code]["rate"] == nil || string(values[code]["rate"]) == "null" {
			return nil, errors.New("invoice tax configuration incomplete")
		}
	}
	return taxes, nil
}

type invoicePaymentVerifier struct {
	provider paymentport.WeChatPayReconciler
	merchant string
}

func (v invoicePaymentVerifier) VerifyInvoicePayment(ctx context.Context, s p.Snapshot) error {
	if v.merchant != s.MerchantID {
		return p.ErrConflict
	}
	q, e := v.provider.QueryPayment(ctx, s.OrderNo)
	if e != nil {
		return p.ErrUnavailable
	}
	if q.Status != "SUCCESS" || q.Currency != "CNY" || q.AmountMinor != s.Amount || q.TransactionReference != s.TransactionID {
		return p.ErrIneligible
	}
	return nil
}
func enableInvoiceProvider(ctx context.Context, s *invoice.Service, payments paymentport.WeChatPayReconciler, cfg config.Runtime) error {
	if !cfg.Invoice.Enabled && !cfg.Invoice.CallbackEnabled {
		return nil
	}
	var raw []byte
	var e error
	trust := invoiceprovider.Trust{Mode: cfg.Invoice.AuthMode}
	switch trust.Mode {
	case "public_key":
		trust.PublicKeyID = cfg.Invoice.PublicKeyID
		trust.PublicKey, e = utils.LoadPublicKeyWithPath(cfg.Invoice.PublicKeyPath)
	case "certificate":
		raw, e = os.ReadFile(cfg.Invoice.PlatformCertPath)
		if e == nil {
			trust.Certificate, e = paymentprovider.ParsePlatformX509Certificate(raw)
		}
	default:
		return p.ErrInvalid
	}
	if e != nil {
		return errors.New("invoice trust material unavailable")
	}
	callback, e := invoiceprovider.NewNotificationVerifier(cfg.Invoice.ServiceMerchantID, cfg.Invoice.APIV3Key, trust)
	if e != nil {
		return e
	}
	s.CallbackVerifier = callback
	if !cfg.Invoice.Enabled {
		return nil
	}
	raw, e = os.ReadFile(cfg.Invoice.PrivateKeyPath)
	if e != nil {
		return errors.New("invoice signing key unavailable")
	}
	signer, e := paymentprovider.ParseMerchantPrivateKey(raw)
	if e != nil {
		return p.ErrInvalid
	}
	key, ok := signer.(*rsa.PrivateKey)
	if !ok {
		return p.ErrInvalid
	}
	provider, e := invoiceprovider.NewWithTrust(ctx, cfg.Invoice.ServiceMerchantID, cfg.Invoice.MerchantSerial, cfg.Invoice.APIV3Key, key, trust, &invoiceprovider.OfficialAccount{AppID: cfg.WeChatPay.H5AppID, Secret: cfg.WeChatPay.H5AppSecret}, s, invoicePaymentVerifier{provider: payments, merchant: cfg.WeChatPay.MerchantID})
	if e != nil {
		return e
	}
	s.Provider = provider
	return nil
}

type invoiceRefundExposureFanout struct {
	invoice, distribution paymentport.RefundExposureConsumer
}

func (f invoiceRefundExposureFanout) ConsumeRefundExposureWithin(ctx context.Context, event paymentport.RefundExposureEvent) error {
	if f.invoice != nil {
		if e := f.invoice.ConsumeRefundExposureWithin(ctx, event); e != nil {
			return e
		}
	}
	if f.distribution != nil {
		return f.distribution.ConsumeRefundExposureWithin(ctx, event)
	}
	return nil
}
func mountInvoice(next http.Handler, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/my/orders" || strings.HasPrefix(r.URL.Path, "/my/orders-assets/") || r.URL.Path == "/api/h5/me/orders" || strings.HasPrefix(r.URL.Path, "/api/h5/me/orders/") || r.URL.Path == "/api/h5/me/invoice-jssdk" || r.URL.Path == "/api/h5/me/invoices" || strings.HasPrefix(r.URL.Path, "/api/h5/me/invoices/") || r.URL.Path == "/api/callbacks/wechat-invoice" {
			h.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
