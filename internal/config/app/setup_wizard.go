package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	config "github.com/qianlan33333-png/AI-CRM-v3/internal/config"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

var (
	ErrInvalidSetupWizardRequest = errors.New("invalid setup wizard request")
	ErrSetupWizardConflict       = errors.New("setup wizard conflict")
	ErrSetupWizardReadback       = errors.New("setup wizard readback mismatch")
)

var setupWizardEditableKeys = []configport.Key{
	configport.WeComAgentID,
	configport.WeComCorpID,
}

// SetupWizardSecretConfigured carries only configured state. It must never
// contain secret material.
type SetupWizardSecretConfigured struct {
	WeComSecret         bool
	WeComCallbackToken  bool
	WeComCallbackAESKey bool
}

type SetupWizardMaskedSetting struct {
	Configured bool `json:"configured"`
	Masked     bool `json:"masked"`
}

type SetupWizardMaskedSettings struct {
	WeComSecret         SetupWizardMaskedSetting `json:"wecom.secret"`
	WeComCallbackToken  SetupWizardMaskedSetting `json:"wecom.callback_token"`
	WeComCallbackAESKey SetupWizardMaskedSetting `json:"wecom.callback_aes_key"`
	AIAPIKey            SetupWizardMaskedSetting `json:"ai.api_key"`
}

type SetupWizardEditableSettings struct {
	WeComCorpID  string `json:"wecom.corp_id"`
	WeComAgentID int64  `json:"wecom.agent_id"`
}

type SetupWizardEditableConfigured struct {
	WeComCorpID  bool `json:"wecom.corp_id"`
	WeComAgentID bool `json:"wecom.agent_id"`
}

// SetupWizardSnapshot is the strictly scoped local configuration state. Its
// digest is the CAS precondition for a new save request.
type SetupWizardSnapshot struct {
	ExpectedDigest string                        `json:"expected_digest"`
	Editable       SetupWizardEditableSettings   `json:"editable"`
	Configured     SetupWizardEditableConfigured `json:"editable_configured"`
	Masked         SetupWizardMaskedSettings     `json:"masked"`
}

type SetupWizardSaveInput struct {
	WeComCorpID  string
	WeComAgentID int64

	// Masked inputs are intentionally accepted only when exactly empty. They
	// are never passed into the Manager batch or any persistence/event path.
	WeComSecret         string
	WeComCallbackToken  string
	WeComCallbackAESKey string
	AIAPIKey            string

	ExpectedDigest string
	Actor          string
	IdempotencyKey string
}

type SetupWizardAuditReceipt struct {
	Key configport.Key `json:"key"`
	ID  int64          `json:"id"`
}

type SetupWizardEventReceipt struct {
	Key  configport.Key `json:"key"`
	Type string         `json:"type"`
}

// SetupWizardReceipt is derived from settings_audit and the transactionally
// appended local events. It deliberately includes neither old nor new values.
type SetupWizardReceipt struct {
	IdempotencyKey string                    `json:"idempotency_key"`
	Replayed       bool                      `json:"replayed"`
	Audits         []SetupWizardAuditReceipt `json:"audits"`
	Events         []SetupWizardEventReceipt `json:"events"`
}

type SetupWizardSaveResult struct {
	Snapshot SetupWizardSnapshot `json:"snapshot"`
	Receipt  SetupWizardReceipt  `json:"receipt"`
}

// SetupWizardService narrows the generic settings owner to the two local
// values that the legacy setup wizard may persist.
type SetupWizardService struct {
	manager *Manager
	secrets SetupWizardSecretConfigured
}

func NewSetupWizardService(manager *Manager, secrets SetupWizardSecretConfigured) (*SetupWizardService, error) {
	if manager == nil {
		return nil, ErrInvalidSetupWizardRequest
	}
	return &SetupWizardService{manager: manager, secrets: secrets}, nil
}

func (service *SetupWizardService) Get(ctx context.Context) (SetupWizardSnapshot, error) {
	if service == nil || service.manager == nil {
		return SetupWizardSnapshot{}, ErrInvalidSetupWizardRequest
	}
	editable, err := service.manager.setupWizardSnapshot(ctx)
	if err != nil {
		return SetupWizardSnapshot{}, err
	}
	return SetupWizardSnapshot{
		ExpectedDigest: editable.digest,
		Editable: SetupWizardEditableSettings{
			WeComCorpID:  editable.corpID,
			WeComAgentID: editable.agentID,
		},
		Configured: editable.configured(),
		Masked:     setupWizardMaskedSettings(service.secrets),
	}, nil
}

