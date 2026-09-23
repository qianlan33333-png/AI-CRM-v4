package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

var _ radarport.PublicRepository = (*Postgres)(nil)
var _ radarport.VisitorStore = (*Postgres)(nil)

func (store *Postgres) CreateOAuthState(ctx context.Context, digest [32]byte, state radarport.OAuthState, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO radar_oauth_states(state_digest,radar_id,radar_version,redirect_path,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6)`, digest[:], state.RadarID, state.Version, state.Path, state.Expires.UTC(), now.UTC())
	return mapError(err)
}

func (store *Postgres) ConsumeOAuthState(ctx context.Context, digest [32]byte, now time.Time) (radarport.OAuthState, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.OAuthState{}, err
	}
	var state radarport.OAuthState
	err = tx.QueryRow(ctx, `UPDATE radar_oauth_states SET consumed_at=$2 WHERE state_digest=$1 AND consumed_at IS NULL AND expires_at>$2 RETURNING radar_id,radar_version,redirect_path,expires_at`, digest[:], now.UTC()).Scan(&state.RadarID, &state.Version, &state.Path, &state.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return radarport.OAuthState{}, radarport.ErrNotFound
	}
	return state, mapError(err)
}

func (store *Postgres) CreateSession(ctx context.Context, digest [32]byte, session radarport.ViewSession, evidence [32]byte, now time.Time) (radarport.ViewSession, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.ViewSession{}, err
	}
	var identityID, customerID any
	var evidenceValue any
	if session.Attribution == radarport.AttributionResolved {
		identityID, customerID, evidenceValue = session.IdentityID, session.CustomerID, evidence[:]
	}
	err = tx.QueryRow(ctx, `INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,identity_id,customer_id,attribution_status,evidence_digest,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, digest[:], session.RadarID, session.Version, identityID, customerID, session.Attribution, evidenceValue, session.ExpiresAt.UTC(), now.UTC()).Scan(&session.ID)
	return session, mapError(err)
}

func (store *Postgres) ReadSession(ctx context.Context, digest [32]byte, radarID radar.RadarID, version radar.LinkVersion, now time.Time) (radarport.ViewSession, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.ViewSession{}, err
	}
	var session radarport.ViewSession
	var identityID, customerID *int64
	err = tx.QueryRow(ctx, `SELECT id,radar_id,radar_version,identity_id,customer_id,attribution_status,expires_at FROM radar_view_sessions WHERE session_digest=$1 AND radar_id=$2 AND radar_version=$3 AND revoked_at IS NULL AND expires_at>$4`, digest[:], radarID, version, now.UTC()).Scan(&session.ID, &session.RadarID, &session.Version, &identityID, &customerID, &session.Attribution, &session.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return radarport.ViewSession{}, radarport.ErrNotFound
	}
	if err != nil {
		return radarport.ViewSession{}, mapError(err)
	}
	if identityID != nil {
		session.IdentityID = *identityID
	}
	if customerID != nil {
		session.CustomerID = customerdomain.CustomerID(*customerID)
	}
	return session, nil
}

func (store *Postgres) AppendEvent(ctx context.Context, record radarport.EventRecord, now time.Time) (radarport.EventProjection, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.EventProjection{}, false, err
	}
	var identityID, customerID any
	if record.Attribution == radarport.AttributionResolved {
		identityID, customerID = record.IdentityID, record.CustomerID
	}
	command, err := tx.Exec(ctx, `INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,identity_id,customer_id,key_digest,payload_digest,failure_code,occurred_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13) ON CONFLICT(session_id,radar_version,stage) DO NOTHING`, record.ReceiptID, record.RadarID, record.Version, record.SessionID, record.Stage, record.Attribution, identityID, customerID, record.KeyDigest[:], record.PayloadDigest[:], record.FailureCode, record.OccurredAt.UTC(), now.UTC())
	if err != nil {
		return radarport.EventProjection{}, false, mapError(err)
	}
	var projection radarport.EventProjection
	var storedPayload []byte
	err = tx.QueryRow(ctx, `SELECT id,receipt_id,radar_id,radar_version,stage,attribution_status,COALESCE('cus_'||customer_id::text,''),occurred_at,payload_digest FROM radar_events WHERE session_id=$1 AND radar_version=$2 AND stage=$3`, record.SessionID, record.Version, record.Stage).Scan(&projection.EventID, &projection.ReceiptID, &projection.RadarID, &projection.Version, &projection.Stage, &projection.Attribution, &projection.CustomerRef, &projection.OccurredAt, &storedPayload)
	if err != nil {
		return radarport.EventProjection{}, false, mapError(err)
	}
	if len(storedPayload) != len(record.PayloadDigest) {
		return radarport.EventProjection{}, false, radarport.ErrConflict
	}
	for i := range storedPayload {
		if storedPayload[i] != record.PayloadDigest[i] {
			return radarport.EventProjection{}, false, radarport.ErrConflict
		}
	}
	return projection, command.RowsAffected() == 0, nil
}

