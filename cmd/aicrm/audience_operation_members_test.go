package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type audienceOperationMemberUnitStub struct{}

var _ platformport.UnitOfWork = audienceOperationMemberUnitStub{}

func (audienceOperationMemberUnitStub) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type audienceOperationMemberDirectoryStub struct {
	items []groupopsport.OperationMember
}

func (stub audienceOperationMemberDirectoryStub) ListEligibleStaff(context.Context) ([]groupopsport.OperationMember, error) {
	return stub.items, nil
}

type audienceOperationMemberSecurityStub struct{}

func (audienceOperationMemberSecurityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}

func TestAudienceOperationMemberPickerReturnsEligibleStaffForAudienceScope(t *testing.T) {
	picker := audienceOperationMemberPicker{
		directory: audienceOperationMemberDirectoryStub{items: []groupopsport.OperationMember{{StaffID: 2, SenderUserID: "QianLan", DisplayName: "QianLan"}}},
		security:  audienceOperationMemberSecurityStub{},
	}
	recorder := httptest.NewRecorder()
	picker.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/common/operation-members?scope=audience_senders&page_size=100&q=qian", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"user_id":"QianLan"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAudienceOperationMemberPickerRefreshAcceptsAudienceScope(t *testing.T) {
	picker := audienceOperationMemberPicker{
		directory: audienceOperationMemberDirectoryStub{items: []groupopsport.OperationMember{{StaffID: 2, SenderUserID: "QianLan", DisplayName: "QianLan"}}},
		security:  audienceOperationMemberSecurityStub{},
	}
	recorder := httptest.NewRecorder()
	picker.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/admin/common/operation-members/sync?scope=audience_senders&page_size=100", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"user_id":"QianLan"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAudienceOperationMemberDirectoryReadsInsideUnitOfWork(t *testing.T) {
	directory := audienceOperationMemberDirectory{
		uow:       audienceOperationMemberUnitStub{},
		directory: audienceOperationMemberDirectoryStub{items: []groupopsport.OperationMember{{StaffID: 2, SenderUserID: "QianLan", DisplayName: "QianLan"}}},
	}
	items, err := directory.ListEligibleStaff(context.Background())
	if err != nil || len(items) != 1 || items[0].SenderUserID != "QianLan" {
		t.Fatalf("items=%v err=%v", items, err)
	}
}