func (service *SetupWizardService) Save(ctx context.Context, input SetupWizardSaveInput) (SetupWizardSaveResult, error) {
	if service == nil || service.manager == nil {
		return SetupWizardSaveResult{}, ErrInvalidSetupWizardRequest
	}
	if input.WeComSecret != "" || input.WeComCallbackToken != "" || input.WeComCallbackAESKey != "" || input.AIAPIKey != "" {
		return SetupWizardSaveResult{}, configport.ErrSecretSetting
	}
	batch, err := service.manager.saveSetupWizard(ctx, input)
	if err != nil {
		return SetupWizardSaveResult{}, err
	}
	return SetupWizardSaveResult{
		Snapshot: SetupWizardSnapshot{
			ExpectedDigest: batch.snapshot.digest,
			Editable: SetupWizardEditableSettings{
				WeComCorpID:  batch.snapshot.corpID,
				WeComAgentID: batch.snapshot.agentID,
			},
			Configured: batch.snapshot.configured(),
			Masked:     setupWizardMaskedSettings(service.secrets),
		},
		Receipt: batch.receipt,
	}, nil
}

func setupWizardMaskedSettings(configured SetupWizardSecretConfigured) SetupWizardMaskedSettings {
	return SetupWizardMaskedSettings{
		WeComSecret:         SetupWizardMaskedSetting{Configured: configured.WeComSecret, Masked: true},
		WeComCallbackToken:  SetupWizardMaskedSetting{Configured: configured.WeComCallbackToken, Masked: true},
		WeComCallbackAESKey: SetupWizardMaskedSetting{Configured: configured.WeComCallbackAESKey, Masked: true},
		// AI has no canonical Root configuration in this local-only slice.
		// settings treats ai.api_key as forbidden, so a historical app-settings
		// row cannot establish that a provider credential is configured.
		AIAPIKey: SetupWizardMaskedSetting{Configured: false, Masked: true},
	}
}

type setupWizardEditableSnapshot struct {
	values  map[configport.Key]json.RawMessage
	corpID  string
	agentID int64
	digest  string
}

func (snapshot setupWizardEditableSnapshot) configured() SetupWizardEditableConfigured {
	_, corpIDConfigured := snapshot.values[configport.WeComCorpID]
	_, agentIDConfigured := snapshot.values[configport.WeComAgentID]
	return SetupWizardEditableConfigured{WeComCorpID: corpIDConfigured, WeComAgentID: agentIDConfigured}
}

type setupWizardDigestEntry struct {
	Key        configport.Key  `json:"key"`
	Configured bool            `json:"configured"`
	Value      json.RawMessage `json:"value"`
}

func (manager *Manager) setupWizardSnapshot(ctx context.Context) (snapshot setupWizardEditableSnapshot, err error) {
	if err = manager.ready(); err != nil {
		return setupWizardEditableSnapshot{}, err
	}
	err = manager.uow.Within(ctx, func(txCtx context.Context) error {
		if err := manager.lockSetupWizardKeys(txCtx); err != nil {
			return err
		}
		var readErr error
		snapshot, readErr = manager.readSetupWizardSnapshot(txCtx)
		return readErr
	})
	return snapshot, err
}

type setupWizardBatchResult struct {
	snapshot setupWizardEditableSnapshot
	receipt  SetupWizardReceipt
}

