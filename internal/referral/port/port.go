// Package port exposes Referral's stable cross-domain and delivery contracts.
// Referral accepts only a verified Distribution browser-session actor; it does
// not resolve, create, or merge customer identities itself.
package port

import (
	"context"
	"errors"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
)

var (
	ErrNotFound             = errors.New("referral record not found")
	ErrCampaignConfigLocked = errors.New("referral campaign configuration locked")
	ErrInvalidRequest       = errors.New("invalid referral request")
	ErrConflict             = errors.New("referral command conflict")
	ErrUnavailable          = errors.New("referral unavailable")
	ErrUnauthorized         = errors.New("referral unauthorized")
	// ErrParticipationRequired means a trusted customer has not yet confirmed
	// this campaign (or their participation is no longer active). It is a
	// business prerequisite for issuing an invitation, never an authentication
	// failure.
	ErrParticipationRequired = errors.New("referral participation required")
	ErrInvitationInvalid     = errors.New("referral invitation invalid")
	ErrCampaignUnavailable   = errors.New("referral campaign unavailable")
	// CreateTeam reports safe, actionable conflicts without exposing storage
	// constraint names or canonical-customer facts to delivery adapters.
	ErrCampaignTeamLocked     = errors.New("referral campaign team configuration is locked")
	ErrCaptainIneligible      = errors.New("referral captain is ineligible")
	ErrTeamNameExists         = errors.New("referral team name already exists")
	ErrCaptainAlreadyAssigned = errors.New("referral captain is already assigned")
	ErrIdempotencyConflict    = errors.New("referral idempotency key conflicts with a different command")
)

// TrustedSessionActor is an alias, not a second session or identity model.
// HTTP derives it only through the existing verified WeChat/Payment bridge.
type TrustedSessionActor = distributionport.TrustedSessionActor

type LeaderboardKind string

const (
	LeaderboardPersonal LeaderboardKind = "personal"
	LeaderboardTeam     LeaderboardKind = "team"
	LeaderboardInTeam   LeaderboardKind = "in_team"
)

func (k LeaderboardKind) Valid() bool {
	return k == LeaderboardPersonal || k == LeaderboardTeam || k == LeaderboardInTeam
}

type LeaderboardPeriod string

const (
	// LeaderboardAll is the canonical all-time period. LeaderboardTotal is a
	// source-compatible Go alias; HTTP normalizes the legacy "total" input.
	LeaderboardAll   LeaderboardPeriod = "all"
	LeaderboardTotal LeaderboardPeriod = LeaderboardAll
	LeaderboardWeek  LeaderboardPeriod = "week"
	LeaderboardDay   LeaderboardPeriod = "day"
)

func (p LeaderboardPeriod) Valid() bool {
	return p == LeaderboardAll || p == LeaderboardWeek || p == LeaderboardDay
}

func NormalizeLeaderboardPeriod(p LeaderboardPeriod) LeaderboardPeriod {
	if p == LeaderboardPeriod("total") {
		return LeaderboardAll
	}
	return p
}

type CampaignSummary struct {
	Campaign         domain.Campaign
	EffectiveState   domain.CampaignState
	ParticipantCount int64
	InvitationCount  int64
	TeamCount        int64
}

type CampaignView struct {
	CampaignSummary
	// Teams remains the public team list. TeamSummaries is populated only by
	// admin reads and contains activity aggregates for the operations dashboard.
	Teams         []domain.Team
	TeamSummaries []AdminTeamSummary
	DailyMetrics  []CampaignDailyMetric
}

type AdminTeamSummary struct {
	Team                  domain.Team
	ParticipantCount      int64
	DirectInvitationCount int64
	// CaptainParticipated is false until the designated captain explicitly
	// confirms activity participation. A captain assignment alone is not a
	// participant and never contributes to team totals.
	CaptainParticipated bool
}

