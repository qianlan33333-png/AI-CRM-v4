// Command migrate-open-platform captures an explicit, read-only auth-platform
// snapshot and imports only inert V3 replacement records. It never reads a
// donor secret hash, credential hint, key, or token; it cannot contact a
// Provider and is never run by installation or application startup.
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

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const (
	historySchemaVersion = "aicrm-open-platform-machine-history-v3"
	historySourceSystem  = "ai-crm"
)

var (
	sourceRevision = regexp.MustCompile(`^[a-f0-9]{40}$`)
	importRunID    = regexp.MustCompile(`^open-platform:[a-f0-9]{32}$`)
)

type historicalManifest struct {
	SchemaVersion  string            `json:"schema_version"`
	SourceSystem   string            `json:"source_system"`
	SourceRevision string            `json:"source_revision"`
	ImportRunID    string            `json:"import_run_id"`
	SnapshotAt     time.Time         `json:"snapshot_at"`
	Counts         map[string]int    `json:"counts"`
	Digests        map[string]string `json:"digests"`
}

type historicalSnapshot struct {
	Manifest historicalManifest    `json:"manifest"`
	Clients  []historicalClientRow `json:"clients"`
	Audits   []historicalAuditRow  `json:"audits"`
}

// historicalClientRow intentionally has no secret_hash, credential_hint, or
// token field. All authorization fields come from auth_api_clients unchanged.
type historicalClientRow struct {
	SourceRowID       string                  `json:"source_row_id"`
	ClientID          string                  `json:"client_id"`
	PrincipalID       string                  `json:"principal_id"`
	PrincipalType     string                  `json:"principal_type"`
	DisplayName       string                  `json:"display_name"`
	Purpose           string                  `json:"purpose"`
	Audiences         []string                `json:"audiences"`
	Scopes            []string                `json:"scopes"`
	Capabilities      []string                `json:"capabilities"`
	AllowedCIDRs      []string                `json:"allowed_cidrs"`
	CorpID            string                  `json:"corp_id"`
	OwnerScope        accessdomain.OwnerScope `json:"owner_scope"`
	SourceEnabled     bool                    `json:"source_enabled"`
	SourceAuthVersion int64                   `json:"source_auth_version"`
	TokenTTLSeconds   int                     `json:"token_ttl_seconds"`
}

