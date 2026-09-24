// Package app coordinates Referral use cases inside one PostgreSQL Unit of
// Work.  Trusted sessions are supplied through Distribution's stable port; no
// HTTP request can choose a customer or a score.
package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformidempotency "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
)

var cryptoRandRead = rand.Read

const invitationTTL = 30 * 24 * time.Hour

type CampaignCloseEnqueuer interface {
	EnqueueCampaignCloseWithin(context.Context, int64, time.Time) error
}

type serviceStore interface {
	LockOperationReceiptWithin(context.Context, string, string, string) error
	ReadOperationReceiptWithin(context.Context, string, string, string) (referralstore.OperationReceipt, bool, error)
	AppendOperationReceiptWithin(context.Context, string, string, string, [sha256.Size]byte, string, int64, time.Time) error
	ReadCampaignWithin(context.Context, int64, bool) (referraldomain.Campaign, error)
	ListCampaignsWithin(context.Context, int32, int32, bool) ([]referraldomain.Campaign, error)
	ProductCampaignsAtWithin(context.Context, int64, string, time.Time) ([]referraldomain.Campaign, error)
	CampaignCountsWithin(context.Context, int64) (referralstore.CampaignCounts, error)
	ListTeamsWithin(context.Context, int64) ([]referraldomain.Team, error)
	CampaignDailyMetricsWithin(context.Context, int64) ([]referralport.CampaignDailyMetric, error)
	ReadTeamWithin(context.Context, int64, bool) (referraldomain.Team, error)
	ReadCaptainTeamWithin(context.Context, int64, int64) (referraldomain.Team, bool, error)
	LockParticipationWithin(context.Context, int64, int64) error
	ReadParticipationWithin(context.Context, int64, int64, bool) (referraldomain.Participation, error)
	InsertParticipationWithin(context.Context, referraldomain.Participation) (referraldomain.Participation, error)
	ReadInvitationByDigestWithin(context.Context, [sha256.Size]byte, bool) (referraldomain.Invitation, error)
	InsertInvitationWithin(context.Context, referraldomain.Invitation) (referraldomain.Invitation, error)
	UpdateInvitationStateWithin(context.Context, referraldomain.Invitation, referraldomain.InvitationState, time.Time) error
	LockRelationshipCustomerWithin(context.Context, int64) error
	ReadCurrentRelationshipWithin(context.Context, int64, bool) (referraldomain.Relationship, error)
	InsertCurrentRelationshipWithin(context.Context, referraldomain.Relationship) (referraldomain.Relationship, error)
	UpdateCurrentRelationshipWithin(context.Context, referraldomain.Relationship, int64) (referraldomain.Relationship, error)
	InsertRelationshipHistoryWithin(context.Context, referraldomain.RelationshipHistory) error
	InsertScoreEventWithin(context.Context, referraldomain.ScoreEvent) (referraldomain.ScoreEvent, error)
	CurrentRelationshipAtWithin(context.Context, int64, time.Time) (referraldomain.Relationship, bool, error)
	CountDirectInvitationsWithin(context.Context, int64, int64) (int64, error)
	ListInviteItemsWithin(context.Context, int64, int64, int32, int32) ([]referralport.InviteItem, error)
	LeaderboardRowsWithin(context.Context, int64, referralport.LeaderboardKind, int64, time.Time, time.Time, int32, int32, int64, int64) ([]referralport.LeaderboardEntry, *referralport.LeaderboardEntry, error)
	ListSalesLeaderboardRowsWithin(context.Context, int64, referralport.LeaderboardKind, referraldomain.LeaderboardMetric, int64, time.Time, time.Time, int32, int32, int64, int64) ([]referralport.LeaderboardEntry, *referralport.LeaderboardEntry, error)
	InsertProductActivityContextWithin(context.Context, referralport.ProductActivityContext) error
}