// CampaignDailyMetric is a server-time (Asia/Shanghai) activity projection.
// It contains aggregate counts only and never exposes customer identifiers.
type CampaignDailyMetric struct {
	Date                          time.Time
	ParticipantCount, InviteCount int64
}

type InvitationPreview struct {
	Campaign          CampaignSummary
	InviterCustomerID int64
	InviterTeam       domain.Team
	ExpiresAt         time.Time
}

type InvitationLink struct {
	URL       string
	ExpiresAt time.Time
}

type MyCampaign struct {
	Campaign              CampaignSummary
	Participation         *domain.Participation
	Team                  *domain.Team
	CurrentRelationship   *domain.Relationship
	DirectInvitationCount int64
	PersonalTotalScore    int64
	TeamTotalScore        int64
	PersonalRank          int64
	TeamRank              int64
	InvitationAvailable   bool
	// CaptainTeam is derived from the activity configuration and is present
	// even before the captain participates. It lets the member UI direct that
	// customer to join their designated team before issuing invitations.
	CaptainTeam *domain.Team
	IsCaptain   bool
}

type InviteItem struct {
	Participation domain.Participation
	ScoreState    string
	ScoreDelta    int64
}

type InvitePage struct {
	Items      []InviteItem
	NextCursor string
}

type LeaderboardQuery struct {
	CampaignID int64
	Kind       LeaderboardKind
	Period     LeaderboardPeriod
	// Anchor is interpreted in Asia/Shanghai.  For total it is ignored; for
	// day it selects that calendar day, and for week it selects its Monday.
	Anchor           time.Time
	ViewerCustomerID int64
	// ViewerTeamID is server-derived for the pinned own-team row. Public HTTP
	// must not let a browser choose another customer's team as its own.
	ViewerTeamID int64
	TeamID       int64 // required only for in_team
	Limit        int32
	Cursor       string
}

type LeaderboardEntry struct {
	Rank, Score        int64
	CustomerID, TeamID int64
	TeamName           string
	FirstReachedAt     time.Time
	SalesAmountMinor   int64
	SalesOrderCount    int64
	Mine               bool
}

type LeaderboardPage struct {
	Kind        LeaderboardKind
	Period      LeaderboardPeriod
	WindowStart time.Time
	WindowEnd   time.Time
	Items       []LeaderboardEntry
	MyEntry     *LeaderboardEntry
	NextCursor  string
}

// SalesFactWriter is the same-UoW handoff from Order/Distribution. It freezes
// campaign/product/promoter attribution and the paid amount; a public client
// cannot submit any of these facts.
type SalesFactWriter interface {
	RecordSalesFactWithin(context.Context, domain.SalesFact) (domain.SalesFact, error)
	ApplySuccessfulSalesRefundWithin(context.Context, int64, int64, int64, int32, time.Time) (domain.SalesFact, error)
}

type JoinCampaignCommand struct {
	Actor           TrustedSessionActor
	CampaignID      int64
	InvitationToken string
	// TeamID can only be selected by a participant who has no invitation.
	TeamID         int64
	IdempotencyKey string
}

type IssueInvitationCommand struct {
	Actor          TrustedSessionActor
	CampaignID     int64
	IdempotencyKey string
}

// PublicApplication is the complete member-facing Referral surface.  Every
// mutation derives its Customer ID from TrustedSessionActor rather than HTTP.
type PublicApplication interface {
	ListPublicCampaigns(context.Context) ([]CampaignSummary, error)
	ReadPublicCampaign(context.Context, int64) (CampaignView, error)
	PreviewInvitation(context.Context, string) (InvitationPreview, error)
	JoinCampaign(context.Context, JoinCampaignCommand) (MyCampaign, error)
	IssueInvitation(context.Context, IssueInvitationCommand) (InvitationLink, error)
	MyCampaign(context.Context, TrustedSessionActor, int64) (MyCampaign, error)
	ListMyInvites(context.Context, TrustedSessionActor, int64, string, int32) (InvitePage, error)
	Leaderboard(context.Context, LeaderboardQuery) (LeaderboardPage, error)
	IssueProductActivityContext(context.Context, TrustedSessionActor, int64, string) (string, error)
}

