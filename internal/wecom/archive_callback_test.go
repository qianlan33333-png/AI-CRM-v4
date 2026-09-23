package wecom

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
)

type archiveDeliveryStub struct {
	calls int
	err   error
	errs  []error
}

func (s *archiveDeliveryStub) ProcessArchiveDelivery(context.Context, archiveport.InboxDelivery) error {
	s.calls++
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return err
	}
	return s.err
}
func TestArchiveNotificationPersistsBeforeACKAndDoesNotRelaxContactParser(t *testing.T) {
	crypto, _ := NewCallbackCrypto("callback-token", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "wx-corp")
	crypto.now = func() time.Time { return fixedNow }
	store := &memoryWebhookStore{}
	inbox, _ := webhook.NewService(store)
	handler := CallbackHandler{Enabled: true, Crypto: crypto, Inbox: inbox, UOW: directUOW{}, Dispatcher: CallbackEventDispatcher{Archive: ArchiveCallbackDispatcher{Inbox: inbox, UOW: directUOW{}}}}
	plain := []byte("<xml><ToUserName>wx-corp</ToUserName><FromUserName>sys</FromUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><AgentID>1000002</AgentID><Event>msgaudit_notify</Event></xml>")
	encrypted := encryptForTest(t, crypto, plain)
	timestamp := "1788336000"
	query := "?msg_signature=" + callbackSignature("callback-token", timestamp, "n", encrypted) + "&timestamp=" + timestamp + "&nonce=n"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback"+query, strings.NewReader("<xml><ToUserName>wx-corp</ToUserName><Encrypt><![CDATA["+encrypted+"]]></Encrypt></xml>")))
	if response.Code != http.StatusOK || len(store.deliveries) != 1 || store.deliveries[0].Provider != archiveCallbackProvider {
		t.Fatalf("status=%d deliveries=%+v", response.Code, store.deliveries)
	}
	// The archive shape lacks ExternalUserID/UserID; the distinct dispatcher is
	// what permits it, leaving the external-contact parser's requirements intact.
	if _, err := parseCallbackEvent(plain, "wx-corp"); !errors.Is(err, ErrMalformedXML) {
		t.Fatalf("contact parser accepted archive event: %v", err)
	}
}
func TestArchiveInboxBudgetIsRetryableAndNeverCompletes(t *testing.T) {
	store := &memoryWebhookStore{}
	inbox, _ := webhook.NewService(store)
	key, _ := idempotency.Parse("wecom:archive-test:0001")
	_, err := inbox.Ingest(context.Background(), webhook.Ingest{Provider: archiveCallbackProvider, IdempotencyKey: key, Payload: []byte(`{"corp_id":"wx-corp","event":"msgaudit_notify"}`)})
	if err != nil {
		t.Fatal(err)
	}
	sink := &archiveDeliveryStub{err: archiveport.ErrWorkBudgetExceeded}
	processor := ArchiveInboxProcessor{Enabled: true, Inbox: inbox, UOW: directUOW{}, Archive: sink}
	count, err := processor.ProcessOnce(context.Background(), "worker", 1)
	if err != nil || count != 1 || sink.calls != 1 {
		t.Fatalf("count=%d calls=%d err=%v", count, sink.calls, err)
	}
	if got := store.deliveries[0]; got.Status != webhook.StatusRetryable {
		t.Fatalf("delivery=%+v", got)
	}
}

func TestArchiveBudgetUsesInboxContinuationPastGenericAttemptLimit(t *testing.T) {
	store := &memoryWebhookStore{}
	inbox, _ := webhook.NewService(store)
	key, _ := idempotency.Parse("wecom:archive-continue:0001")
	_, err := inbox.Ingest(context.Background(), webhook.Ingest{Provider: archiveCallbackProvider, IdempotencyKey: key, MaxAttempts: 1, Payload: []byte(`{"corp_id":"wx-corp","event":"msgaudit_notify"}`)})
	if err != nil {
		t.Fatal(err)
	}
	sink := &archiveDeliveryStub{errs: []error{archiveport.ErrWorkBudgetExceeded, archiveport.ErrWorkBudgetExceeded, nil}}
	processor := ArchiveInboxProcessor{Enabled: true, Inbox: inbox, UOW: directUOW{}, Archive: sink, Now: func() time.Time { return fixedNow }}
	for i := 0; i < 3; i++ {
		if count, processErr := processor.ProcessOnce(context.Background(), "worker", 1); processErr != nil || count != 1 {
			t.Fatalf("round=%d count=%d err=%v delivery=%+v", i, count, processErr, store.deliveries)
		}
	}
	got := store.deliveries[0]
	if sink.calls != 3 || got.Status != webhook.StatusProcessed || got.AttemptCount != 3 || got.MaxAttempts < 3 {
		t.Fatalf("calls=%d delivery=%+v", sink.calls, got)
	}
}
