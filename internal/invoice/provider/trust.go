package provider

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"strings"
	"time"

	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/cipher/decryptors"
	"github.com/wechatpay-apiv3/wechatpay-go/core/cipher/encryptors"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
)

type Trust struct {
	Mode        string
	Certificate *x509.Certificate
	PublicKeyID string
	PublicKey   *rsa.PublicKey
}

// NewTrustedClient uses one explicit, pinned trust source for response validation,
// sensitive-field encryption and notifications. Signing always uses the service
// provider's merchant credential, never the collecting merchant's credential.
func NewTrustedClient(ctx context.Context, merchant, serial string, key *rsa.PrivateKey, t Trust, opts ...core.ClientOption) (*core.Client, auth.Verifier, string, error) {
	if merchant == "" || serial == "" || key == nil || key.N == nil || key.N.BitLen() < 2048 {
		return nil, nil, "", p.ErrInvalid
	}
	verifier, encryptionID, e := trustedVerifier(t)
	if e != nil {
		return nil, nil, "", e
	}
	var authOptions []core.ClientOption
	switch t.Mode {
	case "certificate":
		visitor := core.NewCertificateMapWithList([]*x509.Certificate{t.Certificate})
		authOptions = []core.ClientOption{option.WithMerchantCredential(merchant, serial, key), option.WithVerifier(verifier), option.WithWechatPayCipher(encryptors.NewWechatPayEncryptor(visitor), decryptors.NewWechatPayDecryptor(key))}
	case "public_key":
		authOptions = []core.ClientOption{option.WithWechatPayPublicKeyAuthCipher(merchant, serial, key, t.PublicKeyID, t.PublicKey)}
	default:
		return nil, nil, "", p.ErrInvalid
	}
	c, e := core.NewClient(ctx, append(authOptions, opts...)...)
	if e != nil {
		return nil, nil, "", p.ErrInvalid
	}
	return c, verifier, encryptionID, nil
}

func trustedVerifier(t Trust) (auth.Verifier, string, error) {
	switch t.Mode {
	case "certificate":
		cert := t.Certificate
		if cert == nil || t.PublicKey != nil || t.PublicKeyID != "" || time.Now().Before(cert.NotBefore) || time.Now().After(cert.NotAfter) {
			return nil, "", p.ErrInvalid
		}
		return verifiers.NewSHA256WithRSAVerifier(core.NewCertificateMapWithList([]*x509.Certificate{cert})), strings.ToUpper(cert.SerialNumber.Text(16)), nil
	case "public_key":
		if t.Certificate != nil || t.PublicKey == nil || t.PublicKey.N == nil || t.PublicKey.N.BitLen() < 2048 || t.PublicKey.E < 3 || !strings.HasPrefix(t.PublicKeyID, "PUB_KEY_ID_") || len(t.PublicKeyID) <= len("PUB_KEY_ID_") || strings.TrimSpace(t.PublicKeyID) != t.PublicKeyID {
			return nil, "", p.ErrInvalid
		}
		return verifiers.NewSHA256WithRSAPubkeyVerifier(t.PublicKeyID, *t.PublicKey), t.PublicKeyID, nil
	default:
		return nil, "", p.ErrInvalid
	}
}