func (manager *Manager) saveSetupWizard(ctx context.Context, input SetupWizardSaveInput) (result setupWizardBatchResult, err error) {
	commands, err := setupWizardCommands(input)
	if err != nil {
		return setupWizardBatchResult{}, err
	}
	if err = manager.ready(); err != nil {
		return setupWizardBatchResult{}, err
	}
	updatedAt := manager.now().UTC()
	if updatedAt.IsZero() {
		return setupWizardBatchResult{}, fmt.Errorf("config manager clock is invalid")
	}
	requestDigest, err := setupWizardRequestDigest(input)
	if err != nil {
		return setupWizardBatchResult{}, err
	}
	receipts, ok := manager.repo.(requestReceiptRepository)
	if !ok {
		return setupWizardBatchResult{}, errors.New("config request receipt repository is required")
	}
	err = manager.uow.Within(ctx, func(txCtx context.Context) error {
		if lockErr := manager.lockSetupWizardKeys(txCtx); lockErr != nil {
			return lockErr
		}
		receipt, owned, reserveErr := receipts.ReserveCommandRequest(txCtx, "setup_wizard.save", input.Actor, input.IdempotencyKey, requestDigest, updatedAt)
		if reserveErr != nil {
			return reserveErr
		}
		if !owned && (!bytes.Equal(receipt.PayloadDigest, requestDigest) || receipt.State != "completed") {
			return ErrSetupWizardConflict
		}
		current, readErr := manager.readSetupWizardSnapshot(txCtx)
		if readErr != nil {
			return readErr
		}

		audits := make([]configAuditReceipt, len(commands))
		inserted := make([]bool, len(commands))
		for index, command := range commands {
			oldValue := current.values[command.Key]
			audit, didInsert, auditErr := manager.repo.InsertAudit(txCtx, oldValue, command, command.Value, updatedAt)
			if auditErr != nil {
				return auditErr
			}
			audits[index] = configAuditReceipt{audit: audit, command: command}
			inserted[index] = didInsert
		}

		switch {
		case allSetupWizardInserted(inserted):
			if !owned {
				return ErrSetupWizardConflict
			}
			// A new command must match the state observed while holding both
			// advisory locks. A rejected CAS rolls the provisional audit rows
			// back with the enclosing transaction.
			if input.ExpectedDigest != current.digest {
				return ErrSetupWizardConflict
			}
			for _, item := range audits {
				if _, upsertErr := manager.repo.Upsert(txCtx, item.command, item.command.Value, updatedAt); upsertErr != nil {
					return upsertErr
				}
			}
			for _, item := range audits {
				payload, payloadErr := setupWizardEventPayload(item.audit.ID, item.command.Key)
				if payloadErr != nil {
					return payloadErr
				}
				if _, appendErr := manager.events.Append(txCtx, configport.Event{
					Type:           "setting.updated",
					Payload:        payload,
					OccurredAt:     updatedAt,
					IdempotencyKey: "setting.updated:" + item.command.RequestID,
				}); appendErr != nil {
					if errors.Is(appendErr, configport.ErrIdempotencyConflict) {
						return ErrSetupWizardConflict
					}
					return appendErr
				}
			}
			readback, readbackErr := manager.readSetupWizardSnapshot(txCtx)
			if readbackErr != nil {
				return readbackErr
			}
			if !readback.matches(commands) {
				return ErrSetupWizardReadback
			}
			if completeErr := receipts.CompleteCommandRequest(txCtx, receipt.ID, updatedAt); completeErr != nil {
				return completeErr
			}
			result = setupWizardBatchResult{snapshot: readback, receipt: setupWizardReceipt(input.IdempotencyKey, false, audits)}
			return nil
		case noSetupWizardInserted(inserted):
			if owned {
				return ErrSetupWizardConflict
			}
			for index, item := range audits {
				audit, auditErr := manager.repo.GetAuditByRequestID(txCtx, item.command.RequestID)
				if auditErr != nil || !matchesSetupWizardAudit(audit, item.command) {
					return ErrSetupWizardConflict
				}
				audits[index].audit = audit
			}
			// A retry succeeds even though its original CAS precondition is now
			// stale: it is safe only when both stored receipts and the current
			// locked values exactly match the original command.
			if !current.matches(commands) {
				return ErrSetupWizardConflict
			}
			result = setupWizardBatchResult{snapshot: current, receipt: setupWizardReceipt(input.IdempotencyKey, true, audits)}
			return nil
		default:
			// One existing receipt plus one new receipt is evidence of an
			// inconsistent prior request. Returning an error rolls any newly
			// inserted audit row back; no partial save is accepted.
			return ErrSetupWizardConflict
		}
	})
	return result, err
}

// setupWizardRequestDigest binds a browser idempotency key to its full
// non-secret command and CAS precondition. It is persisted only as a digest.
func setupWizardRequestDigest(input SetupWizardSaveInput) ([]byte, error) {
	payload, err := json.Marshal(struct {
		Actor          string `json:"actor"`
		IdempotencyKey string `json:"idempotency_key"`
		ExpectedDigest string `json:"expected_digest"`
		WeComCorpID    string `json:"wecom.corp_id"`
		WeComAgentID   int64  `json:"wecom.agent_id"`
	}{input.Actor, input.IdempotencyKey, input.ExpectedDigest, input.WeComCorpID, input.WeComAgentID})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	return digest[:], nil
}

type configAuditReceipt struct {
	audit   configport.Audit
	command configport.SetCommand
}

