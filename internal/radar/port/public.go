package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
)

type OAuthProvider interface {
	Enabled() bool
	AuthorizationURL(string) string
	Exchange(context.Context, string) (identitydomain.VerifiedFact, error)
}

type IdentityCoordinator interface {
	Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error)
	ProvisionVerifiedIdentity(context.Context, identityport.ProvisionCommand) (identityport.ProvisionResult, error)
}

type OAuthState struct {
	RadarID radar.RadarID
	Version radar.LinkVersion
	Path    string
	Expires time.Time
}

type ViewSession struct {
	ID          int64
	RadarID     radar.RadarID
	Version     radar.LinkVersion
	IdentityID  int64
	CustomerID  customerdomain.CustomerID
	Attribution AttributionStatus
	ExpiresAt   time.Time
}

type EventRecord struct {
	ReceiptID     string
	SessionID     int64
	RadarID       radar.RadarID
	Version       radar.LinkVersion
	Stage         EventStage
	Attribution   AttributionStatus
	IdentityID    int64
	CustomerID    customerdomain.CustomerID
	KeyDigest     [32]byte
	PayloadDigest [32]byte
	FailureCode   string
	OccurredAt    time.Time
}

type PublicRepository interface {
	CreateOAuthState(context.Context, [32]byte, OAuthState, time.Time) error
	ConsumeOAuthState(context.Context, [32]byte, time.Time) (OAuthState, error)
	CreateSession(context.Context, [32]byte, ViewSession, [32]byte, time.Time) (ViewSession, error)
	ReadSession(context.Context, [32]byte, radar.RadarID, radar.LinkVersion, time.Time) (ViewSession, error)
	AppendEvent(context.Context, EventRecord, time.Time) (EventProjection, bool, error)
}

type Content struct {
	Bytes     []byte
	MediaType string
	FileName  string
	ETag      string
}

// ContentReader is implemented only by the composition root. Radar never
// reaches into Media tables and receives no mutable Media object.
type ContentReader interface {
	ReadRadarContent(context.Context, radar.ContentType, radar.MediaID) (Content, error)
}

type PublicAction string

const (
	PublicOAuthRedirect PublicAction = "oauth_redirect"
	PublicLinkRedirect  PublicAction = "link_redirect"
	PublicViewer        PublicAction = "viewer"
)

type PublicAccess struct {
	Action       PublicAction
	Location     string
	SessionToken string
	Link         radar.Link
}

type PublicService interface {
	Open(context.Context, radar.PublicCode, string) (PublicAccess, error)
	CompleteOAuth(context.Context, string, string) (string, string, error)
	Content(context.Context, radar.PublicCode, string) (Content, error)
	Record(context.Context, radar.PublicCode, string, EventStage, string) (EventProjection, bool, error)
}
