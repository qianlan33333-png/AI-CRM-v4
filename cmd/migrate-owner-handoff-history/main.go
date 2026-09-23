// Command migrate-owner-handoff-history imports frozen legacy owner-migration
// result rows as Customer-owned historical facts. It has no Provider client,
// no EER acceptance path, and never writes customer_local_owners.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/customer"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const (
	historySourceSystem = "ai-crm-production:150.158.82.186/openclaw_wecom"
	historyMarker       = "__AICRM_OWNER_HANDOFF_HISTORY__|"
	historyRowMarker    = "__AICRM_OWNER_HANDOFF_HISTORY_ROW__|"
	historySnapshotV1   = 1
)

type options struct {
	mode, snapshot, sourceStream, digest string
	confirm                              bool
}

type manifest struct {
	SchemaVersion int         `json:"schema_version"`
	RunKey        string      `json:"run_key"`
	SourceSystem  string      `json:"source_system"`
	CapturedAt    time.Time   `json:"captured_at"`
	Rows          []sourceRow `json:"rows"`
}

type sourceRow struct {
	SourceBatchID     string    `json:"source_batch_id"`
	SourceLineID      string    `json:"source_line_id"`
	Mode              string    `json:"mode"`
	SourceState       string    `json:"source_state"`
	OccurredAt        time.Time `json:"occurred_at"`
	CorpScope         string    `json:"corp_scope"`
	ExternalUserID    string    `json:"external_userid"`
	SourceOwnerUserID string    `json:"source_owner_userid"`
	TargetOwnerUserID string    `json:"target_owner_userid"`
	WeComStatus       string    `json:"wecom_status"`
	CRMStatus         string    `json:"crm_status"`
}

type sealedSnapshot struct {
	Version    int    `json:"version"`
	RunKey     string `json:"run_key"`
	Ciphertext string `json:"ciphertext"`
}

type result struct{ Input, Observed, Pending, Conflict, Invalid, Replayed, SourceBatches, EmptyBatches int64 }

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "owner handoff history migration failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-owner-handoff-history", flag.ContinueOnError)
	var cfg options
	flags.StringVar(&cfg.mode, "mode", "inspect", "inspect-stream|inspect|dry-run|apply|verify")
	flags.StringVar(&cfg.snapshot, "snapshot", "", "protected owner-handoff history snapshot")
	flags.StringVar(&cfg.sourceStream, "source-stream", "", "read-only legacy owner migration result stream")
	flags.StringVar(&cfg.digest, "manifest-sha256", "", "exact protected snapshot SHA-256")
	flags.BoolVar(&cfg.confirm, "confirm-apply", false, "confirm exact apply")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if cfg.mode == "inspect-stream" {
		if cfg.snapshot == "" || cfg.sourceStream == "" {
			return errors.New("inspect-stream requires snapshot and source-stream")
		}
		m, err := extractStream(cfg.sourceStream)
		if err != nil {
			return err
		}
		digest, err := save(cfg.snapshot, m)
		if err != nil {
			return err
		}
		return printSummary("inspect-stream", m, digest, result{Input: int64(len(m.Rows))})
	}
	if cfg.snapshot == "" {
		return errors.New("snapshot is required")
	}
	m, digest, err := load(cfg.snapshot)
	if err != nil {
		return err
	}
	if cfg.mode == "inspect" || cfg.mode == "dry-run" {
		return printSummary(cfg.mode, m, digest, result{Input: int64(len(m.Rows))})
	}
	if cfg.digest != hex.EncodeToString(digest[:]) {
		return errors.New("manifest digest confirmation mismatch")
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 8, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.mode == "verify" {
		out, verifyErr := verify(ctx, pool, m, digest)
		if verifyErr != nil {
			return verifyErr
		}
		return printSummary("verify", m, digest, out)
	}
	if cfg.mode != "apply" || !cfg.confirm {
		return errors.New("apply requires --confirm-apply")
	}
	out, applyErr := apply(ctx, pool, m, digest)
	if applyErr != nil {
		return applyErr
	}
	return printSummary("apply", m, digest, out)
}

