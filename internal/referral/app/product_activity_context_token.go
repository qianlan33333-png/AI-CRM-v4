package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
)

const productActivityContextTokenPrefix = "rpa_"

// productActivityContextTokenIssuer derives a context token from the trusted
// actor and request idempotency key. Its key and MAC domain are separate from
// invitation tokens so the two bearer credential types cannot collide.
type productActivityContextTokenIssuer struct{ key [sha256.Size]byte }

func newProductActivityContextTokenIssuer(encodedDataKey string) (productActivityContextTokenIssuer, error) {
	master, err := base64.RawStdEncoding.DecodeString(encodedDataKey)
	if err != nil || len(master) != sha256.Size {
		return productActivityContextTokenIssuer{}, errors.New("referral product activity context token key is invalid")
	}
	material := append(append([]byte(nil), master...), []byte("aicrm/referral/product-activity-context/token-key/v1")...)
	return productActivityContextTokenIssuer{key: sha256.Sum256(material)}, nil
}

func (i productActivityContextTokenIssuer) issue(actorScope string, campaignID int64, idempotencyKey string) (string, error) {
	if actorScope == "" || campaignID < 1 || idempotencyKey == "" {
		return "", errors.New("referral product activity context token input is invalid")
	}
	mac := hmac.New(sha256.New, i.key[:])
	for _, part := range []string{"aicrm/referral/product-activity-context/v1", actorScope, strconv.FormatInt(campaignID, 10), idempotencyKey} {
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(part))
	}
	return productActivityContextTokenPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
