package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	archivemigration "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/migration"
)

var archiveSourceRevision = regexp.MustCompile(`^[a-f0-9]{40}$`)

// extractLegacyArchiveSnapshot is the only code path that opens a donor
// database. It uses an operator-provided 0600 URL file and a repeatable,
// read-only transaction, then writes an explicit offline snapshot for the
// normal dry-run/apply/reconcile command paths. It never contacts a Provider
// or changes a source or target database.
func extractLegacyArchiveSnapshot(ctx context.Context, cfg options) (archivemigration.Manifest, error) {
	if cfg.snapshot == "" || cfg.sourceDatabaseURLFile == "" || !archiveSourceRevision.MatchString(cfg.sourceRevision) || !validCorpID(cfg.corpID) {
		return archivemigration.Manifest{}, errInvalidArguments
	}
	dsn, err := protectedArchiveSourceURL(cfg.sourceDatabaseURLFile)
	if err != nil {
		return archivemigration.Manifest{}, err
	}
	sourceConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return archivemigration.Manifest{}, errors.New("invalid legacy archive source URL")
	}
	conn, err := pgx.ConnectConfig(ctx, sourceConfig)
	if err != nil {
		return archivemigration.Manifest{}, errors.New("legacy archive source unavailable")
	}
	defer conn.Close(ctx)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return archivemigration.Manifest{}, errors.New("begin legacy archive source snapshot")
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout='15s'"); err != nil {
		return archivemigration.Manifest{}, errors.New("configure legacy archive source snapshot")
	}
	manifest, err := extractLegacyArchiveRows(ctx, tx, cfg.sourceRevision, cfg.corpID)
	if err != nil {
		return archivemigration.Manifest{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return archivemigration.Manifest{}, errors.New("finish legacy archive source snapshot")
	}
	return manifest, nil
}

// extractLegacyArchiveRows maps exactly the frozen archived_messages facts.
// to_jsonb(message)->>'group_name' supports donor instances that retained a
// row-level group_name while remaining compatible with the older table where
// the value lives only in raw_payload. The frozen donor stores an SDK wrapper
// in raw_payload: {seq, encrypted_record, decrypted_message}. Only the
// decrypted member is a PlainArchiveRecord payload; the full wrapper is never
// copied into V3, but its SHA-256 is frozen in the offline snapshot.
func extractLegacyArchiveRows(ctx context.Context, tx pgx.Tx, revision, corpID string) (archivemigration.Manifest, error) {
	if tx == nil || !archiveSourceRevision.MatchString(revision) || !validCorpID(corpID) {
		return archivemigration.Manifest{}, errInvalidArguments
	}
	rows, err := tx.Query(ctx, `
		SELECT message.id,message.seq,message.msgid,COALESCE(message.unionid,''),
			COALESCE(to_jsonb(message)->>'group_name',''),COALESCE(message.raw_payload::text,'')
		FROM archived_messages message
		ORDER BY message.seq,message.id`)
	if err != nil {
		return archivemigration.Manifest{}, errors.New("legacy archived_messages contract unavailable")
	}
	defer rows.Close()
	manifest := archivemigration.Manifest{
		SchemaVersion: archivemigration.SchemaVersion,
		SourceName:    "ai-crm:archived_messages:" + revision,
		CorpScope:     "wecom-corp:" + strings.TrimSpace(corpID),
		Records:       []archivemigration.SourceRow{},
	}
	for rows.Next() {
		var id, seq int64
		var msgID, unionID, rowGroupName, rawPayload string
		if err = rows.Scan(&id, &seq, &msgID, &unionID, &rowGroupName, &rawPayload); err != nil {
			return archivemigration.Manifest{}, errors.New("scan legacy archived_messages row")
		}
		row, rowErr := extractedArchiveSourceRow(id, seq, msgID, unionID, rowGroupName, rawPayload)
		if rowErr != nil {
			return archivemigration.Manifest{}, rowErr
		}
		manifest.Records = append(manifest.Records, row)
	}
	if err = rows.Err(); err != nil {
		return archivemigration.Manifest{}, errors.New("read legacy archived_messages rows")
	}
	if len(manifest.Records) == 0 {
		return archivemigration.Manifest{}, errors.New("legacy archived_messages snapshot is empty")
	}
	if err = manifest.Validate(); err != nil {
		return archivemigration.Manifest{}, errors.New("legacy archived_messages contract drift")
	}
	raw, err := archiveSnapshotBytes(manifest)
	if err != nil {
		return archivemigration.Manifest{}, errors.New("canonicalize legacy archive snapshot")
	}
	manifest.Digest = sha256.Sum256(raw)
	return manifest, nil
}

