package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"strings"
	"time"

	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
)

type adminStore interface {
	LockOperationReceiptWithin(context.Context, string, string, string) error
	ReadOperationReceiptWithin(context.Context, string, string, string) (referralstore.OperationReceipt, bool, error)
	AppendOperationReceiptWithin(context.Context, string, string, string, [sha256.Size]byte, string, int64, time.Time) error
	InsertCampaignWithin(context.Context, referraldomain.Campaign) (referraldomain.Campaign, error)
	ReadCampaignWithin(context.Context, int64, bool) (referraldomain.Campaign, error)
	HasTeamParticipationWithin(context.Context, int64) (bool, error)
	UpdateCampaignWithin(context.Context, referraldomain.Campaign, int64) (referraldomain.Campaign, error)
	InsertTeamWithin(context.Context, referraldomain.Team) (referraldomain.Team, error)
	ReadTeamWithin(context.Context, int64, bool) (referraldomain.Team, error)
	LockParticipationWithin(context.Context, int64, int64) error
	ReadInvitationWithin(context.Context, int64, bool) (referraldomain.Invitation, error)
	UpdateInvitationStateWithin(context.Context, referraldomain.Invitation, referraldomain.InvitationState, time.Time) error
	ReadParticipationByIDWithin(context.Context, int64, bool) (referraldomain.Participation, error)
	ReadParticipationWithin(context.Context, int64, int64, bool) (referraldomain.Participation, error)
	ReverseParticipationWithin(context.Context, referraldomain.Participation) (referraldomain.Participation, error)
	ReadCreditScoreEventByParticipationWithin(context.Context, int64, bool) (referraldomain.ScoreEvent, error)
	ReadScoreEventWithin(context.Context, int64, bool) (referraldomain.ScoreEvent, error)
	HasReversalForCreditWithin(context.Context, int64) (bool, error)
	InsertScoreEventWithin(context.Context, referraldomain.ScoreEvent) (referraldomain.ScoreEvent, error)
	FlagRewardsForScoreReversalWithin(context.Context, int64, string, time.Time) error
	InsertRewardWithin(context.Context, referraldomain.RewardRecord) (referraldomain.RewardRecord, error)
	ReadRewardWithin(context.Context, int64, bool) (referraldomain.RewardRecord, error)
	InsertCampaignCloseSnapshotWithin(context.Context, int64, time.Time) (int64, error)
	ListCampaignsWithin(context.Context, int32, int32, bool) ([]referraldomain.Campaign, error)
	CampaignCountsWithin(context.Context, int64) (referralstore.CampaignCounts, error)
	ListTeamsWithin(context.Context, int64) ([]referraldomain.Team, error)
	ListAdminTeamSummariesWithin(context.Context, int64) ([]referralport.AdminTeamSummary, error)
	CampaignDailyMetricsWithin(context.Context, int64) ([]referralport.CampaignDailyMetric, error)
	ListAdminReferralsWithin(context.Context, int64, int32, int32) ([]referralport.AdminReferralRecord, error)
	ListAdminParticipantsWithin(context.Context, referralport.AdminParticipantQuery, *referralstore.AdminJoinedCursor, int32) ([]referralport.AdminParticipantRecord, error)
	ListAdminInvitationsWithin(context.Context, referralport.AdminInvitationQuery, *referralstore.AdminJoinedCursor, int32) ([]referralport.AdminInvitationRecord, error)
	ListAdminParticipantsForExportWithin(context.Context, referralport.AdminParticipantQuery) ([]referralport.AdminParticipantRecord, error)
	ListAdminInvitationsForExportWithin(context.Context, referralport.AdminInvitationQuery) ([]referralport.AdminInvitationRecord, error)
	ListAdminParticipantInvitationsForExportWithin(context.Context, referralport.AdminParticipantInvitationQuery, int64) ([]referralport.AdminInvitationRecord, error)
	ListRelationshipHistoryWithin(context.Context, int64, int32, int32) ([]referraldomain.RelationshipHistory, error)
	ListRewardsWithin(context.Context, int64, int32, int32) ([]referraldomain.RewardRecord, error)
}

type AdminService struct {
	uow       platformport.UnitOfWork
	store     adminStore
	verifier  referralport.CanonicalCustomerVerifier
	closeJobs CampaignCloseEnqueuer
	audit     *platformaudit.Service
	outbox    platformoutbox.Appender
	now       func() time.Time
}

func NewAdminService(uow platformport.UnitOfWork, store adminStore, verifier referralport.CanonicalCustomerVerifier, closeJobs CampaignCloseEnqueuer, audit *platformaudit.Service, outbox platformoutbox.Appender) (*AdminService, error) {
	if uow == nil || store == nil || verifier == nil || closeJobs == nil || audit == nil || outbox == nil {
		return nil, referralport.ErrUnavailable
	}
	return &AdminService{uow: uow, store: store, verifier: verifier, closeJobs: closeJobs, audit: audit, outbox: outbox, now: time.Now}, nil
}

