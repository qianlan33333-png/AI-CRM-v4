package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type OperationReceipt struct {
	Operation, ActorScope, Key string
	PayloadDigest              [sha256.Size]byte
	ResultKind                 string
	ResultID                   int64
	CreatedAt                  time.Time
}

func (r *Repository) LockOperationReceiptWithin(ctx context.Context, operation, actorScope, key string) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if operation == "" || actorScope == "" || key == "" {
		return ErrInvalid
	}
	// Transaction-scoped advisory locks make a first-write receipt deterministic
	// without reserving a phantom row that could outlive a rollback.
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "referral:"+operation+":"+actorScope+":"+key)
	return mapError(err)
}

func (r *Repository) ReadOperationReceiptWithin(ctx context.Context, operation, actorScope, key string) (OperationReceipt, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return OperationReceipt{}, false, err
	}
	if operation == "" || actorScope == "" || key == "" {
		return OperationReceipt{}, false, ErrInvalid
	}
	digest := sha256.Sum256([]byte(key))
	var value OperationReceipt
	var storedKey, payload []byte
	err = tx.QueryRow(ctx, `SELECT operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at FROM referral_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3`, operation, actorScope, digest[:]).Scan(&value.Operation, &value.ActorScope, &storedKey, &payload, &value.ResultKind, &value.ResultID, &value.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return OperationReceipt{}, false, nil
	}
	if err != nil {
		return OperationReceipt{}, false, mapError(err)
	}
	if len(storedKey) != sha256.Size || len(payload) != sha256.Size || value.ResultID < 1 || value.ResultKind == "" {
		return OperationReceipt{}, false, referralport.ErrUnavailable
	}
	copy(value.PayloadDigest[:], payload)
	value.Key = key
	return value, true, nil
}

func (r *Repository) AppendOperationReceiptWithin(ctx context.Context, operation, actorScope, key string, payloadDigest [sha256.Size]byte, resultKind string, resultID int64, at time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if operation == "" || actorScope == "" || key == "" || resultKind == "" || resultID < 1 || at.IsZero() {
		return ErrInvalid
	}
	keyDigest := sha256.Sum256([]byte(key))
	_, err = tx.Exec(ctx, `INSERT INTO referral_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, operation, actorScope, keyDigest[:], payloadDigest[:], resultKind, resultID, at.UTC())
	return mapError(err)
}

func (r *Repository) InsertCampaignWithin(ctx context.Context, value referraldomain.Campaign) (referraldomain.Campaign, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Campaign{}, err
	}
	if !value.ValidForInsert() {
		return referraldomain.Campaign{}, ErrInvalid
	}
	return scanCampaign(tx.QueryRow(ctx, `INSERT INTO referral_campaigns(name,cover_url,description,reward_rules,state,starts_at,ends_at,version,created_by,created_at,updated_at,team_mode,qualification_mode,product_id,product_type,leaderboard_metric) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING `+campaignColumns, value.Name, value.CoverURL, value.Description, value.RewardRules, string(value.State), value.StartsAt.UTC(), value.EndsAt.UTC(), value.Version, value.CreatedBy, value.CreatedAt.UTC(), value.UpdatedAt.UTC(), value.Config().TeamMode, value.Config().QualificationMode, value.Config().ProductID, value.Config().ProductType, value.Config().LeaderboardMetric))
}

func (r *Repository) ReadCampaignWithin(ctx context.Context, campaignID int64, lock bool) (referraldomain.Campaign, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Campaign{}, err
	}
	if campaignID < 1 {
		return referraldomain.Campaign{}, ErrInvalid
	}
	query := `SELECT ` + campaignColumns + ` FROM referral_campaigns WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanCampaign(tx.QueryRow(ctx, query, campaignID))
}

func (r *Repository) UpdateCampaignWithin(ctx context.Context, value referraldomain.Campaign, expectedVersion int64) (referraldomain.Campaign, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Campaign{}, err
	}
	if !value.Valid() || expectedVersion < 1 || value.Version != expectedVersion+1 {
		return referraldomain.Campaign{}, ErrInvalid
	}
	updated, err := scanCampaign(tx.QueryRow(ctx, `UPDATE referral_campaigns SET name=$2,cover_url=$3,description=$4,reward_rules=$5,state=$6,starts_at=$7,ends_at=$8,version=$9,updated_at=$10,team_mode=$12,qualification_mode=$13,product_id=$14,product_type=$15,leaderboard_metric=$16 WHERE id=$1 AND version=$11 RETURNING `+campaignColumns, value.ID, value.Name, value.CoverURL, value.Description, value.RewardRules, string(value.State), value.StartsAt.UTC(), value.EndsAt.UTC(), value.Version, value.UpdatedAt.UTC(), expectedVersion, value.Config().TeamMode, value.Config().QualificationMode, value.Config().ProductID, value.Config().ProductType, value.Config().LeaderboardMetric))
	if err == referralport.ErrNotFound {
		return referraldomain.Campaign{}, referralport.ErrConflict
	}
	return updated, err
}

