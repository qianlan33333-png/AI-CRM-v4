package store

import (
	"testing"
	"time"

	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
)

func TestValidMemberEventDispatchDiagnosticAcceptsPaidTimeSkips(t *testing.T) {
	base := automationapp.MemberEventDispatchDiagnostic{
		PackageID: 27, SnapshotID: 91, ConfigurationVersionID: 43,
		PolicyID: 9, PolicyVersionID: 52, EventDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		State: "skipped", OccurredAt: time.Date(2026, 9, 30, 11, 0, 0, 0, time.UTC),
		RecordedAt: time.Date(2026, 9, 30, 11, 1, 0, 0, time.UTC),
	}
	for _, reason := range []string{
		automationapp.MemberEventDeferredHistoricalMergeReason,
		automationapp.MemberEventDeferredPaidCutoffReason,
		automationapp.MemberEventDeferredPaidMissingReason,
	} {
		t.Run(reason, func(t *testing.T) {
			item := base
			item.Reason = reason
			if !validMemberEventDispatchDiagnostic(item, item.PackageID) {
				t.Fatalf("diagnostic reason %q should be queryable", reason)
			}
		})
	}
	invalid := base
	invalid.Reason = "unknown_skip_reason"
	if validMemberEventDispatchDiagnostic(invalid, invalid.PackageID) {
		t.Fatal("unknown diagnostic skip reason must remain rejected")
	}
}
