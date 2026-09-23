package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestEncryptedSnapshotRoundTripAndTamperDetection(t *testing.T) {
	payload, err := compactJSONObject([]byte(`{ "id": 1, "channel_id": 7, "url": "https://example.test/?a=1&b=<x>" }`))
	if err != nil {
		t.Fatal(err)
	}
	rowDigest := sha256.Sum256(payload)
	row := snapshotRow{SourcePK: "1", Digest: "sha256:" + hex.EncodeToString(rowDigest[:]), Payload: payload}
	manifest := snapshotManifest{SchemaVersion: 1, SnapshotTimestamp: time.Unix(1_788_336_000, 0).UTC(), SourceHostDigest: hashText("source"), Tables: []snapshotTable{{Name: "automation_channel_contact", Columns: []string{"id", "channel_id"}, Rows: []snapshotRow{row}}}}
	manifest.Tables[0].Digest = digestRows(manifest.Tables[0].Rows)
	manifest.ManifestDigest = manifest.computeDigest()
	manifest.SnapshotID = "channel-" + manifest.DigestHex()[:24]
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	path := filepath.Join(t.TempDir(), "snapshot.enc")
	if err := writeEncryptedSnapshot(path, key, manifest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode=%o", info.Mode().Perm())
	}
	loaded, err := loadEncryptedSnapshot(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SnapshotID != manifest.SnapshotID || loaded.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf("loaded=%+v", loaded)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadEncryptedSnapshot(path, key); err == nil {
		t.Fatal("tampered snapshot was accepted")
	}
}

func TestManifestRejectsRowAndTableDrift(t *testing.T) {
	payload := json.RawMessage(`{"id":1}`)
	digest := sha256.Sum256(payload)
	row := snapshotRow{SourcePK: "1", Digest: "sha256:" + hex.EncodeToString(digest[:]), Payload: payload}
	manifest := snapshotManifest{SchemaVersion: 1, SnapshotID: "channel-test", SnapshotTimestamp: time.Now().UTC(), SourceHostDigest: hashText("host"), Tables: []snapshotTable{{Name: "automation_channel", Digest: digestRows([]snapshotRow{row}), Rows: []snapshotRow{row}}}}
	manifest.ManifestDigest = manifest.computeDigest()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	manifest.Tables[0].Rows[0].Payload = json.RawMessage(`{"id":2}`)
	if err := manifest.Validate(); err == nil {
		t.Fatal("row drift was accepted")
	}
}

func TestDuplicateSourceIDsAreDetectedWithoutConflatingOccurrences(t *testing.T) {
	manifest := snapshotManifest{Tables: []snapshotTable{
		{Name: "automation_channel", Rows: []snapshotRow{{SourcePK: "id=7#1", Payload: json.RawMessage(`{"id":7,"code":"one"}`)}, {SourcePK: "id=7#2", Payload: json.RawMessage(`{"id":7,"code":"two"}`)}, {SourcePK: "id=8#1", Payload: json.RawMessage(`{"id":8}`)}}},
		{Name: "automation_channel_contact", Rows: []snapshotRow{{SourcePK: "id=9#1", Payload: json.RawMessage(`{"id":9,"channel_id":7}`)}}},
	}}
	duplicates := duplicateSourceIDs(manifest)
	if !duplicates["automation_channel"][7] || duplicates["automation_channel"][8] || !duplicateRowID(manifest.Tables[0].Rows[1], duplicates["automation_channel"]) {
		t.Fatalf("duplicates=%v", duplicates)
	}
	if duplicateRowID(manifest.Tables[1].Rows[0], duplicates["automation_channel_contact"]) {
		t.Fatalf("unique child row was marked duplicate: %v", duplicates)
	}
}

func TestParseSourceStreamBuildsDeterministicManifest(t *testing.T) {
	columns, err := json.Marshal([]string{"id", "channel_code"})
	if err != nil {
		t.Fatal(err)
	}
	row := []byte(`{ "channel_code": "legacy", "id": 7 }`)
	canonicalRow := `{"channel_code":"legacy","id":7}`
	stream := fmt.Sprintf("ignored psql header\n %s2026-09-04T01:02:03.123456Z \n %sautomation_channel|%s \n %s%s \n(1 row)\n",
		streamSnapshotPrefix, streamTablePrefix, hex.EncodeToString(columns), streamRowPrefix, hex.EncodeToString(row))
	manifest, err := parseSourceStream(bytes.NewBufferString(stream), "source.example")
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.SnapshotTimestamp.Format(time.RFC3339Nano); got != "2026-09-04T01:02:03.123456Z" {
		t.Fatalf("snapshot timestamp=%s", got)
	}
	if len(manifest.Tables) != 1 || manifest.Tables[0].Name != "automation_channel" || len(manifest.Tables[0].Rows) != 1 {
		t.Fatalf("manifest=%+v", manifest)
	}
	if manifest.Tables[0].Rows[0].SourcePK != "id=7#1" || string(manifest.Tables[0].Rows[0].Payload) != canonicalRow {
		t.Fatalf("row=%+v", manifest.Tables[0].Rows[0])
	}
}

func TestParseSourceStreamFailsClosed(t *testing.T) {
	for name, stream := range map[string]string{
		"missing timestamp": streamTablePrefix + "automation_channel|5b226964225d\n",
		"row first":         streamSnapshotPrefix + "2026-09-04T01:02:03Z\n" + streamRowPrefix + "7b7d\n",
		"bad row":           streamSnapshotPrefix + "2026-09-04T01:02:03Z\n" + streamTablePrefix + "automation_channel|5b226964225d\n" + streamRowPrefix + "00\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSourceStream(bytes.NewBufferString(stream), "source.example"); err == nil {
				t.Fatal("invalid source stream was accepted")
			}
		})
	}
}

