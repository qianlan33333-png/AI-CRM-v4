package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// This test only changes the local Distributor enabled flag. The Payment
// reader is deliberately absent; no settlement or receiver action is invoked.
func TestPostgreSQLDistributorEnabledActorScopedOutboxAndReplay(t *testing.T) {
	pool, cleanup := registrationIntegrationPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	service := &AdminService{uow: uow, store: repository, now: func() time.Time { return now }}
	ctx := context.Background()
	ids := []int64{0, 0}
	for i := range ids {
		if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,created_at,updated_at,version) VALUES($1,$2,'v1',true,$3,$3,$3,1) RETURNING id`, 8100+i, "DLOCAL"+decimal(int64(i+1)), now).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	commands := []distributionport.AdminDistributorCommand{
		{DistributorID: ids[0], ExpectedVersion: 1, ActorScope: "access:1", Reason: "暂停推广", IdempotencyKey: "local-distributor-shared-key-0001"},
		{DistributorID: ids[1], ExpectedVersion: 1, ActorScope: "access:2", Reason: "暂停推广", IdempotencyKey: "local-distributor-shared-key-0001"},
	}
	for _, command := range commands {
		if err := service.SetDistributorEnabled(ctx, command, false); err != nil {
			t.Fatalf("disable: %v", err)
		}
		if err := service.SetDistributorEnabled(ctx, command, false); err != nil {
			t.Fatalf("exact replay: %v", err)
		}
		changed := command
		changed.Reason = "changed payload"
		if err := service.SetDistributorEnabled(ctx, changed, false); !errors.Is(err, distributionport.ErrConflict) {
			t.Fatalf("payload drift err=%v", err)
		}
	}
	var disabled, receipts, audits, outbox int64
	err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM distribution_distributors WHERE enabled=false AND version=2),(SELECT count(*) FROM distribution_operation_receipts WHERE operation='distributor_disable'),(SELECT count(*) FROM distribution_audit_events WHERE event_type='distribution.distributor_disabled.v1'),(SELECT count(*) FROM distribution_outbox WHERE event_type='distribution.distributor_disabled.v1')`).Scan(&disabled, &receipts, &audits, &outbox)
	if err != nil || disabled != 2 || receipts != 2 || audits != 2 || outbox != 2 {
		t.Fatalf("disabled/receipts/audits/outbox=%d/%d/%d/%d err=%v", disabled, receipts, audits, outbox, err)
	}
	for i, command := range commands {
		command.ExpectedVersion = 2
		command.Reason = "恢复推广"
		command.IdempotencyKey = strings.Repeat("k", 200)
		if err := service.SetDistributorEnabled(ctx, command, true); err != nil {
			t.Fatalf("enable: %v", err)
		}
		if err := service.SetDistributorEnabled(ctx, command, true); err != nil {
			t.Fatalf("enable replay: %v", err)
		}
		command.ExpectedVersion = 2
		command.IdempotencyKey = "stale-distributor-key-" + decimal(int64(i+1))
		if err := service.SetDistributorEnabled(ctx, command, false); !errors.Is(err, distributionport.ErrConflict) {
			t.Fatalf("stale version err=%v", err)
		}
	}
	var enabled int64
	err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM distribution_distributors WHERE enabled=true AND version=3),(SELECT count(*) FROM distribution_operation_receipts),(SELECT count(*) FROM distribution_audit_events),(SELECT count(*) FROM distribution_outbox)`).Scan(&enabled, &receipts, &audits, &outbox)
	if err != nil || enabled != 2 || receipts != 4 || audits != 4 || outbox != 4 {
		t.Fatalf("enabled/receipts/audits/outbox=%d/%d/%d/%d err=%v", enabled, receipts, audits, outbox, err)
	}
}
