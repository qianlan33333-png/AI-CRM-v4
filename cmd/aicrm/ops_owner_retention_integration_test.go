package main

import (
	"context"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/adminops"
	opsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/store"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	archivestore "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// Exercise the production Owner adapters, including a failure after deletion
// but before the maintenance record commits. Owner-only tests cannot prove this
// composition has kept the two writes in one UnitOfWork.
func TestPostgreSQLOpsOwnerRetentionRecordsAndDeletesCommitTogether(t *testing.T) {
	ctx := context.Background()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, err := postgres.Open(ctx, postgres.Config{URL: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := postgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	config, err := configstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	media, err := mediastore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := opsstore.NewProjectionPostgreSQL(pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	service, err := adminops.NewRetentionService(pool.Native(), media, snapshots, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = bindOpsOwnerRetention(service, config, archivestore.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Native().Exec(ctx, `
INSERT INTO config_runtime_usage(revision,source,consumer,role,operation,subject_kind,subject_id,used_at)
SELECT 0,'environment_default','automation.operations.max_recipients_per_run','api','preview','automation_preview',d,clock_timestamp()-d*interval '1 day' FROM unnest(ARRAY[29,31]) AS d;
INSERT INTO message_archive_sync_state(corp_scope,last_seq) VALUES('wecom-corp:retention-fixture',42);
INSERT INTO message_archive_sync_runs(corp_scope,trigger_type,start_seq,end_seq,status,started_at,finished_at)
SELECT 'wecom-corp:retention-fixture','manual',0,42,'succeeded',clock_timestamp()-interval '32 days',clock_timestamp()-d*interval '1 day' FROM unnest(ARRAY[29,31]) AS d;
CREATE FUNCTION retention_record_fixture_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture maintenance record failure'; END $$;
CREATE TRIGGER retention_record_fixture_failure BEFORE UPDATE ON adminops_retention_runs FOR EACH ROW EXECUTE FUNCTION retention_record_fixture_failure();`)
	if err != nil {
		t.Fatal(err)
	}
	policies := map[string]string{"config_usage": "config_runtime_usage", "archive_sync_runs": "message_archive_sync_runs"}
	for policy, table := range policies {
		preview, e := service.PreviewRetention(ctx, policy)
		if e != nil || preview.Candidates != 1 || preview.EstimatedPayloadBytes <= 0 {
			t.Fatalf("%s preview: %+v %v", policy, preview, e)
		}
		if _, e = service.PruneRetentionBatch(ctx, policy); e == nil {
			t.Fatalf("%s ignored record failure", policy)
		}
		var count int
		if e = pool.Native().QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); e != nil || count != 2 {
			t.Fatalf("%s split delete/record transaction: %d %v", policy, count, e)
		}
	}
	if _, err = pool.Native().Exec(ctx, `DROP TRIGGER retention_record_fixture_failure ON adminops_retention_runs`); err != nil {
		t.Fatal(err)
	}
	for policy, table := range policies {
		result, e := service.PruneRetentionBatch(ctx, policy)
		if e != nil || result.Deleted != 1 || result.PayloadBytes <= 0 || result.State != "completed" {
			t.Fatalf("%s execution: %+v %v", policy, result, e)
		}
		var rows, bytes int64
		if e = pool.Native().QueryRow(ctx, `SELECT deleted_rows,payload_bytes FROM adminops_retention_runs WHERE policy=$1`, policy).Scan(&rows, &bytes); e != nil || rows != 1 || bytes != result.PayloadBytes {
			t.Fatalf("%s counts mismatch: %d %d %v", policy, rows, bytes, e)
		}
		if _, e = service.PruneRetentionBatch(ctx, policy); e != nil {
			t.Fatal(e)
		}
		if e = pool.Native().QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&rows); e != nil || rows != 1 {
			t.Fatalf("%s deleted recent row on replay: %d %v", policy, rows, e)
		}
	}
	var cursor int64
	if err = pool.Native().QueryRow(ctx, `SELECT last_seq FROM message_archive_sync_state WHERE corp_scope='wecom-corp:retention-fixture'`).Scan(&cursor); err != nil || cursor != 42 {
		t.Fatalf("business cursor altered: %d %v", cursor, err)
	}
	// The fixed batch size must not be presented as a complete backlog count.
	_, err = pool.Native().Exec(ctx, `
INSERT INTO config_runtime_usage(revision,source,consumer,role,operation,subject_kind,subject_id,used_at)
SELECT 0,'environment_default','automation.operations.max_recipients_per_run','api','preview','automation_preview',d,clock_timestamp()-interval '31 days' FROM generate_series(100001,101001) AS d;
INSERT INTO message_archive_sync_runs(corp_scope,trigger_type,start_seq,end_seq,status,started_at,finished_at)
SELECT 'wecom-corp:retention-fixture','manual',0,42,'failed',clock_timestamp()-interval '32 days',clock_timestamp()-interval '31 days' FROM generate_series(1,1001);`)
	if err != nil {
		t.Fatal(err)
	}
	for policy := range policies {
		preview, e := service.PreviewRetention(ctx, policy)
		if e != nil || preview.Candidates != 1000 || !preview.HasMore {
			t.Fatalf("%s hid bounded preview: %+v %v", policy, preview, e)
		}
	}
}