func (manager *Manager) lockSetupWizardKeys(ctx context.Context) error {
	for _, key := range setupWizardEditableKeys {
		if err := manager.repo.LockKey(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func (manager *Manager) readSetupWizardSnapshot(ctx context.Context) (setupWizardEditableSnapshot, error) {
	snapshot := setupWizardEditableSnapshot{values: make(map[configport.Key]json.RawMessage, len(setupWizardEditableKeys))}
	for _, key := range setupWizardEditableKeys {
		setting, found, err := manager.repo.Get(ctx, key)
		if err != nil {
			return setupWizardEditableSnapshot{}, err
		}
		if !found {
			continue
		}
		if setting.Key != key {
			return setupWizardEditableSnapshot{}, ErrSetupWizardReadback
		}
		canonical, validateErr := config.ValidateSetting(key, setting.Value)
		if validateErr != nil || !bytes.Equal(canonical, setting.Value) {
			return setupWizardEditableSnapshot{}, ErrSetupWizardReadback
		}
		snapshot.values[key] = canonical
		switch key {
		case configport.WeComCorpID:
			if json.Unmarshal(canonical, &snapshot.corpID) != nil {
				return setupWizardEditableSnapshot{}, ErrSetupWizardReadback
			}
		case configport.WeComAgentID:
			if json.Unmarshal(canonical, &snapshot.agentID) != nil {
				return setupWizardEditableSnapshot{}, ErrSetupWizardReadback
			}
		}
	}
	digest, err := setupWizardDigest(snapshot.values)
	if err != nil {
		return setupWizardEditableSnapshot{}, err
	}
	snapshot.digest = digest
	return snapshot, nil
}

func setupWizardCommands(input SetupWizardSaveInput) ([]configport.SetCommand, error) {
	if !validMetadata(input.Actor) || !validMetadata(input.IdempotencyKey) || !validSetupWizardDigest(input.ExpectedDigest) {
		return nil, ErrInvalidSetupWizardRequest
	}
	corpRaw, _ := json.Marshal(input.WeComCorpID)
	agentRaw, _ := json.Marshal(input.WeComAgentID)
	values := map[configport.Key]json.RawMessage{
		configport.WeComCorpID:  corpRaw,
		configport.WeComAgentID: agentRaw,
	}
	commands := make([]configport.SetCommand, 0, len(setupWizardEditableKeys))
	for _, key := range setupWizardEditableKeys {
		canonical, err := config.ValidateSetting(key, values[key])
		if err != nil {
			return nil, err
		}
		commands = append(commands, configport.SetCommand{
			Key:       key,
			Value:     canonical,
			Actor:     input.Actor,
			RequestID: setupWizardAuditRequestID(input.IdempotencyKey, key),
		})
	}
	return commands, nil
}

func validSetupWizardDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func setupWizardAuditRequestID(idempotencyKey string, key configport.Key) string {
	digest := sha256.Sum256([]byte(idempotencyKey))
	return "setup-wizard:v1:" + hex.EncodeToString(digest[:]) + ":" + string(key)
}

func setupWizardEventPayload(auditID int64, key configport.Key) ([]byte, error) {
	return json.Marshal(struct {
		AuditID int64          `json:"audit_id"`
		Key     configport.Key `json:"key"`
	}{AuditID: auditID, Key: key})
}

func setupWizardDigest(values map[configport.Key]json.RawMessage) (string, error) {
	entries := make([]setupWizardDigestEntry, 0, len(setupWizardEditableKeys))
	for _, key := range setupWizardEditableKeys {
		value, configured := values[key]
		entries = append(entries, setupWizardDigestEntry{Key: key, Configured: configured, Value: value})
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (snapshot setupWizardEditableSnapshot) matches(commands []configport.SetCommand) bool {
	for _, command := range commands {
		value, configured := snapshot.values[command.Key]
		if !configured || !bytes.Equal(value, command.Value) {
			return false
		}
	}
	return true
}

func allSetupWizardInserted(values []bool) bool {
	for _, value := range values {
		if !value {
			return false
		}
	}
	return true
}

func noSetupWizardInserted(values []bool) bool {
	for _, value := range values {
		if value {
			return false
		}
	}
	return true
}

func matchesSetupWizardAudit(audit configport.Audit, command configport.SetCommand) bool {
	return audit.Key == command.Key && audit.UpdatedBy == command.Actor && audit.RequestID == command.RequestID && bytes.Equal(audit.NewValue, command.Value)
}

func setupWizardReceipt(idempotencyKey string, replayed bool, audits []configAuditReceipt) SetupWizardReceipt {
	receipt := SetupWizardReceipt{
		IdempotencyKey: idempotencyKey,
		Replayed:       replayed,
		Audits:         make([]SetupWizardAuditReceipt, 0, len(audits)),
		Events:         make([]SetupWizardEventReceipt, 0, len(audits)),
	}
	for _, item := range audits {
		receipt.Audits = append(receipt.Audits, SetupWizardAuditReceipt{Key: item.command.Key, ID: item.audit.ID})
		receipt.Events = append(receipt.Events, SetupWizardEventReceipt{Key: item.command.Key, Type: "setting.updated"})
	}
	return receipt
}