func (r *Repository) InsertTeamWithin(ctx context.Context, value referraldomain.Team) (referraldomain.Team, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Team{}, err
	}
	if !value.ValidForInsert() {
		return referraldomain.Team{}, ErrInvalid
	}
	return scanTeam(tx.QueryRow(ctx, `INSERT INTO referral_teams(campaign_id,name,logo_url,captain_customer_id,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+teamColumns, value.CampaignID, value.Name, value.LogoURL, value.CaptainCustomerID, value.Version, value.CreatedAt.UTC(), value.UpdatedAt.UTC()))
}

func (r *Repository) ReadTeamWithin(ctx context.Context, teamID int64, lock bool) (referraldomain.Team, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Team{}, err
	}
	if teamID < 1 {
		return referraldomain.Team{}, ErrInvalid
	}
	query := `SELECT ` + teamColumns + ` FROM referral_teams WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanTeam(tx.QueryRow(ctx, query, teamID))
}

func (r *Repository) ReadCaptainTeamWithin(ctx context.Context, campaignID, captainCustomerID int64) (referraldomain.Team, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Team{}, false, err
	}
	if campaignID < 1 || captainCustomerID < 1 {
		return referraldomain.Team{}, false, ErrInvalid
	}
	team, err := scanTeam(tx.QueryRow(ctx, `SELECT `+teamColumns+` FROM referral_teams WHERE campaign_id=$1 AND captain_customer_id=$2`, campaignID, captainCustomerID))
	if errors.Is(err, referralport.ErrNotFound) {
		return referraldomain.Team{}, false, nil
	}
	if err != nil {
		return referraldomain.Team{}, false, err
	}
	return team, true, nil
}

func (r *Repository) LockParticipationWithin(ctx context.Context, campaignID, customerID int64) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if campaignID < 1 || customerID < 1 {
		return ErrInvalid
	}
	if _, err = tx.Exec(ctx, `INSERT INTO referral_participation_locks(campaign_id,customer_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, campaignID, customerID); err != nil {
		return mapError(err)
	}
	_, err = tx.Exec(ctx, `SELECT 1 FROM referral_participation_locks WHERE campaign_id=$1 AND customer_id=$2 FOR UPDATE`, campaignID, customerID)
	return mapError(err)
}

func (r *Repository) ReadParticipationWithin(ctx context.Context, campaignID, customerID int64, lock bool) (referraldomain.Participation, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Participation{}, err
	}
	if campaignID < 1 || customerID < 1 {
		return referraldomain.Participation{}, ErrInvalid
	}
	query := `SELECT ` + participationColumns + ` FROM referral_participations WHERE campaign_id=$1 AND customer_id=$2`
	if lock {
		query += " FOR UPDATE"
	}
	return scanParticipation(tx.QueryRow(ctx, query, campaignID, customerID))
}

func (r *Repository) ReadParticipationByIDWithin(ctx context.Context, participationID int64, lock bool) (referraldomain.Participation, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Participation{}, err
	}
	if participationID < 1 {
		return referraldomain.Participation{}, ErrInvalid
	}
	query := `SELECT ` + participationColumns + ` FROM referral_participations WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanParticipation(tx.QueryRow(ctx, query, participationID))
}

