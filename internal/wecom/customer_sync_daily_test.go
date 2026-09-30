package wecom

import (
	"context"
	"github.com/riverqueue/river"
	"testing"
	"time"
)

type dailySyncCapture struct {
	CustomerSyncStore
	calls []CreateCustomerSyncRun
}

func (s *dailySyncCapture) Create(_ context.Context, c CreateCustomerSyncRun) (CustomerSyncRun, bool, error) {
	s.calls = append(s.calls, c)
	return CustomerSyncRun{ID: 1}, false, nil
}
func TestDailyDirectoryStartsAtOneBeijingTime(t *testing.T) {
	store := &dailySyncCapture{}
	now := time.Date(2026, 9, 30, 16, 59, 59, 0, time.UTC)
	service := CustomerSyncService{Enabled: true, CorpID: "synthetic", Provider: retryExhaustedProvider{}, Identity: retryExhaustedIdentity{}, Projection: retryExhaustedProjection{}, Timeline: retryExhaustedTimeline{}, Store: store, Outbox: retryExhaustedOutbox{}, Enqueuer: retryExhaustedEnqueuer{}, Audit: retryExhaustedAudit{}, UOW: directUOW{}, Now: func() time.Time { return now }}
	worker := CustomerSyncDailyWorker{Service: &service}
	if err := worker.Work(context.Background(), &river.Job[CustomerSyncDailyArgs]{}); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 0 {
		t.Fatal("refresh started before Beijing 01:00")
	}
	now = now.Add(time.Second)
	if err := worker.Work(context.Background(), &river.Job[CustomerSyncDailyArgs]{}); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 || store.calls[0].Trigger != "daily" || store.calls[0].CorpScope != "wecom-corp:synthetic" || store.calls[0].RunKey[len(store.calls[0].RunKey)-10:] != "2026-10-01" {
		t.Fatal(store.calls)
	}
	first := store.calls[0].RunKey
	now = now.Add(time.Hour)
	if err := worker.Work(context.Background(), &river.Job[CustomerSyncDailyArgs]{}); err != nil {
		t.Fatal(err)
	}
	if store.calls[1].RunKey != first {
		t.Fatal("same Beijing day changed dedupe key")
	}
}
