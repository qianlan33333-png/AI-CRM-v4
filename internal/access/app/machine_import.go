package app

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
)

var _ accessport.MachineHistoricalImporter = (*MachineService)(nil)
var _ accessport.MachineHistoricalAuditImporter = (*MachineService)(nil)
var _ accessport.MachineHistoricalBatcher = (*MachineService)(nil)

// BeginHistoricalImport creates or replays the sealed source-snapshot receipt.
// It has no credential side effect and must run before individual source rows.
func (service *MachineService) BeginHistoricalImport(ctx context.Context, batch accessport.HistoricalMachineImportBatch) (accessport.HistoricalMachineImportBatchResult, error) {
	if err := validateHistoricalMachineBatch(batch); err != nil {
		return accessport.HistoricalMachineImportBatchResult{}, err
	}
	repository, ok := service.repository.(accessport.MachineHistoricalBatchRepository)
	if !ok {
		return accessport.HistoricalMachineImportBatchResult{}, errors.New("machine historical batch repository is not configured")
	}
	var replayed bool
	err := service.uow.Within(ctx, func(txContext context.Context) error {
		var beginErr error
		replayed, beginErr = repository.BeginHistoricalMachineImport(txContext, batch)
		return beginErr
	})
	if err != nil {
		return accessport.HistoricalMachineImportBatchResult{}, err
	}
	return accessport.HistoricalMachineImportBatchResult{Replayed: replayed}, nil
}

func (service *MachineService) VerifyHistoricalImport(ctx context.Context, batch accessport.HistoricalMachineImportBatch) error {
	if err := validateHistoricalMachineBatch(batch); err != nil {
		return err
	}
	repository, ok := service.repository.(accessport.MachineHistoricalBatchRepository)
	if !ok {
		return errors.New("machine historical batch repository is not configured")
	}
	return service.uow.Within(ctx, func(txContext context.Context) error {
		return repository.VerifyHistoricalMachineImport(txContext, batch)
	})
}

// ImportHistorical creates an intentionally unusable replacement record for a
// legacy caller. Its source grants are preserved exactly when V3 supports the
// same subset. There is never a purpose-template expansion: unsupported source
// records receive a durable excluded receipt instead of a broader client.
func (service *MachineService) ImportHistorical(ctx context.Context, input accessport.HistoricalMachineImportInput) (accessport.HistoricalMachineImportResult, error) {
	if err := validateHistoricalMachineImport(input); err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	repository, ok := service.repository.(accessport.MachineHistoricalRepository)
	if !ok {
		return accessport.HistoricalMachineImportResult{}, errors.New("machine historical repository is not configured")
	}
	if reason := service.historicalMachineExclusionReason(input); reason != "" {
		return service.excludeHistoricalMachine(ctx, repository, input, reason)
	}
	// Active client creation rejects past expiry, while history must retain an
	// already-expired caller as an inert reissue-required record.
	expiresAt := input.ExpiresAt
	createExpiresAt := expiresAt
	if createExpiresAt != nil && !createExpiresAt.After(service.config.Now().UTC()) {
		createExpiresAt = nil
	}
	client, err := service.newMachineClient(accessport.CreateMachineClientInput{
		ClientID: input.ClientID, DisplayName: input.DisplayName, Purpose: input.Purpose,
		Audiences: input.Audiences, Scopes: input.Scopes, Capabilities: input.Capabilities,
		AllowedCIDRs: input.AllowedCIDRs, OwnerScope: input.OwnerScope, TokenTTLSeconds: input.TokenTTLSeconds, ExpiresAt: createExpiresAt,
	})
	if err != nil {
		return service.excludeHistoricalMachine(ctx, repository, input, "unsupported_source_grant")
	}
	client.CorpID = strings.TrimSpace(input.CorpID)
	entropy := make([]byte, 32)
	if _, err = rand.Read(entropy); err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	client.SecretHash, err = service.passwords.Hash(credentialOpaqueImportInput(entropy))
	if err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	client.CredentialHint = "mc_••••reissue"
	client.ExpiresAt = expiresAt
	client.Enabled = false
	client.ReissueRequired = true
	client.AuthVersion = 1

	var imported domain.MachineClient
	var replayed bool
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		var importErr error
		imported, replayed, importErr = repository.ImportHistoricalMachineClient(txContext, input, client)
		if importErr != nil || replayed {
			return importErr
		}
		return service.audit(txContext, imported, nil, "machine_client_imported", "reissue_required")
	})
	if errors.Is(err, domain.ErrConflict) {
		// A target client established outside this source snapshot must never be
		// overwritten or silently treated as the historical record.
		return service.excludeHistoricalMachine(ctx, repository, input, "target_client_id_conflict")
	}
	if err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	return accessport.HistoricalMachineImportResult{Client: summarizeMachineClient(imported), Outcome: map[bool]string{true: "replayed", false: "reissue_required"}[replayed], Replayed: replayed}, nil
}

