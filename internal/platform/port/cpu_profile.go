package port

import (
	"context"
	"errors"
)

const CPUProfileMaxBytes = 2 * 1024 * 1024
const CPUProfileCapacityBytes = 64 * 1024 * 1024

var (
	ErrCPUProfileUnavailable = errors.New("CPU profile unavailable")
	ErrCPUProfileBusy        = errors.New("CPU profile already running")
	ErrCPUProfileTooLarge    = errors.New("CPU profile exceeds limit")
	ErrCPUProfileCapacity    = errors.New("CPU profile directory capacity reached")
	ErrCPUProfileUncertain   = errors.New("CPU profile artifact cleanup unconfirmed")
)

type CPUProfileArtifact struct {
	SHA256 string
	Bytes  int64
}

// CPUProfiler captures only its own API process for exactly five seconds.
// IDs are server-generated opaque references, never paths. Implementations must
// not persist raw profiles or leave a published artifact after a known failure.
type CPUProfiler interface {
	Capture(context.Context, string) (CPUProfileArtifact, error)
	Read(context.Context, string) ([]byte, error)
}
