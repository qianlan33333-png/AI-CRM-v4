package externaleffects

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/riverqueue/river"
)

type responseLostHTTPAdapter struct {
	client     *http.Client
	url        string
	networkErr error
}

func (a *responseLostHTTPAdapter) Execute(ctx context.Context, envelope Envelope, _ Attempt) (AdapterResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, strings.NewReader(string(envelope.PayloadDigest)))
	if err != nil {
		return AdapterResult{}, err
	}
	response, err := a.client.Do(request)
	if err != nil {
		a.networkErr = err
		return AdapterResult{CallAttempted: true}, err
	}
	defer response.Body.Close()
	return AdapterResult{}, errors.New("unexpected provider response")
}

func TestPostgreSQLProviderAcceptedResponseLostHasNoSecondSend(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely[EffectJobArgs](workers, NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	acceptedBody := make(chan string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil || request.Method != http.MethodPost {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		accepted.Add(1)
		acceptedBody <- string(body)
		connection, _, hijackErr := writer.(http.Hijacker).Hijack()
		if hijackErr == nil {
			_ = connection.Close() // The provider accepted the write but sends no HTTP response.
		}
	}))
	defer provider.Close()
	ctx := context.Background()
	envelope := envelopeForTest()
	envelope.PayloadDigest = digestForTest("provider-accepted-response-lost")
	projection, _, err := repository.AcceptAndQueue(ctx, AcceptCommand{
		ReceiptKey: digestForTest("provider-accepted-response-lost-key"), Envelope: envelope,
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := parseEffectID(projection.ID)
	if err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err = pool.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1 AND generation=1`, id).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	adapter := &responseLostHTTPAdapter{client: &http.Client{Timeout: 5 * time.Second}, url: provider.URL}
	if err = repository.RunAttempt(ctx, id, 1, jobID, adapter); err != nil {
		t.Fatal(err)
	}
	if adapter.networkErr == nil {
		t.Fatal("client did not observe a lost provider response")
	}
	select {
	case got := <-acceptedBody:
		if got != string(envelope.PayloadDigest) {
			t.Fatalf("provider accepted payload=%q want=%q", got, envelope.PayloadDigest)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not record the accepted request")
	}
	current, err := repository.Get(ctx, projection.ID)
	if err != nil || current.State != StateUnknown || current.AttemptCount != 1 {
		t.Fatalf("response-lost state=%+v err=%v", current, err)
	}
	var attempted bool
	var attemptState string
	if err = pool.QueryRow(ctx, `SELECT call_attempted,state FROM external_effect_attempts WHERE effect_id=$1 AND number=1 AND generation=1`, id).Scan(&attempted, &attemptState); err != nil || !attempted || attemptState != string(StateUnknown) {
		t.Fatalf("attempt fact attempted=%t state=%s err=%v", attempted, attemptState, err)
	}
	if err = repository.RunAttempt(ctx, id, 1, jobID, adapter); err != nil {
		t.Fatalf("replayed River job=%v", err)
	}
	if _, _, err = repository.Retry(ctx, ControlCommand{EffectID: projection.ID, ReceiptKey: digestForTest("provider-accepted-second-send"), ActorAdminUserID: 7}); !errors.Is(err, ErrTransition) {
		t.Fatalf("unknown effect was made retryable: %v", err)
	}
	if got := accepted.Load(); got != 1 {
		t.Fatalf("provider accepted writes=%d want=1", got)
	}
}
