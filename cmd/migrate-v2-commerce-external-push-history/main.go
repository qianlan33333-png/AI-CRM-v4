// Command migrate-v2-commerce-external-push-history preserves a sealed V2
// commerce external-push snapshot in Outbound's read-only history ledger. It
// never creates a current push intent, External Effect, Order event, or job.
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
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const (
	schemaVersion       = "aicrm-v2-commerce-external-push-history-v2"
	legacySchemaVersion = "aicrm-v2-commerce-external-push-history-v1"
	// This sentinel is a relationship classification, never a legacy job
	// terminal state. The full relationship remains inside the sealed snapshot.
	ambiguousEffectRelationState = "ambiguous_multiple_effect_jobs"
	// This is the source-system label used by the approved V2 product
	// definition importer. It lets the composition command read, but never
	// modify, its append-only Product source maps.
	sourceSystem = "ai-crm-production:150.158.82.186/openclaw_wecom"
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
type configRow struct {
	ID           int64           `json:"id"`
	TargetType   string          `json:"target_type"`
	TargetID     string          `json:"target_id"`
	EventType    string          `json:"event_type"`
	Enabled      bool            `json:"enabled"`
	WebhookURL   string          `json:"webhook_url"`
	PushType     string          `json:"push_type"`
	ExpiresAt    *int64          `json:"expires_at_ts,omitempty"`
	Day          *int64          `json:"day,omitempty"`
	Frequency    *int64          `json:"frequency,omitempty"`
	Remark       string          `json:"remark"`
	CustomParams json.RawMessage `json:"custom_params"`
	Secret       string          `json:"secret"`
	CreatedBy    string          `json:"created_by"`
	UpdatedBy    string          `json:"updated_by"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}
type effectJobRelation struct {
	ID         int64  `json:"id"`
	EffectType string `json:"effect_type"`
	State      string `json:"state"`
}

type deliveryRow struct {
	ID             int64           `json:"id"`
	ConfigID       int64           `json:"config_id"`
	EventType      string          `json:"event_type"`
	DeliveryID     string          `json:"delivery_id"`
	TargetType     string          `json:"target_type"`
	TargetID       string          `json:"target_id"`
	OrderID        int64           `json:"order_id"`
	ProductID      int64           `json:"product_id"`
	Status         string          `json:"status"`
	AttemptCount   int             `json:"attempt_count"`
	RequestURL     string          `json:"request_url"`
	RequestHeaders json.RawMessage `json:"request_headers"`
	RequestBody    json.RawMessage `json:"request_body"`
	ResponseStatus *int            `json:"response_status,omitempty"`
	ResponseBody   string          `json:"response_body"`
	ErrorMessage   string          `json:"error_message"`
	NextRetryAt    *time.Time      `json:"next_retry_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	// EffectJobs is the V2 snapshot relation. It records every matching
	// external_effect_job, including the zero- and multi-job cases.
	EffectJobs []effectJobRelation `json:"-"`
	// These fields occur only in a sealed v1 snapshot. They keep that immutable
	// legacy artifact readable without reinterpreting its source digest.
	EffectJobID *int64  `json:"effect_job_id,omitempty"`
	EffectState *string `json:"effect_state,omitempty"`
}

// deliveryRowWire deliberately keeps the V1 wire field order. The legacy
// snapshot never contained effect_jobs; V2 includes it only when the decoded
// or extracted slice is non-nil, including an empty [] relation.
type deliveryRowWire struct {
	ID             int64                `json:"id"`
	ConfigID       int64                `json:"config_id"`
	EventType      string               `json:"event_type"`
	DeliveryID     string               `json:"delivery_id"`
	TargetType     string               `json:"target_type"`
	TargetID       string               `json:"target_id"`
	OrderID        int64                `json:"order_id"`
	ProductID      int64                `json:"product_id"`
	Status         string               `json:"status"`
	AttemptCount   int                  `json:"attempt_count"`
	RequestURL     string               `json:"request_url"`
	RequestHeaders json.RawMessage      `json:"request_headers"`
	RequestBody    json.RawMessage      `json:"request_body"`
	ResponseStatus *int                 `json:"response_status,omitempty"`
	ResponseBody   string               `json:"response_body"`
	ErrorMessage   string               `json:"error_message"`
	NextRetryAt    *time.Time           `json:"next_retry_at,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
	EffectJobID    *int64               `json:"effect_job_id,omitempty"`
	EffectState    *string              `json:"effect_state,omitempty"`
	EffectJobs     *[]effectJobRelation `json:"effect_jobs,omitempty"`
}

func (row deliveryRow) MarshalJSON() ([]byte, error) {
	wire := deliveryRowWire{
		ID: row.ID, ConfigID: row.ConfigID, EventType: row.EventType, DeliveryID: row.DeliveryID,
		TargetType: row.TargetType, TargetID: row.TargetID, OrderID: row.OrderID, ProductID: row.ProductID,
		Status: row.Status, AttemptCount: row.AttemptCount, RequestURL: row.RequestURL,
		RequestHeaders: row.RequestHeaders, RequestBody: row.RequestBody, ResponseStatus: row.ResponseStatus,
		ResponseBody: row.ResponseBody, ErrorMessage: row.ErrorMessage, NextRetryAt: row.NextRetryAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, EffectJobID: row.EffectJobID, EffectState: row.EffectState,
	}
	if row.EffectJobs != nil {
		effectJobs := row.EffectJobs
		wire.EffectJobs = &effectJobs
	}
	return json.Marshal(wire)
}

func (row *deliveryRow) UnmarshalJSON(raw []byte) error {
	var wire deliveryRowWire
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil || !errors.Is(dec.Decode(&struct{}{}), io.EOF) {
		return errors.New("invalid delivery row")
	}
	*row = deliveryRow{
		ID: wire.ID, ConfigID: wire.ConfigID, EventType: wire.EventType, DeliveryID: wire.DeliveryID,
		TargetType: wire.TargetType, TargetID: wire.TargetID, OrderID: wire.OrderID, ProductID: wire.ProductID,
		Status: wire.Status, AttemptCount: wire.AttemptCount, RequestURL: wire.RequestURL,
		RequestHeaders: wire.RequestHeaders, RequestBody: wire.RequestBody, ResponseStatus: wire.ResponseStatus,
		ResponseBody: wire.ResponseBody, ErrorMessage: wire.ErrorMessage, NextRetryAt: wire.NextRetryAt,
		CreatedAt: wire.CreatedAt, UpdatedAt: wire.UpdatedAt, EffectJobID: wire.EffectJobID, EffectState: wire.EffectState,
	}
	if wire.EffectJobs != nil {
		row.EffectJobs = *wire.EffectJobs
	}
	return nil
}

type outboxRow struct {
	ID            int64           `json:"id"`
	EventType     string          `json:"event_type"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
	Status        string          `json:"status"`
	RetryCount    int             `json:"retry_count"`
	NextRetryAt   *time.Time      `json:"next_retry_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}
type snapshot struct {
	Manifest   manifest      `json:"manifest"`
	Configs    []configRow   `json:"configs"`
	Deliveries []deliveryRow `json:"deliveries"`
	Outbox     []outboxRow   `json:"domain_event_outbox"`
}
type disposition struct {
	digest          []byte
	outcome, reason string
	targetProductID *int64
}
type result struct{ Input, Imported, Pending, Excluded, Replayed int }

type historyFact struct {
	kind                                        string
	id                                          int64
	configID                                    *int64
	deliveryID, eventType, targetType, targetID string
	orderKind, orderScope, orderKey             string
	orderID, productID                          *int64
	state                                       string
	attempts                                    int
	effectID                                    *int64
	effectState                                 *string
	responseStatus                              *int
	errorMessage                                string
	responseBodyProtected                       bool
	created, updated                            time.Time
	canonical                                   any
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate-v2-commerce-external-push-history", flag.ContinueOnError)
	mode := fs.String("mode", "inspect", "extract|inspect|dry-run|apply|verify")
	snapshotPath := fs.String("snapshot", "", "protected V2 commerce push snapshot")
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
		return writeSummary(map[string]any{"mode": "inspect", "manifest_sha256": hex.EncodeToString(digest[:]), "counts": s.Manifest.Counts, "mapping": summarizeWithoutTarget(s)})
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
		return writeSummary(map[string]any{"mode": "dry-run", "eligible": true, "manifest_sha256": hex.EncodeToString(digest[:]), "mapping": summarizeWithoutTarget(s)})
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
	var s snapshot
	if err = tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&s.Manifest.SnapshotAt); err != nil {
		return snapshot{}, errors.New("read source snapshot time")
	}
	rows, err := tx.Query(ctx, `SELECT id,target_type,target_id,event_type,enabled,webhook_url,push_type,expires_at_ts,day,frequency,remark,custom_params,secret,created_by,updated_by,created_at,updated_at FROM external_push_config WHERE target_type='product' ORDER BY id`)
	if err != nil {
		return snapshot{}, errors.New("source external_push_config contract unavailable")
	}
	for rows.Next() {
		var item configRow
		if err = rows.Scan(&item.ID, &item.TargetType, &item.TargetID, &item.EventType, &item.Enabled, &item.WebhookURL, &item.PushType, &item.ExpiresAt, &item.Day, &item.Frequency, &item.Remark, &item.CustomParams, &item.Secret, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return snapshot{}, errors.New("source external_push_config contract drift")
		}
		s.Configs = append(s.Configs, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return snapshot{}, errors.New("read source external_push_config")
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT d.id,d.config_id,d.event_type,d.delivery_id,d.target_type,d.target_id,d.order_id,d.product_id,d.status,d.attempt_count,d.request_url,d.request_headers,d.request_body,d.response_status,d.response_body,d.error_message,d.next_retry_at,d.created_at,d.updated_at,j.id,j.effect_type,j.status FROM external_push_delivery d LEFT JOIN external_effect_job j ON j.target_type='external_push_delivery' AND j.target_id=d.delivery_id AND j.effect_type IN ('webhook.order_paid.push','webhook.generic.push') ORDER BY d.id,j.id`)
	if err != nil {
		return snapshot{}, errors.New("source external_push_delivery or external_effect_job contract unavailable")
	}
	var current *deliveryRow
	for rows.Next() {
		var item deliveryRow
		var effectJobID *int64
		var effectType, effectState *string
		if err = rows.Scan(&item.ID, &item.ConfigID, &item.EventType, &item.DeliveryID, &item.TargetType, &item.TargetID, &item.OrderID, &item.ProductID, &item.Status, &item.AttemptCount, &item.RequestURL, &item.RequestHeaders, &item.RequestBody, &item.ResponseStatus, &item.ResponseBody, &item.ErrorMessage, &item.NextRetryAt, &item.CreatedAt, &item.UpdatedAt, &effectJobID, &effectType, &effectState); err != nil {
			rows.Close()
			return snapshot{}, errors.New("source external_push_delivery contract drift")
		}
		if current == nil || current.ID != item.ID {
			if current != nil {
				s.Deliveries = append(s.Deliveries, *current)
			}
			item.EffectJobs = make([]effectJobRelation, 0)
			current = &item
		}
		if effectJobID == nil {
			if effectType != nil || effectState != nil {
				rows.Close()
				return snapshot{}, errors.New("source external_effect_job contract drift")
			}
			continue
		}
		if effectType == nil || effectState == nil {
			rows.Close()
			return snapshot{}, errors.New("source external_effect_job contract drift")
		}
		current.EffectJobs = append(current.EffectJobs, effectJobRelation{ID: *effectJobID, EffectType: *effectType, State: *effectState})
	}
	if current != nil {
		s.Deliveries = append(s.Deliveries, *current)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return snapshot{}, errors.New("read source external_push_delivery")
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id,event_type,aggregate_type,aggregate_id,payload,status,retry_count,next_retry_at,created_at,updated_at FROM domain_event_outbox WHERE event_type='transaction.paid' AND aggregate_type='wechat_pay_order' ORDER BY id`)
	if err != nil {
		return snapshot{}, errors.New("source domain_event_outbox contract unavailable")
	}
	for rows.Next() {
		var item outboxRow
		if err = rows.Scan(&item.ID, &item.EventType, &item.AggregateType, &item.AggregateID, &item.Payload, &item.Status, &item.RetryCount, &item.NextRetryAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return snapshot{}, errors.New("source domain_event_outbox contract drift")
		}
		s.Outbox = append(s.Outbox, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return snapshot{}, errors.New("read source domain_event_outbox")
	}
	rows.Close()
	if err = populateManifest(&s, revision, s.Manifest.SnapshotAt); err != nil {
		return snapshot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return snapshot{}, errors.New("finish source snapshot")
	}
	return s, nil
}

func populateManifest(s *snapshot, revision string, at time.Time) error {
	if s == nil || !revisionPattern.MatchString(revision) || at.IsZero() {
		return errors.New("invalid source snapshot")
	}
	normalize(s)
	sets := map[string]any{"configs": s.Configs, "deliveries": s.Deliveries, "domain_event_outbox": s.Outbox}
	digests := map[string]string{}
	counts := map[string]int{}
	for name, value := range sets {
		raw, err := json.Marshal(value)
		if err != nil {
			return errors.New("canonicalize source snapshot")
		}
		sum := sha256.Sum256(raw)
		digests[name] = hex.EncodeToString(sum[:])
		switch v := value.(type) {
		case []configRow:
			counts[name] = len(v)
		case []deliveryRow:
			counts[name] = len(v)
		case []outboxRow:
			counts[name] = len(v)
		}
	}
	s.Manifest = manifest{SchemaVersion: schemaVersion, SourceSystem: sourceSystem, SourceRevision: revision, SnapshotAt: at.UTC(), Counts: counts, Digests: digests}
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
	sort.Slice(s.Configs, func(i, j int) bool { return s.Configs[i].ID < s.Configs[j].ID })
	sort.Slice(s.Deliveries, func(i, j int) bool { return s.Deliveries[i].ID < s.Deliveries[j].ID })
	sort.Slice(s.Outbox, func(i, j int) bool { return s.Outbox[i].ID < s.Outbox[j].ID })
	for i := range s.Configs {
		s.Configs[i].CreatedAt = s.Configs[i].CreatedAt.UTC()
		s.Configs[i].UpdatedAt = s.Configs[i].UpdatedAt.UTC()
		s.Configs[i].CustomParams = canonicalRaw(s.Configs[i].CustomParams)
	}
	for i := range s.Deliveries {
		s.Deliveries[i].CreatedAt = s.Deliveries[i].CreatedAt.UTC()
		s.Deliveries[i].UpdatedAt = s.Deliveries[i].UpdatedAt.UTC()
		if s.Deliveries[i].NextRetryAt != nil {
			x := s.Deliveries[i].NextRetryAt.UTC()
			s.Deliveries[i].NextRetryAt = &x
		}
		s.Deliveries[i].RequestHeaders = canonicalRaw(s.Deliveries[i].RequestHeaders)
		s.Deliveries[i].RequestBody = canonicalRaw(s.Deliveries[i].RequestBody)
		sort.Slice(s.Deliveries[i].EffectJobs, func(a, b int) bool { return s.Deliveries[i].EffectJobs[a].ID < s.Deliveries[i].EffectJobs[b].ID })
	}
	for i := range s.Outbox {
		s.Outbox[i].CreatedAt = s.Outbox[i].CreatedAt.UTC()
		s.Outbox[i].UpdatedAt = s.Outbox[i].UpdatedAt.UTC()
		if s.Outbox[i].NextRetryAt != nil {
			x := s.Outbox[i].NextRetryAt.UTC()
			s.Outbox[i].NextRetryAt = &x
		}
		s.Outbox[i].Payload = canonicalRaw(s.Outbox[i].Payload)
	}
}
func canonicalRaw(raw json.RawMessage) json.RawMessage {
	var out bytes.Buffer
	if !json.Valid(raw) || json.Compact(&out, raw) != nil {
		return raw
	}
	return append(json.RawMessage(nil), out.Bytes()...)
}
func validate(s snapshot) error {
	if (s.Manifest.SchemaVersion != schemaVersion && s.Manifest.SchemaVersion != legacySchemaVersion) || s.Manifest.SourceSystem != sourceSystem || !revisionPattern.MatchString(s.Manifest.SourceRevision) || s.Manifest.SnapshotAt.IsZero() || len(s.Manifest.Counts) != 3 || len(s.Manifest.Digests) != 3 || s.Manifest.Counts["configs"] != len(s.Configs) || s.Manifest.Counts["deliveries"] != len(s.Deliveries) || s.Manifest.Counts["domain_event_outbox"] != len(s.Outbox) {
		return errors.New("invalid source snapshot")
	}
	sets := map[string]any{"configs": s.Configs, "deliveries": s.Deliveries, "domain_event_outbox": s.Outbox}
	for name, value := range sets {
		raw, _ := json.Marshal(value)
		d := sha256.Sum256(raw)
		if s.Manifest.Digests[name] != hex.EncodeToString(d[:]) {
			return errors.New("invalid source snapshot")
		}
	}
	seen := map[string]map[int64]bool{"configs": {}, "deliveries": {}, "domain_event_outbox": {}}
	for _, x := range s.Configs {
		if x.ID < 1 || seen["configs"][x.ID] || x.TargetType != "product" || !validText(x.TargetID, 240) || !validText(x.EventType, 160) || !validText(x.WebhookURL, 4000) || !validText(x.PushType, 200) || !validText(x.Remark, 2000) || !validText(x.Secret, 4096) || !validText(x.CreatedBy, 160) || !validText(x.UpdatedBy, 160) || x.CreatedAt.IsZero() || x.UpdatedAt.IsZero() || !jsonObject(x.CustomParams) || x.Day != nil && *x.Day < 0 || x.Frequency != nil && *x.Frequency < 0 {
			return errors.New("invalid source snapshot")
		}
		seen["configs"][x.ID] = true
	}
	for _, x := range s.Deliveries {
		if x.ID < 1 || seen["deliveries"][x.ID] || x.ConfigID < 1 || !validText(x.EventType, 160) || !validText(x.DeliveryID, 200) || !validText(x.TargetType, 120) || !validText(x.TargetID, 240) || !validText(x.Status, 80) || x.AttemptCount < 0 || !validText(x.RequestURL, 4000) || !validText(x.ResponseBody, 16000) || !validText(x.ErrorMessage, 2000) || (x.ResponseStatus != nil && (*x.ResponseStatus < 100 || *x.ResponseStatus > 599)) || x.CreatedAt.IsZero() || x.UpdatedAt.IsZero() || !jsonObject(x.RequestHeaders) || !jsonObject(x.RequestBody) {
			return errors.New("invalid source snapshot")
		}
		if s.Manifest.SchemaVersion == legacySchemaVersion {
			if x.EffectJobs != nil || (x.EffectJobID == nil) != (x.EffectState == nil) || (x.EffectState != nil && !validText(*x.EffectState, 80)) {
				return errors.New("invalid source snapshot")
			}
		} else {
			if x.EffectJobs == nil || x.EffectJobID != nil || x.EffectState != nil {
				return errors.New("invalid source snapshot")
			}
			seenJobs := map[int64]bool{}
			for _, job := range x.EffectJobs {
				if job.ID < 1 || seenJobs[job.ID] || !validDeliveryEffectType(job.EffectType) || !validText(job.State, 80) {
					return errors.New("invalid source snapshot")
				}
				seenJobs[job.ID] = true
			}
		}
		seen["deliveries"][x.ID] = true
	}
	for _, x := range s.Outbox {
		if x.ID < 1 || seen["domain_event_outbox"][x.ID] || x.EventType != "transaction.paid" || x.AggregateType != "wechat_pay_order" || !validText(x.AggregateID, 240) || !validText(x.Status, 80) || x.RetryCount < 0 || x.CreatedAt.IsZero() || x.UpdatedAt.IsZero() || !jsonObject(x.Payload) {
			return errors.New("invalid source snapshot")
		}
		seen["domain_event_outbox"][x.ID] = true
	}
	return nil
}
func validText(v string, max int) bool {
	return v == strings.TrimSpace(v) && len(v) <= max && !strings.ContainsRune(v, '\x00')
}
func jsonObject(raw json.RawMessage) bool {
	var x map[string]json.RawMessage
	return json.Unmarshal(raw, &x) == nil
}

func validDeliveryEffectType(value string) bool {
	return value == "webhook.order_paid.push" || value == "webhook.generic.push"
}

func facts(s snapshot) []historyFact {
	out := make([]historyFact, 0, len(s.Configs)+len(s.Deliveries)+len(s.Outbox))
	for _, x := range s.Configs {
		id := x.ID
		out = append(out, historyFact{kind: "config", id: x.ID, configID: &id, eventType: x.EventType, targetType: x.TargetType, targetID: x.TargetID, state: boolState(x.Enabled), created: x.CreatedAt, updated: x.UpdatedAt, canonical: x})
	}
	for _, x := range s.Deliveries {
		configID := x.ConfigID
		product := x.ProductID
		var productPtr *int64
		if product > 0 {
			productPtr = &product
		}
		orderID := nullableNonnegative(x.OrderID)
		orderKind, orderScope, orderKey := "", "", ""
		if orderID != nil {
			// This is the old V2 order coordinate, not a V3 primary key. It
			// can be shown only when Order later exposes the identical historical
			// source_system/source_key pair through its stable read Port.
			orderKind, orderScope, orderKey = "wechat_pay_order", "commerce-history", strconv.FormatInt(*orderID, 10)
		}
		effectID, effectState := deliveryEffectProjection(x)
		out = append(out, historyFact{kind: "delivery", id: x.ID, configID: &configID, deliveryID: x.DeliveryID, eventType: x.EventType, targetType: x.TargetType, targetID: x.TargetID, orderKind: orderKind, orderScope: orderScope, orderKey: orderKey, orderID: orderID, productID: productPtr, state: x.Status, attempts: x.AttemptCount, effectID: effectID, effectState: effectState, responseStatus: x.ResponseStatus, errorMessage: x.ErrorMessage, responseBodyProtected: x.ResponseBody != "", created: x.CreatedAt, updated: x.UpdatedAt, canonical: x})
	}
	for _, x := range s.Outbox {
		out = append(out, historyFact{kind: "domain_event_outbox", id: x.ID, eventType: x.EventType, targetType: x.AggregateType, targetID: x.AggregateID, state: x.Status, attempts: x.RetryCount, created: x.CreatedAt, updated: x.UpdatedAt, canonical: x})
	}
	return out
}
func deliveryEffectProjection(row deliveryRow) (*int64, *string) {
	if row.EffectJobs == nil {
		return row.EffectJobID, row.EffectState
	}
	switch len(row.EffectJobs) {
	case 0:
		return nil, nil
	case 1:
		id, state := row.EffectJobs[0].ID, row.EffectJobs[0].State
		return &id, &state
	default:
		state := ambiguousEffectRelationState
		return nil, &state
	}
}

func deliveryEffectRelationCount(row deliveryRow) int {
	if row.EffectJobs != nil {
		return len(row.EffectJobs)
	}
	if row.EffectJobID != nil {
		return 1
	}
	return 0
}

func boolState(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}
func nullableNonnegative(v int64) *int64 {
	if v < 0 {
		return nil
	}
	return &v
}
func sourceDigest(f historyFact) ([]byte, error) {
	raw, err := json.Marshal(f.canonical)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}
func summarizeWithoutTarget(s snapshot) map[string]int {
	out := map[string]int{
		"pending":                           0,
		"excluded":                          len(s.Outbox),
		"candidates":                        len(s.Configs) + len(s.Deliveries),
		"delivery_effect_relation_none":     0,
		"delivery_effect_relation_single":   0,
		"delivery_effect_relation_multiple": 0,
	}
	for _, row := range s.Deliveries {
		switch deliveryEffectRelationCount(row) {
		case 0:
			out["delivery_effect_relation_none"]++
		case 1:
			out["delivery_effect_relation_single"]++
		default:
			out["delivery_effect_relation_multiple"]++
		}
	}
	return out
}

func dispositionFor(ctx context.Context, tx pgx.Tx, s snapshot, f historyFact) (disposition, error) {
	digest, err := sourceDigest(f)
	if err != nil {
		return disposition{}, errors.New("canonicalize source row")
	}
	out := disposition{digest: digest}
	if f.kind == "domain_event_outbox" {
		out.outcome = "excluded"
		out.reason = "legacy_domain_event_not_replayed"
		return out, nil
	}
	var sourceKey string
	if f.kind == "config" {
		sourceKey = f.targetID
	} else if f.productID != nil {
		sourceKey = strconv.FormatInt(*f.productID, 10)
	} else {
		sourceKey = f.targetID
	}
	if f.targetType != "product" || sourceKey == "" {
		out.outcome = "excluded"
		out.reason = "non_product_target"
		return out, nil
	}
	var productID int64
	err = tx.QueryRow(ctx, `SELECT target_id FROM config_definition_import_source_maps WHERE source_system=$1 AND domain='product' AND source_kind='wechat_pay_products' AND source_key=$2 AND target_table='products'`, s.Manifest.SourceSystem, sourceKey).Scan(&productID)
	if errors.Is(err, pgx.ErrNoRows) {
		out.outcome = "pending"
		out.reason = "product_mapping_unavailable"
		return out, nil
	}
	if err != nil {
		return disposition{}, errors.New("read product source mapping")
	}
	out.targetProductID = &productID
	out.outcome = "imported"
	out.reason = "mapped_product_history"
	return out, nil
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
	var out result
	err = tx.QueryRow(ctx, `SELECT id,input_count,imported_count,pending_count,excluded_count
FROM outbound_commerce_push_history_batches
WHERE source_system=$1 AND manifest_digest=$2 FOR UPDATE`, s.Manifest.SourceSystem, manifestDigest[:]).Scan(&batchID, &out.Input, &out.Imported, &out.Pending, &out.Excluded)
	if err == nil {
		if out.Input != len(facts(s)) || out.Input != out.Imported+out.Pending+out.Excluded {
			return result{}, errors.New("historical import drift")
		}
		out.Replayed = out.Input
		if err = tx.Commit(ctx); err != nil {
			return result{}, errors.New("finish historical import")
		}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result{}, errors.New("read historical import")
	}

	all := facts(s)
	out.Input = len(all)
	rows := make([]historySourceRow, len(all))
	for index, fact := range all {
		row, readErr := ensureHistorySourceRow(ctx, tx, s, fact)
		if readErr != nil {
			return result{}, readErr
		}
		rows[index] = row
		switch row.outcome {
		case "imported":
			out.Imported++
		case "pending":
			out.Pending++
		case "excluded":
			out.Excluded++
		default:
			return result{}, errors.New("invalid history disposition")
		}
	}
	err = tx.QueryRow(ctx, `INSERT INTO outbound_commerce_push_history_batches(source_system,source_revision,manifest_digest,snapshot_at,status,input_count,imported_count,pending_count,excluded_count,applied_at)
VALUES($1,$2,$3,$4,'applied',$5,$6,$7,$8,clock_timestamp()) RETURNING id`, s.Manifest.SourceSystem, s.Manifest.SourceRevision, manifestDigest[:], s.Manifest.SnapshotAt, out.Input, out.Imported, out.Pending, out.Excluded).Scan(&batchID)
	if err != nil {
		return result{}, errors.New("write historical import")
	}
	for _, row := range rows {
		if _, err = tx.Exec(ctx, `INSERT INTO outbound_commerce_push_history_batch_rows(batch_id,source_row_id,source_digest) VALUES($1,$2,$3)`, batchID, row.id, row.digest); err != nil {
			return result{}, errors.New("write historical import")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return result{}, errors.New("finish historical import")
	}
	return out, nil
}

type historySourceRow struct {
	id      int64
	digest  []byte
	outcome string
}

// ensureHistorySourceRow establishes source-row idempotency independently from
// the protected snapshot manifest. A second snapshot can contain the same V2
// row, but it cannot reinterpret that row or overwrite its initial read-only
// import disposition; a changed source digest is a hard reconciliation error.
func ensureHistorySourceRow(ctx context.Context, tx pgx.Tx, s snapshot, fact historyFact) (historySourceRow, error) {
	digest, err := sourceDigest(fact)
	if err != nil {
		return historySourceRow{}, errors.New("canonicalize source row")
	}
	var row historySourceRow
	err = tx.QueryRow(ctx, `SELECT id,source_digest,outcome FROM outbound_commerce_push_history_rows
WHERE source_system=$1 AND source_kind=$2 AND source_id=$3 FOR UPDATE`, s.Manifest.SourceSystem, fact.kind, fact.id).Scan(&row.id, &row.digest, &row.outcome)
	if err == nil {
		if !bytes.Equal(row.digest, digest) {
			return historySourceRow{}, errors.New("historical source row digest drift")
		}
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return historySourceRow{}, errors.New("read historical source row")
	}
	disposition, err := dispositionFor(ctx, tx, s, fact)
	if err != nil {
		return historySourceRow{}, err
	}
	row.digest, row.outcome = disposition.digest, disposition.outcome
	err = tx.QueryRow(ctx, `INSERT INTO outbound_commerce_push_history_rows(
source_system,source_kind,source_id,source_digest,source_config_id,source_delivery_id,source_event_type,source_target_type,source_target_id,
source_order_kind,source_order_scope,source_order_key,source_order_id,source_product_id,source_state,source_attempt_count,source_effect_job_id,source_effect_state,
source_response_status,source_error_message,source_response_body_protected,source_created_at,source_updated_at,target_product_id,outcome,reason_code,read_only)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,TRUE) RETURNING id`,
		s.Manifest.SourceSystem, fact.kind, fact.id, disposition.digest, fact.configID, fact.deliveryID, fact.eventType, fact.targetType, fact.targetID,
		fact.orderKind, fact.orderScope, fact.orderKey, fact.orderID, fact.productID, fact.state, fact.attempts, fact.effectID, fact.effectState,
		fact.responseStatus, fact.errorMessage, fact.responseBodyProtected, fact.created.UTC(), fact.updated.UTC(), disposition.targetProductID, disposition.outcome, disposition.reason).Scan(&row.id)
	if err != nil {
		return historySourceRow{}, errors.New("write historical source row")
	}
	return row, nil
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
	var out result
	err = tx.QueryRow(ctx, `SELECT id,input_count,imported_count,pending_count,excluded_count
FROM outbound_commerce_push_history_batches
WHERE source_system=$1 AND manifest_digest=$2 FOR UPDATE`, s.Manifest.SourceSystem, manifestDigest[:]).Scan(&batchID, &out.Input, &out.Imported, &out.Pending, &out.Excluded)
	if errors.Is(err, pgx.ErrNoRows) {
		return result{}, errors.New("historical import missing")
	}
	all := facts(s)
	if err != nil || out.Input != len(all) || out.Input != out.Imported+out.Pending+out.Excluded {
		return result{}, errors.New("historical import drift")
	}
	for _, fact := range all {
		if err = verifyHistorySourceRow(ctx, tx, batchID, s.Manifest.SourceSystem, fact); err != nil {
			return result{}, err
		}
	}
	var rows int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM outbound_commerce_push_history_batch_rows WHERE batch_id=$1`, batchID).Scan(&rows); err != nil || rows != out.Input {
		return result{}, errors.New("historical import drift")
	}
	if _, err = tx.Exec(ctx, `UPDATE outbound_commerce_push_history_batches SET status='reconciled',reconciled_at=clock_timestamp() WHERE id=$1`, batchID); err != nil {
		return result{}, errors.New("finish history reconciliation")
	}
	if err = tx.Commit(ctx); err != nil {
		return result{}, errors.New("finish history reconciliation")
	}
	return out, nil
}

