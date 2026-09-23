package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const HistorySchemaVersion = "aicrm-v2-groupops-history-v1"

type HistoryManifest struct {
	SchemaVersion  string            `json:"schema_version"`
	SourceSystem   string            `json:"source_system"`
	SourceRevision string            `json:"source_revision"`
	SnapshotAt     time.Time         `json:"snapshot_at"`
	Counts         map[string]int    `json:"counts"`
	Digests        map[string]string `json:"digests"`
}
type HistorySnapshot struct {
	Manifest           HistoryManifest            `json:"manifest"`
	Plans              []HistoryPlan              `json:"plans"`
	DirectoryChats     []HistoryDirectoryChat     `json:"directory_group_chats"`
	DirectorySnapshots []HistoryDirectorySnapshot `json:"directory_snapshots"`
	Groups             []HistoryGroup             `json:"groups"`
	Nodes              []HistoryNode              `json:"nodes"`
}
type HistoryPlan struct {
	ID                 int64      `json:"id"`
	PlanCode           string     `json:"plan_code"`
	Name               string     `json:"name"`
	PlanType           string     `json:"plan_type"`
	Status             string     `json:"status"`
	OwnerReference     *string    `json:"owner_reference"`
	CreatedByReference *string    `json:"created_by_reference"`
	UpdatedByReference *string    `json:"updated_by_reference"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ArchivedAt         *time.Time `json:"archived_at"`
}
type HistoryDirectoryChat struct {
	ChatReference  string    `json:"chat_reference"`
	DisplayName    string    `json:"display_name"`
	OwnerReference *string   `json:"owner_reference"`
	MemberCount    int32     `json:"member_count"`
	Status         string    `json:"status"`
	RecordedAt     time.Time `json:"recorded_at"`
}
type HistoryDirectorySnapshot struct {
	ChatReference       string    `json:"chat_reference"`
	DisplayName         string    `json:"display_name"`
	OwnerReference      *string   `json:"owner_reference"`
	OwnerName           string    `json:"owner_name"`
	InternalMemberCount int32     `json:"internal_member_count"`
	ExternalMemberCount int32     `json:"external_member_count"`
	Status              string    `json:"status"`
	RecordedAt          time.Time `json:"recorded_at"`
}
type HistoryGroup struct {
	ID                  int64      `json:"id"`
	PlanID              int64      `json:"plan_id"`
	ChatReference       string     `json:"chat_reference"`
	DisplayName         string     `json:"display_name"`
	OwnerReference      *string    `json:"owner_reference"`
	InternalMemberCount int32      `json:"internal_member_count"`
	ExternalMemberCount int32      `json:"external_member_count"`
	Status              string     `json:"status"`
	CreatedAt           time.Time  `json:"created_at"`
	RemovedAt           *time.Time `json:"removed_at"`
}
type HistoryNode struct {
	ID             int64           `json:"id"`
	PlanID         int64           `json:"plan_id"`
	DayIndex       int32           `json:"day_index"`
	TriggerTime    string          `json:"trigger_time"`
	SortOrder      int32           `json:"sort_order"`
	Status         string          `json:"status"`
	ActionTitle    string          `json:"action_title"`
	TextContent    string          `json:"text_content"`
	ContentPackage json.RawMessage `json:"content_package"`
	Attachments    json.RawMessage `json:"attachments"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

var historyTables = []string{"plans", "directory_group_chats", "directory_snapshots", "groups", "nodes"}
var historyQueries = map[string]string{
	"plans":                 `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]'::jsonb) FROM (SELECT id,plan_code,plan_name AS name,plan_type,status,owner_userid AS owner_reference,created_by AS created_by_reference,updated_by AS updated_by_reference,created_at,updated_at,archived_at FROM public.automation_group_ops_plans) x`,
	"directory_group_chats": `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY chat_reference),'[]'::jsonb) FROM (SELECT chat_id AS chat_reference,group_name AS display_name,owner_userid AS owner_reference,member_count,status,updated_at AS recorded_at FROM public.group_chats) x`,
	"directory_snapshots":   `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY chat_reference),'[]'::jsonb) FROM (SELECT chat_id AS chat_reference,group_name AS display_name,owner_userid AS owner_reference,owner_name,internal_member_count,external_member_count,status,synced_at AS recorded_at FROM public.wecom_group_chat_snapshots) x`,
	"groups":                `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]'::jsonb) FROM (SELECT id,plan_id,chat_id AS chat_reference,group_name_snapshot AS display_name,owner_userid_snapshot AS owner_reference,internal_member_count_snapshot AS internal_member_count,external_member_count_snapshot AS external_member_count,status,created_at,removed_at FROM public.automation_group_ops_plan_groups) x`,
	"nodes":                 `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY plan_id,day_index,sort_order,id),'[]'::jsonb) FROM (SELECT id,plan_id,day_index,trigger_time_label AS trigger_time,sort_order,status,action_title,text_content,content_package_json AS content_package,attachments_json AS attachments,created_at,updated_at FROM public.automation_group_ops_plan_nodes) x`,
}

func validHistorySchema(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	for index, r := range value {
		if !(r == '_' || r >= 'a' && r <= 'z' || index > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
func historyQueriesForSchema(schema string) map[string]string {
	queries := make(map[string]string, len(historyQueries))
	for key, query := range historyQueries {
		queries[key] = strings.ReplaceAll(query, "public.", schema+".")
	}
	return queries
}

func ExtractHistory(ctx context.Context, db TxBeginner, revision string) (HistorySnapshot, error) {
	return ExtractHistoryFromSchema(ctx, db, revision, "public")
}

// ExtractHistoryFromSchema is the same read-only V1 query contract with an
// explicit schema solely for isolated PostgreSQL characterization tests.
func ExtractHistoryFromSchema(ctx context.Context, db TxBeginner, revision, schema string) (HistorySnapshot, error) {
	if db == nil || !validRevision.MatchString(revision) || !validHistorySchema(schema) {
		return HistorySnapshot{}, ErrInvalidSnapshot
	}
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return HistorySnapshot{}, errors.New("begin read-only Group Ops history snapshot")
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='15s'`); err != nil {
		return HistorySnapshot{}, errors.New("set source statement timeout")
	}
	var out HistorySnapshot
	if err = tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&out.Manifest.SnapshotAt); err != nil {
		return out, err
	}
	queries := historyQueriesForSchema(schema)
	for _, row := range []struct {
		name string
		dst  any
	}{{"plans", &out.Plans}, {"directory_group_chats", &out.DirectoryChats}, {"directory_snapshots", &out.DirectorySnapshots}, {"groups", &out.Groups}, {"nodes", &out.Nodes}} {
		var raw []byte
		if err = tx.QueryRow(ctx, queries[row.name]).Scan(&raw); err != nil || !json.Valid(raw) || json.Unmarshal(raw, row.dst) != nil {
			return HistorySnapshot{}, fmt.Errorf("extract Group Ops history %s: %w", row.name, ErrInvalidSnapshot)
		}
	}
	if err = PopulateHistoryManifest(&out, ProductionSourceSystem, revision, out.Manifest.SnapshotAt); err != nil {
		return HistorySnapshot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return HistorySnapshot{}, errors.New("finish read-only Group Ops history snapshot")
	}
	return out, nil
}

func (s HistorySnapshot) Canonical() ([]byte, [sha256.Size]byte, error) {
	normalizeHistory(&s)
	if err := s.Validate(); err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	return raw, sha256.Sum256(raw), nil
}
func (s HistorySnapshot) CanonicalDigest() ([sha256.Size]byte, error) {
	_, d, e := s.Canonical()
	return d, e
}
func (s HistorySnapshot) Summary() map[string]int {
	out := map[string]int{}
	for _, k := range historyTables {
		out[k] = s.Manifest.Counts[k]
	}
	return out
}
func (s HistorySnapshot) Validate() error {
	if s.Manifest.SchemaVersion != HistorySchemaVersion || !validText(s.Manifest.SourceSystem, 160) || !validRevision.MatchString(s.Manifest.SourceRevision) || s.Manifest.SnapshotAt.IsZero() || len(s.Manifest.Counts) != len(historyTables) || len(s.Manifest.Digests) != len(historyTables) {
		return ErrInvalidSnapshot
	}
	for name, rows := range historyRows(s) {
		raw, e := json.Marshal(rows)
		d := sha256.Sum256(raw)
		if e != nil || s.Manifest.Counts[name] != sliceLength(rows) || s.Manifest.Digests[name] != hex.EncodeToString(d[:]) {
			return ErrInvalidSnapshot
		}
	}
	return uniqueHistory(s)
}
func PopulateHistoryManifest(s *HistorySnapshot, system, revision string, at time.Time) error {
	if s == nil {
		return ErrInvalidSnapshot
	}
	normalizeHistory(s)
	s.Manifest = HistoryManifest{SchemaVersion: HistorySchemaVersion, SourceSystem: system, SourceRevision: revision, SnapshotAt: at.UTC(), Counts: map[string]int{}, Digests: map[string]string{}}
	for name, rows := range historyRows(*s) {
		raw, e := json.Marshal(rows)
		if e != nil {
			return e
		}
		d := sha256.Sum256(raw)
		s.Manifest.Counts[name] = sliceLength(rows)
		s.Manifest.Digests[name] = hex.EncodeToString(d[:])
	}
	return s.Validate()
}
func ParseHistory(raw []byte) (HistorySnapshot, [sha256.Size]byte, error) {
	var s HistorySnapshot
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&s) != nil || !errors.Is(dec.Decode(&struct{}{}), io.EOF) || s.Validate() != nil {
		return HistorySnapshot{}, [sha256.Size]byte{}, ErrInvalidSnapshot
	}
	_, d, e := s.Canonical()
	return s, d, e
}
func SealHistoryToFile(s HistorySnapshot, path, keyPath string) ([sha256.Size]byte, error) {
	plain, d, e := s.Canonical()
	if e != nil {
		return d, e
	}
	key, e := ReadKey(keyPath)
	if e != nil {
		return d, e
	}
	sealed, e := seal(key, plain)
	if e != nil {
		return d, e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if e != nil {
		return d, errors.New("create encrypted history snapshot")
	}
	defer f.Close()
	if _, e = f.Write(sealed); e != nil {
		return d, e
	}
	if e = f.Sync(); e != nil {
		return d, e
	}
	return d, nil
}
func LoadHistoryFile(path, keyPath string) (HistorySnapshot, [sha256.Size]byte, error) {
	key, e := ReadKey(keyPath)
	if e != nil {
		return HistorySnapshot{}, [sha256.Size]byte{}, e
	}
	info, e := os.Stat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return HistorySnapshot{}, [sha256.Size]byte{}, errors.New("encrypted history snapshot must be a regular 0600 file")
	}
	sealed, e := os.ReadFile(path)
	if e != nil {
		return HistorySnapshot{}, [sha256.Size]byte{}, e
	}
	plain, e := open(key, sealed)
	if e != nil {
		return HistorySnapshot{}, [sha256.Size]byte{}, e
	}
	return ParseHistory(plain)
}
func historyRows(s HistorySnapshot) map[string]any {
	return map[string]any{"plans": s.Plans, "directory_group_chats": s.DirectoryChats, "directory_snapshots": s.DirectorySnapshots, "groups": s.Groups, "nodes": s.Nodes}
}
func uniqueHistory(s HistorySnapshot) error {
	seen := map[string]bool{}
	for _, x := range s.Plans {
		if x.ID < 1 || seen[fmt.Sprintf("p:%d", x.ID)] {
			return ErrInvalidSnapshot
		}
		seen[fmt.Sprintf("p:%d", x.ID)] = true
	}
	seen = map[string]bool{}
	for _, x := range s.DirectoryChats {
		if x.ChatReference == "" || seen[x.ChatReference] {
			return ErrInvalidSnapshot
		}
		seen[x.ChatReference] = true
	}
	seen = map[string]bool{}
	for _, x := range s.DirectorySnapshots {
		if x.ChatReference == "" || seen[x.ChatReference] {
			return ErrInvalidSnapshot
		}
		seen[x.ChatReference] = true
	}
	seen = map[string]bool{}
	for _, x := range s.Groups {
		if x.ID < 1 || seen[fmt.Sprint(x.ID)] {
			return ErrInvalidSnapshot
		}
		seen[fmt.Sprint(x.ID)] = true
	}
	seen = map[string]bool{}
	for _, x := range s.Nodes {
		if x.ID < 1 || seen[fmt.Sprint(x.ID)] || !json.Valid(x.ContentPackage) || !json.Valid(x.Attachments) {
			return ErrInvalidSnapshot
		}
		seen[fmt.Sprint(x.ID)] = true
	}
	return nil
}
func normalizeHistory(s *HistorySnapshot) {
	if s.Plans == nil {
		s.Plans = []HistoryPlan{}
	}
	if s.DirectoryChats == nil {
		s.DirectoryChats = []HistoryDirectoryChat{}
	}
	if s.DirectorySnapshots == nil {
		s.DirectorySnapshots = []HistoryDirectorySnapshot{}
	}
	if s.Groups == nil {
		s.Groups = []HistoryGroup{}
	}
	if s.Nodes == nil {
		s.Nodes = []HistoryNode{}
	}
	sort.Slice(s.Plans, func(i, j int) bool { return s.Plans[i].ID < s.Plans[j].ID })
	sort.Slice(s.DirectoryChats, func(i, j int) bool { return s.DirectoryChats[i].ChatReference < s.DirectoryChats[j].ChatReference })
	sort.Slice(s.DirectorySnapshots, func(i, j int) bool {
		return s.DirectorySnapshots[i].ChatReference < s.DirectorySnapshots[j].ChatReference
	})
	sort.Slice(s.Groups, func(i, j int) bool { return s.Groups[i].ID < s.Groups[j].ID })
	sort.Slice(s.Nodes, func(i, j int) bool { return s.Nodes[i].ID < s.Nodes[j].ID })
}