func (s *AdminService) CreateCampaign(ctx context.Context, command referralport.CreateCampaignCommand) (referraldomain.Campaign, error) {
	if s == nil || !validAdminCommand(command.ActorAdminID, command.IdempotencyKey) {
		return referraldomain.Campaign{}, referralport.ErrConflict
	}
	actor := adminScope(command.ActorAdminID)
	payload := campaignCreatePayload(command)
	var result referraldomain.Campaign
	err := s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "campaign_create", actor, command.IdempotencyKey); err != nil {
			return err
		}
		if receipt, found, err := s.store.ReadOperationReceiptWithin(tx, "campaign_create", actor, command.IdempotencyKey); err != nil || found {
			if err != nil {
				return err
			}
			if receipt.PayloadDigest != payload || receipt.ResultKind != "campaign" {
				return referralport.ErrConflict
			}
			var readErr error
			result, readErr = s.store.ReadCampaignWithin(tx, receipt.ResultID, false)
			return readErr
		}
		now := s.now().UTC()
		config := createCampaignConfig(command)
		candidate := referraldomain.Campaign{Name: command.Name, CoverURL: command.CoverURL, Description: command.Description, RewardRules: command.RewardRules, State: referraldomain.CampaignDraft, StartsAt: command.StartsAt.UTC(), EndsAt: command.EndsAt.UTC(), Version: 1, CreatedBy: command.ActorAdminID, CreatedAt: now, UpdatedAt: now, TeamMode: config.TeamMode, QualificationMode: config.QualificationMode, ProductID: config.ProductID, ProductType: config.ProductType, LeaderboardMetric: config.LeaderboardMetric}
		if !candidate.ValidForInsert() {
			return referralport.ErrConflict
		}
		var err error
		result, err = s.store.InsertCampaignWithin(tx, candidate)
		if err != nil {
			return err
		}
		if err = s.store.AppendOperationReceiptWithin(tx, "campaign_create", actor, command.IdempotencyKey, payload, "campaign", result.ID, now); err != nil {
			return err
		}
		return appendPlatformEvent(tx, s.audit, s.outbox, "referral.campaign.created", "campaign", result.ID, "admin", command.ActorAdminID, command.IdempotencyKey, map[string]any{"campaign_id": result.ID, "state": result.State}, now)
	})
	return result, err
}

func (s *AdminService) UpdateCampaign(ctx context.Context, command referralport.UpdateCampaignCommand) (referraldomain.Campaign, error) {
	if s == nil || command.CampaignID < 1 || command.ExpectedVersion < 1 || !validAdminCommand(command.ActorAdminID, command.IdempotencyKey) {
		return referraldomain.Campaign{}, referralport.ErrConflict
	}
	actor := adminScope(command.ActorAdminID)
	payload := campaignUpdatePayload(command)
	var result referraldomain.Campaign
	err := s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "campaign_update", actor, command.IdempotencyKey); err != nil {
			return err
		}
		if receipt, found, err := s.store.ReadOperationReceiptWithin(tx, "campaign_update", actor, command.IdempotencyKey); err != nil || found {
			if err != nil {
				return err
			}
			if receipt.PayloadDigest != payload || receipt.ResultKind != "campaign" {
				return referralport.ErrConflict
			}
			var readErr error
			result, readErr = s.store.ReadCampaignWithin(tx, receipt.ResultID, false)
			return readErr
		}
		current, err := s.store.ReadCampaignWithin(tx, command.CampaignID, true)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		next, err := current.UpdateWithConfig(command.ExpectedVersion, command.Name, command.CoverURL, command.Description, command.RewardRules, command.StartsAt, command.EndsAt, updateCampaignConfig(current, command), now)
		if err != nil {
			if errors.Is(err, referraldomain.ErrTransition) {
				return referralport.ErrCampaignConfigLocked
			}
			if errors.Is(err, referraldomain.ErrInvalid) {
				return referralport.ErrInvalidRequest
			}
			return mapDomainError(err)
		}
		result, err = s.store.UpdateCampaignWithin(tx, next, current.Version)
		if err != nil {
			return err
		}
		if result.State == referraldomain.CampaignScheduled || result.State == referraldomain.CampaignActive {
			if err = s.closeJobs.EnqueueCampaignCloseWithin(tx, result.ID, result.EndsAt); err != nil {
				return err
			}
		}
		if err = s.store.AppendOperationReceiptWithin(tx, "campaign_update", actor, command.IdempotencyKey, payload, "campaign", result.ID, now); err != nil {
			return err
		}
		return appendPlatformEvent(tx, s.audit, s.outbox, "referral.campaign.updated", "campaign", result.ID, "admin", command.ActorAdminID, command.IdempotencyKey, map[string]any{"campaign_id": result.ID, "version": result.Version}, now)
	})
	return result, err
}