func (r *Repository) InsertParticipationWithin(ctx context.Context, value referraldomain.Participation) (referraldomain.Participation, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Participation{}, err
	}
	candidate := value
	candidate.ID = 1
	if value.ID != 0 || !candidate.Valid() || value.State != referraldomain.ParticipationActive {
		return referraldomain.Participation{}, ErrInvalid
	}
	return scanParticipation(tx.QueryRow(ctx, `INSERT INTO referral_participations(campaign_id,customer_id,team_id,invitation_id,inviter_customer_id,inviter_team_id,state,joined_at) VALUES($1,$2,NULLIF($3,0),NULLIF($4,0),NULLIF($5,0),NULLIF($6,0),$7,$8) RETURNING `+participationColumns, value.CampaignID, value.CustomerID, value.TeamID, value.InvitationID, value.InviterCustomerID, value.InviterTeamID, string(value.State), value.JoinedAt.UTC()))
}

func (r *Repository) ReverseParticipationWithin(ctx context.Context, value referraldomain.Participation) (referraldomain.Participation, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Participation{}, err
	}
	if !value.Valid() || value.State != referraldomain.ParticipationActive {
		return referraldomain.Participation{}, ErrInvalid
	}
	return scanParticipation(tx.QueryRow(ctx, `UPDATE referral_participations SET state='reversed' WHERE id=$1 AND state='active' RETURNING `+participationColumns, value.ID))
}

func (r *Repository) InsertInvitationWithin(ctx context.Context, value referraldomain.Invitation) (referraldomain.Invitation, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Invitation{}, err
	}
	if value.ID != 0 || value.CampaignID < 1 || value.InviterCustomerID < 1 || value.State != referraldomain.InvitationActive || value.CreatedAt.IsZero() || !value.ExpiresAt.After(value.CreatedAt) || value.RevokedAt != nil {
		return referraldomain.Invitation{}, ErrInvalid
	}
	return scanInvitation(tx.QueryRow(ctx, `INSERT INTO referral_invitations(campaign_id,inviter_customer_id,token_digest,state,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+invitationColumns, value.CampaignID, value.InviterCustomerID, value.TokenDigest[:], string(value.State), value.CreatedAt.UTC(), value.ExpiresAt.UTC()))
}

func (r *Repository) ReadInvitationByDigestWithin(ctx context.Context, digest [sha256.Size]byte, lock bool) (referraldomain.Invitation, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Invitation{}, err
	}
	query := `SELECT ` + invitationColumns + ` FROM referral_invitations WHERE token_digest=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanInvitation(tx.QueryRow(ctx, query, digest[:]))
}

func (r *Repository) ReadInvitationWithin(ctx context.Context, invitationID int64, lock bool) (referraldomain.Invitation, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Invitation{}, err
	}
	if invitationID < 1 {
		return referraldomain.Invitation{}, ErrInvalid
	}
	query := `SELECT ` + invitationColumns + ` FROM referral_invitations WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanInvitation(tx.QueryRow(ctx, query, invitationID))
}

func (r *Repository) UpdateInvitationStateWithin(ctx context.Context, value referraldomain.Invitation, state referraldomain.InvitationState, at time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if !value.Valid() || value.State != referraldomain.InvitationActive || (state != referraldomain.InvitationRevoked && state != referraldomain.InvitationExpired) || at.IsZero() {
		return ErrInvalid
	}
	var revokedAt any
	if state == referraldomain.InvitationRevoked {
		revokedAt = at.UTC()
	}
	result, err := tx.Exec(ctx, `UPDATE referral_invitations SET state=$2,revoked_at=$3 WHERE id=$1 AND state='active'`, value.ID, string(state), revokedAt)
	if err != nil {
		return mapError(err)
	}
	if result.RowsAffected() != 1 {
		return referralport.ErrConflict
	}
	return nil
}

func (r *Repository) LockRelationshipCustomerWithin(ctx context.Context, customerID int64) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if customerID < 1 {
		return ErrInvalid
	}
	if _, err = tx.Exec(ctx, `INSERT INTO referral_relationship_locks(customer_id) VALUES($1) ON CONFLICT DO NOTHING`, customerID); err != nil {
		return mapError(err)
	}
	_, err = tx.Exec(ctx, `SELECT 1 FROM referral_relationship_locks WHERE customer_id=$1 FOR UPDATE`, customerID)
	return mapError(err)
}

func (r *Repository) ReadCurrentRelationshipWithin(ctx context.Context, customerID int64, lock bool) (referraldomain.Relationship, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Relationship{}, err
	}
	if customerID < 1 {
		return referraldomain.Relationship{}, ErrInvalid
	}
	query := `SELECT ` + relationshipColumns + ` FROM referral_current_relationships WHERE customer_id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanRelationship(tx.QueryRow(ctx, query, customerID))
}

