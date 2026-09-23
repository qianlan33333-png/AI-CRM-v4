package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

const RuleVersion = "hxc-current-v3"

var ErrInvalidRow = errors.New("invalid hxc dashboard row")

type Stage string

const (
	ActiveUsed                   Stage = "active_used"
	ActiveUnused                 Stage = "active_unused"
	RegisteredNoActiveMembership Stage = "registered_no_active_membership"
)

type IdentityState string

const (
	Matched   IdentityState = "matched"
	Unmatched IdentityState = "unmatched"
	Conflict  IdentityState = "conflict"
)

type SourceRow struct {
	HXCUserID                                                string
	UnionID                                                  string
	Phone                                                    string
	SubscriptionTier                                         string
	SubscriptionExpiresAt                                    *time.Time
	MonthlyChatQuota, CurrentPeriodUsed                      int64
	ConsultationLimit, ConsultationUsed                      int64
	MembershipAttribution                                    string
	Sessions7D, Sessions30D, SessionsTotal                   int64
	UserMessages7D, UserMessages30D, UserMessagesTotal       int64
	CapabilityUsage                                          []byte
	LastUsedAt                                               *time.Time
	LastCapability, BusinessStage, MainLineType, UserSegment string
	FocusTopics                                              []byte
	PainTag                                                  string
	// Shared facts are read from the same immutable source snapshot. They are
	// deliberately separate from dashboard display-stage fields.
	FormallyLoggedIn, HasTokenUsage        bool
	FormalLoginAt, CardLastOpenedAt        *time.Time
	LearningPlanFound                      bool
	LearningPlanStatus                     string
	LearningPlanCurrent, LearningPlanTotal *int64
	CardOpenCount7D                        int64
	// MembershipSource, status and expiry all come from the same selected
	// new_version_memberships row. Dashboard subscription expiry is separate.
	MembershipRecordFound, IsMember    bool
	MembershipSource, MembershipStatus string
	MembershipExpiresAt                *time.Time
	SourceUpdatedAt                    time.Time
}

type ProjectionRow struct {
	SubjectDigest [32]byte
	UserRef       string
	Stage         Stage
	SourceRow
	CustomerID         customerdomain.CustomerID
	IdentityState      IdentityState
	MatchedBy          string
	IdentityReasonCode string
	IdentityCaseID     int64
	MergeCandidateID   int64
}

type Counts struct {
	Total, ActiveUsed, ActiveUnused, RegisteredNoActiveMembership int64
	Matched, Unmatched, Conflict                                  int64
	MatchedByUnionID, MatchedByPhone, MatchedByBoth               int64
	PendingObservation, InvalidIdentity                           int64
}

type Projection struct {
	RegistrationCoverage           map[int64]string
	ID                             int64
	AsOf                           time.Time
	Watermark                      *time.Time
	SourceDigest, ProjectionDigest [32]byte
	Counts                         Counts
	IdentityReplayVerified         int64
	SharedFactsAvailable           bool
	PublishedAt                    time.Time
	Rows                           []ProjectionRow
}

type RefreshStatus string

const (
	RefreshQueued     RefreshStatus = "queued"
	RefreshRunning    RefreshStatus = "running"
	RefreshPublishing RefreshStatus = "publishing"
	RefreshSucceeded  RefreshStatus = "succeeded"
	RefreshFailed     RefreshStatus = "failed"
)

type RefreshRun struct {
	ID             int64         `json:"run_id"`
	Trigger        string        `json:"trigger"`
	IdentityMode   string        `json:"identity_mode"`
	Status         RefreshStatus `json:"status"`
	ProjectionID   *int64        `json:"projection_id,omitempty"`
	SourceCount    int64         `json:"source_count"`
	ProcessedCount int64         `json:"processed_count"`
	ReplayVerified int64         `json:"identity_replay_verified_count"`
	ErrorCode      string        `json:"error_code,omitempty"`
	Version        int64         `json:"-"`
	RequestedBy    int64         `json:"-"`
	StartedAt      *time.Time    `json:"started_at,omitempty"`
	CompletedAt    *time.Time    `json:"completed_at,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

func Classify(row SourceRow, asOf time.Time) Stage {
	active := !strings.EqualFold(strings.TrimSpace(row.SubscriptionTier), "free") && strings.TrimSpace(row.SubscriptionTier) != "" && row.SubscriptionExpiresAt != nil && row.SubscriptionExpiresAt.After(asOf)
	if !active {
		return RegisteredNoActiveMembership
	}
	if row.LastUsedAt != nil {
		return ActiveUsed
	}
	return ActiveUnused
}

func Subject(key []byte, hxcUserID string) ([32]byte, string, error) {
	if len(key) < 32 || strings.TrimSpace(hxcUserID) != hxcUserID || hxcUserID == "" {
		return [32]byte{}, "", ErrInvalidRow
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("hxc-user-v1\x00" + hxcUserID))
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	return digest, "HXC-" + hex.EncodeToString(digest[:6]), nil
}
