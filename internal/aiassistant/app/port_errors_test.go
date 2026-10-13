package app

import (
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	"testing"
)

func TestStablePortCommandFailuresRetainExistingSentinelIdentity(t *testing.T) {
	if ErrInvalid != ai.ErrInvalid || ErrConflict != ai.ErrConflict || ErrNotFound != ai.ErrNotFound || ErrIdempotencyConflict != ai.ErrIdempotencyConflict {
		t.Fatal("existing Owner callers lost sentinel identity")
	}
}
