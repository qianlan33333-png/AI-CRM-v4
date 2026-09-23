package externaleffects

import (
	"testing"
	"time"

	"github.com/riverqueue/river"
)

func TestEffectWorkerTimeoutCoversMaterialUploadWithinAttemptLease(t *testing.T) {
	timeout := NewWorker(nil, nil).Timeout(&river.Job[EffectJobArgs]{})
	if timeout < 4*time.Minute || timeout >= 5*time.Minute {
		t.Fatalf("worker timeout=%s must cover the maximum upload budget below the attempt lease", timeout)
	}
}
