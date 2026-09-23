package main

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

const snapshotMagic = "AICRM-CHANNEL-SNAPSHOT-V1\n"

var expectedSourceTables = []string{
	"automation_channel", "automation_channel_assignee", "automation_channel_assignment_event", "automation_channel_contact",
	"automation_channel_entry_effect_log", "automation_channel_entry_runtime", "automation_channel_qrcode_asset",
	"automation_channel_scene_alias", "channel_welcome_effect_dependency", "channel_welcome_effect_graph",
	"wecom_customer_acquisition_links",
	"image_library", "miniprogram_library", "attachment_library", "group_invite_library",
	"wecom_corp_tag_groups", "wecom_corp_tags",
}

type snapshotManifest struct {
	SchemaVersion     int             `json:"schema_version"`
	SnapshotID        string          `json:"snapshot_id"`
	SnapshotTimestamp time.Time       `json:"snapshot_timestamp"`
	SourceHostDigest  string          `json:"source_host_digest"`
	Tables            []snapshotTable `json:"tables"`
	ManifestDigest    string          `json:"manifest_digest"`
}
type snapshotTable struct {
	Name    string        `json:"name"`
	Digest  string        `json:"digest"`
	Columns []string      `json:"columns"`
	Rows    []snapshotRow `json:"rows"`
}
type snapshotRow struct {
	SourcePK string          `json:"source_pk"`
	Digest   string          `json:"digest"`
	Payload  json.RawMessage `json:"payload"`
}
type discoveryTable struct {
	Name               string                      `json:"name"`
	Digest             string                      `json:"digest"`
	Columns            []string                    `json:"columns"`
	RowCount           int64                       `json:"row_count"`
	NullCounts         map[string]int64            `json:"null_counts"`
	DuplicatePrimaryID int64                       `json:"duplicate_primary_ids"`
	OrphanChannelIDs   int64                       `json:"orphan_channel_ids"`
	TimeWatermarks     map[string][2]string        `json:"time_watermarks"`
	JSONTypeCounts     map[string]map[string]int64 `json:"json_type_counts"`
}

type discoveryReport struct {
	SnapshotTimestamp time.Time        `json:"snapshot_timestamp"`
	SourceHostDigest  string           `json:"source_host_digest"`
	Tables            []discoveryTable `json:"tables"`
	MissingTables     []string         `json:"missing_tables"`
}

func snapshotKeyFromEnvironment() ([]byte, error) {
	cfg, configErr := platformconfig.LoadChannelHistoryMigration()
	if configErr != nil {
		return nil, configErr
	}
	raw := cfg.SnapshotKey
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, errors.New("AICRM_CHANNEL_SNAPSHOT_KEY must be a base64 encoded 32-byte key")
	}
	return key, nil
}

func inspectSource(ctx context.Context, cfg options) error {
	migrationConfig, configErr := platformconfig.LoadChannelHistoryMigration()
	if configErr != nil {
		return configErr
	}
	dsn := migrationConfig.SourceDatabaseURL
	if dsn == "" {
		return errors.New("AICRM_CHANNEL_SOURCE_DATABASE_URL is required for inspect")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("source database URL is invalid")
	}
	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect source database: %w", err)
	}
	defer connection.Close(ctx)
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var snapshotAt time.Time
	if err = tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&snapshotAt); err != nil {
		return err
	}
	hostDigest := sha256.Sum256([]byte(strings.ToLower(parsed.Hostname())))
	manifest := snapshotManifest{SchemaVersion: 1, SnapshotTimestamp: snapshotAt.UTC(), SourceHostDigest: "sha256:" + hex.EncodeToString(hostDigest[:]), Tables: []snapshotTable{}}
	report := discoveryReport{snapshotAt.UTC(), manifest.SourceHostDigest, []discoveryTable{}, []string{}}
	sourceTables, err := discoverSourceTableNames(ctx, tx)
	if err != nil {
		return err
	}
	discovered := make(map[string]struct{}, len(sourceTables))
	for _, tableName := range sourceTables {
		discovered[tableName] = struct{}{}
		columns, exists, readErr := discoverColumns(ctx, tx, tableName)
		if readErr != nil {
			return readErr
		}
		if !exists {
			report.MissingTables = append(report.MissingTables, tableName)
			continue
		}
		table, readErr := captureTable(ctx, tx, tableName, columns)
		if readErr != nil {
			return readErr
		}
		manifest.Tables = append(manifest.Tables, table)
		report.Tables = append(report.Tables, analyzeDiscoveryTable(table))
	}
	for _, tableName := range expectedSourceTables {
		if _, ok := discovered[tableName]; !ok {
			report.MissingTables = append(report.MissingTables, tableName)
		}
	}
	applyOrphanDiagnostics(report.Tables, manifest)
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	manifest.ManifestDigest = manifest.computeDigest()
	manifest.SnapshotID = "channel-" + strings.TrimPrefix(manifest.ManifestDigest, "sha256:")[:24]
	if cfg.report != "" {
		if err = writeJSONAtomic(cfg.report, report, 0o600); err != nil {
			return err
		}
	}
	if cfg.snapshot != "" {
		key, keyErr := snapshotKeyFromEnvironment()
		if keyErr != nil {
			return keyErr
		}
		if err = writeEncryptedSnapshot(cfg.snapshot, key, manifest); err != nil {
			return err
		}
	}
	return printJSON(map[string]any{"mode": "inspect", "snapshot_id": manifest.SnapshotID, "manifest_sha256": manifest.DigestHex(), "snapshot_written": cfg.snapshot != "", "report": report})
}

