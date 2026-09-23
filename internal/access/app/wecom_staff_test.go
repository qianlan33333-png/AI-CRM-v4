package app

import (
	"context"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
)

type staffAuditRecorder struct{ events []platformaudit.Event }

func (recorder *staffAuditRecorder) Append(_ context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	recorder.events = append(recorder.events, event)
	return event, nil
}

func TestWeComStaffProjectorCreatesViewerAndPreservesExistingState(t *testing.T) {
	repository := newMemoryRepository()
	repository.users[1] = domain.User{ID: 1, Username: "existing", DisplayName: "Existing", WeComUserID: "staff-1", Active: false, Roles: []domain.Role{domain.RoleAdmin}}
	repository.nextID = 2
	audit := &staffAuditRecorder{}
	service, err := NewWeComStaffProjector(repository, testPasswords{}, audit)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ProjectWeComStaffWithin(context.Background(), "periodic-20260904T1200", []accessport.WeComStaffProjection{
		{WeComUserID: "staff-2", DisplayName: ""}, {WeComUserID: "staff-1", DisplayName: "Ignored"}, {WeComUserID: "staff-2"},
	}, time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.Discovered != 2 || result.Created != 1 || result.Existing != 1 || result.Inactive != 1 {
		t.Fatalf("result=%+v", result)
	}
	created, err := repository.UserByWeComUserID(context.Background(), "staff-2", false)
	if err != nil || !created.Active || created.LoginEnabled || created.AccessGrantedAt != nil || len(created.Roles) != 1 || created.Roles[0] != domain.RoleViewer || created.PasswordHash == "" {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	if repository.users[1].Active || repository.users[1].DisplayName != "Existing" {
		t.Fatalf("existing user was mutated: %+v", repository.users[1])
	}
	if len(audit.events) != 1 || audit.events[0].Action != "access.wecom_staff_projected" {
		t.Fatalf("audits=%+v", audit.events)
	}
}
