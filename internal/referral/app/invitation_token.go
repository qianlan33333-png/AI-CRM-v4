package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

const invitationTokenPrefix = "rfi_"

// invitationTokenIssuer deterministically derives opaque bearer tokens from
// the configured application data key and the public command idempotency key.
// This permits an exact retry to reproduce its link without persisting a raw
// bearer token in PostgreSQL.
type invitationTokenIssuer struct{ key [sha256.Size]byte }

func newInvitationTokenIssuer(encodedDataKey string) (invitationTokenIssuer, error) {
	master, err := base64.RawStdEncoding.DecodeString(encodedDataKey)
	if err != nil || len(master) != sha256.Size {
		return invitationTokenIssuer{}, errors.New("referral invitation token key is invalid")
	}
	return invitationTokenIssuer{key: sha256.Sum256(append(append([]byte(nil), master...), []byte("aicrm/referral/invitation-token/v1")...))}, nil
}

func (i invitationTokenIssuer) issue(actorScope string, campaignID int64, idempotencyKey string) (string, error) {
	if actorScope == "" || campaignID < 1 || idempotencyKey == "" {
		return "", errors.New("referral invitation token input is invalid")
	}
	mac := hmac.New(sha256.New, i.key[:])
	for _, part := range []string{"aicrm/referral/invitation-token/v1", actorScope, strconv.FormatInt(campaignID, 10), idempotencyKey} {
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(part))
	}
	return invitationTokenPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func validInvitationToken(value string) bool {
	if len(value) != len(invitationTokenPrefix)+43 || !strings.HasPrefix(value, invitationTokenPrefix) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, invitationTokenPrefix))
	return err == nil
}
