// Package domain contains Referral's closed business facts.  It intentionally
// does not know about HTTP, trusted-session construction, customer tables,
// payment, or the persistence implementation.
package domain

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalid    = errors.New("invalid referral fact")
	ErrTransition = errors.New("invalid referral transition")
	ErrVersion    = errors.New("referral version conflict")
)

// TeamMode controls whether new participation facts may carry a team
// grouping.  Teams are an aggregation dimension only; they do not grant
// authority, invitation privileges, or rewards.
type TeamMode string

const (
	TeamModeTeam       TeamMode = "team"
	TeamModeIndividual TeamMode = "individual"
)

func (m TeamMode) Valid() bool { return m == TeamModeTeam || m == TeamModeIndividual }

// QualificationMode describes the server-side prerequisite for joining an
// activity.  ProductPurchase requires a trusted, refund-checked purchase of
// the exact configured product.
type QualificationMode string

const (
	QualificationFreeSignup      QualificationMode = "free_signup"
	QualificationProductPurchase QualificationMode = "product_purchase"
)

func (m QualificationMode) Valid() bool {
	return m == QualificationFreeSignup || m == QualificationProductPurchase
}

type LeaderboardMetric string

const (
	LeaderboardInvites     LeaderboardMetric = "invites"
	LeaderboardSalesAmount LeaderboardMetric = "sales_amount"
	LeaderboardSalesOrders LeaderboardMetric = "sales_orders"
	// LeaderboardSales is retained as a source-compatible alias for older
	// persisted drafts; new writes normalize it to amount-based sales.
	LeaderboardSales LeaderboardMetric = LeaderboardSalesAmount
)

func (m LeaderboardMetric) Valid() bool {
	return m == LeaderboardInvites || m == LeaderboardSalesAmount || m == LeaderboardSalesOrders || m == LeaderboardMetric("sales")
}

const (
	ProductTypeStandard      = "standard_product"
	ProductTypeServicePeriod = "service_period"
)

// CampaignConfig is kept inside Referral as an opaque product reference. It
// deliberately does not import Product, Order, Payment, or Distribution.
type CampaignConfig struct {
	TeamMode          TeamMode
	QualificationMode QualificationMode
	ProductID         int64
	ProductType       string
	LeaderboardMetric LeaderboardMetric
}

func DefaultCampaignConfig() CampaignConfig {
	return CampaignConfig{TeamMode: TeamModeTeam, QualificationMode: QualificationFreeSignup, LeaderboardMetric: LeaderboardInvites}
}

func (c CampaignConfig) normalized() CampaignConfig {
	defaults := DefaultCampaignConfig()
	if c.TeamMode == "" {
		c.TeamMode = defaults.TeamMode
	}
	if c.QualificationMode == "" {
		c.QualificationMode = defaults.QualificationMode
	}
	if c.LeaderboardMetric == "" {
		c.LeaderboardMetric = defaults.LeaderboardMetric
	}
	if c.LeaderboardMetric == LeaderboardMetric("sales") {
		c.LeaderboardMetric = LeaderboardSalesAmount
	}
	return c
}

func (c CampaignConfig) Valid() bool {
	c = c.normalized()
	if !c.TeamMode.Valid() || !c.QualificationMode.Valid() || !c.LeaderboardMetric.Valid() {
		return false
	}
	if c.QualificationMode == QualificationProductPurchase {
		return c.ProductID > 0 && (c.ProductType == ProductTypeStandard || c.ProductType == ProductTypeServicePeriod)
	}
	return c.ProductID == 0 && c.ProductType == ""
}

type CampaignState string

const (
	CampaignDraft     CampaignState = "draft"
	CampaignScheduled CampaignState = "scheduled"
	CampaignActive    CampaignState = "active"
	CampaignEnded     CampaignState = "ended"
	CampaignDisabled  CampaignState = "disabled"
)

func (s CampaignState) Valid() bool {
	switch s {
	case CampaignDraft, CampaignScheduled, CampaignActive, CampaignEnded, CampaignDisabled:
		return true
	default:
		return false
	}
}

// Campaign is Referral-owned configuration.  Its scoring rule is deliberately
// fixed to one point for a direct, accepted activity participation in v1.
type Campaign struct {
	ID                          int64
	Name, CoverURL, Description string
	RewardRules                 string
	State                       CampaignState
	StartsAt, EndsAt            time.Time
	Version                     int64
	CreatedBy                   int64
	CreatedAt, UpdatedAt        time.Time
	TeamMode                    TeamMode
	QualificationMode           QualificationMode
	ProductID                   int64
	ProductType                 string
	LeaderboardMetric           LeaderboardMetric
}