func discoverSourceTableNames(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE' AND (table_name LIKE 'automation_channel%' OR table_name LIKE 'channel_welcome_effect%' OR table_name IN ('wecom_customer_acquisition_links','image_library','miniprogram_library','attachment_library','group_invite_library','wecom_corp_tag_groups','wecom_corp_tags')) ORDER BY table_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		result = append(result, name)
	}
	return result, rows.Err()
}

// inspectSourceStream consumes the output of one remote psql session. The
// producer starts a repeatable-read, read-only transaction before emitting the
// timestamp, table metadata and hex-encoded JSON rows. Keeping transport out of
// this command means the migration binary never owns SSH credentials and both
// direct-DSN and forced-command sources produce the same authenticated format.
func inspectSourceStream(cfg options) error {
	if cfg.sourceStream == "" || cfg.sourceHost == "" {
		return errors.New("inspect-stream requires --source-stream and --source-host")
	}
	parsed, err := url.Parse("ssh://" + cfg.sourceHost)
	if err != nil || parsed.Hostname() == "" || parsed.Hostname() != cfg.sourceHost {
		return errors.New("source host is invalid")
	}
	input, err := os.Open(cfg.sourceStream)
	if err != nil {
		return err
	}
	defer input.Close()
	manifest, err := parseSourceStream(input, cfg.sourceHost)
	if err != nil {
		return err
	}
	report := discoveryReport{SnapshotTimestamp: manifest.SnapshotTimestamp, SourceHostDigest: manifest.SourceHostDigest, Tables: []discoveryTable{}, MissingTables: []string{}}
	discovered := make(map[string]struct{}, len(manifest.Tables))
	for _, table := range manifest.Tables {
		discovered[table.Name] = struct{}{}
		report.Tables = append(report.Tables, analyzeDiscoveryTable(table))
	}
	for _, tableName := range expectedSourceTables {
		if _, ok := discovered[tableName]; !ok {
			report.MissingTables = append(report.MissingTables, tableName)
		}
	}
	if len(report.MissingTables) != 0 {
		return fmt.Errorf("source stream is missing required tables: %s", strings.Join(report.MissingTables, ","))
	}
	applyOrphanDiagnostics(report.Tables, manifest)
	manifest.ManifestDigest = manifest.computeDigest()
	manifest.SnapshotID = "channel-" + strings.TrimPrefix(manifest.ManifestDigest, "sha256:")[:24]
	if cfg.report != "" {
		if err = writeJSONAtomic(cfg.report, report, 0o600); err != nil {
			return err
		}
	}
	if cfg.snapshot != "" {
		key, keyErr := snapshotKeyFromEnvironment()
		if keyErr != nil {
			return keyErr
		}
		if err = writeEncryptedSnapshot(cfg.snapshot, key, manifest); err != nil {
			return err
		}
	}
	return printJSON(map[string]any{"mode": "inspect-stream", "snapshot_id": manifest.SnapshotID, "manifest_sha256": manifest.DigestHex(), "snapshot_written": cfg.snapshot != "", "report": report})
}

