package postgres

import (
	"context"
	"errors"
	"testing"
)

func TestRejectsMissingPoolAndTransaction(t *testing.T) {
	if _, err := Wrap(nil, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Wrap(nil) error=%v", err)
	}
	if _, err := NewUnitOfWork(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewUnitOfWork(nil) error=%v", err)
	}
	if _, err := RequireTransaction(context.Background()); !errors.Is(err, ErrTransactionNeeded) {
		t.Fatalf("RequireTransaction error=%v", err)
	}
}

func TestOpenRejectsUnsafeConfigurationWithoutConnecting(t *testing.T) {
	_, err := Open(context.Background(), Config{URL: "postgres://example", MaxConnections: 1, MinConnections: 2})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Open error=%v", err)
	}
}
