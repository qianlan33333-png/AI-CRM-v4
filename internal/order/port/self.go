package port

import (
	"context"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	"time"
)

// SelfQuery is supplied only by a trusted session/OneID consumer. Empty
// CustomerIDs must fail closed. The cursor cannot change the owner predicate.
type SelfQuery struct {
	CustomerIDs             []int64
	State, Keyword, OrderNo string
	From, Until             *time.Time
	AfterAt                 time.Time
	AfterID                 int64
	Limit                   int32
}
type SelfOrder struct {
	Order           domain.Snapshot
	ConfirmedPaidAt *time.Time
}
type SelfReader interface {
	ListSelfWithin(context.Context, SelfQuery) ([]SelfOrder, error)
	GetSelfWithin(context.Context, int64, []int64, bool) (SelfOrder, error)
}