const (
	streamSnapshotPrefix = "__AICRM_SNAPSHOT__|"
	streamTablePrefix    = "__AICRM_TABLE__|"
	streamRowPrefix      = "__AICRM_ROW__|"
)

func parseSourceStream(input io.Reader, sourceHost string) (snapshotManifest, error) {
	hostDigest := sha256.Sum256([]byte(strings.ToLower(sourceHost)))
	manifest := snapshotManifest{SchemaVersion: 1, SourceHostDigest: "sha256:" + hex.EncodeToString(hostDigest[:]), Tables: []snapshotTable{}}
	known := map[string]bool{}
	var current *snapshotTable
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, streamSnapshotPrefix):
			if !manifest.SnapshotTimestamp.IsZero() {
				return snapshotManifest{}, errors.New("duplicate source snapshot timestamp")
			}
			manifest.SnapshotTimestamp, _ = time.Parse(time.RFC3339Nano, strings.TrimPrefix(line, streamSnapshotPrefix))
			if manifest.SnapshotTimestamp.IsZero() {
				return snapshotManifest{}, errors.New("invalid source snapshot timestamp")
			}
		case strings.HasPrefix(line, streamTablePrefix):
			parts := strings.SplitN(strings.TrimPrefix(line, streamTablePrefix), "|", 2)
			if len(parts) != 2 || !validSourceTableName(parts[0]) || known[parts[0]] {
				return snapshotManifest{}, errors.New("invalid source stream table marker")
			}
			columnsJSON, decodeErr := hex.DecodeString(parts[1])
			var columns []string
			if decodeErr != nil || json.Unmarshal(columnsJSON, &columns) != nil || len(columns) == 0 {
				return snapshotManifest{}, errors.New("invalid source stream columns")
			}
			known[parts[0]] = true
			manifest.Tables = append(manifest.Tables, snapshotTable{Name: parts[0], Columns: columns, Rows: []snapshotRow{}})
			current = &manifest.Tables[len(manifest.Tables)-1]
		case strings.HasPrefix(line, streamRowPrefix):
			if current == nil {
				return snapshotManifest{}, errors.New("source row appeared before table marker")
			}
			payload, decodeErr := hex.DecodeString(strings.TrimPrefix(line, streamRowPrefix))
			payload, compactErr := compactJSONObject(payload)
			if decodeErr != nil || compactErr != nil {
				return snapshotManifest{}, errors.New("invalid source stream row")
			}
			digest := sha256.Sum256(payload)
			current.Rows = append(current.Rows, snapshotRow{Digest: "sha256:" + hex.EncodeToString(digest[:]), Payload: append(json.RawMessage(nil), payload...)})
		}
	}
	if err := scanner.Err(); err != nil {
		return snapshotManifest{}, err
	}
	if manifest.SnapshotTimestamp.IsZero() || len(manifest.Tables) == 0 {
		return snapshotManifest{}, errors.New("incomplete source stream")
	}
	for index := range manifest.Tables {
		finalizeSnapshotTable(&manifest.Tables[index])
	}
	sort.Slice(manifest.Tables, func(i, j int) bool { return manifest.Tables[i].Name < manifest.Tables[j].Name })
	return manifest, nil
}

func analyzeDiscoveryTable(table snapshotTable) discoveryTable {
	result := discoveryTable{Name: table.Name, Digest: table.Digest, Columns: table.Columns, RowCount: int64(len(table.Rows)), NullCounts: map[string]int64{}, TimeWatermarks: map[string][2]string{}, JSONTypeCounts: map[string]map[string]int64{}}
	ids := map[string]int64{}
	for _, row := range table.Rows {
		object := map[string]json.RawMessage{}
		_ = json.Unmarshal(row.Payload, &object)
		for _, column := range table.Columns {
			raw, ok := object[column]
			if !ok || string(raw) == "null" {
				result.NullCounts[column]++
				continue
			}
			kind := jsonValueKind(raw)
			if result.JSONTypeCounts[column] == nil {
				result.JSONTypeCounts[column] = map[string]int64{}
			}
			result.JSONTypeCounts[column][kind]++
			if column == "id" {
				ids[string(raw)]++
			}
			if strings.HasSuffix(column, "_at") {
				var value string
				if json.Unmarshal(raw, &value) == nil && value != "" {
					watermark := result.TimeWatermarks[column]
					if watermark[0] == "" || value < watermark[0] {
						watermark[0] = value
					}
					if watermark[1] == "" || value > watermark[1] {
						watermark[1] = value
					}
					result.TimeWatermarks[column] = watermark
				}
			}
		}
	}
	for _, count := range ids {
		if count > 1 {
			result.DuplicatePrimaryID += count
		}
	}
	return result
}

