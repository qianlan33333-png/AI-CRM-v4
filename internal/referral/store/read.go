package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type CampaignCounts struct {
	ParticipantCount, InvitationCount, TeamCount int64
}

func (r *Repository) ListCampaignsWithin(ctx context.Context, offset, limit int32, publicOnly bool) ([]referraldomain.Campaign, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if offset < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	query := `SELECT ` + campaignColumns + ` FROM referral_campaigns`
	if publicOnly {
		query += ` WHERE state <> 'draft'`
	}
	query += ` ORDER BY starts_at DESC,id DESC OFFSET $1 LIMIT $2`
	rows, err := tx.Query(ctx, query, offset, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referraldomain.Campaign, 0, limit)
	for rows.Next() {
		value, scanErr := scanCampaign(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

// ProductCampaignsAtWithin filters immutable started-campaign product and
// window configuration. The domain forbids changing these once started;
// lifecycle state still has to be reconstructed from audit facts.
func (r *Repository) ProductCampaignsAtWithin(ctx context.Context, productID int64, productType string, at time.Time) ([]referraldomain.Campaign, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if productID < 1 || (productType != referraldomain.ProductTypeStandard && productType != referraldomain.ProductTypeServicePeriod) || at.IsZero() {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+campaignColumns+` FROM referral_campaigns
WHERE qualification_mode='product_purchase' AND product_id=$1 AND product_type=$2
AND starts_at<=$3 AND ends_at>$3 ORDER BY id`, productID, productType, at.UTC())
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]referraldomain.Campaign, 0)
	for rows.Next() {
		campaign, scanErr := scanCampaign(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, campaign)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (r *Repository) CampaignCountsWithin(ctx context.Context, campaignID int64) (CampaignCounts, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return CampaignCounts{}, err
	}
	if campaignID < 1 {
		return CampaignCounts{}, ErrInvalid
	}
	var result CampaignCounts
	err = tx.QueryRow(ctx, `SELECT
        (SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND state='active'),
		(SELECT count(*) FROM referral_score_events c WHERE c.campaign_id=$1 AND c.kind='credit'
		 AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=c.id)),
        (SELECT count(*) FROM referral_teams WHERE campaign_id=$1)`, campaignID).Scan(&result.ParticipantCount, &result.InvitationCount, &result.TeamCount)
	if err != nil {
		return CampaignCounts{}, mapError(err)
	}
	return result, nil
}

func (r *Repository) CountDirectInvitationsWithin(ctx context.Context, campaignID, inviterCustomerID int64) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	if campaignID < 1 || inviterCustomerID < 1 {
		return 0, ErrInvalid
	}
	var count int64
	err = tx.QueryRow(ctx, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND inviter_customer_id=$2 AND state='active'`, campaignID, inviterCustomerID).Scan(&count)
	if err != nil {
		return 0, mapError(err)
	}
	return count, nil
}

// CountPaidInviteesWithin counts distinct buyers with a positive net paid
// amount in this campaign. Repeated orders count once; fully refunded buyers
// cease to count. The underlying immutable events preserve the original paid
// attribution even when a refund is settled on another day.
func (r *Repository) CountPaidInviteesWithin(ctx context.Context, campaignID, promoterCustomerID int64) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	if campaignID < 1 || promoterCustomerID < 1 {
		return 0, ErrInvalid
	}
	var count int64
	err = tx.QueryRow(ctx, `WITH paid_buyers AS (
		SELECT buyer_customer_id, SUM(amount_delta_minor) AS net_minor
		FROM referral_product_sale_events
		WHERE campaign_id=$1 AND promoter_customer_id=$2 AND buyer_customer_id<>$2
		GROUP BY buyer_customer_id
	) SELECT count(*) FROM paid_buyers WHERE net_minor>0`, campaignID, promoterCustomerID).Scan(&count)
	if err != nil {
		return 0, mapError(err)
	}
	return count, nil
}

// ListPaidInviteItemsWithin uses the same campaign, promoter, buyer and refund
// facts as CountPaidInviteesWithin. A fully refunded buyer remains visible in
// the detail list as reversed while no longer counting as an effective invite.
func (r *Repository) ListPaidInviteItemsWithin(ctx context.Context, campaignID, promoterCustomerID int64, offset, limit int32) ([]referralport.InviteItem, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 || promoterCustomerID < 1 || offset < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT buyer_customer_id,MIN(occurred_at) AS first_paid_at,SUM(amount_delta_minor)::bigint AS net_minor
		FROM referral_product_sale_events
		WHERE campaign_id=$1 AND promoter_customer_id=$2 AND buyer_customer_id<>$2
		GROUP BY buyer_customer_id
		ORDER BY first_paid_at DESC,buyer_customer_id DESC OFFSET $3 LIMIT $4`, campaignID, promoterCustomerID, offset, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	items := make([]referralport.InviteItem, 0, limit)
	for rows.Next() {
		var customerID, netMinor int64
		var paidAt time.Time
		if err = rows.Scan(&customerID, &paidAt, &netMinor); err != nil {
			return nil, mapError(err)
		}
		state := "valid"
		if netMinor <= 0 {
			state = "reversed"
		}
		items = append(items, referralport.InviteItem{Participation: referraldomain.Participation{CampaignID: campaignID, CustomerID: customerID, JoinedAt: paidAt}, ScoreState: state, ScoreDelta: netMinor})
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return items, nil
}

func (r *Repository) ListTeamsWithin(ctx context.Context, campaignID int64) ([]referraldomain.Team, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+teamColumns+` FROM referral_teams WHERE campaign_id=$1 ORDER BY id`, campaignID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referraldomain.Team, 0)
	for rows.Next() {
		value, scanErr := scanTeam(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func (r *Repository) CampaignDailyMetricsWithin(ctx context.Context, campaignID int64) ([]referralport.CampaignDailyMetric, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `WITH participants AS (
        SELECT (joined_at AT TIME ZONE 'Asia/Shanghai')::date AS day, count(*)::bigint AS participants
        FROM referral_participations WHERE campaign_id=$1 AND state='active' GROUP BY 1
    ), invitations AS (
        SELECT (occurred_at AT TIME ZONE 'Asia/Shanghai')::date AS day, count(*)::bigint AS invites
        FROM referral_score_events c WHERE campaign_id=$1 AND kind='credit'
          AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=c.id)
        GROUP BY 1
    ) SELECT COALESCE(p.day,i.day),COALESCE(p.participants,0),COALESCE(i.invites,0)
      FROM participants p FULL OUTER JOIN invitations i USING(day) ORDER BY 1 DESC`, campaignID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referralport.CampaignDailyMetric, 0)
	for rows.Next() {
		var value referralport.CampaignDailyMetric
		if err = rows.Scan(&value.Date, &value.ParticipantCount, &value.InviteCount); err != nil {
			return nil, mapError(err)
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func (r *Repository) MyParticipationWithin(ctx context.Context, campaignID, customerID int64) (referraldomain.Participation, error) {
	return r.ReadParticipationWithin(ctx, campaignID, customerID, false)
}

// HasTeamParticipationWithin is used when an activity is switched to
// individual mode. Existing team facts remain immutable audit history, so a
// publish to individual is allowed only when no participation has a team.
func (r *Repository) HasTeamParticipationWithin(ctx context.Context, campaignID int64) (bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return false, err
	}
	if campaignID < 1 {
		return false, ErrInvalid
	}
	var found bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM referral_participations WHERE campaign_id=$1 AND team_id IS NOT NULL)`, campaignID).Scan(&found); err != nil {
		return false, mapError(err)
	}
	return found, nil
}

func (r *Repository) CurrentRelationshipAtWithin(ctx context.Context, customerID int64, at time.Time) (referraldomain.Relationship, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Relationship{}, false, err
	}
	if customerID < 1 || at.IsZero() {
		return referraldomain.Relationship{}, false, ErrInvalid
	}
	var result referraldomain.Relationship
	err = tx.QueryRow(ctx, `SELECT h.relationship_id,h.customer_id,h.referrer_customer_id,h.source_campaign_id,h.invitation_id,h.version,h.accepted_at
        FROM referral_relationship_history h
        WHERE h.customer_id=$1 AND h.accepted_at <= $2
        ORDER BY h.accepted_at DESC,h.id DESC LIMIT 1`, customerID, at.UTC()).Scan(&result.ID, &result.CustomerID, &result.ReferrerCustomerID, &result.SourceCampaignID, &result.InvitationID, &result.Version, &result.EffectiveAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return referraldomain.Relationship{}, false, nil
	}
	if err != nil {
		return referraldomain.Relationship{}, false, mapError(err)
	}
	if !result.Valid() {
		return referraldomain.Relationship{}, false, referralport.ErrUnavailable
	}
	return result, true, nil
}

func (r *Repository) ListInviteItemsWithin(ctx context.Context, campaignID, inviterCustomerID int64, offset, limit int32) ([]referralport.InviteItem, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 || inviterCustomerID < 1 || offset < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+participationColumns+`,COALESCE((SELECT sum(delta) FROM referral_score_events e WHERE e.participation_id=p.id),0)
        FROM referral_participations p
        WHERE p.campaign_id=$1 AND p.inviter_customer_id=$2
        ORDER BY p.joined_at DESC,p.id DESC OFFSET $3 LIMIT $4`, campaignID, inviterCustomerID, offset, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referralport.InviteItem, 0, limit)
	for rows.Next() {
		var value referraldomain.Participation
		var state string
		var delta int64
		if err = rows.Scan(&value.ID, &value.CampaignID, &value.CustomerID, &value.TeamID, &value.InvitationID, &value.InviterCustomerID, &value.InviterTeamID, &state, &value.JoinedAt, &delta); err != nil {
			return nil, mapError(err)
		}
		value.State = referraldomain.ParticipationState(state)
		if !value.Valid() {
			return nil, referralport.ErrUnavailable
		}
		scoreState := "valid"
		if delta < 1 {
			scoreState = "reversed"
		}
		values = append(values, referralport.InviteItem{Participation: value, ScoreState: scoreState, ScoreDelta: delta})
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func (r *Repository) ListRelationshipHistoryWithin(ctx context.Context, customerID int64, offset, limit int32) ([]referraldomain.RelationshipHistory, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if customerID < 1 || offset < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT id,relationship_id,version,customer_id,COALESCE(previous_referrer_customer_id,0),referrer_customer_id,source_campaign_id,invitation_id,accepted_at
        FROM referral_relationship_history WHERE customer_id=$1 ORDER BY accepted_at DESC,id DESC OFFSET $2 LIMIT $3`, customerID, offset, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referraldomain.RelationshipHistory, 0, limit)
	for rows.Next() {
		var value referraldomain.RelationshipHistory
		if err = rows.Scan(&value.ID, &value.RelationshipID, &value.Version, &value.CustomerID, &value.PreviousReferrerCustomerID, &value.ReferrerCustomerID, &value.SourceCampaignID, &value.InvitationID, &value.AcceptedAt); err != nil {
			return nil, mapError(err)
		}
		if !value.Valid() {
			return nil, referralport.ErrUnavailable
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func (r *Repository) ListRewardsWithin(ctx context.Context, campaignID int64, offset, limit int32) ([]referraldomain.RewardRecord, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 || offset < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+rewardColumns+` FROM referral_reward_records WHERE campaign_id=$1 ORDER BY recorded_at DESC,id DESC OFFSET $2 LIMIT $3`, campaignID, offset, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referraldomain.RewardRecord, 0, limit)
	for rows.Next() {
		value, scanErr := scanReward(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func (r *Repository) ListAdminReferralsWithin(ctx context.Context, campaignID int64, offset, limit int32) ([]referralport.AdminReferralRecord, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 || offset < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	const adminParticipationColumns = `p.id,p.campaign_id,p.customer_id,COALESCE(p.team_id,0),COALESCE(p.invitation_id,0),COALESCE(p.inviter_customer_id,0),COALESCE(p.inviter_team_id,0),p.state,p.joined_at`
	rows, err := tx.Query(ctx, `SELECT `+adminParticipationColumns+`,c.name,COALESCE((SELECT id FROM referral_score_events e WHERE e.participation_id=p.id AND e.kind='credit'),0),COALESCE((SELECT sum(delta) FROM referral_score_events e WHERE e.participation_id=p.id),0)
        FROM referral_participations p JOIN referral_campaigns c ON c.id=p.campaign_id
        WHERE p.campaign_id=$1 ORDER BY p.joined_at DESC,p.id DESC OFFSET $2 LIMIT $3`, campaignID, offset, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referralport.AdminReferralRecord, 0, limit)
	for rows.Next() {
		var item referralport.AdminReferralRecord
		var state string
		var delta int64
		if err = rows.Scan(&item.Participation.ID, &item.Participation.CampaignID, &item.Participation.CustomerID, &item.Participation.TeamID, &item.Participation.InvitationID, &item.Participation.InviterCustomerID, &item.Participation.InviterTeamID, &state, &item.Participation.JoinedAt, &item.CampaignName, &item.ScoreEventID, &delta); err != nil {
			return nil, mapError(err)
		}
		item.Participation.State = referraldomain.ParticipationState(state)
		if !item.Participation.Valid() {
			return nil, referralport.ErrUnavailable
		}
		item.ScoreState = "none"
		if item.ScoreEventID > 0 {
			item.ScoreState = "valid"
		}
		if item.ScoreEventID > 0 && delta < 1 {
			item.ScoreState = "reversed"
		}
		values = append(values, item)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

// LeaderboardRows counts non-reversed credits according to their original
// accepted timestamp. A later reversal corrects the relevant historical day
// and week instead of only subtracting from the day it was administered.
func (r *Repository) LeaderboardRowsWithin(ctx context.Context, campaignID int64, kind referralport.LeaderboardKind, teamID int64, start, end time.Time, offset, limit int32, viewerCustomerID, viewerTeamID int64) ([]referralport.LeaderboardEntry, *referralport.LeaderboardEntry, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, nil, err
	}
	if campaignID < 1 || !kind.Valid() || !start.Before(end) || offset < 0 || limit < 1 || limit > 101 || (kind == referralport.LeaderboardInTeam && teamID < 1) {
		return nil, nil, ErrInvalid
	}
	var query, ownQuery string
	var args, ownArgs []any
	switch kind {
	case referralport.LeaderboardPersonal:
		query = `WITH active_credits AS (
		    SELECT e.inviter_customer_id,COALESCE(e.team_id,0) AS team_id,COALESCE(t.name,'') AS team_name,e.occurred_at,e.id
		    FROM referral_score_events e LEFT JOIN referral_teams t ON t.id=e.team_id
            WHERE e.campaign_id=$1 AND e.kind='credit' AND e.occurred_at >= $2 AND e.occurred_at < $3
              AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=e.id)
        ), ranked AS (
            SELECT row_number() OVER (ORDER BY count(*) DESC,max(occurred_at) ASC,inviter_customer_id ASC)::bigint AS rank,
                count(*)::bigint AS score,inviter_customer_id AS customer_id,team_id,max(team_name) AS team_name,max(occurred_at) AS first_reached_at
            FROM active_credits GROUP BY inviter_customer_id,team_id
		) SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked ORDER BY rank OFFSET $4 LIMIT $5`
		ownQuery = strings.Replace(query, `SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked ORDER BY rank OFFSET $4 LIMIT $5`, `SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked WHERE customer_id=$4`, 1)
		args = []any{campaignID, start.UTC(), end.UTC(), offset, limit}
		ownArgs = []any{campaignID, start.UTC(), end.UTC(), viewerCustomerID}
	case referralport.LeaderboardInTeam:
		query = `WITH active_credits AS (
            SELECT e.inviter_customer_id,e.team_id,t.name AS team_name,e.occurred_at,e.id
            FROM referral_score_events e JOIN referral_teams t ON t.id=e.team_id
            WHERE e.campaign_id=$1 AND e.kind='credit' AND e.occurred_at >= $2 AND e.occurred_at < $3 AND e.team_id=$4
              AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=e.id)
        ), ranked AS (
            SELECT row_number() OVER (ORDER BY count(*) DESC,max(occurred_at) ASC,inviter_customer_id ASC)::bigint AS rank,
                count(*)::bigint AS score,inviter_customer_id AS customer_id,team_id,max(team_name) AS team_name,max(occurred_at) AS first_reached_at
            FROM active_credits GROUP BY inviter_customer_id,team_id
        ) SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked ORDER BY rank OFFSET $5 LIMIT $6`
		ownQuery = strings.Replace(query, `SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked ORDER BY rank OFFSET $5 LIMIT $6`, `SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked WHERE customer_id=$5`, 1)
		args = []any{campaignID, start.UTC(), end.UTC(), teamID, offset, limit}
		ownArgs = []any{campaignID, start.UTC(), end.UTC(), teamID, viewerCustomerID}
	case referralport.LeaderboardTeam:
		query = `WITH active_credits AS (
            SELECT e.team_id,t.name AS team_name,e.occurred_at,e.id
            FROM referral_score_events e JOIN referral_teams t ON t.id=e.team_id
            WHERE e.campaign_id=$1 AND e.kind='credit' AND e.occurred_at >= $2 AND e.occurred_at < $3
              AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=e.id)
        ), ranked AS (
            SELECT row_number() OVER (ORDER BY count(*) DESC,max(occurred_at) ASC,team_id ASC)::bigint AS rank,
                count(*)::bigint AS score,0::bigint AS customer_id,team_id,max(team_name) AS team_name,max(occurred_at) AS first_reached_at
            FROM active_credits GROUP BY team_id
		) SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked ORDER BY rank OFFSET $4 LIMIT $5`
		ownQuery = strings.Replace(query, `SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked ORDER BY rank OFFSET $4 LIMIT $5`, `SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked WHERE team_id=$4`, 1)
		args = []any{campaignID, start.UTC(), end.UTC(), offset, limit}
		ownArgs = []any{campaignID, start.UTC(), end.UTC(), viewerTeamID}
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, mapError(err)
	}
	defer rows.Close()
	items := make([]referralport.LeaderboardEntry, 0, limit)
	for rows.Next() {
		var item referralport.LeaderboardEntry
		if err = rows.Scan(&item.Rank, &item.Score, &item.CustomerID, &item.TeamID, &item.TeamName, &item.FirstReachedAt); err != nil {
			return nil, nil, mapError(err)
		}
		item.Mine = kind != referralport.LeaderboardTeam && item.CustomerID == viewerCustomerID
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, mapError(err)
	}
	viewerKey := viewerCustomerID
	if kind == referralport.LeaderboardTeam {
		viewerKey = viewerTeamID
	}
	if viewerKey < 1 {
		return items, nil, nil
	}
	var own referralport.LeaderboardEntry
	err = tx.QueryRow(ctx, ownQuery, ownArgs...).Scan(&own.Rank, &own.Score, &own.CustomerID, &own.TeamID, &own.TeamName, &own.FirstReachedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return items, nil, nil
	}
	if err != nil {
		return nil, nil, mapError(err)
	}
	own.Mine = true
	return items, &own, nil
}

// ListSalesLeaderboardRowsWithin ranks paid attribution snapshots after
// cumulative successful refunds. It intentionally has a separate query from
// invitation score events so a sales campaign can never silently display
// invite counts when its sales facts are unavailable.
func (r *Repository) ListSalesLeaderboardRowsWithin(ctx context.Context, campaignID int64, kind referralport.LeaderboardKind, metric referraldomain.LeaderboardMetric, teamID int64, start, end time.Time, offset, limit int32, viewerCustomerID, viewerTeamID int64) ([]referralport.LeaderboardEntry, *referralport.LeaderboardEntry, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, nil, err
	}
	if campaignID < 1 || !kind.Valid() || !start.Before(end) || offset < 0 || limit < 1 || limit > 101 || (kind == referralport.LeaderboardInTeam && teamID < 1) || (metric != referraldomain.LeaderboardSalesAmount && metric != referraldomain.LeaderboardSalesOrders) {
		return nil, nil, ErrInvalid
	}
	scoreExpr, orderExpr := "sum(net_minor)", "sum(net_minor) DESC,count(*) DESC"
	if metric == referraldomain.LeaderboardSalesOrders {
		scoreExpr, orderExpr = "count(*)", "count(*) DESC,sum(net_minor) DESC"
	}
	base := `WITH sale_nets AS (
	        SELECT c.promoter_customer_id,c.team_id,c.occurred_at AS paid_at,
	               (c.amount_delta_minor+COALESCE(SUM(r.amount_delta_minor),0))::bigint AS net_minor,
	               (c.order_count_delta+COALESCE(SUM(r.order_count_delta),0))::bigint AS net_orders
	        FROM referral_product_sale_events c
	        LEFT JOIN referral_product_sale_events r ON r.reverses_sale_event_id=c.id AND r.kind='reversal'
	        WHERE c.campaign_id=$1 AND c.kind='credit' AND c.occurred_at >= $2 AND c.occurred_at < $3
	        GROUP BY c.id,c.promoter_customer_id,c.team_id,c.occurred_at,c.amount_delta_minor,c.order_count_delta
	    ), active_sales AS (
	        SELECT f.promoter_customer_id,COALESCE(f.team_id,0) AS team_id,COALESCE(t.name,'') AS team_name,
	               f.net_minor,f.paid_at
	        FROM sale_nets f LEFT JOIN referral_teams t ON t.id=f.team_id
	        WHERE f.net_minor>0 AND f.net_orders>0`
	var query, ownQuery string
	var args, ownArgs []any
	switch kind {
	case referralport.LeaderboardPersonal:
		query = base + `), ranked AS (
	            SELECT row_number() OVER (ORDER BY ` + orderExpr + `,max(paid_at) ASC,promoter_customer_id ASC)::bigint AS rank,
	                   ` + scoreExpr + `::bigint AS score,promoter_customer_id AS customer_id,0::bigint AS team_id,''::text AS team_name,
                   sum(net_minor)::bigint AS sales_amount_minor,count(*)::bigint AS sales_order_count,max(paid_at) AS first_reached_at
            FROM active_sales GROUP BY promoter_customer_id
        ) SELECT rank,score,customer_id,team_id,team_name,sales_amount_minor,sales_order_count,first_reached_at FROM ranked ORDER BY rank OFFSET $4 LIMIT $5`
		ownQuery = strings.Replace(query, `SELECT rank,score,customer_id,team_id,team_name,sales_amount_minor,sales_order_count,first_reached_at FROM ranked ORDER BY rank OFFSET $4 LIMIT $5`, `SELECT rank,score,customer_id,team_id,team_name,sales_amount_minor,sales_order_count,first_reached_at FROM ranked WHERE customer_id=$4`, 1)
		args = []any{campaignID, start.UTC(), end.UTC(), offset, limit}
		ownArgs = []any{campaignID, start.UTC(), end.UTC(), viewerCustomerID}
	case referralport.LeaderboardInTeam:
		query = base + ` AND f.team_id=$4), ranked AS (
	            SELECT row_number() OVER (ORDER BY ` + orderExpr + `,max(paid_at) ASC,promoter_customer_id ASC)::bigint AS rank,
	                   ` + scoreExpr + `::bigint AS score,promoter_customer_id AS customer_id,team_id,max(team_name) AS team_name,
                   sum(net_minor)::bigint AS sales_amount_minor,count(*)::bigint AS sales_order_count,max(paid_at) AS first_reached_at
            FROM active_sales GROUP BY promoter_customer_id,team_id
        ) SELECT rank,score,customer_id,team_id,team_name,sales_amount_minor,sales_order_count,first_reached_at FROM ranked ORDER BY rank OFFSET $5 LIMIT $6`
		ownQuery = strings.Replace(query, `SELECT rank,score,customer_id,team_id,team_name,sales_amount_minor,sales_order_count,first_reached_at FROM ranked ORDER BY rank OFFSET $5 LIMIT $6`, `SELECT rank,score,customer_id,team_id,team_name,sales_amount_minor,sales_order_count,first_reached_at FROM ranked WHERE customer_id=$5`, 1)
		args = []any{campaignID, start.UTC(), end.UTC(), teamID, offset, limit}
		ownArgs = []any{campaignID, start.UTC(), end.UTC(), teamID, viewerCustomerID}
	case referralport.LeaderboardTeam:
		query = base + ` AND f.team_id IS NOT NULL), ranked AS (
	            SELECT row_number() OVER (ORDER BY ` + orderExpr + `,max(paid_at) ASC,team_id ASC)::bigint AS rank,
	                   ` + scoreExpr + `::bigint AS score,0::bigint AS customer_id,team_id,max(team_name) AS team_name,
                   sum(net_minor)::bigint AS sales_amount_minor,count(*)::bigint AS sales_order_count,max(paid_at) AS first_reached_at
            FROM active_sales GROUP BY team_id
        ) SELECT rank,score,customer_id,team_id,team_name,sales_amount_minor,sales_order_count,first_reached_at FROM ranked ORDER BY rank OFFSET $4 LIMIT $5`
		ownQuery = strings.Replace(query, `SELECT rank,score,customer_id,team_id,team_name,sales_amount_minor,sales_order_count,first_reached_at FROM ranked ORDER BY rank OFFSET $4 LIMIT $5`, `SELECT rank,score,customer_id,team_id,team_name,sales_amount_minor,sales_order_count,first_reached_at FROM ranked WHERE team_id=$4`, 1)
		args = []any{campaignID, start.UTC(), end.UTC(), offset, limit}
		ownArgs = []any{campaignID, start.UTC(), end.UTC(), viewerTeamID}
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, mapError(err)
	}
	defer rows.Close()
	items := make([]referralport.LeaderboardEntry, 0, limit)
	for rows.Next() {
		var item referralport.LeaderboardEntry
		if err = rows.Scan(&item.Rank, &item.Score, &item.CustomerID, &item.TeamID, &item.TeamName, &item.SalesAmountMinor, &item.SalesOrderCount, &item.FirstReachedAt); err != nil {
			return nil, nil, mapError(err)
		}
		item.Mine = kind != referralport.LeaderboardTeam && item.CustomerID == viewerCustomerID
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, mapError(err)
	}
	viewerKey := viewerCustomerID
	if kind == referralport.LeaderboardTeam {
		viewerKey = viewerTeamID
	}
	if viewerKey < 1 {
		return items, nil, nil
	}
	var own referralport.LeaderboardEntry
	err = tx.QueryRow(ctx, ownQuery, ownArgs...).Scan(&own.Rank, &own.Score, &own.CustomerID, &own.TeamID, &own.TeamName, &own.SalesAmountMinor, &own.SalesOrderCount, &own.FirstReachedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return items, nil, nil
	}
	if err != nil {
		return nil, nil, mapError(err)
	}
	own.Mine = true
	return items, &own, nil
}

func OffsetCursor(value string) (int32, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed < 0 || value != strconv.FormatInt(parsed, 10) {
		return 0, referralport.ErrConflict
	}
	return int32(parsed), nil
}

func NextOffsetCursor(offset int32, count int, limit int32) string {
	if int32(count) < limit {
		return ""
	}
	return strconv.FormatInt(int64(offset)+int64(count), 10)
}

// ListAdminTeamSummariesWithin returns campaign-scoped operational aggregates.
// Correlated counts deliberately avoid multiplying a team's participants by its
// credits when both facts are present.
func (r *Repository) ListAdminTeamSummariesWithin(ctx context.Context, campaignID int64) ([]referralport.AdminTeamSummary, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+teamColumns+`,
        (SELECT count(*) FROM referral_participations p
         WHERE p.campaign_id=t.campaign_id AND p.team_id=t.id AND p.state='active'),
        (SELECT count(*) FROM referral_score_events e
         WHERE e.campaign_id=t.campaign_id AND e.team_id=t.id AND e.kind='credit'
           AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=e.id)),
        EXISTS (SELECT 1 FROM referral_participations cp
                WHERE cp.campaign_id=t.campaign_id AND cp.customer_id=t.captain_customer_id AND cp.state='active')
        FROM referral_teams t
        WHERE t.campaign_id=$1
        ORDER BY t.id`, campaignID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referralport.AdminTeamSummary, 0)
	for rows.Next() {
		var value referralport.AdminTeamSummary
		if err = rows.Scan(&value.Team.ID, &value.Team.CampaignID, &value.Team.Name, &value.Team.LogoURL, &value.Team.CaptainCustomerID, &value.Team.Version, &value.Team.CreatedAt, &value.Team.UpdatedAt, &value.ParticipantCount, &value.DirectInvitationCount, &value.CaptainParticipated); err != nil {
			return nil, mapError(err)
		}
		if !value.Team.Valid() || value.ParticipantCount < 0 || value.DirectInvitationCount < 0 {
			return nil, referralport.ErrUnavailable
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func validAdminParticipantFilter(query referralport.AdminParticipantQuery) bool {
	return query.CampaignID > 0 && query.TeamID >= 0 && (query.State == "" || query.State.Valid())
}

func validAdminInvitationFilter(query referralport.AdminInvitationQuery) bool {
	return query.CampaignID > 0 && query.TeamID >= 0 && query.InviterCustomerID >= 0 && (query.State == "" || query.State.Valid())
}

const adminParticipationColumns = `p.id,p.campaign_id,p.customer_id,COALESCE(p.team_id,0),COALESCE(p.invitation_id,0),COALESCE(p.inviter_customer_id,0),COALESCE(p.inviter_team_id,0),p.state,p.joined_at`
const adminParticipantTeamColumns = `COALESCE(t.id,0),COALESCE(t.campaign_id,0),COALESCE(t.name,''),COALESCE(t.logo_url,''),COALESCE(t.captain_customer_id,0),COALESCE(t.version,0),COALESCE(t.created_at,'epoch'::timestamptz),COALESCE(t.updated_at,'epoch'::timestamptz)`
const adminInviterTeamColumns = `COALESCE(it.id,0),COALESCE(it.campaign_id,0),COALESCE(it.name,''),COALESCE(it.logo_url,''),COALESCE(it.captain_customer_id,0),COALESCE(it.version,0),COALESCE(it.created_at,'epoch'::timestamptz),COALESCE(it.updated_at,'epoch'::timestamptz)`

func scanAdminParticipant(row rowScanner) (referralport.AdminParticipantRecord, error) {
	var value referralport.AdminParticipantRecord
	var participationState string
	if err := row.Scan(
		&value.Participation.ID, &value.Participation.CampaignID, &value.Participation.CustomerID, &value.Participation.TeamID,
		&value.Participation.InvitationID, &value.Participation.InviterCustomerID, &value.Participation.InviterTeamID, &participationState, &value.Participation.JoinedAt,
		&value.Team.ID, &value.Team.CampaignID, &value.Team.Name, &value.Team.LogoURL, &value.Team.CaptainCustomerID, &value.Team.Version, &value.Team.CreatedAt, &value.Team.UpdatedAt,
		&value.InviterCustomerID, &value.DirectInvitationCount,
	); err != nil {
		return referralport.AdminParticipantRecord{}, mapError(err)
	}
	value.Participation.State = referraldomain.ParticipationState(participationState)
	teamOK := value.Participation.TeamID == 0 && value.Team.ID == 0 || value.Team.Valid() && value.Team.ID == value.Participation.TeamID && value.Team.CampaignID == value.Participation.CampaignID
	if !value.Participation.Valid() || !teamOK || value.InviterCustomerID != value.Participation.InviterCustomerID || value.DirectInvitationCount < 0 {
		return referralport.AdminParticipantRecord{}, referralport.ErrUnavailable
	}
	return value, nil
}

func scanAdminInvitation(row rowScanner) (referralport.AdminInvitationRecord, error) {
	var value referralport.AdminInvitationRecord
	var participationState string
	if err := row.Scan(
		&value.Participation.ID, &value.Participation.CampaignID, &value.Participation.CustomerID, &value.Participation.TeamID,
		&value.Participation.InvitationID, &value.Participation.InviterCustomerID, &value.Participation.InviterTeamID, &participationState, &value.Participation.JoinedAt,
		&value.InviterTeam.ID, &value.InviterTeam.CampaignID, &value.InviterTeam.Name, &value.InviterTeam.LogoURL, &value.InviterTeam.CaptainCustomerID, &value.InviterTeam.Version, &value.InviterTeam.CreatedAt, &value.InviterTeam.UpdatedAt,
		&value.ParticipantTeam.ID, &value.ParticipantTeam.CampaignID, &value.ParticipantTeam.Name, &value.ParticipantTeam.LogoURL, &value.ParticipantTeam.CaptainCustomerID, &value.ParticipantTeam.Version, &value.ParticipantTeam.CreatedAt, &value.ParticipantTeam.UpdatedAt,
		&value.ScoreState,
	); err != nil {
		return referralport.AdminInvitationRecord{}, mapError(err)
	}
	value.Participation.State = referraldomain.ParticipationState(participationState)
	value.InviterCustomerID = value.Participation.InviterCustomerID
	value.InviterTeamID = value.Participation.InviterTeamID
	inviterTeamOK := value.InviterTeamID == 0 && value.InviterTeam.ID == 0 || value.InviterTeam.Valid() && value.InviterTeam.ID == value.InviterTeamID && value.InviterTeam.CampaignID == value.Participation.CampaignID
	participantTeamOK := value.Participation.TeamID == 0 && value.ParticipantTeam.ID == 0 || value.ParticipantTeam.Valid() && value.ParticipantTeam.ID == value.Participation.TeamID && value.ParticipantTeam.CampaignID == value.Participation.CampaignID
	if !value.Participation.Valid() || value.Participation.InvitationID < 1 || !inviterTeamOK || !participantTeamOK ||
		(value.ScoreState != "valid" && value.ScoreState != "reversed" && value.ScoreState != "none") {
		return referralport.AdminInvitationRecord{}, referralport.ErrUnavailable
	}
	return value, nil
}

func adminParticipantSQL(query referralport.AdminParticipantQuery, after *AdminJoinedCursor, limit int32) (string, []any) {
	statement := `SELECT ` + adminParticipationColumns + `,` + adminParticipantTeamColumns + `,
        COALESCE(p.inviter_customer_id,0),
        (SELECT count(*) FROM referral_score_events e
         WHERE e.campaign_id=p.campaign_id AND e.inviter_customer_id=p.customer_id AND e.kind='credit'
           AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=e.id))
        FROM referral_participations p
		LEFT JOIN referral_teams t ON t.id=p.team_id
	        WHERE p.campaign_id=$1
	          AND ($2::bigint=0 OR p.team_id=$2)
	          AND ($3::text='' OR p.state=$3)`
	args := []any{query.CampaignID, query.TeamID, string(query.State)}
	if after != nil {
		args = append(args, after.JoinedAt.UTC(), after.ParticipationID)
		statement += ` AND (p.joined_at,p.id)<($4,$5)`
	}
	statement += ` ORDER BY p.joined_at DESC,p.id DESC`
	if limit > 0 {
		args = append(args, limit)
		statement += ` LIMIT $` + strconv.Itoa(len(args))
	}
	return statement, args
}

func adminInvitationSQL(query referralport.AdminInvitationQuery, after *AdminJoinedCursor, limit int32) (string, []any) {
	statement := `SELECT ` + adminParticipationColumns + `,` + adminInviterTeamColumns + `,` + adminParticipantTeamColumns + `,
        CASE
          WHEN EXISTS (SELECT 1 FROM referral_score_events c WHERE c.participation_id=p.id AND c.kind='credit'
                           AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=c.id)) THEN 'valid'
          WHEN EXISTS (SELECT 1 FROM referral_score_events c WHERE c.participation_id=p.id AND c.kind='credit') THEN 'reversed'
          ELSE 'none'
        END AS score_state
        FROM referral_participations p
		LEFT JOIN referral_teams it ON it.id=p.inviter_team_id
		LEFT JOIN referral_teams t ON t.id=p.team_id
        WHERE p.campaign_id=$1
          AND p.invitation_id IS NOT NULL
          AND ($2::bigint=0 OR p.team_id=$2)
          AND ($3::bigint=0 OR p.inviter_customer_id=$3)
          AND ($4::text='' OR
               ($4='active' AND EXISTS (SELECT 1 FROM referral_score_events c WHERE c.participation_id=p.id AND c.kind='credit'
                                           AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=c.id))) OR
	               ($4='reversed' AND EXISTS (SELECT 1 FROM referral_score_events c JOIN referral_score_events r ON r.reverses_score_event_id=c.id AND r.kind='reversal'
	                                             WHERE c.participation_id=p.id AND c.kind='credit')))`
	args := []any{query.CampaignID, query.TeamID, query.InviterCustomerID, string(query.State)}
	if after != nil {
		args = append(args, after.JoinedAt.UTC(), after.ParticipationID)
		statement += ` AND (p.joined_at,p.id)<($5,$6)`
	}
	statement += ` ORDER BY p.joined_at DESC,p.id DESC`
	if limit > 0 {
		args = append(args, limit)
		statement += ` LIMIT $` + strconv.Itoa(len(args))
	}
	return statement, args
}

func (r *Repository) ListAdminParticipantsWithin(ctx context.Context, query referralport.AdminParticipantQuery, after *AdminJoinedCursor, limit int32) ([]referralport.AdminParticipantRecord, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if !validAdminParticipantFilter(query) || (after != nil && (after.JoinedAt.IsZero() || after.ParticipationID < 1)) || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	statement, args := adminParticipantSQL(query, after, limit)
	rows, err := tx.Query(ctx, statement, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referralport.AdminParticipantRecord, 0, limit)
	for rows.Next() {
		value, scanErr := scanAdminParticipant(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func (r *Repository) ListAdminInvitationsWithin(ctx context.Context, query referralport.AdminInvitationQuery, after *AdminJoinedCursor, limit int32) ([]referralport.AdminInvitationRecord, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if !validAdminInvitationFilter(query) || (after != nil && (after.JoinedAt.IsZero() || after.ParticipationID < 1)) || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	statement, args := adminInvitationSQL(query, after, limit)
	rows, err := tx.Query(ctx, statement, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referralport.AdminInvitationRecord, 0, limit)
	for rows.Next() {
		value, scanErr := scanAdminInvitation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

// ListAdminParticipantsForExportWithin has no OFFSET or LIMIT and executes one
// statement, so PostgreSQL supplies one statement snapshot for the complete
// export result rather than a page sequence that can drift under new joins.
func (r *Repository) ListAdminParticipantsForExportWithin(ctx context.Context, query referralport.AdminParticipantQuery) ([]referralport.AdminParticipantRecord, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if !validAdminParticipantFilter(query) {
		return nil, ErrInvalid
	}
	statement, args := adminParticipantSQL(query, nil, 0)
	rows, err := tx.Query(ctx, statement, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referralport.AdminParticipantRecord, 0)
	for rows.Next() {
		value, scanErr := scanAdminParticipant(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

// ListAdminInvitationsForExportWithin has no OFFSET or LIMIT and executes one
// statement, keeping the invitation rows and their score-state predicate in
// the same PostgreSQL statement snapshot.
func (r *Repository) ListAdminInvitationsForExportWithin(ctx context.Context, query referralport.AdminInvitationQuery) ([]referralport.AdminInvitationRecord, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if !validAdminInvitationFilter(query) {
		return nil, ErrInvalid
	}
	statement, args := adminInvitationSQL(query, nil, 0)
	rows, err := tx.Query(ctx, statement, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referralport.AdminInvitationRecord, 0)
	for rows.Next() {
		value, scanErr := scanAdminInvitation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

// ListAdminParticipantInvitationsForExportWithin executes the unpaged export
// statement after the application service has resolved and verified the source
// participation. The caller supplies only the server-derived inviter customer
// ID; no delivery adapter can broaden the exported invitee set.
func (r *Repository) ListAdminParticipantInvitationsForExportWithin(ctx context.Context, query referralport.AdminParticipantInvitationQuery, inviterCustomerID int64) ([]referralport.AdminInvitationRecord, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if query.CampaignID < 1 || query.ParticipationID < 1 || inviterCustomerID < 1 || (query.State != "" && !query.State.Valid()) {
		return nil, ErrInvalid
	}
	statement, args := adminInvitationSQL(referralport.AdminInvitationQuery{
		CampaignID:        query.CampaignID,
		InviterCustomerID: inviterCustomerID,
		State:             query.State,
	}, nil, 0)
	rows, err := tx.Query(ctx, statement, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referralport.AdminInvitationRecord, 0)
	for rows.Next() {
		value, scanErr := scanAdminInvitation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}
