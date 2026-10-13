package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

// Only the local Access directory is synthetic. Segment receipts, CAS,
// immutable sender snapshots, audit and Outbox use real PostgreSQL/UoW.
type replaySenderDirectory struct {
	mu    sync.Mutex
	items map[string]accessport.StaffEligibility
	err   error
	reads int
}

func (d *replaySenderDirectory) ResolveAutomationSender(_ context.Context, ref string) (accessport.StaffEligibility, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reads++
	item, found := d.items[ref]
	return item, found, d.err
}
func (d *replaySenderDirectory) AutomationSender(_ context.Context, id accessport.StaffID) (accessport.StaffEligibility, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, item := range d.items {
		if item.StaffID == id {
			return item, true, d.err
		}
	}
	return accessport.StaffEligibility{}, false, d.err
}
func (d *replaySenderDirectory) refresh(active bool, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for ref, item := range d.items {
		item.Active, item.Eligible = active, active
		item.EligibilityVersion++
		item.RefreshedAt = item.RefreshedAt.Add(time.Minute)
		d.items[ref] = item
	}
	d.err = err
}

type senderReplayFixture struct {
	pool    *pgxpool.Pool
	uow     *platformpostgres.UnitOfWork
	repo    *segmentstore.Repository
	service *ExecutionService
	dir     *replaySenderDirectory
	command SendersCommand
}