func (c Campaign) Config() CampaignConfig {
	return CampaignConfig{TeamMode: c.TeamMode, QualificationMode: c.QualificationMode, ProductID: c.ProductID, ProductType: c.ProductType, LeaderboardMetric: c.LeaderboardMetric}.normalized()
}

func (c Campaign) Valid() bool {
	return c.ID > 0 && validText(c.Name, 200, true) && validText(c.CoverURL, 2000, false) &&
		validText(c.Description, 5000, false) && validText(c.RewardRules, 5000, false) && c.State.Valid() &&
		!c.StartsAt.IsZero() && !c.EndsAt.IsZero() && c.EndsAt.After(c.StartsAt) && c.Version > 0 &&
		c.CreatedBy >= 0 && !c.CreatedAt.IsZero() && !c.UpdatedAt.Before(c.CreatedAt) && c.Config().Valid()
}

func (c Campaign) ValidForInsert() bool {
	if c.ID != 0 || c.Version != 1 {
		return false
	}
	probe := c
	probe.ID = 1
	return probe.Valid()
}

func (c Campaign) AcceptingAt(at time.Time) bool {
	if !c.Valid() || at.IsZero() {
		return false
	}
	return (c.State == CampaignScheduled || c.State == CampaignActive) && !at.Before(c.StartsAt) && at.Before(c.EndsAt)
}

// EffectiveState lets public reads report a consistent lifecycle even when a
// durable close job has not yet been scheduled after a process restart.
func (c Campaign) EffectiveState(at time.Time) CampaignState {
	if !c.Valid() || at.IsZero() || c.State == CampaignDraft || c.State == CampaignDisabled || c.State == CampaignEnded {
		return c.State
	}
	if !at.Before(c.EndsAt) {
		return CampaignEnded
	}
	if at.Before(c.StartsAt) {
		return CampaignScheduled
	}
	return CampaignActive
}

func (c Campaign) Update(expectedVersion int64, name, coverURL, description, rewardRules string, startsAt, endsAt time.Time, at time.Time) (Campaign, error) {
	return c.UpdateWithConfig(expectedVersion, name, coverURL, description, rewardRules, startsAt, endsAt, c.Config(), at)
}

// UpdateWithConfig applies the CAS update and enforces the immutable team
// mode boundary. A started campaign cannot be made editable by moving its
// start time into the future.
func (c Campaign) UpdateWithConfig(expectedVersion int64, name, coverURL, description, rewardRules string, startsAt, endsAt time.Time, config CampaignConfig, at time.Time) (Campaign, error) {
	if expectedVersion != c.Version {
		return Campaign{}, ErrVersion
	}
	if c.State == CampaignDisabled || c.EffectiveState(at) == CampaignEnded {
		return Campaign{}, ErrTransition
	}
	config = config.normalized()
	if !config.Valid() {
		return Campaign{}, ErrInvalid
	}
	if !at.Before(c.StartsAt) && (config != c.Config() || !startsAt.Equal(c.StartsAt) || !endsAt.Equal(c.EndsAt)) {
		return Campaign{}, ErrTransition
	}
	if config != c.Config() && !at.Before(startsAt.UTC()) {
		return Campaign{}, ErrTransition
	}
	next := c
	next.Name, next.CoverURL, next.Description, next.RewardRules = name, coverURL, description, rewardRules
	next.StartsAt, next.EndsAt, next.Version, next.UpdatedAt = startsAt.UTC(), endsAt.UTC(), c.Version+1, at.UTC()
	next.TeamMode, next.QualificationMode, next.ProductID, next.ProductType, next.LeaderboardMetric = config.TeamMode, config.QualificationMode, config.ProductID, config.ProductType, config.LeaderboardMetric
	if at.IsZero() || at.Before(c.UpdatedAt) || !next.Valid() {
		return Campaign{}, ErrInvalid
	}
	return next, nil
}

func (c Campaign) Transition(expectedVersion int64, target CampaignState, at time.Time) (Campaign, error) {
	if expectedVersion != c.Version {
		return Campaign{}, ErrVersion
	}
	if at.IsZero() || at.Before(c.UpdatedAt) || !target.Valid() {
		return Campaign{}, ErrInvalid
	}
	allowed := false
	switch c.State {
	case CampaignDraft:
		allowed = target == CampaignScheduled || target == CampaignActive || target == CampaignDisabled
	case CampaignScheduled:
		allowed = target == CampaignActive || target == CampaignDisabled || target == CampaignEnded
	case CampaignActive:
		allowed = target == CampaignEnded || target == CampaignDisabled
	}
	if !allowed {
		return Campaign{}, ErrTransition
	}
	next := c
	next.State, next.Version, next.UpdatedAt = target, c.Version+1, at.UTC()
	return next, nil
}