// IssueProductActivityContext mints an opaque, same-origin checkout context.
// It is deliberately separate from invitation credentials and contains no
// customer supplied identity or redirect URL.
func (s *Service) IssueProductActivityContext(ctx context.Context, actor referralport.TrustedSessionActor, campaignID int64, idempotencyKey string) (string, error) {
	if s == nil || !actor.Valid() || campaignID < 1 || !validKey(idempotencyKey) {
		return "", referralport.ErrConflict
	}
	var token string
	err := s.uow.Within(ctx, func(tx context.Context) error {
		campaign, err := s.store.ReadCampaignWithin(tx, campaignID, true)
		if err != nil {
			return err
		}
		if campaign.Config().QualificationMode != referraldomain.QualificationProductPurchase || !campaign.AcceptingAt(s.now().UTC()) {
			return referralport.ErrCampaignUnavailable
		}
		// The activity context is the server-issued bridge from the activity
		// page to the bound product checkout.  It must be issuable before the
		// first purchase; requiring an existing purchase here would make a
		// product-qualified activity circular (the buyer could never reach the
		// checkout that grants qualification).  Qualification is enforced when
		// the paid event is consumed and the participant is created.
		buf := make([]byte, 32)
		if _, err := cryptoRandRead(buf); err != nil {
			return referralport.ErrUnavailable
		}
		token = "rpa_" + base64.RawURLEncoding.EncodeToString(buf)
		digest := sha256.Sum256([]byte(token))
		metric := referralport.SalesMetricAmount
		if campaign.Config().LeaderboardMetric != referraldomain.LeaderboardInvites {
			// The activity config currently distinguishes invite versus sales
			// leaderboards; sales defaults to amount for the product context until
			// the persisted amount/order metric is exposed as its own field.
			metric = referralport.SalesMetricAmount
		}
		return s.store.InsertProductActivityContextWithin(tx, referralport.ProductActivityContext{ContextDigest: digest, CampaignID: campaign.ID, ProductID: campaign.ProductID, ProductType: campaign.ProductType, SalesMetric: metric, State: "active", ExpiresAt: campaign.EndsAt, CreatedAt: s.now().UTC()})
	})
	return token, err
}

type Service struct {
	uow           platformport.UnitOfWork
	store         serviceStore
	origin        string
	tokens        invitationTokenIssuer
	closeJobs     CampaignCloseEnqueuer
	audit         *platformaudit.Service
	outbox        platformoutbox.Appender
	qualification referralport.PurchaseQualificationReader
	attributions  distributionport.FrozenOrderAttributionReader
	timeline      platformaudit.TimelineReader
	now           func() time.Time
}

func NewService(uow platformport.UnitOfWork, store serviceStore, origin, tokenDataKey string, closeJobs CampaignCloseEnqueuer, audit *platformaudit.Service, outbox platformoutbox.Appender) (*Service, error) {
	if uow == nil || store == nil || closeJobs == nil || audit == nil || outbox == nil || !validOrigin(origin) {
		return nil, referralport.ErrUnavailable
	}
	tokens, err := newInvitationTokenIssuer(tokenDataKey)
	if err != nil {
		return nil, referralport.ErrUnavailable
	}
	return &Service{uow: uow, store: store, origin: strings.TrimRight(origin, "/"), tokens: tokens, closeJobs: closeJobs, audit: audit, outbox: outbox, now: time.Now}, nil
}

// SetPurchaseQualificationReader wires the existing Distribution qualification
// read seam. It is optional for free-signup campaigns, but product-qualified
// campaigns fail closed until composition supplies it.
func (s *Service) SetPurchaseQualificationReader(reader referralport.PurchaseQualificationReader) error {
	if s == nil || reader == nil {
		return referralport.ErrUnavailable
	}
	s.qualification = reader
	return nil
}

func (s *Service) SetSaleEvidenceReaders(attributions distributionport.FrozenOrderAttributionReader, timeline platformaudit.TimelineReader) error {
	if s == nil || attributions == nil || timeline == nil {
		return referralport.ErrUnavailable
	}
	s.attributions, s.timeline = attributions, timeline
	return nil
}

func (s *Service) checkPurchaseQualification(ctx context.Context, campaign referraldomain.Campaign, customerID int64) error {
	if campaign.Config().QualificationMode != referraldomain.QualificationProductPurchase {
		return nil
	}
	if s.qualification == nil {
		return referralport.ErrUnavailable
	}
	qualification, err := s.qualification.CheckWithin(ctx, customerID, campaign.ProductID, distributiondomain.ProductType(campaign.ProductType))
	if err != nil {
		return referralport.ErrUnavailable
	}
	if !qualification.AllowsPromotion() {
		return referralport.ErrParticipationRequired
	}
	return nil
}

func (s *Service) ListPublicCampaigns(ctx context.Context) ([]referralport.CampaignSummary, error) {
	if s == nil || s.uow == nil || s.store == nil {
		return nil, referralport.ErrUnavailable
	}
	var campaigns []referraldomain.Campaign
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		campaigns, err = s.store.ListCampaignsWithin(tx, 0, 100, true)
		return err
	})
	if err != nil {
		return nil, err
	}
	result := make([]referralport.CampaignSummary, 0, len(campaigns))
	for _, campaign := range campaigns {
		summary, readErr := s.campaignSummary(ctx, campaign)
		if readErr != nil {
			return nil, readErr
		}
		result = append(result, summary)
	}
	return result, nil
}

