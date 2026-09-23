package store

import (
	"context"
	"errors"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
)

func TestSharedFactsRejectsMoreThanBoundedUniqueCustomerIDsBeforeQuery(t *testing.T) {
	ids := make([]customerdomain.CustomerID, hxcport.MaxSharedFactsCustomerIDs+1)
	for i := range ids {
		ids[i] = customerdomain.CustomerID(i + 1)
	}
	_, err := (&PostgreSQL{}).SharedFacts(context.Background(), ids)
	if !errors.Is(err, hxcport.ErrSharedFactsBatchTooLarge) {
		t.Fatalf("oversized canonical read error=%v", err)
	}
}