func TestSemanticValidationRequiresReferencedDependenciesAndOwnerFallback(t *testing.T) {
	manifest := snapshotManifest{}
	for _, name := range expectedSourceTables {
		manifest.Tables = append(manifest.Tables, snapshotTable{Name: name})
	}
	for index := range manifest.Tables {
		switch manifest.Tables[index].Name {
		case "automation_channel":
			manifest.Tables[index].Rows = []snapshotRow{{Payload: json.RawMessage(`{"id":7,"owner_staff_id":"owner-a","welcome_image_library_ids":[3],"entry_tag_id":"tag-a"}`)}}
		case "image_library":
			manifest.Tables[index].Rows = []snapshotRow{{Payload: json.RawMessage(`{"id":3}`)}}
		case "wecom_corp_tags":
			manifest.Tables[index].Rows = []snapshotRow{{Payload: json.RawMessage(`{"tag_id":"tag-a"}`)}}
		}
	}
	result, err := validateSemantics(manifest)
	if err != nil || result.Channels != 1 || result.ReferencedMaterials != 1 || result.ReferencedTags != 1 {
		t.Fatalf("semantic validation=%+v err=%v", result, err)
	}
	assignees := semanticExpectedAssignees(manifest)
	if len(assignees[7]) != 1 || firstString(assignees[7][0].Payload, "staff_id") != "owner-a" {
		t.Fatalf("owner fallback=%v", assignees)
	}
	for index := range manifest.Tables {
		if manifest.Tables[index].Name == "image_library" {
			manifest.Tables[index].Rows = nil
		}
	}
	if result, err = validateSemantics(manifest); err == nil || result.MissingMaterialRows != 1 {
		t.Fatalf("missing material validation=%+v err=%v", result, err)
	}
}

func TestProjectMappedEntryTagRequiresCompleteOwnerProjection(t *testing.T) {
	id := int64(41)
	projected, name, group, ok := projectMappedEntryTag(&id, "mapped", " 新客 ", " 来源 ")
	if !ok || projected != id || name != "新客" || group != "来源" {
		t.Fatalf("mapped projection=(%v,%q,%q,%v)", projected, name, group, ok)
	}
	for testName, value := range map[string]struct {
		id          *int64
		state       string
		name, group string
	}{
		"unresolved":    {&id, "unresolved", "新客", "来源"},
		"missing id":    {nil, "mapped", "新客", "来源"},
		"missing name":  {&id, "mapped", "", "来源"},
		"missing group": {&id, "mapped", "新客", ""},
	} {
		t.Run(testName, func(t *testing.T) {
			projected, name, group, ok := projectMappedEntryTag(value.id, value.state, value.name, value.group)
			if ok || projected != nil || name != "" || group != "" {
				t.Fatalf("unsafe projection=(%v,%q,%q,%v)", projected, name, group, ok)
			}
		})
	}
}

