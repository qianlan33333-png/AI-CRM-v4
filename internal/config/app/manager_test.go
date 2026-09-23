package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

type fakeUoW struct{ calls int }

func (uow *fakeUoW) Within(ctx context.Context, callback func(context.Context) error) error {
	uow.calls++
	return callback(ctx)
}

type fakeRepository struct {
	lockCalls, getCalls, auditCalls, upsertCalls int
	setting                                      configport.Setting
	found                                        bool
	audit                                        configport.Audit
	auditInserted                                bool
	receipt                                      configport.RequestReceipt
	receiptOwned                                 bool
	completedReceipt                             int64
}

func (repository *fakeRepository) ReserveCommandRequest(_ context.Context, _ string, _ string, _ string, digest []byte, _ time.Time) (configport.RequestReceipt, bool, error) {
	if repository.receipt.ID == 0 && repository.receiptOwned {
		repository.receipt = configport.RequestReceipt{ID: 1, PayloadDigest: append([]byte(nil), digest...), State: "reserved"}
	}
	return repository.receipt, repository.receiptOwned, nil
}
func (repository *fakeRepository) CompleteCommandRequest(_ context.Context, id int64, _ time.Time) error {
	repository.completedReceipt = id
	return nil
}

func (repository *fakeRepository) LockKey(context.Context, configport.Key) error {
	repository.lockCalls++
	return nil
}
func (repository *fakeRepository) Get(context.Context, configport.Key) (configport.Setting, bool, error) {
	repository.getCalls++
	return repository.setting, repository.found, nil
}
func (repository *fakeRepository) InsertAudit(context.Context, []byte, configport.SetCommand, []byte, time.Time) (configport.Audit, bool, error) {
	repository.auditCalls++
	return repository.audit, repository.auditInserted, nil
}
func (repository *fakeRepository) GetAuditByRequestID(context.Context, string) (configport.Audit, error) {
	return repository.audit, nil
}
func (repository *fakeRepository) Upsert(_ context.Context, command configport.SetCommand, canonical []byte, updatedAt time.Time) (configport.Setting, error) {
	repository.upsertCalls++
	return configport.Setting{Key: command.Key, Value: canonical, UpdatedBy: command.Actor, UpdatedAt: updatedAt}, nil
}

type fakeAppender struct {
	calls int
	event configport.Event
	err   error
}

func (appender *fakeAppender) Append(_ context.Context, event configport.Event) (configport.EventID, error) {
	appender.calls++
	appender.event = event
	return 1, appender.err
}

func TestSetRejectsSecretBeforeTransactionAndDoesNotExposeValue(t *testing.T) {
	uow, repo, events := &fakeUoW{}, &fakeRepository{}, &fakeAppender{}
	manager := NewManager(uow, repo, events)
	const sentinel = "database-password-sentinel"
	_, err := manager.Set(context.Background(), configport.SetCommand{
		Key: configport.WeComSecret, Value: []byte(`"` + sentinel + `"`),
		Actor: "admin:1", RequestID: "request-1",
	})
	if !errors.Is(err, configport.ErrSecretSetting) {
		t.Fatalf("Set() error = %v, want ErrSecretSetting", err)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatal("Set() error exposed secret value")
	}
	if uow.calls != 0 || repo.lockCalls != 0 || events.calls != 0 {
		t.Fatalf("secret reached transaction/repository/events: %d/%d/%d", uow.calls, repo.lockCalls, events.calls)
	}
}

