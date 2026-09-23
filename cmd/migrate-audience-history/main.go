// Command migrate-audience-history preserves encrypted source history and imports
// inert static audiences through the Segment and Identity Ports. It never executes source SQL.
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
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

const schema = "audience-source-history/v1"
const magic = "AICRM-AUDIENCE-HISTORY-V1\x00"

var tableNames = []string{"ai_audience_package_group", "ai_audience_package", "ai_audience_package_version", "ai_audience_member_current"}

type row struct {
	ID     int64           `json:"source_id"`
	Digest string          `json:"sha256"`
	Fact   json.RawMessage `json:"fact"`
}
type table struct {
	Count  int    `json:"count"`
	Digest string `json:"sha256"`
	Rows   []row  `json:"rows"`
}
type snapshot struct {
	Schema           string                 `json:"schema"`
	SourceSystem     string                 `json:"source_system"`
	CapturedAt       time.Time              `json:"captured_at"`
	ScopeDeclaration map[string]string      `json:"scope_declaration"`
	ScopeVerified    bool                   `json:"scope_verified"`
	Executable       bool                   `json:"executable"`
	Tables           map[string]table       `json:"tables"`
	ExistingProof    *existingProofEnvelope `json:"existing_wecom_proof,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "audience history capture/inspect failed closed:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: extract|inspect")
	}
	if args[0] != "derive-existing-wecom" && args[0] != "extract" && args[0] != "inspect" && args[0] != "preflight" && args[0] != "apply" && args[0] != "reconcile" {
		return errors.New("unsupported mode")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	predecessor := f.String("predecessor-snapshot", "", "previous derived envelope for resolution-only replay")
	proofSource := f.String("proof-source-sha256", "", "exact frozen raw evidence SHA256")
	proofPath := f.String("identity-proof", "", "protected existing-WeCom proof snapshot")
	proofKey := f.String("identity-proof-key-file", "", "proof decryption key")
	outputPath := f.String("output-snapshot", "", "new protected derived audience snapshot")
	targetEnv := f.String("target-url-env", "AICRM_DATABASE_URL", "target URL environment variable")
	adminID := f.Int64("admin-id", 0, "actual importing administrator ID")
	confirm := f.Bool("confirm-static-import", false, "acknowledge paused static import without activation")
	path := f.String("snapshot", "", "protected encrypted snapshot")
	keyPath := f.String("snapshot-key-file", "", "0600 base64 32-byte key")
	sourceEnv := f.String("source-url-env", "AICRM_AUDIENCE_SOURCE_DATABASE_URL", "environment variable containing read-only source URL")
	sourceSystem := f.String("source-system", "", "stable non-sensitive source name")
	corp := f.String("declared-corp-scope", "", "source declaration only; never verified")
	union := f.String("declared-unionid-scope", "", "source declaration only; never verified")
	expected := f.String("expected-sha256", "", "optional exact canonical plaintext digest for inspect")
	if err := f.Parse(args[1:]); err != nil {
		return errors.New("invalid arguments")
	}
	if f.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *path == "" || *keyPath == "" {
		return errors.New("snapshot and snapshot-key-file required")
	}
	key, err := readKey(*keyPath)
	if err != nil {
		return err
	}
	var s snapshot
	if args[0] == "extract" {
		if !safeName(*sourceSystem) || *sourceEnv == "" {
			return errors.New("source-system and source-url-env required")
		}
		if *corp != "" && !strings.HasPrefix(*corp, "wecom-corp:") {
			return errors.New("invalid declared corp scope")
		}
		if *union != "" && !strings.HasPrefix(*union, "wechat-open-platform:") {
			return errors.New("invalid declared UnionID scope")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		sourceURL, e := platformconfig.NamedDatabaseURL(*sourceEnv)
		if e != nil {
			return errors.New("source database not configured")
		}
		s, err = extract(ctx, sourceURL, *sourceSystem, map[string]string{"wecom_external_userid": *corp, "unionid": *union})
		if err != nil {
			return err
		}
		if err = validate(s); err != nil {
			return err
		}
		plain, _ := json.Marshal(s)
		sealed, e := seal(plain, key)
		if e != nil {
			return e
		}
		if e = writeExclusive(*path, sealed); e != nil {
			return e
		}
	} else {
		sealed, e := readPrivate(*path, 512<<20)
		if e != nil {
			return e
		}
		plain, e := open(sealed, key)
		if e != nil {
			return e
		}
		decoder := json.NewDecoder(bytes.NewReader(plain))
		decoder.DisallowUnknownFields()
		if e = decoder.Decode(&s); e != nil {
			return errors.New("invalid snapshot schema")
		}
		if decoder.Decode(new(any)) != io.EOF {
			return errors.New("trailing snapshot data")
		}
		if e = validate(s); e != nil {
			return e
		}
	}
	raw, _ := json.Marshal(s)
	digest := sum(raw)
	if *expected != "" && *expected != digest {
		return errors.New("snapshot digest mismatch")
	}
	if args[0] == "derive-existing-wecom" {
		if *expected == "" {
			return errors.New("exact source expected-sha256 required")
		}
		return deriveExisting(s, digest, *proofPath, *proofKey, *outputPath, *proofSource, *predecessor, key)
	}
	if args[0] == "apply" || args[0] == "preflight" || args[0] == "reconcile" {
		if *expected == "" {
			return errors.New("exact expected-sha256 required")
		}
		if args[0] == "apply" && !*confirm {
			return errors.New("confirm-static-import required")
		}
		evidence, e := readPrivate(*path, 512<<20)
		if e != nil {
			return e
		}
		targetURL, e := platformconfig.NamedDatabaseURL(*targetEnv)
		if e != nil {
			return errors.New("target database not configured")
		}
		return applySnapshot(s, digest, evidence, targetURL, *adminID, args[0])
	}
	return json.NewEncoder(os.Stdout).Encode(summary(s, digest))
}
func safeName(v string) bool {
	if len(v) == 0 || len(v) > 120 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-", r)) {
			return false
		}
	}
	return true
}
func extract(ctx context.Context, url, source string, scopes map[string]string) (snapshot, error) {
	s := snapshot{Schema: schema, SourceSystem: source, ScopeDeclaration: scopes, Tables: map[string]table{}}
	if url == "" {
		return s, errors.New("source URL environment variable empty")
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return s, errors.New("connect source")
	}
	defer conn.Close(ctx)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return s, errors.New("begin read-only snapshot")
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout = '60s'"); err != nil {
		return s, errors.New("source timeout setup")
	}
	if err = tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&s.CapturedAt); err != nil {
		return s, errors.New("source snapshot time")
	}
	s.CapturedAt = s.CapturedAt.UTC()
	for _, name := range tableNames {
		// Fixed identifiers, never SQL read from a package definition or CLI.
		rows, e := tx.Query(ctx, "SELECT to_jsonb(t) FROM public."+name+" t ORDER BY id")
		if e != nil {
			return s, errors.New("read source table " + name)
		}
		t := table{Rows: []row{}}
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return s, errors.New("read source row")
			}
			canonical, obj, e := canonicalFact(raw)
			if e != nil {
				rows.Close()
				return s, e
			}
			id, e := integer(obj, "id", true)
			if e != nil {
				rows.Close()
				return s, e
			}
			t.Rows = append(t.Rows, row{ID: id, Digest: sum(canonical), Fact: canonical})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return s, errors.New("source row stream interrupted")
		}
		t.Count = len(t.Rows)
		raw, _ := json.Marshal(t.Rows)
		t.Digest = sum(raw)
		s.Tables[name] = t
	}
	if err = tx.Commit(ctx); err != nil {
		return s, errors.New("commit source read snapshot")
	}
	return s, nil
}
func canonicalFact(raw []byte) ([]byte, map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, nil, errors.New("invalid source fact")
	}
	// RawMessage preserves numeric precision and all original field values.
	b, e := json.Marshal(m)
	if e != nil {
		return nil, nil, errors.New("canonical source fact")
	}
	return b, m, nil
}
func integer(m map[string]json.RawMessage, k string, required bool) (int64, error) {
	v, ok := m[k]
	if !ok || string(v) == "null" {
		if required {
			return 0, errors.New("missing source association")
		}
		return 0, nil
	}
	n, e := strconv.ParseInt(string(v), 10, 64)
	if e != nil || n < 1 {
		return 0, errors.New("invalid source association")
	}
	return n, nil
}
func validate(s snapshot) error {
	if err := validateExistingProof(s); err != nil {
		return err
	}
	if s.Schema != schema || !safeName(s.SourceSystem) || s.CapturedAt.IsZero() || s.ScopeVerified || s.Executable || len(s.Tables) != len(tableNames) {
		return errors.New("invalid snapshot boundary")
	}
	if len(s.ScopeDeclaration) != 2 {
		return errors.New("invalid scope declaration")
	}
	for k, v := range s.ScopeDeclaration {
		prefix := ""
		switch k {
		case "unionid":
			prefix = "wechat-open-platform:"
		case "wecom_external_userid":
			prefix = "wecom-corp:"
		default:
			return errors.New("invalid scope declaration")
		}
		if v != "" && !strings.HasPrefix(v, prefix) {
			return errors.New("invalid scope declaration")
		}
	}
	maps := map[string]map[int64]map[string]json.RawMessage{}
	for _, name := range tableNames {
		t, ok := s.Tables[name]
		if !ok || t.Count != len(t.Rows) {
			return errors.New("table count mismatch")
		}
		b, _ := json.Marshal(t.Rows)
		if sum(b) != t.Digest {
			return errors.New("table digest mismatch")
		}
		maps[name] = map[int64]map[string]json.RawMessage{}
		var last int64
		for _, r := range t.Rows {
			canonical, m, e := canonicalFact(r.Fact)
			if e != nil {
				return e
			}
			id, e := integer(m, "id", true)
			if e != nil || r.ID != id || id <= last || sum(canonical) != r.Digest {
				return errors.New("source row digest/order mismatch")
			}
			last = id
			maps[name][id] = m
		}
	}
	packages := maps["ai_audience_package"]
	versions := maps["ai_audience_package_version"]
	groups := maps["ai_audience_package_group"]
	for _, m := range packages {
		g, e := integer(m, "group_id", false)
		if e != nil {
			return e
		}
		if g != 0 && groups[g] == nil {
			return errors.New("orphan package group")
		}
		v, e := integer(m, "current_version_id", false)
		if e != nil {
			return e
		}
		if v != 0 {
			vm := versions[v]
			if vm == nil {
				return errors.New("orphan current version")
			}
			pid, e := integer(vm, "package_id", true)
			self, _ := integer(m, "id", true)
			if e != nil || pid != self {
				return errors.New("current version belongs to another package")
			}
		}
	}
	for _, name := range []string{"ai_audience_package_version", "ai_audience_member_current"} {
		for _, m := range maps[name] {
			pid, e := integer(m, "package_id", true)
			if e != nil || packages[pid] == nil {
				return errors.New("orphan package child")
			}
		}
	}
	return nil
}
func summary(s snapshot, digest string) map[string]any {
	counts := map[string]int{}
	for k, t := range s.Tables {
		counts[k] = t.Count
	}
	states := map[string]int{"packages_active": 0, "packages_archived": 0, "packages_other": 0, "members_active": 0, "members_exited": 0, "members_other": 0, "versions_template_null": 0, "versions_template_present": 0}
	for _, name := range []string{"ai_audience_package", "ai_audience_member_current", "ai_audience_package_version"} {
		for _, r := range s.Tables[name].Rows {
			var m map[string]json.RawMessage
			_ = json.Unmarshal(r.Fact, &m)
			var status string
			_ = json.Unmarshal(m["status"], &status)
			if name == "ai_audience_package_version" {
				var template string
				_ = json.Unmarshal(m["template_key"], &template)
				if template == "" {
					states["versions_template_null"]++
				} else {
					states["versions_template_present"]++
				}
				continue
			}
			if name == "ai_audience_member_current" {
				var uid, value string
				_ = json.Unmarshal(m["unionid"], &uid)
				_ = json.Unmarshal(m["identity_value"], &value)
				if strings.TrimSpace(uid) != "" {
					states["members_unionid_present"]++
				}
				if strings.TrimSpace(value) != "" {
					states["members_identity_value_present"]++
				}
				if strings.TrimSpace(uid) == "" && strings.TrimSpace(value) == "" {
					states["members_no_identity_value"]++
				}
			}
			prefix := "packages_"
			allowed := status == "active" || status == "archived"
			if name == "ai_audience_member_current" {
				prefix = "members_"
				allowed = status == "active" || status == "exited"
			}
			if !allowed {
				status = "other"
			}
			states[prefix+status]++
		}
	}
	return map[string]any{"schema": schema, "snapshot_sha256": digest, "captured_at": s.CapturedAt, "counts": counts, "states": states, "source_sql_executed": false, "scope_verified": false, "target_writes": 0, "provider_calls": 0}
}
func sum(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func readPrivate(path string, max int64) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > max {
		return nil, errors.New("protected file must be regular 0600 within size limit")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, errors.New("read protected file")
	}
	return b, nil
}
func readKey(path string) ([]byte, error) {
	raw, e := readPrivate(path, 4096)
	if e != nil {
		return nil, e
	}
	s := strings.TrimSpace(string(raw))
	k, e := base64.RawStdEncoding.DecodeString(s)
	if e != nil || len(k) != 32 {
		return nil, errors.New("key requires raw standard base64 32 bytes")
	}
	return k, nil
}
func aead(key []byte) (cipher.AEAD, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, errors.New("invalid encryption key")
	}
	return cipher.NewGCM(b)
}
func seal(plain, key []byte) ([]byte, error) {
	a, e := aead(key)
	if e != nil {
		return nil, e
	}
	n := make([]byte, a.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, errors.New("nonce generation")
	}
	out := append([]byte(magic), n...)
	return a.Seal(out, n, plain, []byte(magic)), nil
}
func open(raw, key []byte) ([]byte, error) {
	a, e := aead(key)
	if e != nil {
		return nil, e
	}
	offset := len(magic)
	if len(raw) < offset+a.NonceSize()+a.Overhead() || string(raw[:offset]) != magic {
		return nil, errors.New("invalid encrypted envelope")
	}
	plain, e := a.Open(nil, raw[offset:offset+a.NonceSize()], raw[offset+a.NonceSize():], []byte(magic))
	if e != nil {
		return nil, errors.New("snapshot authentication failed")
	}
	return plain, nil
}
func writeExclusive(path string, b []byte) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return errors.New("create new protected snapshot")
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	if _, e = f.Write(b); e != nil {
		return errors.New("write encrypted snapshot")
	}
	if e = f.Sync(); e != nil {
		return errors.New("sync encrypted snapshot")
	}
	if e = f.Close(); e != nil {
		return errors.New("close encrypted snapshot")
	}
	ok = true
	return nil
}
