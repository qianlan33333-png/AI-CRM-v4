package diagnostics

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/pprof/profile"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"golang.org/x/sys/unix"
)

const CPUProfileDirectory = "/var/lib/aicrm/process-diagnostics"
const cpuProfileDuration = 5 * time.Second
const profileUncompressedLimit = 8 * 1024 * 1024
const profileDirectoryMarker = ".aicrm-disposable-v1"
const profileDirectoryMarkerContent = "aicrm-process-artifacts-v1\n"

// Process-local protection supplements the AdminOps database session lock.
var cpuProfileActive atomic.Bool

type CPUProfiler struct{ directory string }

var _ platformport.CPUProfiler = (*CPUProfiler)(nil)

// NewCPUProfiler has no caller-selectable filesystem location.
func NewCPUProfiler() *CPUProfiler { return &CPUProfiler{directory: CPUProfileDirectory} }

type cappedProfileBuffer struct {
	bytes.Buffer
	overflow bool
}

func (b *cappedProfileBuffer) Write(p []byte) (int, error) {
	if b.overflow || len(p) > platformport.CPUProfileMaxBytes-b.Len() {
		b.overflow = true
		// Runtime profiling writes asynchronously. Drain safely until Stop rather
		// than blocking that goroutine or growing the buffer after the ceiling.
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

func (p *CPUProfiler) Capture(ctx context.Context, id string) (out platformport.CPUProfileArtifact, err error) {
	if ctx == nil || ctx.Err() != nil || !hexToken(id, 32) {
		return out, platformport.ErrCPUProfileUnavailable
	}
	if !cpuProfileActive.CompareAndSwap(false, true) {
		return out, platformport.ErrCPUProfileBusy
	}
	defer cpuProfileActive.Store(false)
	dir, err := p.openDirectory()
	if err != nil {
		return out, platformport.ErrCPUProfileUnavailable
	}
	defer dir.Close()
	// Database reservations cannot attest that expired artifacts were physically
	// removed. Account for actual registered-root bytes as well, so a failed or
	// disabled host cleaner cannot cause unbounded growth after TTL releases.
	if err = checkProfileDirectoryCapacity(dir); err != nil {
		return out, err
	}
	var raw cappedProfileBuffer
	if err = pprof.StartCPUProfile(&raw); err != nil {
		return out, platformport.ErrCPUProfileBusy
	}
	// Stop only after our own successful Start. Always drain on cancellation.
	func() {
		defer pprof.StopCPUProfile()
		timer := time.NewTimer(cpuProfileDuration)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}()
	if ctx.Err() != nil {
		return out, platformport.ErrCPUProfileUnavailable
	}
	if raw.overflow {
		return out, platformport.ErrCPUProfileTooLarge
	}
	clean, err := scrubCPUProfile(raw.Bytes())
	if err != nil {
		return out, err
	}
	if ctx.Err() != nil {
		return out, platformport.ErrCPUProfileUnavailable
	}
	// The registered directory is pinned by FD; no pathname traversal after
	// verification. O_EXCL refuses existing symlinks and hardlinks alike.
	name := id + ".cpu.pprof"
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return out, platformport.ErrCPUProfileUnavailable
	}
	file := os.NewFile(uintptr(fd), "cpu-profile")
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			if cleanupErr := unix.Unlinkat(int(dir.Fd()), name, 0); cleanupErr != nil && cleanupErr != unix.ENOENT {
				err = platformport.ErrCPUProfileUncertain
			}
		}
	}()
	if err = file.Chmod(0600); err != nil {
		return out, platformport.ErrCPUProfileUnavailable
	}
	if _, err = file.Write(clean); err != nil {
		return out, platformport.ErrCPUProfileUnavailable
	}
	if err = file.Close(); err != nil || ctx.Err() != nil {
		return out, platformport.ErrCPUProfileUnavailable
	}
	digest := sha256.Sum256(clean)
	complete = true
	return platformport.CPUProfileArtifact{SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(clean))}, nil
}

