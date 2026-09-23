package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
)

// promotionTokenIssuer derives opaque, bearer-safe referral tokens from the
// configured application data key. The command idempotency key is part of the
// authenticated input, so an exact replay can return the original URL without
// persisting a recoverable bearer token.
type promotionTokenIssuer struct{ key [sha256.Size]byte }

func newPromotionTokenIssuer(encodedDataKey string) (promotionTokenIssuer, error) {
	master, err := base64.RawStdEncoding.DecodeString(encodedDataKey)
	if err != nil || len(master) != sha256.Size {
		return promotionTokenIssuer{}, errors.New("distribution promotion token key is invalid")
	}
	return promotionTokenIssuer{key: sha256.Sum256(append(append([]byte(nil), master...), []byte("aicrm/distribution/promotion-token/v1")...))}, nil
}

func (i promotionTokenIssuer) issue(actorScope string, productID int64, productType distributiondomain.ProductType, idempotencyKey string) (string, error) {
	if actorScope == "" || productID < 1 || !productType.Valid() || idempotencyKey == "" {
		return "", errors.New("distribution promotion token input is invalid")
	}
	mac := hmac.New(sha256.New, i.key[:])
	for _, part := range []string{"aicrm/distribution/promotion-token/v1", actorScope, strconv.FormatInt(productID, 10), string(productType), idempotencyKey} {
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(part))
	}
	return "dpc_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