func (store *Postgres) Stats(ctx context.Context, id radar.RadarID) (radarport.Stats, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.Stats{}, err
	}
	var stats radarport.Stats
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM radar_links WHERE id=$1)`, id).Scan(&exists); err != nil {
		return radarport.Stats{}, mapError(err)
	}
	if !exists {
		return radarport.Stats{}, radarport.ErrNotFound
	}
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE stage='landing'),count(DISTINCT customer_id) FILTER(WHERE attribution_status='resolved'),count(*) FILTER(WHERE stage IN ('content_opened','redirected','image_loaded','pdf_opened')),count(*) FILTER(WHERE stage IN ('content_opened','redirected','image_loaded','pdf_opened') AND attribution_status='resolved'),count(*) FILTER(WHERE stage='redirected'),count(*) FILTER(WHERE stage='image_loaded'),count(*) FILTER(WHERE stage='pdf_opened'),count(*) FILTER(WHERE stage='landing' AND occurred_at >= date_trunc('day',clock_timestamp())),count(*) FILTER(WHERE stage IN ('content_opened','redirected','image_loaded','pdf_opened') AND occurred_at >= date_trunc('day',clock_timestamp())),max(occurred_at) FILTER(WHERE stage IN ('content_opened','redirected','image_loaded','pdf_opened')) FROM radar_events WHERE radar_id=$1`, id).Scan(&stats.TotalEvents, &stats.TotalLandings, &stats.AuthorizedUsers, &stats.ViewCount, &stats.AuthorizedViews, &stats.Redirects, &stats.ImageLoaded, &stats.PDFOpened, &stats.TodayLandings, &stats.TodayViews, &stats.LastViewedAt)
	if err != nil {
		return radarport.Stats{}, mapError(err)
	}
	if stats.TotalLandings > 0 {
		stats.ConversionRate = float64(stats.AuthorizedUsers) / float64(stats.TotalLandings)
	}
	return stats, nil
}

