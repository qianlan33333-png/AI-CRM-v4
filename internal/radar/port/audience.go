package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"time"
)

type AudienceFirstClick struct {
	CustomerID        customerdomain.CustomerID
	RadarID           int64
	FirstClickEventID int64
	FirstClickedAt    time.Time
	// OwnerUserID is the provider owner recorded by the first attributable
	// click. Empty means the Radar Owner has no such historical fact; callers
	// must not substitute a current WeCom relationship.
	OwnerUserID string
}
type AudienceFirstClickReader interface {
	AudienceFirstClicks(context.Context, time.Time) ([]AudienceFirstClick, error)
}
