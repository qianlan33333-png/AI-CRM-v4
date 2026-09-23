package hostmaintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

const releaseResultFile = resultDirectory + "/release-cleanup.json"

var releaseSHAFormat = regexp.MustCompile(`^[0-9a-f]{40}$`)

func readCurrentSHA() (string, error) {
	// Every mutable component must be controlled by the root installer.
	for _, path := range []string{"/opt/aicrm", "/opt/aicrm/releases", "/opt/aicrm/current"} {
		info, err := os.Lstat(path)
		if err != nil {
			return "", ErrEvidence
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != 0 {
			return "", ErrEvidence
		}
		if path != "/opt/aicrm/current" && (!info.IsDir() || info.Mode().Perm()&0022 != 0) {
			return "", ErrEvidence
		}
		if path == "/opt/aicrm/current" && info.Mode()&os.ModeSymlink == 0 {
			return "", ErrEvidence
		}
	}
	target, err := os.Readlink("/opt/aicrm/current")
	if err != nil {
		return "", ErrEvidence
	}
	sha := filepath.Base(target)
	if !releaseSHAFormat.MatchString(sha) || target != "/opt/aicrm/releases/"+sha {
		return "", ErrEvidence
	}
	return sha, nil
}

func (adapter *Adapter) ReadReleaseLatest(ctx context.Context) (platformport.ReleaseMaintenanceReport, error) {
	if ctx == nil || ctx.Err() != nil || adapter.readRelease == nil || adapter.currentSHA == nil {
		return platformport.ReleaseMaintenanceReport{}, ErrUnavailable
	}
	payload, err := adapter.readRelease()
	if err != nil {
		return platformport.ReleaseMaintenanceReport{}, ErrEvidence
	}
	sha, err := adapter.currentSHA()
	if err != nil {
		return platformport.ReleaseMaintenanceReport{}, ErrEvidence
	}
	report, err := decodeReleaseReport(payload, sha, adapter.now().UTC())
	// Refuse a release switch concurrent with reading the evidence.
	afterSHA, afterErr := adapter.currentSHA()
	if afterErr != nil || afterSHA != sha {
		return platformport.ReleaseMaintenanceReport{}, ErrEvidence
	}
	return report, err
}

func decodeReleaseReport(payload []byte, currentSHA string, now time.Time) (platformport.ReleaseMaintenanceReport, error) {
	var report platformport.ReleaseMaintenanceReport
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return platformport.ReleaseMaintenanceReport{}, ErrEvidence
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || report.Version != 1 || !releaseSHAFormat.MatchString(report.ReleaseSHA) || report.ReleaseSHA != currentSHA || report.ObservedAt.IsZero() || report.ObservedAt.After(now.Add(time.Second)) {
		return platformport.ReleaseMaintenanceReport{}, ErrEvidence
	}
	for _, count := range []*int64{report.DeletedCount, report.DeletedBytes, report.ConfirmedDeletedCount, report.ConfirmedDeletedBytes, report.RollbackCount} {
		if count != nil && *count < 0 {
			return platformport.ReleaseMaintenanceReport{}, ErrEvidence
		}
	}
	if (report.DeletedCount == nil) != (report.DeletedBytes == nil) || (report.ConfirmedDeletedCount == nil) != (report.ConfirmedDeletedBytes == nil) || !validSpaceObservations(report.SpaceObservations, map[string]bool{"release_storage": true}) {
		return platformport.ReleaseMaintenanceReport{}, ErrEvidence
	}
	if report.State == "completed" {
		if report.Reason != "verified_cleanup_completed" || report.DeletedCount == nil || report.RollbackCount == nil || *report.RollbackCount < 2 || report.ConfirmedDeletedCount != nil {
			return platformport.ReleaseMaintenanceReport{}, ErrEvidence
		}
		return report, nil
	}
	if report.State != "gap" {
		return platformport.ReleaseMaintenanceReport{}, ErrEvidence
	}
	switch report.Reason {
	case "two_verified_schema_compatible_rollbacks_missing":
		if report.DeletedCount == nil || *report.DeletedCount != 0 || *report.DeletedBytes != 0 || report.RollbackCount == nil || *report.RollbackCount >= 2 || report.ConfirmedDeletedCount != nil {
			return platformport.ReleaseMaintenanceReport{}, ErrEvidence
		}
	case "release_cleanup_evidence_or_execution_gap", "release_cleanup_result_persistence_failed", "cleanup_not_attempted":
	default:
		return platformport.ReleaseMaintenanceReport{}, ErrEvidence
	}
	return report, ErrPartial
}

func validSpaceObservations(observations []platformport.HostSpaceObservation, allowed map[string]bool) bool {
	seen := map[string]bool{}
	for _, observation := range observations {
		if !allowed[observation.Resource] || seen[observation.Resource] {
			return false
		}
		seen[observation.Resource] = true
		before, after, change := observation.AvailableBytesBefore, observation.AvailableBytesAfter, observation.AvailableBytesNetChange
		if (before != nil && *before < 0) || (after != nil && *after < 0) {
			return false
		}
		switch observation.State {
		case "sampled":
			if before == nil || after == nil || change == nil || *change != *after-*before {
				return false
			}
		case "unavailable":
			if change != nil {
				return false
			}
		default:
			return false
		}
	}
	return true
}
