package domain

import (
	"errors"
	"testing"
	"time"
)

func TestGroupUpdateUsesCAS(t *testing.T) {
	now := time.Now().UTC()
	group, err := NewGroup("新客", 1, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = group.Update("老客", 2, 2, 8, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if err = group.Update("老客", 2, 1, 8, now.Add(time.Minute)); err != nil || group.Version != 2 || group.UpdatedBy != 8 {
		t.Fatalf("group=%+v err=%v", group, err)
	}
}