func TestSemanticSourceAssignmentDefectsRemainBlockedWithoutInventingData(t *testing.T) {
	if got := semanticAssignmentBlockers("ratio", nil); len(got) != 1 || got[0] != "assignees_missing" {
		t.Fatalf("empty assignment blockers=%v", got)
	}
	assigned := []semanticAssignment{{id: 1, priority: 1, ratio: 100}, {id: 2, priority: 2, ratio: 100}}
	if got := semanticAssignmentBlockers("ratio", assigned); len(got) != 1 || got[0] != "assignment_ratio_invalid" {
		t.Fatalf("invalid ratio blockers=%v", got)
	}
	if assigned[0].ratio != 100 || assigned[1].ratio != 100 {
		t.Fatalf("source ratios were mutated: %+v", assigned)
	}
	assigned[1].priority = 1
	if !normalizeSemanticPriorities(assigned) || assigned[0].priority != 1 || assigned[1].priority != 2 {
		t.Fatalf("duplicate priorities were not deterministically projected: %+v", assigned)
	}
	if got := semanticAssignmentBlockers("ratio", []semanticAssignment{{ratio: 40}, {ratio: 60}}); len(got) != 0 {
		t.Fatalf("valid ratio blockers=%v", got)
	}
}

func TestSemanticConfigMismatchCountDoesNotDoubleCountConflicts(t *testing.T) {
	if got := semanticConfigMismatchCount(51, 46); got != 5 {
		t.Fatalf("mismatch count=%d", got)
	}
	if got := semanticConfigMismatchCount(51, 51); got != 0 {
		t.Fatalf("complete mismatch count=%d", got)
	}
	if got := semanticConfigMismatchCount(51, 52); got != 0 {
		t.Fatalf("overcomplete mismatch count=%d", got)
	}
}

func TestUnavailableHistoricalAssigneeIsRepresentedWithoutInventingAssignment(t *testing.T) {
	if got := semanticUnprojectedAssigneeCount(2, 1, true); got != 0 {
		t.Fatalf("represented unavailable assignee counted as loss: %d", got)
	}
	if got := semanticUnprojectedAssigneeCount(2, 1, false); got != 1 {
		t.Fatalf("silent missing assignee count=%d", got)
	}
	if got := semanticUnprojectedAssigneeCount(1, 1, false); got != 0 {
		t.Fatalf("fully projected assignee count=%d", got)
	}
}

func TestLegacyStateBindingAssetVersionIsDeterministicAndPositive(t *testing.T) {
	digest := sha256.Sum256([]byte("legacy-state"))
	first := legacyStateBindingAssetVersion(digest)
	second := legacyStateBindingAssetVersion(digest)
	if first != second || first < 4_000_000_000 {
		t.Fatalf("legacy binding version is not deterministic and positive: %d %d", first, second)
	}
	other := sha256.Sum256([]byte("other-state"))
	if first == legacyStateBindingAssetVersion(other) {
		t.Fatal("distinct binding digests produced the same deterministic test version")
	}
}