func printSummary(mode string, m manifest, digest [32]byte, out result) error {
	if out.SourceBatches == 0 {
		seen := map[string]bool{}
		for _, row := range m.Rows {
			seen[row.SourceBatchID] = true
			if row.SourceState == "empty_batch" {
				out.EmptyBatches++
			}
		}
		out.SourceBatches = int64(len(seen))
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"mode": mode, "run_key": m.RunKey, "manifest_sha256": hex.EncodeToString(digest[:]),
		"input": out.Input, "source_batches": out.SourceBatches, "empty_batches": out.EmptyBatches, "observed": out.Observed, "pending_mapping": out.Pending,
		"conflict": out.Conflict, "invalid": out.Invalid, "replayed": out.Replayed,
		"provider_calls": 0, "external_effects": 0, "local_owner_updates": 0, "oneid_links_created": 0,
	})
}

func extractStream(path string) (manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return manifest{}, err
	}
	defer file.Close()
	m := manifest{SchemaVersion: historySnapshotV1, SourceSystem: historySourceSystem, Rows: []sourceRow{}}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	markers := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, historyMarker):
			if markers != 0 {
				return manifest{}, errors.New("duplicate owner handoff history marker")
			}
			captured, parseErr := time.Parse(time.RFC3339Nano, strings.TrimPrefix(line, historyMarker))
			if parseErr != nil {
				return manifest{}, errors.New("invalid owner handoff history marker")
			}
			m.CapturedAt, markers = captured.UTC(), 1
		case strings.HasPrefix(line, historyRowMarker):
			raw, decodeErr := hex.DecodeString(strings.TrimPrefix(line, historyRowMarker))
			if decodeErr != nil || !json.Valid(raw) {
				return manifest{}, errors.New("invalid owner handoff history source row")
			}
			var row sourceRow
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&row) != nil {
				return manifest{}, errors.New("invalid owner handoff history source row")
			}
			m.Rows = append(m.Rows, row)
		}
	}
	if err = scanner.Err(); err != nil {
		return manifest{}, err
	}
	if markers != 1 {
		return manifest{}, errors.New("owner handoff history marker unavailable")
	}
	digest := sha256.Sum256([]byte("owner-handoff-history\x00" + m.CapturedAt.Format(time.RFC3339Nano)))
	m.RunKey = "owner-handoff-history-" + hex.EncodeToString(digest[:12])
	if err = validate(m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

func cipherFromEnvironment() (*customer.OwnerHandoffCipher, error) {
	// Owner handoff already uses the existing protected Survey data-key domain.
	// Loading it through platform/config keeps this offline tool on the same key
	// encoding/validation path as Composition without introducing an Owner key.
	cfg, err := platformconfig.Load()
	if err != nil {
		return nil, err
	}
	return customer.NewOwnerHandoffCipher(cfg.Survey.DataKey)
}

func save(path string, m manifest) ([32]byte, error) {
	if filepath.Clean(path) != path {
		return [32]byte{}, errors.New("snapshot path must be clean")
	}
	plain, err := json.Marshal(m)
	if err != nil {
		return [32]byte{}, err
	}
	cipher, err := cipherFromEnvironment()
	if err != nil {
		return [32]byte{}, err
	}
	encrypted, err := cipher.Seal(m.RunKey, 0, "history_snapshot", string(plain))
	if err != nil {
		return [32]byte{}, err
	}
	raw, err := json.Marshal(sealedSnapshot{Version: historySnapshotV1, RunKey: m.RunKey, Ciphertext: base64.RawStdEncoding.EncodeToString(encrypted)})
	if err != nil {
		return [32]byte{}, err
	}
	raw = append(raw, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return [32]byte{}, err
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return [32]byte{}, err
	}
	if closeErr != nil {
		return [32]byte{}, closeErr
	}
	return sha256.Sum256(raw), nil
}

func load(path string) (manifest, [32]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, [32]byte{}, err
	}
	if len(raw) == 0 || len(raw) > 512<<20 {
		return manifest{}, [32]byte{}, errors.New("invalid protected snapshot size")
	}
	var sealed sealedSnapshot
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&sealed) != nil || sealed.Version != historySnapshotV1 || sealed.RunKey == "" {
		return manifest{}, [32]byte{}, errors.New("invalid protected snapshot")
	}
	encrypted, err := base64.RawStdEncoding.DecodeString(sealed.Ciphertext)
	if err != nil {
		return manifest{}, [32]byte{}, errors.New("invalid protected snapshot")
	}
	cipher, err := cipherFromEnvironment()
	if err != nil {
		return manifest{}, [32]byte{}, err
	}
	plain, err := cipher.Open(sealed.RunKey, 0, "history_snapshot", encrypted)
	if err != nil {
		return manifest{}, [32]byte{}, errors.New("protected snapshot unavailable")
	}
	var m manifest
	decoder = json.NewDecoder(strings.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || m.RunKey != sealed.RunKey {
		return manifest{}, [32]byte{}, errors.New("invalid protected snapshot payload")
	}
	if err = validate(m); err != nil {
		return manifest{}, [32]byte{}, err
	}
	return m, sha256.Sum256(raw), nil
}

var runKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,200}$`)
var sourceKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,200}$`)

func validate(m manifest) error {
	if m.SchemaVersion != historySnapshotV1 || !runKeyPattern.MatchString(m.RunKey) || m.SourceSystem != historySourceSystem || m.CapturedAt.IsZero() || len(m.Rows) > 2_000_000 {
		return errors.New("invalid owner handoff history manifest")
	}
	seen := map[string]bool{}
	for _, row := range m.Rows {
		key := row.SourceBatchID + "\x00" + row.SourceLineID
		missingSubject := strings.TrimSpace(row.ExternalUserID) == "" || strings.TrimSpace(row.SourceOwnerUserID) == "" || strings.TrimSpace(row.TargetOwnerUserID) == ""
		if seen[key] || !sourceKeyPattern.MatchString(row.SourceBatchID) || !sourceKeyPattern.MatchString(row.SourceLineID) || (row.Mode != "local_only" && row.Mode != "wecom_then_crm") || strings.TrimSpace(row.SourceState) == "" || len(row.SourceState) > 128 || row.OccurredAt.IsZero() || !strings.HasPrefix(row.CorpScope, "wecom-corp:") || (missingSubject && row.SourceState != "invalid_source" && row.SourceState != "empty_batch") || len(row.ExternalUserID) > 128 || len(row.SourceOwnerUserID) > 128 || len(row.TargetOwnerUserID) > 128 || len(row.WeComStatus) > 128 || len(row.CRMStatus) > 128 {
			return errors.New("invalid owner handoff history row")
		}
		seen[key] = true
	}
	return nil
}

func rowDigest(row sourceRow) [32]byte { raw, _ := json.Marshal(row); return sha256.Sum256(raw) }
func resultDigest(row sourceRow) [32]byte {
	raw, _ := json.Marshal(struct{ State, WeCom, CRM string }{row.SourceState, row.WeComStatus, row.CRMStatus})
	return sha256.Sum256(raw)
}
func stringDigest(value string) [32]byte { return sha256.Sum256([]byte(value)) }

type resolution struct {
	state                                    string
	customerID, sourceStaffID, targetStaffID int64
	digest                                   [32]byte
}