type Team struct {
	ID, CampaignID, CaptainCustomerID int64
	Name, LogoURL                     string
	Version                           int64
	CreatedAt, UpdatedAt              time.Time
}

func (t Team) Valid() bool {
	return t.ID > 0 && t.CampaignID > 0 && t.CaptainCustomerID > 0 && validText(t.Name, 100, true) &&
		validText(t.LogoURL, 2000, false) && t.Version > 0 && !t.CreatedAt.IsZero() && !t.UpdatedAt.Before(t.CreatedAt)
}

func (t Team) ValidForInsert() bool {
	if t.ID != 0 || t.Version != 1 {
		return false
	}
	probe := t
	probe.ID = 1
	return probe.Valid()
}

type ParticipationState string

const (
	ParticipationActive   ParticipationState = "active"
	ParticipationReversed ParticipationState = "reversed"
)

func (s ParticipationState) Valid() bool {
	return s == ParticipationActive || s == ParticipationReversed
}

// Participation freezes team and invitation source for one campaign.  Later
// current-referrer changes never rewrite this record.
type Participation struct {
	ID, CampaignID, CustomerID, TeamID             int64
	InvitationID, InviterCustomerID, InviterTeamID int64
	State                                          ParticipationState
	JoinedAt                                       time.Time
}

func (p Participation) Valid() bool {
	return p.ID > 0 && p.CampaignID > 0 && p.CustomerID > 0 && p.TeamID >= 0 && p.InvitationID >= 0 &&
		p.InviterCustomerID >= 0 && p.InviterTeamID >= 0 && p.State.Valid() && !p.JoinedAt.IsZero() &&
		((p.InvitationID == 0 && p.InviterCustomerID == 0 && p.InviterTeamID == 0) ||
			(p.InvitationID > 0 && p.InviterCustomerID > 0 && p.InviterTeamID >= 0))
}

type InvitationState string

const (
	InvitationActive  InvitationState = "active"
	InvitationRevoked InvitationState = "revoked"
	InvitationExpired InvitationState = "expired"
)

func (s InvitationState) Valid() bool {
	return s == InvitationActive || s == InvitationRevoked || s == InvitationExpired
}

type Invitation struct {
	ID, CampaignID, InviterCustomerID int64
	TokenDigest                       [32]byte
	State                             InvitationState
	CreatedAt, ExpiresAt              time.Time
	RevokedAt                         *time.Time
}

func (i Invitation) Valid() bool {
	return i.ID > 0 && i.CampaignID > 0 && i.InviterCustomerID > 0 && i.State.Valid() && !i.CreatedAt.IsZero() &&
		i.ExpiresAt.After(i.CreatedAt) && ((i.State == InvitationRevoked) == (i.RevokedAt != nil))
}

type Relationship struct {
	ID, CustomerID, ReferrerCustomerID, SourceCampaignID, InvitationID int64
	Version                                                            int64
	EffectiveAt                                                        time.Time
}

func (r Relationship) Valid() bool {
	return r.ID > 0 && r.CustomerID > 0 && r.ReferrerCustomerID > 0 && r.CustomerID != r.ReferrerCustomerID &&
		r.SourceCampaignID > 0 && r.InvitationID > 0 && r.Version > 0 && !r.EffectiveAt.IsZero()
}

// RelationshipHistory is append-only evidence of a successful, explicit
// invitation acceptance.  Zero old values mean the customer had no relation.
type RelationshipHistory struct {
	ID, RelationshipID, Version, CustomerID, PreviousReferrerCustomerID, ReferrerCustomerID, SourceCampaignID, InvitationID int64
	AcceptedAt                                                                                                              time.Time
}

func (h RelationshipHistory) Valid() bool {
	return h.ID > 0 && h.RelationshipID > 0 && h.Version > 0 && h.CustomerID > 0 && h.ReferrerCustomerID > 0 &&
		h.CustomerID != h.ReferrerCustomerID && h.SourceCampaignID > 0 && h.InvitationID > 0 && !h.AcceptedAt.IsZero()
}

type ScoreEventKind string

