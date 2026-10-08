package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	invoice "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice"
	ip "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/provider"
	pp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	config "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

type invoiceSetupPayment struct{}

func (invoiceSetupPayment) QueryPayment(context.Context, string) (pp.WeChatPayPaymentQuery, error) {
	return pp.WeChatPayPaymentQuery{}, pp.ErrUnavailable
}
func (invoiceSetupPayment) QueryRefund(context.Context, string) (pp.WeChatPayRefundQuery, error) {
	return pp.WeChatPayRefundQuery{}, pp.ErrUnavailable
}
func TestInvoiceCompositionUsesSeparateServicePublicKey(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	public, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "private.pem")
	publicPath := filepath.Join(dir, "public.pem")
	raw, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}), 0600)
	raw, e = x509.MarshalPKIXPublicKey(&public.PublicKey)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: raw}), 0600)
	cfg := config.Runtime{Invoice: config.Invoice{Enabled: true, AuthMode: "public_key", ServiceMerchantID: "service-fixture", MerchantSerial: "merchant-serial", PrivateKeyPath: privatePath, PublicKeyID: "PUB_KEY_ID_fixture", PublicKeyPath: publicPath, APIV3Key: strings.Repeat("k", 32)}}
	cfg.WeChatPay.MerchantID = "collecting-fixture"
	cfg.WeChatPay.H5AppID = "wx-fixture"
	s := &invoice.Service{}
	if e := enableInvoiceProvider(context.Background(), s, invoiceSetupPayment{}, cfg); e != nil {
		t.Fatal(e)
	}
	w, ok := s.Provider.(*ip.WeChat)
	if !ok || w.MerchantID != "service-fixture" || w.PlatformSerial != "PUB_KEY_ID_fixture" || w.OA.AppID != "wx-fixture" {
		t.Fatal("composition mixed service and collection credentials")
	}
	cfg.Invoice.AuthMode = "certificate"
	if e := enableInvoiceProvider(context.Background(), s, invoiceSetupPayment{}, cfg); e == nil {
		t.Fatal("invalid certificate mode accepted")
	}
	cfg.Invoice.Enabled = false
	cfg.Invoice.PrivateKeyPath = "/missing"
	if e := enableInvoiceProvider(context.Background(), s, invoiceSetupPayment{}, cfg); e != nil {
		t.Fatal("disabled provider loaded credentials")
	}
	// The callback-only bootstrap neither reads the missing signing key nor
	// requires payment, official-account or finance write capabilities.
	cfg.Invoice.CallbackEnabled = true
	cfg.Invoice.AuthMode = "public_key"
	cfg.WeChatPay.H5AppID = ""
	s = &invoice.Service{}
	if e := enableInvoiceProvider(context.Background(), s, nil, cfg); e != nil {
		t.Fatal("callback bootstrap failed", e)
	}
	if s.CallbackVerifier == nil || s.Provider != nil || s.ProviderAdapter() != nil || s.Config.Enabled {
		t.Fatal("callback initialization enabled invoice business writes")
	}
}

func TestInvoiceFinanceMappingRequiresExplicitTaxFacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "taxes.json")
	for _, raw := range []string{`{"course":{"code":"3010101020203000000","goods_name":"fixture"}}`, `{"course":{"code":"3010101020203000000","rate":null,"goods_name":"fixture"}}`, `{"course":{"code":"not-a-tax-code","rate":0,"goods_name":"fixture"}}`, `{}`} {
		os.WriteFile(path, []byte(raw), 0600)
		if _, e := loadInvoiceTaxes(path); e == nil {
			t.Fatal("incomplete finance configuration accepted")
		}
	}
	os.WriteFile(path, []byte(`{"course":{"code":"3010101020203000000","rate":0,"goods_name":"fixture"}}`), 0600)
	values, e := loadInvoiceTaxes(path)
	if e != nil || values["course"].Rate != 0 {
		t.Fatal("explicit zero tax rejected", e)
	}
}
