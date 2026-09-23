package wecom

import (
	"context"
	"errors"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type classifiedDirectoryFailure struct {
	code      string
	retryable bool
}

func (failure classifiedDirectoryFailure) Error() string                   { return failure.code }
func (failure classifiedDirectoryFailure) DirectoryFailureCode() string    { return failure.code }
func (failure classifiedDirectoryFailure) DirectoryFailureRetryable() bool { return failure.retryable }

func TestClassifySyncFailureKeepsBusinessFailureStateDistinct(t *testing.T) {
	tests := []struct {
		name       string
		cause      error
		wantStatus CustomerSyncStatus
		wantCode   string
	}{
		{name: "disabled", cause: wecomport.ErrDirectoryDisabled, wantStatus: SyncFailedTerminal, wantCode: "provider_disabled"},
		{name: "permission", cause: classifiedDirectoryFailure{code: "provider_permission_denied"}, wantStatus: SyncFailedTerminal, wantCode: "provider_permission_denied"},
		{name: "rate limited", cause: classifiedDirectoryFailure{code: "provider_rate_limited", retryable: true}, wantStatus: SyncFailedRetryable, wantCode: "provider_rate_limited"},
		{name: "temporary", cause: classifiedDirectoryFailure{code: "provider_unavailable", retryable: true}, wantStatus: SyncFailedRetryable, wantCode: "provider_unavailable"},
		{name: "unknown", cause: errors.New("temporary failure"), wantStatus: SyncFailedRetryable, wantCode: "provider_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, code := classifySyncFailure(test.cause)
			if status != test.wantStatus || code != test.wantCode {
				t.Fatalf("status=%s code=%s", status, code)
			}
		})
	}
}

type retryExhaustedProvider struct{ failure error }

func (retryExhaustedProvider) DirectoryReady() bool { return true }
func (provider retryExhaustedProvider) ListContactStaff(context.Context) ([]string, error) {
	return nil, provider.failure
}
func (retryExhaustedProvider) BatchExternalContacts(context.Context, string, string, int) (wecomport.ExternalContactPage, error) {
	return wecomport.ExternalContactPage{}, nil
}

type retryExhaustedIdentity struct{}

func (retryExhaustedIdentity) ProvisionVerifiedIdentity(context.Context, identityport.ProvisionCommand) (identityport.ProvisionResult, error) {
	return identityport.ProvisionResult{}, errors.New("unexpected identity provision")
}

type retryExhaustedProjection struct{}

func (retryExhaustedProjection) UpsertDirectoryProjection(context.Context, customerport.DirectoryProjection) error {
	return errors.New("unexpected directory projection")
}
func (retryExhaustedProjection) MarkDirectoryStale(context.Context, []customerdomain.CustomerID, time.Time) (int64, error) {
	return 0, errors.New("unexpected stale projection")
}
func (retryExhaustedProjection) UpdateDirectoryPhone(context.Context, customerdomain.CustomerID, string, identitydomain.Assurance, int64, time.Time) error {
	return errors.New("unexpected phone projection")
}
func (retryExhaustedProjection) ClearDirectoryPhone(context.Context, customerdomain.CustomerID, time.Time) error {
	return errors.New("unexpected phone projection")
}

type retryExhaustedTimeline struct{}

func (retryExhaustedTimeline) AppendTimeline(context.Context, customerport.TimelineEvent) error {
	return errors.New("unexpected timeline append")
}

type retryExhaustedOutbox struct{}

func (retryExhaustedOutbox) Append(context.Context, platformoutbox.Event) (platformoutbox.Event, error) {
	return platformoutbox.Event{}, errors.New("unexpected outbox append")
}
func (retryExhaustedOutbox) PendingForSyncRun(context.Context, int64) (int64, error) {
	return 0, errors.New("unexpected outbox reconciliation")
}

type retryExhaustedStore struct {
	CustomerSyncStore
	run CustomerSyncRun
}

func (store *retryExhaustedStore) Get(context.Context, int64) (CustomerSyncRun, error) {
	return store.run, nil
}
func (store *retryExhaustedStore) Fail(_ context.Context, id, version int64, status CustomerSyncStatus, code string) error {
	if id != store.run.ID || version != store.run.Version {
		return ErrSyncCAS
	}
	store.run.ResumeStatus, store.run.Status, store.run.LastErrorCode = store.run.Status, status, code
	store.run.Version++
	return nil
}
func (store *retryExhaustedStore) Terminate(_ context.Context, id int64, code string) error {
	if id != store.run.ID || store.run.Status != SyncFailedRetryable {
		return ErrSyncCAS
	}
	store.run.Status, store.run.ResumeStatus, store.run.LastErrorCode = SyncFailedTerminal, "", code
	store.run.Version++
	return nil
}

type retryExhaustedEnqueuer struct{}

func (retryExhaustedEnqueuer) EnqueueCustomerSync(context.Context, int64) error { return nil }

type retryExhaustedAudit struct{}

func (retryExhaustedAudit) Append(_ context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	return event, nil
}

type boundedClassifiedDirectoryFailure struct {
	classifiedDirectoryFailure
	maxAttempts int
}

func (failure boundedClassifiedDirectoryFailure) DirectoryFailureMaxAttempts() int {
	return failure.maxAttempts
}

func TestCustomerSyncWorkerPersistsRetryExhaustionAsBusinessTerminalState(t *testing.T) {
	tests := []struct {
		name       string
		failure    error
		attempt    int
		wantStatus CustomerSyncStatus
		wantCode   string
		wantErr    bool
	}{
		{name: "system busy stops at its narrower third attempt", failure: boundedClassifiedDirectoryFailure{classifiedDirectoryFailure: classifiedDirectoryFailure{code: "provider_unavailable", retryable: true}, maxAttempts: 3}, attempt: 3, wantStatus: SyncFailedTerminal, wantCode: "retry_exhausted:provider_unavailable"},
		{name: "rate limit keeps the normal budget after attempt three", failure: classifiedDirectoryFailure{code: "provider_rate_limited", retryable: true}, attempt: 3, wantStatus: SyncFailedRetryable, wantCode: "provider_rate_limited", wantErr: true},
		{name: "rate limit exhausts the normal twelfth attempt", failure: classifiedDirectoryFailure{code: "provider_rate_limited", retryable: true}, attempt: 12, wantStatus: SyncFailedTerminal, wantCode: "retry_exhausted:provider_rate_limited"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &retryExhaustedStore{run: CustomerSyncRun{ID: 8, Status: SyncListingStaff, Version: 3}}
			service := CustomerSyncService{
				Enabled: true, CorpID: "corp", Provider: retryExhaustedProvider{failure: test.failure}, Identity: retryExhaustedIdentity{}, Projection: retryExhaustedProjection{},
				Timeline: retryExhaustedTimeline{}, Store: store, Outbox: retryExhaustedOutbox{}, Enqueuer: retryExhaustedEnqueuer{}, Audit: retryExhaustedAudit{}, UOW: directUOW{},
			}
			worker := NewCustomerSyncWorker()
			if err := worker.BindService(service); err != nil {
				t.Fatal(err)
			}
			err := worker.Work(context.Background(), &river.Job[CustomerSyncJobArgs]{JobRow: &rivertype.JobRow{Attempt: test.attempt, MaxAttempts: 12}, Args: CustomerSyncJobArgs{RunID: 8}})
			if (err != nil) != test.wantErr || store.run.Status != test.wantStatus || store.run.LastErrorCode != test.wantCode {
				t.Fatalf("err=%v run=%+v", err, store.run)
			}
		})
	}
}