const (
	ScoreCredit  ScoreEventKind = "credit"
	ScoreReverse ScoreEventKind = "reversal"
)

func (k ScoreEventKind) Valid() bool { return k == ScoreCredit || k == ScoreReverse }

// ScoreEvent is immutable.  Reversing an invitation appends a negative event
// rather than editing the original credit, preserving rank explainability.
type ScoreEvent struct {
	ID, CampaignID, ParticipationID, InviterCustomerID, TeamID, Delta, ReversesScoreEventID int64
	Kind                                                                                    ScoreEventKind
	Reason                                                                                  string
	OccurredAt                                                                              time.Time
}

func (e ScoreEvent) Valid() bool {
	if e.ID < 1 || e.CampaignID < 1 || e.ParticipationID < 1 || e.InviterCustomerID < 1 || e.TeamID < 0 || !e.Kind.Valid() || e.OccurredAt.IsZero() {
		return false
	}
	if e.Kind == ScoreCredit {
		return e.Delta == 1 && e.ReversesScoreEventID == 0 && e.Reason == ""
	}
	return e.Delta == -1 && e.ReversesScoreEventID > 0 && validText(e.Reason, 500, true)
}

type RewardState string

const (
	RewardRecorded    RewardState = "recorded"
	RewardNeedsReview RewardState = "needs_review"
)

func (s RewardState) Valid() bool { return s == RewardRecorded || s == RewardNeedsReview }

// SalesFact is the immutable checkout attribution snapshot owned by Referral.
// Order/Payment/Distribution supply the trusted source facts through the
// stable port; no Referral query reads their tables. Refund totals are a
// cumulative, CAS-updated projection while OriginalPaidMinor is immutable.
type SalesFact struct {
	ID, CampaignID, OrderID, ProductID, PromoterCustomerID, TeamID int64
	OrderItemLine                                                  int32
	ProductType, SourceReference                                   string
	OriginalPaidMinor, SuccessfulRefundMinor                       int64
	PaidAt, CreatedAt, UpdatedAt                                   time.Time
	Version                                                        int64
}

func (s SalesFact) NetPaidMinor() int64 { return s.OriginalPaidMinor - s.SuccessfulRefundMinor }

func (s SalesFact) Valid() bool {
	return s.ID > 0 && s.CampaignID > 0 && s.OrderID > 0 && s.OrderItemLine > 0 && s.ProductID > 0 && s.PromoterCustomerID > 0 && s.TeamID >= 0 &&
		(s.ProductType == ProductTypeStandard || s.ProductType == ProductTypeServicePeriod) && s.SourceReference == strings.TrimSpace(s.SourceReference) && len(s.SourceReference) >= 1 && len(s.SourceReference) <= 200 &&
		s.OriginalPaidMinor > 0 && s.SuccessfulRefundMinor >= 0 && s.SuccessfulRefundMinor <= s.OriginalPaidMinor && !s.PaidAt.IsZero() && !s.CreatedAt.IsZero() && !s.UpdatedAt.Before(s.CreatedAt) && s.Version > 0
}

func (s SalesFact) ValidForInsert() bool {
	if s.ID != 0 || s.Version != 1 {
		return false
	}
	probe := s
	probe.ID = 1
	return probe.Valid()
}

func (s SalesFact) ApplySuccessfulRefund(expectedVersion, delta int64, at time.Time) (SalesFact, error) {
	if !s.Valid() || expectedVersion != s.Version || delta <= 0 || s.SuccessfulRefundMinor+delta > s.OriginalPaidMinor || at.IsZero() || at.Before(s.UpdatedAt) {
		return SalesFact{}, ErrInvalid
	}
	next := s
	next.SuccessfulRefundMinor += delta
	next.Version++
	next.UpdatedAt = at.UTC()
	return next, nil
}

type RewardRecord struct {
	ID, CampaignID, CustomerID, ScoreEventID int64
	Period, Reward, EvidenceReference        string
	State                                    RewardState
	RecordedBy                               int64
	RecordedAt                               time.Time
}

func (r RewardRecord) Valid() bool {
	return r.ID > 0 && r.CampaignID > 0 && r.CustomerID > 0 && r.ScoreEventID >= 0 &&
		validText(r.Period, 32, true) && validText(r.Reward, 500, true) && validText(r.EvidenceReference, 500, false) &&
		r.State.Valid() && r.RecordedBy > 0 && !r.RecordedAt.IsZero()
}

func validText(value string, maximum int, required bool) bool {
	return value == strings.TrimSpace(value) && len(value) <= maximum && (!required || value != "")
}
