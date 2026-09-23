package store

import (
	"errors"
	"testing"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

type zeroSettlementScanner struct{}

func (zeroSettlementScanner) Scan(...any) error { return nil }

func TestSettlementScannersRejectInvalidRowsWhenScanSucceeds(t *testing.T) {
	if _, err := scanSettlement(zeroSettlementScanner{}); !errors.Is(err, distributionport.ErrUnavailable) {
		t.Fatalf("settlement invalid row error=%v, want unavailable", err)
	}
	if _, err := scanException(zeroSettlementScanner{}); !errors.Is(err, distributionport.ErrUnavailable) {
		t.Fatalf("exception invalid row error=%v, want unavailable", err)
	}
}
