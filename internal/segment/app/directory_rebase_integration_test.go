package app

import (
	"context"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	"testing"
	"time"
)

type rebaseSource struct {
	ids     []customerdomain.CustomerID
	version int64
	err     error
}

func (s *rebaseSource) Evaluate(_ context.Context, _ segmentport.Definition, at time.Time) (segmentport.Evaluation, error) {
	source := "former.relationship.source"
	if s.version > 0 {
		source = "wecom.directory.published.v2"
	}
	return segmentport.Evaluation{CustomerIDs: s.ids, ReferenceAt: at, Watermarks: []segmentport.SourceWatermark{{Source: source, Version: s.version, AsOf: at, Fresh: true}}}, s.err
}
func TestPublishedDirectoryCutoverCalibratesSilentlyThenTriggersRealChangesPostgreSQL(t *testing.T) {
	ctx := context.Background()
	native, cleanup := scheduleRuntimeDatabase(t, ctx)
	defer cleanup()
	pool, _ := platformpostgres.Wrap(native, time.Second)
	uow, _ := platformpostgres.NewUnitOfWork(pool)
	repo, _ := segmentstore.NewPostgreSQL(native, uow)
	now := time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC)
	var packageID int64
	if err := uow.Within(ctx, func(tx context.Context) error {
		pkg, e := segmentdomain.NewPackage("source-cutover", "统一资料校准", nil, 7, now)
		if e != nil {
			return e
		}
		pkg, e = repo.CreatePackage(tx, pkg)
		if e != nil {
			return e
		}
		config, e := segmentdomain.NewConfigurationVersion(pkg.ID, 1, []byte(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`), "", "manual", 7, now)
		if e != nil {
			return e
		}
		config, e = repo.CreateConfigurationVersion(tx, config)
		if e != nil {
			return e
		}
		pkg, e = repo.SetCurrentConfiguration(tx, pkg.ID, config.ID, pkg.Version, 7, now)
		packageID = pkg.ID
		return e
	}); err != nil {
		t.Fatal(err)
	}
	source := &rebaseSource{ids: []customerdomain.CustomerID{1, 2}}
	evaluator, _ := NewEvaluator(segmentcompiler.Compiler{}, source, scheduleRuntimeCanonical{})
	events := &memberEventEnqueueStub{}
	service, _ := NewSnapshotService(uow, repo, evaluator, &scheduleRuntimeEnqueuer{}, events)
	service.now = func() time.Time { return now }
	refresh := func(key string) {
		t.Helper()
		now = now.Add(time.Minute)
		run, e := service.AcceptRefresh(ctx, RefreshCommand{PackageID: packageID, Actor: 7, IdempotencyKey: key, ReferenceTime: now, RefreshKind: segmentdomain.RefreshDaily})
		if e != nil {
			t.Fatal(e)
		}
		if e = service.ProcessRefresh(ctx, run.ID); e != nil {
			t.Fatal(e)
		}
	}
	refresh("former-source-refresh-0001")
	var enteredBefore int
	if err := native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_events`).Scan(&enteredBefore); err != nil {
		t.Fatal(err)
	}
	if enteredBefore != 2 || events.calls != 1 {
		t.Fatalf("before events=%d queues=%d", enteredBefore, events.calls)
	}
	source.version = 1
	source.ids = []customerdomain.CustomerID{2, 3}
	refresh("directory-cutover-refresh-0001")
	var members, entered, exits int
	if err := native.QueryRow(ctx, `SELECT member_count FROM segment_audience_snapshots WHERE id=(SELECT published_snapshot_id FROM segment_audience_packages WHERE id=$1)`, packageID).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_events`).Scan(&entered); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_exit_events`).Scan(&exits); err != nil {
		t.Fatal(err)
	}
	if members != 2 || entered != enteredBefore || exits != 0 || events.calls != 1 {
		t.Fatalf("calibration members/events/exits/queues=%d/%d/%d/%d", members, entered, exits, events.calls)
	}
	source.err = ErrUnavailable
	failed, err := service.AcceptRefresh(ctx, RefreshCommand{PackageID: packageID, Actor: 7, IdempotencyKey: "unavailable-directory-refresh", ReferenceTime: now.Add(time.Second), RefreshKind: segmentdomain.RefreshDaily})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ProcessRefresh(ctx, failed.ID); err == nil || !errors.Is(err, ErrEvaluationUnavailable) {
		t.Fatalf("unavailable directory did not fail evaluation: %v", err)
	}
	if err = native.QueryRow(ctx, `SELECT member_count FROM segment_audience_snapshots WHERE id=(SELECT published_snapshot_id FROM segment_audience_packages WHERE id=$1)`, packageID).Scan(&members); err != nil || members != 2 || events.calls != 1 {
		t.Fatalf("unavailable directory changed published members=%d queues=%d err=%v", members, events.calls, err)
	}
	source.err = nil

	source.version = 2
	source.ids = []customerdomain.CustomerID{3, 4}
	refresh("directory-real-change-refresh-0002")
	_ = native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_events`).Scan(&entered)
	_ = native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_exit_events`).Scan(&exits)
	if entered != 3 || exits != 1 || events.calls != 2 {
		t.Fatalf("real changes events/exits/queues=%d/%d/%d", entered, exits, events.calls)
	}
}
