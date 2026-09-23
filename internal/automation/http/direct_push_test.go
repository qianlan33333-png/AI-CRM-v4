package http

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

type directPushProtocolStub struct{ err error }

func (s directPushProtocolStub) AuthenticateAudienceWebhook(context.Context, *http.Request, string, []byte) (AudienceProtocolClaim, error) {
	return AudienceProtocolClaim{EventIDDigest: sha256.Sum256([]byte("event")), PayloadDigest: sha256.Sum256([]byte("payload"))}, s.err
}

type directPushServiceStub struct{ seen int }

func (s *directPushServiceStub) AcceptDirectPush(_ context.Context, command automationport.DirectPushCommand) (automationport.DirectPushBatchResult, error) {
	s.seen = len(command.Items)
	return automationport.DirectPushBatchResult{BatchID: "apb_1", AcceptedCount: len(command.Items), Items: []automationport.DirectPushItemResult{}}, nil
}
func (*directPushServiceStub) DirectPushStatuses(context.Context, string, []string) ([]automationport.DirectPushStatus, error) {
	return []automationport.DirectPushStatus{}, nil
}

func TestDirectPushHandlerAcceptsOneThousandItemsAndRejectsOneThousandOne(t *testing.T) {
	service := &directPushServiceStub{}
	handler, err := NewDirectPushHandler(service, directPushProtocolStub{})
	if err != nil {
		t.Fatal(err)
	}
	for count, want := range map[int]int{1000: http.StatusAccepted, 1001: http.StatusBadRequest} {
		items := make([]automationport.DirectPushInput, count)
		for index := range items {
			items[index] = automationport.DirectPushInput{UnionID: "union", Text: "message", MiniProgramID: 1, SenderUserID: "sender"}
		}
		body, _ := json.Marshal(map[string]any{"items": items})
		request := httptest.NewRequest(http.MethodPost, "/api/automation/audience/webhooks/awh_fixture", strings.NewReader(string(body)))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("count %d returned %d: %s", count, response.Code, response.Body.String())
		}
	}
	if service.seen != 1000 {
		t.Fatalf("accepted item count = %d", service.seen)
	}
}

func TestDirectPushHandlerDoesNotEchoSensitiveInput(t *testing.T) {
	service := &directPushServiceStub{}
	handler, _ := NewDirectPushHandler(service, directPushProtocolStub{})
	request := httptest.NewRequest(http.MethodPost, "/api/automation/audience/webhooks/awh_fixture", strings.NewReader(`{"items":[{"unionid":"secret-union","text":"secret-message","miniprogram_id":1,"sender_userid":"sender"}]}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || strings.Contains(response.Body.String(), "secret-union") || strings.Contains(response.Body.String(), "secret-message") {
		t.Fatalf("sensitive response: %d %s", response.Code, response.Body.String())
	}
}

var _ AudienceProtocolAuthenticator = directPushProtocolStub{}
var _ automationport.DirectPushService = (*directPushServiceStub)(nil)
