// Package store owns Radar PostgreSQL persistence.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

type Postgres struct{}

var _ radarport.Repository = (*Postgres)(nil)
var _ radarport.MutationJournal = (*Postgres)(nil)

func NewPostgres() *Postgres { return &Postgres{} }

type scanner interface{ Scan(...any) error }

const linkColumns = `id,public_code,name,title,description,content_type,destination_url,media_id,auth_policy,status,version,created_by,updated_by,created_at,updated_at`

func scanLink(row scanner) (radar.Link, error) {
	var link radar.Link
	var destination *string
	var mediaID *int64
	err := row.Scan(&link.ID, &link.PublicCode, &link.Name, &link.Title, &link.Description, &link.Content.Type, &destination, &mediaID, &link.AuthPolicy, &link.Status, &link.Version, &link.CreatedBy, &link.UpdatedBy, &link.CreatedAt, &link.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return radar.Link{}, radarport.ErrNotFound
	}
	if err != nil {
		return radar.Link{}, mapError(err)
	}
	if destination != nil {
		link.Content.DestinationURL = *destination
	}
	if mediaID != nil {
		link.Content.MediaID = radar.MediaID(*mediaID)
	}
	if err = link.Validate(); err != nil {
		return radar.Link{}, radarport.ErrUnavailable
	}
	return link, nil
}

func (store *Postgres) Get(ctx context.Context, id radar.RadarID) (radar.Link, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radar.Link{}, err
	}
	return scanLink(tx.QueryRow(ctx, `SELECT `+linkColumns+` FROM radar_links WHERE id=$1`, id))
}

func (store *Postgres) GetByPublicCode(ctx context.Context, code radar.PublicCode) (radar.Link, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radar.Link{}, err
	}
	return scanLink(tx.QueryRow(ctx, `SELECT `+linkColumns+` FROM radar_links WHERE public_code=$1`, code))
}

func (store *Postgres) List(ctx context.Context, query radarport.ListQuery) (radarport.LinkPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.LinkPage{}, err
	}
	conditions := []string{"TRUE"}
	args := make([]any, 0, 8)
	add := func(column string, value any) {
		args = append(args, value)
		conditions = append(conditions, column+"=$"+strconv.Itoa(len(args)))
	}
	if query.Search != "" {
		args = append(args, "%"+escapeLike(query.Search)+"%")
		conditions = append(conditions, "(name ILIKE $"+strconv.Itoa(len(args))+" ESCAPE '\\' OR title ILIKE $"+strconv.Itoa(len(args))+" ESCAPE '\\' OR destination_url ILIKE $"+strconv.Itoa(len(args))+" ESCAPE '\\')")
	}
	if query.ContentType != "" {
		add("content_type", query.ContentType)
	}
	if query.Status != "" {
		add("status", query.Status)
	}
	if query.AuthPolicy != "" {
		add("auth_policy", query.AuthPolicy)
	}
	if query.CreatedAfter != nil {
		args = append(args, query.CreatedAfter.UTC())
		conditions = append(conditions, "created_at >= $"+strconv.Itoa(len(args)))
	}
	if query.CreatedBefore != nil {
		args = append(args, query.CreatedBefore.UTC())
		conditions = append(conditions, "created_at < $"+strconv.Itoa(len(args)))
	}
	where := strings.Join(conditions, " AND ")
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM radar_links WHERE `+where, args...).Scan(&total); err != nil {
		return radarport.LinkPage{}, mapError(err)
	}
	args = append(args, query.Limit, query.Offset)
	rows, err := tx.Query(ctx, `SELECT `+linkColumns+` FROM radar_links WHERE `+where+` ORDER BY updated_at DESC,id DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return radarport.LinkPage{}, mapError(err)
	}
	defer rows.Close()
	items := make([]radarport.LinkSummary, 0)
	for rows.Next() {
		link, scanErr := scanLink(rows)
		if scanErr != nil {
			return radarport.LinkPage{}, scanErr
		}
		items = append(items, radarport.LinkSummary{Link: link, StatisticsStatus: radarport.LinkStatisticsReady})
	}
	if err = rows.Err(); err != nil {
		return radarport.LinkPage{}, mapError(err)
	}
	rows.Close()
	store.populateLinkSummaries(ctx, tx, items)
	return radarport.LinkPage{Items: items, Total: total, Limit: query.Limit, Offset: query.Offset, HasMore: int64(query.Offset)+int64(len(items)) < total}, nil
}