func applyOrphanDiagnostics(tables []discoveryTable, manifest snapshotManifest) {
	channels := map[int64]struct{}{}
	if table, ok := manifest.table("automation_channel"); ok {
		for _, row := range table.Rows {
			if id, found := jsonInt(row.Payload, "id"); found {
				channels[id] = struct{}{}
			}
		}
	}
	for index := range tables {
		table, ok := manifest.table(tables[index].Name)
		if !ok || table.Name == "automation_channel" {
			continue
		}
		for _, row := range table.Rows {
			if id, found := jsonInt(row.Payload, "channel_id"); found {
				if _, exists := channels[id]; !exists {
					tables[index].OrphanChannelIDs++
				}
			}
		}
	}
}

func jsonValueKind(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "invalid"
	}
	switch trimmed[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	default:
		return "number"
	}
}

func discoverColumns(ctx context.Context, tx pgx.Tx, table string) ([]string, bool, error) {
	rows, err := tx.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var column string
		if err = rows.Scan(&column); err != nil {
			return nil, false, err
		}
		columns = append(columns, column)
	}
	return columns, len(columns) > 0, rows.Err()
}

func captureTable(ctx context.Context, tx pgx.Tx, name string, columns []string) (snapshotTable, error) {
	identifier := pgx.Identifier{"public", name}.Sanitize()
	rows, err := tx.Query(ctx, "SELECT to_jsonb(source_row) FROM "+identifier+" AS source_row")
	if err != nil {
		return snapshotTable{}, err
	}
	defer rows.Close()
	table := snapshotTable{Name: name, Columns: columns, Rows: []snapshotRow{}}
	for rows.Next() {
		var payload []byte
		if err = rows.Scan(&payload); err != nil {
			return snapshotTable{}, err
		}
		payload, err = compactJSONObject(payload)
		if err != nil {
			return snapshotTable{}, errors.New("source row is not a JSON object")
		}
		digest := sha256.Sum256(payload)
		table.Rows = append(table.Rows, snapshotRow{Digest: "sha256:" + hex.EncodeToString(digest[:]), Payload: append(json.RawMessage(nil), payload...)})
	}
	if err = rows.Err(); err != nil {
		return snapshotTable{}, err
	}
	finalizeSnapshotTable(&table)
	return table, nil
}

func compactJSONObject(payload []byte) ([]byte, error) {
	if !json.Valid(payload) {
		return nil, errors.New("invalid JSON")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, errors.New("JSON value is not an object")
	}
	// Marshal through RawMessage fields once so key ordering, insignificant
	// whitespace and the encoder's HTML escaping are already in the exact form
	// that the enclosing encrypted manifest will preserve.
	compact, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	if len(compact) == 0 || compact[0] != '{' {
		return nil, errors.New("JSON value is not an object")
	}
	return compact, nil
}

func finalizeSnapshotTable(table *snapshotTable) {
	sort.SliceStable(table.Rows, func(i, j int) bool { return string(table.Rows[i].Payload) < string(table.Rows[j].Payload) })
	occurrences := map[string]int{}
	for index := range table.Rows {
		base := "row=" + strings.TrimPrefix(table.Rows[index].Digest, "sha256:")[:24]
		if raw, ok := decodeObject(table.Rows[index].Payload)["id"]; ok && string(raw) != "null" {
			base = "id=" + strings.Trim(string(raw), `"`)
		}
		occurrences[base]++
		table.Rows[index].SourcePK = fmt.Sprintf("%s#%d", base, occurrences[base])
	}
	table.Digest = digestRows(table.Rows)
}

