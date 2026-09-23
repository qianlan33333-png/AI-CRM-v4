package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validFixture(t *testing.T) Snapshot {
	t.Helper()
	snapshot := Snapshot{Manifest: Manifest{SourceSystem: "fixture", DonorCommit: DonorCommit, SnapshotAt: time.Now().UTC(), SchemaDigest: strings.Repeat("1", 64), SourceWatermarkDigest: strings.Repeat("2", 64), Counts: map[string]int{}, Digests: map[string]string{}}, Tables: map[string]json.RawMessage{}}
	for _, name := range LogicalTables {
		raw := json.RawMessage(`[]`)
		snapshot.Tables[name] = raw
		snapshot.Manifest.Counts[name] = 0
		digest := sha256.Sum256(raw)
		snapshot.Manifest.Digests[name] = hex.EncodeToString(digest[:])
	}
	return snapshot
}

func TestValidateSnapshotDetectsDrift(t *testing.T) {
	snapshot := validFixture(t)
	if err := ValidateSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Tables["audience_groups"] = json.RawMessage(`[{"id":1}]`)
	if err := ValidateSnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "count mismatch") {
		t.Fatalf("expected count drift, got %v", err)
	}
}

func TestValidateSchemaRequiresAllowlistedColumns(t *testing.T) {
	rows := []schemaRow{}
	for table, columns := range requiredColumns {
		for _, column := range columns {
			rows = append(rows, schemaRow{Table: table, Column: column})
		}
	}
	if err := validateSchema(rows); err != nil {
		t.Fatal(err)
	}
	rows = rows[1:]
	if err := validateSchema(rows); err == nil {
		t.Fatal("missing required column must fail closed")
	}
}

func TestValidateSnapshotRejectsOrphanAudienceRows(t *testing.T) {
	snapshot := validFixture(t)
	snapshot.Tables["audience_members"] = json.RawMessage(`[{"segment_id":9,"customer_id":1}]`)
	snapshot.Manifest.Counts["audience_members"] = 1
	digest := sha256.Sum256(snapshot.Tables["audience_members"])
	snapshot.Manifest.Digests["audience_members"] = hex.EncodeToString(digest[:])
	if err := ValidateSnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "orphan") {
		t.Fatalf("expected orphan rejection, got %v", err)
	}
}