func (s *AdminService) SetCampaignState(ctx context.Context, command referralport.SetCampaignStateCommand) (referraldomain.Campaign, error) {
	if s == nil || command.CampaignID < 1 || command.ExpectedVersion < 1 || !command.Target.Valid() || !validAdminCommand(command.ActorAdminID, command.IdempotencyKey) {
		return referraldomain.Campaign{}, referralport.ErrConflict
	}
	actor := adminScope(command.ActorAdminID)
	payload := statePayload(command)
	var result referraldomain.Campaign
	err := s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "campaign_state", actor, command.IdempotencyKey); err != nil {
			return err
		}
		if receipt, found, err := s.store.ReadOperationReceiptWithin(tx, "campaign_state", actor, command.IdempotencyKey); err != nil || found {
			if err != nil {
				return err
			}
			if receipt.PayloadDigest != payload || receipt.ResultKind != "campaign" {
				return referralport.ErrConflict
			}
			var readErr error
			result, readErr = s.store.ReadCampaignWithin(tx, receipt.ResultID, false)
			return readErr
		}
		current, err := s.store.ReadCampaignWithin(tx, command.CampaignID, true)
		if err != nil {
			return err
		}
		if current.Config().TeamMode == referraldomain.TeamModeIndividual && command.Target == referraldomain.CampaignActive {
			hasTeam, teamErr := s.store.HasTeamParticipationWithin(tx, current.ID)
			if teamErr != nil {
				return teamErr
			}
			if hasTeam {
				return referralport.ErrConflict
			}
		}
		now := s.now().UTC()
		next, err := current.Transition(command.ExpectedVersion, command.Target, now)
		if err != nil {
			return mapDomainError(err)
		}
		result, err = s.store.UpdateCampaignWithin(tx, next, current.Version)
		if err != nil {
			return err
		}
		if result.State == referraldomain.CampaignScheduled || result.State == referraldomain.CampaignActive {
			if err = s.closeJobs.EnqueueCampaignCloseWithin(tx, result.ID, result.EndsAt); err != nil {
				return err
			}
		}
		if err = s.store.AppendOperationReceiptWithin(tx, "campaign_state", actor, command.IdempotencyKey, payload, "campaign", result.ID, now); err != nil {
			return err
		}
		return appendPlatformEvent(tx, s.audit, s.outbox, "referral.campaign.state_changed", "campaign", result.ID, "admin", command.ActorAdminID, command.IdempotencyKey, map[string]any{"campaign_id": result.ID, "state": result.State, "version": result.Version}, now)
	})
	return result, err
}

func (s *AdminService) CreateTeam(ctx context.Context, command referralport.CreateTeamCommand) (referraldomain.Team, error) {
	if s == nil || command.CampaignID < 1 || command.CaptainCustomerID < 1 || !validAdminCommand(command.ActorAdminID, command.IdempotencyKey) {
		return referraldomain.Team{}, referralport.ErrConflict
	}
	actor := adminScope(command.ActorAdminID)
	payload := teamPayload(command)
	var result referraldomain.Team
	err := s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "team_create", actor, command.IdempotencyKey); err != nil {
			return err
		}
		if receipt, found, err := s.store.ReadOperationReceiptWithin(tx, "team_create", actor, command.IdempotencyKey); err != nil || found {
			if err != nil {
				return err
			}
			if receipt.PayloadDigest != payload || receipt.ResultKind != "team" {
				return referralport.ErrIdempotencyConflict
			}
			var readErr error
			result, readErr = s.store.ReadTeamWithin(tx, receipt.ResultID, false)
			return readErr
		}
		trusted, verifyErr := s.verifier.VerifyCanonicalCustomer(tx, command.CaptainCustomerID)
		if verifyErr != nil {
			return verifyErr
		}
		if !trusted {
			return referralport.ErrCaptainIneligible
		}
		campaign, err := s.store.ReadCampaignWithin(tx, command.CampaignID, true)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		switch campaign.EffectiveState(now) {
		case referraldomain.CampaignDraft, referraldomain.CampaignScheduled, referraldomain.CampaignActive:
		default:
			return referralport.ErrCampaignTeamLocked
		}
		candidate := referraldomain.Team{CampaignID: campaign.ID, Name: command.Name, LogoURL: command.LogoURL, CaptainCustomerID: command.CaptainCustomerID, Version: 1, CreatedAt: now, UpdatedAt: now}
		if !candidate.ValidForInsert() {
			return referralport.ErrConflict
		}
		result, err = s.store.InsertTeamWithin(tx, candidate)
		if err != nil {
			return err
		}
		if err = s.store.AppendOperationReceiptWithin(tx, "team_create", actor, command.IdempotencyKey, payload, "team", result.ID, now); err != nil {
			return err
		}
		return appendPlatformEvent(tx, s.audit, s.outbox, "referral.team.created", "team", result.ID, "admin", command.ActorAdminID, command.IdempotencyKey, map[string]any{"campaign_id": campaign.ID, "team_id": result.ID}, now)
	})
	return result, err
}

func (s *AdminService) ReverseInvitation(ctx context.Context, command referralport.ReverseInvitationCommand) error {
	if s == nil || command.ParticipationID < 1 || command.Reason != strings.TrimSpace(command.Reason) || command.Reason == "" || len(command.Reason) > 500 || !validAdminCommand(command.ActorAdminID, command.IdempotencyKey) {
		return referralport.ErrConflict
	}
	actor := adminScope(command.ActorAdminID)
	payload := reversePayload(command)
	return s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "invite_reverse", actor, command.IdempotencyKey); err != nil {
			return err
		}
		if receipt, found, err := s.store.ReadOperationReceiptWithin(tx, "invite_reverse", actor, command.IdempotencyKey); err != nil || found {
			if err != nil {
				return err
			}
			if receipt.PayloadDigest != payload || receipt.ResultKind != "participation" {
				return referralport.ErrConflict
			}
			return nil
		}
		participation, err := s.store.ReadParticipationByIDWithin(tx, command.ParticipationID, true)
		if err != nil {
			return err
		}
		if participation.State != referraldomain.ParticipationActive || participation.InvitationID < 1 {
			return referralport.ErrConflict
		}
		credit, err := s.store.ReadCreditScoreEventByParticipationWithin(tx, participation.ID, true)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		reversal, err := s.store.InsertScoreEventWithin(tx, referraldomain.ScoreEvent{CampaignID: credit.CampaignID, ParticipationID: participation.ID, InviterCustomerID: credit.InviterCustomerID, TeamID: credit.TeamID, Kind: referraldomain.ScoreReverse, Delta: -1, ReversesScoreEventID: credit.ID, Reason: command.Reason, OccurredAt: now})
		if err != nil {
			return err
		}
		if _, err = s.store.ReverseParticipationWithin(tx, participation); err != nil {
			return err
		}
		if err = s.store.FlagRewardsForScoreReversalWithin(tx, credit.ID, command.Reason, now); err != nil {
			return err
		}
		if err = s.store.AppendOperationReceiptWithin(tx, "invite_reverse", actor, command.IdempotencyKey, payload, "participation", participation.ID, now); err != nil {
			return err
		}
		return appendPlatformEvent(tx, s.audit, s.outbox, "referral.invitation.reversed", "participation", participation.ID, "admin", command.ActorAdminID, command.IdempotencyKey, map[string]any{"participation_id": participation.ID, "score_event_id": credit.ID, "reversal_event_id": reversal.ID}, now)
	})
}

