package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

type backlogRebaseSource struct {
	cutover                               time.Time
	unified, unavailable, failCurrentOnce bool
	calls                                 int
}

func (s *backlogRebaseSource) Evaluate(_ context.Context, _ segmentport.Definition, at time.Time) (segmentport.Evaluation, error) {
	s.calls++
	if s.unavailable {
		return segmentport.Evaluation{}, ErrUnavailable
	}
	if s.failCurrentOnce && !at.Before(s.cutover) {
		s.failCurrentOnce = false
		return segmentport.Evaluation{}, ErrUnavailable
	}
	ids := []customerdomain.CustomerID{1, 2}
	source := "former.relationship.source"
	if s.unified {
		source = "wecom.directory.published.v2"
		ids = []customerdomain.CustomerID{2}
		if !at.Before(s.cutover.Add(-3 * time.Hour)) {
			ids = append(ids, 3)
		}
		if !at.Before(s.cutover.Add(-time.Hour)) {
			ids = append(ids, 4)
		}
		if !at.Before(s.cutover.Add(time.Hour)) {
			ids = []customerdomain.CustomerID{3, 4, 5}
		}
	}
	facts := map[customerdomain.CustomerID]segmentport.PaidOrderFact{}
	for _, id := range ids {
		paidAt := s.cutover.Add(-8 * time.Hour)
		order := int64(100 + id)
		if s.unified && id == 2 && !at.Before(s.cutover.Add(-2*time.Hour)) {
			paidAt = s.cutover.Add(-2 * time.Hour)
			order = 202
		}
		if id == 3 {
			paidAt = s.cutover.Add(-3 * time.Hour)
		}
		if id == 4 {
			paidAt = s.cutover.Add(-time.Hour)
		}
		if id == 5 {
			paidAt = s.cutover.Add(time.Hour)
		}
		if s.unified && id == 3 && !at.Before(s.cutover.Add(time.Hour)) {
			paidAt = s.cutover.Add(time.Hour)
			order = 303
		}
		facts[id] = segmentport.PaidOrderFact{PaidOrderID: order, PaidAt: paidAt}
	}
	return segmentport.Evaluation{CustomerIDs: ids, QualifiedPaidOrder: facts, ReferenceAt: at, Watermarks: []segmentport.SourceWatermark{{Source: source, Version: 1, AsOf: s.cutover, Fresh: true}}}, nil
}

type backlogFixture struct {
	native    *pgxpool.Pool
	service   *SnapshotService
	source    *backlogRebaseSource
	events    *memberEventEnqueueStub
	packageID int64
	now       time.Time
}