func (r *Repository) InsertCurrentRelationshipWithin(ctx context.Context, value referraldomain.Relationship) (referraldomain.Relationship, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Relationship{}, err
	}
	if value.ID != 0 || value.Version != 1 || value.CustomerID < 1 || value.ReferrerCustomerID < 1 || value.CustomerID == value.ReferrerCustomerID || value.SourceCampaignID < 1 || value.InvitationID < 1 || value.EffectiveAt.IsZero() {
		return referraldomain.Relationship{}, ErrInvalid
	}
	return scanRelationship(tx.QueryRow(ctx, `INSERT INTO referral_current_relationships(customer_id,referrer_customer_id,source_campaign_id,invitation_id,version,effective_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+relationshipColumns, value.CustomerID, value.ReferrerCustomerID, value.SourceCampaignID, value.InvitationID, value.Version, value.EffectiveAt.UTC()))
}

func (r *Repository) UpdateCurrentRelationshipWithin(ctx context.Context, value referraldomain.Relationship, expectedVersion int64) (referraldomain.Relationship, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Relationship{}, err
	}
	if !value.Valid() || expectedVersion < 1 || value.Version != expectedVersion+1 {
		return referraldomain.Relationship{}, ErrInvalid
	}
	updated, err := scanRelationship(tx.QueryRow(ctx, `UPDATE referral_current_relationships SET referrer_customer_id=$2,source_campaign_id=$3,invitation_id=$4,version=$5,effective_at=$6 WHERE id=$1 AND version=$7 RETURNING `+relationshipColumns, value.ID, value.ReferrerCustomerID, value.SourceCampaignID, value.InvitationID, value.Version, value.EffectiveAt.UTC(), expectedVersion))
	if err == referralport.ErrNotFound {
		return referraldomain.Relationship{}, referralport.ErrConflict
	}
	return updated, err
}

func (r *Repository) InsertRelationshipHistoryWithin(ctx context.Context, value referraldomain.RelationshipHistory) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if value.ID != 0 || value.RelationshipID < 1 || value.Version < 1 || value.CustomerID < 1 || value.ReferrerCustomerID < 1 || value.CustomerID == value.ReferrerCustomerID || value.SourceCampaignID < 1 || value.InvitationID < 1 || value.AcceptedAt.IsZero() {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO referral_relationship_history(relationship_id,version,customer_id,previous_referrer_customer_id,referrer_customer_id,source_campaign_id,invitation_id,accepted_at) VALUES($1,$2,$3,NULLIF($4,0),$5,$6,$7,$8)`, value.RelationshipID, value.Version, value.CustomerID, value.PreviousReferrerCustomerID, value.ReferrerCustomerID, value.SourceCampaignID, value.InvitationID, value.AcceptedAt.UTC())
	return mapError(err)
}