func (s *AdminService) RevokeInvitation(ctx context.Context, command referralport.RevokeInvitationCommand) error {
	if s == nil || command.InvitationID < 1 || command.Reason != strings.TrimSpace(command.Reason) || command.Reason == "" || len(command.Reason) > 500 || !validAdminCommand(command.ActorAdminID, command.IdempotencyKey) {
		return referralport.ErrConflict
	}
	actor := adminScope(command.ActorAdminID)
	payload := revokePayload(command)
	return s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "invite_revoke", actor, command.IdempotencyKey); err != nil {
			return err
		}
		if receipt, found, err := s.store.ReadOperationReceiptWithin(tx, "invite_revoke", actor, command.IdempotencyKey); err != nil || found {
			if err != nil {
				return err
			}
			if receipt.PayloadDigest != payload || receipt.ResultKind != "invitation" {
				return referralport.ErrConflict
			}
			return nil
		}
		invitation, err := s.store.ReadInvitationWithin(tx, command.InvitationID, true)
		if err != nil {
			return err
		}
		if invitation.State != referraldomain.InvitationActive {
			return referralport.ErrConflict
		}
		now := s.now().UTC()
		if err = s.store.UpdateInvitationStateWithin(tx, invitation, referraldomain.InvitationRevoked, now); err != nil {
			return err
		}
		if err = s.store.AppendOperationReceiptWithin(tx, "invite_revoke", actor, command.IdempotencyKey, payload, "invitation", invitation.ID, now); err != nil {
			return err
		}
		return appendPlatformEvent(tx, s.audit, s.outbox, "referral.invitation.revoked", "invitation", invitation.ID, "admin", command.ActorAdminID, command.IdempotencyKey, map[string]any{"invitation_id": invitation.ID, "reason": command.Reason}, now)
	})
}

func (s *AdminService) RecordReward(ctx context.Context, command referralport.RecordRewardCommand) (referraldomain.RewardRecord, error) {
	if s == nil || command.CampaignID < 1 || command.CustomerID < 1 || command.Period != strings.TrimSpace(command.Period) || command.Reward != strings.TrimSpace(command.Reward) || command.Reward == "" || len(command.Period) < 1 || len(command.Period) > 32 || len(command.Reward) > 500 || command.EvidenceReference != strings.TrimSpace(command.EvidenceReference) || len(command.EvidenceReference) > 500 || !validAdminCommand(command.ActorAdminID, command.IdempotencyKey) {
		return referraldomain.RewardRecord{}, referralport.ErrConflict
	}
	actor := adminScope(command.ActorAdminID)
	payload := rewardPayload(command)
	var result referraldomain.RewardRecord
	err := s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "reward_record", actor, command.IdempotencyKey); err != nil {
			return err
		}
		if receipt, found, err := s.store.ReadOperationReceiptWithin(tx, "reward_record", actor, command.IdempotencyKey); err != nil || found {
			if err != nil {
				return err
			}
			if receipt.PayloadDigest != payload || receipt.ResultKind != "reward" {
				return referralport.ErrConflict
			}
			var readErr error
			result, readErr = s.store.ReadRewardWithin(tx, receipt.ResultID, false)
			return readErr
		}
		campaign, err := s.store.ReadCampaignWithin(tx, command.CampaignID, false)
		if err != nil {
			return err
		}
		if campaign.State == referraldomain.CampaignDraft {
			return referralport.ErrConflict
		}
		participation, err := s.store.ReadParticipationWithin(tx, command.CampaignID, command.CustomerID, false)
		if err != nil || participation.State != referraldomain.ParticipationActive {
			return referralport.ErrConflict
		}
		if command.ScoreEventID > 0 {
			// Lock the credit first. ReverseInvitation locks the same immutable
			// credit before inserting its reversal, so either a prior reversal
			// rejects this award or a later reversal marks this record for review.
			event, eventErr := s.store.ReadScoreEventWithin(tx, command.ScoreEventID, true)
			if eventErr != nil || event.Kind != referraldomain.ScoreCredit || event.CampaignID != command.CampaignID || event.InviterCustomerID != command.CustomerID {
				return referralport.ErrConflict
			}
			reversed, reversalErr := s.store.HasReversalForCreditWithin(tx, event.ID)
			if reversalErr != nil {
				return reversalErr
			}
			if reversed {
				return referralport.ErrConflict
			}
		}
		now := s.now().UTC()
		candidate := referraldomain.RewardRecord{CampaignID: command.CampaignID, CustomerID: command.CustomerID, ScoreEventID: command.ScoreEventID, Period: command.Period, Reward: command.Reward, EvidenceReference: command.EvidenceReference, State: referraldomain.RewardRecorded, RecordedBy: command.ActorAdminID, RecordedAt: now}
		result, err = s.store.InsertRewardWithin(tx, candidate)
		if err != nil {
			return err
		}
		if err = s.store.AppendOperationReceiptWithin(tx, "reward_record", actor, command.IdempotencyKey, payload, "reward", result.ID, now); err != nil {
			return err
		}
		return appendPlatformEvent(tx, s.audit, s.outbox, "referral.reward.recorded", "reward", result.ID, "admin", command.ActorAdminID, command.IdempotencyKey, map[string]any{"campaign_id": result.CampaignID, "reward_id": result.ID, "customer_id": result.CustomerID}, now)
	})
	return result, err
}