func extractedArchiveSourceRow(id, seq int64, msgID, unionID, rowGroupName, rawPayload string) (archivemigration.SourceRow, error) {
	if id < 1 || seq < 1 || strings.TrimSpace(msgID) != msgID || msgID == "" || !json.Valid([]byte(rawPayload)) {
		return archivemigration.SourceRow{}, errors.New("legacy archived_messages row is invalid")
	}
	var wrapper map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(rawPayload))
	decoder.UseNumber()
	if decoder.Decode(&wrapper) != nil || wrapper == nil {
		return archivemigration.SourceRow{}, errors.New("legacy archived_messages payload is invalid")
	}
	wrappedSeq, ok := archiveWrapperSeq(wrapper["seq"])
	if !ok || wrappedSeq != seq {
		return archivemigration.SourceRow{}, errors.New("legacy archived_messages sequence contract drift")
	}
	payload, payloadObject, ok := archiveWrapperObject(wrapper["decrypted_message"])
	if !ok {
		return archivemigration.SourceRow{}, errors.New("legacy archived_messages decrypted message is invalid")
	}
	_, encrypted, ok := archiveWrapperObject(wrapper["encrypted_record"])
	if !ok {
		return archivemigration.SourceRow{}, errors.New("legacy archived_messages encrypted record is invalid")
	}
	if archiveWrapperMessageID(payloadObject, encrypted, seq) != msgID {
		return archivemigration.SourceRow{}, errors.New("legacy archived_messages message contract drift")
	}
	groupName := strings.TrimSpace(rowGroupName)
	if groupName == "" {
		groupName = archiveWrapperText(wrapper["group_name"])
	}
	if len(groupName) > 512 {
		return archivemigration.SourceRow{}, errors.New("legacy archived_messages group name is invalid")
	}
	sourceDigest := sha256.Sum256([]byte(rawPayload))
	return archivemigration.SourceRow{
		SourceRowKey:        fmt.Sprintf("archived_messages/%d", id),
		Seq:                 uint64(seq),
		MsgID:               msgID,
		Payload:             payload,
		SourcePayloadDigest: fmt.Sprintf("%x", sourceDigest),
		HistoricalUnionID:   strings.TrimSpace(unionID),
		HistoricalGroupName: groupName,
	}, nil
}

func archiveWrapperObject(raw json.RawMessage) (json.RawMessage, map[string]json.RawMessage, bool) {
	if !json.Valid(raw) {
		return nil, nil, false
	}
	object := map[string]json.RawMessage{}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&object) != nil || object == nil {
		return nil, nil, false
	}
	return append(json.RawMessage(nil), raw...), object, true
}

func archiveWrapperSeq(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&number) != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	return value, err == nil && value > 0
}

func archiveWrapperText(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func archiveWrapperMessageID(payload, encrypted map[string]json.RawMessage, seq int64) string {
	if value := archiveWrapperText(payload["msgid"]); value != "" {
		return value
	}
	if value := archiveWrapperText(encrypted["msgid"]); value != "" {
		return value
	}
	return fmt.Sprintf("seq-%d", seq)
}

func validCorpID(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= 256 && !strings.ContainsAny(value, "\x00\r\n")
}

func protectedArchiveSourceURL(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return "", errors.New("legacy archive source URL file must be a non-symlink 0600 file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.Contains(value, "\x00") {
		return "", errors.New("legacy archive source URL file is invalid")
	}
	return value, nil
}

func archiveSnapshotBytes(manifest archivemigration.Manifest) ([]byte, error) {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func saveArchiveSnapshot(path string, manifest archivemigration.Manifest) error {
	if path == "" || filepath.Clean(path) != path {
		return errInvalidArguments
	}
	raw, err := archiveSnapshotBytes(manifest)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