func checkProfileDirectoryCapacity(dir *os.File) error {
	names, err := dir.Readdirnames(4097)
	if err != nil && err != io.EOF {
		return platformport.ErrCPUProfileUnavailable
	}
	if len(names) > 4096 {
		return platformport.ErrCPUProfileCapacity
	}
	var total int64
	for _, name := range names {
		if name == profileDirectoryMarker {
			continue
		}
		var st unix.Stat_t
		if err = unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err == unix.ENOENT {
			continue
		}
		if err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Size < 0 {
			return platformport.ErrCPUProfileUnavailable
		}
		if st.Size > platformport.CPUProfileCapacityBytes-total {
			return platformport.ErrCPUProfileCapacity
		}
		total += st.Size
	}
	if total > platformport.CPUProfileCapacityBytes-platformport.CPUProfileMaxBytes {
		return platformport.ErrCPUProfileCapacity
	}
	return nil
}

func (p *CPUProfiler) Read(ctx context.Context, id string) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil || !hexToken(id, 32) {
		return nil, platformport.ErrCPUProfileUnavailable
	}
	dir, err := p.openDirectory()
	if err != nil {
		return nil, platformport.ErrCPUProfileUnavailable
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), id+".cpu.pprof", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, platformport.ErrCPUProfileUnavailable
	}
	file := os.NewFile(uintptr(fd), "cpu-profile")
	defer file.Close()
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0777 != 0600 || stat.Size < 1 || stat.Size > platformport.CPUProfileMaxBytes {
		return nil, platformport.ErrCPUProfileUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, platformport.CPUProfileMaxBytes+1))
	if err != nil || len(data) > platformport.CPUProfileMaxBytes || ctx.Err() != nil {
		return nil, platformport.ErrCPUProfileUnavailable
	}
	return data, nil
}

// openDirectory rejects symlinks in every component, then pins the actual
// registered owner directory. It never initializes or adopts an unknown root.
func (p *CPUProfiler) openDirectory() (*os.File, error) {
	if p == nil || !filepath.IsAbs(p.directory) || filepath.Clean(p.directory) != p.directory {
		return nil, platformport.ErrCPUProfileUnavailable
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(p.directory, "/"), "/") {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	dir := os.NewFile(uintptr(fd), "profile-directory")
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0777 != 0700 {
		dir.Close()
		return nil, platformport.ErrCPUProfileUnavailable
	}
	markerFD, err := unix.Openat(fd, profileDirectoryMarker, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		dir.Close()
		return nil, err
	}
	marker := os.NewFile(uintptr(markerFD), "profile-marker")
	defer marker.Close()
	if err = unix.Fstat(markerFD, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Mode&0022 != 0 || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) {
		dir.Close()
		return nil, platformport.ErrCPUProfileUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(marker, 128))
	if err != nil || string(data) != profileDirectoryMarkerContent {
		dir.Close()
		return nil, platformport.ErrCPUProfileUnavailable
	}
	return dir, nil
}

func scrubCPUProfile(raw []byte) ([]byte, error) {
	if len(raw) > platformport.CPUProfileMaxBytes {
		return nil, platformport.ErrCPUProfileTooLarge
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, platformport.ErrCPUProfileUnavailable
	}
	defer gz.Close()
	plain, err := io.ReadAll(io.LimitReader(gz, profileUncompressedLimit+1))
	if len(plain) > profileUncompressedLimit {
		return nil, platformport.ErrCPUProfileTooLarge
	}
	if err != nil {
		return nil, platformport.ErrCPUProfileUnavailable
	}
	parsed, err := profile.ParseUncompressed(plain)
	if err != nil || parsed.CheckValid() != nil {
		return nil, platformport.ErrCPUProfileUnavailable
	}
	// Empty label allowlist: reconstruct the protobuf string table on Write,
	// rather than removing references while retaining secret string bytes.
	for _, sample := range parsed.Sample {
		sample.Label = nil
		sample.NumLabel = nil
		sample.NumUnit = nil
	}
	parsed.Comments = nil
	parsed.DocURL = ""
	parsed.DropFrames = ""
	parsed.KeepFrames = ""
	for _, mapping := range parsed.Mapping {
		mapping.File = filepath.Base(mapping.File)
	}
	for _, function := range parsed.Function {
		function.Filename = filepath.Base(function.Filename)
	}
	var clean cappedProfileBuffer
	if err = parsed.Write(&clean); err != nil {
		return nil, platformport.ErrCPUProfileUnavailable
	}
	if clean.overflow {
		return nil, platformport.ErrCPUProfileTooLarge
	}
	return clean.Bytes(), nil
}
