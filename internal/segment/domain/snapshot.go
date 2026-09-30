package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

var ErrInvalidSnapshot = errors.New("invalid audience snapshot")

type RefreshState string

const (
	RefreshAccepted   RefreshState = "accepted"
	RefreshQueued     RefreshState = "queued"
	RefreshEvaluating RefreshState = "evaluating"
	RefreshStaging    RefreshState = "staging"
	RefreshPublished  RefreshState = "published"
	RefreshFailed     RefreshState = "failed"
)

type RefreshKind string

const (
	RefreshLegacy      RefreshKind = "legacy"
	RefreshManual      RefreshKind = "manual"
	RefreshIncremental RefreshKind = "incremental"
	RefreshDaily       RefreshKind = "daily"
)

func ValidRefreshKind(value RefreshKind) bool {
	return value == RefreshLegacy || value == RefreshManual || value == RefreshIncremental || value == RefreshDaily
}

func (value RefreshKind) IsComplete() bool { return value == RefreshDaily || value == RefreshLegacy }

type RefreshRun struct {
	ID                     int64        `json:"id"`
	PackageID              int64        `json:"package_id"`
	ConfigurationVersionID int64        `json:"configuration_version_id"`
	SourceKeyDigest        [32]byte     `json:"-"`
	ReferenceTime          time.Time    `json:"reference_time"`
	RefreshKind            RefreshKind  `json:"refresh_kind"`
	State                  RefreshState `json:"state"`
	RiverJobID             *int64       `json:"river_job_id,omitempty"`
	ErrorCode              string       `json:"error_code,omitempty"`
	CreatedAt              time.Time    `json:"created_at"`
	UpdatedAt              time.Time    `json:"updated_at"`
	CompletedAt            *time.Time   `json:"completed_at,omitempty"`
}

type PublishedRefresh struct {
	Snapshot           Snapshot
	PreviousSnapshotID *int64
	ExitedMemberCount  int64
}

type Snapshot struct {
	ID                     int64      `json:"id"`
	PackageID              int64      `json:"package_id"`
	ConfigurationVersionID int64      `json:"configuration_version_id"`
	RefreshRunID           int64      `json:"refresh_run_id"`
	State                  string     `json:"state"`
	ReferenceTime          time.Time  `json:"reference_time"`
	MemberCount            int64      `json:"member_count"`
	MemberDigest           [32]byte   `json:"-"`
	SourceWatermarkDigest  [32]byte   `json:"-"`
	CreatedAt              time.Time  `json:"created_at"`
	PublishedAt            *time.Time `json:"published_at,omitempty"`
}

// SnapshotMemberFact contains only immutable, canonical qualifying-payment
// evidence copied from the evaluation into the published snapshot and events.
type SnapshotMemberFact struct {
	CustomerID  customerdomain.CustomerID
	PaidOrderID int64
	PaidAt      *time.Time
}

func DigestMembers(ids []customerdomain.CustomerID) [32]byte {
	h := sha256.New()
	var value [8]byte
	for _, id := range ids {
		binary.BigEndian.PutUint64(value[:], uint64(id))
		_, _ = h.Write(value[:])
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

func DigestSnapshotMemberFacts(facts []SnapshotMemberFact) [32]byte {
	h := sha256.New()
	var idBytes [8]byte
	for _, fact := range facts {
		binary.BigEndian.PutUint64(idBytes[:], uint64(fact.CustomerID))
		_, _ = h.Write(idBytes[:])
		if fact.PaidAt == nil {
			_, _ = h.Write([]byte{0})
			continue
		}
		_, _ = h.Write([]byte{1})
		var orderID [8]byte
		binary.BigEndian.PutUint64(orderID[:], uint64(fact.PaidOrderID))
		_, _ = h.Write(orderID[:])
		value := fact.PaidAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(value)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(value))
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}