// RunCampaignClose is invoked only by the durable River worker. It takes a
// real rankings snapshot at the close watermark; ordinary historical boards
// remain source-of-truth and later reversals remain explicit corrections.
func (s *AdminService) RunCampaignClose(ctx context.Context, campaignID int64) error {
	if s == nil || campaignID < 1 {
		return referralport.ErrConflict
	}
	return s.uow.Within(ctx, func(tx context.Context) error {
		campaign, err := s.store.ReadCampaignWithin(tx, campaignID, true)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if now.Before(campaign.EndsAt) {
			return s.closeJobs.EnqueueCampaignCloseWithin(tx, campaign.ID, campaign.EndsAt)
		}
		if campaign.State == referraldomain.CampaignDraft || campaign.State == referraldomain.CampaignDisabled {
			return nil
		}
		if campaign.State != referraldomain.CampaignEnded {
			next, transitionErr := campaign.Transition(campaign.Version, referraldomain.CampaignEnded, now)
			if transitionErr != nil {
				return mapDomainError(transitionErr)
			}
			campaign, err = s.store.UpdateCampaignWithin(tx, next, campaign.Version)
			if err != nil {
				return err
			}
		}
		key := "campaign-close-" + strconv.FormatInt(campaign.ID, 10)
		actor := "worker:referral-close"
		payload := sha256.Sum256([]byte("referral.close.v1:" + strconv.FormatInt(campaign.ID, 10)))
		if err = s.store.LockOperationReceiptWithin(tx, "campaign_close", actor, key); err != nil {
			return err
		}
		if receipt, found, err := s.store.ReadOperationReceiptWithin(tx, "campaign_close", actor, key); err != nil || found {
			if err != nil {
				return err
			}
			if receipt.PayloadDigest != payload || receipt.ResultKind != "snapshot" {
				return referralport.ErrConflict
			}
			return nil
		}
		snapshotID, err := s.store.InsertCampaignCloseSnapshotWithin(tx, campaign.ID, now)
		if err != nil {
			return err
		}
		if err = s.store.AppendOperationReceiptWithin(tx, "campaign_close", actor, key, payload, "snapshot", snapshotID, now); err != nil {
			return err
		}
		return appendPlatformEvent(tx, s.audit, s.outbox, "referral.campaign.closed", "campaign", campaign.ID, "worker", 0, key, map[string]any{"campaign_id": campaign.ID, "snapshot_id": snapshotID}, now)
	})
}

func (s *AdminService) ReadAdminCampaign(ctx context.Context, campaignID int64) (referralport.CampaignView, error) {
	if s == nil || campaignID < 1 {
		return referralport.CampaignView{}, referralport.ErrConflict
	}
	var campaign referraldomain.Campaign
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		campaign, err = s.store.ReadCampaignWithin(tx, campaignID, false)
		return err
	})
	if err != nil {
		return referralport.CampaignView{}, err
	}
	return s.adminCampaignView(ctx, campaign)
}
func (s *AdminService) ListAdminCampaigns(ctx context.Context, cursor string, limit int32) (referralport.AdminCampaignPage, error) {
	if s == nil || limit < 1 || limit > 100 {
		return referralport.AdminCampaignPage{}, referralport.ErrConflict
	}
	offset, err := referralstore.OffsetCursor(cursor)
	if err != nil {
		return referralport.AdminCampaignPage{}, err
	}
	var values []referraldomain.Campaign
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		values, err = s.store.ListCampaignsWithin(tx, offset, limit+1, false)
		return err
	})
	if err != nil {
		return referralport.AdminCampaignPage{}, err
	}
	page := referralport.AdminCampaignPage{Items: make([]referralport.CampaignSummary, 0, len(values))}
	for _, campaign := range values {
		summary, readErr := s.adminCampaignSummary(ctx, campaign)
		if readErr != nil {
			return referralport.AdminCampaignPage{}, readErr
		}
		page.Items = append(page.Items, summary)
	}
	if int32(len(page.Items)) > limit {
		page.Items = page.Items[:limit]
		page.NextCursor = referralstore.NextOffsetCursor(offset, int(limit), limit)
	}
	return page, nil
}
func (s *AdminService) ListAdminReferrals(ctx context.Context, campaignID int64, cursor string, limit int32) (referralport.AdminReferralPage, error) {
	if s == nil || campaignID < 1 || limit < 1 || limit > 100 {
		return referralport.AdminReferralPage{}, referralport.ErrConflict
	}
	offset, err := referralstore.OffsetCursor(cursor)
	if err != nil {
		return referralport.AdminReferralPage{}, err
	}
	var values []referralport.AdminReferralRecord
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		values, err = s.store.ListAdminReferralsWithin(tx, campaignID, offset, limit+1)
		return err
	})
	if err != nil {
		return referralport.AdminReferralPage{}, err
	}
	page := referralport.AdminReferralPage{Items: values}
	if int32(len(values)) > limit {
		page.Items = values[:limit]
		page.NextCursor = referralstore.NextOffsetCursor(offset, int(limit), limit)
	}
	return page, nil
}

