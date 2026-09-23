package provider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
)

func TestOfficialProfitSharingSDKCertificateAuthenticationUsesConfiguredCertificate(t *testing.T) {
	merchant := newProfitSharingAuthKey(t)
	certificate := newProfitSharingPlatformCertificate(t)
	sdk, err := newOfficialProfitSharingSDKWithAuthentication(context.Background(), "merchant", "merchant-serial", merchant, ProfitSharingAuthentication{
		Mode:                ProfitSharingAuthenticationCertificate,
		PlatformCertificate: certificate,
	})
	if err != nil || sdk == nil {
		t.Fatalf("certificate SDK=%v err=%v", sdk, err)
	}
}

func TestOfficialProfitSharingSDKPublicKeyAuthenticationRequiresExplicitPair(t *testing.T) {
	merchant := newProfitSharingAuthKey(t)
	certificate := newProfitSharingPlatformCertificate(t)
	platformKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatal("fixture platform key is not RSA")
	}
	sdk, err := newOfficialProfitSharingSDKWithAuthentication(context.Background(), "merchant", "merchant-serial", merchant, ProfitSharingAuthentication{
		Mode:                ProfitSharingAuthenticationPublicKey,
		PlatformPublicKey:   platformKey,
		PlatformPublicKeyID: "PUB_KEY_ID_fixture",
	})
	if err != nil || sdk == nil {
		t.Fatalf("public-key SDK=%v err=%v", sdk, err)
	}

	for name, authentication := range map[string]ProfitSharingAuthentication{
		"missing-public-key-id": {Mode: ProfitSharingAuthenticationPublicKey, PlatformPublicKey: platformKey},
		"missing-public-key":    {Mode: ProfitSharingAuthenticationPublicKey, PlatformPublicKeyID: "PUB_KEY_ID_fixture"},
		"certificate-as-public-key": {
			Mode:                ProfitSharingAuthenticationPublicKey,
			PlatformCertificate: certificate,
			PlatformPublicKey:   platformKey,
			PlatformPublicKeyID: "PUB_KEY_ID_fixture",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newOfficialProfitSharingSDKWithAuthentication(context.Background(), "merchant", "merchant-serial", merchant, authentication); err != ErrInvalidConfig {
				t.Fatalf("err=%v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestOfficialProfitSharingSDKAutoCertificateIsExplicitAndInjectableForReadOnlyDownloadTesting(t *testing.T) {
	merchant := newProfitSharingAuthKey(t)
	certificate := newProfitSharingPlatformCertificate(t)
	autoCalls := 0
	autoClient := func(ctx context.Context, merchantID, serial string, signer *rsa.PrivateKey, apiV3Key string) (*core.Client, error) {
		autoCalls++
		if merchantID != "merchant" || serial != "merchant-serial" || len(apiV3Key) != 32 {
			t.Fatalf("unexpected explicit auto authentication input")
		}
		// This is a read-only SDK downloader stub. It lets the Provider leaf
		// test its selected lifecycle without calling the WeChat network.
		return core.NewClient(ctx, option.WithMerchantCredential(merchantID, serial, signer), option.WithWechatPayCertificate([]*x509.Certificate{certificate}))
	}
	sdk, err := newOfficialProfitSharingSDKWithAutoCertificate(context.Background(), "merchant", "merchant-serial", "0123456789abcdef0123456789abcdef", merchant, autoClient)
	if err != nil || sdk == nil || autoCalls != 1 {
		t.Fatalf("auto SDK=%v calls=%d err=%v", sdk, autoCalls, err)
	}
}

func TestOfficialProfitSharingSDKRejectsImplicitAuthenticationFallback(t *testing.T) {
	merchant := newProfitSharingAuthKey(t)
	certificate := newProfitSharingPlatformCertificate(t)
	inputs := map[string]ProfitSharingAuthentication{
		"missing-mode":                      {PlatformCertificate: certificate},
		"certificate-with-public-key-input": {Mode: ProfitSharingAuthenticationCertificate, PlatformCertificate: certificate, PlatformPublicKey: certificate.PublicKey.(*rsa.PublicKey)},
		"public-key-with-certificate":       {Mode: ProfitSharingAuthenticationPublicKey, PlatformCertificate: certificate, PlatformPublicKey: certificate.PublicKey.(*rsa.PublicKey), PlatformPublicKeyID: "PUB_KEY_ID_fixture"},
	}
	for name, authentication := range inputs {
		t.Run(name, func(t *testing.T) {
			_, err := newOfficialProfitSharingSDKWithAuthentication(context.Background(), "merchant", "merchant-serial", merchant, authentication)
			if err != ErrInvalidConfig {
				t.Fatalf("err=%v; invalid input must not fallback", err)
			}
		})
	}
}

func TestOfficialProfitSharingSDKRejectsExpiredConfiguredCertificate(t *testing.T) {
	merchant := newProfitSharingAuthKey(t)
	expired := *newProfitSharingPlatformCertificate(t)
	expired.NotAfter = time.Now().UTC().Add(-time.Second)
	if _, err := NewOfficialProfitSharingSDKWithCertificate(context.Background(), "merchant", "merchant-serial", merchant, &expired); err != ErrInvalidConfig {
		t.Fatalf("err=%v, want ErrInvalidConfig", err)
	}
}

func TestOfficialProfitSharingSDKPublicKeyVerifierRejectsMismatchedKeyID(t *testing.T) {
	certificate := newProfitSharingPlatformCertificate(t)
	key, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatal("fixture platform key is not RSA")
	}
	verifier := verifiers.NewSHA256WithRSAPubkeyVerifier("PUB_KEY_ID_fixture", *key)
	if err := verifier.Verify(context.Background(), "platform-certificate-serial", "message", "not-used-after-id-check"); err == nil {
		t.Fatal("mismatched WeChat public-key ID was accepted")
	}
}

func TestParsePlatformX509CertificatePreservesTrustedCertificate(t *testing.T) {
	certificate := newProfitSharingPlatformCertificate(t)
	raw := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	parsed, err := ParsePlatformX509Certificate(raw)
	if err != nil || parsed == nil || parsed.SerialNumber.Cmp(certificate.SerialNumber) != 0 {
		t.Fatalf("parsed=%v err=%v", parsed, err)
	}
}

func newProfitSharingAuthKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func newProfitSharingPlatformCertificate(t *testing.T) *x509.Certificate {
	t.Helper()
	key := newProfitSharingAuthKey(t)
	now := time.Now().UTC()
	raw, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber:          big.NewInt(42),
		Subject:               pkix.Name{CommonName: "profit-sharing-platform"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}, &x509.Certificate{SerialNumber: big.NewInt(42)}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}
