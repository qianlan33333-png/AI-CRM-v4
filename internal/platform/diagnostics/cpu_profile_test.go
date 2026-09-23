package diagnostics

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

func testCPUProfiler(t *testing.T) *CPUProfiler {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, profileDirectoryMarker), []byte(profileDirectoryMarkerContent), 0644); err != nil {
		t.Fatal(err)
	}
	return &CPUProfiler{directory: dir}
}

func TestCPUProfileRealFiveSecondsSanitizedPrivateAndReadable(t *testing.T) {
	p := testCPUProfiler(t)
	id := strings.Repeat("1", 32)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		pprof.Do(ctx, pprof.Labels("token", "SECRET-profile-label", "customer_phone", "13812345678"), func(ctx context.Context) {
			for ctx.Err() == nil {
				for i := 0; i < 100000; i++ {
					_ = i * i
				}
			}
		})
	}()
	started := time.Now()
	artifact, err := p.Capture(context.Background(), id)
	stop()
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 5*time.Second || elapsed > 9*time.Second {
		t.Fatalf("fixed sample duration=%s", elapsed)
	}
	data, err := p.Read(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != artifact.Bytes || artifact.Bytes < 1 {
		t.Fatalf("artifact=%+v", artifact)
	}
	parsed, err := profile.ParseData(data)
	if err != nil || len(parsed.Sample) == 0 {
		t.Fatalf("real profile did not parse: %v", err)
	}
	if parsed.DurationNanos < int64(5*time.Second) {
		t.Fatalf("profile duration=%d", parsed.DurationNanos)
	}
	for _, sample := range parsed.Sample {
		if len(sample.Label)+len(sample.NumLabel)+len(sample.NumUnit) != 0 {
			t.Fatal("labels retained")
		}
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(gz)
	gz.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SECRET-profile-label", "13812345678", "customer_phone"} {
		if bytes.Contains(plain, []byte(secret)) {
			t.Fatal("secret remains in serialized string table")
		}
	}
	info, err := os.Stat(filepath.Join(p.directory, id+".cpu.pprof"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file permissions=%v %v", info, err)
	}
}

func TestCPUProfileCancelledCollectionStopsAndSingleProcessGuard(t *testing.T) {
	p := testCPUProfiler(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := p.Capture(ctx, strings.Repeat("2", 32)); done <- err }()
	deadline := time.After(2 * time.Second)
	for !cpuProfileActive.Load() {
		select {
		case <-deadline:
			t.Fatal("sample did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if _, err := p.Capture(context.Background(), strings.Repeat("3", 32)); !errors.Is(err, platformport.ErrCPUProfileBusy) {
		t.Fatalf("parallel sample=%v", err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled sample reported success")
	}
	if cpuProfileActive.Load() {
		t.Fatal("process guard retained after cancellation")
	}
	files, err := os.ReadDir(p.directory)
	if err != nil || len(files) != 1 {
		t.Fatalf("cancelled sample left artifact: %v %v", files, err)
	}
	// A later profiler can start: cancellation released the runtime-wide CPU slot.
	var b bytes.Buffer
	if err = pprof.StartCPUProfile(&b); err != nil {
		t.Fatal(err)
	}
	pprof.StopCPUProfile()
}

func TestCPUProfileScrubsLabelsCommentsAndRejectsBounds(t *testing.T) {
	fixture := &profile.Profile{SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}, Sample: []*profile.Sample{{Value: []int64{1}, Label: map[string][]string{"token": {"SECRET-label"}}, NumLabel: map[string][]int64{"secret-id": {123}}, NumUnit: map[string][]string{"secret-id": {"SECRET-unit"}}}}, Comments: []string{"SECRET-comment"}, DocURL: "https://SECRET-url.invalid", DropFrames: "SECRET-drop", KeepFrames: "SECRET-keep", Function: []*profile.Function{{ID: 1, Name: "safeFunction", Filename: "/Users/SECRET-path/main.go"}}, Mapping: []*profile.Mapping{{ID: 1, File: "/Users/SECRET-binary/aicrm"}}}
	var raw bytes.Buffer
	if err := fixture.Write(&raw); err != nil {
		t.Fatal(err)
	}
	clean, err := scrubCPUProfile(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(clean))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(gz)
	gz.Close()
	if bytes.Contains(data, []byte("SECRET")) || bytes.Contains(data, []byte("secret-id")) {
		t.Fatal("secret retained after rewrite")
	}
	if _, err = profile.ParseData(clean); err != nil {
		t.Fatal(err)
	}
	if _, err = scrubCPUProfile(make([]byte, platformport.CPUProfileMaxBytes+1)); !errors.Is(err, platformport.ErrCPUProfileTooLarge) {
		t.Fatalf("raw bound=%v", err)
	}
	var bomb bytes.Buffer
	writer := gzip.NewWriter(&bomb)
	_, _ = writer.Write(make([]byte, profileUncompressedLimit+1))
	writer.Close()
	if _, err = scrubCPUProfile(bomb.Bytes()); !errors.Is(err, platformport.ErrCPUProfileTooLarge) {
		t.Fatalf("decompressed bound=%v", err)
	}
	var capped cappedProfileBuffer
	_, _ = capped.Write(make([]byte, platformport.CPUProfileMaxBytes))
	_, _ = capped.Write([]byte{1})
	if !capped.overflow || capped.Len() != platformport.CPUProfileMaxBytes {
		t.Fatal("writer did not cap memory")
	}
}

func TestCPUProfileFileGuards(t *testing.T) {
	p := testCPUProfiler(t)
	id := strings.Repeat("4", 32)
	path := filepath.Join(p.directory, id+".cpu.pprof")
	if _, err := p.Read(context.Background(), "../"+id); err == nil {
		t.Fatal("path traversal accepted")
	}
	if err := os.Symlink(filepath.Join(p.directory, profileDirectoryMarker), path); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(context.Background(), id); err == nil {
		t.Fatal("symlink read accepted")
	}
	os.Remove(path)
	if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(context.Background(), id); err == nil {
		t.Fatal("public file read accepted")
	}
	os.Chmod(path, 0600)
	if err := os.Link(path, path+".link"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(context.Background(), id); err == nil {
		t.Fatal("hardlink read accepted")
	}
	os.Remove(path + ".link")
	if err := os.Truncate(path, platformport.CPUProfileMaxBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(context.Background(), id); err == nil {
		t.Fatal("oversized read accepted")
	}
	link := filepath.Join(p.directory, "alias")
	if err := os.Symlink(p.directory, link); err != nil {
		t.Fatal(err)
	}
	alias := &CPUProfiler{directory: link}
	if _, err := alias.openDirectory(); err == nil {
		t.Fatal("symlink root accepted")
	}
	os.Remove(filepath.Join(p.directory, profileDirectoryMarker))
	if _, err := p.openDirectory(); err == nil {
		t.Fatal("unregistered root accepted")
	}
}

func TestCPUProfilePhysicalCapacityIncludesExpiredArtifacts(t *testing.T) {
	p := testCPUProfiler(t)
	path := filepath.Join(p.directory, strings.Repeat("5", 32)+".cpu.pprof")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(platformport.CPUProfileCapacityBytes); err != nil {
		t.Fatal(err)
	}
	file.Close()
	old := time.Now().Add(-721 * time.Hour)
	if err = os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err = p.Capture(context.Background(), strings.Repeat("6", 32)); !errors.Is(err, platformport.ErrCPUProfileCapacity) {
		t.Fatalf("expired physical files bypassed quota=%v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("profiler started despite full physical directory")
	}
	if cpuProfileActive.Load() {
		t.Fatal("capacity refusal leaked local guard")
	}
}

func TestCPUProfileDoesNotStopAnExistingProfiler(t *testing.T) {
	p := testCPUProfiler(t)
	var first, second bytes.Buffer
	if err := pprof.StartCPUProfile(&first); err != nil {
		t.Fatal(err)
	}
	defer pprof.StopCPUProfile()
	if _, err := p.Capture(context.Background(), strings.Repeat("7", 32)); !errors.Is(err, platformport.ErrCPUProfileBusy) {
		t.Fatalf("existing runtime profile=%v", err)
	}
	if err := pprof.StartCPUProfile(&second); err == nil {
		pprof.StopCPUProfile()
		t.Fatal("failed capture stopped someone else's profiler")
	}
}
