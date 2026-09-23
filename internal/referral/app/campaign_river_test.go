package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type closeRecorder struct {
	id  int64
	err error
}

func (r *closeRecorder) RunCampaignClose(_ context.Context, id int64) error { r.id = id; return r.err }
func TestCampaignCloseWorkerPropagatesRetryAndUsesPersistedReference(t *testing.T) {
	worker := NewCampaignCloseWorker()
	service := &closeRecorder{err: errors.New("snapshot write failed")}
	if err := worker.BindService(service); err != nil {
		t.Fatal(err)
	}
	job := &river.Job[CampaignCloseJobArgs]{JobRow: &rivertype.JobRow{ID: 1}, Args: CampaignCloseJobArgs{CampaignID: 7, ExpectedEnd: time.Now()}}
	if err := worker.Work(context.Background(), job); !errors.Is(err, service.err) {
		t.Fatalf("lost retryable failure: %v", err)
	}
	if service.id != 7 {
		t.Fatal("did not reload campaign reference")
	}
	if err := worker.BindService(&closeRecorder{}); err == nil {
		t.Fatal("rebound running worker")
	}
	if err := worker.Work(context.Background(), nil); err == nil {
		t.Fatal("accepted missing job")
	}
}
