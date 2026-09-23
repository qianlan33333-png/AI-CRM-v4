// Command migrate-v2-runtime-config-releases imports a sealed, read-only V2
// config-release snapshot into Config's historical ledger. It deliberately
// never creates a live runtime release or changes the active pointer.
package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const (
	schemaVersion = "aicrm-v2-runtime-config-release-history-v2"
	sourceSystem  = "ai-crm-v2"
)

var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type manifest struct {
	SchemaVersion  string            `json:"schema_version"`
	SourceSystem   string            `json:"source_system"`
	SourceRevision string            `json:"source_revision"`
	SnapshotAt     time.Time         `json:"snapshot_at"`
	Counts         map[string]int    `json:"counts"`
	Digests        map[string]string `json:"digests"`
}
type release struct {
	ID                  int64           `json:"id"`
	ReleaseKey          string          `json:"release_key"`
	ProfileID           string          `json:"profile_id"`
	Status              string          `json:"status"`
	Changes             json.RawMessage `json:"changes"`
	Before              json.RawMessage `json:"before"`
	ValidationErrors    json.RawMessage `json:"validation_errors"`
	Checksum            string          `json:"checksum"`
	BasedOnReleaseID    *int64          `json:"based_on_release_id,omitempty"`
	RollbackOfReleaseID *int64          `json:"rollback_of_release_id,omitempty"`
	CreatedBy           string          `json:"created_by"`
	CreatedAt           time.Time       `json:"created_at"`
	ValidatedAt         *time.Time      `json:"validated_at,omitempty"`
	PublishedBy         string          `json:"published_by,omitempty"`
	PublishedAt         *time.Time      `json:"published_at,omitempty"`
}
type snapshot struct {
	Manifest manifest  `json:"manifest"`
	Releases []release `json:"releases"`
}
type rowDisposition struct {
	SourceDigest []byte
	Outcome      string
	Reason       string
}
type result struct{ Input, Pending, Excluded, Replayed int }

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate-v2-runtime-config-releases", flag.ContinueOnError)
	mode := fs.String("mode", "inspect", "extract|inspect|dry-run|apply|verify")
	snapshotPath := fs.String("snapshot", "", "protected V2 release snapshot")
	keyPath := fs.String("snapshot-key-file", "", "0600 base64 AES-256 key")
	revision := fs.String("source-revision", "", "40-character V2 source revision for extract")
	want := fs.String("manifest-sha256", "", "snapshot digest confirmation")
	confirm := fs.Bool("confirm-apply", false, "confirm read-only ledger write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *mode == "extract" {
		if *snapshotPath == "" || *keyPath == "" || !revisionPattern.MatchString(*revision) {
			return errors.New("extract requires snapshot, snapshot-key-file, and a 40-character source-revision")
		}
		url, err := platformconfig.SourceDatabaseURL()
		if err != nil {
			return errors.New("source database is unavailable")
		}
		pool, err := pgxpool.New(ctx, url)
		if err != nil {
			return errors.New("source database is unavailable")
		}
		defer pool.Close()
		s, err := extract(ctx, pool, *revision)
		if err != nil {
			return err
		}
		digest, err := sealToFile(s, *snapshotPath, *keyPath)
		if err != nil {
			return err
		}
		return writeSummary(map[string]any{"mode": "extract", "manifest_sha256": hex.EncodeToString(digest[:]), "counts": s.Manifest.Counts})
	}
	if *snapshotPath == "" || *keyPath == "" {
		return errors.New("snapshot and snapshot-key-file are required")
	}
	s, digest, err := loadFile(*snapshotPath, *keyPath)
	if err != nil {
		return err
	}
	if *mode == "inspect" {
		return writeSummary(map[string]any{"mode": "inspect", "manifest_sha256": hex.EncodeToString(digest[:]), "counts": s.Manifest.Counts, "mapping": summarize(s)})
	}
	if *mode != "dry-run" && *mode != "apply" && *mode != "verify" {
		return errors.New("unknown mode")
	}
	if *want != hex.EncodeToString(digest[:]) {
		return errors.New("manifest-sha256 confirmation mismatch")
	}
	if *mode == "apply" && !*confirm {
		return errors.New("apply requires --confirm-apply")
	}
	if *mode == "dry-run" {
		return writeSummary(map[string]any{"mode": "dry-run", "eligible": true, "manifest_sha256": hex.EncodeToString(digest[:]), "mapping": summarize(s)})
	}
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		return errors.New("target database is unavailable")
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: url})
	if err != nil {
		return errors.New("target database is unavailable")
	}
	defer pool.Close()
	if *mode == "apply" {
		out, err := apply(ctx, pool.Native(), s, digest)
		if err != nil {
			return err
		}
		return writeSummary(map[string]any{"mode": "apply", "manifest_sha256": hex.EncodeToString(digest[:]), "result": out})
	}
	out, err := verify(ctx, pool.Native(), s, digest)
	if err != nil {
		return err
	}
	return writeSummary(map[string]any{"mode": "verify", "manifest_sha256": hex.EncodeToString(digest[:]), "result": out})
}