func (s *Service) ReadPublicCampaign(ctx context.Context, campaignID int64) (referralport.CampaignView, error) {
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
	if campaign.State == referraldomain.CampaignDraft {
		return referralport.CampaignView{}, referralport.ErrNotFound
	}
	return s.campaignView(ctx, campaign)
}

func (s *Service) PreviewInvitation(ctx context.Context, token string) (referralport.InvitationPreview, error) {
	if s == nil || !validInvitationToken(token) {
		return referralport.InvitationPreview{}, referralport.ErrNotFound
	}
	digest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	var invitation referraldomain.Invitation
	var campaign referraldomain.Campaign
	var participation referraldomain.Participation
	var team referraldomain.Team
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		invitation, err = s.store.ReadInvitationByDigestWithin(tx, digest, false)
		if err != nil {
			return err
		}
		if invitation.State != referraldomain.InvitationActive || !invitation.ExpiresAt.After(now) {
			return referralport.ErrInvitationInvalid
		}
		campaign, err = s.store.ReadCampaignWithin(tx, invitation.CampaignID, false)
		if err != nil {
			return err
		}
		if !campaign.AcceptingAt(now) {
			return referralport.ErrCampaignUnavailable
		}
		participation, err = s.store.ReadParticipationWithin(tx, campaign.ID, invitation.InviterCustomerID, false)
		if err != nil {
			return referralport.ErrInvitationInvalid
		}
		if participation.State != referraldomain.ParticipationActive {
			return referralport.ErrInvitationInvalid
		}
		if campaign.Config().TeamMode == referraldomain.TeamModeTeam && participation.TeamID > 0 {
			team, err = s.store.ReadTeamWithin(tx, participation.TeamID, false)
			if err != nil || team.CampaignID != campaign.ID {
				return referralport.ErrInvitationInvalid
			}
		}
		return nil
	})
	if err != nil {
		return referralport.InvitationPreview{}, err
	}
	summary, err := s.campaignSummary(ctx, campaign)
	if err != nil {
		return referralport.InvitationPreview{}, err
	}
	return referralport.InvitationPreview{Campaign: summary, InviterCustomerID: invitation.InviterCustomerID, InviterTeam: team, ExpiresAt: invitation.ExpiresAt}, nil
}

