package store

import (
	"context"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

// AudienceFirstClicks uses the first resolved authorization or content fact.
// A successful authorization is already an attributable click even when later
// content delivery fails, and later events never reset its anchor. V3 radar
// events do not persist dd8's owner precedence inputs (identity primary owner,
// staff snapshot, or staff id), so OwnerUserID is deliberately empty.
func (Postgres) AudienceFirstClicks(ctx context.Context, reference time.Time) ([]radarport.AudienceFirstClick, error) {
	if reference.IsZero() {
		return nil, radar.ErrInvalidArgument
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH ranked AS (
		SELECT id,customer_id,radar_id,occurred_at,
			row_number() OVER (PARTITION BY customer_id,radar_id ORDER BY occurred_at,id) AS occurrence_rank
		FROM radar_events
		WHERE attribution_status='resolved' AND customer_id IS NOT NULL
			AND stage IN ('oauth_verified','identity_resolved','content_opened','redirected','image_loaded','pdf_opened')
			AND occurred_at <= $1
	)
	SELECT customer_id,radar_id,id,occurred_at,''::text AS owner_userid
	FROM ranked WHERE occurrence_rank=1 ORDER BY radar_id,customer_id`, reference.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []radarport.AudienceFirstClick{}
	for rows.Next() {
		var item radarport.AudienceFirstClick
		if err = rows.Scan(&item.CustomerID, &item.RadarID, &item.FirstClickEventID, &item.FirstClickedAt, &item.OwnerUserID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ radarport.AudienceFirstClickReader = Postgres{}