// historicalAuditRow retains the action evidence but only digests donor JSON
// payloads. The encrypted snapshot is needed for owner scope; raw audit JSON
// is deliberately never copied into either snapshot or target database.
type historicalAuditRow struct {
	SourceAuditID string    `json:"source_audit_id"`
	Operator      string    `json:"operator"`
	Action        string    `json:"action"`
	TargetType    string    `json:"target_type"`
	TargetID      string    `json:"target_id"`
	BeforeDigest  string    `json:"before_digest"`
	AfterDigest   string    `json:"after_digest"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type importResult struct {
	Imported      int `json:"imported"`
	Excluded      int `json:"excluded"`
	Replayed      int `json:"replayed"`
	AuditImported int `json:"audit_imported"`
	AuditReplayed int `json:"audit_replayed"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate-open-platform", flag.ContinueOnError)
	mode := fs.String("mode", "inspect", "extract|inspect|dry-run|apply|verify")
	snapshotPath := fs.String("snapshot", "", "AES-GCM protected non-secret historical auth snapshot")
	keyPath := fs.String("snapshot-key-file", "", "0600 base64 AES-256 snapshot key")
	revision := fs.String("source-revision", "", "40-character frozen source revision for extract")
	wantDigest := fs.String("manifest-sha256", "", "exact protected snapshot digest confirmation")
	confirmApply := fs.Bool("confirm-apply", false, "confirm inert historical credential write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *mode == "extract" {
		if *snapshotPath == "" || *keyPath == "" || !sourceRevision.MatchString(*revision) {
			return errors.New("extract requires snapshot, snapshot-key-file, and a 40-character source-revision")
		}
		sourceURL, err := platformconfig.SourceDatabaseURL()
		if err != nil {
			return errors.New("source database is unavailable")
		}
		pool, err := pgxpool.New(ctx, sourceURL)
		if err != nil {
			return errors.New("source database is unavailable")
		}
		defer pool.Close()
		snapshot, err := extractSnapshot(ctx, pool, *revision)
		if err != nil {
			return err
		}
		digest, err := sealToFile(snapshot, *snapshotPath, *keyPath)
		if err != nil {
			return err
		}
		return writeSummary(map[string]any{"mode": "extract", "manifest_sha256": hex.EncodeToString(digest[:]), "counts": snapshot.Manifest.Counts, "mapping": "auth_api_clients + admin_operation_logs(api_client); no secret/hash/token"})
	}
	if *snapshotPath == "" || *keyPath == "" {
		return errors.New("snapshot and snapshot-key-file are required")
	}
	snapshot, digest, err := loadFile(*snapshotPath, *keyPath)
	if err != nil {
		return err
	}
	if *mode == "inspect" {
		return writeSummary(map[string]any{"mode": "inspect", "manifest_sha256": hex.EncodeToString(digest[:]), "counts": snapshot.Manifest.Counts, "mapping": "supported grants reissue_required; unsupported grants excluded with reason"})
	}
	if *mode != "dry-run" && *mode != "apply" && *mode != "verify" {
		return errors.New("unknown mode")
	}
	if *wantDigest != hex.EncodeToString(digest[:]) {
		return errors.New("manifest-sha256 confirmation mismatch")
	}
	if *mode == "dry-run" {
		return writeSummary(map[string]any{"mode": "dry-run", "manifest_sha256": hex.EncodeToString(digest[:]), "eligible": true, "counts": snapshot.Manifest.Counts})
	}
	if *mode == "apply" && !*confirmApply {
		return errors.New("apply requires --confirm-apply")
	}
	service, closeService, err := machineHistoryService(ctx)
	if err != nil {
		return err
	}
	defer closeService()
	if *mode == "apply" {
		result, applyErr := applySnapshot(ctx, service, snapshot, digest)
		if applyErr != nil {
			return applyErr
		}
		return writeSummary(map[string]any{"mode": "apply", "manifest_sha256": hex.EncodeToString(digest[:]), "result": result})
	}
	result, verifyErr := verifySnapshot(ctx, service, snapshot, digest)
	if verifyErr != nil {
		return verifyErr
	}
	return writeSummary(map[string]any{"mode": "verify", "manifest_sha256": hex.EncodeToString(digest[:]), "result": result})
}