func (s *Service) JoinCampaign(ctx context.Context, command referralport.JoinCampaignCommand) (referralport.MyCampaign, error) {
	if s == nil || s.uow == nil || s.store == nil || !command.Actor.Valid() || command.CampaignID < 1 || command.TeamID < 0 || !validKey(command.IdempotencyKey) || (command.InvitationToken != "" && !validInvitationToken(command.InvitationToken)) {
		return referralport.MyCampaign{}, referralport.ErrConflict
	}
	actorScope := customerScope(command.Actor.CustomerID)
	payload := joinPayload(command)
	result := int64(0)
	err := s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "join", actorScope, command.IdempotencyKey); err != nil {
			return err
		}
		if receipt, found, err := s.store.ReadOperationReceiptWithin(tx, "join", actorScope, command.IdempotencyKey); err != nil || found {
			if err != nil {
				return err
			}
			if receipt.PayloadDigest != payload || receipt.ResultKind != "participation" {
				return referralport.ErrConflict
			}
			result = receipt.ResultID
			return nil
		}
		campaign, err := s.store.ReadCampaignWithin(tx, command.CampaignID, true)
		if err != nil {
			return err
		}
		var invitation referraldomain.Invitation
		invitationFound := false
		if command.InvitationToken != "" {
			digest := sha256.Sum256([]byte(command.InvitationToken))
			invitation, err = s.store.ReadInvitationByDigestWithin(tx, digest, true)
			if err == nil {
				invitationFound = true
			} else if !errors.Is(err, referralport.ErrNotFound) {
				return err
			}
		}
		lockFirst, lockSecond := command.Actor.CustomerID, int64(0)
		if invitationFound && invitation.InviterCustomerID != command.Actor.CustomerID {
			lockSecond = invitation.InviterCustomerID
			if lockSecond < lockFirst {
				lockFirst, lockSecond = lockSecond, lockFirst
			}
		}
		if err = s.store.LockParticipationWithin(tx, command.CampaignID, lockFirst); err != nil {
			return err
		}
		if lockSecond > 0 {
			if err = s.store.LockParticipationWithin(tx, command.CampaignID, lockSecond); err != nil {
				return err
			}
		}
		if invitationFound && invitation.InviterCustomerID == command.Actor.CustomerID {
			return referralport.ErrInvitationInvalid
		}
		if existing, readErr := s.store.ReadParticipationWithin(tx, command.CampaignID, command.Actor.CustomerID, true); readErr == nil {
			now := s.now().UTC()
			if err = s.store.AppendOperationReceiptWithin(tx, "join", actorScope, command.IdempotencyKey, payload, "participation", existing.ID, now); err != nil {
				return err
			}
			result = existing.ID
			return nil
		} else if !errors.Is(readErr, referralport.ErrNotFound) {
			return readErr
		}
		// Capture time after the stable relationship lock.  This makes committed
		// relation history order match its effective time under concurrent accepts.
		if err = s.store.LockRelationshipCustomerWithin(tx, command.Actor.CustomerID); err != nil {
			return err
		}
		now := s.now().UTC()
		if !campaign.AcceptingAt(now) {
			return referralport.ErrCampaignUnavailable
		}
		config := campaign.Config()
		if config.TeamMode == referraldomain.TeamModeIndividual && command.TeamID != 0 {
			return referralport.ErrConflict
		}
		if err = s.checkPurchaseQualification(tx, campaign, command.Actor.CustomerID); err != nil {
			return err
		}

		teamID, invitationID, inviterCustomerID, inviterTeamID := command.TeamID, int64(0), int64(0), int64(0)
		if command.InvitationToken != "" {
			if !invitationFound {
				return referralport.ErrInvitationInvalid
			}
			if invitation.State != referraldomain.InvitationActive || invitation.CampaignID != campaign.ID || !invitation.ExpiresAt.After(now) || invitation.InviterCustomerID == command.Actor.CustomerID {
				return referralport.ErrInvitationInvalid
			}
			// This lock coordinates invite acceptance with an administrator
			// reversal of the inviter's participation.  Reversal committed first
			// is observed as inactive below and cannot receive a new credit.
			inviter, inviterErr := s.store.ReadParticipationWithin(tx, campaign.ID, invitation.InviterCustomerID, true)
			if inviterErr != nil || inviter.State != referraldomain.ParticipationActive {
				return referralport.ErrInvitationInvalid
			}
			if config.TeamMode == referraldomain.TeamModeTeam && inviter.TeamID > 0 {
				team, teamErr := s.store.ReadTeamWithin(tx, inviter.TeamID, false)
				if teamErr != nil || team.CampaignID != campaign.ID {
					return referralport.ErrInvitationInvalid
				}
				teamID, inviterTeamID = inviter.TeamID, inviter.TeamID
			}
			invitationID, inviterCustomerID = invitation.ID, invitation.InviterCustomerID
		} else if config.TeamMode == referraldomain.TeamModeTeam && teamID > 0 {
			team, teamErr := s.store.ReadTeamWithin(tx, teamID, false)
			if teamErr != nil || team.CampaignID != campaign.ID {
				return referralport.ErrConflict
			}
		}
		participation, insertErr := s.store.InsertParticipationWithin(tx, referraldomain.Participation{CampaignID: campaign.ID, CustomerID: command.Actor.CustomerID, TeamID: teamID, InvitationID: invitationID, InviterCustomerID: inviterCustomerID, InviterTeamID: inviterTeamID, State: referraldomain.ParticipationActive, JoinedAt: now})
		if insertErr != nil {
			return insertErr
		}
		result = participation.ID
		if invitationID > 0 {
			current, currentErr := s.store.ReadCurrentRelationshipWithin(tx, command.Actor.CustomerID, true)
			previousReferrer := int64(0)
			if errors.Is(currentErr, referralport.ErrNotFound) {
				current, currentErr = s.store.InsertCurrentRelationshipWithin(tx, referraldomain.Relationship{CustomerID: command.Actor.CustomerID, ReferrerCustomerID: inviterCustomerID, SourceCampaignID: campaign.ID, InvitationID: invitationID, Version: 1, EffectiveAt: now})
			} else if currentErr == nil {
				previousReferrer = current.ReferrerCustomerID
				current.ReferrerCustomerID, current.SourceCampaignID, current.InvitationID, current.Version, current.EffectiveAt = inviterCustomerID, campaign.ID, invitationID, current.Version+1, now
				current, currentErr = s.store.UpdateCurrentRelationshipWithin(tx, current, current.Version-1)
			}
			if currentErr != nil {
				return currentErr
			}
			if err = s.store.InsertRelationshipHistoryWithin(tx, referraldomain.RelationshipHistory{RelationshipID: current.ID, Version: current.Version, CustomerID: command.Actor.CustomerID, PreviousReferrerCustomerID: previousReferrer, ReferrerCustomerID: inviterCustomerID, SourceCampaignID: campaign.ID, InvitationID: invitationID, AcceptedAt: now}); err != nil {
				return err
			}
			if _, err = s.store.InsertScoreEventWithin(tx, referraldomain.ScoreEvent{CampaignID: campaign.ID, ParticipationID: participation.ID, InviterCustomerID: inviterCustomerID, TeamID: inviterTeamID, Kind: referraldomain.ScoreCredit, Delta: 1, OccurredAt: now}); err != nil {
				return err
			}
			if err = s.appendEvent(tx, "referral.relationship.changed", "relationship", current.ID, "customer", command.Actor.CustomerID, command.IdempotencyKey, map[string]any{"relationship_id": current.ID, "version": current.Version, "source_campaign_id": campaign.ID, "invitation_id": invitationID}, now); err != nil {
				return err
			}
		}
		if err = s.store.AppendOperationReceiptWithin(tx, "join", actorScope, command.IdempotencyKey, payload, "participation", participation.ID, now); err != nil {
			return err
		}
		return s.appendEvent(tx, "referral.participation.joined", "participation", participation.ID, "customer", command.Actor.CustomerID, command.IdempotencyKey, map[string]any{"campaign_id": campaign.ID, "participation_id": participation.ID, "invited": invitationID > 0}, now)
	})
	if err != nil {
		return referralport.MyCampaign{}, err
	}
	if result < 1 {
		return referralport.MyCampaign{}, referralport.ErrUnavailable
	}
	return s.MyCampaign(ctx, command.Actor, command.CampaignID)
}

