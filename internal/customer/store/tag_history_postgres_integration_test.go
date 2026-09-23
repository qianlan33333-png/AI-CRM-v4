package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestTagHistoryPostgreSQLApplyVerifyReplayAndDrift(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := tagCommandPGPool(t, ctx, url)
	defer clean()
	if _, err = pool.Native().Exec(ctx, `INSERT INTO admin_users(id,username,password_hash,display_name,is_active,session_version) OVERRIDING SYSTEM VALUE VALUES(7,'history-staff','$argon2id$test','History staff',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(11,'active'); INSERT INTO tag_groups(id,group_name,sort_order) OVERRIDING SYSTEM VALUE VALUES(13,'history',1); INSERT INTO tag_catalog_tags(id,group_id,tag_name,sort_order) OVERRIDING SYSTEM VALUE VALUES(17,13,'history tag',1)`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	service := customerapp.HistoricalTagImportService{UOW: uow, Store: TagHistoryPostgreSQL{}}
	at := time.Date(2026, 9, 6, 11, 30, 0, 0, time.UTC)
	completed := at.Add(time.Minute)
	batch := customerport.HistoricalTagBatch{SourceSystem: "v2_external_effect_job", SnapshotDigest: string(effectport.Hash("v2-tag-snapshot")), SnapshotAt: at}
	records := []customerport.HistoricalTagRecord{
		{SourceJobID: 41, SourceDigest: string(effectport.Hash("v2-job-41")), EffectType: "wecom.contact.tag.mark", Operation: "tag_mark", SourceState: "provider_result_received", Resolution: "imported", CustomerID: customerdomain.CustomerID(11), StaffID: 7, AddTagIDs: []int64{17}, OccurredAt: at, CompletedAt: &completed},
		// An unresolved legacy external identity stays as a pending immutable
		// source fact. It never creates a customer, command, EER, River job, or
		// Provider effect during historical import.
		{SourceJobID: 42, SourceDigest: string(effectport.Hash("v2-job-42")), EffectType: "wecom.contact.tag.unmark", Operation: "tag_unmark", SourceState: "provider_accepted", Resolution: "pending", Reason: "identity_unresolved", OccurredAt: at.Add(time.Second)},
		{SourceJobID: 43, SourceDigest: string(effectport.Hash("v2-job-43")), EffectType: "wecom.contact.tag.mark", Operation: "tag_mark", SourceState: "failed", Resolution: "excluded", Reason: "provider_payload_unavailable", OccurredAt: at.Add(2 * time.Second)},
	}
	result, err := service.ApplyHistoricalTagRecords(ctx, batch, records)
	if err != nil || result.Imported != 1 || result.Pending != 1 || result.Excluded != 1 || result.Replayed != 0 {
		t.Fatalf("apply result=%+v err=%v", result, err)
	}
	replayed, err := service.ApplyHistoricalTagRecords(ctx, batch, records)
	if err != nil || replayed.Replayed != len(records) {
		t.Fatalf("replay result=%+v err=%v", replayed, err)
	}
	verified, err := service.VerifyHistoricalTagRecords(ctx, batch, records)
	if err != nil || verified.Imported != 1 || verified.Pending != 1 || verified.Excluded != 1 {
		t.Fatalf("verify result=%+v err=%v", verified, err)
	}
	var effects, commands, jobs int
	if err = pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM external_effects),(SELECT count(*) FROM customer_tag_commands),(SELECT count(*) FROM river_job)`).Scan(&effects, &commands, &jobs); err != nil {
		t.Fatal(err)
	}
	if effects != 0 || commands != 0 || jobs != 0 {
		t.Fatalf("historical import created effects=%d commands=%d jobs=%d", effects, commands, jobs)
	}
	drifted := append([]customerport.HistoricalTagRecord(nil), records...)
	drifted[0].SourceState = "executed"
	if _, err = service.VerifyHistoricalTagRecords(ctx, batch, drifted); !errors.Is(err, customerport.ErrTagCommandConflict) {
		t.Fatalf("target drift err=%v", err)
	}
	conflicting := append([]customerport.HistoricalTagRecord(nil), records...)
	conflicting[0].SourceDigest = string(effectport.Hash("changed-source-fact"))
	if _, err = service.ApplyHistoricalTagRecords(ctx, batch, conflicting); !errors.Is(err, customerport.ErrTagCommandConflict) {
		t.Fatalf("source drift err=%v", err)
	}
}

func TestTagHistoryPostgreSQLConcurrentOverlappingSnapshotsReplayOnce(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := tagCommandPGPool(t, ctx, url)
	defer clean()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	service := customerapp.HistoricalTagImportService{UOW: uow, Store: TagHistoryPostgreSQL{}}
	at := time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC)
	record := customerport.HistoricalTagRecord{SourceJobID: 91, SourceDigest: string(effectport.Hash("same-v2-job")), EffectType: "wecom.contact.tag.mark", Operation: "tag_mark", SourceState: "provider_result_received", Resolution: "pending", Reason: "identity_unresolved", OccurredAt: at}
	batches := []customerport.HistoricalTagBatch{{SourceSystem: "v2_external_effect_job", SnapshotDigest: string(effectport.Hash("snapshot-one")), SnapshotAt: at}, {SourceSystem: "v2_external_effect_job", SnapshotDigest: string(effectport.Hash("snapshot-two")), SnapshotAt: at.Add(time.Second)}}
	type outcome struct {
		result customerport.HistoricalTagImportResult
		err    error
	}
	outcomes := make(chan outcome, len(batches))
	var start sync.WaitGroup
	start.Add(1)
	for _, batch := range batches {
		batch := batch
		go func() {
			start.Wait()
			got, applyErr := service.ApplyHistoricalTagRecords(ctx, batch, []customerport.HistoricalTagRecord{record})
			outcomes <- outcome{result: got, err: applyErr}
		}()
	}
	start.Done()
	var first, second customerport.HistoricalTagImportResult
	for index := 0; index < len(batches); index++ {
		got := <-outcomes
		if got.err != nil {
			t.Fatal(got.err)
		}
		if index == 0 {
			first = got.result
		} else {
			second = got.result
		}
	}
	if first.Pending+second.Pending != 1 || first.Replayed+second.Replayed != 1 {
		t.Fatalf("concurrent results first=%+v second=%+v", first, second)
	}
	var count int
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM customer_tag_history_receipts WHERE source_job_id=91`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("receipt count=%d err=%v", count, err)
	}
}