func extractSnapshot(ctx context.Context, pool *pgxpool.Pool, revision string) (historicalSnapshot, error) {
	if pool == nil || !sourceRevision.MatchString(revision) {
		return historicalSnapshot{}, errors.New("invalid source snapshot request")
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return historicalSnapshot{}, errors.New("begin source snapshot")
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout='15s'"); err != nil {
		return historicalSnapshot{}, errors.New("configure source snapshot")
	}
	var snapshot historicalSnapshot
	if err = tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&snapshot.Manifest.SnapshotAt); err != nil {
		return historicalSnapshot{}, errors.New("read source snapshot time")
	}
	rows, err := tx.Query(ctx, `SELECT client_id,principal_id,principal_type,purpose,display_name,audiences_json,scopes_json,capabilities_json,allowed_cidrs_json,corp_id,owner_scope_json,auth_version,token_ttl_seconds,enabled FROM auth_api_clients ORDER BY client_id`)
	if err != nil {
		return historicalSnapshot{}, errors.New("read source auth clients")
	}
	for rows.Next() {
		var row historicalClientRow
		var audiences, scopes, capabilities, cidrs, owner []byte
		if err = rows.Scan(&row.ClientID, &row.PrincipalID, &row.PrincipalType, &row.Purpose, &row.DisplayName, &audiences, &scopes, &capabilities, &cidrs, &row.CorpID, &owner, &row.SourceAuthVersion, &row.TokenTTLSeconds, &row.SourceEnabled); err != nil {
			rows.Close()
			return historicalSnapshot{}, errors.New("scan source auth client")
		}
		row.SourceRowID = "auth_api_clients/" + row.ClientID
		if row.Audiences, err = decodeStringArray(audiences); err != nil {
			rows.Close()
			return historicalSnapshot{}, errors.New("decode source auth client")
		}
		if row.Scopes, err = decodeStringArray(scopes); err != nil {
			rows.Close()
			return historicalSnapshot{}, errors.New("decode source auth client")
		}
		if row.Capabilities, err = decodeStringArray(capabilities); err != nil {
			rows.Close()
			return historicalSnapshot{}, errors.New("decode source auth client")
		}
		if row.AllowedCIDRs, err = decodeStringArray(cidrs); err != nil {
			rows.Close()
			return historicalSnapshot{}, errors.New("decode source auth client")
		}
		if row.OwnerScope, err = accessdomain.NormalizeOwnerScope(owner); err != nil {
			rows.Close()
			return historicalSnapshot{}, errors.New("decode source auth client")
		}
		snapshot.Clients = append(snapshot.Clients, row)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return historicalSnapshot{}, errors.New("read source auth clients")
	}
	rows.Close()
	audits, err := tx.Query(ctx, `SELECT id,operator,action_type,target_type,target_id,before_json,after_json,created_at FROM admin_operation_logs WHERE target_type='api_client' ORDER BY id`)
	if err != nil {
		return historicalSnapshot{}, errors.New("read source auth audit")
	}
	for audits.Next() {
		var id int64
		var row historicalAuditRow
		var before, after []byte
		if err = audits.Scan(&id, &row.Operator, &row.Action, &row.TargetType, &row.TargetID, &before, &after, &row.OccurredAt); err != nil {
			audits.Close()
			return historicalSnapshot{}, errors.New("scan source auth audit")
		}
		beforeDigest, digestErr := canonicalJSONDigest(before)
		if digestErr != nil {
			audits.Close()
			return historicalSnapshot{}, errors.New("decode source auth audit")
		}
		afterDigest, digestErr := canonicalJSONDigest(after)
		if digestErr != nil {
			audits.Close()
			return historicalSnapshot{}, errors.New("decode source auth audit")
		}
		row.SourceAuditID = fmt.Sprintf("%d", id)
		row.BeforeDigest, row.AfterDigest = hex.EncodeToString(beforeDigest[:]), hex.EncodeToString(afterDigest[:])
		snapshot.Audits = append(snapshot.Audits, row)
	}
	if err = audits.Err(); err != nil {
		audits.Close()
		return historicalSnapshot{}, errors.New("read source auth audit")
	}
	audits.Close()
	if err = tx.Commit(ctx); err != nil {
		return historicalSnapshot{}, errors.New("finish source snapshot")
	}
	if err = populateManifest(&snapshot, revision); err != nil {
		return historicalSnapshot{}, err
	}
	return snapshot, nil
}

func decodeStringArray(raw []byte) ([]string, error) {
	var values []string
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&values); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return nil, errors.New("invalid string array")
	}
	return values, nil
}

func canonicalJSONDigest(raw []byte) ([sha256.Size]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return [sha256.Size]byte{}, errors.New("invalid JSON")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func populateManifest(snapshot *historicalSnapshot, revision string) error {
	if snapshot == nil || !sourceRevision.MatchString(revision) || snapshot.Manifest.SnapshotAt.IsZero() {
		return errors.New("invalid source snapshot")
	}
	runID, err := newImportRunID()
	if err != nil {
		return err
	}
	snapshot.Manifest = historicalManifest{SchemaVersion: historySchemaVersion, SourceSystem: historySourceSystem, SourceRevision: revision, ImportRunID: runID, SnapshotAt: snapshot.Manifest.SnapshotAt.UTC()}
	normalizeSnapshot(snapshot)
	clientRaw, err := json.Marshal(snapshot.Clients)
	if err != nil {
		return errors.New("canonicalize source snapshot")
	}
	auditRaw, err := json.Marshal(snapshot.Audits)
	if err != nil {
		return errors.New("canonicalize source snapshot")
	}
	clientDigest, auditDigest := sha256.Sum256(clientRaw), sha256.Sum256(auditRaw)
	snapshot.Manifest.Counts = map[string]int{"auth_api_clients": len(snapshot.Clients), "admin_operation_logs_api_client": len(snapshot.Audits)}
	snapshot.Manifest.Digests = map[string]string{"auth_api_clients": hex.EncodeToString(clientDigest[:]), "admin_operation_logs_api_client": hex.EncodeToString(auditDigest[:])}
	_, _, err = canonicalSnapshot(*snapshot)
	return err
}

func newImportRunID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "open-platform:" + hex.EncodeToString(raw[:]), nil
}