func (s *AdminService) ListAdminParticipants(ctx context.Context, query referralport.AdminParticipantQuery) (referralport.AdminParticipantPage, error) {
	if s == nil || !validAdminParticipantQuery(query) {
		return referralport.AdminParticipantPage{}, referralport.ErrConflict
	}
	after, err := referralstore.ParseAdminJoinedCursor(query.Cursor)
	if err != nil {
		return referralport.AdminParticipantPage{}, err
	}
	var values []referralport.AdminParticipantRecord
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		values, readErr = s.store.ListAdminParticipantsWithin(tx, query, after, query.Limit+1)
		return readErr
	})
	if err != nil {
		return referralport.AdminParticipantPage{}, err
	}
	page := referralport.AdminParticipantPage{Items: values}
	if int32(len(values)) > query.Limit {
		page.Items = values[:query.Limit]
		last := page.Items[len(page.Items)-1].Participation
		page.NextCursor = referralstore.EncodeAdminJoinedCursor(last.JoinedAt, last.ID)
	}
	return page, nil
}

func (s *AdminService) ListAdminInvitations(ctx context.Context, query referralport.AdminInvitationQuery) (referralport.AdminInvitationPage, error) {
	if s == nil || !validAdminInvitationQuery(query) {
		return referralport.AdminInvitationPage{}, referralport.ErrConflict
	}
	after, err := referralstore.ParseAdminJoinedCursor(query.Cursor)
	if err != nil {
		return referralport.AdminInvitationPage{}, err
	}
	var values []referralport.AdminInvitationRecord
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		values, readErr = s.store.ListAdminInvitationsWithin(tx, query, after, query.Limit+1)
		return readErr
	})
	if err != nil {
		return referralport.AdminInvitationPage{}, err
	}
	page := referralport.AdminInvitationPage{Items: values}
	if int32(len(values)) > query.Limit {
		page.Items = values[:query.Limit]
		last := page.Items[len(page.Items)-1].Participation
		page.NextCursor = referralstore.EncodeAdminJoinedCursor(last.JoinedAt, last.ID)
	}
	return page, nil
}

// ListAdminParticipantInvitations resolves the inviter from a campaign-owned
// participation before listing their direct invitees. This avoids treating a
// browser-supplied canonical customer identifier as a drilldown authority.
func (s *AdminService) ListAdminParticipantInvitations(ctx context.Context, query referralport.AdminParticipantInvitationQuery) (referralport.AdminInvitationPage, error) {
	if s == nil || !validAdminParticipantInvitationQuery(query) {
		return referralport.AdminInvitationPage{}, referralport.ErrConflict
	}
	after, err := referralstore.ParseAdminJoinedCursor(query.Cursor)
	if err != nil {
		return referralport.AdminInvitationPage{}, err
	}
	var values []referralport.AdminInvitationRecord
	err = s.uow.Within(ctx, func(tx context.Context) error {
		participation, readErr := s.store.ReadParticipationByIDWithin(tx, query.ParticipationID, false)
		if readErr != nil {
			return readErr
		}
		if participation.CampaignID != query.CampaignID {
			return referralport.ErrNotFound
		}
		values, readErr = s.store.ListAdminInvitationsWithin(tx, referralport.AdminInvitationQuery{
			CampaignID:        query.CampaignID,
			InviterCustomerID: participation.CustomerID,
			State:             query.State,
		}, after, query.Limit+1)
		return readErr
	})
	if err != nil {
		return referralport.AdminInvitationPage{}, err
	}
	page := referralport.AdminInvitationPage{Items: values}
	if int32(len(values)) > query.Limit {
		page.Items = values[:query.Limit]
		last := page.Items[len(page.Items)-1].Participation
		page.NextCursor = referralstore.EncodeAdminJoinedCursor(last.JoinedAt, last.ID)
	}
	return page, nil
}

func (s *AdminService) ListAdminParticipantsForExport(ctx context.Context, query referralport.AdminParticipantQuery) ([]referralport.AdminParticipantRecord, error) {
	if s == nil || !validAdminParticipantExportQuery(query) {
		return nil, referralport.ErrConflict
	}
	var values []referralport.AdminParticipantRecord
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		values, readErr = s.store.ListAdminParticipantsForExportWithin(tx, query)
		return readErr
	})
	return values, err
}

func (s *AdminService) ListAdminInvitationsForExport(ctx context.Context, query referralport.AdminInvitationQuery) ([]referralport.AdminInvitationRecord, error) {
	if s == nil || !validAdminInvitationExportQuery(query) {
		return nil, referralport.ErrConflict
	}
	var values []referralport.AdminInvitationRecord
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		values, readErr = s.store.ListAdminInvitationsForExportWithin(tx, query)
		return readErr
	})
	return values, err
}