func digestRows(rows []snapshotRow) string {
	hash := sha256.New()
	for _, row := range rows {
		_, _ = io.WriteString(hash, row.SourcePK+"\x00"+row.Digest+"\n")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func (manifest snapshotManifest) computeDigest() string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, fmt.Sprintf("%d\x00%s\x00%s\n", manifest.SchemaVersion, manifest.SnapshotTimestamp.UTC().Format(time.RFC3339Nano), manifest.SourceHostDigest))
	tables := append([]snapshotTable(nil), manifest.Tables...)
	sort.Slice(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })
	for _, table := range tables {
		_, _ = io.WriteString(hash, table.Name+"\x00"+table.Digest+"\n")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func (manifest snapshotManifest) DigestHex() string {
	return strings.TrimPrefix(manifest.ManifestDigest, "sha256:")
}
func (manifest snapshotManifest) Summary() map[string]int {
	result := map[string]int{}
	for _, table := range manifest.Tables {
		result[table.Name] = len(table.Rows)
	}
	return result
}
func (manifest snapshotManifest) Validate() error {
	if manifest.SchemaVersion != 1 || manifest.SnapshotID == "" || manifest.SnapshotTimestamp.IsZero() || !validSHA(manifest.SourceHostDigest) || !validSHA(manifest.ManifestDigest) || manifest.computeDigest() != manifest.ManifestDigest {
		return errors.New("invalid channel snapshot manifest")
	}
	seen := map[string]struct{}{}
	for _, table := range manifest.Tables {
		if _, exists := seen[table.Name]; exists {
			return errors.New("duplicate snapshot table")
		}
		seen[table.Name] = struct{}{}
		if !validSourceTableName(table.Name) || table.Digest != digestRows(table.Rows) {
			return errors.New("invalid snapshot table digest")
		}
		rowSeen := map[string]struct{}{}
		for _, row := range table.Rows {
			if row.SourcePK == "" || !json.Valid(row.Payload) || !validSHA(row.Digest) {
				return errors.New("invalid snapshot row")
			}
			digest := sha256.Sum256(row.Payload)
			if row.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
				return errors.New("snapshot row digest mismatch")
			}
			if _, exists := rowSeen[row.SourcePK]; exists {
				return errors.New("duplicate source primary key")
			}
			rowSeen[row.SourcePK] = struct{}{}
		}
	}
	return nil
}

func validSourceTableName(name string) bool {
	if strings.HasPrefix(name, "automation_channel") || strings.HasPrefix(name, "channel_welcome_effect") || name == "wecom_customer_acquisition_links" || name == "image_library" || name == "miniprogram_library" || name == "attachment_library" || name == "group_invite_library" || name == "wecom_corp_tag_groups" || name == "wecom_corp_tags" {
		for _, character := range name {
			if character != '_' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return false
			}
		}
		return len(name) <= 200
	}
	return false
}
func validSHA(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(value[7:])
	return err == nil
}

func writeEncryptedSnapshot(path string, key []byte, manifest snapshotManifest) error {
	plain, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nil, nonce, plain, []byte(snapshotMagic))
	data := append([]byte(snapshotMagic), nonce...)
	data = append(data, sealed...)
	return writeAtomic(path, data, 0o600)
}
func loadEncryptedSnapshot(path string, key []byte) (snapshotManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return snapshotManifest{}, err
	}
	if !strings.HasPrefix(string(data), snapshotMagic) {
		return snapshotManifest{}, errors.New("invalid snapshot header")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return snapshotManifest{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return snapshotManifest{}, err
	}
	data = data[len(snapshotMagic):]
	if len(data) <= aead.NonceSize() {
		return snapshotManifest{}, errors.New("invalid encrypted snapshot")
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte(snapshotMagic))
	if err != nil {
		return snapshotManifest{}, errors.New("snapshot authentication failed")
	}
	var manifest snapshotManifest
	if json.Unmarshal(plain, &manifest) != nil {
		return snapshotManifest{}, errors.New("invalid snapshot payload")
	}
	return manifest, nil
}
func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'), mode)
}
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".channel-snapshot-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err = temporary.Chmod(mode); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