func (service *MachineService) excludeHistoricalMachine(ctx context.Context, repository accessport.MachineHistoricalRepository, input accessport.HistoricalMachineImportInput, reason string) (accessport.HistoricalMachineImportResult, error) {
	var replayed bool
	err := service.uow.Within(ctx, func(txContext context.Context) error {
		var excludeErr error
		replayed, excludeErr = repository.RecordHistoricalMachineExclusion(txContext, input, reason)
		return excludeErr
	})
	if err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	outcome := "excluded"
	if replayed {
		outcome = "replayed"
	}
	return accessport.HistoricalMachineImportResult{Outcome: outcome, ReasonCode: reason, Replayed: replayed}, nil
}

// VerifyHistorical confirms a prior receipt without inserting a client or
// audit record. It also verifies excluded records, so a source row cannot
// disappear from reconciliation merely because V3 cannot safely host it.
func (service *MachineService) VerifyHistorical(ctx context.Context, input accessport.HistoricalMachineImportInput) (accessport.HistoricalMachineImportResult, error) {
	if err := validateHistoricalMachineImport(input); err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	repository, ok := service.repository.(accessport.MachineHistoricalVerificationRepository)
	if !ok {
		return accessport.HistoricalMachineImportResult{}, errors.New("machine historical verification repository is not configured")
	}
	var stored domain.MachineClient
	var outcome, reason string
	err := service.uow.Within(ctx, func(txContext context.Context) error {
		var verifyErr error
		stored, outcome, reason, verifyErr = repository.VerifyHistoricalMachineClient(txContext, input)
		return verifyErr
	})
	if err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	if outcome == "excluded" {
		expectedReason := service.historicalMachineExclusionReason(input)
		if expectedReason == "" {
			// These two reasons are produced only after this exact source row has
			// passed frozen-grant validation. They preserve a collision or a V3
			// validation incompatibility without treating an arbitrary receipt
			// mutation as a successful reconciliation.
			switch reason {
			case "target_client_id_conflict", "unsupported_source_grant":
				expectedReason = reason
			}
		}
		if expectedReason == "" || reason != expectedReason {
			return accessport.HistoricalMachineImportResult{}, domain.ErrConflict
		}
		return accessport.HistoricalMachineImportResult{Outcome: "excluded", ReasonCode: reason}, nil
	}
	if outcome != "reissue_required" || reason != "" || stored.ClientID != input.ClientID || stored.DisplayName != strings.TrimSpace(input.DisplayName) || stored.Purpose != strings.TrimSpace(input.Purpose) || stored.Enabled || !stored.ReissueRequired || stored.TokenTTLSeconds != input.TokenTTLSeconds || stored.CorpID != strings.TrimSpace(input.CorpID) || !equalMachineStrings(stored.Audiences, input.Audiences) || !equalMachineStrings(stored.Scopes, input.Scopes) || !equalMachineStrings(stored.Capabilities, input.Capabilities) || !equalHistoricalCIDRs(stored.AllowedCIDRs, input.AllowedCIDRs) || string(stored.OwnerScope.JSON()) != string(input.OwnerScope.JSON()) || !equalMachineExpiry(stored.ExpiresAt, input.ExpiresAt) {
		return accessport.HistoricalMachineImportResult{}, domain.ErrConflict
	}
	return accessport.HistoricalMachineImportResult{Client: summarizeMachineClient(stored), Outcome: "reissue_required"}, nil
}