// ListAdminParticipantInvitationsForExport keeps the export scoped to a
// campaign-owned participation. The subsequent store read is one unpaged SQL
// statement for all matching invitees; the browser never supplies an inviter
// customer identifier.
func (s *AdminService) ListAdminParticipantInvitationsForExport(ctx context.Context, query referralport.AdminParticipantInvitationQuery) ([]referralport.AdminInvitationRecord, error) {
	if s == nil || !validAdminParticipantInvitationExportQuery(query) {
		return nil, referralport.ErrConflict
	}
	var values []referralport.AdminInvitationRecord
	err := s.uow.Within(ctx, func(tx context.Context) error {
		participation, readErr := s.store.ReadParticipationByIDWithin(tx, query.ParticipationID, false)
		if readErr != nil {
			return readErr
		}
		if participation.CampaignID != query.CampaignID {
			return referralport.ErrNotFound
		}
		values, readErr = s.store.ListAdminParticipantInvitationsForExportWithin(tx, query, participation.CustomerID)
		return readErr
	})
	return values, err
}

func (s *AdminService) ListRelationshipHistory(ctx context.Context, customerID int64, cursor string, limit int32) (referralport.RelationshipHistoryPage, error) {
	if s == nil || customerID < 1 || limit < 1 || limit > 100 {
		return referralport.RelationshipHistoryPage{}, referralport.ErrConflict
	}
	offset, err := referralstore.OffsetCursor(cursor)
	if err != nil {
		return referralport.RelationshipHistoryPage{}, err
	}
	var values []referraldomain.RelationshipHistory
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		values, err = s.store.ListRelationshipHistoryWithin(tx, customerID, offset, limit+1)
		return err
	})
	if err != nil {
		return referralport.RelationshipHistoryPage{}, err
	}
	page := referralport.RelationshipHistoryPage{Items: values}
	if int32(len(values)) > limit {
		page.Items = values[:limit]
		page.NextCursor = referralstore.NextOffsetCursor(offset, int(limit), limit)
	}
	return page, nil
}
func (s *AdminService) ListRewards(ctx context.Context, campaignID int64, cursor string, limit int32) (referralport.RewardPage, error) {
	if s == nil || campaignID < 1 || limit < 1 || limit > 100 {
		return referralport.RewardPage{}, referralport.ErrConflict
	}
	offset, err := referralstore.OffsetCursor(cursor)
	if err != nil {
		return referralport.RewardPage{}, err
	}
	var values []referraldomain.RewardRecord
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		values, err = s.store.ListRewardsWithin(tx, campaignID, offset, limit+1)
		return err
	})
	if err != nil {
		return referralport.RewardPage{}, err
	}
	page := referralport.RewardPage{Items: values}
	if int32(len(values)) > limit {
		page.Items = values[:limit]
		page.NextCursor = referralstore.NextOffsetCursor(offset, int(limit), limit)
	}
	return page, nil
}

func (s *AdminService) adminCampaignSummary(ctx context.Context, campaign referraldomain.Campaign) (referralport.CampaignSummary, error) {
	var counts referralstore.CampaignCounts
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		counts, err = s.store.CampaignCountsWithin(tx, campaign.ID)
		return err
	})
	if err != nil {
		return referralport.CampaignSummary{}, err
	}
	if campaign.Config().TeamMode == referraldomain.TeamModeIndividual {
		counts.TeamCount = 0
	}
	return referralport.CampaignSummary{Campaign: campaign, EffectiveState: campaign.EffectiveState(s.now().UTC()), ParticipantCount: counts.ParticipantCount, InvitationCount: counts.InvitationCount, TeamCount: counts.TeamCount}, nil
}
func (s *AdminService) adminCampaignView(ctx context.Context, campaign referraldomain.Campaign) (referralport.CampaignView, error) {
	summary, err := s.adminCampaignSummary(ctx, campaign)
	if err != nil {
		return referralport.CampaignView{}, err
	}
	var teams []referraldomain.Team
	var teamSummaries []referralport.AdminTeamSummary
	var daily []referralport.CampaignDailyMetric
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		if campaign.Config().TeamMode == referraldomain.TeamModeTeam {
			teams, err = s.store.ListTeamsWithin(tx, campaign.ID)
			if err != nil {
				return err
			}
			teamSummaries, err = s.store.ListAdminTeamSummariesWithin(tx, campaign.ID)
			if err != nil {
				return err
			}
		}
		daily, err = s.store.CampaignDailyMetricsWithin(tx, campaign.ID)
		return err
	})
	if err != nil {
		return referralport.CampaignView{}, err
	}
	return referralport.CampaignView{CampaignSummary: summary, Teams: teams, TeamSummaries: teamSummaries, DailyMetrics: daily}, nil
}

func appendPlatformEvent(ctx context.Context, audit *platformaudit.Service, outbox platformoutbox.Appender, eventType, resourceType string, resourceID int64, actorType string, actorID int64, commandKey string, payload any, at time.Time) error {
	return (&Service{audit: audit, outbox: outbox}).appendEvent(ctx, eventType, resourceType, resourceID, actorType, actorID, commandKey, payload, at)
}
func validAdminCommand(actorID int64, key string) bool { return actorID > 0 && validKey(key) }