func (s *Service) IssueInvitation(ctx context.Context, command referralport.IssueInvitationCommand) (referralport.InvitationLink, error) {
	if s == nil || s.uow == nil || s.store == nil || !command.Actor.Valid() || command.CampaignID < 1 || !validKey(command.IdempotencyKey) {
		return referralport.InvitationLink{}, referralport.ErrConflict
	}
	actorScope := customerScope(command.Actor.CustomerID)
	token, err := s.tokens.issue(actorScope, command.CampaignID, command.IdempotencyKey)
	if err != nil {
		return referralport.InvitationLink{}, referralport.ErrUnavailable
	}
	digest, payload := sha256.Sum256([]byte(token)), issuePayload(command.CampaignID)
	var expiresAt time.Time
	err = s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "issue_invitation", actorScope, command.IdempotencyKey); err != nil {
			return err
		}
		if receipt, found, readErr := s.store.ReadOperationReceiptWithin(tx, "issue_invitation", actorScope, command.IdempotencyKey); readErr != nil || found {
			if readErr != nil {
				return readErr
			}
			if receipt.PayloadDigest != payload || receipt.ResultKind != "invitation" {
				return referralport.ErrConflict
			}
			invitation, invitationErr := s.store.ReadInvitationByDigestWithin(tx, digest, false)
			if invitationErr != nil || invitation.ID != receipt.ResultID || invitation.CampaignID != command.CampaignID || invitation.InviterCustomerID != command.Actor.CustomerID {
				return referralport.ErrConflict
			}
			expiresAt = invitation.ExpiresAt
			return nil
		}
		campaign, readErr := s.store.ReadCampaignWithin(tx, command.CampaignID, true)
		if readErr != nil {
			return readErr
		}
		now := s.now().UTC()
		if !campaign.AcceptingAt(now) {
			return referralport.ErrCampaignUnavailable
		}
		participation, partErr := s.store.ReadParticipationWithin(tx, campaign.ID, command.Actor.CustomerID, false)
		if partErr != nil {
			if errors.Is(partErr, referralport.ErrNotFound) {
				return referralport.ErrParticipationRequired
			}
			return partErr
		}
		if participation.State != referraldomain.ParticipationActive {
			return referralport.ErrParticipationRequired
		}
		expiresAt = now.Add(invitationTTL)
		if campaign.EndsAt.Before(expiresAt) {
			expiresAt = campaign.EndsAt
		}
		if !expiresAt.After(now) {
			return referralport.ErrCampaignUnavailable
		}
		invitation, insertErr := s.store.InsertInvitationWithin(tx, referraldomain.Invitation{CampaignID: campaign.ID, InviterCustomerID: command.Actor.CustomerID, TokenDigest: digest, State: referraldomain.InvitationActive, CreatedAt: now, ExpiresAt: expiresAt})
		if insertErr != nil {
			return insertErr
		}
		if err = s.store.AppendOperationReceiptWithin(tx, "issue_invitation", actorScope, command.IdempotencyKey, payload, "invitation", invitation.ID, now); err != nil {
			return err
		}
		return s.appendEvent(tx, "referral.invitation.issued", "invitation", invitation.ID, "customer", command.Actor.CustomerID, command.IdempotencyKey, map[string]any{"campaign_id": campaign.ID, "invitation_id": invitation.ID, "expires_at": expiresAt}, now)
	})
	if err != nil {
		return referralport.InvitationLink{}, err
	}
	return referralport.InvitationLink{URL: s.origin + "/referral/invite/" + token, ExpiresAt: expiresAt}, nil
}

