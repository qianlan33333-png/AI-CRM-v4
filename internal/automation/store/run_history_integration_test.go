package store

import (
	"context"
	"strconv"
	"testing"
	"time"

	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLRunHistoryFiltersBeforePaginationAndCountsAllPages(t *testing.T) {
	native, cleanup := automationRuntimeIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	// All selected-package records precede over 100 newer records in another
	// package. The previous global-page/filter-in-browser implementation hid them.
	insert := `INSERT INTO automation_runs(package_id,package_version,snapshot_id,agent_id,agent_published_version,binding_version,sender_set_version,preview_digest,state,target_count,skipped_count,created_by,created_at,updated_at) SELECT $1,1,1,1,1,1,1,decode(repeat('1a',32),'hex'),'completed',1,0,7,clock_timestamp(),clock_timestamp() FROM generate_series(1,$2::int)`
	for _, fixture := range [][2]int{{33, 205}, {34, 150}} {
		if _, err = native.Exec(ctx, insert, fixture[0], fixture[1]); err != nil {
			t.Fatal(err)
		}
	}
	read := func(packageID, cursor int64, limit int) automationapp.RunPage {
		t.Helper()
		var page automationapp.RunPage
		if err := uow.Within(ctx, func(tx context.Context) error {
			var e error
			page, e = repo.ListRunPage(tx, packageID, cursor, limit)
			return e
		}); err != nil {
			t.Fatal(err)
		}
		return page
	}
	all := read(0, 0, 100)
	if all.Total != 355 || len(all.Items) != 100 || all.Items[0].PackageID != 34 {
		t.Fatalf("global compatibility: %+v", all)
	}
	seen := map[int64]bool{}
	cursor := int64(0)
	for _, size := range []int{100, 100, 5} {
		page := read(33, cursor, 100)
		if page.Total != 205 || len(page.Items) != size {
			t.Fatalf("page total/size=%d/%d want=205/%d", page.Total, len(page.Items), size)
		}
		prior := int64(206)
		if cursor > 0 {
			prior = cursor
		}
		for _, r := range page.Items {
			if r.PackageID != 33 || seen[r.ID] || r.ID >= prior {
				t.Fatalf("leaked/duplicate/unordered run=%+v", r)
			}
			seen[r.ID] = true
			prior = r.ID
		}
		if size == 5 && page.NextCursor != "" {
			t.Fatal("last page must have no continuation")
		}
		if page.NextCursor != "" {
			cursor, err = strconv.ParseInt(page.NextCursor, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(seen) != 205 {
		t.Fatalf("historical runs missing: %d", len(seen))
	}
	exhausted := read(33, 1, 100)
	if exhausted.Total != 205 || len(exhausted.Items) != 0 || exhausted.NextCursor != "" {
		t.Fatalf("exhausted page lost historical total: %+v", exhausted)
	}
	empty := read(999, 0, 100)
	if empty.Total != 0 || len(empty.Items) != 0 || empty.NextCursor != "" {
		t.Fatalf("empty package leaked history: %+v", empty)
	}
	var unchanged int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_runs`).Scan(&unchanged); err != nil || unchanged != 355 {
		t.Fatalf("read path changed history: count=%d err=%v", unchanged, err)
	}
}
