// Package port is the stable cross-domain boundary for canonical audiences.
package port

import (
	"context"
	"encoding/json"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type (
	PackageID              int64
	ConfigurationVersionID int64
	SnapshotID             int64
	RefreshRunID           int64
	Digest                 [32]byte
)

type PackageStatus string

const (
	PackagePaused   PackageStatus = "paused"
	PackageActive   PackageStatus = "active"
	PackageArchived PackageStatus = "archived"
)

type SnapshotState string

const (
	SnapshotPreparing SnapshotState = "preparing"
	SnapshotPublished SnapshotState = "published"
	SnapshotFailed    SnapshotState = "failed"
)

type IdentityDisposition string

const (
	IdentityResolved   IdentityDisposition = "resolved"
	IdentityUnresolved IdentityDisposition = "unresolved"
	IdentityConflict   IdentityDisposition = "conflict"
	IdentityInvalid    IdentityDisposition = "invalid"
)

type Definition struct {
	SchemaVersion int             `json:"schema_version"`
	Expression    json.RawMessage `json:"expression"`
	Digest        Digest          `json:"-"`
}

type Package struct {
	ID                     PackageID              `json:"id"`
	Code                   string                 `json:"code"`
	Name                   string                 `json:"name"`
	Status                 PackageStatus          `json:"status"`
	Version                int64                  `json:"version"`
	ConfigurationVersionID ConfigurationVersionID `json:"configuration_version_id,omitempty"`
	PublishedSnapshotID    SnapshotID             `json:"published_snapshot_id,omitempty"`
	UpdatedAt              time.Time              `json:"updated_at"`
}

type Snapshot struct {
	ID                     SnapshotID             `json:"id"`
	PackageID              PackageID              `json:"package_id"`
	ConfigurationVersionID ConfigurationVersionID `json:"configuration_version_id"`
	State                  SnapshotState          `json:"state"`
	ReferenceTime          time.Time              `json:"reference_time"`
	MemberCount            int64                  `json:"member_count"`
	MemberDigest           Digest                 `json:"-"`
	SourceWatermarkDigest  Digest                 `json:"-"`
	PublishedAt            *time.Time             `json:"published_at,omitempty"`
}

type Member struct {
	Operations  *CoreMemberDetail         `json:"operations,omitempty"`
	SnapshotID  SnapshotID                `json:"snapshot_id"`
	CustomerID  customerdomain.CustomerID `json:"customer_id"`
	EnteredAt   time.Time                 `json:"entered_at"`
	Disposition IdentityDisposition       `json:"identity_disposition"`
}

type MemberPage struct {
	Items      []Member `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

type MemberEventPage struct {
	Items      []MemberEnteredV1
	NextCursor string
}

// MemberEventReader exposes only immutable canonical events created by a
// published snapshot. It never resolves or provisions an identity.
type MemberEventReader interface {
	MemberEvents(context.Context, SnapshotID, string, int) (MemberEventPage, error)
}

// SnapshotReader exposes only immutable, published audience facts.
type SnapshotReader interface {
	PublishedSnapshot(context.Context, PackageID) (Snapshot, bool, error)
	Snapshot(context.Context, SnapshotID) (Snapshot, bool, error)
	Members(context.Context, SnapshotID, string, int) (MemberPage, error)
}

type PackageReader interface {
	Package(context.Context, PackageID) (Package, bool, error)
}

const EventAudienceMemberEnteredV1 = "audience.member_entered.v1"

type MemberEnteredV1 struct {
	EventID                string                    `json:"event_id"`
	PackageID              PackageID                 `json:"package_id"`
	SnapshotID             SnapshotID                `json:"snapshot_id"`
	ConfigurationVersionID ConfigurationVersionID    `json:"configuration_version_id"`
	CustomerID             customerdomain.CustomerID `json:"customer_id"`
	OccurredAt             time.Time                 `json:"occurred_at"`
}