type CreateCampaignCommand struct {
	ActorAdminID                int64
	Name, CoverURL, Description string
	RewardRules                 string
	StartsAt, EndsAt            time.Time
	TeamMode                    domain.TeamMode
	QualificationMode           domain.QualificationMode
	ProductID                   int64
	ProductType                 string
	LeaderboardMetric           domain.LeaderboardMetric
	IdempotencyKey              string
}

type UpdateCampaignCommand struct {
	CampaignID, ExpectedVersion int64
	ActorAdminID                int64
	Name, CoverURL, Description string
	RewardRules                 string
	StartsAt, EndsAt            time.Time
	TeamMode                    domain.TeamMode
	QualificationMode           domain.QualificationMode
	ProductID                   int64
	ProductType                 string
	LeaderboardMetric           domain.LeaderboardMetric
	IdempotencyKey              string
}

// PurchaseQualificationReader is the only Referral seam for product-based
// participation. The implementation owns Identity/Order/Payment reads and
// is injected by composition; Referral never queries those tables.
type PurchaseQualificationReader interface {
	CheckWithin(context.Context, int64, int64, distributiondomain.ProductType) (distributiondomain.Qualification, error)
}

type SetCampaignStateCommand struct {
	CampaignID, ExpectedVersion int64
	ActorAdminID                int64
	Target                      domain.CampaignState
	IdempotencyKey              string
}

type CreateTeamCommand struct {
	CampaignID, CaptainCustomerID int64
	ActorAdminID                  int64
	Name, LogoURL                 string
	IdempotencyKey                string
}

type ReverseInvitationCommand struct {
	ParticipationID int64
	ActorAdminID    int64
	Reason          string
	IdempotencyKey  string
}

type RecordRewardCommand struct {
	CampaignID, CustomerID, ScoreEventID int64
	ActorAdminID                         int64
	Period, Reward, EvidenceReference    string
	IdempotencyKey                       string
}

type RevokeInvitationCommand struct {
	InvitationID   int64
	ActorAdminID   int64
	Reason         string
	IdempotencyKey string
}

type AdminCampaignPage struct {
	Items      []CampaignSummary
	NextCursor string
}

type AdminReferralRecord struct {
	Participation domain.Participation
	CampaignName  string
	ScoreEventID  int64
	ScoreState    string
}

type AdminReferralPage struct {
	Items      []AdminReferralRecord
	NextCursor string
}

// AdminParticipantQuery filters confirmed activity participations. An empty
// State means all states; otherwise only "active" and "reversed" are valid.
// TeamID applies to the participant's immutable activity team. Cursor is an
// opaque joined-at/id keyset continuation; legacy numeric OFFSET cursors are
// rejected so a live activity cannot mix pagination semantics.
type AdminParticipantQuery struct {
	CampaignID int64
	TeamID     int64
	State      domain.ParticipationState
	Cursor     string
	Limit      int32
}

type AdminParticipantRecord struct {
	Participation         domain.Participation
	Team                  domain.Team
	InviterCustomerID     int64
	DirectInvitationCount int64
}

type AdminParticipantPage struct {
	Items      []AdminParticipantRecord
	NextCursor string
}

// AdminInvitationQuery filters invited activity participations. State is the
// score truth: active selects an unreversed credit and reversed selects a
// reversed credit. TeamID applies to the invitee's immutable activity team.
// Cursor follows the same opaque joined-at/id keyset contract as participants.
type AdminInvitationQuery struct {
	CampaignID        int64
	TeamID            int64
	InviterCustomerID int64
	State             domain.ParticipationState
	Cursor            string
	Limit             int32
}

type AdminInvitationRecord struct {
	Participation     domain.Participation
	InviterCustomerID int64
	InviterTeamID     int64
	InviterTeam       domain.Team
	ParticipantTeam   domain.Team
	// ScoreState is one of "valid", "reversed", or "none" for a legacy or
	// inconsistent read row. Invited v1 participations normally have valid or
	// reversed credit facts.
	ScoreState string
}

type AdminInvitationPage struct {
	Items      []AdminInvitationRecord
	NextCursor string
}

// AdminParticipantInvitationQuery scopes an operations drilldown to one
// immutable participation record. The service resolves that record's customer
// server-side before applying the invitation query, so an admin page does not
// need to pass a raw inviter customer ID from the browser. Cursor follows the
// same opaque joined-at/id keyset contract as participants.
type AdminParticipantInvitationQuery struct {
	CampaignID      int64
	ParticipationID int64
	State           domain.ParticipationState
	Cursor          string
	Limit           int32
}

type RelationshipHistoryPage struct {
	Items      []domain.RelationshipHistory
	NextCursor string
}

type RewardPage struct {
	Items      []domain.RewardRecord
	NextCursor string
}

type AdminApplication interface {
	CreateCampaign(context.Context, CreateCampaignCommand) (domain.Campaign, error)
	UpdateCampaign(context.Context, UpdateCampaignCommand) (domain.Campaign, error)
	SetCampaignState(context.Context, SetCampaignStateCommand) (domain.Campaign, error)
	CreateTeam(context.Context, CreateTeamCommand) (domain.Team, error)
	ReverseInvitation(context.Context, ReverseInvitationCommand) error
	RecordReward(context.Context, RecordRewardCommand) (domain.RewardRecord, error)
	RevokeInvitation(context.Context, RevokeInvitationCommand) error
	ReadAdminCampaign(context.Context, int64) (CampaignView, error)
	ListAdminCampaigns(context.Context, string, int32) (AdminCampaignPage, error)
	ListAdminReferrals(context.Context, int64, string, int32) (AdminReferralPage, error)
	ListAdminParticipants(context.Context, AdminParticipantQuery) (AdminParticipantPage, error)
	ListAdminInvitations(context.Context, AdminInvitationQuery) (AdminInvitationPage, error)
	ListAdminParticipantInvitations(context.Context, AdminParticipantInvitationQuery) (AdminInvitationPage, error)
	// Export reads every matching record with a single SQL statement. It does
	// not accept cursor pagination and never silently truncates results.
	ListAdminParticipantsForExport(context.Context, AdminParticipantQuery) ([]AdminParticipantRecord, error)
	ListAdminInvitationsForExport(context.Context, AdminInvitationQuery) ([]AdminInvitationRecord, error)
	ListAdminParticipantInvitationsForExport(context.Context, AdminParticipantInvitationQuery) ([]AdminInvitationRecord, error)
	ListRelationshipHistory(context.Context, int64, string, int32) (RelationshipHistoryPage, error)
	ListRewards(context.Context, int64, string, int32) (RewardPage, error)
}

// RelationshipSnapshot is the only planned bridge for a later order-time
// relation attribution decision.  This release writes it but never asks Order
// to use it for commission calculation or overrides an existing promotion URL.
type RelationshipSnapshot struct {
	RelationshipID, Version, CustomerID, ReferrerCustomerID, SourceCampaignID, InvitationID int64
	EffectiveAt                                                                             time.Time
}

type CurrentRelationshipReader interface {
	CurrentRelationship(context.Context, int64) (RelationshipSnapshot, bool, error)
	// RelationshipAt reads the relation version effective at a given instant.
	// It is reserved for a future order-time attribution snapshot and never
	// enables commission in this release.
	RelationshipAt(context.Context, int64, time.Time) (RelationshipSnapshot, bool, error)
}

// CanonicalCustomerVerifier is supplied by a composed Identity/Customer
// adapter.  Referral only needs this narrow verification for staff-selected
// captains; it never reads identity tables or accepts a browser assertion.
type CanonicalCustomerVerifier interface {
	VerifyCanonicalCustomer(context.Context, int64) (bool, error)
}