func verifyHistorySourceRow(ctx context.Context, tx pgx.Tx, batchID int64, source string, fact historyFact) error {
	wantDigest, err := sourceDigest(fact)
	if err != nil {
		return errors.New("canonicalize source row")
	}
	var gotDigest, membershipDigest []byte
	var configID, orderID, productID, effectID, targetProductID *int64
	var deliveryID, eventType, targetType, targetID, orderKind, orderScope, orderKey, state, effectState, outcome, reason, errorMessage string
	var attempts int
	var responseStatus *int
	var responseBodyProtected, readonly bool
	var created, updated time.Time
	err = tx.QueryRow(ctx, `SELECT r.source_digest,membership.source_digest,r.source_config_id,r.source_delivery_id,r.source_event_type,r.source_target_type,r.source_target_id,
r.source_order_kind,r.source_order_scope,r.source_order_key,r.source_order_id,r.source_product_id,r.source_state,r.source_attempt_count,r.source_effect_job_id,
COALESCE(r.source_effect_state,''),r.source_response_status,r.source_error_message,r.source_response_body_protected,r.source_created_at,r.source_updated_at,
r.target_product_id,r.outcome,r.reason_code,r.read_only
FROM outbound_commerce_push_history_batch_rows membership
JOIN outbound_commerce_push_history_rows r ON r.id=membership.source_row_id
WHERE membership.batch_id=$1 AND r.source_system=$2 AND r.source_kind=$3 AND r.source_id=$4`, batchID, source, fact.kind, fact.id).Scan(
		&gotDigest, &membershipDigest, &configID, &deliveryID, &eventType, &targetType, &targetID, &orderKind, &orderScope, &orderKey, &orderID, &productID, &state, &attempts, &effectID,
		&effectState, &responseStatus, &errorMessage, &responseBodyProtected, &created, &updated, &targetProductID, &outcome, &reason, &readonly)
	if err != nil || !bytes.Equal(gotDigest, wantDigest) || !bytes.Equal(membershipDigest, wantDigest) || !sameOptionalInt(configID, fact.configID) || deliveryID != fact.deliveryID || eventType != fact.eventType || targetType != fact.targetType || targetID != fact.targetID || orderKind != fact.orderKind || orderScope != fact.orderScope || orderKey != fact.orderKey || !sameOptionalInt(orderID, fact.orderID) || !sameOptionalInt(productID, fact.productID) || state != fact.state || attempts != fact.attempts || !sameOptionalInt(effectID, fact.effectID) || !sameOptionalText(optionalText(effectState), fact.effectState) || !sameOptionalIntValue(responseStatus, fact.responseStatus) || errorMessage != fact.errorMessage || responseBodyProtected != fact.responseBodyProtected || !created.Equal(fact.created.UTC()) || !updated.Equal(fact.updated.UTC()) || !readonly {
		return errors.New("historical target drift")
	}
	// target_product_id/outcome/reason are frozen first-import conclusions. The
	// current Product source map cannot silently re-route an old V2 delivery.
	if outcome == "" || reason == "" || (outcome == "imported" && targetProductID == nil) || (outcome != "imported" && targetProductID != nil) {
		return errors.New("historical target drift")
	}
	return nil
}

func sameOptionalIntValue(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameOptionalInt(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func optionalText(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
func sameOptionalText(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
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
	var raw []byte
	for _, version := range []string{schemaVersion, legacySchemaVersion} {
		raw, err = aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(version))
		if err == nil {
			break
		}
	}
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
