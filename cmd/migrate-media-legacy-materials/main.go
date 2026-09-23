// migrate-media-legacy-materials imports immutable legacy-to-V3 Media facts
// from an already frozen source snapshot. It never connects to a donor
// runtime and is deliberately separate from authenticated business intake.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const expectedSourceSystem = "ai-crm-v2"

type snapshot struct {
	Manifest struct {
		SourceSystem   string    `json:"source_system"`
		SourceRevision string    `json:"source_revision"`
		SnapshotAt     time.Time `json:"snapshot_at"`
	} `json:"manifest"`
	Materials []material `json:"materials"`
}

type material struct {
	Kind               string          `json:"kind"`
	LegacyID           string          `json:"legacy_id"`
	SourceRecord       json.RawMessage `json:"source_record"`
	SourceRecordDigest string          `json:"source_record_digest"`
	MaterialID         int64           `json:"material_id"`
	SourceDigest       string          `json:"source_digest"`
}

type report struct {
	Mode           string `json:"mode"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
	SourceSystem   string `json:"source_system"`
	SourceRevision string `json:"source_revision"`
	Count          int    `json:"count"`
	New            int    `json:"new"`
	Replayed       int    `json:"replayed"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-media-legacy-materials", flag.ContinueOnError)
	mode := flags.String("mode", "inspect", "inspect|dry-run|apply|verify")
	path := flags.String("snapshot", "", "frozen JSON mapping snapshot")
	wantDigest := flags.String("snapshot-sha256", "", "exact sha256 of frozen snapshot")
	actor := flags.Int64("actor-admin-user-id", 0, "explicit administrator authorizing import")
	confirm := flags.Bool("confirm-apply", false, "confirm immutable mapping writes")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("snapshot is required")
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	digest := digestHex(raw)
	var source snapshot
	if err = json.Unmarshal(raw, &source); err != nil {
		return err
	}
	if err = source.validate(); err != nil {
		return err
	}
	out := report{Mode: *mode, SnapshotSHA256: digest, SourceSystem: source.Manifest.SourceSystem, SourceRevision: source.Manifest.SourceRevision, Count: len(source.Materials)}
	if *mode == "inspect" {
		return print(out)
	}
	if *mode != "dry-run" && *mode != "apply" && *mode != "verify" {
		return errors.New("mode must be inspect, dry-run, apply, or verify")
	}
	if *actor < 1 {
		return errors.New("explicit actor-admin-user-id is required")
	}
	if *wantDigest != digest {
		return errors.New("snapshot-sha256 confirmation mismatch")
	}
	if *mode == "apply" && !*confirm {
		return errors.New("apply requires --confirm-apply")
	}
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: url})
	if err != nil {
		return err
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	repository, err := mediastore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	for _, item := range source.Materials {
		mapped, state, verifyErr := verify(ctx, uow, repository, source.Manifest.SourceSystem, item, *mode == "verify")
		if verifyErr != nil {
			return verifyErr
		}
		if state == "new" {
			out.New++
		} else {
			out.Replayed++
		}
		if *mode == "apply" && !mapped {
			mapping := mediaport.LegacyMaterialMapping{Reference: mediaport.LegacyMaterialReference{SourceSystem: source.Manifest.SourceSystem, MaterialKind: item.Kind, LegacyID: item.LegacyID}, MaterialKind: item.Kind, MaterialID: item.MaterialID, SourceDigest: item.SourceDigest, SourceRecordDigest: item.SourceRecordDigest}
			if err = repository.ImportLegacyMaterialMapping(ctx, mapping, fmt.Sprintf("frozen-import:admin:%d:%s", *actor, digest)); err != nil {
				return err
			}
		}
	}
	return print(out)
}