func newBacklogFixture(t *testing.T) *backlogFixture {
	t.Helper()
	ctx := context.Background()
	native, cleanup := scheduleRuntimeDatabase(t, ctx)
	t.Cleanup(cleanup)
	pool, _ := platformpostgres.Wrap(native, time.Second)
	uow, _ := platformpostgres.NewUnitOfWork(pool)
	repo, _ := segmentstore.NewPostgreSQL(native, uow)
	f := &backlogFixture{native: native, now: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	f.source = &backlogRebaseSource{cutover: f.now}
	f.events = &memberEventEnqueueStub{}
	if err := uow.Within(ctx, func(tx context.Context) error {
		p, e := segmentdomain.NewPackage("backlog-cutover", "积压参考时间校准", nil, 7, f.now)
		if e != nil {
			return e
		}
		p, e = repo.CreatePackage(tx, p)
		if e != nil {
			return e
		}
		c, e := segmentdomain.NewConfigurationVersion(p.ID, 1, []byte(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`), "", "manual", 7, f.now)
		if e != nil {
			return e
		}
		c, e = repo.CreateConfigurationVersion(tx, c)
		if e != nil {
			return e
		}
		p, e = repo.SetCurrentConfiguration(tx, p.ID, c.ID, p.Version, 7, f.now)
		f.packageID = p.ID
		return e
	}); err != nil {
		t.Fatal(err)
	}
	evaluator, _ := NewEvaluator(segmentcompiler.Compiler{}, f.source, scheduleRuntimeCanonical{})
	f.service, _ = NewSnapshotService(uow, repo, evaluator, &scheduleRuntimeEnqueuer{}, f.events)
	f.service.now = func() time.Time { return f.now }
	baseline := f.accept(t, "backlog-former-baseline", f.now.Add(-6*time.Hour))
	if e := f.service.ProcessRefresh(ctx, baseline.ID); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *backlogFixture) accept(t *testing.T, key string, at time.Time) segmentdomain.RefreshRun {
	t.Helper()
	r, e := f.service.AcceptRefresh(context.Background(), RefreshCommand{PackageID: f.packageID, Actor: 7, IdempotencyKey: key, ReferenceTime: at, RefreshKind: segmentdomain.RefreshDaily})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func (f *backlogFixture) assertQuiet(t *testing.T, reference time.Time) {
	t.Helper()
	ctx := context.Background()
	var count, entered, qualified, exits, reservations int
	var actual time.Time
	var kind string
	if e := f.native.QueryRow(ctx, `SELECT s.member_count,s.reference_time,r.refresh_kind FROM segment_audience_packages p JOIN segment_audience_snapshots s ON s.id=p.published_snapshot_id JOIN segment_audience_refresh_runs r ON r.id=s.refresh_run_id WHERE p.id=$1`, f.packageID).Scan(&count, &actual, &kind); e != nil {
		t.Fatal(e)
	}
	if e := f.native.QueryRow(ctx, `SELECT count(*) FILTER(WHERE event_kind='audience.member_entered.v1'),count(*) FILTER(WHERE event_kind='audience.member_paid_qualified.v1') FROM segment_audience_member_events`).Scan(&entered, &qualified); e != nil {
		t.Fatal(e)
	}
	if e := f.native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_exit_events`).Scan(&exits); e != nil {
		t.Fatal(e)
	}
	if e := f.native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_outbox WHERE event_type='audience.source_calibration.reserved.v1'`).Scan(&reservations); e != nil {
		t.Fatal(e)
	}
	if count != 3 || !actual.Equal(reference) || kind != "source_rebase" || entered != 2 || qualified != 0 || exits != 0 || f.events.calls != 1 || reservations != 1 {
		t.Fatalf("calibration count/time/kind/events/qualified/exits/queue/reservations=%d/%v/%s/%d/%d/%d/%d/%d", count, actual, kind, entered, qualified, exits, f.events.calls, reservations)
	}
	var ids []int64
	var orders []int64
	if e := f.native.QueryRow(ctx, `SELECT array_agg(customer_id ORDER BY customer_id),array_agg(paid_order_id ORDER BY customer_id) FROM segment_audience_snapshot_members WHERE snapshot_id=(SELECT published_snapshot_id FROM segment_audience_packages WHERE id=$1)`, f.packageID).Scan(&ids, &orders); e != nil {
		t.Fatal(e)
	}
	if fmt.Sprint(ids) != "[2 3 4]" || fmt.Sprint(orders) != "[202 103 104]" {
		t.Fatalf("calibration missed historical members or qualification: ids=%v orders=%v", ids, orders)
	}
}
func TestDirectoryBacklogCalibratesAtCurrentTimeAndRejectsHistoricalReplayPostgreSQL(t *testing.T) {
	for _, firstCurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("current_first_%t", firstCurrent), func(t *testing.T) {
			f := newBacklogFixture(t)
			ctx := context.Background()
			cutover := f.now
			early := f.accept(t, "backlog-early-ref-0001", cutover.Add(-4*time.Hour))
			later := f.accept(t, "backlog-later-ref-0002", cutover.Add(-30*time.Minute))
			current := f.accept(t, "backlog-current-ref-0003", cutover)
			f.source.unavailable = true
			if e := f.service.ProcessRefresh(ctx, early.ID); !errors.Is(e, ErrEvaluationUnavailable) {
				t.Fatalf("unavailable=%v", e)
			}
			f.source.unavailable = false
			f.source.unified = true
			first := early
			if firstCurrent {
				first = current
			}
			if e := f.service.ProcessRefresh(ctx, first.ID); e != nil {
				t.Fatal(e)
			}
			f.assertQuiet(t, cutover)
			for _, r := range []segmentdomain.RefreshRun{early, later} {
				if r.ID == first.ID {
					continue
				}
				if e := f.service.ProcessRefresh(ctx, r.ID); e != nil {
					t.Fatalf("old run %d did not terminate: %v", r.ID, e)
				}
				failed, e := f.service.GetRefresh(ctx, r.ID)
				if e != nil || failed.State != segmentdomain.RefreshFailed {
					t.Fatalf("old run %d unexpectedly published: %s/%v", r.ID, failed.State, e)
				}
			}
			if e := f.service.ProcessRefresh(ctx, first.ID); e != nil {
				t.Fatal(e)
			}
			f.assertQuiet(t, cutover)
			// Original idempotent request stays valid; changing its reference is rejected.
			original := f.accept(t, map[bool]string{false: "backlog-early-ref-0001", true: "backlog-current-ref-0003"}[firstCurrent], first.ReferenceTime)
			if original.ID != first.ID {
				t.Fatal("calibration created another run")
			}
			if _, e := f.service.AcceptRefresh(ctx, RefreshCommand{PackageID: f.packageID, Actor: 7, IdempotencyKey: map[bool]string{false: "backlog-early-ref-0001", true: "backlog-current-ref-0003"}[firstCurrent], ReferenceTime: first.ReferenceTime.Add(time.Second), RefreshKind: segmentdomain.RefreshDaily}); !errors.Is(e, ErrConflict) {
				t.Fatalf("changed reference bypassed idempotency: %v", e)
			}
			if !firstCurrent {
				if _, e := f.service.AcceptRefresh(ctx, RefreshCommand{PackageID: f.packageID, Actor: 7, IdempotencyKey: "backlog-early-ref-0001", ReferenceTime: cutover, RefreshKind: segmentdomain.RefreshDaily}); !errors.Is(e, ErrConflict) {
					t.Fatalf("calibration clock accepted as a different original request: %v", e)
				}
			}
			f.now = cutover.Add(time.Hour)
			real := f.accept(t, "backlog-real-change-0004", f.now)
			if e := f.service.ProcessRefresh(ctx, real.ID); e != nil {
				t.Fatal(e)
			}
			var entered, qualified, exits int
			if e := f.native.QueryRow(ctx, `SELECT count(*) FILTER(WHERE event_kind='audience.member_entered.v1'),count(*) FILTER(WHERE event_kind='audience.member_paid_qualified.v1') FROM segment_audience_member_events`).Scan(&entered, &qualified); e != nil {
				t.Fatal(e)
			}
			if e := f.native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_exit_events`).Scan(&exits); e != nil {
				t.Fatal(e)
			}
			if entered != 3 || qualified != 1 || exits != 1 || f.events.calls != 2 {
				t.Fatalf("real changes entered/qualified/exits/queues=%d/%d/%d/%d", entered, qualified, exits, f.events.calls)
			}
		})
	}
}
func TestDirectoryCalibrationRetryKeepsReservedClockAndReceiptPostgreSQL(t *testing.T) {
	f := newBacklogFixture(t)
	ctx := context.Background()
	cutover := f.now
	run := f.accept(t, "backlog-clock-retry-0001", cutover.Add(-4*time.Hour))
	f.source.unified = true
	f.source.failCurrentOnce = true
	if e := f.service.ProcessRefresh(ctx, run.ID); !errors.Is(e, ErrEvaluationUnavailable) {
		t.Fatalf("expected failure after reservation: %v", e)
	}
	saved, e := f.service.GetRefresh(ctx, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if saved.RefreshKind != segmentdomain.RefreshSourceRebase || !saved.ReferenceTime.Equal(cutover) {
		t.Fatalf("reservation=%+v", saved)
	}
	f.now = cutover.Add(10 * time.Minute)
	if e = f.service.ProcessRefresh(ctx, run.ID); e != nil {
		t.Fatal(e)
	}
	f.assertQuiet(t, cutover)
	replay := f.accept(t, "backlog-clock-retry-0001", run.ReferenceTime)
	if replay.ID != run.ID {
		t.Fatal("retry duplicated run")
	}
}

func TestConcurrentReservedDirectoryCalibrationsRemainSilentPostgreSQL(t *testing.T) {
	for _, newerFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("newer_first_%t", newerFirst), func(t *testing.T) {
			f := newBacklogFixture(t)
			ctx := context.Background()
			cutover := f.now
			a := f.accept(t, "concurrent-calibration-older", cutover.Add(-4*time.Hour))
			b := f.accept(t, "concurrent-calibration-newer", cutover.Add(-2*time.Hour))
			f.source.unified = true
			f.source.failCurrentOnce = true
			if e := f.service.ProcessRefresh(ctx, a.ID); !errors.Is(e, ErrEvaluationUnavailable) {
				t.Fatalf("older reservation: %v", e)
			}
			f.now = cutover.Add(time.Minute)
			f.source.failCurrentOnce = true
			if e := f.service.ProcessRefresh(ctx, b.ID); !errors.Is(e, ErrEvaluationUnavailable) {
				t.Fatalf("newer reservation: %v", e)
			}
			first, second := a, b
			if newerFirst {
				first, second = b, a
			}
			if e := f.service.ProcessRefresh(ctx, first.ID); e != nil {
				t.Fatal(e)
			}
			e := f.service.ProcessRefresh(ctx, second.ID)
			if newerFirst {
				failed, readErr := f.service.GetRefresh(ctx, second.ID)
				if e != nil || readErr != nil || failed.State != segmentdomain.RefreshFailed {
					t.Fatalf("older calibration did not terminate: %s/%v/%v", failed.State, e, readErr)
				}
			}
			if !newerFirst && e != nil {
				t.Fatal(e)
			}
			var entered, qualified, exits, audits int
			var ref time.Time
			if e := f.native.QueryRow(ctx, `SELECT count(*) FILTER(WHERE event_kind='audience.member_entered.v1'),count(*) FILTER(WHERE event_kind='audience.member_paid_qualified.v1') FROM segment_audience_member_events`).Scan(&entered, &qualified); e != nil {
				t.Fatal(e)
			}
			if e := f.native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_exit_events`).Scan(&exits); e != nil {
				t.Fatal(e)
			}
			if e := f.native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_outbox WHERE event_type='audience.source_calibration.reserved.v1'`).Scan(&audits); e != nil {
				t.Fatal(e)
			}
			if e := f.native.QueryRow(ctx, `SELECT reference_time FROM segment_audience_snapshots WHERE id=(SELECT published_snapshot_id FROM segment_audience_packages WHERE id=$1)`, f.packageID).Scan(&ref); e != nil {
				t.Fatal(e)
			}
			if entered != 2 || qualified != 0 || exits != 0 || f.events.calls != 1 || audits != 2 || !ref.Equal(cutover.Add(time.Minute)) {
				t.Fatalf("concurrent calibration events/qualified/exits/queues/audits/ref=%d/%d/%d/%d/%d/%v", entered, qualified, exits, f.events.calls, audits, ref)
			}
		})
	}
}

