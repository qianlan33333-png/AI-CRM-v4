package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/readshare"
	"os"
	"testing"
)

func TestDashboardWorkspacePostgreSQLReceiptsCASAndRevocation(t *testing.T) {
	pool, uow, cleanup := hxcIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	raw, err := os.ReadFile(hxcMigrationPath(t, "0181_hxc_dashboard_views.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	s := NewPostgreSQL(pool)
	if _, err := s.QueryWorkspace(ctx, Query{ProjectionID: 99999, Limit: 10}); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing/pruned generation must not become zero metrics", err)
	}
	key := sha256.Sum256([]byte("create-view"))
	digest := sha256.Sum256([]byte("payload"))
	v := View{Name: "有效会员", Config: json.RawMessage(`{"query":{"filters":{"stage":["active_used"]}}}`)}
	var saved View
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		saved, _, e = s.SaveView(tx, "admin", 1, key[:], digest[:], v, false)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if saved.ID == 0 || saved.Version != 1 {
		t.Fatal("view was not persisted")
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		out, replay, e := s.SaveView(tx, "admin", 1, key[:], digest[:], v, false)
		if !replay || out.ID != saved.ID {
			t.Fatal("retry must replay the original view")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	key2 := sha256.Sum256([]byte("second"))
	saved.Version = 9
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, _, e := s.SaveView(tx, "admin", 1, key2[:], digest[:], saved, false)
		return e
	}); !errors.Is(err, ErrViewConflict) {
		t.Fatalf("stale CAS: %v", err)
	}
	other, err := s.ListViews(ctx, "admin", 2)
	if err != nil || len(other) != 0 {
		t.Fatal("another actor can read private views")
	}
	staffViews, e := s.ListViews(ctx, "staff", 1)
	if e != nil || len(staffViews) != 0 {
		t.Fatal("staff must not inherit same-number administrator views", e)
	}
	saved.Version = 1
	sentinel := errors.New("audit failure")
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, _, e := s.SaveView(tx, "admin", 1, key2[:], digest[:], saved, true)
		if e != nil {
			return e
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	views, err := s.ListViews(ctx, "admin", 1)
	if err != nil || len(views) != 1 {
		t.Fatal("failed audit must roll back deletion")
	}
	tokenDigest := readshare.Digest("test-capability")
	share := readshare.Share{ResourceID: 1, Mode: "metrics", Config: json.RawMessage(`{"query":{}}`), Fields: []string{}, Digest: tokenDigest}
	if err = uow.Within(ctx, func(tx context.Context) error { var e error; share, e = s.SaveReadShare(tx, share); return e }); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadShare(ctx, tokenDigest); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadShare(ctx, readshare.Digest("wrong")); err == nil {
		t.Fatal("invalid capability accepted")
	}
	if err = uow.Within(ctx, func(tx context.Context) error { _, e := s.SaveReadShare(tx, share); return e }); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadShare(ctx, tokenDigest); err == nil {
		t.Fatal("revoked capability remains readable")
	}
}