func (s *Service) MyCampaign(ctx context.Context, actor referralport.TrustedSessionActor, campaignID int64) (referralport.MyCampaign, error) {
	if s == nil || !actor.Valid() || campaignID < 1 {
		return referralport.MyCampaign{}, referralport.ErrUnauthorized
	}
	var campaign referraldomain.Campaign
	var participation referraldomain.Participation
	var relationship referraldomain.Relationship
	var captainTeam referraldomain.Team
	var hasParticipation, hasRelationship, assignedCaptain bool
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		campaign, err = s.store.ReadCampaignWithin(tx, campaignID, false)
		if err != nil {
			return err
		}
		participation, err = s.store.ReadParticipationWithin(tx, campaignID, actor.CustomerID, false)
		if err == nil {
			hasParticipation = true
		} else if !errors.Is(err, referralport.ErrNotFound) {
			return err
		}
		if campaign.Config().TeamMode == referraldomain.TeamModeTeam {
			captainTeam, assignedCaptain, err = s.store.ReadCaptainTeamWithin(tx, campaignID, actor.CustomerID)
			if err != nil {
				return err
			}
		}
		relationship, err = s.store.ReadCurrentRelationshipWithin(tx, actor.CustomerID, false)
		if err == nil {
			hasRelationship = true
		} else if !errors.Is(err, referralport.ErrNotFound) {
			return err
		}
		return nil
	})
	if err != nil {
		return referralport.MyCampaign{}, err
	}
	summary, err := s.campaignSummary(ctx, campaign)
	if err != nil {
		return referralport.MyCampaign{}, err
	}
	result := referralport.MyCampaign{Campaign: summary, IsCaptain: assignedCaptain}
	if assignedCaptain {
		result.CaptainTeam = &captainTeam
	}
	if hasParticipation {
		if participation.TeamID > 0 {
			team, teamErr := s.readTeam(ctx, participation.TeamID)
			if teamErr != nil {
				return referralport.MyCampaign{}, teamErr
			}
			result.Participation, result.Team = &participation, &team
		} else {
			result.Participation = &participation
		}
		result.InvitationAvailable = participation.State == referraldomain.ParticipationActive && campaign.AcceptingAt(s.now().UTC())
		var countErr error
		err = s.uow.Within(ctx, func(tx context.Context) error {
			result.DirectInvitationCount, countErr = s.store.CountDirectInvitationsWithin(tx, campaignID, actor.CustomerID)
			return countErr
		})
		if err != nil {
			return referralport.MyCampaign{}, err
		}
		page, rankErr := s.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: campaignID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, ViewerCustomerID: actor.CustomerID, Limit: 1})
		if rankErr != nil {
			return referralport.MyCampaign{}, rankErr
		}
		if page.MyEntry != nil {
			result.PersonalTotalScore, result.PersonalRank = page.MyEntry.Score, page.MyEntry.Rank
		}
		if campaign.Config().TeamMode == referraldomain.TeamModeTeam && participation.TeamID > 0 {
			teamPage, teamErr := s.Leaderboard(ctx, referralport.LeaderboardQuery{CampaignID: campaignID, Kind: referralport.LeaderboardTeam, Period: referralport.LeaderboardAll, Limit: 1, ViewerTeamID: participation.TeamID})
			if teamErr != nil {
				return referralport.MyCampaign{}, teamErr
			}
			if teamPage.MyEntry != nil {
				result.TeamTotalScore, result.TeamRank = teamPage.MyEntry.Score, teamPage.MyEntry.Rank
			}
		}
	}
	if hasRelationship {
		result.CurrentRelationship = &relationship
	}
	return result, nil
}

func (s *Service) ListMyInvites(ctx context.Context, actor referralport.TrustedSessionActor, campaignID int64, cursor string, limit int32) (referralport.InvitePage, error) {
	if s == nil || !actor.Valid() || campaignID < 1 || limit < 1 || limit > 100 {
		return referralport.InvitePage{}, referralport.ErrConflict
	}
	offset, err := referralstore.OffsetCursor(cursor)
	if err != nil {
		return referralport.InvitePage{}, err
	}
	var values []referralport.InviteItem
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		// The actor has already been authenticated by the shared verified
		// browser session. A missing participation means this customer has no
		// invitation details yet; it is not a failed login. Read the campaign
		// first so an unknown campaign remains a real not-found result, and do
		// not hide store failures behind an authorization error.
		if _, err = s.store.ReadCampaignWithin(tx, campaignID, false); err != nil {
			return err
		}
		if _, err = s.store.ReadParticipationWithin(tx, campaignID, actor.CustomerID, false); err != nil {
			if errors.Is(err, referralport.ErrNotFound) {
				return nil
			}
			return err
		}
		values, err = s.store.ListInviteItemsWithin(tx, campaignID, actor.CustomerID, offset, limit+1)
		return err
	})
	if err != nil {
		return referralport.InvitePage{}, err
	}
	page := referralport.InvitePage{Items: values}
	if int32(len(values)) > limit {
		page.Items = values[:limit]
		page.NextCursor = referralstore.NextOffsetCursor(offset, int(limit), limit)
	}
	return page, nil
}