func canonicalSnapshot(snapshot historicalSnapshot) (historicalSnapshot, [sha256.Size]byte, error) {
	normalizeSnapshot(&snapshot)
	if err := validateSnapshot(snapshot); err != nil {
		return historicalSnapshot{}, [sha256.Size]byte{}, err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return historicalSnapshot{}, [sha256.Size]byte{}, errors.New("canonicalize source snapshot")
	}
	return snapshot, sha256.Sum256(raw), nil
}

func normalizeSnapshot(snapshot *historicalSnapshot) {
	if snapshot == nil {
		return
	}
	manifest := &snapshot.Manifest
	manifest.SchemaVersion = strings.TrimSpace(manifest.SchemaVersion)
	manifest.SourceSystem = strings.TrimSpace(manifest.SourceSystem)
	manifest.SourceRevision = strings.TrimSpace(manifest.SourceRevision)
	manifest.ImportRunID = strings.TrimSpace(manifest.ImportRunID)
	manifest.SnapshotAt = manifest.SnapshotAt.UTC()
	for index := range snapshot.Clients {
		row := &snapshot.Clients[index]
		row.SourceRowID, row.ClientID, row.PrincipalID, row.PrincipalType = strings.TrimSpace(row.SourceRowID), strings.TrimSpace(row.ClientID), strings.TrimSpace(row.PrincipalID), strings.TrimSpace(row.PrincipalType)
		row.DisplayName, row.Purpose, row.CorpID = strings.TrimSpace(row.DisplayName), strings.TrimSpace(row.Purpose), strings.TrimSpace(row.CorpID)
		row.Audiences, row.Scopes, row.Capabilities, row.AllowedCIDRs = canonicalStrings(row.Audiences), canonicalStrings(row.Scopes), canonicalStrings(row.Capabilities), canonicalStrings(row.AllowedCIDRs)
		if scope, err := accessdomain.NormalizeOwnerScope(row.OwnerScope.JSON()); err == nil {
			row.OwnerScope = scope
		}
	}
	sort.Slice(snapshot.Clients, func(i, j int) bool { return snapshot.Clients[i].SourceRowID < snapshot.Clients[j].SourceRowID })
	for index := range snapshot.Audits {
		row := &snapshot.Audits[index]
		row.SourceAuditID, row.Operator, row.Action, row.TargetType, row.TargetID = strings.TrimSpace(row.SourceAuditID), strings.TrimSpace(row.Operator), strings.TrimSpace(row.Action), strings.TrimSpace(row.TargetType), strings.TrimSpace(row.TargetID)
		row.BeforeDigest, row.AfterDigest, row.OccurredAt = strings.TrimSpace(row.BeforeDigest), strings.TrimSpace(row.AfterDigest), row.OccurredAt.UTC()
	}
	sort.Slice(snapshot.Audits, func(i, j int) bool {
		left, _ := parseAuditID(snapshot.Audits[i].SourceAuditID)
		right, _ := parseAuditID(snapshot.Audits[j].SourceAuditID)
		return left < right
	})
}

func canonicalStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func validateSnapshot(snapshot historicalSnapshot) error {
	manifest := snapshot.Manifest
	if manifest.SchemaVersion != historySchemaVersion || manifest.SourceSystem != historySourceSystem || !sourceRevision.MatchString(manifest.SourceRevision) || !importRunID.MatchString(manifest.ImportRunID) || manifest.SnapshotAt.IsZero() || len(snapshot.Clients) > 100000 || len(snapshot.Audits) > 1000000 || len(manifest.Counts) != 2 || len(manifest.Digests) != 2 || manifest.Counts["auth_api_clients"] != len(snapshot.Clients) || manifest.Counts["admin_operation_logs_api_client"] != len(snapshot.Audits) {
		return errors.New("invalid protected source snapshot")
	}
	clientRaw, err := json.Marshal(snapshot.Clients)
	if err != nil {
		return errors.New("invalid protected source snapshot")
	}
	auditRaw, err := json.Marshal(snapshot.Audits)
	if err != nil {
		return errors.New("invalid protected source snapshot")
	}
	clientDigest, auditDigest := sha256.Sum256(clientRaw), sha256.Sum256(auditRaw)
	if manifest.Digests["auth_api_clients"] != hex.EncodeToString(clientDigest[:]) || manifest.Digests["admin_operation_logs_api_client"] != hex.EncodeToString(auditDigest[:]) {
		return errors.New("invalid protected source snapshot")
	}
	seenClients := map[string]struct{}{}
	for _, row := range snapshot.Clients {
		if row.SourceRowID != "auth_api_clients/"+row.ClientID || row.ClientID == "" || len(row.SourceRowID) > 240 || len(row.ClientID) > 120 || len(row.PrincipalID) > 240 || len(row.PrincipalType) > 80 || len(row.DisplayName) > 160 || len(row.Purpose) > 120 || len(row.CorpID) > 256 || row.SourceAuthVersion < 1 || row.TokenTTLSeconds < 60 || row.TokenTTLSeconds > 3600 || strings.IndexFunc(row.CorpID, func(r rune) bool { return r == 0 }) >= 0 {
			return errors.New("invalid protected source client")
		}
		if _, exists := seenClients[row.SourceRowID]; exists {
			return errors.New("duplicate protected source client")
		}
		seenClients[row.SourceRowID] = struct{}{}
		if _, err := accessdomain.NormalizeOwnerScope(row.OwnerScope.JSON()); err != nil {
			return errors.New("invalid protected source client")
		}
	}
	seenAudits := map[int64]struct{}{}
	for _, row := range snapshot.Audits {
		id, err := parseAuditID(row.SourceAuditID)
		if err != nil || row.TargetType != "api_client" || len(row.Operator) > 240 || len(row.Action) > 240 || len(row.TargetID) > 240 || row.OccurredAt.IsZero() || !validDigestHex(row.BeforeDigest) || !validDigestHex(row.AfterDigest) {
			return errors.New("invalid protected source audit")
		}
		if _, exists := seenAudits[id]; exists {
			return errors.New("duplicate protected source audit")
		}
		seenAudits[id] = struct{}{}
	}
	return nil
}

func parseAuditID(raw string) (int64, error) {
	var value int64
	if _, err := fmt.Sscan(raw, &value); err != nil || value < 1 || fmt.Sprintf("%d", value) != raw {
		return 0, errors.New("invalid source audit id")
	}
	return value, nil
}

func validDigestHex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func applySnapshot(ctx context.Context, service *accessapp.MachineService, snapshot historicalSnapshot, digest [sha256.Size]byte) (importResult, error) {
	batch := accessport.HistoricalMachineImportBatch{ImportRunID: snapshot.Manifest.ImportRunID, SourceSystem: snapshot.Manifest.SourceSystem, SourceRevision: snapshot.Manifest.SourceRevision, ManifestDigest: digest, SnapshotAt: snapshot.Manifest.SnapshotAt, ClientCount: len(snapshot.Clients), AuditCount: len(snapshot.Audits)}
	if _, err := service.BeginHistoricalImport(ctx, batch); err != nil {
		return importResult{}, err
	}
	result := importResult{}
	for _, row := range snapshot.Clients {
		input, err := clientInput(snapshot.Manifest.ImportRunID, row)
		if err != nil {
			return importResult{}, err
		}
		out, err := service.ImportHistorical(ctx, input)
		if err != nil {
			return importResult{}, err
		}
		switch out.Outcome {
		case "reissue_required":
			result.Imported++
		case "excluded":
			result.Excluded++
		case "replayed":
			result.Replayed++
		default:
			return importResult{}, errors.New("invalid historical client outcome")
		}
	}
	for _, row := range snapshot.Audits {
		input, err := auditInput(snapshot.Manifest.ImportRunID, row)
		if err != nil {
			return importResult{}, err
		}
		out, err := service.ImportHistoricalAudit(ctx, input)
		if err != nil {
			return importResult{}, err
		}
		if out.Replayed {
			result.AuditReplayed++
		} else {
			result.AuditImported++
		}
	}
	return result, nil
}

