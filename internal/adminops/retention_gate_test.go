package adminops

import (
	"context"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/riverqueue/river"
	"testing"
)

type forbiddenHostMaintenance struct{ t *testing.T }

func (f forbiddenHostMaintenance) Run(context.Context) (platformport.HostMaintenanceReport, error) {
	f.t.Fatal("disabled maintenance reached privileged host")
	return platformport.HostMaintenanceReport{}, nil
}
func TestRetentionPersistedJobHonorsDisabledExecutionGate(t *testing.T) {
	w := NewRetentionWorker()
	if err := w.BindService(&RetentionService{enabled: false}); err != nil {
		t.Fatal(err)
	}
	if err := w.BindHostMaintenance(forbiddenHostMaintenance{t}); err != nil {
		t.Fatal(err)
	}
	if err := w.Work(context.Background(), &river.Job[RetentionJobArgs]{}); err != nil {
		t.Fatal(err)
	}
}