func validAdminParticipantQuery(query referralport.AdminParticipantQuery) bool {
	return query.CampaignID > 0 && query.TeamID >= 0 && (query.State == "" || query.State.Valid()) && query.Limit >= 1 && query.Limit <= 100
}

func validAdminInvitationQuery(query referralport.AdminInvitationQuery) bool {
	return query.CampaignID > 0 && query.TeamID >= 0 && query.InviterCustomerID >= 0 && (query.State == "" || query.State.Valid()) && query.Limit >= 1 && query.Limit <= 100
}

func validAdminParticipantInvitationQuery(query referralport.AdminParticipantInvitationQuery) bool {
	return query.CampaignID > 0 && query.ParticipationID > 0 && (query.State == "" || query.State.Valid()) && query.Limit >= 1 && query.Limit <= 100
}

func validAdminParticipantInvitationExportQuery(query referralport.AdminParticipantInvitationQuery) bool {
	return query.CampaignID > 0 && query.ParticipationID > 0 && (query.State == "" || query.State.Valid()) && query.Cursor == ""
}

func validAdminParticipantExportQuery(query referralport.AdminParticipantQuery) bool {
	return query.CampaignID > 0 && query.TeamID >= 0 && (query.State == "" || query.State.Valid()) && query.Cursor == ""
}

func validAdminInvitationExportQuery(query referralport.AdminInvitationQuery) bool {
	return query.CampaignID > 0 && query.TeamID >= 0 && query.InviterCustomerID >= 0 && (query.State == "" || query.State.Valid()) && query.Cursor == ""
}
func mapDomainError(err error) error {
	if errors.Is(err, referraldomain.ErrVersion) || errors.Is(err, referraldomain.ErrTransition) || errors.Is(err, referraldomain.ErrInvalid) {
		return referralport.ErrConflict
	}
	return err
}
func campaignCreatePayload(c referralport.CreateCampaignCommand) [sha256.Size]byte {
	return digestStrings("create", c.Name, c.CoverURL, c.Description, c.RewardRules, c.StartsAt.UTC().Format(time.RFC3339Nano), c.EndsAt.UTC().Format(time.RFC3339Nano), string(c.TeamMode), string(c.QualificationMode), strconv.FormatInt(c.ProductID, 10), c.ProductType, string(c.LeaderboardMetric))
}
func campaignUpdatePayload(c referralport.UpdateCampaignCommand) [sha256.Size]byte {
	return digestStrings("update", strconv.FormatInt(c.CampaignID, 10), strconv.FormatInt(c.ExpectedVersion, 10), c.Name, c.CoverURL, c.Description, c.RewardRules, c.StartsAt.UTC().Format(time.RFC3339Nano), c.EndsAt.UTC().Format(time.RFC3339Nano), string(c.TeamMode), string(c.QualificationMode), strconv.FormatInt(c.ProductID, 10), c.ProductType, string(c.LeaderboardMetric))
}

func createCampaignConfig(c referralport.CreateCampaignCommand) referraldomain.CampaignConfig {
	return referraldomain.CampaignConfig{TeamMode: c.TeamMode, QualificationMode: c.QualificationMode, ProductID: c.ProductID, ProductType: c.ProductType, LeaderboardMetric: c.LeaderboardMetric}
}

func updateCampaignConfig(current referraldomain.Campaign, c referralport.UpdateCampaignCommand) referraldomain.CampaignConfig {
	config := current.Config()
	// Zero-valued fields are omitted by legacy clients and therefore preserve
	// the current configuration. Explicit mode values replace it atomically.
	if c.TeamMode != "" {
		config.TeamMode = c.TeamMode
	}
	if c.QualificationMode != "" {
		config.QualificationMode = c.QualificationMode
		if c.QualificationMode == referraldomain.QualificationFreeSignup {
			config.ProductID, config.ProductType = 0, ""
		}
	}
	if c.ProductID != 0 || c.ProductType != "" {
		config.ProductID, config.ProductType = c.ProductID, c.ProductType
	}
	if c.LeaderboardMetric != "" {
		config.LeaderboardMetric = c.LeaderboardMetric
	}
	return config
}
func statePayload(c referralport.SetCampaignStateCommand) [sha256.Size]byte {
	return digestStrings("state", strconv.FormatInt(c.CampaignID, 10), strconv.FormatInt(c.ExpectedVersion, 10), string(c.Target))
}
func teamPayload(c referralport.CreateTeamCommand) [sha256.Size]byte {
	return digestStrings("team", strconv.FormatInt(c.CampaignID, 10), strconv.FormatInt(c.CaptainCustomerID, 10), c.Name, c.LogoURL)
}
func reversePayload(c referralport.ReverseInvitationCommand) [sha256.Size]byte {
	return digestStrings("reverse", strconv.FormatInt(c.ParticipationID, 10), c.Reason)
}
func revokePayload(c referralport.RevokeInvitationCommand) [sha256.Size]byte {
	return digestStrings("revoke", strconv.FormatInt(c.InvitationID, 10), c.Reason)
}
func rewardPayload(c referralport.RecordRewardCommand) [sha256.Size]byte {
	return digestStrings("reward", strconv.FormatInt(c.CampaignID, 10), strconv.FormatInt(c.CustomerID, 10), strconv.FormatInt(c.ScoreEventID, 10), c.Period, c.Reward, c.EvidenceReference)
}
func digestStrings(parts ...string) [sha256.Size]byte {
	return sha256.Sum256([]byte(strings.Join(parts, "\x00")))
}