func verifySnapshot(ctx context.Context, service *accessapp.MachineService, snapshot historicalSnapshot, digest [sha256.Size]byte) (importResult, error) {
	batch := accessport.HistoricalMachineImportBatch{ImportRunID: snapshot.Manifest.ImportRunID, SourceSystem: snapshot.Manifest.SourceSystem, SourceRevision: snapshot.Manifest.SourceRevision, ManifestDigest: digest, SnapshotAt: snapshot.Manifest.SnapshotAt, ClientCount: len(snapshot.Clients), AuditCount: len(snapshot.Audits)}
	if err := service.VerifyHistoricalImport(ctx, batch); err != nil {
		return importResult{}, err
	}
	result := importResult{}
	for _, row := range snapshot.Clients {
		input, err := clientInput(snapshot.Manifest.ImportRunID, row)
		if err != nil {
			return importResult{}, err
		}
		out, err := service.VerifyHistorical(ctx, input)
		if err != nil {
			return importResult{}, err
		}
		if out.Outcome == "reissue_required" {
			result.Imported++
		} else if out.Outcome == "excluded" {
			result.Excluded++
		} else {
			return importResult{}, errors.New("historical client verification failed")
		}
	}
	for _, row := range snapshot.Audits {
		input, err := auditInput(snapshot.Manifest.ImportRunID, row)
		if err != nil {
			return importResult{}, err
		}
		if err = service.VerifyHistoricalAudit(ctx, input); err != nil {
			return importResult{}, err
		}
		result.AuditImported++
	}
	return result, nil
}

const (
	historyClientSourceScope = "auth_api_clients"
	historyAuditSourceScope  = "admin_operation_logs:api_client"
	legacyDirectClientID     = "aicrm-direct-external-api-key"
)

func clientInput(runID string, source historicalClientRow) (accessport.HistoricalMachineImportInput, error) {
	digest, err := clientRowDigest(source)
	if err != nil {
		return accessport.HistoricalMachineImportInput{}, err
	}
	ownerScopeDigest := sha256.Sum256(source.OwnerScope.JSON())
	target := mappedHistoricalClient(source)
	return accessport.HistoricalMachineImportInput{ImportRunID: runID, SourceSystem: historySourceSystem, SourceScope: historyClientSourceScope, SourceRowID: source.SourceRowID, SourceClientID: source.ClientID, SourceRowDigest: digest, SourceOwnerScopeDigest: ownerScopeDigest, OwnerScopeMappingStatus: historicalOwnerScopeMappingStatus(source.OwnerScope), ClientID: target.ClientID, PrincipalID: source.PrincipalID, PrincipalType: source.PrincipalType, DisplayName: target.DisplayName, Purpose: target.Purpose, Audiences: target.Audiences, Scopes: target.Scopes, Capabilities: target.Capabilities, AllowedCIDRs: target.AllowedCIDRs, CorpID: target.CorpID, OwnerScope: target.OwnerScope, SourceEnabled: source.SourceEnabled, SourceAuthVersion: source.SourceAuthVersion, TokenTTLSeconds: target.TokenTTLSeconds}, nil
}

// mappedHistoricalClient is the only donor identifier translation. The legacy
// direct key was a fixed, read-only credential, so it becomes V3's fixed
// direct-key record and stays disabled/reissue-required. Its source ID remains
// in the global receipt; no old secret or verifier crosses this boundary.
func mappedHistoricalClient(source historicalClientRow) historicalClientRow {
	if source.ClientID != legacyDirectClientID {
		return source
	}
	if source.Purpose != "external_agent" || source.PrincipalType != "api_client" || !sameStrings(source.Audiences, []string{"external_integration"}) || !sameStrings(source.Scopes, []string{"read"}) || !sameStrings(source.Capabilities, []string{"external_read"}) {
		// Preserve an invalid donor fact for exclusion rather than presenting it
		// as a normal external-api client or broadening the direct-key boundary.
		source.Purpose = "legacy_direct_mapping_invalid"
		return source
	}
	source.ClientID = accessapp.DirectExternalAPIKeyClientID
	source.Purpose = "direct_api_key"
	return source
}

func historicalOwnerScopeMappingStatus(scope accessdomain.OwnerScope) string {
	if len(scope) == 0 {
		return "not_required"
	}
	if _, localCustomerID := scope["customer_id"]; localCustomerID {
		return "pending"
	}
	for key := range scope {
		if key != "owner_userid" && key != "external_userid" {
			return "pending"
		}
	}
	return "mapped"
}