func TestSetPersistsAuditAndEventWithoutValueInPayload(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	uow := &fakeUoW{}
	repo := &fakeRepository{auditInserted: true, audit: configport.Audit{ID: 9}}
	events := &fakeAppender{}
	manager := NewManager(uow, repo, events)
	manager.now = func() time.Time { return now }

	setting, err := manager.Set(context.Background(), configport.SetCommand{
		Key: configport.WeComCorpID, Value: []byte(`"corp-secret-looking-value"`),
		Actor: "admin:1", RequestID: "request-1",
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if string(setting.Value) != `"corp-secret-looking-value"` || repo.upsertCalls != 1 || events.calls != 1 {
		t.Fatalf("Set() result/upsert/events = %s/%d/%d", setting.Value, repo.upsertCalls, events.calls)
	}
	if strings.Contains(string(events.event.Payload), "corp-secret-looking-value") {
		t.Fatal("event payload included a setting value")
	}
	if events.event.IdempotencyKey != "setting.updated:request-1" || events.event.OccurredAt != now {
		t.Fatalf("event identity/time = %q/%v", events.event.IdempotencyKey, events.event.OccurredAt)
	}
}

func TestSetIdempotentReplayAndConflict(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	command := configport.SetCommand{Key: configport.OutboundMaxAttempts, Value: []byte(`3`), Actor: "admin:1", RequestID: "request-1"}
	repo := &fakeRepository{
		audit: configport.Audit{ID: 1, Key: command.Key, NewValue: []byte(`3`), UpdatedBy: command.Actor, RequestID: command.RequestID, UpdatedAt: now},
	}
	manager := NewManager(&fakeUoW{}, repo, &fakeAppender{})
	manager.now = func() time.Time { return now }
	setting, err := manager.Set(context.Background(), command)
	if err != nil || string(setting.Value) != "3" || repo.upsertCalls != 0 {
		t.Fatalf("idempotent Set() = %#v, %v; upserts=%d", setting, err, repo.upsertCalls)
	}

	command.Value = []byte(`4`)
	_, err = manager.Set(context.Background(), command)
	if !errors.Is(err, configport.ErrIdempotencyConflict) {
		t.Fatalf("conflicting Set() error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestSetPropagatesEventFailure(t *testing.T) {
	sentinel := errors.New("event append failed")
	repo := &fakeRepository{auditInserted: true, audit: configport.Audit{ID: 1}}
	manager := NewManager(&fakeUoW{}, repo, &fakeAppender{err: sentinel})
	_, err := manager.Set(context.Background(), configport.SetCommand{
		Key: configport.OutboundRatePerSecond, Value: []byte(`5`), Actor: "admin:1", RequestID: "request-1",
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Set() error = %v, want event sentinel", err)
	}
}

func TestSetManyUsesOneUoWForAllSettingsAuditAndOutboxFacts(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	uow := &fakeUoW{}
	repo := &fakeRepository{auditInserted: true, audit: configport.Audit{ID: 9}}
	events := &fakeAppender{}
	manager := NewManager(uow, repo, events)
	manager.now = func() time.Time { return now }
	err := manager.SetMany(context.Background(), []configport.SetCommand{
		{Key: configport.WeComCorpID, Value: []byte(`"corp"`), Actor: "admin:1", RequestID: "batch-1:corp"},
		{Key: configport.WeComAgentID, Value: []byte(`7`), Actor: "admin:1", RequestID: "batch-1:agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if uow.calls != 1 || repo.auditCalls != 2 || repo.upsertCalls != 2 || events.calls != 2 {
		t.Fatalf("uow/audits/upserts/events=%d/%d/%d/%d", uow.calls, repo.auditCalls, repo.upsertCalls, events.calls)
	}
}

func TestSetManyWithRequestTreatsTheWholeFormAsTheIdempotencyPayload(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	uow := &fakeUoW{}
	repo := &fakeRepository{auditInserted: true, audit: configport.Audit{ID: 9}, receiptOwned: true}
	events := &fakeAppender{}
	manager := NewManager(uow, repo, events)
	manager.now = func() time.Time { return now }
	commands := []configport.SetCommand{{Key: configport.WeComCorpID, Value: []byte(`"corp"`), Actor: "admin:1", RequestID: "browser-1:wecom.corp_id"}}
	if err := manager.SetManyWithRequest(context.Background(), "admin:1", "browser-1", []byte(`[{"key":"wecom.corp_id","value":"corp"}]`), commands); err != nil {
		t.Fatal(err)
	}
	if uow.calls != 1 || repo.completedReceipt != 1 || repo.upsertCalls != 1 || events.calls != 1 {
		t.Fatalf("uow/receipt/upsert/events=%d/%d/%d/%d", uow.calls, repo.completedReceipt, repo.upsertCalls, events.calls)
	}
	repo.receiptOwned = false
	repo.receipt.State = "completed"
	if err := manager.SetManyWithRequest(context.Background(), "admin:1", "browser-1", []byte(`[{"key":"wecom.corp_id","value":"other"}]`), commands); !errors.Is(err, configport.ErrIdempotencyConflict) {
		t.Fatalf("payload drift error=%v", err)
	}
	if repo.upsertCalls != 1 || events.calls != 1 {
		t.Fatalf("payload drift mutated config: upserts/events=%d/%d", repo.upsertCalls, events.calls)
	}
}

func TestSetRejectsAmbiguousAuditMetadataBeforeTransaction(t *testing.T) {
	for _, command := range []configport.SetCommand{
		{Key: configport.WeComAgentID, Value: []byte(`1`), Actor: " admin:1", RequestID: "request-1"},
		{Key: configport.WeComAgentID, Value: []byte(`1`), Actor: "admin:1", RequestID: "request-1 "},
	} {
		uow := &fakeUoW{}
		manager := NewManager(uow, &fakeRepository{}, &fakeAppender{})
		if _, err := manager.Set(context.Background(), command); !errors.Is(err, configport.ErrInvalidSetting) {
			t.Fatalf("Set() error = %v, want ErrInvalidSetting", err)
		}
		if uow.calls != 0 {
			t.Fatalf("invalid metadata entered %d transactions", uow.calls)
		}
	}
}
