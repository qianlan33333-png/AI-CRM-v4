package hostmaintenance

import (
	"bytes"
	"context"
	"encoding/json"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"io"
	"os"
	"regexp"
	"syscall"
	"time"
)

const governanceReleaseFile = resultDirectory + "/governance-releases.json"
const governanceReleaseLimit = 1024 * 1024

var governanceDigestFormat = regexp.MustCompile(`^[a-f0-9]{64}$`)

type GovernanceReleaseAdapter struct {
	read       func() ([]byte, error)
	currentSHA func() (string, error)
	now        func() time.Time
}

var _ platformport.GovernanceReleaseReader = (*GovernanceReleaseAdapter)(nil)

func NewGovernanceReleaseReader() *GovernanceReleaseAdapter {
	return &GovernanceReleaseAdapter{read: readGovernanceReleaseFile, currentSHA: readCurrentSHA, now: time.Now}
}
func readGovernanceReleaseFile() ([]byte, error) {
	return readGovernanceReleaseFileFrom(governanceReleaseFile, []string{"/var", "/var/lib", resultDirectory}, 0)
}

// The production entry point fixes both path and root authority; this private
// seam permits filesystem attack tests without touching the host installation.
func readGovernanceReleaseFileFrom(filename string, directories []string, uid uint32) ([]byte, error) {
	for _, path := range directories {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
			return nil, ErrEvidence
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uid {
			return nil, ErrEvidence
		}
	}
	file, err := os.OpenFile(filename, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrEvidence
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > governanceReleaseLimit {
		return nil, ErrEvidence
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uid || owner.Nlink != 1 {
		return nil, ErrEvidence
	}
	raw, err := io.ReadAll(io.LimitReader(file, governanceReleaseLimit+1))
	if err != nil || len(raw) > governanceReleaseLimit {
		return nil, ErrEvidence
	}
	after, err := os.Lstat(filename)
	if err != nil || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) || info.Size() != after.Size() {
		return nil, ErrEvidence
	}
	return raw, nil
}
func (a *GovernanceReleaseAdapter) ReadGovernanceReleases(ctx context.Context) (out platformport.GovernanceReleaseEvidence, err error) {
	if ctx == nil || ctx.Err() != nil || a.read == nil || a.currentSHA == nil || a.now == nil {
		return out, ErrUnavailable
	}
	sha, err := a.currentSHA()
	if err != nil {
		return out, ErrEvidence
	}
	raw, err := a.read()
	if err != nil || len(raw) > governanceReleaseLimit {
		return out, ErrEvidence
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil {
		return platformport.GovernanceReleaseEvidence{}, ErrEvidence
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || out.Version != 1 || out.GeneratedAt.IsZero() || out.GeneratedAt.After(a.now().Add(time.Minute)) || out.CurrentRelease != sha || !releaseSHAFormat.MatchString(sha) || len(out.Facts) == 0 || len(out.Facts) > 2048 {
		return platformport.GovernanceReleaseEvidence{}, ErrEvidence
	}
	seen := map[int64]bool{}
	current := false
	for _, f := range out.Facts {
		if f.Sequence < 1 || seen[f.Sequence] || !releaseSHAFormat.MatchString(f.ReleaseSHA) || !governanceDigestFormat.MatchString(f.ReceiptDigest) || f.SucceededAt.IsZero() || f.SucceededAt.After(out.GeneratedAt.Add(time.Minute)) {
			return platformport.GovernanceReleaseEvidence{}, ErrEvidence
		}
		seen[f.Sequence] = true
		if f.ReleaseSHA == sha && !f.Revoked {
			current = true
		}
	}
	after, err := a.currentSHA()
	if err != nil || after != sha || !current {
		return platformport.GovernanceReleaseEvidence{}, ErrEvidence
	}
	return out, nil
}
