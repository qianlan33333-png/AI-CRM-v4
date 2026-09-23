package store

import (
	"errors"
	"testing"
	"time"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

func TestAdminJoinedCursorRoundTripAndRejectsLegacyOffset(t *testing.T) {
	joinedAt := time.Date(2026, 9, 18, 4, 5, 6, 700, time.UTC)
	cursor := EncodeAdminJoinedCursor(joinedAt, 42)
	if cursor == "" || cursor == "2" {
		t.Fatalf("cursor must be a nonnumeric opaque keyset value: %q", cursor)
	}
	parsed, err := ParseAdminJoinedCursor(cursor)
	if err != nil || parsed == nil || parsed.ParticipationID != 42 || !parsed.JoinedAt.Equal(joinedAt) {
		t.Fatalf("round trip cursor=%q parsed=%+v err=%v", cursor, parsed, err)
	}
	for _, invalid := range []string{"2", "rj1_", "rj1_invalid", "rj2_eyJ2IjoyfQ"} {
		if _, err = ParseAdminJoinedCursor(invalid); !errors.Is(err, referralport.ErrConflict) {
			t.Fatalf("invalid cursor %q err=%v", invalid, err)
		}
	}
}
