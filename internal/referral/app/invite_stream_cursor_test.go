package app

import (
	"encoding/base64"
	"errors"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	"strings"
	"testing"
	"time"
)

func TestInviteStreamCursorRejectsCrossIdentityCampaignAndTampering(t *testing.T) {
	issuer, err := newInvitationTokenIssuer(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{tokens: issuer}
	original := inviteStreamCursor{Watermark: 100, Total: 50, At: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC), ID: 17}
	token, err := s.writeInviteStreamCursor(original, 101, 7)
	if err != nil {
		t.Fatal(err)
	}
	value, err := s.readInviteStreamCursor(token, 101, 7)
	if err != nil || *value != original {
		t.Fatalf("roundtrip=%+v err=%v", value, err)
	}
	for _, test := range []struct {
		token           string
		actor, campaign int64
	}{{token, 102, 7}, {token, 101, 8}, {"50", 101, 7}, {inviteStreamPrefix, 101, 7}, {strings.TrimSuffix(token, token[len(token)-4:]) + "AAAA", 101, 7}} {
		if _, err = s.readInviteStreamCursor(test.token, test.actor, test.campaign); !errors.Is(err, referralport.ErrConflict) {
			t.Fatalf("cursor accepted incompatible scope/tampering: %v", err)
		}
	}
	if s.inviteEntryKey(101, 7, 17) == s.inviteEntryKey(102, 7, 17) || s.inviteEntryKey(101, 7, 17) == s.inviteEntryKey(101, 8, 17) {
		t.Fatal("entry key crosses identity/campaign boundary")
	}
}
