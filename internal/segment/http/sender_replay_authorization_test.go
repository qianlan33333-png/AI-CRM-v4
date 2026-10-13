package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

type senderReceiptCapture struct {
	ExecutionApplication
	calls int
	actor int64
}

func (s *senderReceiptCapture) ReplaceSenders(_ context.Context, c segmentapp.SendersCommand) (segmentdomain.SenderSet, error) {
	s.calls++
	s.actor = c.Actor
	return segmentdomain.SenderSet{ID: 41, PackageID: c.PackageID, Version: 1}, nil
}

func TestSenderReceiptReplayStillRequiresCurrentAuthenticationRBACAndCSRF(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	viewer := admin
	viewer.Roles = []accessdomain.Role{accessdomain.RoleViewer}
	capture := &senderReceiptCapture{}
	h, err := NewHandler(fakeApplication{}, fakeSecurity{principal: admin})
	if err != nil {
		t.Fatal(err)
	}
	h.execution = capture
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/ai-audience/packages/1/senders", strings.NewReader(`{"expected_version":1,"provider_member_references":["synthetic-sender"]}`))
		req.Header.Set("Idempotency-Key", "same-accepted-sender-command")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		return response
	}
	if response := request(); response.Code != 200 || capture.calls != 1 || capture.actor != 7 {
		t.Fatalf("accepted command did not use authenticated actor: status=%d calls=%d actor=%d", response.Code, capture.calls, capture.actor)
	}
	for _, scenario := range []struct {
		name     string
		security fakeSecurity
		status   int
	}{{"revoked_session", fakeSecurity{authErr: errors.New("revoked")}, 401}, {"role_removed", fakeSecurity{principal: viewer}, 403}, {"csrf_missing", fakeSecurity{principal: admin, csrfErr: errors.New("csrf")}, 403}} {
		t.Run(scenario.name, func(t *testing.T) {
			h.security = scenario.security
			response := request()
			if response.Code != scenario.status || capture.calls != 1 || strings.Contains(response.Body.String(), `"sender_set"`) {
				t.Fatalf("known key bypassed current authorization: status=%d receipt_calls=%d", response.Code, capture.calls)
			}
		})
	}
}
