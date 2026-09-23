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

func count(value int64) *int64 { return &value }

func TestReleaseReaderRequiresCurrentSHAAndReturnsProtectedGap(t *testing.T) {
	now := time.Now().UTC()
	sha := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name   string
		change func(*platformport.ReleaseMaintenanceReport)
		want   error
	}{
		{name: "stable current release does not expire after two hours", change: func(r *platformport.ReleaseMaintenanceReport) { r.ObservedAt = now.Add(-90 * 24 * time.Hour) }},
		{name: "different release rejected", change: func(r *platformport.ReleaseMaintenanceReport) { r.ReleaseSHA = strings.Repeat("b", 40) }, want: ErrEvidence},
		{name: "future evidence rejected", change: func(r *platformport.ReleaseMaintenanceReport) { r.ObservedAt = now.Add(time.Minute) }, want: ErrEvidence},
		{name: "unknown code rejected", change: func(r *platformport.ReleaseMaintenanceReport) { r.Reason = "RAW_SECRET" }, want: ErrEvidence},
		{name: "gap survives", change: func(r *platformport.ReleaseMaintenanceReport) {
			r.State = "gap"
			r.Reason = "two_verified_schema_compatible_rollbacks_missing"
			r.RollbackCount = count(1)
			r.DeletedCount = count(0)
			r.DeletedBytes = count(0)
		}, want: ErrPartial},
		{name: "gap cannot claim deletion", change: func(r *platformport.ReleaseMaintenanceReport) {
			r.State = "gap"
			r.Reason = "two_verified_schema_compatible_rollbacks_missing"
			r.RollbackCount = count(1)
		}, want: ErrEvidence},
		{name: "unknown cleanup quantity stays null", change: func(r *platformport.ReleaseMaintenanceReport) {
			r.State = "gap"
			r.Reason = "release_cleanup_evidence_or_execution_gap"
			r.DeletedCount = nil
			r.DeletedBytes = nil
			r.RollbackCount = nil
			r.ConfirmedDeletedCount = count(1)
			r.ConfirmedDeletedBytes = count(1024)
		}, want: ErrPartial},
		{name: "invalid quantity rejected", change: func(r *platformport.ReleaseMaintenanceReport) { r.DeletedBytes = count(-1) }, want: ErrEvidence},
		{name: "inconsistent space delta rejected", change: func(r *platformport.ReleaseMaintenanceReport) {
			r.SpaceObservations[0].AvailableBytesNetChange = count(50)
		}, want: ErrEvidence},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := platformport.ReleaseMaintenanceReport{Version: 1, ReleaseSHA: sha, ObservedAt: now, State: "completed", Reason: "verified_cleanup_completed", DeletedCount: count(1), DeletedBytes: count(1024), RollbackCount: count(2), SpaceObservations: []platformport.HostSpaceObservation{{Resource: "release_storage", State: "sampled", AvailableBytesBefore: count(1000), AvailableBytesAfter: count(600), AvailableBytesNetChange: count(-400)}}}
			if tc.change != nil {
				tc.change(&r)
			}
			payload, _ := json.Marshal(r)
			adapter := &Adapter{start: func(context.Context) error { t.Fatal("read must never mutate"); return nil }, readRelease: func() ([]byte, error) { return payload, nil }, currentSHA: func() (string, error) { return sha, nil }, now: func() time.Time { return now }}
			report, err := adapter.ReadReleaseLatest(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if errors.Is(err, ErrPartial) && report.Reason != r.Reason {
				t.Fatal("protected gap evidence lost")
			}
			encoded, _ := json.Marshal(report)
			if strings.Contains(string(encoded), "RAW_SECRET") {
				t.Fatal("unknown diagnostic escaped reader")
			}
		})
	}
}

func TestReleaseReaderRejectsConcurrentReleaseSwitchAndUnknownFields(t *testing.T) {
	now := time.Now().UTC()
	sha := strings.Repeat("a", 40)
	reads := 0
	payload := []byte(`{"version":1,"release_sha":"` + sha + `","observed_at":"` + now.Format(time.RFC3339Nano) + `","state":"gap","reason":"cleanup_not_attempted","deleted_count":null,"deleted_bytes":null}`)
	adapter := &Adapter{readRelease: func() ([]byte, error) { return payload, nil }, currentSHA: func() (string, error) {
		reads++
		if reads == 2 {
			return strings.Repeat("b", 40), nil
		}
		return sha, nil
	}, now: func() time.Time { return now }}
	if _, err := adapter.ReadReleaseLatest(context.Background()); !errors.Is(err, ErrEvidence) {
		t.Fatal("concurrent switch accepted")
	}
	adapter.currentSHA = func() (string, error) { return sha, nil }
	payload = []byte(`{"version":1,"secret":"RAW_SECRET"}`)
	if _, err := adapter.ReadReleaseLatest(context.Background()); !errors.Is(err, ErrEvidence) {
		t.Fatal("unknown fields accepted")
	}
}
