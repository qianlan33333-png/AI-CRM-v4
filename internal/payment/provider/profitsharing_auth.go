package provider

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/cipher/decryptors"
	"github.com/wechatpay-apiv3/wechatpay-go/core/cipher/encryptors"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/profitsharing"
)

// ProfitSharingAuthenticationMode makes the trust material used by the
// official SDK explicit. It deliberately does not choose one mode from the
// presence or absence of another credential.
type ProfitSharingAuthenticationMode string

const (
	// ProfitSharingAuthenticationCertificate uses a configured, trusted WeChat
	// platform X.509 certificate. It does not start the SDK downloader or make
	// a network request during client construction.
	ProfitSharingAuthenticationCertificate ProfitSharingAuthenticationMode = "certificate"
	// ProfitSharingAuthenticationPublicKey uses a configured WeChat Pay public
	// key and its matching WeChat public-key ID.
	ProfitSharingAuthenticationPublicKey ProfitSharingAuthenticationMode = "public_key"
)

// ProfitSharingAuthentication is transient Composition input. Each mode
// accepts exactly one trust-material shape so that, for example, a platform
// certificate serial can never be treated as a WeChat public-key ID.
type ProfitSharingAuthentication struct {
	Mode ProfitSharingAuthenticationMode

	PlatformCertificate *x509.Certificate
	PlatformPublicKey   *rsa.PublicKey
	PlatformPublicKeyID string
}

// ParsePlatformX509Certificate preserves the complete configured platform
// certificate for SDK certificate authentication. ParsePlatformCertificate is
// retained for ordinary Payment's narrow response-verification key path.
func ParsePlatformX509Certificate(raw []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, ErrInvalidConfig
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !validPlatformCertificate(certificate) {
		return nil, ErrInvalidConfig
	}
	return certificate, nil
}

func validPlatformCertificate(certificate *x509.Certificate) bool {
	if certificate == nil || certificate.SerialNumber == nil || certificate.SerialNumber.Sign() <= 0 || !certificate.NotBefore.Before(time.Now().UTC()) || !certificate.NotAfter.After(time.Now().UTC()) {
		return false
	}
	key, ok := certificate.PublicKey.(*rsa.PublicKey)
	return ok && key != nil && key.N != nil && key.N.BitLen() >= 2048
}

func validProfitSharingAuthentication(value ProfitSharingAuthentication) bool {
	switch value.Mode {
	case ProfitSharingAuthenticationCertificate:
		return validPlatformCertificate(value.PlatformCertificate) && value.PlatformPublicKey == nil && value.PlatformPublicKeyID == ""
	case ProfitSharingAuthenticationPublicKey:
		return value.PlatformCertificate == nil && validProfitSharingPublicKey(value.PlatformPublicKey) && validProfitSharingPublicKeyID(value.PlatformPublicKeyID)
	default:
		return false
	}
}

func validProfitSharingPublicKey(key *rsa.PublicKey) bool {
	return key != nil && key.N != nil && key.N.BitLen() >= 2048
}

func validProfitSharingPublicKeyID(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

type profitSharingAutoClientFactory func(context.Context, string, string, *rsa.PrivateKey, string) (*core.Client, error)

func newProfitSharingAutoClient(ctx context.Context, merchantID, serial string, signer *rsa.PrivateKey, apiV3Key string) (*core.Client, error) {
	return core.NewClient(ctx, option.WithWechatPayAutoAuthCipher(merchantID, serial, signer, apiV3Key))
}

// NewOfficialProfitSharingSDKWithAuthentication constructs an official SDK
// client with only the caller-selected authentication mode. It never falls
// back across certificate, public-key, and downloader modes.
func NewOfficialProfitSharingSDKWithAuthentication(ctx context.Context, merchantID, serial string, signer *rsa.PrivateKey, authentication ProfitSharingAuthentication) (*OfficialProfitSharingSDK, error) {
	return newOfficialProfitSharingSDKWithAuthentication(ctx, merchantID, serial, signer, authentication)
}

func newOfficialProfitSharingSDKWithAuthentication(ctx context.Context, merchantID, serial string, signer *rsa.PrivateKey, authentication ProfitSharingAuthentication) (*OfficialProfitSharingSDK, error) {
	if !validProfitSharingSDKIdentity(merchantID, serial, signer) || !validProfitSharingAuthentication(authentication) {
		return nil, ErrInvalidConfig
	}

	var (
		client *core.Client
		err    error
	)
	switch authentication.Mode {
	case ProfitSharingAuthenticationCertificate:
		// v0.2.21 exposes no direct ClientOption that receives a
		// CertificateVisitor. Build the visitor explicitly and share it between
		// response verification and field encryption. This is equivalent to the
		// SDK's deprecated certificate-list helper without any downloader.
		visitor := core.NewCertificateMapWithList([]*x509.Certificate{authentication.PlatformCertificate})
		client, err = core.NewClient(ctx,
			option.WithMerchantCredential(merchantID, serial, signer),
			option.WithVerifier(verifiers.NewSHA256WithRSAVerifier(visitor)),
			option.WithWechatPayCipher(encryptors.NewWechatPayEncryptor(visitor), decryptors.NewWechatPayDecryptor(signer)),
		)
	case ProfitSharingAuthenticationPublicKey:
		client, err = core.NewClient(ctx, option.WithWechatPayPublicKeyAuthCipher(merchantID, serial, signer, authentication.PlatformPublicKeyID, authentication.PlatformPublicKey))
	default:
		return nil, ErrInvalidConfig
	}
	if err != nil || client == nil {
		return nil, ErrInvalidConfig
	}
	return &OfficialProfitSharingSDK{receivers: profitsharing.ReceiversApiService{Client: client}, orders: profitsharing.OrdersApiService{Client: client}}, nil
}

// newOfficialProfitSharingSDKWithAutoCertificate retains the earlier explicit
// AutoAuth entry point. SDK v0.2.21 registers and immediately reads through a
// certificate downloader here; it is intentionally outside the static
// authentication selector so Composition cannot silently fall back to it.
func newOfficialProfitSharingSDKWithAutoCertificate(ctx context.Context, merchantID, serial, apiV3Key string, signer *rsa.PrivateKey, autoClient profitSharingAutoClientFactory) (*OfficialProfitSharingSDK, error) {
	if !validProfitSharingSDKIdentity(merchantID, serial, signer) || strings.TrimSpace(apiV3Key) != apiV3Key || len(apiV3Key) != 32 || autoClient == nil {
		return nil, ErrInvalidConfig
	}
	client, err := autoClient(ctx, merchantID, serial, signer, apiV3Key)
	if err != nil || client == nil {
		return nil, ErrInvalidConfig
	}
	return &OfficialProfitSharingSDK{receivers: profitsharing.ReceiversApiService{Client: client}, orders: profitsharing.OrdersApiService{Client: client}}, nil
}

func validProfitSharingSDKIdentity(merchantID, serial string, signer *rsa.PrivateKey) bool {
	return strings.TrimSpace(merchantID) == merchantID && merchantID != "" && strings.TrimSpace(serial) == serial && serial != "" && signer != nil && signer.N != nil && signer.N.BitLen() >= 2048
}