// populateLinkSummaries reads statistics for one page in one aggregate query.
// A statistics failure must not replace a known link page with invented zeroes:
// callers receive the link records with an explicit unavailable status instead.
func (store *Postgres) populateLinkSummaries(ctx context.Context, tx pgx.Tx, items []radarport.LinkSummary) {
	if len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	positions := make(map[radar.RadarID]int, len(items))
	for index := range items {
		ids = append(ids, int64(items[index].Link.ID))
		positions[items[index].Link.ID] = index
	}
	statisticsTx, err := tx.Begin(ctx)
	if err != nil {
		markLinkStatisticsUnavailable(items)
		return
	}
	rows, err := statisticsTx.Query(ctx, `SELECT radar_id,
		count(*) FILTER(WHERE stage='landing'),
		count(DISTINCT customer_id) FILTER(WHERE attribution_status='resolved'),
		count(*) FILTER(WHERE stage IN ('content_opened','redirected','image_loaded','pdf_opened') AND attribution_status='resolved'),
		count(*) FILTER(WHERE stage IN ('content_opened','redirected','image_loaded','pdf_opened')),
		max(occurred_at) FILTER(WHERE stage IN ('content_opened','redirected','image_loaded','pdf_opened'))
		FROM radar_events
		WHERE radar_id = ANY($1)
		GROUP BY radar_id`, ids)
	if err != nil {
		_ = statisticsTx.Rollback(ctx)
		markLinkStatisticsUnavailable(items)
		return
	}
	for rows.Next() {
		var id radar.RadarID
		var totalLandings, authorizedUsers, authorizedViews, viewCount int64
		var lastViewedAt *time.Time
		if err = rows.Scan(&id, &totalLandings, &authorizedUsers, &authorizedViews, &viewCount, &lastViewedAt); err != nil {
			rows.Close()
			_ = statisticsTx.Rollback(ctx)
			markLinkStatisticsUnavailable(items)
			return
		}
		index, ok := positions[id]
		if !ok {
			rows.Close()
			_ = statisticsTx.Rollback(ctx)
			markLinkStatisticsUnavailable(items)
			return
		}
		items[index].TotalLandings = totalLandings
		items[index].AuthorizedUsers = authorizedUsers
		items[index].AuthorizedViews = authorizedViews
		items[index].ViewCount = viewCount
		items[index].LastViewedAt = lastViewedAt
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		_ = statisticsTx.Rollback(ctx)
		markLinkStatisticsUnavailable(items)
		return
	}
	if err = statisticsTx.Commit(ctx); err != nil {
		_ = statisticsTx.Rollback(ctx)
		markLinkStatisticsUnavailable(items)
	}
}

func markLinkStatisticsUnavailable(items []radarport.LinkSummary) {
	for index := range items {
		items[index].StatisticsStatus = radarport.LinkStatisticsUnavailable
		items[index].TotalLandings = 0
		items[index].AuthorizedUsers = 0
		items[index].AuthorizedViews = 0
		items[index].ViewCount = 0
		items[index].LastViewedAt = nil
	}
}

func (store *Postgres) Create(ctx context.Context, record radarport.CreateRecord, actor int64, now time.Time) (radar.Link, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radar.Link{}, err
	}
	destination, mediaID := contentColumns(record.Content)
	link, err := scanLink(tx.QueryRow(ctx, `INSERT INTO radar_links(public_code,name,title,description,content_type,destination_url,media_id,auth_policy,status,version,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'draft',1,$9,$9,$10,$10) RETURNING `+linkColumns, record.PublicCode, record.Name, record.Title, record.Description, record.Content.Type, destination, mediaID, record.AuthPolicy, actor, now.UTC()))
	if err != nil {
		return radar.Link{}, err
	}
	if err = insertVersion(ctx, tx, link, actor, now); err != nil {
		return radar.Link{}, err
	}
	return link, nil
}

func (store *Postgres) Save(ctx context.Context, link radar.Link, expected radar.LinkVersion, actor int64, now time.Time) (radar.Link, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radar.Link{}, err
	}
	if err = link.Validate(); err != nil || link.Version != expected+1 {
		return radar.Link{}, radar.ErrInvalidArgument
	}
	destination, mediaID := contentColumns(link.Content)
	stored, err := scanLink(tx.QueryRow(ctx, `UPDATE radar_links SET name=$3,title=$4,description=$5,content_type=$6,destination_url=$7,media_id=$8,auth_policy=$9,status=$10,version=$11,updated_by=$12,updated_at=$13 WHERE id=$1 AND version=$2 RETURNING `+linkColumns, link.ID, expected, link.Name, link.Title, link.Description, link.Content.Type, destination, mediaID, link.AuthPolicy, link.Status, link.Version, actor, now.UTC()))
	if errors.Is(err, radarport.ErrNotFound) {
		return radar.Link{}, radarport.ErrConflict
	}
	if err != nil {
		return radar.Link{}, err
	}
	if stored.PublicCode != link.PublicCode {
		return radar.Link{}, radarport.ErrUnavailable
	}
	if err = insertVersion(ctx, tx, stored, actor, now); err != nil {
		return radar.Link{}, err
	}
	return stored, nil
}