// ImportHistoricalAudit persists a source audit fact separately from the V3
// import audit. The only payload retained is the source before/after digest;
// protected source contents such as owner scope never become a new audit data
// store or a credential recovery channel.
func (service *MachineService) ImportHistoricalAudit(ctx context.Context, input accessport.HistoricalMachineAuditInput) (accessport.HistoricalMachineAuditResult, error) {
	if err := validateHistoricalMachineAudit(input); err != nil {
		return accessport.HistoricalMachineAuditResult{}, err
	}
	repository, ok := service.repository.(accessport.MachineHistoricalAuditRepository)
	if !ok {
		return accessport.HistoricalMachineAuditResult{}, errors.New("machine historical audit repository is not configured")
	}
	var replayed bool
	err := service.uow.Within(ctx, func(txContext context.Context) error {
		var importErr error
		replayed, importErr = repository.ImportHistoricalMachineAudit(txContext, input)
		return importErr
	})
	if err != nil {
		return accessport.HistoricalMachineAuditResult{}, err
	}
	return accessport.HistoricalMachineAuditResult{Outcome: map[bool]string{true: "replayed", false: "imported"}[replayed], Replayed: replayed}, nil
}

func (service *MachineService) VerifyHistoricalAudit(ctx context.Context, input accessport.HistoricalMachineAuditInput) error {
	if err := validateHistoricalMachineAudit(input); err != nil {
		return err
	}
	repository, ok := service.repository.(accessport.MachineHistoricalAuditRepository)
	if !ok {
		return errors.New("machine historical audit repository is not configured")
	}
	return service.uow.Within(ctx, func(txContext context.Context) error {
		return repository.VerifyHistoricalMachineAudit(txContext, input)
	})
}

func equalMachineExpiry(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.UTC().Equal(right.UTC())
}

func equalHistoricalCIDRs(left, right []string) bool {
	normalized, err := domain.NormalizeCIDRs(right)
	return err == nil && equalMachineStrings(left, normalized)
}

func validateHistoricalMachineBatch(batch accessport.HistoricalMachineImportBatch) error {
	if !historicalImportRunID(strings.TrimSpace(batch.ImportRunID)) || batch.SourceSystem != "ai-crm" || len(batch.SourceRevision) != 40 || batch.SnapshotAt.IsZero() || batch.ClientCount < 0 || batch.AuditCount < 0 || allZeroDigest(batch.ManifestDigest) {
		return domain.ErrInvalidInput
	}
	for _, value := range batch.SourceRevision {
		if !(value >= 'a' && value <= 'f') && !(value >= '0' && value <= '9') {
			return domain.ErrInvalidInput
		}
	}
	return nil
}

func validateHistoricalMachineImport(input accessport.HistoricalMachineImportInput) error {
	if !historicalImportRunID(strings.TrimSpace(input.ImportRunID)) || input.SourceSystem != "ai-crm" || !historicalSourceScope(input.SourceScope) || len(strings.TrimSpace(input.SourceRowID)) < 1 || len(strings.TrimSpace(input.SourceRowID)) > 240 || len(strings.TrimSpace(input.SourceClientID)) < 1 || len(strings.TrimSpace(input.SourceClientID)) > 120 {
		return domain.ErrInvalidInput
	}
	if allZeroDigest(input.SourceRowDigest) || allZeroDigest(input.SourceOwnerScopeDigest) {
		return domain.ErrInvalidInput
	}
	switch input.OwnerScopeMappingStatus {
	case "not_required", "mapped", "pending", "incompatible_corp":
	default:
		return domain.ErrInvalidInput
	}
	return nil
}

