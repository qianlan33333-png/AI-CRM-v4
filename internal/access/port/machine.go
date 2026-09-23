package port

import (
	"context"
	"encoding/json"
	"net/netip"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// MachineRepository is a separate Access-owned seam so existing session/RBAC
// repositories are not widened by machine-auth implementation details.
type MachineRepository interface {
	MachineClientByID(context.Context, string, bool) (domain.MachineClient, error)
	ListMachineClients(context.Context) ([]domain.MachineClient, error)
	CreateMachineClient(context.Context, domain.MachineClient) (domain.MachineClient, error)
	ReplaceMachineClient(context.Context, domain.MachineClient) error
	SetMachineClientLastUsed(context.Context, int64, time.Time) error
	AppendMachineAudit(context.Context, domain.MachineAudit) error
	ListMachineAudit(context.Context, int64, int) ([]MachineAuditEntry, error)
}

// The following DTOs are the stable Access boundary consumed by the machine
// HTTP host. They contain no secret hash or administrator role.
type CreateMachineClientInput struct {
	ClientID        string            `json:"client_id"`
	DisplayName     string            `json:"display_name"`
	Purpose         string            `json:"purpose"`
	Audiences       []string          `json:"audiences"`
	Scopes          []string          `json:"scopes"`
	Capabilities    []string          `json:"capabilities"`
	AllowedCIDRs    []string          `json:"allowed_cidrs"`
	OwnerScope      domain.OwnerScope `json:"owner_scope,omitempty"`
	TokenTTLSeconds int               `json:"token_ttl_seconds"`
	ExpiresAt       *time.Time        `json:"expires_at"`
}

type IssuedMachineClient struct {
	Client MachineClientSummary `json:"client"`
	Secret string               `json:"secret"`
}

type MachineClientSummary struct {
	ClientID        string            `json:"client_id"`
	DisplayName     string            `json:"display_name"`
	Purpose         string            `json:"purpose"`
	CredentialHint  string            `json:"credential_hint"`
	Audiences       []string          `json:"audiences"`
	Scopes          []string          `json:"scopes"`
	Capabilities    []string          `json:"capabilities"`
	AllowedCIDRs    []string          `json:"allowed_cidrs"`
	CorpID          string            `json:"corp_id,omitempty"`
	OwnerScope      domain.OwnerScope `json:"owner_scope,omitempty"`
	TokenTTLSeconds int               `json:"token_ttl_seconds"`
	ExpiresAt       *time.Time        `json:"expires_at,omitempty"`
	Enabled         bool              `json:"enabled"`
	ReissueRequired bool              `json:"reissue_required"`
	AuthVersion     int64             `json:"auth_version"`
	LastUsedAt      *time.Time        `json:"last_used_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
}

// UpdateMachineClientInput is deliberately narrower than creation. Frozen
// API-client templates keep their purpose, audience, scopes and capabilities;
// only a disabled caller's presentation, TTL and CIDR boundary are editable.
type UpdateMachineClientInput struct {
	DisplayName     string   `json:"display_name"`
	TokenTTLSeconds int      `json:"token_ttl_seconds"`
	AllowedCIDRs    []string `json:"allowed_cidrs"`
}

// PatchMachineClientInput is the V1 administrator control-plane edit. Nil
// fields preserve the stored value. OwnerScopeSet and ExpiresAtSet distinguish
// an omitted field from an explicit JSON null, so clearing either boundary is
// deliberate and reviewable. A changed grant advances auth_version atomically.
type PatchMachineClientInput struct {
	DisplayName     *string
	Audiences       *[]string
	Scopes          *[]string
	Capabilities    *[]string
	AllowedCIDRs    *[]string
	TokenTTLSeconds *int
	OwnerScope      domain.OwnerScope
	OwnerScopeSet   bool
	ExpiresAt       *time.Time
	ExpiresAtSet    bool
}

// MachineAuditEntry is the safe, client-scoped audit read model for the
// administrator control plane. Details are written by Access-owned code only;
// credentials, tokens, source addresses and customer identifiers are never
// retained in this table.
type MachineAuditEntry struct {
	ActorAdminUserID *int64          `json:"actor_admin_user_id,omitempty"`
	Action           string          `json:"action"`
	Outcome          string          `json:"outcome"`
	Details          json.RawMessage `json:"details"`
	CreatedAt        time.Time       `json:"created_at"`
}

type ClientCredentialsInput struct {
	ClientID        string
	ClientSecret    string
	Audience        string
	RequestedScopes []string
	SourceIP        netip.Addr
}

type IssuedAccessToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

type MachineTokenIssuer interface {
	IssueClientCredentialsToken(context.Context, ClientCredentialsInput) (IssuedAccessToken, error)
	AuthenticateBearer(context.Context, string, string, netip.Addr) (domain.MachinePrincipal, error)
}

// MachineRequestLimiter is the Access-owned persistent throttle used by the
// public machine protocol. The HTTP host supplies only an already-normalized
// client identifier/principal and source address; it never reads or writes
// Access rate-limit state itself.
//
// Both methods must make the decision durably. In-process counters would let a
// restart or a second API process bypass the same credential boundary.
type MachineRequestLimiter interface {
	AllowClientCredentials(context.Context, string, netip.Addr) error
	AllowMachineRequest(context.Context, domain.MachinePrincipal, netip.Addr) error
}

type MachineManagement interface {
	Create(context.Context, domain.Principal, CreateMachineClientInput) (IssuedMachineClient, error)
	CreateV1(context.Context, domain.Principal, CreateMachineClientInput) (IssuedMachineClient, error)
	List(context.Context, domain.Principal) ([]MachineClientSummary, error)
	Rotate(context.Context, domain.Principal, string) (IssuedMachineClient, error)
	Update(context.Context, domain.Principal, string, UpdateMachineClientInput) (MachineClientSummary, error)
	Get(context.Context, domain.Principal, string) (MachineClientSummary, error)
	PatchV1(context.Context, domain.Principal, string, PatchMachineClientInput) (MachineClientSummary, error)
	ListAudit(context.Context, domain.Principal, string, int) ([]MachineAuditEntry, error)
	Activate(context.Context, domain.Principal, string, string, bool) (MachineClientSummary, error)
	SetEnabled(context.Context, domain.Principal, string, bool) (MachineClientSummary, error)
}

// HistoricalMachineImportInput contains only non-secret source facts. A
// migration never accepts old secret hashes, keys, or access tokens.
type HistoricalMachineImportInput struct {
	// ImportRunID identifies the sealed source snapshot batch. It is never a
	// donor Git revision: multiple factual snapshots may share one revision.
	ImportRunID  string
	SourceSystem string
	SourceScope  string
	SourceRowID  string
	// SourceClientID is the donor identifier. It may differ from ClientID only
	// for an explicit frozen compatibility mapping (the legacy direct key).
	SourceClientID          string
	SourceRowDigest         [32]byte
	SourceOwnerScopeDigest  [32]byte
	OwnerScopeMappingStatus string
	ClientID                string
	PrincipalID             string
	PrincipalType           string
	DisplayName             string
	Purpose                 string
	Audiences               []string
	Scopes                  []string
	Capabilities            []string
	AllowedCIDRs            []string
	CorpID                  string
	OwnerScope              domain.OwnerScope
	SourceEnabled           bool
	SourceAuthVersion       int64
	TokenTTLSeconds         int
	ExpiresAt               *time.Time
}

type HistoricalMachineImportResult struct {
	Client     MachineClientSummary
	Outcome    string
	ReasonCode string
	Replayed   bool
}

// HistoricalMachineAuditInput carries a redacted historical audit fact. The
// source before/after payloads never leave the protected source snapshot: the
// target stores their digests together with the original action, target and
// occurrence time so import audit is never confused with a legacy action.
type HistoricalMachineAuditInput struct {
	ImportRunID     string
	SourceSystem    string
	SourceScope     string
	SourceAuditID   int64
	SourceRowDigest [32]byte
	Operator        string
	Action          string
	TargetType      string
	TargetID        string
	BeforeDigest    [32]byte
	AfterDigest     [32]byte
	OccurredAt      time.Time
}

type HistoricalMachineAuditResult struct {
	Outcome  string
	Replayed bool
}

// HistoricalMachineImportBatch seals one protected source snapshot. SourceRevision
// identifies the frozen donor code; ImportRunID identifies this actual source
// snapshot, so later snapshots from the same revision remain possible.
type HistoricalMachineImportBatch struct {
	ImportRunID    string
	SourceSystem   string
	SourceRevision string
	ManifestDigest [32]byte
	SnapshotAt     time.Time
	ClientCount    int
	AuditCount     int
}

type HistoricalMachineImportBatchResult struct{ Replayed bool }

// MachineHistoricalImporter is for an explicit offline migration command. It
// has no secret-returning method and always records disabled/reissue-required
// credentials.
type MachineHistoricalImporter interface {
	ImportHistorical(context.Context, HistoricalMachineImportInput) (HistoricalMachineImportResult, error)
}

// MachineHistoricalRepository is the Access-owned persistence seam for
// idempotent source-row receipts. The command never writes these tables.
type MachineHistoricalRepository interface {
	ImportHistoricalMachineClient(context.Context, HistoricalMachineImportInput, domain.MachineClient) (domain.MachineClient, bool, error)
	RecordHistoricalMachineExclusion(context.Context, HistoricalMachineImportInput, string) (bool, error)
}

// MachineHistoricalVerificationRepository reads an already-written receipt
// without making an import side effect.
type MachineHistoricalVerificationRepository interface {
	VerifyHistoricalMachineClient(context.Context, HistoricalMachineImportInput) (domain.MachineClient, string, string, error)
}

// MachineHistoricalAuditRepository owns immutable legacy-audit mappings. It
// has no write path for a live credential or a provider effect.
type MachineHistoricalAuditRepository interface {
	ImportHistoricalMachineAudit(context.Context, HistoricalMachineAuditInput) (bool, error)
	VerifyHistoricalMachineAudit(context.Context, HistoricalMachineAuditInput) error
}

type MachineHistoricalAuditImporter interface {
	ImportHistoricalAudit(context.Context, HistoricalMachineAuditInput) (HistoricalMachineAuditResult, error)
	VerifyHistoricalAudit(context.Context, HistoricalMachineAuditInput) error
}

type MachineHistoricalBatchRepository interface {
	BeginHistoricalMachineImport(context.Context, HistoricalMachineImportBatch) (bool, error)
	VerifyHistoricalMachineImport(context.Context, HistoricalMachineImportBatch) error
}

type MachineHistoricalBatcher interface {
	BeginHistoricalImport(context.Context, HistoricalMachineImportBatch) (HistoricalMachineImportBatchResult, error)
	VerifyHistoricalImport(context.Context, HistoricalMachineImportBatch) error
}
