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
	"slices"
	"strconv"
	"strings"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
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
	ReadInviteStreamWithin(context.Context, referralstore.InviteStreamQuery) (referralstore.InviteStreamPage, error)
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
	CountPaidInviteesWithin(context.Context, int64, int64) (int64, error)
	ListInviteItemsWithin(context.Context, int64, int64, int32, int32) ([]referralport.InviteItem, error)
	ListPaidInviteItemsWithin(context.Context, int64, int64, int32, int32) ([]referralport.InviteItem, error)
	LeaderboardRowsWithin(context.Context, int64, referralport.LeaderboardKind, int64, time.Time, time.Time, int32, int32, int64, int64) ([]referralport.LeaderboardEntry, *referralport.LeaderboardEntry, error)
	ListSalesLeaderboardRowsWithin(context.Context, int64, referralport.LeaderboardKind, referraldomain.LeaderboardMetric, int64, time.Time, time.Time, int32, int32, int64, int64) ([]referralport.LeaderboardEntry, *referralport.LeaderboardEntry, error)
	InsertProductActivityContextWithin(context.Context, referralport.ProductActivityContext) error
	ReadProductActivityContextByDigestWithin(context.Context, [sha256.Size]byte) (referralport.ProductActivityContext, error)
}

// IssueProductActivityContext mints an opaque, same-origin checkout context.
// It is deliberately separate from invitation credentials and contains no
// customer supplied identity or redirect URL.
func (s *Service) IssueProductActivityContext(ctx context.Context, actor referralport.TrustedSessionActor, campaignID int64, idempotencyKey string) (referralport.ProductActivityContextCredential, error) {
	if s == nil || !actor.Valid() || campaignID < 1 || !validKey(idempotencyKey) {
		return referralport.ProductActivityContextCredential{}, referralport.ErrConflict
	}
	payload := productActivityContextPayload(campaignID)
	var credential referralport.ProductActivityContextCredential
	err := s.uow.Within(ctx, func(tx context.Context) error {
		lineage, err := s.actorLineageWithin(tx, actor, true)
		if err != nil {
			return err
		}
		receipt, actorScope, found, err := s.actorReceiptWithin(tx, "issue_product_activity_context", lineage, idempotencyKey, payload)
		if err != nil {
			return err
		}
		token, err := s.activityContextTokens.issue(actorScope, campaignID, idempotencyKey)
		if err != nil {
			return referralport.ErrUnavailable
		}
		digest := sha256.Sum256([]byte(token))
		if found {
			if receipt.ResultKind != "campaign" || receipt.ResultID != campaignID {
				return referralport.ErrConflict
			}
			stored, readErr := s.store.ReadProductActivityContextByDigestWithin(tx, digest)
			if readErr != nil {
				return readErr
			}
			if stored.CampaignID != campaignID || stored.State != "active" || stored.ContextDigest != digest {
				return referralport.ErrUnavailable
			}
			credential = referralport.ProductActivityContextCredential{Token: token, ExpiresAt: stored.ExpiresAt}
			return nil
		}
		campaign, err := s.store.ReadCampaignWithin(tx, campaignID, true)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if campaign.Config().QualificationMode != referraldomain.QualificationProductPurchase || !campaign.AcceptingAt(now) {
			return referralport.ErrCampaignUnavailable
		}
		stored := referralport.ProductActivityContext{ContextDigest: digest, CampaignID: campaign.ID, ProductID: campaign.ProductID, ProductType: campaign.ProductType, SalesMetric: referralport.SalesMetricAmount, State: "active", ExpiresAt: campaign.EndsAt, CreatedAt: now}
		if err := s.store.InsertProductActivityContextWithin(tx, stored); err != nil {
			return err
		}
		// The receipt references its owning campaign; the operation identifies
		// context issuance and its deterministic token addresses the snapshot.
		if err := s.store.AppendOperationReceiptWithin(tx, "issue_product_activity_context", actorScope, idempotencyKey, payload, "campaign", campaign.ID, now); err != nil {
			return err
		}
		credential = referralport.ProductActivityContextCredential{Token: token, ExpiresAt: stored.ExpiresAt}
		return nil
	})
	return credential, err
}

