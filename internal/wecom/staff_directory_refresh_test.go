package wecom

import (
	"context"
	"errors"
	"testing"
	"time"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type staffRefreshProvider struct {
	values []string
	err    error
}

func (provider staffRefreshProvider) DirectoryReady() bool { return true }
func (provider staffRefreshProvider) ListContactStaff(context.Context) ([]string, error) {
	return provider.values, provider.err
}
func (staffRefreshProvider) BatchExternalContacts(context.Context, string, string, int) (wecomport.ExternalContactPage, error) {
	return wecomport.ExternalContactPage{}, nil
}

type staffRefreshProjector struct {
	input []accessport.WeComStaffProjection
}

func (projector *staffRefreshProjector) ProjectWeComStaffWithin(_ context.Context, _ string, input []accessport.WeComStaffProjection, _ time.Time) (accessport.WeComStaffProjectionResult, error) {
	projector.input = append([]accessport.WeComStaffProjection(nil), input...)
	return accessport.WeComStaffProjectionResult{Discovered: int64(len(input)), Created: int64(len(input))}, nil
}

type staffRefreshStore struct {
	state  string
	replay bool
	result accessport.WeComStaffProjectionResult
	code   string
}

func (store *staffRefreshStore) Begin(context.Context, string, string, time.Time) (StaffDirectoryRefreshRun, bool, error) {
	store.state = "running"
	return StaffDirectoryRefreshRun{ID: 7, State: "running"}, store.replay, nil
}
func (store *staffRefreshStore) Succeed(_ context.Context, _ int64, result accessport.WeComStaffProjectionResult, _ [32]byte, _ time.Time) error {
	store.state, store.result = "succeeded", result
	return nil
}
func (store *staffRefreshStore) Fail(_ context.Context, _ int64, code string, terminal bool, _ time.Time) error {
	store.state, store.code = "failed_retryable", code
	if terminal {
		store.state = "failed_terminal"
	}
	return nil
}

type staffRefreshAudit struct{ count int }

func (audit *staffRefreshAudit) Append(_ context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	audit.count++
	return event, nil
}

func TestStaffDirectoryRefreshCanonicalizesAndProjectsProviderDirectory(t *testing.T) {
	projector, store, audit := &staffRefreshProjector{}, &staffRefreshStore{}, &staffRefreshAudit{}
	service := StaffDirectoryRefreshService{Enabled: true, Provider: staffRefreshProvider{values: []string{"staff-b", "staff-a", "staff-b"}}, Projector: projector, DisplayNames: map[string]string{"staff-a": "客服甲"}, Store: store, Audit: audit, UOW: directUOW{}, Now: func() time.Time { return time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC) }}
	if err := service.Refresh(context.Background(), "periodic-20260904T120000Z", "periodic", false); err != nil {
		t.Fatal(err)
	}
	if store.state != "succeeded" || store.result.Discovered != 2 || store.result.Created != 2 || audit.count != 1 {
		t.Fatalf("state=%s result=%+v audits=%d", store.state, store.result, audit.count)
	}
	if len(projector.input) != 2 || projector.input[0].WeComUserID != "staff-a" || projector.input[1].WeComUserID != "staff-b" {
		t.Fatalf("projected=%+v", projector.input)
	}
	if projector.input[0].DisplayName != "客服甲" || projector.input[1].DisplayName != "" {
		t.Fatalf("display names=%+v", projector.input)
	}
}

func TestStaffDirectoryRefreshRecordsTerminalProviderFailureWithoutProjection(t *testing.T) {
	projector, store := &staffRefreshProjector{}, &staffRefreshStore{}
	service := StaffDirectoryRefreshService{Enabled: true, Provider: staffRefreshProvider{err: errors.New("provider unavailable")}, Projector: projector, Store: store, Audit: &staffRefreshAudit{}, UOW: directUOW{}}
	err := service.Refresh(context.Background(), "periodic-20260904T120000Z", "periodic", true)
	if err == nil || store.state != "failed_terminal" || store.code != "provider_read_failed" || len(projector.input) != 0 {
		t.Fatalf("err=%v state=%s code=%s projected=%d", err, store.state, store.code, len(projector.input))
	}
}

func TestStaffDirectoryRefreshRejectsUnexpectedEmptyProviderDirectory(t *testing.T) {
	projector, store := &staffRefreshProjector{}, &staffRefreshStore{}
	service := StaffDirectoryRefreshService{Enabled: true, Provider: staffRefreshProvider{values: []string{}}, Projector: projector, Store: store, Audit: &staffRefreshAudit{}, UOW: directUOW{}}
	err := service.Refresh(context.Background(), "periodic-20260904T120000Z", "periodic", false)
	if err == nil || store.state != "failed_retryable" || store.code != "provider_response_invalid" || len(projector.input) != 0 {
		t.Fatalf("err=%v state=%s code=%s projected=%d", err, store.state, store.code, len(projector.input))
	}
}

func TestStaffDirectoryRefreshDisabledIsNoop(t *testing.T) {
	service := StaffDirectoryRefreshService{}
	if err := service.Refresh(context.Background(), "ignored", "periodic", false); err != nil {
		t.Fatal(err)
	}
}