func resolveRow(ctx context.Context, oneID identityport.Resolver, users interface {
	UserByWeComUserID(context.Context, string, bool) (accessdomain.User, error)
}, row sourceRow) (resolution, error) {
	if row.SourceState == "invalid_source" || row.SourceState == "empty_batch" {
		return resolution{state: "invalid", digest: resolutionDigest("invalid", 0, 0, 0)}, nil
	}
	resolved, err := oneID.Resolve(ctx, identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: row.CorpScope, Value: row.ExternalUserID, Assurance: identitydomain.AssuranceVerified, Source: "owner_handoff_history"})
	if err != nil {
		return resolution{}, err
	}
	out := resolution{}
	switch resolved.Status {
	case identityport.ResolveFound:
		out.customerID = int64(resolved.CustomerID)
	case identityport.ResolveConflict:
		out.state = "conflict"
	default:
		out.state = "pending_mapping"
	}
	source, sourceErr := users.UserByWeComUserID(ctx, row.SourceOwnerUserID, false)
	target, targetErr := users.UserByWeComUserID(ctx, row.TargetOwnerUserID, false)
	if out.state == "" && (errors.Is(sourceErr, accessdomain.ErrNotFound) || errors.Is(targetErr, accessdomain.ErrNotFound)) {
		out.state = "pending_mapping"
	}
	if sourceErr != nil && !errors.Is(sourceErr, accessdomain.ErrNotFound) {
		return resolution{}, sourceErr
	}
	if targetErr != nil && !errors.Is(targetErr, accessdomain.ErrNotFound) {
		return resolution{}, targetErr
	}
	if sourceErr == nil {
		out.sourceStaffID = source.ID
	}
	if targetErr == nil {
		out.targetStaffID = target.ID
	}
	if out.state == "" {
		out.state = "observed"
	}
	out.digest = resolutionDigest(out.state, out.customerID, out.sourceStaffID, out.targetStaffID)
	return out, nil
}

func resolutionDigest(state string, customerID, sourceStaffID, targetStaffID int64) [32]byte {
	raw, _ := json.Marshal(struct {
		State                    string
		Customer, Source, Target int64
	}{state, customerID, sourceStaffID, targetStaffID})
	return sha256.Sum256(raw)
}