func (s *Service) Leaderboard(ctx context.Context, query referralport.LeaderboardQuery) (referralport.LeaderboardPage, error) {
	query.Period = referralport.NormalizeLeaderboardPeriod(query.Period)
	if s == nil || query.CampaignID < 1 || !query.Kind.Valid() || !query.Period.Valid() || query.Limit < 1 || query.Limit > 100 {
		return referralport.LeaderboardPage{}, referralport.ErrConflict
	}
	offset, err := referralstore.OffsetCursor(query.Cursor)
	if err != nil {
		return referralport.LeaderboardPage{}, err
	}
	var campaign referraldomain.Campaign
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		campaign, err = s.store.ReadCampaignWithin(tx, query.CampaignID, false)
		return err
	})
	if err != nil {
		return referralport.LeaderboardPage{}, err
	}
	if campaign.Config().TeamMode == referraldomain.TeamModeIndividual && query.Kind != referralport.LeaderboardPersonal {
		return referralport.LeaderboardPage{}, referralport.ErrConflict
	}
	start, end, err := leaderboardWindow(campaign, query.Period, query.Anchor, s.now().UTC())
	if err != nil {
		return referralport.LeaderboardPage{}, err
	}
	var items []referralport.LeaderboardEntry
	var mine *referralport.LeaderboardEntry
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		if campaign.Config().LeaderboardMetric != referraldomain.LeaderboardInvites {
			items, mine, err = s.store.ListSalesLeaderboardRowsWithin(tx, query.CampaignID, query.Kind, campaign.Config().LeaderboardMetric, query.TeamID, start, end, offset, query.Limit+1, query.ViewerCustomerID, query.ViewerTeamID)
		} else {
			items, mine, err = s.store.LeaderboardRowsWithin(tx, query.CampaignID, query.Kind, query.TeamID, start, end, offset, query.Limit+1, query.ViewerCustomerID, query.ViewerTeamID)
		}
		return err
	})
	if err != nil {
		return referralport.LeaderboardPage{}, err
	}
	page := referralport.LeaderboardPage{Kind: query.Kind, Period: query.Period, WindowStart: start, WindowEnd: end, Items: items, MyEntry: mine}
	if int32(len(items)) > query.Limit {
		page.Items = items[:query.Limit]
		page.NextCursor = referralstore.NextOffsetCursor(offset, int(query.Limit), query.Limit)
	}
	return page, nil
}

func (s *Service) CurrentRelationship(ctx context.Context, customerID int64) (referralport.RelationshipSnapshot, bool, error) {
	return s.RelationshipAt(ctx, customerID, s.now().UTC())
}

func (s *Service) RelationshipAt(ctx context.Context, customerID int64, at time.Time) (referralport.RelationshipSnapshot, bool, error) {
	if s == nil || customerID < 1 || at.IsZero() {
		return referralport.RelationshipSnapshot{}, false, referralport.ErrConflict
	}
	var relation referraldomain.Relationship
	var found bool
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		relation, found, err = s.store.CurrentRelationshipAtWithin(tx, customerID, at)
		return err
	})
	if err != nil {
		return referralport.RelationshipSnapshot{}, false, err
	}
	if !found {
		return referralport.RelationshipSnapshot{}, false, nil
	}
	return relationshipSnapshot(relation), true, nil
}

func (s *Service) campaignSummary(ctx context.Context, campaign referraldomain.Campaign) (referralport.CampaignSummary, error) {
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

func (s *Service) campaignView(ctx context.Context, campaign referraldomain.Campaign) (referralport.CampaignView, error) {
	summary, err := s.campaignSummary(ctx, campaign)
	if err != nil {
		return referralport.CampaignView{}, err
	}
	var teams []referraldomain.Team
	var daily []referralport.CampaignDailyMetric
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		if campaign.Config().TeamMode == referraldomain.TeamModeTeam {
			teams, err = s.store.ListTeamsWithin(tx, campaign.ID)
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
	return referralport.CampaignView{CampaignSummary: summary, Teams: teams, DailyMetrics: daily}, nil
}

func (s *Service) readTeam(ctx context.Context, teamID int64) (referraldomain.Team, error) {
	var team referraldomain.Team
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		team, err = s.store.ReadTeamWithin(tx, teamID, false)
		return err
	})
	return team, err
}