func TestLegacyAssetUpsertAdvancesOnlyLiveCanonicalProjection(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("channel_asset_replay_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `CREATE TABLE channel_legacy_acquisition_assets (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		import_run_id BIGINT NOT NULL,
		source_asset_id BIGINT NOT NULL,
		channel_id BIGINT NOT NULL,
		config_version BIGINT NOT NULL,
		asset_version BIGINT NOT NULL,
		kind TEXT NOT NULL,
		provider_asset_ref TEXT NOT NULL DEFAULT '',
		result_url TEXT NOT NULL DEFAULT '',
		source_status TEXT NOT NULL,
		verification_status TEXT NOT NULL DEFAULT 'legacy_unverified',
		source_digest BYTEA NOT NULL,
		provider_readback_digest TEXT NOT NULL DEFAULT '',
		verified_at TIMESTAMPTZ,
		retired_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
		UNIQUE(import_run_id,source_asset_id),
		UNIQUE(channel_id,kind,asset_version)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	digestOne := sha256.Sum256([]byte("one"))
	digestTwo := sha256.Sum256([]byte("two"))
	args := []any{int64(1), int64(7), int64(11), int64(3), int64(1_000_000_007), "config-old", "https://example.test/old", "active", digestOne[:]}
	if _, err = pool.Exec(ctx, legacyAssetUpsertSQL, args...); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE channel_legacy_acquisition_assets SET verification_status='legacy_verified_active',provider_readback_digest=$1,verified_at=clock_timestamp()`, "sha256:"+hex.EncodeToString(digestOne[:])); err != nil {
		t.Fatal(err)
	}
	args = []any{int64(2), int64(7), int64(11), int64(4), int64(1_000_000_007), "config-new", "https://example.test/new", "active", digestTwo[:]}
	if _, err = pool.Exec(ctx, legacyAssetUpsertSQL, args...); err != nil {
		t.Fatalf("later snapshot replay failed: %v", err)
	}
	var runID, configVersion int64
	var providerRef, status, readback string
	var verifiedAt *time.Time
	if err = pool.QueryRow(ctx, `SELECT import_run_id,config_version,provider_asset_ref,verification_status,provider_readback_digest,verified_at FROM channel_legacy_acquisition_assets`).Scan(&runID, &configVersion, &providerRef, &status, &readback, &verifiedAt); err != nil {
		t.Fatal(err)
	}
	if runID != 2 || configVersion != 4 || providerRef != "config-new" || status != "legacy_unverified" || readback != "" || verifiedAt != nil {
		t.Fatalf("advanced projection=(run=%d config=%d ref=%q status=%q readback=%q verified=%v)", runID, configVersion, providerRef, status, readback, verifiedAt)
	}
	if _, err = pool.Exec(ctx, `UPDATE channel_legacy_acquisition_assets SET retired_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	args[0] = int64(3)
	args[3] = int64(5)
	if _, err = pool.Exec(ctx, legacyAssetUpsertSQL, args...); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT import_run_id,config_version FROM channel_legacy_acquisition_assets`).Scan(&runID, &configVersion); err != nil {
		t.Fatal(err)
	}
	if runID != 2 || configVersion != 4 {
		t.Fatalf("retired projection was resurrected: run=%d config=%d", runID, configVersion)
	}
}

func TestChannelActivationRestoresArchivedShapeAtomically(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("channel_activation_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `CREATE TABLE channels (
		id BIGINT PRIMARY KEY,
		status TEXT NOT NULL CHECK(status IN ('active','inactive','archived')),
		current_config_version BIGINT NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL,
		archived_at TIMESTAMPTZ,
		CHECK((status='archived' AND archived_at IS NOT NULL) OR (status<>'archived' AND archived_at IS NULL))
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO channels(id,status,current_config_version,updated_at) VALUES(7,'inactive',3,clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	if result, execErr := pool.Exec(ctx, channelActivationSQL, int64(7), "archived", int64(3)); execErr != nil || result.RowsAffected() != 1 {
		t.Fatalf("archive activation result=%v err=%v", result.RowsAffected(), execErr)
	}
	var status string
	var archivedAt *time.Time
	if err = pool.QueryRow(ctx, `SELECT status,archived_at FROM channels WHERE id=7`).Scan(&status, &archivedAt); err != nil {
		t.Fatal(err)
	}
	if status != "archived" || archivedAt == nil {
		t.Fatalf("archived shape=(%q,%v)", status, archivedAt)
	}
	if _, err = pool.Exec(ctx, channelActivationSQL, int64(7), "active", int64(3)); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status,archived_at FROM channels WHERE id=7`).Scan(&status, &archivedAt); err != nil {
		t.Fatal(err)
	}
	if status != "active" || archivedAt != nil {
		t.Fatalf("active shape=(%q,%v)", status, archivedAt)
	}
}

func hashText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}