func apply(ctx context.Context, pool *platformpostgres.Pool, m manifest, snapshotDigest [32]byte) (result, error) {
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return result{}, err
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	users := accessstore.NewPostgreSQL()
	out := result{Input: int64(len(m.Rows))}
	err = uow.Within(ctx, func(txctx context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		var existing []byte
		err := tx.QueryRow(txctx, `INSERT INTO customer_owner_handoff_history_runs(run_key,snapshot_digest,source_system,input_count,status) VALUES($1,$2,$3,$4,'applied') ON CONFLICT(run_key) DO UPDATE SET run_key=EXCLUDED.run_key RETURNING snapshot_digest`, m.RunKey, snapshotDigest[:], m.SourceSystem, len(m.Rows)).Scan(&existing)
		if err != nil {
			return err
		}
		if string(existing) != string(snapshotDigest[:]) {
			return errors.New("history run snapshot drift")
		}
		for _, row := range m.Rows {
			digest := rowDigest(row)
			var saved []byte
			err = tx.QueryRow(txctx, `SELECT source_digest FROM customer_owner_handoff_history_imports WHERE source_batch_id=$1 AND source_line_id=$2 FOR UPDATE`, row.SourceBatchID, row.SourceLineID).Scan(&saved)
			if err == nil {
				if string(saved) != string(digest[:]) {
					return errors.New("history source row drift")
				}
				out.Replayed++
				continue
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			resolved, resolveErr := resolveRow(txctx, oneID, users, row)
			if resolveErr != nil {
				return resolveErr
			}
			subject, sourceRef, targetRef, resultFact := stringDigest(row.ExternalUserID), stringDigest(row.SourceOwnerUserID), stringDigest(row.TargetOwnerUserID), resultDigest(row)
			var customer, sourceStaff, targetStaff any
			if resolved.customerID > 0 {
				customer = resolved.customerID
			}
			if resolved.sourceStaffID > 0 {
				sourceStaff = resolved.sourceStaffID
			}
			if resolved.targetStaffID > 0 {
				targetStaff = resolved.targetStaffID
			}
			_, err = tx.Exec(txctx, `INSERT INTO customer_owner_handoff_history_imports(run_key,source_batch_id,source_line_id,mode,source_state,source_occurred_at,source_digest,source_subject_digest,customer_id,source_staff_id,target_staff_id,source_staff_ref_digest,target_staff_ref_digest,source_result_digest,resolution_digest,imported_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, m.RunKey, row.SourceBatchID, row.SourceLineID, row.Mode, row.SourceState, row.OccurredAt, digest[:], subject[:], customer, sourceStaff, targetStaff, sourceRef[:], targetRef[:], resultFact[:], resolved.digest[:], resolved.state)
			if err != nil {
				return err
			}
			switch resolved.state {
			case "observed":
				out.Observed++
			case "pending_mapping":
				out.Pending++
			case "conflict":
				out.Conflict++
			default:
				out.Invalid++
			}
		}
		return nil
	})
	return out, err
}

func verify(ctx context.Context, pool *platformpostgres.Pool, m manifest, snapshotDigest [32]byte) (result, error) {
	tx, err := pool.Native().Begin(ctx)
	if err != nil {
		return result{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var saved []byte
	var status string
	var input int64
	if err = tx.QueryRow(ctx, `SELECT snapshot_digest,status,input_count FROM customer_owner_handoff_history_runs WHERE run_key=$1 FOR UPDATE`, m.RunKey).Scan(&saved, &status, &input); err != nil {
		return result{}, err
	}
	if string(saved) != string(snapshotDigest[:]) || input != int64(len(m.Rows)) || (status != "applied" && status != "reconciled") {
		return result{}, errors.New("history run reconciliation mismatch")
	}
	out := result{Input: int64(len(m.Rows))}
	for _, row := range m.Rows {
		digest := rowDigest(row)
		subject, sourceRef, targetRef, resultFact := stringDigest(row.ExternalUserID), stringDigest(row.SourceOwnerUserID), stringDigest(row.TargetOwnerUserID), resultDigest(row)
		var sourceDigest, subjectDigest, sourceStaffDigest, targetStaffDigest, savedResult, savedResolution []byte
		var state, mode, sourceState string
		var occurredAt time.Time
		var customerID, sourceStaffID, targetStaffID *int64
		err = tx.QueryRow(ctx, `SELECT source_digest,source_subject_digest,source_staff_ref_digest,target_staff_ref_digest,source_result_digest,resolution_digest,imported_state,mode,source_state,source_occurred_at,customer_id,source_staff_id,target_staff_id FROM customer_owner_handoff_history_imports WHERE source_batch_id=$1 AND source_line_id=$2`, row.SourceBatchID, row.SourceLineID).Scan(&sourceDigest, &subjectDigest, &sourceStaffDigest, &targetStaffDigest, &savedResult, &savedResolution, &state, &mode, &sourceState, &occurredAt, &customerID, &sourceStaffID, &targetStaffID)
		if err != nil {
			return result{}, err
		}
		var customerValue, sourceValue, targetValue int64
		if customerID != nil {
			customerValue = *customerID
		}
		if sourceStaffID != nil {
			sourceValue = *sourceStaffID
		}
		if targetStaffID != nil {
			targetValue = *targetStaffID
		}
		wantResolution := resolutionDigest(state, customerValue, sourceValue, targetValue)
		if string(sourceDigest) != string(digest[:]) || string(subjectDigest) != string(subject[:]) || string(sourceStaffDigest) != string(sourceRef[:]) || string(targetStaffDigest) != string(targetRef[:]) || string(savedResult) != string(resultFact[:]) || string(savedResolution) != string(wantResolution[:]) || mode != row.Mode || sourceState != row.SourceState || !occurredAt.Equal(row.OccurredAt) {
			return result{}, errors.New("history ledger drift")
		}
		switch state {
		case "observed":
			out.Observed++
		case "pending_mapping":
			out.Pending++
		case "conflict":
			out.Conflict++
		case "invalid":
			out.Invalid++
		default:
			return result{}, errors.New("history imported state invalid")
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE customer_owner_handoff_history_runs SET status='reconciled',reconciled_at=clock_timestamp() WHERE run_key=$1`, m.RunKey); err != nil {
		return result{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result{}, err
	}
	return out, nil
}

// sortRows is used only by tests and capture callers that need a deterministic
// stream order; row identity itself remains the old batch+line key.
func sortRows(rows []sourceRow) {
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].SourceBatchID+"\x00"+rows[i].SourceLineID < rows[j].SourceBatchID+"\x00"+rows[j].SourceLineID
	})
}
