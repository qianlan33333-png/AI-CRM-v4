package store

import (
	"context"
	"errors"
	"testing"
)

func TestDashboardTerminalRefreshCannotPublishLateProjection(t *testing.T) {
	for _, terminal := range []string{"failed", "succeeded"} {
		t.Run(terminal, func(t *testing.T) {
			pool, uow, cleanup := hxcIntegrationPool(t)
			defer cleanup()
			ctx := context.Background()
			store := NewPostgreSQL(pool)
			publishCurrent := func(index int) (int64, int64) {
				t.Helper()
				var run, projection int64
				if err := pool.QueryRow(ctx, `INSERT INTO hxc_dashboard_refresh_runs(run_key,request_digest,trigger,identity_mode,status) VALUES($1,$2,'initial','inspect','publishing') RETURNING id`, terminal+"-current-"+string(rune('0'+index)), make([]byte, 32)).Scan(&run); err != nil {
					t.Fatal(err)
				}
				if err := uow.Within(ctx, func(tx context.Context) error {
					var e error
					projection, e = store.Publish(tx, run, hxcRetentionProjection(index))
					return e
				}); err != nil {
					t.Fatal(err)
				}
				return run, projection
			}
			lateRun, current := publishCurrent(1)
			wantVersions := 1
			if terminal == "succeeded" {
				_, current = publishCurrent(2)
				wantVersions = 2
			} else {
				if err := pool.QueryRow(ctx, `INSERT INTO hxc_dashboard_refresh_runs(run_key,request_digest,trigger,identity_mode,status) VALUES('late-failed',$1,'initial','inspect','running') RETURNING id`, make([]byte, 32)).Scan(&lateRun); err != nil {
					t.Fatal(err)
				}
				if err := uow.Within(ctx, func(tx context.Context) error { return store.Fail(tx, lateRun, "synthetic_failure") }); err != nil {
					t.Fatal(err)
				}
			}
			if err := uow.Within(ctx, func(tx context.Context) error { return store.MarkPublishing(tx, lateRun, 1, 0) }); !errors.Is(err, ErrNotFound) {
				t.Errorf("late transition from %s returned err=%v", terminal, err)
			}
			var lateProjection int64
			err := uow.Within(ctx, func(tx context.Context) error {
				var e error
				lateProjection, e = store.Publish(tx, lateRun, hxcRetentionProjection(3))
				return e
			})
			if !errors.Is(err, ErrNotFound) || lateProjection != 0 {
				t.Errorf("late publication from %s returned projection=%d err=%v", terminal, lateProjection, err)
			}
			var versions int
			var published int64
			if e := pool.QueryRow(ctx, `SELECT count(*),max(id) FILTER(WHERE status='published') FROM hxc_dashboard_versions`).Scan(&versions, &published); e != nil {
				t.Fatal(e)
			}
			if versions != wantVersions || published != current {
				t.Fatalf("late %s result changed current dashboard: versions=%d want=%d current=%d want=%d", terminal, versions, wantVersions, published, current)
			}
		})
	}
}
