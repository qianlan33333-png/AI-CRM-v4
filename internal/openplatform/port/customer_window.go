package port

import (
	"context"
	"encoding/json"
	"time"
)

type CustomerWindowItem struct {
	Key       string
	ChangedAt time.Time
	Data      json.RawMessage
}

type CustomerWindow struct {
	ID          string
	ClientID    string
	GrantDigest string
	From        time.Time
	To          time.Time
	ExpiresAt   time.Time
	Items       []CustomerWindowItem
	ItemCount   int
}

type CustomerWindowRepository interface {
	FreezeCustomerWindow(context.Context, CustomerWindow) error
	ReadCustomerWindow(context.Context, string, int, int) (CustomerWindow, error)
}
