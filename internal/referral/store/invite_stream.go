package store

import (
	"context"
	"time"

	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type InviteStreamPosition struct {
	At time.Time
	ID int64
}
type InviteStreamQuery struct {
	CampaignID, InviterID, Watermark int64
	Paid                             bool
	After                            *InviteStreamPosition
	Limit                            int32
}
type InviteStreamPage struct {
	Items                 []referralport.InviteItem
	TotalCount, Watermark int64
}

// ReadInviteStreamWithin counts and reads the same membership snapshot in one
// statement. The immutable source watermark excludes later arrivals, while
// keyset continuation tolerates inserted or removed rows ahead of the position.
// Reversals update display state without removing retained invitation records.
func (r *Repository) ReadInviteStreamWithin(ctx context.Context, q InviteStreamQuery) (InviteStreamPage, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return InviteStreamPage{}, err
	}
	if q.CampaignID < 1 || q.InviterID < 1 || q.Watermark < 0 || q.Limit < 1 || q.Limit > 101 {
		return InviteStreamPage{}, ErrInvalid
	}
	facts := `SELECT p.customer_id AS key_id,p.customer_id,MIN(p.joined_at) AS joined_at,MIN(p.id) AS source_id,
 COALESCE(SUM((SELECT SUM(e.delta) FROM referral_score_events e WHERE e.participation_id=p.id)),0)::bigint AS delta
 FROM referral_participations p WHERE p.campaign_id=$1 AND p.inviter_customer_id=$2 AND p.customer_id<>$2 GROUP BY p.customer_id`
	if q.Paid {
		facts = `SELECT buyer_customer_id AS key_id,buyer_customer_id AS customer_id,
  MIN(occurred_at) FILTER (WHERE kind='credit') AS joined_at,MIN(id) FILTER (WHERE kind='credit') AS source_id,SUM(amount_delta_minor)::bigint AS delta
  FROM referral_product_sale_events WHERE campaign_id=$1 AND promoter_customer_id=$2 AND buyer_customer_id<>$2 GROUP BY buyer_customer_id`
	}
	var after any
	var afterID int64
	if q.After != nil {
		after = q.After.At
		afterID = q.After.ID
	}
	query := `WITH facts AS (` + facts + `), bound AS (
 SELECT COALESCE(NULLIF($3::bigint,0),MAX(source_id),0) AS watermark FROM facts
 ), eligible AS (SELECT facts.* FROM facts,bound WHERE source_id<=bound.watermark), summary AS (
 SELECT (SELECT COUNT(*) FROM eligible) AS total_count,(SELECT watermark FROM bound) AS watermark
 ) SELECT summary.total_count,summary.watermark,page.key_id,page.customer_id,page.joined_at,page.delta
 FROM summary LEFT JOIN LATERAL (
 SELECT * FROM eligible WHERE $4::timestamptz IS NULL OR (joined_at,key_id)<($4::timestamptz,$5::bigint)
 ORDER BY joined_at DESC,key_id DESC LIMIT $6
 ) page ON true ORDER BY page.joined_at DESC,page.key_id DESC`
	query, args, err := r.canonicalReportingWithin(ctx, q.CampaignID, query, []any{q.CampaignID, q.InviterID, q.Watermark, after, afterID, q.Limit}, 1)
	if err != nil {
		return InviteStreamPage{}, err
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return InviteStreamPage{}, mapError(err)
	}
	defer rows.Close()
	result := InviteStreamPage{Items: []referralport.InviteItem{}}
	for rows.Next() {
		var keyID, customerID, delta *int64
		var at *time.Time
		if err = rows.Scan(&result.TotalCount, &result.Watermark, &keyID, &customerID, &at, &delta); err != nil {
			return InviteStreamPage{}, mapError(err)
		}
		if keyID == nil {
			continue
		}
		state := "valid"
		if *delta < 1 {
			state = "reversed"
		}
		result.Items = append(result.Items, referralport.InviteItem{EntryID: *keyID, Participation: referraldomain.Participation{CampaignID: q.CampaignID, CustomerID: *customerID, JoinedAt: *at}, ScoreState: state, ScoreDelta: *delta})
	}
	if err = rows.Err(); err != nil {
		return InviteStreamPage{}, mapError(err)
	}
	return result, nil
}