// IssuePublicProductActivityContext is used only by the signed, campaign-bound
// promotion handoff. It conveys no customer or promoter identity; Order still
// freezes both from its own trusted checkout and Distribution credential.
func (s *Service) IssuePublicProductActivityContext(ctx context.Context, campaignID int64) (referralport.ProductActivityContextCredential, error) {
	if s == nil || campaignID < 1 {
		return referralport.ProductActivityContextCredential{}, referralport.ErrConflict
	}
	return s.issueProductActivityContext(ctx, campaignID)
}

func (s *Service) issueProductActivityContext(ctx context.Context, campaignID int64) (referralport.ProductActivityContextCredential, error) {
	var credential referralport.ProductActivityContextCredential
	err := s.uow.Within(ctx, func(tx context.Context) error {
		campaign, err := s.store.ReadCampaignWithin(tx, campaignID, true)
		if err != nil {
			return err
		}
		if campaign.Config().QualificationMode != referraldomain.QualificationProductPurchase || !campaign.AcceptingAt(s.now().UTC()) {
			return referralport.ErrCampaignUnavailable
		}
		buf := make([]byte, 32)
		if _, err := cryptoRandRead(buf); err != nil {
			return referralport.ErrUnavailable
		}
		token := "rpa_" + base64.RawURLEncoding.EncodeToString(buf)
		digest := sha256.Sum256([]byte(token))
		stored := referralport.ProductActivityContext{ContextDigest: digest, CampaignID: campaign.ID, ProductID: campaign.ProductID, ProductType: campaign.ProductType, SalesMetric: referralport.SalesMetricAmount, State: "active", ExpiresAt: campaign.EndsAt, CreatedAt: s.now().UTC()}
		if err := s.store.InsertProductActivityContextWithin(tx, stored); err != nil {
			return err
		}
		credential = referralport.ProductActivityContextCredential{Token: token, ExpiresAt: stored.ExpiresAt}
		return nil
	})
	return credential, err
}

type Service struct {
	uow                   platformport.UnitOfWork
	store                 serviceStore
	origin                string
	tokens                invitationTokenIssuer
	activityContextTokens productActivityContextTokenIssuer
	closeJobs             CampaignCloseEnqueuer
	audit                 *platformaudit.Service
	outbox                platformoutbox.Appender
	qualification         referralport.PurchaseQualificationReader
	attributions          distributionport.FrozenOrderAttributionReader
	timeline              platformaudit.TimelineReader
	subjects              trustedReferralSubjects
	reportingUow          platformport.UnitOfWork
	identityReportingUow  platformport.UnitOfWork
	now                   func() time.Time
}

type trustedReferralSubjects interface {
	identityport.VerifiedIdentitySubjectReader
	identityport.WriteLockedVerifiedIdentitySubjectReader
}