func (store *Postgres) CustomerActivities(ctx context.Context, query radarport.CustomerActivityQuery) (radarport.CustomerActivityPage, error) {
	if query.CustomerID < 1 || query.Limit < 1 || query.Limit > 101 || query.Watermark.IsZero() || query.AfterID < 0 ||
		(query.AfterAt.IsZero() && query.AfterID != 0) || (!query.AfterAt.IsZero() && query.AfterAt.After(query.Watermark)) {
		return radarport.CustomerActivityPage{}, radar.ErrInvalidArgument
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.CustomerActivityPage{}, err
	}
	afterAt, afterID := query.AfterAt, query.AfterID
	if afterAt.IsZero() {
		afterAt, afterID = query.Watermark.UTC(), math.MaxInt64
	}
	rows, err := tx.Query(ctx, `SELECT e.id,e.radar_id,e.stage,e.occurred_at,l.title
		FROM radar_events e JOIN radar_links l ON l.id=e.radar_id
		WHERE e.customer_id=$1 AND e.attribution_status='resolved' AND e.occurred_at <= $2
		AND (e.occurred_at,e.id) < ($3,$4)
		AND (NOT $6::boolean OR e.stage IN ('landing','content_opened','redirected','image_loaded','pdf_opened'))
		ORDER BY e.occurred_at DESC,e.id DESC LIMIT $5`, int64(query.CustomerID), query.Watermark.UTC(), afterAt.UTC(), afterID, query.Limit, query.BusinessOnly)
	if err != nil {
		return radarport.CustomerActivityPage{}, mapError(err)
	}
	defer rows.Close()
	page := radarport.CustomerActivityPage{Items: []radarport.CustomerActivity{}}
	for rows.Next() {
		var item radarport.CustomerActivity
		if err = rows.Scan(&item.EventID, &item.RadarID, &item.Stage, &item.OccurredAt, &item.Title); err != nil {
			return radarport.CustomerActivityPage{}, mapError(err)
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return radarport.CustomerActivityPage{}, mapError(err)
	}
	return page, nil
}

var _ radarport.CustomerActivityStore = (*Postgres)(nil)

func (store *Postgres) Events(ctx context.Context, query radarport.EventQuery) (radarport.EventPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.EventPage{}, err
	}
	where := "radar_id=$1"
	args := []any{query.RadarID}
	add := func(sql string, value any) { args = append(args, value); where += fmt.Sprintf(" AND "+sql, len(args)) }
	if query.Stage != "" {
		add("stage=$%d", query.Stage)
	}
	if query.Attribution != "" {
		add("attribution_status=$%d", query.Attribution)
	}
	if query.Start != nil {
		add("occurred_at>=$%d", query.Start.UTC())
	}
	if query.End != nil {
		add("occurred_at<$%d", query.End.UTC())
	}
	var total int64
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM radar_links WHERE id=$1)`, query.RadarID).Scan(&exists); err != nil {
		return radarport.EventPage{}, mapError(err)
	}
	if !exists {
		return radarport.EventPage{}, radarport.ErrNotFound
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM radar_events WHERE `+where, args...).Scan(&total); err != nil {
		return radarport.EventPage{}, mapError(err)
	}
	args = append(args, query.Limit, query.Offset)
	rows, err := tx.Query(ctx, `SELECT id,receipt_id,radar_id,radar_version,stage,attribution_status,COALESCE('cus_'||customer_id::text,''),occurred_at FROM radar_events WHERE `+where+fmt.Sprintf(" ORDER BY occurred_at DESC,id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return radarport.EventPage{}, mapError(err)
	}
	defer rows.Close()
	items := make([]radarport.EventProjection, 0)
	for rows.Next() {
		var item radarport.EventProjection
		if err = rows.Scan(&item.EventID, &item.ReceiptID, &item.RadarID, &item.Version, &item.Stage, &item.Attribution, &item.CustomerRef, &item.OccurredAt); err != nil {
			return radarport.EventPage{}, mapError(err)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return radarport.EventPage{}, mapError(err)
	}
	return radarport.EventPage{Items: items, Total: total, Limit: query.Limit, Offset: query.Offset, HasMore: int64(query.Offset)+int64(len(items)) < total}, nil
}

func (store *Postgres) Visitors(ctx context.Context, query radarport.VisitorQuery) (radarport.VisitorSessionPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.VisitorSessionPage{}, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM radar_links WHERE id=$1)`, query.RadarID).Scan(&exists); err != nil {
		return radarport.VisitorSessionPage{}, mapError(err)
	}
	if !exists {
		return radarport.VisitorSessionPage{}, radarport.ErrNotFound
	}
	ids := make([]int64, 0, len(query.CustomerIDs))
	seen := make(map[customerdomain.CustomerID]struct{}, len(query.CustomerIDs))
	for _, id := range query.CustomerIDs {
		if id < 1 {
			return radarport.VisitorSessionPage{}, radar.ErrInvalidArgument
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
	}
	// A caller that supplies candidates is necessarily filtering. This keeps a
	// future direct store caller from accidentally widening a non-empty search
	// to every visitor merely because it omitted FilterApplied.
	filterApplied := query.FilterApplied || len(ids) > 0
	args := []any{query.RadarID, filterApplied, ids}
	outerConditions := []string{"TRUE"}
	if query.Start != nil {
		args = append(args, query.Start.UTC())
		outerConditions = append(outerConditions, fmt.Sprintf("opened_at >= $%d", len(args)))
	}
	if query.End != nil {
		args = append(args, query.End.UTC())
		outerConditions = append(outerConditions, fmt.Sprintf("opened_at < $%d", len(args)))
	}
	base := `WITH visitor_sessions AS (
		SELECT event.session_id,session.radar_version,COALESCE(session.customer_id,0) AS customer_id,
			session.attribution_status,min(event.occurred_at) AS opened_at
		FROM radar_events event JOIN radar_view_sessions session
			ON session.id=event.session_id AND session.radar_version=event.radar_version
		WHERE event.radar_id=$1
			AND session.radar_id=$1
			AND event.stage=ANY(ARRAY['content_opened','redirected','image_loaded','pdf_opened']::text[])
			AND (NOT $2::boolean OR session.customer_id=ANY($3::bigint[]))
		GROUP BY event.session_id,event.radar_version,session.radar_version,session.customer_id,session.attribution_status
	)`
	// The aggregation must complete before [start,end) is evaluated so a later
	// image/PDF receipt cannot move a session's first successful opening.
	outerWhere := strings.Join(outerConditions, " AND ")
	var total int64
	if err = tx.QueryRow(ctx, base+` SELECT count(*) FROM visitor_sessions WHERE `+outerWhere, args...).Scan(&total); err != nil {
		return radarport.VisitorSessionPage{}, mapError(err)
	}
	args = append(args, query.Limit, query.Offset)
	rows, err := tx.Query(ctx, base+` SELECT session_id,radar_version,customer_id,attribution_status,opened_at FROM visitor_sessions WHERE `+outerWhere+fmt.Sprintf(" ORDER BY opened_at DESC,session_id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return radarport.VisitorSessionPage{}, mapError(err)
	}
	defer rows.Close()
	items := make([]radarport.VisitorSession, 0)
	for rows.Next() {
		var item radarport.VisitorSession
		if err = rows.Scan(&item.SessionID, &item.Version, &item.CustomerID, &item.Attribution, &item.OpenedAt); err != nil {
			return radarport.VisitorSessionPage{}, mapError(err)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return radarport.VisitorSessionPage{}, mapError(err)
	}
	return radarport.VisitorSessionPage{Items: items, Total: total, Limit: query.Limit, Offset: query.Offset, HasMore: int64(query.Offset)+int64(len(items)) < total}, nil
}
