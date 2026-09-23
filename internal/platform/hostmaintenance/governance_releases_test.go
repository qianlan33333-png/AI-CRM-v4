package hostmaintenance

import (
	"context"
	"encoding/json"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGovernanceReleaseReaderVerifiesNarrowRootSummary(t *testing.T) {
	now := time.Now().UTC()
	sha := strings.Repeat("a", 40)
	original := platformport.GovernanceReleaseEvidence{Version: 1, GeneratedAt: now, CurrentRelease: sha, Facts: []platformport.GovernanceReleaseFact{{Sequence: 1, ReleaseSHA: sha, SucceededAt: now.Add(-time.Hour), ReceiptDigest: strings.Repeat("b", 64)}}}
	for _, tc := range []struct {
		name    string
		mutate  func(*platformport.GovernanceReleaseEvidence)
		wantErr bool
	}{
		{name: "valid permanent snapshot"},
		{name: "unknown version", mutate: func(e *platformport.GovernanceReleaseEvidence) { e.Version = 2 }, wantErr: true},
		{name: "future", mutate: func(e *platformport.GovernanceReleaseEvidence) { e.GeneratedAt = now.Add(2 * time.Minute) }, wantErr: true},
		{name: "different current", mutate: func(e *platformport.GovernanceReleaseEvidence) { e.CurrentRelease = strings.Repeat("c", 40) }, wantErr: true},
		{name: "no facts", mutate: func(e *platformport.GovernanceReleaseEvidence) { e.Facts = nil }, wantErr: true},
		{name: "duplicate sequence", mutate: func(e *platformport.GovernanceReleaseEvidence) { e.Facts = append(e.Facts, e.Facts[0]) }, wantErr: true},
		{name: "invalid digest", mutate: func(e *platformport.GovernanceReleaseEvidence) { e.Facts[0].ReceiptDigest = "SECRET" }, wantErr: true},
		{name: "current revoked", mutate: func(e *platformport.GovernanceReleaseEvidence) { e.Facts[0].Revoked = true }, wantErr: true},
		{name: "future fact", mutate: func(e *platformport.GovernanceReleaseEvidence) { e.Facts[0].SucceededAt = now.Add(time.Hour) }, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := original
			e.Facts = append([]platformport.GovernanceReleaseFact(nil), original.Facts...)
			if tc.mutate != nil {
				tc.mutate(&e)
			}
			raw, _ := json.Marshal(e)
			a := &GovernanceReleaseAdapter{read: func() ([]byte, error) { return raw, nil }, currentSHA: func() (string, error) { return sha, nil }, now: func() time.Time { return now }}
			out, err := a.ReadGovernanceReleases(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatal(out, err)
			}
			if err != nil && len(out.Facts) > 0 {
				t.Fatal("unverified data escaped")
			}
		})
	}
	raw, _ := json.Marshal(original)
	reads := 0
	a := &GovernanceReleaseAdapter{read: func() ([]byte, error) { return raw, nil }, currentSHA: func() (string, error) {
		reads++
		if reads == 2 {
			return strings.Repeat("c", 40), nil
		}
		return sha, nil
	}, now: func() time.Time { return now }}
	if _, err := a.ReadGovernanceReleases(context.Background()); err == nil {
		t.Fatal("concurrent release switch accepted")
	}
	a.currentSHA = func() (string, error) { return sha, nil }
	raw = []byte(strings.TrimSuffix(string(raw), "}") + `,"token":"SECRET"}`)
	if out, err := a.ReadGovernanceReleases(context.Background()); err == nil || len(out.Facts) != 0 {
		t.Fatal("unknown payload accepted")
	}
}

func TestGovernanceReleaseFileRejectsSymlinksSharedFilesAndWritableControl(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "control")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "governance-releases.json")
	write := func() {
		t.Helper()
		if err := os.WriteFile(file, []byte("{\"version\":1}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	read := func() error {
		_, err := readGovernanceReleaseFileFrom(file, []string{root, dir}, uint32(os.Getuid()))
		return err
	}
	if err := read(); err != nil {
		t.Fatal("valid controlled file", err)
	}
	if err := os.Chmod(file, 0666); err != nil {
		t.Fatal(err)
	}
	if read() == nil {
		t.Fatal("world writable file accepted")
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "hardlink")
	if err := os.Link(file, link); err != nil {
		t.Fatal(err)
	}
	if read() == nil {
		t.Fatal("shared inode accepted")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(link, file); err != nil {
		t.Fatal(err)
	}
	if read() == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	write()
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if read() == nil {
		t.Fatal("writable parent accepted")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, make([]byte, governanceReleaseLimit+1), 0644); err != nil {
		t.Fatal(err)
	}
	if read() == nil {
		t.Fatal("unbounded file accepted")
	}
}