func (r *Repository) InsertScoreEventWithin(ctx context.Context, value referraldomain.ScoreEvent) (referraldomain.ScoreEvent, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.ScoreEvent{}, err
	}
	if value.ID != 0 || value.CampaignID < 1 || value.ParticipationID < 1 || value.InviterCustomerID < 1 || value.TeamID < 0 || !value.Kind.Valid() || value.OccurredAt.IsZero() {
		return referraldomain.ScoreEvent{}, ErrInvalid
	}
	return scanScoreEvent(tx.QueryRow(ctx, `INSERT INTO referral_score_events(campaign_id,participation_id,inviter_customer_id,team_id,kind,delta,reverses_score_event_id,reason,occurred_at) VALUES($1,$2,$3,NULLIF($4,0),$5,$6,NULLIF($7,0),$8,$9) RETURNING `+scoreEventColumns, value.CampaignID, value.ParticipationID, value.InviterCustomerID, value.TeamID, string(value.Kind), value.Delta, value.ReversesScoreEventID, value.Reason, value.OccurredAt.UTC()))
}

func (r *Repository) ReadCreditScoreEventByParticipationWithin(ctx context.Context, participationID int64, lock bool) (referraldomain.ScoreEvent, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.ScoreEvent{}, err
	}
	if participationID < 1 {
		return referraldomain.ScoreEvent{}, ErrInvalid
	}
	query := `SELECT ` + scoreEventColumns + ` FROM referral_score_events WHERE participation_id=$1 AND kind='credit'`
	if lock {
		query += " FOR UPDATE"
	}
	return scanScoreEvent(tx.QueryRow(ctx, query, participationID))
}

func (r *Repository) ReadScoreEventWithin(ctx context.Context, scoreEventID int64, lock bool) (referraldomain.ScoreEvent, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.ScoreEvent{}, err
	}
	if scoreEventID < 1 {
		return referraldomain.ScoreEvent{}, ErrInvalid
	}
	query := `SELECT ` + scoreEventColumns + ` FROM referral_score_events WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanScoreEvent(tx.QueryRow(ctx, query, scoreEventID))
}

func (r *Repository) HasReversalForCreditWithin(ctx context.Context, scoreEventID int64) (bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return false, err
	}
	if scoreEventID < 1 {
		return false, ErrInvalid
	}
	var found bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM referral_score_events WHERE kind='reversal' AND reverses_score_event_id=$1)`, scoreEventID).Scan(&found)
	if err != nil {
		return false, mapError(err)
	}
	return found, nil
}

func (r *Repository) FlagRewardsForScoreReversalWithin(ctx context.Context, scoreEventID int64, reason string, at time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if scoreEventID < 1 || reason == "" || at.IsZero() {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `WITH target AS (
        SELECT id,campaign_id,inviter_customer_id,(occurred_at AT TIME ZONE 'Asia/Shanghai') AS local_at
        FROM referral_score_events WHERE id=$1 AND kind='credit'
    ), affected AS (
        UPDATE referral_reward_records r SET state='needs_review' FROM target t
        WHERE r.campaign_id=t.campaign_id AND r.customer_id=t.inviter_customer_id AND r.state='recorded'
          AND (
              r.score_event_id=t.id
              OR (r.score_event_id IS NULL AND (
                  r.period='total'
                  OR r.period=('day:' || to_char(t.local_at,'YYYY-MM-DD'))
                  OR r.period=('week:' || to_char(t.local_at,'IYYY-"W"IW'))
                  OR r.period !~ '^(total|day:[0-9]{4}-[0-9]{2}-[0-9]{2}|week:[0-9]{4}-W[0-9]{2})$'
              ))
          )
        RETURNING r.id,r.score_event_id,r.period
    ) INSERT INTO referral_reward_reviews(reward_id,reason,score_event_id,occurred_at)
    SELECT id,left(CASE
        WHEN score_event_id=$1 THEN 'score_event_reversed:'
        WHEN period='total' THEN 'period_total_score_reversed:'
        WHEN period LIKE 'day:%' THEN 'period_day_score_reversed:'
        WHEN period LIKE 'week:%' THEN 'period_week_score_reversed:'
        ELSE 'unscoped_reward_requires_review:'
    END || $2,500),$1,$3 FROM affected ON CONFLICT(reward_id,score_event_id) DO NOTHING`, scoreEventID, reason, at.UTC())
	return mapError(err)
}

