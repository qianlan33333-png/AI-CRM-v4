package store

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

// AdminJoinedCursor is an immutable position in the joined-at descending
// administration streams. The cursor identifies the last delivered
// participation; later reads use a strict keyset predicate, so new joins ahead
// of that position cannot shift or duplicate the following page.
type AdminJoinedCursor struct {
	JoinedAt        time.Time
	ParticipationID int64
}

const adminJoinedCursorPrefix = "rj1_"

type adminJoinedCursorPayload struct {
	Version         int    `json:"v"`
	JoinedAt        string `json:"j"`
	ParticipationID int64  `json:"i"`
}

// ParseAdminJoinedCursor accepts only the current opaque keyset cursor.
// Legacy numeric OFFSET cursors are deliberately rejected rather than mixed
// with keyset traversal, because the two continuation semantics can skip or
// repeat activity records while new participants join.
func ParseAdminJoinedCursor(value string) (*AdminJoinedCursor, error) {
	if value == "" {
		return nil, nil
	}
	if !strings.HasPrefix(value, adminJoinedCursorPrefix) {
		return nil, referralport.ErrConflict
	}
	encoded := strings.TrimPrefix(value, adminJoinedCursorPrefix)
	if encoded == "" {
		return nil, referralport.ErrConflict
	}
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, referralport.ErrConflict
	}
	var payload adminJoinedCursorPayload
	if err = json.Unmarshal(body, &payload); err != nil || payload.Version != 1 || payload.ParticipationID < 1 || payload.JoinedAt == "" {
		return nil, referralport.ErrConflict
	}
	joinedAt, err := time.Parse(time.RFC3339Nano, payload.JoinedAt)
	if err != nil || joinedAt.IsZero() || joinedAt.UTC().Format(time.RFC3339Nano) != payload.JoinedAt {
		return nil, referralport.ErrConflict
	}
	return &AdminJoinedCursor{JoinedAt: joinedAt.UTC(), ParticipationID: payload.ParticipationID}, nil
}

// EncodeAdminJoinedCursor returns a URL-safe opaque continuation for a record
// already returned to the caller. Invalid records cannot yield a cursor.
func EncodeAdminJoinedCursor(joinedAt time.Time, participationID int64) string {
	if joinedAt.IsZero() || participationID < 1 {
		return ""
	}
	body, err := json.Marshal(adminJoinedCursorPayload{
		Version:         1,
		JoinedAt:        joinedAt.UTC().Format(time.RFC3339Nano),
		ParticipationID: participationID,
	})
	if err != nil {
		return ""
	}
	return adminJoinedCursorPrefix + base64.RawURLEncoding.EncodeToString(body)
}
