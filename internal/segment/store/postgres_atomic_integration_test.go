package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

func TestPostgreSQLAudienceConfigurationAtomicity(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	group, _ := segmentdomain.NewGroup("新客", 10, 7, now)

	rollback := errors.New("inject rollback")
	err = uow.Within(ctx, func(tx context.Context) error {
		createdGroup, e := repo.CreateGroup(tx, group)
		if e != nil {
			return e
		}
		item, _ := segmentdomain.NewPackage("rollback", "回滚测试", &createdGroup.ID, 7, now)
		created, e := repo.CreatePackage(tx, item)
		if e != nil {
			return e
		}
		receipt, _, e := repo.Reserve(tx, reservationFor("rollback-key", json.RawMessage(`{"name":"回滚"}`), now))
		if e != nil {
			return e
		}
		if _, e = repo.Complete(tx, receipt.ID, json.RawMessage(`{"id":1}`), now); e != nil {
			return e
		}
		if _, e = repo.AppendMutationFacts(tx, testFact(created.ID, "rollback-key", now)); e != nil {
			return e
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback injection=%v", err)
	}
	assertSegmentCounts(t, ctx, native, [6]int{})

	err = uow.Within(ctx, func(tx context.Context) error {
		createdGroup, e := repo.CreateGroup(tx, group)
		if e != nil {
			return e
		}
		item, _ := segmentdomain.NewPackage("new-customers", "新客 30 天", &createdGroup.ID, 7, now)
		created, e := repo.CreatePackage(tx, item)
		if e != nil {
			return e
		}
		definition := json.RawMessage(`{"schema_version":1,"expression":{"kind":"all"}}`)
		configuration, _ := segmentdomain.NewConfigurationVersion(created.ID, 1, definition, "0 1 * * *", "legacy_custom", 7, now)
		configuration, e = repo.CreateConfigurationVersion(tx, configuration)
		if e != nil {
			return e
		}
		created, e = repo.SetCurrentConfiguration(tx, created.ID, configuration.ID, created.Version, 7, now)
		if e != nil {
			return e
		}
		if created.Version != 2 || created.CurrentConfigurationVersionID == nil {
			t.Fatalf("package=%+v", created)
		}
		refresh, owned, reserveErr := repo.ReserveRefresh(tx, segmentdomain.RefreshRun{
			PackageID: created.ID, ConfigurationVersionID: configuration.ID,
			SourceKeyDigest: [32]byte{1}, ReferenceTime: now, RefreshKind: segmentdomain.RefreshLegacy, CreatedAt: now, UpdatedAt: now,
		})
		if reserveErr != nil || !owned || refresh.ErrorCode != "" || refresh.RiverJobID != nil {
			t.Fatalf("nullable refresh fields: run=%+v owned=%v err=%v", refresh, owned, reserveErr)
		}
		receipt, _, e := repo.Reserve(tx, reservationFor("create-key", json.RawMessage(`{"code":"new-customers"}`), now))
		if e != nil {
			return e
		}
		if _, e = repo.Complete(tx, receipt.ID, json.RawMessage(`{"package_id":1}`), now); e != nil {
			return e
		}
		_, e = repo.AppendMutationFacts(tx, testFact(created.ID, "create-key", now))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSegmentCounts(t, ctx, native, [6]int{1, 1, 1, 1, 1, 1})
}

func TestPostgreSQLAudienceConfigurationEmptyCronRoundTrips(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 5, 0, 0, 0, time.UTC)
	err = uow.Within(ctx, func(tx context.Context) error {
		group, createErr := segmentdomain.NewGroup("默认运营人群", 100, 7, now)
		if createErr != nil {
			return createErr
		}
		group, createErr = repo.CreateGroup(tx, group)
		if createErr != nil {
			return createErr
		}
		item, createErr := segmentdomain.NewPackage("empty-cron", "空刷新计划", &group.ID, 7, now)
		if createErr != nil {
			return createErr
		}
		item, createErr = repo.CreatePackage(tx, item)
		if createErr != nil {
			return createErr
		}
		definition := json.RawMessage(`{"schema_version":1,"expression":{"kind":"all"}}`)
		configuration, createErr := segmentdomain.NewConfigurationVersion(item.ID, 1, definition, "", "manual", 7, now)
		if createErr != nil {
			return createErr
		}
		configuration, createErr = repo.CreateConfigurationVersion(tx, configuration)
		if createErr != nil {
			return createErr
		}
		if configuration.RefreshCronUTC != "" {
			return errors.New("empty refresh cron did not round trip")
		}
		item, createErr = repo.SetCurrentConfiguration(tx, item.ID, configuration.ID, item.Version, 7, now)
		if createErr != nil {
			return createErr
		}
		current, createErr := repo.CurrentConfiguration(tx, item.ID)
		if createErr != nil {
			return createErr
		}
		if current.RefreshCronUTC != "" {
			return errors.New("empty current refresh cron did not round trip")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLCurrentBindingQualifiesJoinedColumns(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}

	var packageID int64
	err = native.QueryRow(ctx, `INSERT INTO segment_audience_packages(code,name,created_by,created_actor_kind,created_actor_ref,updated_by,updated_actor_kind,updated_actor_ref,created_at,updated_at) VALUES('binding-read','Binding read',7,'admin','admin:7',7,'admin','admin:7',now(),now()) RETURNING id`).Scan(&packageID)
	if err != nil {
		t.Fatal(err)
	}
	var bindingID int64
	err = native.QueryRow(ctx, `INSERT INTO segment_audience_automation_binding_versions(package_id,version,agent_id,automation_type,agent_published_version,content_digest,materials_digest,created_by,created_actor_kind,created_actor_ref,created_at) VALUES($1,1,9,'fixed_script',3,decode(repeat('11',32),'hex'),decode(repeat('22',32),'hex'),7,'admin','admin:7',now()) RETURNING id`, packageID).Scan(&bindingID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE segment_audience_packages SET current_automation_binding_id=$2 WHERE id=$1`, packageID, bindingID); err != nil {
		t.Fatal(err)
	}

	err = uow.Within(ctx, func(tx context.Context) error {
		binding, readErr := repo.CurrentBinding(tx, packageID)
		if readErr != nil {
			return readErr
		}
		if binding.ID != bindingID || binding.PackageID != packageID || binding.AgentID != 9 || binding.AgentPublishedVersion != 3 {
			t.Fatalf("binding=%+v", binding)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLAudienceImmutableFacts(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	statements := []string{
		`INSERT INTO segment_audience_groups(name,created_by,created_actor_kind,created_actor_ref,updated_by,updated_actor_kind,updated_actor_ref,created_at,updated_at) VALUES('g',1,'admin','admin:1',1,'admin','admin:1',now(),now())`,
		`INSERT INTO segment_audience_packages(code,name,created_by,created_actor_kind,created_actor_ref,updated_by,updated_actor_kind,updated_actor_ref,created_at,updated_at) VALUES('p','p',1,'admin','admin:1',1,'admin','admin:1',now(),now())`,
		`INSERT INTO segment_audience_configuration_versions(package_id,version,schema_version,definition,digest,created_by,created_actor_kind,created_actor_ref,created_at) VALUES(1,1,1,'{}',decode(repeat('00',32),'hex'),1,'admin','admin:1',now())`,
		`INSERT INTO segment_audience_audit_events(resource_kind,resource_id,operation,actor_id,actor_kind,actor_ref,occurred_at,payload_digest) VALUES('package',1,'create',1,'admin','admin:1',now(),decode(repeat('00',32),'hex'))`,
		`INSERT INTO segment_audience_outbox(event_type,aggregate_kind,aggregate_id,payload,idempotency_digest,occurred_at) VALUES('created','package',1,'{}',decode(repeat('00',32),'hex'),now())`,
	}
	for _, statement := range statements {
		if _, err := native.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`UPDATE segment_audience_configuration_versions SET definition='{}'`,
		`UPDATE segment_audience_audit_events SET operation='changed'`,
		`UPDATE segment_audience_outbox SET event_type='changed'`,
	} {
		if _, err := native.Exec(ctx, statement); err == nil {
			t.Fatalf("append-only fact accepted mutation: %s", statement)
		}
	}
}

func TestPostgreSQLAudienceExtendedFactKindsRemainAccepted(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 4, 0, 0, 0, time.UTC)
	for index, kind := range []string{"webhook_receipt", "schedule", "member_event_batch"} {
		err = uow.Within(ctx, func(tx context.Context) error {
			_, appendErr := repo.AppendMutationFacts(tx, MutationFact{
				ResourceKind: kind, ResourceID: int64(index + 11), Operation: "create",
				EventType: "audience." + kind + ".created.v1", ActorID: 7,
				Payload: json.RawMessage(`{"resource_id":11}`), IdempotencyKey: kind + ":11", OccurredAt: now,
			})
			return appendErr
		})
		if err != nil {
			t.Fatalf("append %s facts after schedule migration: %v", kind, err)
		}
	}
}

func TestPostgreSQLAudienceMemberEventsUseTypedSnapshotParameters(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 6, 30, 0, 0, time.UTC)
	var packageID, configurationID, refreshID, snapshotID int64
	if err = native.QueryRow(ctx, `INSERT INTO segment_audience_packages(code,name,created_by,created_actor_kind,created_actor_ref,updated_by,updated_actor_kind,updated_actor_ref,created_at,updated_at) VALUES('typed-events','typed events',7,'admin','admin:7',7,'admin','admin:7',$1,$1) RETURNING id`, now).Scan(&packageID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO segment_audience_configuration_versions(package_id,version,schema_version,definition,digest,created_by,created_actor_kind,created_actor_ref,created_at) VALUES($1,1,1,'{"schema_version":1,"expression":{"kind":"all"}}',decode(repeat('00',32),'hex'),7,'admin','admin:7',$2) RETURNING id`, packageID, now).Scan(&configurationID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE segment_audience_packages SET current_configuration_version_id=$2 WHERE id=$1`, packageID, configurationID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO segment_audience_refresh_runs(package_id,configuration_version_id,source_key_digest,reference_time,state,created_at,updated_at,completed_at) VALUES($1,$2,decode(repeat('01',32),'hex'),$3,'published',$3,$3,$3) RETURNING id`, packageID, configurationID, now).Scan(&refreshID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO segment_audience_snapshots(package_id,configuration_version_id,refresh_run_id,state,reference_time,member_count,member_digest,source_watermark_digest,created_at,published_at) VALUES($1,$2,$3,'published',$4,2,decode(repeat('02',32),'hex'),decode(repeat('03',32),'hex'),$4,$4) RETURNING id`, packageID, configurationID, refreshID, now).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO segment_audience_snapshot_members(snapshot_id,customer_id,entered_at,identity_disposition) VALUES($1,11,$2,'resolved'),($1,12,$2,'resolved')`, snapshotID, now); err != nil {
		t.Fatal(err)
	}

	err = uow.Within(ctx, func(tx context.Context) error {
		created, createErr := repo.CreateMemberEnteredEvents(tx, segmentdomain.Snapshot{
			ID: snapshotID, PackageID: packageID, ConfigurationVersionID: configurationID,
			State: "published", ReferenceTime: now, PublishedAt: &now,
		}, nil, 7, now)
		if createErr != nil {
			return createErr
		}
		if created != 2 {
			return errors.New("unexpected member event count")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var events int64
	if err = native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_events WHERE snapshot_id=$1`, snapshotID).Scan(&events); err != nil || events != 2 {
		t.Fatalf("member events=%d err=%v", events, err)
	}
}

func TestPostgreSQLRefreshKindsPreserveIncrementalMembersAndDailyExits(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 18, 0, 0, 0, time.UTC)
	var packageID, configurationID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		group, e := segmentdomain.NewGroup("refresh kinds", 1, 7, now)
		if e != nil {
			return e
		}
		group, e = repo.CreateGroup(tx, group)
		if e != nil {
			return e
		}
		pkg, e := segmentdomain.NewPackage("refresh-kinds", "refresh kinds", &group.ID, 7, now)
		if e != nil {
			return e
		}
		pkg, e = repo.CreatePackage(tx, pkg)
		if e != nil {
			return e
		}
		config, e := segmentdomain.NewConfigurationVersion(pkg.ID, 1, json.RawMessage(`{"schema_version":1,"expression":{"kind":"all"}}`), "", "every_3m_plus_daily_0200", 7, now)
		if e != nil {
			return e
		}
		config, e = repo.CreateConfigurationVersion(tx, config)
		if e != nil {
			return e
		}
		if _, e = repo.SetCurrentConfiguration(tx, pkg.ID, config.ID, pkg.Version, 7, now); e != nil {
			return e
		}
		packageID, configurationID = pkg.ID, config.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	publish := func(kind segmentdomain.RefreshKind, reference time.Time, ids []customerdomain.CustomerID, key byte) (segmentdomain.PublishedRefresh, error) {
		var out segmentdomain.PublishedRefresh
		err := uow.Within(ctx, func(tx context.Context) error {
			run, owned, e := repo.ReserveRefresh(tx, segmentdomain.RefreshRun{PackageID: packageID, ConfigurationVersionID: configurationID, SourceKeyDigest: [32]byte{key}, ReferenceTime: reference, RefreshKind: kind, CreatedAt: now, UpdatedAt: now})
			if e != nil || !owned {
				return e
			}
			if _, e = repo.AttachRefreshJob(tx, run.ID, int64(key), now); e != nil {
				return e
			}
			if _, _, e = repo.BeginRefresh(tx, run.ID, now); e != nil {
				return e
			}
			if len(ids) > 0 {
				if e = repo.StageRefreshBatch(tx, run.ID, 0, ids, segmentdomain.DigestMembers(ids), now); e != nil {
					return e
				}
			}
			out, e = repo.PublishRefresh(tx, run.ID, int64(len(ids)), segmentdomain.DigestMembers(ids), [32]byte{}, 7, now)
			return e
		})
		return out, err
	}
	first, err := publish(segmentdomain.RefreshDaily, now, []customerdomain.CustomerID{1, 2}, 1)
	if err != nil || first.Snapshot.MemberCount != 2 || first.ExitedMemberCount != 0 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	incremental, err := publish(segmentdomain.RefreshIncremental, now.Add(time.Minute), []customerdomain.CustomerID{3}, 2)
	if err != nil || incremental.Snapshot.MemberCount != 3 || incremental.ExitedMemberCount != 0 {
		t.Fatalf("incremental=%+v err=%v", incremental, err)
	}
	daily, err := publish(segmentdomain.RefreshDaily, now.Add(2*time.Minute), []customerdomain.CustomerID{1, 3}, 3)
	if err != nil || daily.Snapshot.MemberCount != 2 || daily.ExitedMemberCount != 1 {
		t.Fatalf("daily=%+v err=%v", daily, err)
	}
	var exits int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_exit_events WHERE snapshot_id=$1 AND customer_id=2`, daily.Snapshot.ID).Scan(&exits); err != nil || exits != 1 {
		t.Fatalf("daily exits=%d err=%v", exits, err)
	}
	manual, err := publish(segmentdomain.RefreshManual, now.Add(3*time.Minute), []customerdomain.CustomerID{4}, 4)
	if err != nil || manual.Snapshot.MemberCount != 3 || manual.ExitedMemberCount != 0 {
		t.Fatalf("manual=%+v err=%v", manual, err)
	}
	_, err = publish(segmentdomain.RefreshDaily, now.Add(time.Second), []customerdomain.CustomerID{1}, 5)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale daily publish=%v", err)
	}
	var currentCount int64
	if err = native.QueryRow(ctx, `SELECT s.member_count FROM segment_audience_packages p JOIN segment_audience_snapshots s ON s.id=p.published_snapshot_id WHERE p.id=$1`, packageID).Scan(&currentCount); err != nil || currentCount != 3 {
		t.Fatalf("stale overwrite count=%d err=%v", currentCount, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		run, owned, e := repo.ReserveRefresh(tx, segmentdomain.RefreshRun{PackageID: packageID, ConfigurationVersionID: configurationID, SourceKeyDigest: [32]byte{9}, ReferenceTime: now.Add(5 * time.Minute), RefreshKind: segmentdomain.RefreshIncremental, CreatedAt: now, UpdatedAt: now})
		if e != nil || !owned {
			return e
		}
		if _, e = repo.AttachRefreshJob(tx, run.ID, 99, now); e != nil {
			return e
		}
		if _, _, e = repo.BeginRefresh(tx, run.ID, now); e != nil {
			return e
		}
		_, _, e = repo.ReserveRefresh(tx, segmentdomain.RefreshRun{PackageID: packageID, ConfigurationVersionID: configurationID, SourceKeyDigest: [32]byte{9}, ReferenceTime: now.Add(5 * time.Minute), RefreshKind: segmentdomain.RefreshDaily, CreatedAt: now, UpdatedAt: now})
		if !errors.Is(e, ErrConflict) {
			return errors.New("started incremental accepted a daily upgrade")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
