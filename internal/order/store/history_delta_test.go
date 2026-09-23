package store

import (
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	"testing"
)

func TestHistoryDeltaNeverRegressesSettledOrder(t *testing.T) {
	for _, v := range []struct {
		from, to domain.Status
		ok       bool
	}{
		{domain.StatusPaid, domain.StatusPartiallyRefunded, true},
		{domain.StatusPendingPayment, domain.StatusRefunded, true},
		{domain.StatusRefunded, domain.StatusPaid, false},
		{domain.StatusClosed, domain.StatusPaid, false},
		{domain.StatusPaid, domain.StatusPendingPayment, false},
	} {
		if historicalDeltaTransition(v.from, v.to) != v.ok {
			t.Fatalf("transition %s/%s", v.from, v.to)
		}
	}
}
