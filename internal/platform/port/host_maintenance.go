package port

import (
	"context"
	"errors"
	"time"
)

var ErrHostMaintenancePartial = errors.New("host maintenance partially completed")

type HostRuntimeRetention struct {
	Candidates         int64 `json:"candidates"`
	Deleted            int64 `json:"deleted"`
	Bytes              int64 `json:"bytes"`
	Protected          int64 `json:"protected"`
	UninitializedRoots int64 `json:"uninitialized_roots"`
	Remaining          bool  `json:"remaining"`
}

// HostSpaceObservation measures filesystem free space before/after maintenance.
// Net change can be negative and includes concurrent activity; it is neither
// attributed reclaimed bytes nor additive across resources on the same device.
type HostSpaceObservation struct {
	Resource                string `json:"resource"`
	State                   string `json:"state"`
	AvailableBytesBefore    *int64 `json:"available_bytes_before"`
	AvailableBytesAfter     *int64 `json:"available_bytes_after"`
	AvailableBytesNetChange *int64 `json:"available_bytes_net_change"`
}

type HostMaintenanceReport struct {
	Version           int                    `json:"version"`
	StartedAt         time.Time              `json:"started_at"`
	FinishedAt        *time.Time             `json:"finished_at"`
	State             string                 `json:"state"`
	Runtime           *HostRuntimeRetention  `json:"runtime"`
	Journal           string                 `json:"journal"`
	FailureCodes      []string               `json:"failure_codes"`
	SpaceObservations []HostSpaceObservation `json:"space_observations"`
}

// HostMaintenance invokes one fixed root-owned systemd unit. It accepts no
// executable, unit, path, command arguments, or privileged environment values.
type HostMaintenance interface {
	Run(context.Context) (HostMaintenanceReport, error)
}

type HostMaintenanceReader interface {
	ReadLatest(context.Context) (HostMaintenanceReport, error)
}

type ReleaseMaintenanceReport struct {
	Version               int                    `json:"version"`
	ReleaseSHA            string                 `json:"release_sha"`
	ObservedAt            time.Time              `json:"observed_at"`
	State                 string                 `json:"state"`
	Reason                string                 `json:"reason"`
	DeletedCount          *int64                 `json:"deleted_count"`
	DeletedBytes          *int64                 `json:"deleted_bytes"` // Logical file sizes, not physical space reclaimed.
	ConfirmedDeletedCount *int64                 `json:"confirmed_deleted_count,omitempty"`
	ConfirmedDeletedBytes *int64                 `json:"confirmed_deleted_bytes,omitempty"`
	RollbackCount         *int64                 `json:"rollback_count,omitempty"`
	SpaceObservations     []HostSpaceObservation `json:"space_observations"`
}

// ReleaseMaintenanceReader observes the last release hook for the current SHA.
// It never triggers cleanup or reads database credentials.
type ReleaseMaintenanceReader interface {
	ReadReleaseLatest(context.Context) (ReleaseMaintenanceReport, error)
}
