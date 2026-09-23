// Package hostmaintenance is a fixed systemd adapter, not a scheduler.
package hostmaintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var ErrUnavailable = errors.New("host maintenance unavailable")
var ErrEvidence = errors.New("host maintenance evidence missing, stale or invalid")
var ErrPartial = platformport.ErrHostMaintenancePartial

const unit = "aicrm-runtime-retention.service"
const resultDirectory = "/var/lib/aicrm-maintenance"
const resultFile = resultDirectory + "/runtime-retention.json"

type Adapter struct {
	start       func(context.Context) error
	read        func() ([]byte, error)
	readRelease func() ([]byte, error)
	currentSHA  func() (string, error)
	now         func() time.Time
}

var _ platformport.HostMaintenance = (*Adapter)(nil)
var _ platformport.HostMaintenanceReader = (*Adapter)(nil)
var _ platformport.ReleaseMaintenanceReader = (*Adapter)(nil)

func New() *Adapter {
	return &Adapter{start: func(ctx context.Context) error {
		if runtime.GOOS != "linux" {
			return ErrUnavailable
		}
		command := exec.CommandContext(ctx, "/usr/bin/systemctl", "--no-ask-password", "start", unit)
		command.Stdout = io.Discard
		command.Stderr = io.Discard
		return command.Run()
	}, read: readResult, readRelease: func() ([]byte, error) { return readEvidence(releaseResultFile) }, currentSHA: readCurrentSHA, now: time.Now}
}

func readResult() ([]byte, error) {
	return readEvidence(resultFile)
}

func readEvidence(path string) ([]byte, error) {
	info, err := os.Lstat(resultDirectory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return nil, ErrEvidence
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != 0 {
		return nil, ErrEvidence
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrEvidence
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 65536 {
		return nil, ErrEvidence
	}
	owner, ok = info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != 0 {
		return nil, ErrEvidence
	}
	return io.ReadAll(io.LimitReader(file, 65537))
}

func (adapter *Adapter) Run(ctx context.Context) (platformport.HostMaintenanceReport, error) {
	var report platformport.HostMaintenanceReport
	if ctx == nil {
		return report, ErrUnavailable
	}
	started := adapter.now().UTC()
	bounded, cancel := context.WithTimeout(ctx, 95*time.Second)
	defer cancel()
	startErr := adapter.start(bounded)
	payload, err := adapter.read()
	if err != nil {
		return report, ErrEvidence
	}
	report, err = decodeReport(payload, started.Add(-time.Second), adapter.now().UTC().Add(time.Second))
	if err != nil {
		return report, err
	}
	if startErr != nil {
		return report, ErrUnavailable
	}
	return report, nil
}

func (adapter *Adapter) ReadLatest(ctx context.Context) (platformport.HostMaintenanceReport, error) {
	if ctx == nil || ctx.Err() != nil {
		return platformport.HostMaintenanceReport{}, ErrUnavailable
	}
	payload, err := adapter.read()
	if err != nil {
		return platformport.HostMaintenanceReport{}, ErrEvidence
	}
	now := adapter.now().UTC()
	return decodeReport(payload, now.Add(-2*time.Hour), now.Add(time.Second))
}

func decodeReport(payload []byte, earliest, latest time.Time) (platformport.HostMaintenanceReport, error) {
	var report platformport.HostMaintenanceReport
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return platformport.HostMaintenanceReport{}, ErrEvidence
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return platformport.HostMaintenanceReport{}, ErrEvidence
	}
	if report.Version != 1 || report.StartedAt.Before(earliest) || report.StartedAt.After(latest) || report.FinishedAt == nil || report.FinishedAt.Before(report.StartedAt) || report.FinishedAt.After(latest) {
		return platformport.HostMaintenanceReport{}, ErrEvidence
	}
	if !validSpaceObservations(report.SpaceObservations, map[string]bool{"process_storage": true, "journal_persistent_storage": true, "journal_runtime_storage": true}) {
		return platformport.HostMaintenanceReport{}, ErrEvidence
	}
	for _, code := range report.FailureCodes {
		if code != "runtime_cleanup_failed" && code != "runtime_roots_uninitialized" && code != "journal_cleanup_failed" {
			return platformport.HostMaintenanceReport{}, ErrEvidence
		}
	}
	if len(report.FailureCodes) > 3 || (report.Journal != "completed" && report.Journal != "failed") {
		return platformport.HostMaintenanceReport{}, ErrEvidence
	}
	if report.Runtime != nil {
		r := report.Runtime
		if r.Candidates < 0 || r.Candidates > 1000 || r.Deleted < 0 || r.Deleted > r.Candidates || r.Bytes < 0 || r.Protected < 0 || r.UninitializedRoots < 0 {
			return platformport.HostMaintenanceReport{}, ErrEvidence
		}
	}
	if report.State == "partial_failed" {
		return report, ErrPartial
	}
	if report.State != "completed" || report.Runtime == nil || report.Runtime.UninitializedRoots != 0 || report.Journal != "completed" || len(report.FailureCodes) != 0 {
		return platformport.HostMaintenanceReport{}, ErrEvidence
	}
	return report, nil
}
