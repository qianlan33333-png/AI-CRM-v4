package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
)

type channelOperationMemberDirectoryStub struct {
	items []channelstore.AcquisitionCandidate
	err   error
}

func (stub channelOperationMemberDirectoryStub) LocalCandidates(context.Context) ([]channelstore.AcquisitionCandidate, error) {
	return stub.items, stub.err
}

type channelOperationMemberSecurityStub struct {
	principal accessdomain.Principal
	err       error
}

func (stub channelOperationMemberSecurityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return stub.principal, stub.err
}

func TestChannelOperationMemberPickerReadsOnlyLocalStaffDirectory(t *testing.T) {
	picker := channelOperationMemberPicker{
		directory: channelOperationMemberDirectoryStub{items: []channelstore.AcquisitionCandidate{
			{ID: 9, WeComUserID: "zoe", DisplayName: "周客服"},
			{ID: 7, WeComUserID: "alice", DisplayName: "Alice"},
		}},
		security: channelOperationMemberSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 3, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}},
	}
	response := httptest.NewRecorder()
	picker.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/common/operation-members?scope=channel_code&page_size=1&q=alice", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	for _, want := range []string{`"scope":"channel_code"`, `"staff_id":7`, `"user_id":"alice"`, `"display_name":"Alice"`, `"active":true`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("missing %s in %s", want, response.Body.String())
		}
	}
	if strings.Contains(response.Body.String(), "zoe") {
		t.Fatalf("query/page filter leaked another staff: %s", response.Body.String())
	}
}

func TestChannelOperationMemberPickerReportsIndependentDirectoryFailure(t *testing.T) {
	picker := channelOperationMemberPicker{
		directory: channelOperationMemberDirectoryStub{err: errors.New("local access directory unavailable")},
		security:  channelOperationMemberSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 3, Roles: []accessdomain.Role{accessdomain.RoleViewer}}},
	}
	response := httptest.NewRecorder()
	picker.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/common/operation-members?scope=channel_code", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"staff_directory_unavailable"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestChannelOperationMemberPickerRejectsOtherScopesBeforeReading(t *testing.T) {
	picker := channelOperationMemberPicker{directory: channelOperationMemberDirectoryStub{}, security: channelOperationMemberSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 3, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}}
	response := httptest.NewRecorder()
	picker.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/common/operation-members?scope=group_ops", nil))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"invalid_request"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
