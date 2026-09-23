// Package store owns Referral PostgreSQL persistence.  It only accesses the
// referral_* schema; canonical Customer IDs are opaque foreign facts supplied
// through Referral's stable application boundary.
package store

import (
	"context"
	"crypto/sha256"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

var ErrInvalid = errors.New("invalid referral persistence request")

type Repository struct {
	pool *pgxpool.Pool
	uow  platformport.UnitOfWork
}

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork) (*Repository, error) {
	if pool == nil || uow == nil {
		return nil, ErrInvalid
	}
	return &Repository{pool: pool, uow: uow}, nil
}

func (r *Repository) Within(ctx context.Context, fn func(context.Context) error) error {
	if r == nil || r.uow == nil || fn == nil {
		return ErrInvalid
	}
	return r.uow.Within(ctx, fn)
}

func transaction(ctx context.Context) (pgx.Tx, error) {
	return platformpostgres.RequireTransaction(ctx)
}

type rowScanner interface{ Scan(...any) error }

const campaignColumns = `id,name,cover_url,description,reward_rules,state,starts_at,ends_at,version,created_by,created_at,updated_at,team_mode,qualification_mode,product_id,product_type,leaderboard_metric`
const teamColumns = `id,campaign_id,name,logo_url,captain_customer_id,version,created_at,updated_at`
const participationColumns = `id,campaign_id,customer_id,COALESCE(team_id,0),COALESCE(invitation_id,0),COALESCE(inviter_customer_id,0),COALESCE(inviter_team_id,0),state,joined_at`
const invitationColumns = `id,campaign_id,inviter_customer_id,token_digest,state,created_at,expires_at,revoked_at`
const relationshipColumns = `id,customer_id,referrer_customer_id,source_campaign_id,invitation_id,version,effective_at`
const scoreEventColumns = `id,campaign_id,participation_id,inviter_customer_id,COALESCE(team_id,0),delta,kind,COALESCE(reverses_score_event_id,0),reason,occurred_at`
const rewardColumns = `id,campaign_id,customer_id,COALESCE(score_event_id,0),period,reward,evidence_reference,state,recorded_by,recorded_at`
const salesFactColumns = `id,campaign_id,order_id,order_item_line,product_id,product_type,promoter_customer_id,COALESCE(team_id,0),original_paid_minor,successful_refund_minor,source_reference,paid_at,version,created_at,updated_at`

func scanCampaign(row rowScanner) (referraldomain.Campaign, error) {
	var value referraldomain.Campaign
	var state string
	err := row.Scan(&value.ID, &value.Name, &value.CoverURL, &value.Description, &value.RewardRules, &state, &value.StartsAt, &value.EndsAt, &value.Version, &value.CreatedBy, &value.CreatedAt, &value.UpdatedAt, &value.TeamMode, &value.QualificationMode, &value.ProductID, &value.ProductType, &value.LeaderboardMetric)
	if errors.Is(err, pgx.ErrNoRows) {
		return referraldomain.Campaign{}, referralport.ErrNotFound
	}
	if err != nil {
		return referraldomain.Campaign{}, mapError(err)
	}
	value.State = referraldomain.CampaignState(state)
	if !value.Valid() {
		return referraldomain.Campaign{}, referralport.ErrUnavailable
	}
	return value, nil
}

func scanTeam(row rowScanner) (referraldomain.Team, error) {
	var value referraldomain.Team
	err := row.Scan(&value.ID, &value.CampaignID, &value.Name, &value.LogoURL, &value.CaptainCustomerID, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return referraldomain.Team{}, referralport.ErrNotFound
	}
	if err != nil {
		return referraldomain.Team{}, mapError(err)
	}
	if !value.Valid() {
		return referraldomain.Team{}, referralport.ErrUnavailable
	}
	return value, nil
}

func scanParticipation(row rowScanner) (referraldomain.Participation, error) {
	var value referraldomain.Participation
	var state string
	err := row.Scan(&value.ID, &value.CampaignID, &value.CustomerID, &value.TeamID, &value.InvitationID, &value.InviterCustomerID, &value.InviterTeamID, &state, &value.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return referraldomain.Participation{}, referralport.ErrNotFound
	}
	if err != nil {
		return referraldomain.Participation{}, mapError(err)
	}
	value.State = referraldomain.ParticipationState(state)
	if !value.Valid() {
		return referraldomain.Participation{}, referralport.ErrUnavailable
	}
	return value, nil
}

