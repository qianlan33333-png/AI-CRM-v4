package app

import (
	"context"
	"testing"
	"time"

	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

type scheduleStoreStub struct {
	items   []segmentdomain.ScheduledConfiguration
	claimed int
	next    time.Time
}

func (store *scheduleStoreStub) ScheduledConfigurations(context.Context, int) ([]segmentdomain.ScheduledConfiguration, error) {
	return append([]segmentdomain.ScheduledConfiguration(nil), store.items...), nil
}
func (store *scheduleStoreStub) ClaimScheduledOccurrence(_ context.Context, _ segmentdomain.ScheduledConfiguration, _, next, _ time.Time) (bool, error) {
	store.claimed++
	store.next = next
	return true, nil
}

type scheduledAccepterStub struct {
	commands []RefreshCommand
}

func (stub *scheduledAccepterStub) AcceptRefreshWithin(_ context.Context, command RefreshCommand) (segmentdomain.RefreshRun, error) {
	stub.commands = append(stub.commands, command)
	return segmentdomain.RefreshRun{ID: 1}, nil
}

func TestScheduledRefreshClaimsAndAcceptsSameOccurrence(t *testing.T) {
	created := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC)
	store := &scheduleStoreStub{items: []segmentdomain.ScheduledConfiguration{{PackageID: 8, ConfigurationVersionID: 9, CronUTC: "0 2 * * *", Actor: 4, ConfigurationCreatedAt: created}}}
	accepter := &scheduledAccepterStub{}
	service, err := NewScheduledRefreshService(directUOW{}, store, accepter)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	if err = service.ScanScheduled(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.claimed != 1 || len(accepter.commands) != 1 {
		t.Fatalf("claimed=%d commands=%d", store.claimed, len(accepter.commands))
	}
	if !accepter.commands[0].ReferenceTime.Equal(time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC)) || !store.next.Equal(time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)) {
		t.Fatalf("command=%+v next=%s", accepter.commands[0], store.next)
	}
	if len(accepter.commands[0].IdempotencyKey) != len("schedule-")+64 {
		t.Fatalf("idempotency=%q", accepter.commands[0].IdempotencyKey)
	}
}

func TestScheduledRefreshSkipsFutureAndRejectsInvalidCron(t *testing.T) {
	now := time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC)
	next := now.Add(time.Hour)
	store := &scheduleStoreStub{items: []segmentdomain.ScheduledConfiguration{{PackageID: 8, ConfigurationVersionID: 9, CronUTC: "0 * * * *", Actor: 4, ConfigurationCreatedAt: now.Add(-time.Hour), NextDueAt: &next, ScheduleVersion: 1}}}
	accepter := &scheduledAccepterStub{}
	service, _ := NewScheduledRefreshService(directUOW{}, store, accepter)
	service.now = func() time.Time { return now }
	if err := service.ScanScheduled(context.Background()); err != nil || store.claimed != 0 || len(accepter.commands) != 0 {
		t.Fatalf("claimed=%d commands=%d err=%v", store.claimed, len(accepter.commands), err)
	}
	if ValidateRefreshCronUTC("bad") == nil || ValidateRefreshCronUTC("0 2 * * *") != nil || ValidateRefreshCronUTC("") != nil {
		t.Fatal("cron validation mismatch")
	}
	for _, mode := range []string{"manual", "every_3m", "daily_0200", "every_3m_plus_daily_0200"} {
		if err := ValidateRefresh(mode, ""); err != nil {
			t.Fatalf("mode=%s err=%v", mode, err)
		}
	}
	if ValidateRefresh("daily_0200", "0 2 * * *") == nil || ValidateRefresh("legacy_custom", "0 2 * * *") != nil {
		t.Fatal("new and legacy refresh mode validation mismatch")
	}
}

func TestScheduledRefreshKeepsKindsIndependent(t *testing.T) {
	now := time.Date(2026, 9, 4, 18, 1, 0, 0, time.UTC) // Shanghai 02:01 on Sep 5.
	store := &scheduleStoreStub{items: []segmentdomain.ScheduledConfiguration{
		{PackageID: 8, ConfigurationVersionID: 9, Kind: "incremental", CronUTC: "*/3 * * * *", Actor: 4, ConfigurationCreatedAt: now.Add(-5 * time.Minute)},
		{PackageID: 8, ConfigurationVersionID: 9, Kind: "daily", CronUTC: "0 18 * * *", Actor: 4, ConfigurationCreatedAt: now.Add(1 * time.Minute)},
	}}
	accepter := &scheduledAccepterStub{}
	service, _ := NewScheduledRefreshService(directUOW{}, store, accepter)
	service.now = func() time.Time { return now }
	if err := service.ScanScheduled(context.Background()); err != nil || store.claimed != 1 || len(accepter.commands) != 1 {
		t.Fatalf("claimed=%d commands=%d err=%v", store.claimed, len(accepter.commands), err)
	}
}

func TestScheduledRefreshPreservesMachineMutationActor(t *testing.T) {
	created := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC)
	store := &scheduleStoreStub{items: []segmentdomain.ScheduledConfiguration{{
		PackageID: 8, ConfigurationVersionID: 9, CronUTC: "0 2 * * *", ActorKind: "machine", ActorReference: "machine:open-audience", ConfigurationCreatedAt: created,
	}}}
	accepter := &scheduledAccepterStub{}
	service, err := NewScheduledRefreshService(directUOW{}, store, accepter)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	if err = service.ScanScheduled(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(accepter.commands) != 1 {
		t.Fatalf("commands=%+v", accepter.commands)
	}
	command := accepter.commands[0]
	if command.Actor != 0 || command.MutationActor.Kind != "machine" || command.MutationActor.Reference != "machine:open-audience" {
		t.Fatalf("machine schedule actor lost: %+v", command)
	}
}