func sameStrings(left, right []string) bool {
	left, right = canonicalStrings(left), canonicalStrings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func auditInput(runID string, row historicalAuditRow) (accessport.HistoricalMachineAuditInput, error) {
	id, err := parseAuditID(row.SourceAuditID)
	if err != nil {
		return accessport.HistoricalMachineAuditInput{}, err
	}
	sourceDigest, err := auditRowDigest(row)
	if err != nil {
		return accessport.HistoricalMachineAuditInput{}, err
	}
	before, err := decodeDigest(row.BeforeDigest)
	if err != nil {
		return accessport.HistoricalMachineAuditInput{}, err
	}
	after, err := decodeDigest(row.AfterDigest)
	if err != nil {
		return accessport.HistoricalMachineAuditInput{}, err
	}
	return accessport.HistoricalMachineAuditInput{ImportRunID: runID, SourceSystem: historySourceSystem, SourceScope: historyAuditSourceScope, SourceAuditID: id, SourceRowDigest: sourceDigest, Operator: row.Operator, Action: row.Action, TargetType: row.TargetType, TargetID: row.TargetID, BeforeDigest: before, AfterDigest: after, OccurredAt: row.OccurredAt}, nil
}

func clientRowDigest(row historicalClientRow) ([sha256.Size]byte, error) {
	raw, err := json.Marshal(row)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(raw), nil
}
func auditRowDigest(row historicalAuditRow) ([sha256.Size]byte, error) {
	raw, err := json.Marshal(row)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(raw), nil
}
func decodeDigest(value string) ([sha256.Size]byte, error) {
	var out [sha256.Size]byte
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != len(out) {
		return out, errors.New("invalid digest")
	}
	copy(out[:], raw)
	return out, nil
}

func machineHistoryService(ctx context.Context) (*accessapp.MachineService, func(), error) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return nil, nil, errors.New("target database is unavailable")
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL})
	if err != nil {
		return nil, nil, errors.New("target database is unavailable")
	}
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	migrationConfig, err := platformconfig.LoadOpenPlatformMigration()
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	service, err := accessapp.NewMachineService(accessstore.NewPostgreSQL(), unit, credential.PasswordHasher{}, accessapp.MachineConfig{CorpID: migrationConfig.WeComCorpID})
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return service, pool.Close, nil
}

func readKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("snapshot key must be a non-symlink regular 0600 file")
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

func sealToFile(snapshot historicalSnapshot, path, keyPath string) ([sha256.Size]byte, error) {
	canonical, digest, err := canonicalSnapshot(snapshot)
	if err != nil {
		return digest, err
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return digest, errors.New("canonicalize source snapshot")
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
	sealed := append(nonce, aead.Seal(nil, nonce, raw, []byte(historySchemaVersion))...)
	if _, err = file.Write(sealed); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return digest, errors.New("write protected snapshot")
	}
	if closeErr != nil {
		return digest, errors.New("write protected snapshot")
	}
	return digest, nil
}

func loadFile(path, keyPath string) (historicalSnapshot, [sha256.Size]byte, error) {
	key, err := readKey(keyPath)
	if err != nil {
		return historicalSnapshot{}, [sha256.Size]byte{}, err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return historicalSnapshot{}, [sha256.Size]byte{}, errors.New("protected snapshot must be a non-symlink regular 0600 file")
	}
	sealed, err := os.ReadFile(path)
	if err != nil || len(sealed) > 512<<20 {
		return historicalSnapshot{}, [sha256.Size]byte{}, errors.New("read protected snapshot")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return historicalSnapshot{}, [sha256.Size]byte{}, errors.New("open protected snapshot")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < aead.NonceSize() {
		return historicalSnapshot{}, [sha256.Size]byte{}, errors.New("open protected snapshot")
	}
	raw, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(historySchemaVersion))
	if err != nil {
		return historicalSnapshot{}, [sha256.Size]byte{}, errors.New("open protected snapshot")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var snapshot historicalSnapshot
	if err = decoder.Decode(&snapshot); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return historicalSnapshot{}, [sha256.Size]byte{}, errors.New("invalid protected source snapshot")
	}
	canonical, digest, err := canonicalSnapshot(snapshot)
	return canonical, digest, err
}

func writeSummary(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }
