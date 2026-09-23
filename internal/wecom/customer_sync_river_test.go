package wecom

import (
	"testing"
	"time"
)

func TestCustomerSyncWorkerTimeoutCoversFullResumableRun(t *testing.T) {
	worker := NewCustomerSyncWorker()
	if got := worker.Timeout(nil); got != 30*time.Minute {
		t.Fatalf("timeout=%s", got)
	}
}
