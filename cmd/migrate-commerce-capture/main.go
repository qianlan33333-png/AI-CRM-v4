// Command migrate-commerce-capture freezes complete financial and identity source
// evidence. It has no target database or Provider writing capability.
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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

var sourceTables = []string{"wechat_pay_orders", "wechat_pay_refunds", "wechat_shop_orders", "wechat_shop_refunds", "alipay_pay_orders", "crm_user_identity", "wecom_external_contact_identity_map"}

const envelope = "AICRM-COMMERCE-CUTOVER-RAW-1\n"

type snapshot struct {
	Schema  string                     `json:"schema"`
	Source  string                     `json:"source_system"`
	At      time.Time                  `json:"snapshot_at"`
	Tables  map[string]json.RawMessage `json:"tables"`
	Counts  map[string]int             `json:"counts"`
	Digests map[string]string          `json:"digests"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "commerce capture failed:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	fs := flag.NewFlagSet("migrate-commerce-capture", flag.ContinueOnError)
	mode := fs.String("mode", "capture", "capture|inspect|normalize")
	directory := fs.String("directory", "", "new protected capture directory, or existing directory for inspect")
	outputDirectory := fs.String("output-directory", "", "new protected normalization directory")
	runKey := fs.String("run-key", "", "normalized migration run key")
	keyPath := fs.String("key-file", "", "protected file containing 32-byte base64 encryption key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *mode != "capture" && *mode != "inspect" && *mode != "normalize" {
		return errors.New("invalid mode")
	}
	key, err := readKey(*keyPath)
	if err != nil {
		return err
	}
	if *directory == "" {
		return errors.New("directory required")
	}
	if *mode == "inspect" || *mode == "normalize" {
		info, e := os.Lstat(*directory)
		if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return errors.New("protected directory required")
		}
		snapshotPath := filepath.Join(*directory, "source.enc")
		snapshotInfo, e := os.Lstat(snapshotPath)
		if e != nil || !snapshotInfo.Mode().IsRegular() || snapshotInfo.Mode().Perm()&0077 != 0 {
			return errors.New("protected snapshot file required")
		}
		sealed, e := os.ReadFile(snapshotPath)
		if e != nil {
			return errors.New("snapshot unreadable")
		}
		plain, e := open(key, sealed)
		if e != nil {
			return e
		}
		var s snapshot
		if json.Unmarshal(plain, &s) != nil {
			return errors.New("invalid snapshot")
		}
		if e = validate(s); e != nil {
			return e
		}
		if *mode == "normalize" {
			return normalizeCapture(s, *outputDirectory, *runKey)
		}
		return report(s)
	}
	sourceURL := config.LoadCommerceCutoverSourceURL()
	if sourceURL == "" {
		return errors.New("AICRM_COMMERCE_SOURCE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	conn, e := pgx.Connect(ctx, sourceURL)
	if e != nil {
		return errors.New("source connection failed")
	}
	defer conn.Close(ctx)
	tx, e := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return errors.New("source transaction failed")
	}
	defer tx.Rollback(ctx)
	s := snapshot{Schema: envelope, Source: "aicrm-production", Tables: map[string]json.RawMessage{}, Counts: map[string]int{}, Digests: map[string]string{}}
	if e = tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&s.At); e != nil {
		return errors.New("source time failed")
	}
	for _, table := range sourceTables {
		var raw []byte
		// Fixed allowlist identifiers only. Every table must be read successfully;
		// an empty array proves zero rows, whereas missing permissions fail closed.
		query := `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb) FROM ` + pgx.Identifier{"public", table}.Sanitize() + ` t`
		if e = tx.QueryRow(ctx, query).Scan(&raw); e != nil {
			return fmt.Errorf("source table unavailable: %s", table)
		}
		var rows []json.RawMessage
		if json.Unmarshal(raw, &rows) != nil {
			return fmt.Errorf("invalid source table: %s", table)
		}
		var compact bytes.Buffer
		if e = json.Compact(&compact, raw); e != nil {
			return errors.New("source canonical encoding failed")
		}
		raw = compact.Bytes()
		digest := sha256.Sum256(raw)
		s.Tables[table] = raw
		s.Counts[table] = len(rows)
		s.Digests[table] = hex.EncodeToString(digest[:])
	}
	if e = tx.Commit(ctx); e != nil {
		return errors.New("source read commit failed")
	}
	if e = validate(s); e != nil {
		return e
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	e = encoder.Encode(s)
	plain := encoded.Bytes()
	if e != nil {
		return errors.New("snapshot encoding failed")
	}
	sealed, e := seal(key, plain)
	if e != nil {
		return e
	}
	// Refuse existing paths rather than replacing any previous frozen evidence.
	if e = os.Mkdir(*directory, 0700); e != nil {
		return errors.New("capture requires a new directory")
	}
	if e = writeExclusive(filepath.Join(*directory, "source.enc"), sealed); e != nil {
		return e
	}
	rawDigest := sha256.Sum256(plain)
	metadata, _ := json.MarshalIndent(map[string]any{"source_system": s.Source, "snapshot_at": s.At, "counts": s.Counts, "raw_sha256": hex.EncodeToString(rawDigest[:]), "tables_sha256": s.Digests, "normalization": "not_performed", "provider_effects_created": 0}, "", "  ")
	if e = writeExclusive(filepath.Join(*directory, "evidence.json"), metadata); e != nil {
		return e
	}
	return report(s)
}
func validate(s snapshot) error {
	if s.Schema != envelope || s.Source != "aicrm-production" || s.At.IsZero() || len(s.Tables) != len(sourceTables) || len(s.Counts) != len(sourceTables) || len(s.Digests) != len(sourceTables) {
		return errors.New("incomplete snapshot envelope")
	}
	for _, table := range sourceTables {
		raw, ok := s.Tables[table]
		var rows []json.RawMessage
		if !ok || json.Unmarshal(raw, &rows) != nil || rows == nil || len(rows) != s.Counts[table] {
			return fmt.Errorf("snapshot table coverage mismatch: %s", table)
		}
		digest := sha256.Sum256(raw)
		if hex.EncodeToString(digest[:]) != s.Digests[table] {
			// Legacy json.Marshal HTML-escaped RawMessage after hashing it.
			// Only accept reversal when it exactly reproduces the frozen hash.
			restored := append([]byte(nil), raw...)
			for _, pair := range [][2]string{{`\u003c`, "<"}, {`\u003e`, ">"}, {`\u0026`, "&"}, {`\u2028`, " "}, {`\u2029`, " "}} {
				restored = bytes.ReplaceAll(restored, []byte(pair[0]), []byte(pair[1]))
			}
			restoredDigest := sha256.Sum256(restored)
			if hex.EncodeToString(restoredDigest[:]) != s.Digests[table] || !json.Valid(restored) {
				return fmt.Errorf("snapshot table digest mismatch: %s", table)
			}
			s.Tables[table] = restored
		}
	}
	return nil
}
func report(s snapshot) error {
	// Counts only: never print table rows, identities, credentials or provider payloads.
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"source_system": s.Source, "snapshot_at": s.At, "counts": s.Counts, "complete_raw_capture": true, "normalized_manifest_ready": false, "provider_effects_created": 0})
}
func readKey(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("protected regular key file required")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		return nil, errors.New("key unavailable")
	}
	key, e := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if e != nil {
		key, e = base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	}
	if e != nil || len(key) != 32 {
		return nil, errors.New("invalid encryption key")
	}
	return key, nil
}
func writeExclusive(path string, raw []byte) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return errors.New("protected evidence creation failed")
	}
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return errors.New("evidence write failed")
	}
	return nil
}
func seal(key, plain []byte) ([]byte, error) {
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, errors.New("invalid encryption key")
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	out := append([]byte(envelope), nonce...)
	return g.Seal(out, nonce, plain, []byte(envelope)), nil
}
func open(key, sealed []byte) ([]byte, error) {
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, errors.New("invalid encryption key")
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	if len(sealed) < len(envelope)+g.NonceSize()+g.Overhead() || string(sealed[:len(envelope)]) != envelope {
		return nil, errors.New("invalid encrypted envelope")
	}
	nonce := sealed[len(envelope) : len(envelope)+g.NonceSize()]
	plain, e := g.Open(nil, nonce, sealed[len(envelope)+g.NonceSize():], []byte(envelope))
	if e != nil {
		return nil, errors.New("snapshot authentication failed")
	}
	return plain, nil
}
