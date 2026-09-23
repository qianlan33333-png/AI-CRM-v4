// Package port contains stable Customer-domain boundaries.
package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

type DirectoryProjection struct {
	CustomerID      customerdomain.CustomerID
	CustomerStatus  customerdomain.Status
	DisplayName     string
	AvatarURL       string
	Gender          int16
	ContactType     int16
	CorpName        string
	OneIDLabel      string
	PhoneMasked     string
	PhoneAssurance  identitydomain.Assurance
	ActivationState string
	Source          string
	SourceVersion   int64
	LastSyncedAt    time.Time
	UpdatedAt       time.Time
}

// DirectoryDisplayNameReader exposes only the presentation-safe names needed
// by another domain after it already holds canonical Customer IDs. Missing
// projections are omitted; this boundary never resolves external identities.
type DirectoryDisplayNameReader interface {
	DisplayNames(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error)
}

// DirectoryPublicProfile is the smallest public-facing identity presentation
// projection. It is used only after a domain already holds a canonical
// Customer ID; it cannot search, resolve, provision, or reveal contact data.
type DirectoryPublicProfile struct {
	DisplayName string
	AvatarURL   string
}

type DirectoryPublicProfileReader interface {
	PublicProfiles(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]DirectoryPublicProfile, error)
}

// DirectoryContactDisplay is the minimum already-masked Customer projection
// that another domain may render after it already holds a canonical customer
// ID. It cannot resolve an identity or expose an unmasked phone number.
type DirectoryContactDisplay struct {
	CustomerNumber string
	DisplayName    string
	PhoneMasked    string
}

type DirectoryContactDisplayReader interface {
	ContactDisplays(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]DirectoryContactDisplay, error)
}

// RadarVisitorDirectoryDisplay is the smallest Customer-owned projection an
// already-authorized Radar visitor read can render. It deliberately contains
// neither contact values nor identity evidence.
type RadarVisitorDirectoryDisplay struct {
	DisplayName string
}

// RadarVisitorDirectoryReader exposes safe directory names and canonical-label
// search candidates for the Radar admin visitor projection. The caller already
// holds Customer IDs from its own read model; this boundary never resolves,
// provisions, or links an identity. Returned labels must derive from the
// canonical CustomerID, never a mutable directory oneid_label cache.
type RadarVisitorDirectoryReader interface {
	RadarVisitorDisplays(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]RadarVisitorDirectoryDisplay, error)
	SearchRadarVisitorCustomers(context.Context, string, int) ([]customerdomain.CustomerID, error)
}

// ProjectionWriter is called by versioned event consumers. It does not grant
// access to Identity or WeCom-owned tables.
type ProjectionWriter interface {
	UpsertDirectoryProjection(context.Context, DirectoryProjection) error
	MarkDirectoryStale(context.Context, []customerdomain.CustomerID, time.Time) (int64, error)
	UpdateDirectoryPhone(context.Context, customerdomain.CustomerID, string, identitydomain.Assurance, int64, time.Time) error
	ClearDirectoryPhone(context.Context, customerdomain.CustomerID, time.Time) error
}

// CallbackProjectionWriter exposes only the narrow mutation required by a
// verified WeCom callback. It cannot write profile or phone attributes.
type CallbackProjectionWriter interface {
	ActivateDirectoryCustomer(context.Context, customerdomain.CustomerID, string, time.Time) error
}

// ProviderProfileObservation contains presentation-only facts returned by a
// trusted Provider read. These values never participate in OneID resolution,
// provisioning, linking, or merging.
type ProviderProfileObservation struct {
	DisplayName string
	AvatarURL   string
	Source      string
	ObservedAt  time.Time
}

// ProviderProfileWriter applies trusted presentation facts to Customer's own
// directory projection inside the caller's existing PostgreSQL UoW.
type ProviderProfileWriter interface {
	ObserveProviderProfile(context.Context, customerdomain.CustomerID, ProviderProfileObservation) error
}