func newSenderReplayFixture(t *testing.T) senderReplayFixture {
	t.Helper()
	ctx := context.Background()
	pool, cleanup := scheduleRuntimeDatabase(t, ctx)
	t.Cleanup(cleanup)
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := segmentstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := NewService(uow, repo).CreatePackage(ctx, PackageCreateCommand{Name: "Sender replay", TemplateKey: "active_contacts", Actor: 7, IdempotencyKey: "sender-replay-package-001"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC)
	dir := &replaySenderDirectory{items: map[string]accessport.StaffEligibility{
		"sender-a": {StaffID: 11, Active: true, Eligible: true, EligibilityVersion: 2, RefreshedAt: now},
		"sender-b": {StaffID: 12, Active: true, Eligible: true, EligibilityVersion: 3, RefreshedAt: now},
	}}
	service, err := NewExecutionService(uow, repo, publishedAgentStub{}, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return senderReplayFixture{pool: pool, uow: uow, repo: repo, service: service, dir: dir, command: SendersCommand{PackageID: pkg.ID, ExpectedPackageVersion: pkg.Version, ProviderMemberIDs: []string{"sender-a", "sender-b"}, Actor: 7, IdempotencyKey: "sender-replay-command-001"}}
}

func (f senderReplayFixture) facts(t *testing.T) map[string][32]byte {
	t.Helper()
	result := map[string][32]byte{}
	for _, table := range []string{"segment_audience_packages", "segment_audience_sender_sets", "segment_audience_sender_set_members", "segment_audience_operation_receipts", "segment_audience_audit_events", "segment_audience_outbox"} {
		var raw string
		if err := f.pool.QueryRow(context.Background(), `SELECT COALESCE(jsonb_agg(to_jsonb(f) ORDER BY to_jsonb(f)::text),'[]'::jsonb)::text FROM `+table+` f`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		result[table] = sha256.Sum256([]byte(raw))
	}
	return result
}
func (f senderReplayFixture) assertFacts(t *testing.T, expected map[string][32]byte) {
	t.Helper()
	if !reflect.DeepEqual(f.facts(t), expected) {
		t.Fatal("receipt replay/rejection changed package, snapshots, receipts, audit or Outbox")
	}
}
func (f senderReplayFixture) assertAcceptedOnce(t *testing.T) {
	t.Helper()
	var sets, receipts, audits, events int
	if err := f.pool.QueryRow(context.Background(), `SELECT
	 (SELECT count(*) FROM segment_audience_sender_sets),
	 (SELECT count(*) FROM segment_audience_operation_receipts WHERE operation='replace_senders'),
	 (SELECT count(*) FROM segment_audience_audit_events WHERE resource_kind='sender_set'),
	 (SELECT count(*) FROM segment_audience_outbox WHERE event_type='audience.senders.replaced.v1')`).Scan(&sets, &receipts, &audits, &events); err != nil {
		t.Fatal(err)
	}
	if sets != 1 || receipts != 1 || audits != 1 || events != 1 {
		t.Fatalf("accepted more than once: sets/receipts/audit/outbox=%d/%d/%d/%d", sets, receipts, audits, events)
	}
}

func TestPostgreSQLSenderReplayIgnoresMutableDirectoryButNewCommandsValidate(t *testing.T) {
	f := newSenderReplayFixture(t)
	ctx := context.Background()
	first, err := f.service.ReplaceSenders(ctx, f.command)
	if err != nil {
		t.Fatal(err)
	}
	accepted := f.facts(t)
	for _, state := range []struct {
		name   string
		active bool
		err    error
	}{{"directory_refresh", true, nil}, {"employee_disabled", false, nil}, {"directory_unavailable", false, errors.New("synthetic directory failure")}} {
		t.Run(state.name, func(t *testing.T) {
			f.dir.refresh(state.active, state.err)
			beforeReads := f.dir.reads
			got, e := f.service.ReplaceSenders(ctx, f.command)
			if e != nil || !reflect.DeepEqual(got, first) || f.dir.reads != beforeReads {
				t.Fatalf("accepted replay consulted mutable eligibility or lost original result: exact=%t read_unchanged=%t err=%v", reflect.DeepEqual(got, first), f.dir.reads == beforeReads, e)
			}
			f.assertFacts(t, accepted)
		})
	}
	f.dir.refresh(false, nil)
	newCommand := f.command
	newCommand.ExpectedPackageVersion++
	newCommand.IdempotencyKey = "sender-replay-new-command-002"
	if _, err = f.service.ReplaceSenders(ctx, newCommand); !errors.Is(err, ErrNotReady) {
		t.Fatalf("new command reused disabled employee eligibility: %v", err)
	}
	f.assertFacts(t, accepted)
	for _, drift := range []SendersCommand{
		{PackageID: f.command.PackageID, ExpectedPackageVersion: f.command.ExpectedPackageVersion + 1, ProviderMemberIDs: f.command.ProviderMemberIDs, Actor: 7, IdempotencyKey: f.command.IdempotencyKey},
		{PackageID: f.command.PackageID, ExpectedPackageVersion: f.command.ExpectedPackageVersion, ProviderMemberIDs: []string{"sender-b", "sender-a"}, Actor: 7, IdempotencyKey: f.command.IdempotencyKey},
		{PackageID: f.command.PackageID, ExpectedPackageVersion: f.command.ExpectedPackageVersion, ProviderMemberIDs: []string{"sender-a"}, Actor: 7, IdempotencyKey: f.command.IdempotencyKey},
		{PackageID: f.command.PackageID, ExpectedPackageVersion: f.command.ExpectedPackageVersion, ProviderMemberIDs: f.command.ProviderMemberIDs, Actor: 8, IdempotencyKey: f.command.IdempotencyKey},
	} {
		if got, e := f.service.ReplaceSenders(ctx, drift); !errors.Is(e, ErrConflict) || got.ID != 0 {
			t.Fatalf("request/actor drift exposed an old receipt: empty=%t err=%v", got.ID == 0, e)
		}
		f.assertFacts(t, accepted)
	}
	f.assertAcceptedOnce(t)
}

func TestPostgreSQLLegacySenderReceiptRecoversOnlyProvenStaffMapping(t *testing.T) {
	f := newSenderReplayFixture(t)
	ctx := context.Background()
	first, err := f.service.ReplaceSenders(ctx, f.command)
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := mutationActor(f.command.Actor, f.command.MutationActor)
	members := append([]segmentdomain.Sender(nil), first.Members...)
	for i := range members {
		members[i].SortOrder = 0
	}
	legacy := sha256.Sum256(mutationCommandPayload(actor, struct {
		PackageID int64
		Expected  int64
		StaffIDs  []segmentdomain.Sender
	}{f.command.PackageID, f.command.ExpectedPackageVersion, members}))
	// Fixture-only pre-fix receipt. No production backfill rewrites a digest.
	if _, err = f.pool.Exec(ctx, `UPDATE segment_audience_operation_receipts SET payload_digest=$1 WHERE operation='replace_senders'`, legacy[:]); err != nil {
		t.Fatal(err)
	}
	accepted := f.facts(t)
	f.dir.refresh(false, nil)
	got, err := f.service.ReplaceSenders(ctx, f.command)
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("legacy eligibility refresh/disable lost original result: exact=%t err=%v", reflect.DeepEqual(got, first), err)
	}
	f.assertFacts(t, accepted)
	f.dir.items["sender-a"] = accessport.StaffEligibility{StaffID: 999} // Reassigned reference.
	if got, e := f.service.ReplaceSenders(ctx, f.command); !errors.Is(e, ErrConflict) || got.ID != 0 {
		t.Fatalf("legacy replay guessed reassigned employee: empty=%t err=%v", got.ID == 0, e)
	}
	delete(f.dir.items, "sender-a")
	if _, err = f.service.ReplaceSenders(ctx, f.command); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy missing mapping did not fail closed: %v", err)
	}
	f.assertFacts(t, accepted)
	f.assertAcceptedOnce(t)
}

func TestPostgreSQLSenderConcurrentCommandsKeepReceiptAndCASAtomic(t *testing.T) {
	for _, kind := range []string{"same_key_same_payload", "same_key_different_payload", "different_keys_same_version"} {
		t.Run(kind, func(t *testing.T) {
			f := newSenderReplayFixture(t)
			commands := []SendersCommand{f.command, f.command}
			if kind == "same_key_different_payload" {
				commands[1].ProviderMemberIDs = []string{"sender-b"}
			}
			if kind == "different_keys_same_version" {
				commands[1].IdempotencyKey = "sender-replay-concurrent-new-key"
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			values := make([]segmentdomain.SenderSet, len(commands))
			errorsFound := make([]error, len(commands))
			for i := range commands {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					values[i], errorsFound[i] = f.service.ReplaceSenders(context.Background(), commands[i])
				}(i)
			}
			close(start)
			wg.Wait()
			successes := 0
			for _, e := range errorsFound {
				if e == nil {
					successes++
				} else if !errors.Is(e, ErrConflict) {
					t.Fatalf("unexpected concurrent error: %v", e)
				}
			}
			if kind == "same_key_same_payload" {
				if successes != 2 || !reflect.DeepEqual(values[0], values[1]) {
					t.Fatal("same command did not return one exact original result")
				}
			} else if successes != 1 {
				t.Fatalf("competing commands bypassed payload or CAS fence: successes=%d", successes)
			}
			f.assertAcceptedOnce(t)
		})
	}
}

type senderCompletionFailure struct{ *segmentstore.Repository }

func (s senderCompletionFailure) Complete(context.Context, int64, json.RawMessage, time.Time) (segmentstore.Receipt, error) {
	return segmentstore.Receipt{}, errors.New("synthetic completion failure")
}

func TestPostgreSQLSenderFailedCompletionRollsBackAllFactsAndAllowsOriginalKey(t *testing.T) {
	f := newSenderReplayFixture(t)
	before := f.facts(t)
	failing, err := NewExecutionService(f.uow, senderCompletionFailure{f.repo}, publishedAgentStub{}, f.dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = failing.ReplaceSenders(context.Background(), f.command); err == nil {
		t.Fatal("injected completion failure unexpectedly succeeded")
	}
	f.assertFacts(t, before)
	if _, err = f.service.ReplaceSenders(context.Background(), f.command); err != nil {
		t.Fatalf("rolled-back reservation poisoned original key: %v", err)
	}
	f.assertAcceptedOnce(t)
}
