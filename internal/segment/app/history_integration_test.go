package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

func TestHistoricalImportRealPostgreSQLAtomicReplayAndStaticReadPath(t *testing.T) {
	ctx := context.Background()
	native, cleanup := scheduleRuntimeDatabase(t, ctx)
	defer cleanup()
	applySegmentRuntimeMigration(t, native, "0130_segment_historical_import.sql")
	applySegmentRuntimeMigration(t, native, "0135_segment_history_resolution_proof.sql")
	pool, e := platformpostgres.Wrap(native, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := platformpostgres.NewUnitOfWork(pool)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := segmentstore.NewPostgreSQL(native, uow)
	if e != nil {
		t.Fatal(e)
	}
	actor, _ := segmentport.AdminMutationActor(42)
	digest := sha256.Sum256([]byte("source one"))
	now := time.Now().UTC()
	in := segmentport.HistoricalImport{Source: "fixture", Digest: segmentport.Digest(digest), CapturedAt: now, EncryptedEvidence: make([]byte, 80), Actor: actor, Groups: []segmentport.HistoricalGroup{{SourceID: 1, Name: "Historical group"}}, Packages: []segmentport.HistoricalPackage{{SourceID: 1, GroupSourceID: 1, Name: "Historical active", Members: []segmentport.HistoricalMember{{SourceID: 1, CustomerID: 11, Active: true, EnteredAt: now, Reason: "resolved"}, {SourceID: 2, CustomerID: 11, Active: true, EnteredAt: now, Reason: "resolved"}, {SourceID: 3, Active: true, EnteredAt: now, Reason: "unresolved"}, {SourceID: 4, Reason: "exited"}}}, {SourceID: 2, Name: "Historical archive", Archived: true}}, Rows: []segmentport.HistoricalRow{{Kind: "package", SourceID: 1, Digest: segmentport.Digest(digest)}}}
	in.Rows = nil
	for kind, ids := range map[string][]int64{"group": {1}, "package": {1, 2}, "version": {1, 2}, "member": {1, 2, 3, 4}} {
		for _, id := range ids {
			in.Rows = append(in.Rows, segmentport.HistoricalRow{Kind: kind, SourceID: id, Digest: segmentport.Digest(digest)})
		}
	}
	apply := func(input segmentport.HistoricalImport) (segmentport.HistoricalImportResult, error) {
		var out segmentport.HistoricalImportResult
		e := uow.Within(ctx, func(tx context.Context) error { var err error; out, err = repo.ImportHistorical(tx, input); return err })
		return out, e
	}
	out, e := apply(in)
	if e != nil {
		t.Fatal(e)
	}
	if out.Packages != 2 || out.SourceMembers != 4 || out.ResolvedActive != 2 || out.Quarantined != 1 || out.Exited != 1 || out.PublishedMembers != 1 {
		t.Fatalf("bad conservation %+v", out)
	}
	replay, e := apply(in)
	if e != nil || !replay.Replayed {
		t.Fatalf("replay %+v %v", replay, e)
	}
	e = uow.Within(ctx, func(tx context.Context) error {
		packages, e := repo.ListPackages(tx, 100, 0, true)
		if e != nil {
			return e
		}
		if len(packages) != 2 {
			t.Fatal("native list missing imports")
		}
		for _, p := range packages {
			snapshot, found, e := repo.PublishedSnapshot(tx, segmentport.PackageID(p.ID))
			if e != nil {
				return e
			}
			if !found {
				t.Fatal("missing readable static snapshot")
			}
			members, e := repo.Members(tx, snapshot.ID, "", 100)
			if e != nil {
				return e
			}
			if len(members.Items) != int(snapshot.MemberCount) {
				t.Fatal("read path counts")
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	var jobs, schedules, events, active int
	for sql, dest := range map[string]*int{`SELECT count(*) FROM segment_audience_refresh_runs WHERE river_job_id IS NOT NULL`: &jobs, `SELECT count(*) FROM segment_audience_schedule_states`: &schedules, `SELECT count(*) FROM segment_audience_member_events`: &events, `SELECT count(*) FROM segment_audience_packages WHERE lifecycle='active'`: &active} {
		if e = native.QueryRow(ctx, sql).Scan(dest); e != nil {
			t.Fatal(e)
		}
	}
	if jobs+schedules+events+active != 0 {
		t.Fatal("migration activated effects")
	}
	if _, e = native.Exec(ctx, `UPDATE segment_audience_packages SET lifecycle='active' WHERE lifecycle='paused'`); e == nil {
		t.Fatal("historical package activated")
	}
	e = uow.Within(ctx, func(tx context.Context) error {
		verified, e := repo.VerifyHistorical(tx, in.Source, in.Digest)
		if e != nil {
			return e
		}
		if verified.SourceMembers != 4 || verified.PublishedMembers != 1 {
			t.Fatal("verify mismatch")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	// A source delta creates a new immutable static snapshot; original remains.
	newer := in
	newer.Digest = segmentport.Digest(sha256.Sum256([]byte("source two")))
	newer.CapturedAt = now.Add(time.Minute)
	newer.Packages = append([]segmentport.HistoricalPackage{}, in.Packages...)
	newer.Packages[0].Members = append([]segmentport.HistoricalMember{}, in.Packages[0].Members...)
	newer.Packages[0].Members[0].CustomerID = 12
	if _, e = apply(newer); e != nil {
		t.Fatal(e)
	}
	var snapshots int
	if e = native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_snapshots`).Scan(&snapshots); e != nil || snapshots != 4 {
		t.Fatalf("delta snapshots %d %v", snapshots, e)
	}
	// Deliberate late failure rolls back evidence, mappings, receipt and all rows.
	bad := newer
	bad.Digest = segmentport.Digest(sha256.Sum256([]byte("invalid three")))
	bad.CapturedAt = now.Add(2 * time.Minute)
	bad.Packages = append([]segmentport.HistoricalPackage{}, newer.Packages...)
	bad.Packages[1].GroupSourceID = 999
	if _, e = apply(bad); e == nil {
		t.Fatal("invalid mapping accepted")
	}
	var batches, receipts int
	native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_history_batches`).Scan(&batches)
	native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_operation_receipts`).Scan(&receipts)
	if batches != 2 || receipts != 2 {
		t.Fatalf("partial rollback %d %d", batches, receipts)
	}
	// Even successful owner import must roll back with its caller's UoW.
	bad.Packages = in.Packages
	sentinel := errors.New("caller rollback")
	e = uow.Within(ctx, func(tx context.Context) error {
		if _, e := repo.ImportHistorical(tx, bad); e != nil {
			return e
		}
		return sentinel
	})
	if !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_history_batches`).Scan(&batches)
	if batches != 2 {
		t.Fatal("owner escaped outer UoW")
	}

	// User edits after an import are never silently overwritten by a delta.
	if _, e = native.Exec(ctx, `UPDATE segment_audience_packages SET name='locally edited',version=version+1 WHERE lifecycle='paused'`); e != nil {
		t.Fatal(e)
	}
	if _, e = apply(bad); e == nil {
		t.Fatal("concurrent native edit overwritten")
	}
	native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_history_batches`).Scan(&batches)
	if batches != 2 {
		t.Fatal("CAS failure leaked batch")
	}
}
