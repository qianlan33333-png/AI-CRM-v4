package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	proof "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/cutoverproof"
	"os"
	"path/filepath"
	"testing"
)

func TestOpaqueCaptureCannotImportWithoutExternalProof(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "manifest")
	row := `{"source_id":7,"unionid":"opaque-test","service_product_id":11,"product_name":"test","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z"}`
	if err := os.WriteFile(source, []byte(snapshotMarker+"2026-09-11T01:02:03Z\n"+entitlementMarker+hex.EncodeToString([]byte(row))+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := run(ctx, []string{"--mode", "inspect-stream", "--source-stream", source, "--snapshot", destination, "--opaque-source"}); err != nil {
		t.Fatal(err)
	}
	m, err := load(destination)
	if err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != 4 || m.UnionIDScope != "" || m.ResolutionMode != "opaque_source_only" || len(m.Entitlements) != 1 {
		t.Fatal("opaque capture contract")
	}
	if _, err := extractStreamMode(source, "wechat-open-platform:test", true); err == nil {
		t.Fatal("opaque capture asserted scope")
	}
	if _, err := extractStream(source, ""); err == nil {
		t.Fatal("implicit empty scope accepted")
	}
	for _, mode := range []string{"preflight", "apply", "reconcile"} {
		if err := run(ctx, []string{"--mode", mode, "--snapshot", destination, "--manifest-sha256", hex.EncodeToString(m.rawDigest[:]), "--confirm-apply"}); err == nil {
			t.Fatalf("%s accepted raw capture", mode)
		}
	}
	if preflight(ctx, nil, m) == nil || apply(ctx, nil, m) == nil || reconcile(ctx, nil, m) == nil {
		t.Fatal("direct importer accepted raw capture")
	}
	if err := bindExternalProof(options{output: filepath.Join(dir, "derived")}, m); err == nil {
		t.Fatal("missing proof accepted")
	}

	keyPath := filepath.Join(dir, "proof.key")
	proofPath := filepath.Join(dir, "proof.enc")
	if err := os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString(make([]byte, 32))), 0600); err != nil {
		t.Fatal(err)
	}
	ps := proof.Snapshot{Version: 2, ResolutionMode: proof.ExistingWecomOnly, Scopes: proof.Scopes{CorpID: "test"}, CapturedAt: m.CapturedAt, Rows: []proof.Row{}}
	d, err := proof.Seal(ps, proofPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "bound")
	cfg := options{output: output, proofPath: proofPath, proofKey: keyPath, proofDigest: hex.EncodeToString(d[:]), corp: "test", matchedCorp: true}
	if err := bindExternalProof(cfg, m); err != nil {
		t.Fatal(err)
	}
	bound, err := load(output)
	if err != nil {
		t.Fatal(err)
	}
	if bound.SchemaVersion != 3 || bound.UnionIDScope != "" || bound.SourceSHA256 != hex.EncodeToString(m.rawDigest[:]) || !bound.CapturedAt.Equal(m.CapturedAt) || bound.Entitlements[0].UnionID != m.Entitlements[0].UnionID {
		t.Fatal("derivation lost protected source provenance")
	}
	proofctx, err := withExternalProof(ctx, cfg, bound)
	if err != nil {
		t.Fatal(err)
	}
	owner := &recordingResolver{}
	if _, err := resolveSidebarSubject(proofctx, owner, "", m.Entitlements[0].UnionID); err != nil || owner.calls != 0 {
		t.Fatal("missing proof source escaped quarantine")
	}
	forged := m
	forged.UnionIDScope = "wechat-open-platform:test"
	if validate(forged) == nil {
		t.Fatal("forged scope accepted")
	}
	forged = m
	forged.ProofSHA256 = "proof"
	if validate(forged) == nil {
		t.Fatal("partial proof accepted")
	}
}