func (s *Service) appendEvent(ctx context.Context, eventType, resourceType string, resourceID int64, actorType string, actorID int64, commandKey string, payload any, at time.Time) error {
	if s.audit == nil || s.outbox == nil || resourceID < 1 || actorID < 0 || at.IsZero() {
		return referralport.ErrUnavailable
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return referralport.ErrUnavailable
	}
	digest := sha256.Sum256([]byte(eventType + ":" + resourceType + ":" + strconv.FormatInt(resourceID, 10) + ":" + commandKey))
	key := "referral:" + hex.EncodeToString(digest[:])
	idempotencyKey, err := platformidempotency.Parse(key)
	if err != nil {
		return referralport.ErrUnavailable
	}
	if _, err = s.audit.Append(ctx, platformaudit.Event{IdempotencyKey: idempotencyKey, Action: eventType, ActorType: actorType, ActorID: strconv.FormatInt(actorID, 10), ResourceType: resourceType, ResourceID: strconv.FormatInt(resourceID, 10), Payload: encoded, OccurredAt: at.UTC()}); err != nil {
		return err
	}
	_, err = s.outbox.Append(ctx, platformoutbox.Event{AggregateType: "referral_" + resourceType, AggregateID: strconv.FormatInt(resourceID, 10), Type: eventType, Version: 1, IdempotencyKey: key, Payload: encoded, OccurredAt: at.UTC()})
	return err
}

func customerScope(customerID int64) string { return "customer:" + strconv.FormatInt(customerID, 10) }
func adminScope(adminID int64) string       { return "admin:" + strconv.FormatInt(adminID, 10) }
func validKey(value string) bool            { _, err := platformidempotency.Parse(value); return err == nil }
func validOrigin(raw string) bool {
	value, err := url.Parse(raw)
	return err == nil && value.Host != "" && (value.Scheme == "https" || value.Scheme == "http") && value.RawQuery == "" && value.Fragment == ""
}
func joinPayload(command referralport.JoinCampaignCommand) [sha256.Size]byte {
	return sha256.Sum256([]byte("referral.join.v1:campaign=" + strconv.FormatInt(command.CampaignID, 10) + "&invitation=" + command.InvitationToken + "&team=" + strconv.FormatInt(command.TeamID, 10)))
}
func issuePayload(campaignID int64) [sha256.Size]byte {
	return sha256.Sum256([]byte("referral.invitation.v1:campaign=" + strconv.FormatInt(campaignID, 10)))
}
func relationshipSnapshot(value referraldomain.Relationship) referralport.RelationshipSnapshot {
	return referralport.RelationshipSnapshot{RelationshipID: value.ID, Version: value.Version, CustomerID: value.CustomerID, ReferrerCustomerID: value.ReferrerCustomerID, SourceCampaignID: value.SourceCampaignID, InvitationID: value.InvitationID, EffectiveAt: value.EffectiveAt}
}

func leaderboardWindow(campaign referraldomain.Campaign, period referralport.LeaderboardPeriod, anchor, now time.Time) (time.Time, time.Time, error) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.Time{}, time.Time{}, referralport.ErrUnavailable
	}
	start, end := campaign.StartsAt.UTC(), campaign.EndsAt.UTC()
	switch period {
	case referralport.LeaderboardTotal:
	case referralport.LeaderboardDay:
		if anchor.IsZero() {
			anchor = now
		}
		local := anchor.In(loc)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		periodStart := day.UTC()
		periodEnd := day.AddDate(0, 0, 1).UTC()
		if periodStart.After(start) {
			start = periodStart
		}
		if periodEnd.Before(end) {
			end = periodEnd
		}
	case referralport.LeaderboardWeek:
		if anchor.IsZero() {
			anchor = now
		}
		local := anchor.In(loc)
		midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		offset := (int(midnight.Weekday()) + 6) % 7
		weekStartLocal := midnight.AddDate(0, 0, -offset)
		weekStart := weekStartLocal.UTC()
		weekEnd := weekStartLocal.AddDate(0, 0, 7).UTC()
		if weekStart.After(start) {
			start = weekStart
		}
		if weekEnd.Before(end) {
			end = weekEnd
		}
	default:
		return time.Time{}, time.Time{}, referralport.ErrConflict
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, referralport.ErrNotFound
	}
	return start, end, nil
}
