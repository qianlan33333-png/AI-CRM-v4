package config

import (
	"errors"
	"io"
	"os"
	"strings"
)

// ValidateInvoice never infers a trust mode from a partially supplied key.
// Certificate is the legacy default; public_key must be selected explicitly.
func ValidateInvoice(c Invoice) error {
	values := []string{c.ServiceMerchantID, c.MerchantSerial, c.PrivateKeyPath, c.SellerName, c.SellerTaxID, c.BillingPersonID, c.TaxConfigPath}
	for _, v := range values {
		if v == "" || strings.TrimSpace(v) != v {
			return errors.New("invoice credentials and finance configuration incomplete")
		}
	}
	return ValidateInvoiceCallback(c)
}

// Callback setup precedes business enablement. It needs only the service scope,
// pinned notification trust and APIv3 decryption key, not finance or a signer.
func ValidateInvoiceCallback(c Invoice) error {
	if c.ServiceMerchantID == "" || strings.TrimSpace(c.ServiceMerchantID) != c.ServiceMerchantID {
		return errors.New("invoice callback service merchant incomplete")
	}
	if len(c.APIV3Key) != 32 || strings.TrimSpace(c.APIV3Key) != c.APIV3Key {
		return errors.New("invoice APIv3 key must contain exactly 32 bytes")
	}
	switch c.AuthMode {
	case "certificate":
		if c.PlatformCertPath == "" || strings.TrimSpace(c.PlatformCertPath) != c.PlatformCertPath || c.PublicKeyID != "" || c.PublicKeyPath != "" {
			return errors.New("invoice certificate mode requires only platform certificate trust")
		}
	case "public_key":
		if !strings.HasPrefix(c.PublicKeyID, "PUB_KEY_ID_") || len(c.PublicKeyID) <= len("PUB_KEY_ID_") || strings.TrimSpace(c.PublicKeyID) != c.PublicKeyID || c.PublicKeyPath == "" || strings.TrimSpace(c.PublicKeyPath) != c.PublicKeyPath || c.PlatformCertPath != "" {
			return errors.New("invoice public-key mode requires explicit public-key id and path without a platform certificate")
		}
	default:
		return errors.New("invalid invoice authentication mode")
	}
	return nil
}

func readInvoiceKey(value, path string) (string, error) {
	if path == "" {
		return value, nil
	}
	if value != "" {
		return "", errors.New("invoice APIv3 key must use exactly one source")
	}
	f, e := os.Open(path)
	if e != nil {
		return "", errors.New("invoice APIv3 key file unavailable")
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 34 {
		return "", errors.New("invoice APIv3 key file must be private and contain 32 bytes")
	}
	raw, e := io.ReadAll(io.LimitReader(f, 35))
	if e != nil {
		return "", errors.New("invoice APIv3 key file unreadable")
	}
	// Accept a single terminal line ending from a text editor, never trim key bytes.
	key := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if len(key) != 32 || strings.TrimSpace(key) != key {
		return "", errors.New("invoice APIv3 key file must contain 32 bytes")
	}
	return key, nil
}