func extract(ctx context.Context, pool *pgxpool.Pool, revision string) (snapshot, error) {
	if pool == nil || !revisionPattern.MatchString(revision) {
		return snapshot{}, errors.New("invalid source snapshot request")
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return snapshot{}, errors.New("begin source snapshot")
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout='15s'"); err != nil {
		return snapshot{}, errors.New("configure source snapshot")
	}
	var out snapshot
	if err = tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&out.Manifest.SnapshotAt); err != nil {
		return snapshot{}, errors.New("read source snapshot time")
	}
	rows, err := tx.Query(ctx, `SELECT id,release_key,profile_id,status,changes_json,before_json,validation_errors_json,checksum,based_on_release_id,rollback_of_release_id,created_by,created_at,validated_at,COALESCE(published_by,''),published_at FROM config_releases ORDER BY id`)
	if err != nil {
		return snapshot{}, errors.New("read source config releases")
	}
	defer rows.Close()
	for rows.Next() {
		var item release
		if err = rows.Scan(&item.ID, &item.ReleaseKey, &item.ProfileID, &item.Status, &item.Changes, &item.Before, &item.ValidationErrors, &item.Checksum, &item.BasedOnReleaseID, &item.RollbackOfReleaseID, &item.CreatedBy, &item.CreatedAt, &item.ValidatedAt, &item.PublishedBy, &item.PublishedAt); err != nil {
			return snapshot{}, errors.New("read source config release")
		}
		out.Releases = append(out.Releases, item)
	}
	if err = rows.Err(); err != nil {
		return snapshot{}, errors.New("read source config releases")
	}
	if err = populateManifest(&out, revision, out.Manifest.SnapshotAt); err != nil {
		return snapshot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return snapshot{}, errors.New("finish source snapshot")
	}
	return out, nil
}