func (r *Repository) InsertRewardWithin(ctx context.Context, value referraldomain.RewardRecord) (referraldomain.RewardRecord, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.RewardRecord{}, err
	}
	if value.ID != 0 || value.CampaignID < 1 || value.CustomerID < 1 || value.Period == "" || value.Reward == "" || value.RecordedBy < 1 || value.RecordedAt.IsZero() || value.State != referraldomain.RewardRecorded {
		return referraldomain.RewardRecord{}, ErrInvalid
	}
	return scanReward(tx.QueryRow(ctx, `INSERT INTO referral_reward_records(campaign_id,customer_id,score_event_id,period,reward,evidence_reference,state,recorded_by,recorded_at) VALUES($1,$2,NULLIF($3,0),$4,$5,$6,$7,$8,$9) RETURNING `+rewardColumns, value.CampaignID, value.CustomerID, value.ScoreEventID, value.Period, value.Reward, value.EvidenceReference, string(value.State), value.RecordedBy, value.RecordedAt.UTC()))
}

func (r *Repository) ReadRewardWithin(ctx context.Context, rewardID int64, lock bool) (referraldomain.RewardRecord, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.RewardRecord{}, err
	}
	if rewardID < 1 {
		return referraldomain.RewardRecord{}, ErrInvalid
	}
	query := `SELECT ` + rewardColumns + ` FROM referral_reward_records WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanReward(tx.QueryRow(ctx, query, rewardID))
}

func (r *Repository) InsertCampaignCloseSnapshotWithin(ctx context.Context, campaignID int64, at time.Time) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	if campaignID < 1 || at.IsZero() {
		return 0, ErrInvalid
	}
	var id int64
	err = tx.QueryRow(ctx, `WITH active_credits AS (
        SELECT c.id,c.inviter_customer_id,c.team_id,c.occurred_at
        FROM referral_score_events c
        WHERE c.campaign_id=$1 AND c.kind='credit' AND c.occurred_at <= $2
          AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=c.id AND r.occurred_at <= $2)
    ), personal AS (
        SELECT inviter_customer_id AS customer_id,team_id,count(*)::bigint AS score,max(occurred_at) AS first_reached_at
        FROM active_credits GROUP BY inviter_customer_id,team_id
    ), teams AS (
        SELECT team_id,count(*)::bigint AS score,max(occurred_at) AS first_reached_at FROM active_credits GROUP BY team_id
    ), payload AS (
        SELECT jsonb_build_object(
          'personal',COALESCE((SELECT jsonb_agg(jsonb_build_object('customer_id',customer_id,'team_id',team_id,'score',score,'first_reached_at',first_reached_at) ORDER BY score DESC,first_reached_at,customer_id) FROM personal),'[]'::jsonb),
          'team',COALESCE((SELECT jsonb_agg(jsonb_build_object('team_id',team_id,'score',score,'first_reached_at',first_reached_at) ORDER BY score DESC,first_reached_at,team_id) FROM teams),'[]'::jsonb)
        ) AS rankings,
        COALESCE((SELECT max(id) FROM referral_score_events WHERE campaign_id=$1),0)::bigint AS watermark
    ) INSERT INTO referral_campaign_snapshots(campaign_id,snapshot_kind,captured_at,score_watermark,rankings)
      SELECT $1,'close',$2,watermark,rankings FROM payload
      ON CONFLICT(campaign_id,snapshot_kind) DO NOTHING RETURNING id`, campaignID, at.UTC()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id FROM referral_campaign_snapshots WHERE campaign_id=$1 AND snapshot_kind='close'`, campaignID).Scan(&id)
	}
	if err != nil {
		return 0, mapError(err)
	}
	return id, nil
}