func (store *Postgres) ReserveOperation(ctx context.Context, receipt radarport.OperationReceipt, now time.Time) (radarport.OperationReceipt, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.OperationReceipt{}, false, err
	}
	var stored radarport.OperationReceipt
	var keyDigest, payloadDigest []byte
	err = tx.QueryRow(ctx, `INSERT INTO radar_operation_receipts(operation,actor_id,key_digest,payload_digest,state,created_at) VALUES($1,$2,$3,$4,'in_progress',$5) ON CONFLICT(operation,actor_id,key_digest) DO NOTHING RETURNING id,operation,actor_id,key_digest,payload_digest,state,COALESCE(radar_id,0),COALESCE(version,0),completed_at`, receipt.Operation, receipt.ActorID, receipt.KeyDigest[:], receipt.PayloadDigest[:], now.UTC()).Scan(&stored.ID, &stored.Operation, &stored.ActorID, &keyDigest, &payloadDigest, &stored.State, &stored.RadarID, &stored.Version, &stored.CompletedAt)
	if err == nil {
		if !copyDigest(&stored.KeyDigest, keyDigest) || !copyDigest(&stored.PayloadDigest, payloadDigest) {
			return radarport.OperationReceipt{}, false, radarport.ErrUnavailable
		}
		return stored, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return radarport.OperationReceipt{}, false, mapError(err)
	}
	err = tx.QueryRow(ctx, `SELECT id,operation,actor_id,key_digest,payload_digest,state,COALESCE(radar_id,0),COALESCE(version,0),completed_at FROM radar_operation_receipts WHERE operation=$1 AND actor_id=$2 AND key_digest=$3`, receipt.Operation, receipt.ActorID, receipt.KeyDigest[:]).Scan(&stored.ID, &stored.Operation, &stored.ActorID, &keyDigest, &payloadDigest, &stored.State, &stored.RadarID, &stored.Version, &stored.CompletedAt)
	if err != nil {
		return radarport.OperationReceipt{}, true, mapError(err)
	}
	if !copyDigest(&stored.KeyDigest, keyDigest) || !copyDigest(&stored.PayloadDigest, payloadDigest) {
		return radarport.OperationReceipt{}, true, radarport.ErrUnavailable
	}
	return stored, true, nil
}

func (store *Postgres) CompleteOperation(ctx context.Context, receiptID int64, radarID radar.RadarID, version radar.LinkVersion, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	var snapshot []byte
	if err = tx.QueryRow(ctx, `SELECT to_jsonb(radar_links) FROM radar_links WHERE id=$1 AND version=$2`, radarID, version).Scan(&snapshot); err != nil {
		return mapError(err)
	}
	result, err := tx.Exec(ctx, `UPDATE radar_operation_receipts SET state='completed',radar_id=$2,version=$3,result_snapshot=$4,completed_at=$5 WHERE id=$1 AND state='in_progress'`, receiptID, radarID, version, snapshot, now.UTC())
	if err != nil {
		return mapError(err)
	}
	if result.RowsAffected() != 1 {
		return radarport.ErrConflict
	}
	return nil
}

func (store *Postgres) AppendAudit(ctx context.Context, record radarport.AuditRecord) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO radar_audit_events(operation,radar_id,version,actor_id,payload_digest,occurred_at) VALUES($1,$2,$3,$4,$5,$6)`, record.Operation, record.RadarID, record.Version, record.ActorID, record.PayloadDigest[:], record.OccurredAt.UTC())
	return mapError(err)
}

func (store *Postgres) AppendOutbox(ctx context.Context, record radarport.OutboxRecord) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO radar_outbox(event_id,event_type,aggregate_id,aggregate_version,payload,idempotency_digest,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, record.EventID, record.EventType, record.AggregateID, record.AggregateVer, record.Payload, record.IdempotencyDigest[:], record.OccurredAt.UTC())
	return mapError(err)
}

func insertVersion(ctx context.Context, tx pgx.Tx, link radar.Link, actor int64, now time.Time) error {
	snapshot, err := json.Marshal(link)
	if err != nil {
		return radarport.ErrUnavailable
	}
	_, err = tx.Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,$2,$3,$4,$5)`, link.ID, link.Version, snapshot, actor, now.UTC())
	return mapError(err)
}

func contentColumns(content radar.Content) (*string, *int64) {
	if content.Type == radar.ContentTypeLink {
		value := content.DestinationURL
		return &value, nil
	}
	value := int64(content.MediaID)
	return nil, &value
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return radarport.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23514", "23503":
			return radarport.ErrConflict
		}
	}
	return err
}

func copyDigest(target *[32]byte, value []byte) bool {
	if len(value) != len(target) {
		return false
	}
	copy(target[:], value)
	return true
}