func populateManifest(s *snapshot, revision string, at time.Time) error {
	if s == nil || !revisionPattern.MatchString(revision) || at.IsZero() {
		return errors.New("invalid source snapshot")
	}
	normalize(s)
	raw, err := json.Marshal(s.Releases)
	if err != nil {
		return errors.New("canonicalize source snapshot")
	}
	digest := sha256.Sum256(raw)
	s.Manifest = manifest{SchemaVersion: schemaVersion, SourceSystem: sourceSystem, SourceRevision: revision, SnapshotAt: at.UTC(), Counts: map[string]int{"config_releases": len(s.Releases)}, Digests: map[string]string{"config_releases": hex.EncodeToString(digest[:])}}
	return validate(*s)
}
func canonical(s snapshot) ([]byte, [32]byte, error) {
	normalize(&s)
	if err := validate(s); err != nil {
		return nil, [32]byte{}, err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, [32]byte{}, errors.New("canonicalize source snapshot")
	}
	return raw, sha256.Sum256(raw), nil
}
func normalize(s *snapshot) {
	if s == nil {
		return
	}
	s.Manifest.SnapshotAt = s.Manifest.SnapshotAt.UTC()
	sort.Slice(s.Releases, func(i, j int) bool { return s.Releases[i].ID < s.Releases[j].ID })
	for i := range s.Releases {
		s.Releases[i].CreatedAt = s.Releases[i].CreatedAt.UTC()
		if s.Releases[i].ValidatedAt != nil {
			x := s.Releases[i].ValidatedAt.UTC()
			s.Releases[i].ValidatedAt = &x
		}
		if s.Releases[i].PublishedAt != nil {
			x := s.Releases[i].PublishedAt.UTC()
			s.Releases[i].PublishedAt = &x
		}
		s.Releases[i].Changes = canonicalRaw(s.Releases[i].Changes)
		s.Releases[i].Before = canonicalRaw(s.Releases[i].Before)
		s.Releases[i].ValidationErrors = canonicalRaw(s.Releases[i].ValidationErrors)
	}
}
func canonicalRaw(raw json.RawMessage) json.RawMessage {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}
func validate(s snapshot) error {
	if s.Manifest.SchemaVersion != schemaVersion || s.Manifest.SourceSystem != sourceSystem || !revisionPattern.MatchString(s.Manifest.SourceRevision) || s.Manifest.SnapshotAt.IsZero() || s.Manifest.Counts["config_releases"] != len(s.Releases) || len(s.Manifest.Counts) != 1 || len(s.Manifest.Digests) != 1 {
		return errors.New("invalid source snapshot")
	}
	raw, err := json.Marshal(s.Releases)
	if err != nil {
		return errors.New("invalid source snapshot")
	}
	d := sha256.Sum256(raw)
	if s.Manifest.Digests["config_releases"] != hex.EncodeToString(d[:]) {
		return errors.New("invalid source snapshot")
	}
	seen := map[int64]bool{}
	for _, item := range s.Releases {
		if item.ID < 1 || seen[item.ID] || item.ReleaseKey == "" || len(item.ReleaseKey) > 200 || item.ProfileID == "" || len(item.ProfileID) > 200 || !validLegacyStatus(item.Status) || item.CreatedAt.IsZero() || !jsonObject(item.Changes) || !jsonObject(item.Before) || !jsonArray(item.ValidationErrors) || len(item.Checksum) > 200 || len(item.CreatedBy) > 200 || len(item.PublishedBy) > 200 {
			return errors.New("invalid source snapshot")
		}
		seen[item.ID] = true
	}
	return nil
}
func validLegacyStatus(v string) bool {
	switch v {
	case "draft", "validated", "validation_failed", "published", "superseded":
		return true
	}
	return false
}
func jsonObject(raw json.RawMessage) bool {
	var v map[string]json.RawMessage
	return json.Unmarshal(raw, &v) == nil
}
func jsonArray(raw json.RawMessage) bool {
	var v []json.RawMessage
	return json.Unmarshal(raw, &v) == nil
}
func digestRelease(item release) ([32]byte, error) {
	copy := item
	copy.Changes = canonicalRaw(copy.Changes)
	copy.ValidationErrors = canonicalRaw(copy.ValidationErrors)
	raw, err := json.Marshal(copy)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}
func classify(item release) (rowDisposition, error) {
	digest, err := digestRelease(item)
	if err != nil {
		return rowDisposition{}, errors.New("canonicalize source row")
	}
	// The frozen V2 catalog has no key semantically equivalent to this V3-only
	// Automation limit. Do not reinterpret a similarly shaped number: preserve
	// the protected original row and record an explicit excluded outcome.
	return rowDisposition{SourceDigest: digest[:], Outcome: "excluded", Reason: "no_v3_runtime_equivalence"}, nil
}

func summarize(s snapshot) map[string]int {
	out := map[string]int{"pending": 0, "excluded": 0}
	for _, item := range s.Releases {
		d, e := classify(item)
		if e != nil {
			out["excluded"]++
			continue
		}
		out[d.Outcome]++
	}
	return out
}

