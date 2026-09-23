package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	segmentmigration "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/migration"
)

func validSnapshot() segmentmigration.Snapshot {
	tables := map[string]json.RawMessage{}
	counts := map[string]int{}
	digests := map[string]string{}
	for _, name := range segmentmigration.LogicalTables {
		tables[name] = json.RawMessage("[]")
		counts[name] = 0
		digests[name] = "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"
	}
	return segmentmigration.Snapshot{Manifest: segmentmigration.Manifest{SourceSystem: "v2-test", DonorCommit: segmentmigration.DonorCommit, SnapshotAt: time.Unix(1, 0).UTC(), SchemaDigest: strings.Repeat("1", 64), SourceWatermarkDigest: strings.Repeat("2", 64), Counts: counts, Digests: digests}, Tables: tables}
}

func TestEncryptedSnapshotRoundTripAndTamper(t *testing.T) {
	directory := t.TempDir()
	key := filepath.Join(directory, "snapshot.key")
	path := filepath.Join(directory, "snapshot.enc")
	if err := generateKey(key); err != nil {
		t.Fatal(err)
	}
	if err := writeEncryptedSnapshot(path, key, validSnapshot()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	decoded, err := readEncryptedSnapshot(path, key)
	if err != nil || decoded.Manifest.SourceSystem != "v2-test" {
		t.Fatalf("decoded=%+v err=%v", decoded.Manifest, err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)-1] ^= 1
	if err = os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = readEncryptedSnapshot(path, key); err == nil || strings.Contains(err.Error(), "v2-test") {
		t.Fatalf("expected safe authentication error, got %v", err)
	}
}

func TestKeyPermissionsAndExclusiveOutputs(t *testing.T) {
	directory := t.TempDir()
	key := filepath.Join(directory, "snapshot.key")
	if err := generateKey(key); err != nil {
		t.Fatal(err)
	}
	if err := generateKey(key); err == nil {
		t.Fatal("expected existing key refusal")
	}
	if err := os.Chmod(key, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadKey(key); err == nil {
		t.Fatal("expected permissive key refusal")
	}
}

func TestCommandValidationDoesNotNeedDatabase(t *testing.T) {
	var output bytes.Buffer
	if err := execute(nil, &output); err == nil {
		t.Fatal("expected missing command")
	}
	if err := execute([]string{"apply"}, &output); err == nil || !strings.Contains(err.Error(), "confirm-import") {
		t.Fatalf("unexpected apply error %v", err)
	}
	if err := execute([]string{"rollback"}, &output); err == nil || !strings.Contains(err.Error(), "confirm-rollback") {
		t.Fatalf("unexpected rollback error %v", err)
	}
	if err := execute([]string{"reconcile", "--batch-key", "batch"}, &output); err == nil || !strings.Contains(err.Error(), "snapshot-file") {
		t.Fatalf("unexpected reconcile error %v", err)
	}
	if err := execute([]string{"unknown"}, &output); err == nil {
		t.Fatal("expected unknown command")
	}
}