func (s *Service) BindIdentitySubjects(reader trustedReferralSubjects) error {
	if s == nil || reader == nil {
		return referralport.ErrUnavailable
	}
	s.subjects = reader
	if roots, ok := reader.(identityport.CanonicalCustomerRootsReader); ok {
		if binder, ok := s.store.(interface {
			BindCanonicalCustomerRoots(identityport.CanonicalCustomerRootsReader) error
		}); ok {
			if err := binder.BindCanonicalCustomerRoots(roots); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) BindReportingUnitOfWork(uow platformport.UnitOfWork) error {
	if s == nil || uow == nil || s.reportingUow != nil {
		return referralport.ErrUnavailable
	}
	s.reportingUow = uow
	return nil
}

func (s *Service) reportingUnitOfWork() platformport.UnitOfWork {
	if s.reportingUow != nil {
		return s.reportingUow
	}
	return s.uow
}

// VerifiedIdentitySubject needs FOR SHARE. Its reporting snapshot therefore
// uses a writable Repeatable Read transaction, although this path only reads.
func (s *Service) BindIdentityReportingUnitOfWork(uow platformport.UnitOfWork) error {
	if s == nil || uow == nil || s.identityReportingUow != nil {
		return referralport.ErrUnavailable
	}
	s.identityReportingUow = uow
	return nil
}

func (s *Service) identityReportingUnitOfWork() platformport.UnitOfWork {
	if s.identityReportingUow != nil {
		return s.identityReportingUow
	}
	return s.uow
}

// The immutable verified handle, rather than a session's historical customer
// ID, identifies the current subject. Identity owns merge and reversal rules.
func (s *Service) actorLineageWithin(ctx context.Context, actor referralport.TrustedSessionActor, locked bool) ([]int64, error) {
	if s.subjects == nil {
		return []int64{actor.CustomerID}, nil
	}
	var subject identityport.VerifiedIdentitySubject
	var found bool
	var err error
	if locked {
		subject, found, err = s.subjects.WriteLockedVerifiedIdentitySubject(ctx, actor.IdentityID)
	} else {
		subject, found, err = s.subjects.VerifiedIdentitySubject(ctx, actor.IdentityID)
	}
	if err != nil {
		return nil, referralport.ErrUnavailable
	}
	if !found || subject.CustomerID < 1 || len(subject.Lineage) == 0 || subject.Lineage[0] != subject.CustomerID {
		return nil, referralport.ErrUnauthorized
	}
	lineage := make([]int64, len(subject.Lineage))
	for index, id := range subject.Lineage {
		if id < 1 || slices.Contains(lineage[:index], int64(id)) {
			return nil, referralport.ErrUnavailable
		}
		lineage[index] = int64(id)
	}
	return lineage, nil
}

// Existing actor-scoped receipts remain at their original historical scope.
// A holder may replay only receipts owned by its currently locked lineage.
func (s *Service) actorReceiptWithin(ctx context.Context, operation string, lineage []int64, key string, payload [sha256.Size]byte) (referralstore.OperationReceipt, string, bool, error) {
	ordered := slices.Clone(lineage)
	slices.Sort(ordered)
	for _, customerID := range ordered {
		if err := s.store.LockOperationReceiptWithin(ctx, operation, customerScope(customerID), key); err != nil {
			return referralstore.OperationReceipt{}, "", false, err
		}
	}
	var result referralstore.OperationReceipt
	scope := customerScope(lineage[0])
	found := false
	for _, customerID := range lineage {
		candidateScope := customerScope(customerID)
		receipt, exists, err := s.store.ReadOperationReceiptWithin(ctx, operation, candidateScope, key)
		if err != nil {
			return result, scope, false, err
		}
		if !exists {
			continue
		}
		if receipt.PayloadDigest != payload || found && (receipt.ResultKind != result.ResultKind || receipt.ResultID != result.ResultID) {
			return result, scope, false, referralport.ErrConflict
		}
		result, scope, found = receipt, candidateScope, true
	}
	return result, scope, found, nil
}

func (s *Service) ownParticipationWithin(ctx context.Context, campaignID int64, lineage []int64, locked bool) (referraldomain.Participation, error) {
	var result referraldomain.Participation
	for _, customerID := range lineage {
		participation, err := s.store.ReadParticipationWithin(ctx, campaignID, customerID, locked)
		if errors.Is(err, referralport.ErrNotFound) {
			continue
		}
		if err != nil {
			return result, err
		}
		if result.ID > 0 && result.ID != participation.ID {
			// Merging pre-existing participants does not erase or combine facts.
			return result, referralport.ErrConflict
		}
		result = participation
	}
	if result.ID == 0 {
		return result, referralport.ErrNotFound
	}
	return result, nil
}

func NewService(uow platformport.UnitOfWork, store serviceStore, origin, tokenDataKey string, closeJobs CampaignCloseEnqueuer, audit *platformaudit.Service, outbox platformoutbox.Appender) (*Service, error) {
	if uow == nil || store == nil || closeJobs == nil || audit == nil || outbox == nil || !validOrigin(origin) {
		return nil, referralport.ErrUnavailable
	}
	tokens, err := newInvitationTokenIssuer(tokenDataKey)
	if err != nil {
		return nil, referralport.ErrUnavailable
	}
	activityContextTokens, err := newProductActivityContextTokenIssuer(tokenDataKey)
	if err != nil {
		return nil, referralport.ErrUnavailable
	}
	return &Service{uow: uow, store: store, origin: strings.TrimRight(origin, "/"), tokens: tokens, activityContextTokens: activityContextTokens, closeJobs: closeJobs, audit: audit, outbox: outbox, now: time.Now}, nil
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
	payload := joinPayload(command)
	result := int64(0)
	err := s.uow.Within(ctx, func(tx context.Context) error {
		lineage, err := s.actorLineageWithin(tx, command.Actor, true)
		if err != nil {
			return err
		}
		command.Actor.CustomerID = lineage[0]
		receipt, actorScope, found, err := s.actorReceiptWithin(tx, "join", lineage, command.IdempotencyKey, payload)
		if err != nil {
			return err
		}
		if found {
			participation, readErr := s.ownParticipationWithin(tx, command.CampaignID, lineage, true)
			if readErr != nil {
				return readErr
			}
			if receipt.ResultKind != "participation" || receipt.ResultID != participation.ID {
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
		locks := slices.Clone(lineage)
		if invitationFound && !slices.Contains(locks, invitation.InviterCustomerID) {
			locks = append(locks, invitation.InviterCustomerID)
		}
		slices.Sort(locks)
		for _, customerID := range locks {
			if err = s.store.LockParticipationWithin(tx, command.CampaignID, customerID); err != nil {
				return err
			}
		}
		if invitationFound && slices.Contains(lineage, invitation.InviterCustomerID) {
			return referralport.ErrInvitationInvalid
		}
		if existing, readErr := s.ownParticipationWithin(tx, command.CampaignID, lineage, true); readErr == nil {
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
		}
		if config.TeamMode == referraldomain.TeamModeTeam && teamID > 0 {
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
	payload := issuePayload(command.CampaignID)
	var token string
	var expiresAt time.Time
	var canonicalCustomerID int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		lineage, err := s.actorLineageWithin(tx, command.Actor, true)
		if err != nil {
			return err
		}
		canonicalCustomerID = lineage[0]
		receipt, actorScope, found, err := s.actorReceiptWithin(tx, "issue_invitation", lineage, command.IdempotencyKey, payload)
		if err != nil {
			return err
		}
		token, err = s.tokens.issue(actorScope, command.CampaignID, command.IdempotencyKey)
		if err != nil {
			return referralport.ErrUnavailable
		}
		digest := sha256.Sum256([]byte(token))
		if found {
			participation, partErr := s.ownParticipationWithin(tx, command.CampaignID, lineage, true)
			if partErr != nil {
				return partErr
			}
			if receipt.ResultKind != "invitation" {
				return referralport.ErrConflict
			}
			invitation, invitationErr := s.store.ReadInvitationByDigestWithin(tx, digest, false)
			if invitationErr != nil || invitation.ID != receipt.ResultID || invitation.CampaignID != command.CampaignID || invitation.InviterCustomerID != participation.CustomerID {
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
		participation, partErr := s.ownParticipationWithin(tx, campaign.ID, lineage, true)
		if errors.Is(partErr, referralport.ErrNotFound) {
			return referralport.ErrParticipationRequired
		}
		if partErr != nil {
			return partErr
		}
		command.Actor.CustomerID = participation.CustomerID
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
		fact := referraldomain.Invitation{CampaignID: campaign.ID, InviterCustomerID: command.Actor.CustomerID, TokenDigest: digest, State: referraldomain.InvitationActive, CreatedAt: now, ExpiresAt: expiresAt}
		if campaign.Config().QualificationMode == referraldomain.QualificationProductPurchase && s.subjects != nil {
			fact.IssuerIdentityID, fact.IssuerAppID, fact.IssuerAppScope, fact.IssuerChannel = command.Actor.IdentityID, command.Actor.AppID, command.Actor.AppScope, command.Actor.Channel
		}
		invitation, insertErr := s.store.InsertInvitationWithin(tx, fact)
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
	return referralport.InvitationLink{URL: s.origin + "/referral/invite/" + token, ExpiresAt: expiresAt, InviterCanonicalCustomerID: canonicalCustomerID}, nil
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
	err := s.identityReportingUnitOfWork().Within(ctx, func(tx context.Context) error {
		lineage, err := s.actorLineageWithin(tx, actor, false)
		if err != nil {
			return err
		}
		actor.CustomerID = lineage[0]
		campaign, err = s.store.ReadCampaignWithin(tx, campaignID, false)
		if err != nil {
			return err
		}
		participation, err = s.ownParticipationWithin(tx, campaignID, lineage, false)
		if err == nil {
			hasParticipation = true
			actor.CustomerID = participation.CustomerID
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
		err = s.reportingUnitOfWork().Within(ctx, func(tx context.Context) error {
			if campaign.Config().QualificationMode == referraldomain.QualificationProductPurchase {
				result.DirectInvitationCount, countErr = s.store.CountPaidInviteesWithin(tx, campaignID, actor.CustomerID)
			} else {
				result.DirectInvitationCount, countErr = s.store.CountDirectInvitationsWithin(tx, campaignID, actor.CustomerID)
			}
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
	position, err := s.readInviteStreamCursor(cursor, actor.CustomerID, campaignID)
	if err != nil {
		return referralport.InvitePage{}, err
	}
	result := referralport.InvitePage{Items: []referralport.InviteItem{}}
	err = s.identityReportingUnitOfWork().Within(ctx, func(tx context.Context) error {
		lineage, err := s.actorLineageWithin(tx, actor, false)
		if err != nil {
			return err
		}
		campaign, err := s.store.ReadCampaignWithin(tx, campaignID, false)
		if err != nil {
			return err
		}
		participation, err := s.ownParticipationWithin(tx, campaignID, lineage, false)
		if err != nil {
			if errors.Is(err, referralport.ErrNotFound) {
				return nil
			}
			return err
		}
		query := referralstore.InviteStreamQuery{CampaignID: campaignID, InviterID: participation.CustomerID, Paid: campaign.Config().QualificationMode == referraldomain.QualificationProductPurchase, Limit: limit + 1}
		if position != nil {
			query.Watermark = position.Watermark
			query.After = &referralstore.InviteStreamPosition{At: position.At, ID: position.ID}
		}
		stream, err := s.store.ReadInviteStreamWithin(tx, query)
		if err != nil {
			return err
		}
		result.Items = stream.Items
		result.TotalCount = stream.TotalCount
		if position != nil {
			result.TotalCount = position.Total
		}
		if int32(len(result.Items)) > limit {
			result.Items = result.Items[:limit]
			last := result.Items[len(result.Items)-1]
			result.NextCursor, err = s.writeInviteStreamCursor(inviteStreamCursor{Watermark: stream.Watermark, Total: result.TotalCount, At: last.Participation.JoinedAt, ID: last.EntryID}, actor.CustomerID, campaignID)
			if err != nil {
				return err
			}
		}
		for index := range result.Items {
			result.Items[index].EntryKey = s.inviteEntryKey(actor.CustomerID, campaignID, result.Items[index].EntryID)
		}
		return nil
	})
	if err != nil {
		return referralport.InvitePage{}, err
	}
	return result, nil
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
	err = s.reportingUnitOfWork().Within(ctx, func(tx context.Context) error {
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
	err = s.reportingUnitOfWork().Within(ctx, func(tx context.Context) error {
		var err error
		if campaign.Config().LeaderboardMetric != referraldomain.LeaderboardInvites || campaign.Config().QualificationMode == referraldomain.QualificationProductPurchase {
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
func productActivityContextPayload(campaignID int64) [sha256.Size]byte {
	return sha256.Sum256([]byte("referral.product_activity_context.v1:campaign=" + strconv.FormatInt(campaignID, 10)))
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
