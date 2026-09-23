package domain

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestPackageLifecycleCopyAndCAS(t *testing.T) {
	now := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	groupID := int64(12)
	p, err := NewPackage(" NEW-CUSTOMERS ", " 新客人群 ", &groupID, 7, now)
	if err != nil || p.Code != "new-customers" || p.Lifecycle != Paused || p.Version != 1 {
		t.Fatalf("package=%+v err=%v", p, err)
	}
	if err = p.UpdateDetails("新客 30 天", nil, 2, 7, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected CAS conflict, got %v", err)
	}
	if err = p.Transition(Active, 1, 7, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = p.UpdateDetails("renamed active package", nil, 2, 7, now.Add(2*time.Minute)); err != nil || p.Lifecycle != Active || p.Version != 3 {
		t.Fatalf("expected metadata edit without lifecycle change, got %v", err)
	}
	copy, err := p.Copy("new-customers-copy", "新客人群副本", 8, now.Add(3*time.Minute))
	if err != nil || copy.ID != 0 || copy.Version != 1 || copy.Lifecycle != Paused || copy.CurrentConfigurationVersionID != nil {
		t.Fatalf("copy=%+v err=%v", copy, err)
	}
	if err = p.Transition(Archived, 3, 7, now.Add(4*time.Minute)); err != nil || p.ArchivedAt == nil {
		t.Fatalf("archive=%+v err=%v", p, err)
	}
	if err = p.Transition(Paused, 4, 7, now.Add(5*time.Minute)); !errors.Is(err, ErrArchived) {
		t.Fatalf("expected archived terminal state, got %v", err)
	}
}

func TestConfigurationVersionIsImmutableInputSnapshot(t *testing.T) {
	definition := json.RawMessage(`{"schema_version":1,"expression":{"kind":"all"}}`)
	v, err := NewConfigurationVersion(4, 1, definition, "0 1 * * *", "legacy_custom", 9, time.Now().UTC())
	if err != nil || v.Digest == ([32]byte{}) || v.SchemaVersion != 1 {
		t.Fatalf("version=%+v err=%v", v, err)
	}
	definition[0] = '['
	if v.Definition[0] != '{' {
		t.Fatal("configuration retained caller-owned mutable bytes")
	}
	for _, invalid := range []json.RawMessage{nil, json.RawMessage(`[]`), json.RawMessage(`null`), json.RawMessage(`{"broken"`)} {
		if _, err = NewConfigurationVersion(4, 1, invalid, "", "manual", 9, time.Now().UTC()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("definition %q accepted: %v", invalid, err)
		}
	}
}

func TestGroupAndPackageValidation(t *testing.T) {
	now := time.Now().UTC()
	if _, err := NewGroup(" ", 0, 1, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank group accepted: %v", err)
	}
	if _, err := NewPackage("Bad Code", "name", nil, 1, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid code accepted: %v", err)
	}
}
