package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

func TestProtectedHistoricalSnapshotPreservesActualGrantAndIsCanonical(t *testing.T) {
	snapshot := testSnapshot(t)
	// A least-privilege source MCP caller must remain a read-only MCP caller;
	// this proves the snapshot does not reconstruct a purpose template.
	snapshot.Clients[0].Scopes = []string{"read"}
	snapshot.Clients[0].Capabilities = []string{"mcp_read"}
	if err := populateManifest(&snapshot, snapshot.Manifest.SourceRevision); err != nil {
		t.Fatal(err)
	}
	canonical, digest, err := canonicalSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if digest == [32]byte{} || canonical.Clients[0].Capabilities[0] != "mcp_read" || len(canonical.Clients[0].Capabilities) != 1 || canonical.Clients[0].CorpID != "corp-historic" || canonical.Clients[0].OwnerScope["customer_id"][0] != "42" {
		t.Fatalf("canonical snapshot=%+v digest=%x", canonical, digest)
	}
	input, err := clientInput(canonical.Manifest.ImportRunID, canonical.Clients[0])
	if err != nil || len(input.Capabilities) != 1 || input.Capabilities[0] != "mcp_read" || input.CorpID != "corp-historic" || input.SourceAuthVersion != 9 {
		t.Fatalf("historical input=%+v err=%v", input, err)
	}
}

func TestProtectedSnapshotRejectsUnsafePermissionsAndDrift(t *testing.T) {
	directory := t.TempDir()
	keyPath, snapshotPath := writeTestKey(t, directory)
	snapshot := testSnapshot(t)
	if _, err := sealToFile(snapshot, snapshotPath, keyPath); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(snapshotPath)
	if err != nil || info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("protected snapshot=%v err=%v", info, err)
	}
	loaded, digest, err := loadFile(snapshotPath, keyPath)
	if err != nil || digest == [32]byte{} || len(loaded.Clients) != 1 || len(loaded.Audits) != 1 {
		t.Fatalf("load snapshot=%+v digest=%x err=%v", loaded, digest, err)
	}
	loaded.Manifest.Digests["auth_api_clients"] = stringsOf("00", 64)
	if _, _, err = canonicalSnapshot(loaded); err == nil {
		t.Fatal("snapshot digest drift was accepted")
	}
	loaded = testSnapshot(t)
	loaded.Clients[0].Capabilities = []string{"not_a_frozen_capability"}
	if err = populateManifest(&loaded, loaded.Manifest.SourceRevision); err != nil {
		t.Fatal(err)
	}
	// The protected snapshot preserves the unsupported source fact. It is
	// classified later as excluded instead of silently replaced by mcp_execute.
	if _, _, err = canonicalSnapshot(loaded); err != nil {
		t.Fatalf("unsupported source authorization was discarded instead of preserved: %v", err)
	}
}

func TestRunApplyRequiresDigestAndExplicitConfirmation(t *testing.T) {
	directory := t.TempDir()
	keyPath, snapshotPath := writeTestKey(t, directory)
	snapshot := testSnapshot(t)
	digest, err := sealToFile(snapshot, snapshotPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(t.Context(), []string{"-mode", "apply", "-snapshot", snapshotPath, "-snapshot-key-file", keyPath}); err == nil {
		t.Fatal("apply without snapshot digest reached the database path")
	}
	if err = run(t.Context(), []string{"-mode", "apply", "-snapshot", snapshotPath, "-snapshot-key-file", keyPath, "-manifest-sha256", fmt.Sprintf("%x", digest)}); err == nil {
		t.Fatal("apply without confirmation reached the database path")
	}
}

func testSnapshot(t *testing.T) historicalSnapshot {
	t.Helper()
	before, after := sha256.Sum256([]byte(`{"enabled":true}`)), sha256.Sum256([]byte(`{"enabled":false}`))
	snapshot := historicalSnapshot{
		Manifest: historicalManifest{SnapshotAt: time.Date(2026, 9, 6, 1, 2, 3, 0, time.FixedZone("historic", 8*60*60)), SourceRevision: "dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f"},
		Clients:  []historicalClientRow{{SourceRowID: "auth_api_clients/historic.mcp", ClientID: "historic.mcp", PrincipalID: "api_client:historic.mcp", PrincipalType: "api_client", DisplayName: "Historic MCP", Purpose: "mcp", Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{"mcp_read", "mcp_execute"}, AllowedCIDRs: []string{"203.0.113.0/24"}, CorpID: "corp-historic", OwnerScope: accessdomain.OwnerScope{"customer_id": {"42"}}, SourceEnabled: true, SourceAuthVersion: 9, TokenTTLSeconds: 1800}},
		Audits:   []historicalAuditRow{{SourceAuditID: "7", Operator: "crm_console", Action: "api_client_disabled", TargetType: "api_client", TargetID: "historic.mcp", BeforeDigest: hex.EncodeToString(before[:]), AfterDigest: hex.EncodeToString(after[:]), OccurredAt: time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)}},
	}
	if err := populateManifest(&snapshot, snapshot.Manifest.SourceRevision); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func writeTestKey(t *testing.T, directory string) (string, string) {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	keyPath := filepath.Join(directory, "snapshot.key")
	if err := os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	return keyPath, filepath.Join(directory, "snapshot.bin")
}

func stringsOf(value string, count int) string {
	out := ""
	for len(out) < count {
		out += value
	}
	return out[:count]
}