func TestDirectoryCalibrationReservationRollsBackWithSnapshotAndAuditPostgreSQL(t *testing.T) {
	f := newBacklogFixture(t)
	ctx := context.Background()
	old := f.now.Add(-4 * time.Hour)
	run := f.accept(t, "calibration-atomic-rollback", old)
	f.source.unavailable = true
	if e := f.service.ProcessRefresh(ctx, run.ID); !errors.Is(e, ErrEvaluationUnavailable) {
		t.Fatal(e)
	}
	sentinel := errors.New("rollback calibration reservation")
	e := f.service.uow.Within(ctx, func(tx context.Context) error {
		repo := f.service.store.(interface {
			ReserveSourceCalibrationReference(context.Context, int64, []segmentport.SourceWatermark, segmentstore.Actor, time.Time) (time.Time, error)
		})
		if _, e := repo.ReserveSourceCalibrationReference(tx, run.ID, []segmentport.SourceWatermark{{Source: "wecom.directory.published.v2", Version: 1, Fresh: true}}, segmentstore.Actor{Kind: "admin", StaffID: 7, Reference: "admin:7"}, f.now); e != nil {
			return e
		}
		return sentinel
	})
	if !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	saved, e := f.service.GetRefresh(ctx, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	var ref time.Time
	var audits int
	if e := f.native.QueryRow(ctx, `SELECT reference_time FROM segment_audience_snapshots WHERE refresh_run_id=$1`, run.ID).Scan(&ref); e != nil {
		t.Fatal(e)
	}
	if e := f.native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_outbox WHERE event_type='audience.source_calibration.reserved.v1'`).Scan(&audits); e != nil {
		t.Fatal(e)
	}
	if saved.RefreshKind != segmentdomain.RefreshDaily || !saved.ReferenceTime.Equal(old) || !ref.Equal(old) || audits != 0 {
		t.Fatalf("non-atomic reservation: run=%+v snapshot=%v audits=%d", saved, ref, audits)
	}
}

func TestSupersededRefreshFinishesWithoutSourceEvaluationPostgreSQL(t *testing.T) {
	for _, staged := range []bool{false, true} {
		t.Run(fmt.Sprintf("staged_%t", staged), func(t *testing.T) {
			f := newBacklogFixture(t)
			ctx := context.Background()
			cutover := f.now
			old := f.accept(t, "superseded-old-run-001", cutover.Add(-4*time.Hour))
			if staged {
				if e := f.service.uow.Within(ctx, func(tx context.Context) error {
					if _, _, e := f.service.store.BeginRefresh(tx, old.ID, f.now); e != nil {
						return e
					}
					ids := []customerdomain.CustomerID{1}
					return f.service.store.StageRefreshBatch(tx, old.ID, 0, ids, segmentdomain.DigestMembers(ids), f.now)
				}); e != nil {
					t.Fatal(e)
				}
			}
			f.source.unified = true
			current := f.accept(t, "superseded-current-run-002", cutover)
			if e := f.service.ProcessRefresh(ctx, current.ID); e != nil {
				t.Fatal(e)
			}
			f.assertQuiet(t, cutover)
			calls := f.source.calls
			f.source.unavailable = true
			for i := 0; i < 2; i++ {
				if e := f.service.ProcessRefresh(ctx, old.ID); e != nil {
					t.Fatalf("permanently superseded task retried: %v", e)
				}
			}
			if f.source.calls != calls {
				t.Fatal("superseded run performed source evaluation")
			}
			var state, code, snapshotState string
			var completed bool
			if e := f.native.QueryRow(ctx, `SELECT r.state,r.error_code,r.completed_at IS NOT NULL,s.state FROM segment_audience_refresh_runs r JOIN segment_audience_snapshots s ON s.refresh_run_id=r.id WHERE r.id=$1`, old.ID).Scan(&state, &code, &completed, &snapshotState); e != nil {
				t.Fatal(e)
			}
			if state != "failed" || code != "reference_superseded" || !completed || snapshotState != "failed" {
				t.Fatalf("terminal receipt=%s/%s/%t/%s", state, code, completed, snapshotState)
			}
			replay := f.accept(t, "superseded-old-run-001", old.ReferenceTime)
			if replay.ID != old.ID || replay.State != segmentdomain.RefreshFailed {
				t.Fatal("old idempotent receipt replaced")
			}
			f.assertQuiet(t, cutover)
			f.source.unavailable = false
			equal := f.accept(t, "superseded-equal-run-003", cutover)
			if e := f.service.ProcessRefresh(ctx, equal.ID); e != nil {
				t.Fatal(e)
			}
			run, e := f.service.GetRefresh(ctx, equal.ID)
			if e != nil || run.State != segmentdomain.RefreshPublished {
				t.Fatalf("equal reference discarded: %v/%s", e, run.State)
			}
		})
	}
}

func TestFirstDirectoryCalibrationPreservesReferenceBeforeFormerSnapshotPostgreSQL(t *testing.T) {
	f := newBacklogFixture(t)
	ctx := context.Background()
	cutover := f.now
	old := f.accept(t, "calibration-before-former-ref", cutover.Add(-8*time.Hour))
	f.source.unified = true
	if e := f.service.ProcessRefresh(ctx, old.ID); e != nil {
		t.Fatal(e)
	}
	f.assertQuiet(t, cutover)
}
