package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

const DirectExternalAPIKeyClientID = "direct_external_api_key"

var machineAudiences = map[string]struct{}{
	"external_integration": {},
}

var machineScopes = map[string]struct{}{
	"read":  {},
	"write": {},
}

// This vocabulary is the frozen 03a route inventory. A machine can receive a
// least-privilege subset, but can never receive a human role.
var machineCapabilities = map[string]struct{}{
	"mcp_read":                           {},
	"mcp_execute":                        {},
	"identity_resolve":                   {},
	"external_read":                      {},
	"external_write":                     {},
	"group_broadcast_execute":            {},
	"campaign_draft_create":              {},
	"campaign_status_read":               {},
	"campaign_preparation_create":        {},
	"campaign_preparation_read":          {},
	"campaign_preparation_commit":        {},
	"operation_cycle_report_write":       {},
	"operation_cycle_runner_heartbeat":   {},
	"operation_cycle_action_claim":       {},
	"operation_cycle_action_event_write": {},
	"operation_cycle_context_read":       {},
	"operation_cycle_strategy_propose":   {},
	// These grants occur only in frozen legacy system profiles. No route is
	// added for a capability merely because an inactive historical client
	// retains it.
	"customer_read_limited": {},
	"customer_resolve_read": {},
	"material_create":       {},
	"material_read":         {},
	// V1 native open-platform operation grants. They use the same external
	// integration audience and read/write token scopes as the retained client
	// credentials protocol; they do not introduce a human role or another
	// authorization system.
	"platform.capabilities.read":      {},
	"customer.resolve":                {},
	"customer.read":                   {},
	"customer.list.read":              {},
	"customer.activity.read":          {},
	"ai.review_plan.create":           {},
	"ai.workbench.package.create":     {},
	"operation.read":                  {},
	"order.read":                      {},
	"identity.read":                   {},
	"questionnaire.read":              {},
	"customer.detail.read":            {},
	"radar.click.read":                {},
	"radar.link.read":                 {},
	"chat.read":                       {},
	"audience.product.read":           {},
	"audience.member.read":            {},
	"audience.member.operations.read": {},
	"audience.member.history.read":    {},
	"audience.push.write":             {},
}

// v1ManagedMachineCapabilities is the only capability vocabulary that the
// current administrator control plane may grant. Frozen historical profiles
// are imported through their explicit offline path and retain their source
// facts there; a browser cannot turn those retained facts into a new V1 grant.
var v1ManagedMachineCapabilities = map[string]struct{}{
	"platform.capabilities.read":      {},
	"customer.resolve":                {},
	"customer.read":                   {},
	"customer.list.read":              {},
	"customer.activity.read":          {},
	"ai.review_plan.create":           {},
	"ai.workbench.package.create":     {},
	"operation.read":                  {},
	"order.read":                      {},
	"identity.read":                   {},
	"questionnaire.read":              {},
	"customer.detail.read":            {},
	"radar.click.read":                {},
	"radar.link.read":                 {},
	"chat.read":                       {},
	"audience.product.read":           {},
	"audience.member.read":            {},
	"audience.member.operations.read": {},
	"audience.member.history.read":    {},
	"audience.push.write":             {},
}

type MachineConfig struct {
	SigningKey []byte
	Issuer     string
	CorpID     string
	Now        func() time.Time
}

type MachineService struct {
	repository accessport.MachineRepository
	uow        platformport.UnitOfWork
	passwords  Passwords
	config     MachineConfig
}

// App aliases preserve its public callers while the machine protocol is
// consumed through the stable Access Port.
type CreateMachineClientInput = accessport.CreateMachineClientInput
type IssuedMachineClient = accessport.IssuedMachineClient
type MachineClientSummary = accessport.MachineClientSummary
type ClientCredentialsInput = accessport.ClientCredentialsInput
type IssuedAccessToken = accessport.IssuedAccessToken
type UpdateMachineClientInput = accessport.UpdateMachineClientInput
type PatchMachineClientInput = accessport.PatchMachineClientInput
type MachineAuditEntry = accessport.MachineAuditEntry
type HistoricalMachineImportInput = accessport.HistoricalMachineImportInput
type HistoricalMachineImportResult = accessport.HistoricalMachineImportResult

var _ accessport.MachineTokenIssuer = (*MachineService)(nil)
var _ accessport.MachineManagement = (*MachineService)(nil)

func NewMachineService(repository accessport.MachineRepository, uow platformport.UnitOfWork, passwords Passwords, config MachineConfig) (*MachineService, error) {
	if repository == nil || uow == nil || passwords == nil {
		return nil, errors.New("machine access dependencies are required")
	}
	if config.Issuer == "" {
		config.Issuer = "aicrm-v3"
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &MachineService{repository: repository, uow: uow, passwords: passwords, config: config}, nil
}

func (service *MachineService) Create(ctx context.Context, actor domain.Principal, input CreateMachineClientInput) (IssuedMachineClient, error) {
	if err := requireSuperAdmin(actor); err != nil {
		return IssuedMachineClient{}, err
	}
	if len(service.config.SigningKey) < 32 {
		return IssuedMachineClient{}, domain.ErrMachineIssuerUnready
	}
	client, err := service.newMachineClient(input)
	if err != nil {
		return IssuedMachineClient{}, err
	}
	secret, _, err := credential.IssueOpaque("mc_")
	if err != nil {
		return IssuedMachineClient{}, err
	}
	client.SecretHash, err = service.passwords.Hash(secret)
	if err != nil {
		return IssuedMachineClient{}, err
	}
	client.CredentialHint = machineCredentialHint(secret)
	var created domain.MachineClient
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		created, err = service.repository.CreateMachineClient(txContext, client)
		if err != nil {
			return err
		}
		return service.audit(txContext, created, &actor.InternalID, "machine_client_created", "succeeded")
	})
	if err != nil {
		return IssuedMachineClient{}, err
	}
	return IssuedMachineClient{Client: summarizeMachineClient(created), Secret: secret}, nil
}

// CreateV1 applies the narrow V1 management allowlist before issuing the
// one-time credential. Generic Create remains available for the offline
// historical importer and retained compatibility tests; it is not the V1
// browser control-plane entry point.
func (service *MachineService) CreateV1(ctx context.Context, actor domain.Principal, input CreateMachineClientInput) (IssuedMachineClient, error) {
	if err := requireSuperAdmin(actor); err != nil {
		return IssuedMachineClient{}, err
	}
	candidate, err := service.newMachineClient(input)
	if err != nil {
		return IssuedMachineClient{}, err
	}
	if err = validateV1ManagedMachineClient(candidate); err != nil {
		return IssuedMachineClient{}, err
	}
	return service.Create(ctx, actor, input)
}

func (service *MachineService) List(ctx context.Context, actor domain.Principal) ([]MachineClientSummary, error) {
	if err := requireSuperAdmin(actor); err != nil {
		return nil, err
	}
	var clients []domain.MachineClient
	err := service.uow.Within(ctx, func(txContext context.Context) error {
		var listErr error
		clients, listErr = service.repository.ListMachineClients(txContext)
		return listErr
	})
	if err != nil {
		return nil, err
	}
	result := make([]MachineClientSummary, 0, len(clients))
	for _, client := range clients {
		result = append(result, summarizeMachineClient(client))
	}
	return result, nil
}

// Get returns the current non-secret control-plane view of one caller.
func (service *MachineService) Get(ctx context.Context, actor domain.Principal, clientID string) (MachineClientSummary, error) {
	if err := requireSuperAdmin(actor); err != nil {
		return MachineClientSummary{}, err
	}
	clientID, err := domain.NormalizeMachineClientID(clientID)
	if err != nil {
		return MachineClientSummary{}, err
	}
	var client domain.MachineClient
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		var lookupErr error
		client, lookupErr = service.repository.MachineClientByID(txContext, clientID, false)
		return lookupErr
	})
	if err != nil {
		return MachineClientSummary{}, err
	}
	return summarizeMachineClient(client), nil
}

// ListAudit reads the Access-owned, client-scoped audit history. The lookup
// deliberately happens in the same transaction so a missing caller cannot be
// indistinguishable from an empty audit stream.
func (service *MachineService) ListAudit(ctx context.Context, actor domain.Principal, clientID string, limit int) ([]MachineAuditEntry, error) {
	if err := requireSuperAdmin(actor); err != nil {
		return nil, err
	}
	clientID, err := domain.NormalizeMachineClientID(clientID)
	if err != nil || limit < 1 || limit > 100 {
		return nil, domain.ErrInvalidInput
	}
	var entries []MachineAuditEntry
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		client, lookupErr := service.repository.MachineClientByID(txContext, clientID, false)
		if lookupErr != nil {
			return lookupErr
		}
		var listErr error
		entries, listErr = service.repository.ListMachineAudit(txContext, client.ID, limit)
		return listErr
	})
	return entries, err
}

// Rotate returns a newly generated secret exactly once and advances
// auth_version so both old opaque credentials and issued JWTs stop working.
func (service *MachineService) Rotate(ctx context.Context, actor domain.Principal, clientID string) (IssuedMachineClient, error) {
	if err := requireSuperAdmin(actor); err != nil {
		return IssuedMachineClient{}, err
	}
	clientID, err := domain.NormalizeMachineClientID(clientID)
	if err != nil {
		return IssuedMachineClient{}, err
	}
	secret, _, err := credential.IssueOpaque("mc_")
	if err != nil {
		return IssuedMachineClient{}, err
	}
	secretHash, err := service.passwords.Hash(secret)
	if err != nil {
		return IssuedMachineClient{}, err
	}
	var updated domain.MachineClient
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		client, lookupErr := service.repository.MachineClientByID(txContext, clientID, true)
		if lookupErr != nil {
			return lookupErr
		}
		client.SecretHash = secretHash
		client.CredentialHint = machineCredentialHint(secret)
		client.ReissueRequired = false
		client.Enabled = client.Purpose == "direct_api_key"
		client.AuthVersion++
		if replaceErr := service.repository.ReplaceMachineClient(txContext, client); replaceErr != nil {
			return replaceErr
		}
		updated = client
		return service.audit(txContext, updated, &actor.InternalID, "machine_client_rotated", "succeeded")
	})
	if err != nil {
		return IssuedMachineClient{}, err
	}
	return IssuedMachineClient{Client: summarizeMachineClient(updated), Secret: secret}, nil
}

func (service *MachineService) SetEnabled(ctx context.Context, actor domain.Principal, clientID string, enabled bool) (MachineClientSummary, error) {
	if err := requireSuperAdmin(actor); err != nil {
		return MachineClientSummary{}, err
	}
	clientID, err := domain.NormalizeMachineClientID(clientID)
	if err != nil {
		return MachineClientSummary{}, err
	}
	var updated domain.MachineClient
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		client, lookupErr := service.repository.MachineClientByID(txContext, clientID, true)
		if lookupErr != nil {
			return lookupErr
		}
		if enabled && (client.ReissueRequired || client.Purpose != "direct_api_key") {
			return domain.ErrMachineActivation
		}
		if client.Enabled != enabled {
			client.Enabled = enabled
			client.AuthVersion++
			if replaceErr := service.repository.ReplaceMachineClient(txContext, client); replaceErr != nil {
				return replaceErr
			}
		}
		updated = client
		return service.audit(txContext, updated, &actor.InternalID, "machine_client_enabled", map[bool]string{true: "enabled", false: "disabled"}[enabled])
	})
	if err != nil {
		return MachineClientSummary{}, err
	}
	return summarizeMachineClient(updated), nil
}

// Update preserves the frozen API-client permission template. An active
// client is never edited in place: callers must disable it, update its local
// boundary, then deliberately activate with the one-time secret self-check.
func (service *MachineService) Update(ctx context.Context, actor domain.Principal, clientID string, input UpdateMachineClientInput) (MachineClientSummary, error) {
	if err := requireSuperAdmin(actor); err != nil {
		return MachineClientSummary{}, err
	}
	clientID, err := domain.NormalizeMachineClientID(clientID)
	if err != nil || strings.TrimSpace(input.DisplayName) == "" || len(strings.TrimSpace(input.DisplayName)) > 160 || input.TokenTTLSeconds < 60 || input.TokenTTLSeconds > 3600 {
		return MachineClientSummary{}, domain.ErrInvalidInput
	}
	cidrs, err := domain.NormalizeCIDRs(input.AllowedCIDRs)
	if err != nil {
		return MachineClientSummary{}, err
	}
	var updated domain.MachineClient
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		client, lookupErr := service.repository.MachineClientByID(txContext, clientID, true)
		if lookupErr != nil {
			return lookupErr
		}
		if client.Purpose == "direct_api_key" || client.Enabled {
			return domain.ErrMachineClientActive
		}
		client.DisplayName, client.TokenTTLSeconds, client.AllowedCIDRs = strings.TrimSpace(input.DisplayName), input.TokenTTLSeconds, cidrs
		client.AuthVersion++
		if replaceErr := service.repository.ReplaceMachineClient(txContext, client); replaceErr != nil {
			return replaceErr
		}
		updated = client
		return service.audit(txContext, updated, &actor.InternalID, "machine_client_updated", "succeeded")
	})
	if err != nil {
		return MachineClientSummary{}, err
	}
	return summarizeMachineClient(updated), nil
}

// PatchV1 applies a presence-aware partial update to a V1-managed caller.
// It permits enabled callers to change grants because incrementing auth_version
// in the same transaction invalidates all prior bearer tokens immediately.
func (service *MachineService) PatchV1(ctx context.Context, actor domain.Principal, clientID string, input PatchMachineClientInput) (MachineClientSummary, error) {
	if err := requireSuperAdmin(actor); err != nil {
		return MachineClientSummary{}, err
	}
	clientID, err := domain.NormalizeMachineClientID(clientID)
	if err != nil {
		return MachineClientSummary{}, err
	}
	var updated domain.MachineClient
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		client, lookupErr := service.repository.MachineClientByID(txContext, clientID, true)
		if lookupErr != nil {
			return lookupErr
		}
		next, changed, patchErr := service.patchV1ManagedMachineClient(client, input)
		if patchErr != nil {
			return patchErr
		}
		if changed {
			next.AuthVersion++
			if replaceErr := service.repository.ReplaceMachineClient(txContext, next); replaceErr != nil {
				return replaceErr
			}
			updated = next
			return service.audit(txContext, updated, &actor.InternalID, "machine_client_grants_updated", "revoked_prior_bearers")
		}
		updated = client
		return service.audit(txContext, updated, &actor.InternalID, "machine_client_grants_updated", "unchanged")
	})
	if err != nil {
		return MachineClientSummary{}, err
	}
	return summarizeMachineClient(updated), nil
}

func (service *MachineService) patchV1ManagedMachineClient(client domain.MachineClient, input PatchMachineClientInput) (domain.MachineClient, bool, error) {
	next := client
	if input.DisplayName != nil {
		next.DisplayName = strings.TrimSpace(*input.DisplayName)
	}
	if input.Audiences != nil {
		audiences, err := domain.NormalizeMachineStrings(*input.Audiences, machineAudiences)
		if err != nil {
			return domain.MachineClient{}, false, err
		}
		next.Audiences = audiences
	}
	if input.Scopes != nil {
		scopes, err := domain.NormalizeMachineStrings(*input.Scopes, machineScopes)
		if err != nil {
			return domain.MachineClient{}, false, err
		}
		next.Scopes = scopes
	}
	if input.Capabilities != nil {
		capabilities, err := domain.NormalizeMachineStrings(*input.Capabilities, v1ManagedMachineCapabilities)
		if err != nil {
			return domain.MachineClient{}, false, err
		}
		next.Capabilities = capabilities
	}
	if input.AllowedCIDRs != nil {
		cidrs, err := domain.NormalizeCIDRs(*input.AllowedCIDRs)
		if err != nil {
			return domain.MachineClient{}, false, err
		}
		next.AllowedCIDRs = cidrs
	}
	if input.TokenTTLSeconds != nil {
		next.TokenTTLSeconds = *input.TokenTTLSeconds
	}
	if input.OwnerScopeSet {
		ownerScope, err := domain.NormalizeOwnerScope(input.OwnerScope.JSON())
		if err != nil {
			return domain.MachineClient{}, false, err
		}
		next.OwnerScope = ownerScope
	}
	if input.ExpiresAtSet {
		if input.ExpiresAt != nil && !input.ExpiresAt.After(service.config.Now().UTC()) {
			return domain.MachineClient{}, false, domain.ErrInvalidInput
		}
		if input.ExpiresAt == nil {
			next.ExpiresAt = nil
		} else {
			expiresAt := input.ExpiresAt.UTC()
			next.ExpiresAt = &expiresAt
		}
	}
	if err := validateV1ManagedMachineClient(next); err != nil {
		return domain.MachineClient{}, false, err
	}
	changed := next.DisplayName != client.DisplayName || !equalMachineStrings(next.Audiences, client.Audiences) || !equalMachineStrings(next.Scopes, client.Scopes) || !equalMachineStrings(next.Capabilities, client.Capabilities) || !equalMachineStrings(next.AllowedCIDRs, client.AllowedCIDRs) || string(next.OwnerScope.JSON()) != string(client.OwnerScope.JSON()) || next.TokenTTLSeconds != client.TokenTTLSeconds || !sameOptionalMachineTime(next.ExpiresAt, client.ExpiresAt)
	return next, changed, nil
}

func validateV1ManagedMachineClient(client domain.MachineClient) error {
	if client.Purpose != "external_agent" || strings.TrimSpace(client.DisplayName) == "" || len(client.DisplayName) > 160 || client.TokenTTLSeconds < 60 || client.TokenTTLSeconds > 3600 {
		return domain.ErrInvalidInput
	}
	if !equalMachineStrings(client.Audiences, []string{"external_integration"}) {
		return domain.ErrInvalidInput
	}
	if _, err := domain.NormalizeMachineStrings(client.Scopes, machineScopes); err != nil {
		return err
	}
	if _, err := domain.NormalizeMachineStrings(client.Capabilities, v1ManagedMachineCapabilities); err != nil {
		return err
	}
	return nil
}

func sameOptionalMachineTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

// Activate verifies the just-copied one-time secret before moving a regular
// API client from its disabled handoff state to active service.
func (service *MachineService) Activate(ctx context.Context, actor domain.Principal, clientID, secret string, copiedConfirmed bool) (MachineClientSummary, error) {
	if err := requireSuperAdmin(actor); err != nil {
		return MachineClientSummary{}, err
	}
	clientID, err := domain.NormalizeMachineClientID(clientID)
	if err != nil || !copiedConfirmed || strings.TrimSpace(secret) == "" {
		return MachineClientSummary{}, domain.ErrMachineActivation
	}
	var updated domain.MachineClient
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		client, lookupErr := service.repository.MachineClientByID(txContext, clientID, true)
		if lookupErr != nil {
			return lookupErr
		}
		if client.Purpose == "direct_api_key" || client.Enabled || client.ReissueRequired || !service.passwords.Verify(secret, client.SecretHash) {
			return domain.ErrMachineActivation
		}
		client.Enabled = true
		client.AuthVersion++
		if replaceErr := service.repository.ReplaceMachineClient(txContext, client); replaceErr != nil {
			return replaceErr
		}
		updated = client
		return service.audit(txContext, updated, &actor.InternalID, "machine_client_activated", "succeeded")
	})
	if err != nil {
		return MachineClientSummary{}, err
	}
	return summarizeMachineClient(updated), nil
}

func (service *MachineService) IssueClientCredentialsToken(ctx context.Context, input ClientCredentialsInput) (IssuedAccessToken, error) {
	clientID, err := domain.NormalizeMachineClientID(input.ClientID)
	if err != nil || strings.TrimSpace(input.ClientSecret) == "" || !input.SourceIP.IsValid() {
		return IssuedAccessToken{}, domain.ErrMachineCredential
	}
	audience := strings.TrimSpace(input.Audience)
	if _, exists := machineAudiences[audience]; !exists {
		return IssuedAccessToken{}, domain.ErrMachineAudience
	}
	var issued IssuedAccessToken
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		client, lookupErr := service.repository.MachineClientByID(txContext, clientID, true)
		if lookupErr != nil || !service.passwords.Verify(input.ClientSecret, client.SecretHash) {
			return domain.ErrMachineCredential
		}
		grantedScopes := input.RequestedScopes
		if len(grantedScopes) == 0 {
			grantedScopes = client.Scopes
		}
		if err := service.authorizeClient(client, audience, grantedScopes, input.SourceIP); err != nil {
			return err
		}
		accessToken, tokenErr := service.sign(client, audience, grantedScopes)
		if tokenErr != nil {
			return tokenErr
		}
		now := service.config.Now().UTC()
		if updateErr := service.repository.SetMachineClientLastUsed(txContext, client.ID, now); updateErr != nil {
			return updateErr
		}
		if auditErr := service.audit(txContext, client, nil, "machine_token_issued", "succeeded"); auditErr != nil {
			return auditErr
		}
		issued = IssuedAccessToken{AccessToken: accessToken, TokenType: "Bearer", ExpiresIn: client.TokenTTLSeconds, Scope: strings.Join(grantedScopes, " ")}
		return nil
	})
	return issued, err
}

// AuthenticateBearer recognizes either an issued JWT or the legacy direct
// key. Only the fixed direct key client may authenticate as a raw Bearer key.
func (service *MachineService) AuthenticateBearer(ctx context.Context, bearer, audience string, source netip.Addr) (domain.MachinePrincipal, error) {
	if strings.Count(bearer, ".") == 2 {
		return service.authenticateJWT(ctx, bearer, audience, source)
	}
	return service.authenticateDirectKey(ctx, bearer, audience, source)
}

func (service *MachineService) authenticateDirectKey(ctx context.Context, secret, audience string, source netip.Addr) (domain.MachinePrincipal, error) {
	if strings.TrimSpace(secret) == "" || audience != "external_integration" || !source.IsValid() {
		return domain.MachinePrincipal{}, domain.ErrMachineCredential
	}
	var principal domain.MachinePrincipal
	err := service.uow.Within(ctx, func(txContext context.Context) error {
		client, err := service.repository.MachineClientByID(txContext, DirectExternalAPIKeyClientID, true)
		if err != nil || !service.passwords.Verify(secret, client.SecretHash) {
			return domain.ErrMachineCredential
		}
		if client.Purpose != "direct_api_key" {
			return domain.ErrMachineCredential
		}
		if err := service.authorizeClient(client, audience, []string{"read"}, source); err != nil {
			return err
		}
		if !containsMachine(client.Capabilities, "external_read") || len(client.Capabilities) != 1 {
			return domain.ErrMachineCredential
		}
		if err := service.repository.SetMachineClientLastUsed(txContext, client.ID, service.config.Now().UTC()); err != nil {
			return err
		}
		if err := service.audit(txContext, client, nil, "direct_key_authenticated", "succeeded"); err != nil {
			return err
		}
		principal = principalFrom(client, audience, []string{"read"}, true)
		return nil
	})
	return principal, err
}

func (service *MachineService) authenticateJWT(ctx context.Context, token, audience string, source netip.Addr) (domain.MachinePrincipal, error) {
	claims, err := service.verify(token)
	if err != nil || claims.Audience != audience || !source.IsValid() {
		return domain.MachinePrincipal{}, domain.ErrMachineCredential
	}
	var principal domain.MachinePrincipal
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		client, lookupErr := service.repository.MachineClientByID(txContext, claims.ClientID, true)
		if lookupErr != nil {
			return domain.ErrMachineCredential
		}
		if client.Purpose == "direct_api_key" {
			return domain.ErrMachineCredential
		}
		if authorizeErr := service.authorizeClient(client, audience, claims.Scopes, source); authorizeErr != nil {
			return authorizeErr
		}
		if client.AuthVersion != claims.AuthVersion {
			return domain.ErrMachineCredential
		}
		if updateErr := service.repository.SetMachineClientLastUsed(txContext, client.ID, service.config.Now().UTC()); updateErr != nil {
			return updateErr
		}
		principal = principalFrom(client, audience, claims.Scopes, false)
		return nil
	})
	return principal, err
}

func (service *MachineService) authorizeClient(client domain.MachineClient, audience string, requestedScopes []string, source netip.Addr) error {
	now := service.config.Now().UTC()
	if !client.Enabled {
		return domain.ErrMachineClientDisabled
	}
	if client.ReissueRequired {
		return domain.ErrMachineReissueRequired
	}
	if client.ExpiresAt != nil && !now.Before(*client.ExpiresAt) {
		return domain.ErrMachineClientExpired
	}
	if !containsMachine(client.Audiences, audience) {
		return domain.ErrMachineAudience
	}
	if len(requestedScopes) == 0 {
		requestedScopes = client.Scopes
	}
	for _, scope := range requestedScopes {
		if !containsMachine(client.Scopes, scope) {
			return domain.ErrMachineScope
		}
	}
	if !domain.MachineSourceAllowed(client.AllowedCIDRs, source) {
		return domain.ErrMachineSourceIP
	}
	return nil
}

func (service *MachineService) newMachineClient(input CreateMachineClientInput) (domain.MachineClient, error) {
	clientID, err := domain.NormalizeMachineClientID(input.ClientID)
	if err != nil || strings.TrimSpace(input.DisplayName) == "" || len(strings.TrimSpace(input.DisplayName)) > 160 {
		return domain.MachineClient{}, domain.ErrInvalidInput
	}
	audiences, err := domain.NormalizeMachineStrings(input.Audiences, machineAudiences)
	if err != nil {
		return domain.MachineClient{}, err
	}
	scopes, err := domain.NormalizeMachineStrings(input.Scopes, machineScopes)
	if err != nil {
		return domain.MachineClient{}, err
	}
	capabilities, err := domain.NormalizeMachineStrings(input.Capabilities, machineCapabilities)
	if err != nil {
		return domain.MachineClient{}, err
	}
	cidrs, err := domain.NormalizeCIDRs(input.AllowedCIDRs)
	if err != nil {
		return domain.MachineClient{}, err
	}
	if input.TokenTTLSeconds == 0 {
		input.TokenTTLSeconds = 1800
	}
	if input.TokenTTLSeconds < 60 || input.TokenTTLSeconds > 3600 || (input.ExpiresAt != nil && !input.ExpiresAt.After(service.config.Now().UTC())) {
		return domain.MachineClient{}, domain.ErrInvalidInput
	}
	purpose := strings.TrimSpace(input.Purpose)
	if !domain.IsMachinePurpose(purpose) {
		return domain.MachineClient{}, domain.ErrInvalidInput
	}
	if profile, system := domain.SystemMachineProfileForPurpose(purpose); system &&
		(!equalMachineStrings(audiences, profile.Audiences) || !equalMachineStrings(scopes, profile.Scopes) || !equalMachineStrings(capabilities, profile.Capabilities)) {
		return domain.MachineClient{}, domain.ErrInvalidInput
	}
	ownerScope, err := domain.NormalizeOwnerScope(input.OwnerScope.JSON())
	if err != nil {
		return domain.MachineClient{}, err
	}
	corpID := strings.TrimSpace(service.config.CorpID)
	if len(corpID) > 256 || strings.IndexFunc(corpID, unicode.IsControl) >= 0 {
		return domain.MachineClient{}, domain.ErrInvalidInput
	}
	client := domain.MachineClient{ClientID: clientID, DisplayName: strings.TrimSpace(input.DisplayName), Purpose: purpose,
		Audiences: audiences, Scopes: scopes, Capabilities: capabilities, AllowedCIDRs: cidrs,
		CorpID: corpID, OwnerScope: ownerScope, TokenTTLSeconds: input.TokenTTLSeconds, ExpiresAt: input.ExpiresAt, Enabled: purpose == "direct_api_key", AuthVersion: 1}
	if purpose == "direct_api_key" && (clientID != DirectExternalAPIKeyClientID || !equalMachineStrings(audiences, []string{"external_integration"}) || !equalMachineStrings(scopes, []string{"read"}) || !equalMachineStrings(capabilities, []string{"external_read"})) {
		return domain.MachineClient{}, domain.ErrInvalidInput
	}
	return client, nil
}

func (service *MachineService) sign(client domain.MachineClient, audience string, requestedScopes []string) (string, error) {
	if len(service.config.SigningKey) < 32 {
		return "", domain.ErrMachineIssuerUnready
	}
	if len(requestedScopes) == 0 {
		requestedScopes = client.Scopes
	}
	now := service.config.Now().UTC()
	claims := machineJWTClaims{Issuer: service.config.Issuer, Subject: "machine:" + client.ClientID, Audience: audience,
		ClientID: client.ClientID, AuthVersion: client.AuthVersion, Scopes: requestedScopes,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Duration(client.TokenTTLSeconds) * time.Second).Unix()}
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := encodedHeader + "." + encodedPayload
	mac := hmac.New(sha256.New, service.config.SigningKey)
	_, _ = mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (service *MachineService) verify(token string) (machineJWTClaims, error) {
	if len(service.config.SigningKey) < 32 {
		return machineJWTClaims{}, domain.ErrMachineIssuerUnready
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(parts[0]) == 0 || len(parts[1]) == 0 || len(parts[2]) == 0 {
		return machineJWTClaims{}, errors.New("invalid jwt")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return machineJWTClaims{}, err
	}
	var header struct {
		Algorithm string `json:"alg"`
	}
	if err = json.Unmarshal(headerBytes, &header); err != nil || header.Algorithm != "HS256" {
		return machineJWTClaims{}, errors.New("invalid jwt algorithm")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size {
		return machineJWTClaims{}, errors.New("invalid jwt signature")
	}
	mac := hmac.New(sha256.New, service.config.SigningKey)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if subtle.ConstantTimeCompare(signature, mac.Sum(nil)) != 1 {
		return machineJWTClaims{}, errors.New("invalid jwt signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return machineJWTClaims{}, err
	}
	var claims machineJWTClaims
	if err = json.Unmarshal(payload, &claims); err != nil || claims.Issuer != service.config.Issuer || claims.ClientID == "" || claims.AuthVersion < 1 || claims.ExpiresAt <= service.config.Now().UTC().Unix() || claims.IssuedAt > service.config.Now().UTC().Add(time.Minute).Unix() || claims.Subject != "machine:"+claims.ClientID {
		return machineJWTClaims{}, errors.New("invalid jwt claims")
	}
	if _, err = domain.NormalizeMachineStrings(claims.Scopes, machineScopes); err != nil {
		return machineJWTClaims{}, errors.New("invalid jwt scopes")
	}
	return claims, nil
}

type machineJWTClaims struct {
	Issuer      string   `json:"iss"`
	Subject     string   `json:"sub"`
	Audience    string   `json:"aud"`
	ClientID    string   `json:"client_id"`
	AuthVersion int64    `json:"auth_version"`
	Scopes      []string `json:"scopes"`
	IssuedAt    int64    `json:"iat"`
	ExpiresAt   int64    `json:"exp"`
}

func principalFrom(client domain.MachineClient, audience string, scopes []string, direct bool) domain.MachinePrincipal {
	return domain.MachinePrincipal{ClientID: client.ClientID, ClientRecord: client.ID, Audience: audience,
		Scopes: append([]string(nil), scopes...), Capabilities: append([]string(nil), client.Capabilities...), CorpID: client.CorpID, OwnerScope: cloneOwnerScope(client.OwnerScope), AuthVersion: client.AuthVersion, DirectKey: direct}
}

func summarizeMachineClient(client domain.MachineClient) MachineClientSummary {
	return MachineClientSummary{ClientID: client.ClientID, DisplayName: client.DisplayName, Purpose: client.Purpose,
		CredentialHint: client.CredentialHint, Audiences: append([]string(nil), client.Audiences...),
		Scopes: append([]string(nil), client.Scopes...), Capabilities: append([]string(nil), client.Capabilities...),
		AllowedCIDRs: append([]string{}, client.AllowedCIDRs...), TokenTTLSeconds: client.TokenTTLSeconds,
		CorpID: client.CorpID, OwnerScope: cloneOwnerScope(client.OwnerScope),
		ExpiresAt: client.ExpiresAt, Enabled: client.Enabled, ReissueRequired: client.ReissueRequired,
		AuthVersion: client.AuthVersion, LastUsedAt: client.LastUsedAt, CreatedAt: client.CreatedAt}
}

func cloneOwnerScope(scope domain.OwnerScope) domain.OwnerScope {
	result := make(domain.OwnerScope, len(scope))
	for key, values := range scope {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func (service *MachineService) audit(ctx context.Context, client domain.MachineClient, actor *int64, action, outcome string) error {
	return service.repository.AppendMachineAudit(ctx, domain.MachineAudit{MachineClientID: client.ID, ActorAdminID: actor,
		Action: action, Outcome: outcome, Details: []byte(`{}`), CreatedAt: service.config.Now().UTC()})
}

func machineCredentialHint(secret string) string {
	if len(secret) < 10 {
		return "mc_••••"
	}
	return secret[:3] + "••••" + secret[len(secret)-6:]
}

func containsMachine(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func equalMachineStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
