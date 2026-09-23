package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/http"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

const groupOpsWebhookClientID = groupopsport.WebhookClientID

// groupOpsProtocolAuthenticator is the composition-owned inbound adapter.
// Group Ops receives only an opaque webhook reference and a deterministic
// idempotency key; this adapter never stores the signature or raw body.
type groupOpsProtocolAuthenticator struct {
	key    []byte
	replay groupopsport.WebhookReplayStore
	now    func() time.Time
}

func (a *groupOpsProtocolAuthenticator) AuthenticateGroupOpsWebhook(ctx context.Context, request *http.Request, resource string, body []byte) (string, error) {
	if a == nil || ctx == nil || request == nil || request.URL == nil || a.now == nil || len(a.key) < 32 || a.replay == nil || !validGroupOpsProtocolOpaque(resource) {
		return "", groupopshttp.ErrProtocolUnavailable
	}
	client, ok := singleGroupOpsHeader(request, groupopsport.WebhookClientIDHeader)
	if !ok || client != groupOpsWebhookClientID {
		return "", errors.New("group ops webhook authentication failed")
	}
	timestamp, ok := singleGroupOpsHeader(request, groupopsport.WebhookTimestampHeader)
	if !ok {
		return "", errors.New("group ops webhook authentication failed")
	}
	event, ok := singleGroupOpsHeader(request, groupopsport.WebhookNonceHeader)
	if !ok || len(event) < 16 || len(event) > 256 || !validGroupOpsProtocolEvent(event) {
		return "", errors.New("group ops webhook authentication failed")
	}
	signature, ok := singleGroupOpsHeader(request, groupopsport.WebhookSignatureHeader)
	if !ok {
		return "", errors.New("group ops webhook authentication failed")
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || timestamp != strconv.FormatInt(seconds, 10) {
		return "", errors.New("group ops webhook authentication failed")
	}
	now := a.now().UTC()
	signedAt := time.Unix(seconds, 0).UTC()
	if now.Sub(signedAt) > 5*time.Minute || signedAt.Sub(now) > time.Minute {
		return "", errors.New("group ops webhook authentication failed")
	}
	encoded := strings.TrimPrefix(signature, "sha256=")
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size || encoded != strings.ToLower(encoded) {
		return "", errors.New("group ops webhook authentication failed")
	}
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(timestamp + "\n" + event + "\n"))
	_, _ = mac.Write(body)
	if !hmac.Equal(decoded, mac.Sum(nil)) {
		return "", errors.New("group ops webhook authentication failed")
	}
	eventDigest := sha256.Sum256([]byte(event))
	payloadBytes := make([]byte, 0, len(resource)+1+len(body))
	payloadBytes = append(payloadBytes, resource...)
	payloadBytes = append(payloadBytes, '\n')
	payloadBytes = append(payloadBytes, body...)
	payloadDigest := sha256.Sum256(payloadBytes)
	created, err := a.replay.ClaimWebhookReplay(ctx, groupOpsWebhookClientID, resource, eventDigest, payloadDigest, now)
	if err != nil {
		if errors.Is(err, groupopsapp.ErrConflict) {
			return "", groupopshttp.ErrProtocolReplayConflict
		}
		return "", groupopshttp.ErrProtocolUnavailable
	}
	// A verified repeat with the same scoped event/body is intentionally passed
	// through to the runtime. Its durable run source key returns the already
	// frozen run and prevents a second effect from being accepted.
	_ = created
	// The event id is retained only as a digest in the replay store. Returning
	// a derived key keeps long (up to 256-byte) protocol event IDs within the
	// application idempotency contract and prevents the raw ID becoming an
	// actor/key log field downstream.
	return "webhook-" + hex.EncodeToString(eventDigest[:]), nil
}

func singleGroupOpsHeader(request *http.Request, name string) (string, bool) {
	values := request.Header.Values(name)
	returnValue := ""
	if len(values) == 1 {
		returnValue = values[0]
	}
	return returnValue, len(values) == 1 && returnValue != "" && strings.TrimSpace(returnValue) == returnValue
}

func validGroupOpsProtocolEvent(value string) bool {
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func validGroupOpsProtocolOpaque(value string) bool {
	if value == "" || len(value) > 128 || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}

var _ groupopshttp.ProtocolAuthenticator = (*groupOpsProtocolAuthenticator)(nil)