func historicalImportRunID(value string) bool {
	if len(value) != len("open-platform:")+32 || !strings.HasPrefix(value, "open-platform:") {
		return false
	}
	for _, character := range value[len("open-platform:"):] {
		if !(character >= 'a' && character <= 'f') && !(character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func historicalSourceScope(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 1 && len(value) <= 320 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func (service *MachineService) historicalMachineExclusionReason(input accessport.HistoricalMachineImportInput) string {
	if reason := service.historicalOwnerScopeExclusionReason(input); reason != "" {
		return reason
	}
	principalType := strings.TrimSpace(input.PrincipalType)
	if principalType != "api_client" && !(principalType == "service" && strings.TrimSpace(input.Purpose) == "group_broadcast") {
		return "unsupported_principal_type"
	}
	if len(strings.TrimSpace(input.PrincipalID)) == 0 || len(strings.TrimSpace(input.PrincipalID)) > 240 || strings.IndexFunc(input.PrincipalID, unicode.IsControl) >= 0 {
		return "invalid_source_principal"
	}
	if input.SourceAuthVersion < 1 {
		return "invalid_source_auth_version"
	}
	if len(strings.TrimSpace(input.CorpID)) > 256 || strings.IndexFunc(input.CorpID, unicode.IsControl) >= 0 {
		return "invalid_source_corp_id"
	}
	profile, exists := domain.MachineProfileForPurpose(strings.TrimSpace(input.Purpose))
	if !exists {
		return "unsupported_purpose"
	}
	audiences, err := domain.NormalizeMachineStrings(input.Audiences, machineAudiences)
	if err != nil || !machineSubset(audiences, profile.Audiences) {
		return "unsupported_audience"
	}
	scopes, err := domain.NormalizeMachineStrings(input.Scopes, machineScopes)
	if err != nil || !machineSubset(scopes, profile.Scopes) {
		return "unsupported_scope"
	}
	capabilities, err := domain.NormalizeMachineStrings(input.Capabilities, machineCapabilities)
	if err != nil || !machineSubset(capabilities, profile.Capabilities) {
		return "unsupported_capability"
	}
	if _, err = domain.NormalizeCIDRs(input.AllowedCIDRs); err != nil {
		return "invalid_source_cidr"
	}
	if _, err = domain.NormalizeOwnerScope(input.OwnerScope.JSON()); err != nil {
		return "invalid_source_owner_scope"
	}
	if input.TokenTTLSeconds < 60 || input.TokenTTLSeconds > 3600 || strings.TrimSpace(input.ClientID) == "" || strings.TrimSpace(input.DisplayName) == "" {
		return "invalid_source_client"
	}
	// The donor system profiles are immutable service registrations. A subset
	// has no V3 route-equivalent registration, so it is retained as excluded
	// rather than widened to that profile's full grant set.
	if _, system := domain.SystemMachineProfileForPurpose(profile.Purpose); system &&
		(!equalMachineStrings(audiences, profile.Audiences) || !equalMachineStrings(scopes, profile.Scopes) || !equalMachineStrings(capabilities, profile.Capabilities)) {
		return "unsupported_system_profile_subset"
	}
	return ""
}

func (service *MachineService) historicalOwnerScopeExclusionReason(input accessport.HistoricalMachineImportInput) string {
	if len(input.OwnerScope) == 0 {
		if input.OwnerScopeMappingStatus != "not_required" {
			return "invalid_owner_scope_mapping"
		}
		return ""
	}
	if _, exists := input.OwnerScope["customer_id"]; exists {
		// The donor local integer has no identity proof in this snapshot. Never
		// compare it to customers.id: same-number collisions would widen access.
		return "owner_scope_mapping_pending"
	}
	for key := range input.OwnerScope {
		if key != "owner_userid" && key != "external_userid" {
			return "owner_scope_mapping_pending"
		}
	}
	if input.OwnerScopeMappingStatus != "mapped" {
		return "owner_scope_mapping_pending"
	}
	if strings.TrimSpace(service.config.CorpID) == "" || strings.TrimSpace(input.CorpID) != strings.TrimSpace(service.config.CorpID) {
		return "owner_scope_incompatible_corp"
	}
	return ""
}

func machineSubset(actual, allowed []string) bool {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	for _, value := range actual {
		if _, ok := allowedSet[value]; !ok {
			return false
		}
	}
	return true
}

func validateHistoricalMachineAudit(input accessport.HistoricalMachineAuditInput) error {
	if !historicalImportRunID(strings.TrimSpace(input.ImportRunID)) || input.SourceSystem != "ai-crm" || !historicalSourceScope(input.SourceScope) || input.SourceAuditID < 1 || allZeroDigest(input.SourceRowDigest) || allZeroDigest(input.BeforeDigest) || allZeroDigest(input.AfterDigest) || input.OccurredAt.IsZero() {
		return domain.ErrInvalidInput
	}
	for _, value := range []string{input.Operator, input.Action, input.TargetType, input.TargetID} {
		if len(value) > 240 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return domain.ErrInvalidInput
		}
	}
	if strings.TrimSpace(input.TargetType) != "api_client" {
		return domain.ErrInvalidInput
	}
	return nil
}

func allZeroDigest(digest [32]byte) bool {
	for _, value := range digest {
		if value != 0 {
			return false
		}
	}
	return true
}

func credentialOpaqueImportInput(entropy []byte) string {
	// This is never persisted or returned. Avoid an old secret or a constant
	// fallback that could become usable if an unrelated future defect regressed
	// the reissue guard.
	return "mc_import_" + string(entropy)
}