func apply(ctx context.Context, pool *pgxpool.Pool, s snapshot, manifestDigest [32]byte) (result, error) {
	if pool == nil {
		return result{}, errors.New("target database is unavailable")
	}
	if err := validate(s); err != nil {
		return result{}, err
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return result{}, errors.New("begin target history import")
	}
	defer tx.Rollback(ctx)
	var batchID int64
	var existing []byte
	err = tx.QueryRow(ctx, `SELECT id,manifest_digest FROM config_runtime_release_history_batches WHERE source_system=$1 AND source_revision=$2 FOR UPDATE`, s.Manifest.SourceSystem, s.Manifest.SourceRevision).Scan(&batchID, &existing)
	if err == nil {
		if !bytes.Equal(existing, manifestDigest[:]) {
			return result{}, errors.New("source revision digest drift")
		}
		var input, pending, excluded int
		if err = tx.QueryRow(ctx, `SELECT input_count,pending_count,excluded_count FROM config_runtime_release_history_batches WHERE id=$1`, batchID).Scan(&input, &pending, &excluded); err != nil {
			return result{}, errors.New("read historical import")
		}
		if err = tx.Commit(ctx); err != nil {
			return result{}, errors.New("finish historical import")
		}
		return result{Input: input, Pending: pending, Excluded: excluded, Replayed: input}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result{}, errors.New("read historical import")
	}
	out := result{Input: len(s.Releases)}
	rows := make([]rowDisposition, len(s.Releases))
	for i, item := range s.Releases {
		rows[i], err = classify(item)
		if err != nil {
			return result{}, err
		}
		if rows[i].Outcome == "pending" {
			out.Pending++
		} else {
			out.Excluded++
		}
	}
	err = tx.QueryRow(ctx, `INSERT INTO config_runtime_release_history_batches(source_system,source_revision,manifest_digest,snapshot_at,status,input_count,pending_count,excluded_count,applied_at) VALUES($1,$2,$3,$4,'applied',$5,$6,$7,clock_timestamp()) RETURNING id`, s.Manifest.SourceSystem, s.Manifest.SourceRevision, manifestDigest[:], s.Manifest.SnapshotAt.UTC(), out.Input, out.Pending, out.Excluded).Scan(&batchID)
	if err != nil {
		return result{}, errors.New("write historical import")
	}
	for i, item := range s.Releases {
		if _, err = tx.Exec(ctx, `INSERT INTO config_runtime_release_history_rows(batch_id,source_release_id,source_release_key,source_profile_id,source_checksum,source_digest,source_state,source_created_by,source_published_by,source_created_at,source_validated_at,source_published_at,source_based_on_release_id,source_rollback_of_release_id,outcome,reason_code,read_only) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,TRUE)`, batchID, item.ID, item.ReleaseKey, item.ProfileID, item.Checksum, rows[i].SourceDigest, item.Status, item.CreatedBy, item.PublishedBy, item.CreatedAt.UTC(), item.ValidatedAt, item.PublishedAt, item.BasedOnReleaseID, item.RollbackOfReleaseID, rows[i].Outcome, rows[i].Reason); err != nil {
			return result{}, errors.New("write historical import")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return result{}, errors.New("finish historical import")
	}
	return out, nil
}
func verify(ctx context.Context, pool *pgxpool.Pool, s snapshot, manifestDigest [32]byte) (result, error) {
	if pool == nil {
		return result{}, errors.New("target database is unavailable")
	}
	if err := validate(s); err != nil {
		return result{}, err
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return result{}, errors.New("begin history reconciliation")
	}
	defer tx.Rollback(ctx)
	var batchID int64
	var stored []byte
	var input, pending, excluded int
	err = tx.QueryRow(ctx, `SELECT id,manifest_digest,input_count,pending_count,excluded_count FROM config_runtime_release_history_batches WHERE source_system=$1 AND source_revision=$2 FOR UPDATE`, s.Manifest.SourceSystem, s.Manifest.SourceRevision).Scan(&batchID, &stored, &input, &pending, &excluded)
	if errors.Is(err, pgx.ErrNoRows) {
		return result{}, errors.New("historical import missing")
	}
	if err != nil || !bytes.Equal(stored, manifestDigest[:]) || input != len(s.Releases) {
		return result{}, errors.New("historical import drift")
	}
	out := result{Input: input, Pending: pending, Excluded: excluded}
	if pending+excluded != input {
		return result{}, errors.New("historical import drift")
	}
	for _, item := range s.Releases {
		expected, e := classify(item)
		if e != nil {
			return result{}, e
		}
		var digest []byte
		var releaseKey, profileID, checksum, sourceState, createdBy, publishedBy, outcome, reason string
		var createdAt time.Time
		var validatedAt, publishedAt *time.Time
		var basedOn, rollbackOf *int64
		var readonly bool
		e = tx.QueryRow(ctx, `SELECT source_release_key,source_profile_id,source_checksum,source_digest,source_state,source_created_by,source_published_by,source_created_at,source_validated_at,source_published_at,source_based_on_release_id,source_rollback_of_release_id,outcome,reason_code,read_only FROM config_runtime_release_history_rows WHERE batch_id=$1 AND source_release_id=$2`, batchID, item.ID).Scan(&releaseKey, &profileID, &checksum, &digest, &sourceState, &createdBy, &publishedBy, &createdAt, &validatedAt, &publishedAt, &basedOn, &rollbackOf, &outcome, &reason, &readonly)
		if e != nil || releaseKey != item.ReleaseKey || profileID != item.ProfileID || checksum != item.Checksum || !bytes.Equal(digest, expected.SourceDigest) || sourceState != item.Status || createdBy != item.CreatedBy || publishedBy != item.PublishedBy || !createdAt.Equal(item.CreatedAt.UTC()) || !sameOptionalTime(validatedAt, item.ValidatedAt) || !sameOptionalTime(publishedAt, item.PublishedAt) || !sameOptionalInt64(basedOn, item.BasedOnReleaseID) || !sameOptionalInt64(rollbackOf, item.RollbackOfReleaseID) || outcome != expected.Outcome || reason != expected.Reason || !readonly {
			return result{}, errors.New("historical target drift")
		}
	}
	var rows int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM config_runtime_release_history_rows WHERE batch_id=$1`, batchID).Scan(&rows); err != nil || rows != len(s.Releases) {
		return result{}, errors.New("historical import drift")
	}
	if _, err = tx.Exec(ctx, `UPDATE config_runtime_release_history_batches SET status='reconciled',reconciled_at=clock_timestamp() WHERE id=$1`, batchID); err != nil {
		return result{}, errors.New("finish history reconciliation")
	}
	if err = tx.Commit(ctx); err != nil {
		return result{}, errors.New("finish history reconciliation")
	}
	return out, nil
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(right.UTC())
}
func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func readKey(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("snapshot key must be a regular 0600 file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read snapshot key")
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid snapshot key")
	}
	return key, nil
}
func sealToFile(s snapshot, path, keyPath string) ([32]byte, error) {
	raw, digest, err := canonical(s)
	if err != nil {
		return digest, err
	}
	key, err := readKey(keyPath)
	if err != nil {
		return digest, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return digest, errors.New("initialize snapshot encryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return digest, errors.New("initialize snapshot encryption")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return digest, errors.New("initialize snapshot encryption")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return digest, errors.New("create protected snapshot")
	}
	defer file.Close()
	sealed := append(nonce, aead.Seal(nil, nonce, raw, []byte(schemaVersion))...)
	if _, err = file.Write(sealed); err != nil {
		return digest, errors.New("write protected snapshot")
	}
	if err = file.Sync(); err != nil {
		return digest, errors.New("write protected snapshot")
	}
	return digest, nil
}
func loadFile(path, keyPath string) (snapshot, [32]byte, error) {
	key, err := readKey(keyPath)
	if err != nil {
		return snapshot{}, [32]byte{}, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return snapshot{}, [32]byte{}, errors.New("protected snapshot must be a regular 0600 file")
	}
	sealed, err := os.ReadFile(path)
	if err != nil {
		return snapshot{}, [32]byte{}, errors.New("read protected snapshot")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return snapshot{}, [32]byte{}, errors.New("open protected snapshot")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < aead.NonceSize() {
		return snapshot{}, [32]byte{}, errors.New("open protected snapshot")
	}
	raw, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(schemaVersion))
	if err != nil {
		return snapshot{}, [32]byte{}, errors.New("open protected snapshot")
	}
	var s snapshot
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&s); err != nil || !errors.Is(dec.Decode(&struct{}{}), io.EOF) {
		return snapshot{}, [32]byte{}, errors.New("invalid protected snapshot")
	}
	_, digest, err := canonical(s)
	return s, digest, err
}
func writeSummary(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }
