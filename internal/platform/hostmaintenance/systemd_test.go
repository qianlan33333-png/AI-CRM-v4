package hostmaintenance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

func TestRunRequiresFreshBoundedSanitizedCompletion(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name   string
		change func(*platformport.HostMaintenanceReport)
		want   error
	}{
		{name: "completed"},
		{name: "stale", change: func(r *platformport.HostMaintenanceReport) { r.StartedAt = now.Add(-time.Hour) }, want: ErrEvidence},
		{name: "unfinished", change: func(r *platformport.HostMaintenanceReport) { r.State = "running"; r.FinishedAt = nil }, want: ErrEvidence},
		{name: "unbounded", change: func(r *platformport.HostMaintenanceReport) { r.Runtime.Deleted = 1001 }, want: ErrEvidence},
		{name: "partial failure counts retained", change: func(r *platformport.HostMaintenanceReport) {
			r.State = "partial_failed"
			r.Journal = "failed"
			r.FailureCodes = []string{"journal_cleanup_failed"}
		}, want: ErrPartial},
		{name: "unsafe diagnostic rejected", change: func(r *platformport.HostMaintenanceReport) {
			r.State = "partial_failed"
			r.FailureCodes = []string{"RAW_PROVIDER_SECRET"}
		}, want: ErrEvidence},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := platformport.HostMaintenanceReport{Version: 1, StartedAt: now, FinishedAt: &now, State: "completed", Runtime: &platformport.HostRuntimeRetention{Candidates: 3, Deleted: 3, Bytes: 42}, Journal: "completed", FailureCodes: []string{}}
			if tc.change != nil {
				tc.change(&r)
			}
			payload, _ := json.Marshal(r)
			calls := 0
			adapter := &Adapter{start: func(ctx context.Context) error {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("must bound service wait")
				}
				return nil
			}, read: func() ([]byte, error) { return payload, nil }, now: func() time.Time { return now }}
			report, err := adapter.Run(context.Background())
			if !errors.Is(err, tc.want) || calls != 1 {
				t.Fatalf("calls=%d error=%v want=%v", calls, err, tc.want)
			}
			if errors.Is(err, ErrPartial) && report.Runtime.Deleted != 3 {
				t.Fatal("confirmed partial counts lost")
			}
			serialized, _ := json.Marshal(report)
			if strings.Contains(string(serialized), "RAW_PROVIDER_SECRET") {
				t.Fatal("unsafe code escaped adapter")
			}
		})
	}
}

func TestReadLatestDoesNotTriggerMaintenanceAndRejectsOldOrExtraEvidence(t *testing.T) {
	now := time.Now().UTC()
	r := platformport.HostMaintenanceReport{Version: 1, StartedAt: now.Add(-time.Hour), FinishedAt: &now, State: "completed", Runtime: &platformport.HostRuntimeRetention{}, Journal: "completed", FailureCodes: []string{}}
	payload, _ := json.Marshal(r)
	adapter := &Adapter{start: func(context.Context) error { t.Fatal("read must never start maintenance"); return nil }, read: func() ([]byte, error) { return payload, nil }, now: func() time.Time { return now }}
	if _, err := adapter.ReadLatest(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.StartedAt = now.Add(-3 * time.Hour)
	payload, _ = json.Marshal(r)
	if _, err := adapter.ReadLatest(context.Background()); !errors.Is(err, ErrEvidence) {
		t.Fatal("old evidence accepted")
	}
	payload = []byte(`{"version":1,"secret":"do-not-return"}`)
	if _, err := adapter.ReadLatest(context.Background()); !errors.Is(err, ErrEvidence) {
		t.Fatal("unknown fields accepted")
	}
}
