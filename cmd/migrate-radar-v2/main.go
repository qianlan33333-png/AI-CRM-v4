package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
)

const donorCommit = "6bfbe5816bb89913c70adaca87d6a486260e016e"
const donorSystem = "AI-CRM-" + "v2"

type sourceLink struct {
	ID             int64     `json:"id"`
	PublicCode     string    `json:"public_code"`
	Name           string    `json:"name"`
	Title          string    `json:"title"`
	DestinationURL string    `json:"destination_url"`
	Status         string    `json:"status"`
	CoverImageID   *int64    `json:"cover_image_id"`
	AttachmentID   *int64    `json:"attachment_id"`
	Version        int64     `json:"version"`
	CreatedBy      int64     `json:"created_by"`
	UpdatedBy      int64     `json:"updated_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type sourceEvent struct {
	SourceTable string    `json:"source_table"`
	ID          int64     `json:"id"`
	LinkID      int64     `json:"link_id"`
	Stage       string    `json:"stage"`
	Page        *int32    `json:"page"`
	CreatedAt   time.Time `json:"created_at"`
}
type snapshot struct {
	Schema      int           `json:"schema"`
	DonorCommit string        `json:"donor_commit"`
	CapturedAt  time.Time     `json:"captured_at"`
	Links       []sourceLink  `json:"links"`
	Events      []sourceEvent `json:"events"`
	Digest      string        `json:"digest"`
}
type options struct {
	Mode, Snapshot, SourceDSN, SourceStream, BatchKey, Digest string
	ActorID                                                   int64
	Confirm                                                   bool
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "radar migration failed:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	migrationConfig, configErr := platformconfig.LoadRadarMigration()
	if configErr != nil {
		return configErr
	}
	flags := flag.NewFlagSet("migrate-radar-v2", flag.ContinueOnError)
	var o options
	flags.StringVar(&o.Mode, "mode", "inspect", "inspect|inspect-stream|dry-run|import|reconcile")
	flags.StringVar(&o.Snapshot, "snapshot", "", "safe JSON snapshot path")
	flags.StringVar(&o.SourceDSN, "source-dsn", migrationConfig.SourceDatabaseURL, "read-only v2 PostgreSQL DSN")
	flags.StringVar(&o.SourceStream, "source-stream", "", "safe psql marker stream path")
	flags.StringVar(&o.BatchKey, "batch-key", "", "unique reviewed import batch key")
	flags.StringVar(&o.Digest, "snapshot-sha256", "", "required digest confirmation for import")
	flags.Int64Var(&o.ActorID, "actor-id", 0, "migration administrator id")
	flags.BoolVar(&o.Confirm, "confirm", false, "confirm target mutation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if o.Mode == "inspect" {
		if o.SourceDSN == "" || o.Snapshot == "" {
			return errors.New("inspect requires --source-dsn and --snapshot")
		}
		s, err := extract(ctx, o.SourceDSN)
		if err != nil {
			return err
		}
		raw, _ := json.MarshalIndent(s, "", "  ")
		if err = os.WriteFile(o.Snapshot, append(raw, '\n'), 0600); err != nil {
			return err
		}
		return print(map[string]any{"mode": "inspect", "snapshot": o.Snapshot, "snapshot_sha256": s.Digest, "links": len(s.Links), "events": len(s.Events), "pii_fields_read": 0})
	}
	if o.Mode == "inspect-stream" {
		if o.SourceStream == "" || o.Snapshot == "" {
			return errors.New("inspect-stream requires --source-stream and --snapshot")
		}
		s, err := extractStream(o.SourceStream)
		if err != nil {
			return err
		}
		raw, _ := json.MarshalIndent(s, "", "  ")
		if err = os.WriteFile(o.Snapshot, append(raw, '\n'), 0600); err != nil {
			return err
		}
		return print(map[string]any{"mode": "inspect-stream", "snapshot": o.Snapshot, "snapshot_sha256": s.Digest, "links": len(s.Links), "events": len(s.Events), "pii_fields_read": 0})
	}
	if o.Snapshot == "" {
		return errors.New("--snapshot is required")
	}
	s, err := load(o.Snapshot)
	if err != nil {
		return err
	}
	report := preflight(s)
	if o.Mode == "dry-run" {
		return print(map[string]any{"mode": "dry-run", "snapshot_sha256": s.Digest, "report": report, "provider_calls": 0, "oneid_links_created": 0})
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	switch o.Mode {
	case "import":
		if !o.Confirm || o.ActorID < 1 || o.BatchKey == "" || !strings.EqualFold(o.Digest, s.Digest) {
			return errors.New("import requires --confirm, positive --actor-id, --batch-key and matching --snapshot-sha256")
		}
		result, err := apply(ctx, pool, s, o)
		if err != nil {
			return err
		}
		return print(map[string]any{"mode": "import", "snapshot_sha256": s.Digest, "result": result})
	case "reconcile":
		return reconcile(ctx, pool, s)
	default:
		return errors.New("unsupported mode")
	}
}

func extractStream(path string) (snapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return snapshot{}, err
	}
	defer file.Close()
	s := snapshot{Schema: 1, DonorCommit: donorCommit, Links: []sourceLink{}, Events: []sourceEvent{}}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	seenSnapshot := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "__AICRM_RADAR_SNAPSHOT__|"):
			if seenSnapshot {
				return snapshot{}, errors.New("duplicate radar snapshot marker")
			}
			s.CapturedAt, err = time.Parse(time.RFC3339Nano, strings.TrimPrefix(line, "__AICRM_RADAR_SNAPSHOT__|"))
			seenSnapshot = err == nil
		case strings.HasPrefix(line, "__AICRM_RADAR_LINK__|"):
			var row sourceLink
			err = decodeMarker(strings.TrimPrefix(line, "__AICRM_RADAR_LINK__|"), &row)
			if err == nil {
				s.Links = append(s.Links, row)
			}
		case strings.HasPrefix(line, "__AICRM_RADAR_EVENT__|"):
			var row sourceEvent
			err = decodeMarker(strings.TrimPrefix(line, "__AICRM_RADAR_EVENT__|"), &row)
			if err == nil {
				if row.SourceTable == "" {
					row.SourceTable = "radar_link_events"
				}
				s.Events = append(s.Events, row)
			}
		}
		if err != nil {
			return snapshot{}, err
		}
	}
	if err = scanner.Err(); err != nil {
		return snapshot{}, err
	}
	if !seenSnapshot {
		return snapshot{}, errors.New("radar snapshot marker unavailable")
	}
	s.Digest = digest(s)
	return s, nil
}

func decodeMarker(encoded string, target any) error {
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return errors.New("invalid radar source marker")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return errors.New("invalid radar source row")
	}
	return nil
}

func extract(ctx context.Context, dsn string) (snapshot, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return snapshot{}, err
	}
	defer pool.Close()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return snapshot{}, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,public_code,name,title,destination_url,cover_image_id,attachment_id,status,version,created_by,updated_by,created_at,updated_at FROM radar_links ORDER BY id`)
	if err != nil {
		return snapshot{}, errors.New("v2 radar_links schema unavailable")
	}
	links := []sourceLink{}
	for rows.Next() {
		var v sourceLink
		if err = rows.Scan(&v.ID, &v.PublicCode, &v.Name, &v.Title, &v.DestinationURL, &v.CoverImageID, &v.AttachmentID, &v.Status, &v.Version, &v.CreatedBy, &v.UpdatedBy, &v.CreatedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return snapshot{}, err
		}
		links = append(links, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return snapshot{}, err
	}
	events := []sourceEvent{}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass('public.radar_link_events') IS NOT NULL`).Scan(&exists); err != nil {
		return snapshot{}, err
	}
	if exists {
		eventRows, e := tx.Query(ctx, `SELECT id,link_id,stage,page_no,created_at FROM radar_link_events ORDER BY id`)
		if e != nil {
			return snapshot{}, e
		}
		for eventRows.Next() {
			var v sourceEvent
			if e = eventRows.Scan(&v.ID, &v.LinkID, &v.Stage, &v.Page, &v.CreatedAt); e != nil {
				eventRows.Close()
				return snapshot{}, e
			}
			events = append(events, v)
		}
		eventRows.Close()
		if e = eventRows.Err(); e != nil {
			return snapshot{}, e
		}
	}
	s := snapshot{Schema: 1, DonorCommit: donorCommit, CapturedAt: time.Now().UTC(), Links: links, Events: events}
	s.Digest = digest(s)
	if err = tx.Commit(ctx); err != nil {
		return snapshot{}, err
	}
	return s, nil
}
func load(path string) (snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return snapshot{}, err
	}
	var s snapshot
	if json.Unmarshal(raw, &s) != nil || s.Schema != 1 || s.DonorCommit != donorCommit || s.Digest == "" {
		return snapshot{}, errors.New("invalid radar snapshot")
	}
	if got := digest(s); !strings.EqualFold(got, s.Digest) {
		return snapshot{}, errors.New("radar snapshot digest mismatch")
	}
	return s, nil
}
func digest(s snapshot) string {
	s.Digest = ""
	s.CapturedAt = time.Time{}
	sort.Slice(s.Links, func(i, j int) bool { return s.Links[i].ID < s.Links[j].ID })
	sort.Slice(s.Events, func(i, j int) bool { return s.Events[i].ID < s.Events[j].ID })
	raw, _ := json.Marshal(s)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func preflight(s snapshot) map[string]int {
	out := map[string]int{"links": len(s.Links), "events": len(s.Events), "eligible_links": 0, "quarantine_links": 0, "legacy_events": 0}
	for _, v := range s.Links {
		if _, reason, err := adapt(v); err == nil {
			out["eligible_links"]++
		} else {
			out["quarantine_links"]++
			out["quarantine_links_"+reason]++
		}
	}
	linkIDs := map[int64]bool{}
	for _, v := range s.Links {
		if _, _, err := adapt(v); err == nil {
			linkIDs[v.ID] = true
		}
	}
	for _, e := range s.Events {
		if linkIDs[e.LinkID] && validEvent(e) {
			out["legacy_events"]++
		} else if !validEvent(e) {
			out["quarantine_events_invalid_event"]++
		} else {
			out["quarantine_events_link_unavailable"]++
		}
	}
	return out
}
func adapt(v sourceLink) (radar.Content, string, error) {
	if v.ID < 1 || !radar.PublicCode(v.PublicCode).Valid() || !validSourceText(v.Name, radar.MaxNameRunes) || !validSourceText(v.Title, radar.MaxTitleRunes) || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || v.UpdatedAt.Before(v.CreatedAt) {
		return radar.Content{}, "invalid_definition", radar.ErrInvalidArgument
	}
	switch {
	case v.CoverImageID == nil && v.AttachmentID == nil:
		c := radar.Content{Type: radar.ContentTypeLink, DestinationURL: v.DestinationURL}
		return c, "", c.Validate()
	case v.CoverImageID != nil && *v.CoverImageID > 0 && v.AttachmentID == nil:
		c := radar.Content{Type: radar.ContentTypeImage, MediaID: radar.MediaID(*v.CoverImageID)}
		return c, "", c.Validate()
	case v.AttachmentID != nil && *v.AttachmentID > 0 && v.CoverImageID == nil:
		c := radar.Content{Type: radar.ContentTypePDF, MediaID: radar.MediaID(*v.AttachmentID)}
		return c, "", c.Validate()
	default:
		return radar.Content{}, "ambiguous_media", radar.ErrInvalidArgument
	}
}

func validSourceText(value string, maximum int) bool {
	if value == "" || !utf8.ValidString(value) || value != strings.TrimSpace(value) || utf8.RuneCountInString(value) > maximum {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validEvent(v sourceEvent) bool {
	return (v.SourceTable == "radar_link_events" || v.SourceTable == "radar_click_events") &&
		v.ID > 0 && v.LinkID > 0 && !v.CreatedAt.IsZero() && validSourceText(v.Stage, 80)
}

func apply(ctx context.Context, pool *pgxpool.Pool, s snapshot, o options) (map[string]int, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	sum, _ := hex.DecodeString(s.Digest)
	var batchID int64
	var existingStatus string
	err = tx.QueryRow(ctx, `SELECT id,status FROM radar_migration_batches WHERE snapshot_digest=$1`, sum).Scan(&batchID, &existingStatus)
	if err == nil {
		if existingStatus != "imported" && existingStatus != "reconciled" {
			return nil, errors.New("existing radar import batch is incomplete")
		}
		var imported, quarantined int
		if err = tx.QueryRow(ctx, `SELECT imported_count,quarantined_count FROM radar_migration_batches WHERE id=$1`, batchID).Scan(&imported, &quarantined); err != nil {
			return nil, err
		}
		return map[string]int{"replayed": 1, "imported": imported, "quarantined": quarantined}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO radar_migration_batches(batch_key,source_system,donor_commit,snapshot_at,snapshot_digest,source_count,status,created_at) VALUES($1,$2,$3,$4,$5,$6,'importing',clock_timestamp()) RETURNING id`, o.BatchKey, donorSystem, donorCommit, s.CapturedAt, sum, len(s.Links)+len(s.Events)).Scan(&batchID)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{"imported_links": 0, "quarantined_links": 0, "legacy_events": 0, "quarantined_events": 0}
	targetBySource := map[int64]int64{}
	for _, v := range s.Links {
		content, reason, e := adapt(v)
		recordDigest := recordHash(v)
		if e != nil {
			if e = quarantine(ctx, tx, batchID, "radar_links", v.ID, reason, recordDigest); e != nil {
				return nil, e
			}
			counts["quarantined_links"]++
			continue
		}
		if content.Type == radar.ContentTypeImage {
			var ok bool
			e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_images WHERE id=$1 AND enabled)`, content.MediaID).Scan(&ok)
			if e != nil || !ok {
				if e = quarantine(ctx, tx, batchID, "radar_links", v.ID, "image_missing", recordDigest); e != nil {
					return nil, e
				}
				counts["quarantined_links"]++
				continue
			}
		}
		if content.Type == radar.ContentTypePDF {
			var ok bool
			e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_attachments WHERE id=$1 AND enabled)`, content.MediaID).Scan(&ok)
			if e != nil || !ok {
				if e = quarantine(ctx, tx, batchID, "radar_links", v.ID, "pdf_missing", recordDigest); e != nil {
					return nil, e
				}
				counts["quarantined_links"]++
				continue
			}
		}
		var targetID int64
		var disposition = "imported"
		var destination any
		var media any
		if content.Type == radar.ContentTypeLink {
			destination = content.DestinationURL
		} else {
			media = content.MediaID
		}
		e = tx.QueryRow(ctx, `INSERT INTO radar_links(public_code,name,title,description,content_type,destination_url,media_id,auth_policy,status,version,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,'',$4,$5,$6,'unionid_required','disabled',1,$7,$7,$8,$9) ON CONFLICT(public_code) DO NOTHING RETURNING id`, v.PublicCode, v.Name, v.Title, content.Type, destination, media, o.ActorID, v.CreatedAt.UTC(), v.UpdatedAt.UTC()).Scan(&targetID)
		if errors.Is(e, pgx.ErrNoRows) {
			if e = quarantine(ctx, tx, batchID, "radar_links", v.ID, "public_code_conflict", recordDigest); e != nil {
				return nil, e
			}
			counts["quarantined_links"]++
			continue
		} else if e == nil {
			snapshotJSON, _ := json.Marshal(map[string]any{"id": targetID, "public_code": v.PublicCode, "name": v.Name, "title": v.Title, "description": "", "content": content, "auth_policy": "unionid_required", "status": "disabled", "version": 1, "created_at": v.CreatedAt.UTC(), "updated_at": v.UpdatedAt.UTC()})
			_, e = tx.Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,1,$2,$3,$4)`, targetID, snapshotJSON, o.ActorID, v.UpdatedAt.UTC())
			counts["imported_links"]++
		}
		if e != nil {
			return nil, e
		}
		targetBySource[v.ID] = targetID
		_, e = tx.Exec(ctx, `INSERT INTO radar_migration_source_map(batch_id,source_table,source_pk,target_table,target_pk,record_digest,disposition,created_at) VALUES($1,'radar_links',$2,'radar_links',$3,$4,$5,clock_timestamp()) ON CONFLICT(batch_id,source_table,source_pk) DO NOTHING`, batchID, fmt.Sprint(v.ID), targetID, recordDigest, disposition)
		if e != nil {
			return nil, e
		}
	}
	for _, v := range s.Events {
		sourceTable := v.SourceTable
		if sourceTable == "" {
			sourceTable = "radar_link_events"
		}
		if !validEvent(sourceEvent{SourceTable: sourceTable, ID: v.ID, LinkID: v.LinkID, Stage: v.Stage, Page: v.Page, CreatedAt: v.CreatedAt}) {
			recordDigest := recordHash(v)
			if err = quarantine(ctx, tx, batchID, sourceTable, v.ID, "invalid_event", recordDigest); err != nil {
				return nil, err
			}
			counts["quarantined_events"]++
			continue
		}
		targetID := targetBySource[v.LinkID]
		if targetID == 0 {
			recordDigest := recordHash(v)
			if err = quarantine(ctx, tx, batchID, sourceTable, v.ID, "link_unavailable", recordDigest); err != nil {
				return nil, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO radar_migration_source_map(batch_id,source_table,source_pk,target_table,target_pk,record_digest,disposition,created_at) VALUES($1,$2,$3,'radar_legacy_events',NULL,$4,'quarantine',clock_timestamp())`, batchID, sourceTable, fmt.Sprint(v.ID), recordDigest); err != nil {
				return nil, err
			}
			counts["quarantined_events"]++
			continue
		}
		recordDigest := recordHash(v)
		summary, _ := json.Marshal(map[string]any{"page": v.Page})
		command, e := tx.Exec(ctx, `INSERT INTO radar_legacy_events(batch_id,source_table,source_pk,radar_id,source_stage,record_digest,safe_summary,occurred_at,imported_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp()) ON CONFLICT(batch_id,source_table,source_pk) DO NOTHING`, batchID, sourceTable, fmt.Sprint(v.ID), targetID, v.Stage, recordDigest, summary, v.CreatedAt.UTC())
		if e != nil {
			return nil, e
		}
		if command.RowsAffected() > 0 {
			counts["legacy_events"]++
		}
		_, e = tx.Exec(ctx, `INSERT INTO radar_migration_source_map(batch_id,source_table,source_pk,target_table,target_pk,record_digest,disposition,created_at) VALUES($1,$2,$3,'radar_legacy_events',NULL,$4,'unattributed',clock_timestamp()) ON CONFLICT(batch_id,source_table,source_pk) DO NOTHING`, batchID, sourceTable, fmt.Sprint(v.ID), recordDigest)
		if e != nil {
			return nil, e
		}
	}
	_, err = tx.Exec(ctx, `UPDATE radar_migration_batches SET imported_count=$2,quarantined_count=$3,status='imported',completed_at=clock_timestamp() WHERE id=$1`, batchID, counts["imported_links"]+counts["legacy_events"], counts["quarantined_links"]+counts["quarantined_events"])
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return counts, nil
}
func quarantine(ctx context.Context, tx pgx.Tx, batchID int64, table string, id int64, reason string, digest []byte) error {
	summary, _ := json.Marshal(map[string]any{"source_id": id})
	_, err := tx.Exec(ctx, `INSERT INTO radar_migration_quarantine(batch_id,source_table,source_pk,reason_code,safe_summary,record_digest,created_at) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp()) ON CONFLICT(batch_id,source_table,source_pk,reason_code) DO NOTHING`, batchID, table, fmt.Sprint(id), reason, summary, digest)
	return err
}
func reconcile(ctx context.Context, pool *pgxpool.Pool, s snapshot) error {
	sum, _ := hex.DecodeString(s.Digest)
	var batchID int64
	var status string
	var source, imported, quarantined, mappings, legacy int64
	err := pool.QueryRow(ctx, `SELECT b.id,b.status,b.source_count,b.imported_count,b.quarantined_count,(SELECT count(*) FROM radar_migration_source_map m WHERE m.batch_id=b.id),(SELECT count(*) FROM radar_legacy_events e WHERE e.batch_id=b.id) FROM radar_migration_batches b WHERE snapshot_digest=$1`, sum).Scan(&batchID, &status, &source, &imported, &quarantined, &mappings, &legacy)
	if err != nil {
		return err
	}
	reasons := map[string]int64{}
	rows, err := pool.Query(ctx, `SELECT reason_code,count(*) FROM radar_migration_quarantine WHERE batch_id=$1 GROUP BY reason_code ORDER BY reason_code`, batchID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var reason string
		var count int64
		if err = rows.Scan(&reason, &count); err != nil {
			return err
		}
		reasons[reason] = count
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return print(map[string]any{"mode": "reconcile", "snapshot_sha256": s.Digest, "status": status, "source_count": source, "imported_count": imported, "quarantined_count": quarantined, "quarantine_by_reason": reasons, "mapping_count": mappings, "legacy_event_count": legacy, "oneid_links_created": 0})
}
func recordHash(v any) []byte { raw, _ := json.Marshal(v); sum := sha256.Sum256(raw); return sum[:] }
func print(v any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(v)
}
