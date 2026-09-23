package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"time"
)

type AudienceEntry struct {
	CustomerID customerdomain.CustomerID
	ChannelID  int64
	// ChannelCode is the immutable business code selected by the legacy form;
	// ChannelID is retained only as an Owner-local lookup key.
	ChannelCode                   string
	OwnerReference                string
	OwnerStaffID                  *int64
	FirstEnteredAt, LastEnteredAt time.Time
}
type AudienceEntryReader interface {
	AudienceEntries(context.Context, time.Time) ([]AudienceEntry, error)
}