func scanInvitation(row rowScanner) (referraldomain.Invitation, error) {
	var value referraldomain.Invitation
	var raw []byte
	var state string
	err := row.Scan(&value.ID, &value.CampaignID, &value.InviterCustomerID, &raw, &state, &value.CreatedAt, &value.ExpiresAt, &value.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return referraldomain.Invitation{}, referralport.ErrNotFound
	}
	if err != nil {
		return referraldomain.Invitation{}, mapError(err)
	}
	if len(raw) != sha256.Size {
		return referraldomain.Invitation{}, referralport.ErrUnavailable
	}
	copy(value.TokenDigest[:], raw)
	value.State = referraldomain.InvitationState(state)
	if !value.Valid() {
		return referraldomain.Invitation{}, referralport.ErrUnavailable
	}
	return value, nil
}

func scanRelationship(row rowScanner) (referraldomain.Relationship, error) {
	var value referraldomain.Relationship
	err := row.Scan(&value.ID, &value.CustomerID, &value.ReferrerCustomerID, &value.SourceCampaignID, &value.InvitationID, &value.Version, &value.EffectiveAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return referraldomain.Relationship{}, referralport.ErrNotFound
	}
	if err != nil {
		return referraldomain.Relationship{}, mapError(err)
	}
	if !value.Valid() {
		return referraldomain.Relationship{}, referralport.ErrUnavailable
	}
	return value, nil
}

func scanScoreEvent(row rowScanner) (referraldomain.ScoreEvent, error) {
	var value referraldomain.ScoreEvent
	var kind string
	err := row.Scan(&value.ID, &value.CampaignID, &value.ParticipationID, &value.InviterCustomerID, &value.TeamID, &value.Delta, &kind, &value.ReversesScoreEventID, &value.Reason, &value.OccurredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return referraldomain.ScoreEvent{}, referralport.ErrNotFound
	}
	if err != nil {
		return referraldomain.ScoreEvent{}, mapError(err)
	}
	value.Kind = referraldomain.ScoreEventKind(kind)
	if !value.Valid() {
		return referraldomain.ScoreEvent{}, referralport.ErrUnavailable
	}
	return value, nil
}

func scanReward(row rowScanner) (referraldomain.RewardRecord, error) {
	var value referraldomain.RewardRecord
	var state string
	err := row.Scan(&value.ID, &value.CampaignID, &value.CustomerID, &value.ScoreEventID, &value.Period, &value.Reward, &value.EvidenceReference, &state, &value.RecordedBy, &value.RecordedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return referraldomain.RewardRecord{}, referralport.ErrNotFound
	}
	if err != nil {
		return referraldomain.RewardRecord{}, mapError(err)
	}
	value.State = referraldomain.RewardState(state)
	if !value.Valid() {
		return referraldomain.RewardRecord{}, referralport.ErrUnavailable
	}
	return value, nil
}

func scanSalesFact(row rowScanner) (referraldomain.SalesFact, error) {
	var value referraldomain.SalesFact
	err := row.Scan(&value.ID, &value.CampaignID, &value.OrderID, &value.OrderItemLine, &value.ProductID, &value.ProductType, &value.PromoterCustomerID, &value.TeamID, &value.OriginalPaidMinor, &value.SuccessfulRefundMinor, &value.SourceReference, &value.PaidAt, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return referraldomain.SalesFact{}, referralport.ErrNotFound
	}
	if err != nil {
		return referraldomain.SalesFact{}, mapError(err)
	}
	if !value.Valid() {
		return referraldomain.SalesFact{}, referralport.ErrUnavailable
	}
	return value, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return referralport.ErrNotFound
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		if databaseError.Code == "23505" {
			switch databaseError.ConstraintName {
			case "referral_teams_campaign_id_name_key":
				return referralport.ErrTeamNameExists
			case "referral_teams_campaign_id_captain_customer_id_key":
				return referralport.ErrCaptainAlreadyAssigned
			}
		}
		switch databaseError.Code {
		case "23505", "23514", "40001", "40P01":
			return referralport.ErrConflict
		}
	}
	return err
}