func verify(ctx context.Context, uow interface {
	Within(context.Context, func(context.Context) error) error
}, repository interface {
	mediaport.LegacyMaterialMappingResolver
	mediaport.GroupOpsMaterialSourceCapturer
}, sourceSystem string, item material, requireMapped bool) (bool, string, error) {
	mapped := false
	state := "new"
	err := uow.Within(ctx, func(tx context.Context) error {
		reference := mediaport.LegacyMaterialReference{SourceSystem: sourceSystem, MaterialKind: item.Kind, LegacyID: item.LegacyID}
		current, found, err := repository.ResolveLegacyMaterialMapping(tx, reference)
		if err != nil {
			return err
		}
		if found {
			if current.MaterialKind != item.Kind || current.MaterialID != item.MaterialID || current.SourceDigest != item.SourceDigest || current.SourceRecordDigest != item.SourceRecordDigest {
				return errors.New("immutable mapping drift for " + item.Kind + ":" + item.LegacyID)
			}
			mapped, state = true, "replayed"
		}
		if requireMapped && !mapped {
			return errors.New("verified mapping is missing for " + item.Kind + ":" + item.LegacyID)
		}
		captured, err := repository.CaptureGroupOpsMaterialSources(tx, mediaport.GroupOpsMaterialPlan{References: []mediaport.GroupOpsMaterialReference{{Kind: item.Kind, ID: item.MaterialID}}})
		if err != nil || len(captured.References) != 1 || captured.References[0].SourceDigest != item.SourceDigest {
			return errors.New("target Media source drift or unavailable for " + item.Kind + ":" + item.LegacyID)
		}
		return nil
	})
	return mapped, state, err
}

func (s snapshot) validate() error {
	if s.Manifest.SourceSystem != expectedSourceSystem || len(s.Manifest.SourceRevision) != 40 || s.Manifest.SnapshotAt.IsZero() || len(s.Materials) == 0 {
		return errors.New("invalid frozen Media mapping snapshot manifest")
	}
	if _, err := hex.DecodeString(s.Manifest.SourceRevision); err != nil || s.Manifest.SourceRevision != strings.ToLower(s.Manifest.SourceRevision) {
		return errors.New("invalid frozen Media mapping source revision")
	}
	seen := make(map[string]struct{}, len(s.Materials))
	for _, item := range s.Materials {
		if !validKind(item.Kind) || item.MaterialID < 1 || !validDigest(item.SourceDigest) || !validDigest(item.SourceRecordDigest) || !validLegacyID(item.LegacyID) || !validSourceRecord(item.SourceRecord, item.SourceRecordDigest, item.Kind, item.LegacyID) {
			return errors.New("invalid frozen Media mapping record")
		}
		key := item.Kind + "\x00" + item.LegacyID
		if _, exists := seen[key]; exists {
			return errors.New("duplicate frozen Media mapping record")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validSourceRecord(raw json.RawMessage, expected, kind, legacyID string) bool {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil || len(record) == 0 {
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return false
	}
	if sourceKind, _ := record["kind"].(string); sourceKind != kind {
		return false
	}
	var sourceID string
	switch value := record["legacy_id"].(type) {
	case json.Number:
		sourceID = string(value)
	case string:
		sourceID = value
	}
	if sourceID == "" {
		switch value := record["id"].(type) {
		case json.Number:
			sourceID = string(value)
		case string:
			sourceID = value
		}
	}
	if sourceID != legacyID {
		return false
	}
	canonical, err := json.Marshal(record)
	return err == nil && digestHex(canonical) == expected
}

func validKind(value string) bool {
	return value == "image" || value == "attachment" || value == "miniprogram" || value == "group_invite"
}
func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && value == strings.ToLower(value)
}
func validLegacyID(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\t\x00")
}
func digestHex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func print(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }

// Keep a stable order in tests and future non-stream snapshot constructors.
func (s *snapshot) sort() {
	sort.Slice(s.Materials, func(i, j int) bool {
		return s.Materials[i].Kind+"\x00"+s.Materials[i].LegacyID < s.Materials[j].Kind+"\x00"+s.Materials[j].LegacyID
	})
}
