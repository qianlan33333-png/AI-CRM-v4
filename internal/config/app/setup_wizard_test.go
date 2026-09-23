package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

func TestSetupWizardSaveAtomicallyWritesTwoSettingsAuditsEventsAndReadback(t *testing.T) {
	service, uow, repo, events := newSetupWizardServiceForTest(t, SetupWizardSecretConfigured{WeComSecret: true})
	before := setupWizardSnapshotForTest(t, service)
	callsBeforeSave := uow.calls
	input := setupWizardInput(before.ExpectedDigest, "setup-request-1")
	input.WeComAgentID = 730001

	result, err := service.Save(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if uow.calls != callsBeforeSave+1 {
		t.Fatalf("save transactions=%d want=%d", uow.calls, callsBeforeSave+1)
	}
	if result.Receipt.Replayed || len(result.Receipt.Audits) != 2 || len(result.Receipt.Events) != 2 {
		t.Fatalf("receipt=%#v", result.Receipt)
	}
	if result.Snapshot.Editable != (SetupWizardEditableSettings{WeComCorpID: input.WeComCorpID, WeComAgentID: input.WeComAgentID}) || result.Snapshot.ExpectedDigest == before.ExpectedDigest {
		t.Fatalf("snapshot=%#v before=%#v", result.Snapshot, before)
	}
	if !result.Snapshot.Masked.WeComSecret.Configured || !result.Snapshot.Masked.WeComSecret.Masked || result.Snapshot.Masked.AIAPIKey.Configured || !result.Snapshot.Masked.AIAPIKey.Masked || result.Snapshot.Masked.WeComCallbackToken.Configured || !result.Snapshot.Masked.WeComCallbackToken.Masked {
		t.Fatalf("masked=%#v", result.Snapshot.Masked)
	}
	if got := repo.locks[len(repo.locks)-len(setupWizardEditableKeys):]; !reflect.DeepEqual(got, setupWizardEditableKeys) {
		t.Fatalf("lock order=%#v want=%#v", got, setupWizardEditableKeys)
	}
	if len(repo.settings) != 2 || len(repo.audits) != 2 || len(events.records) != 2 {
		t.Fatalf("persisted settings/audits/events=%d/%d/%d", len(repo.settings), len(repo.audits), len(events.records))
	}
	for index, key := range setupWizardEditableKeys {
		commandID := setupWizardAuditRequestID(input.IdempotencyKey, key)
		if len(commandID) > 200 {
			t.Fatalf("derived request id too long: %d", len(commandID))
		}
		audit, ok := repo.audits[commandID]
		if !ok || audit.Key != key || audit.UpdatedBy != input.Actor || audit.RequestID != commandID {
			t.Fatalf("audit[%s]=%#v present=%v", key, audit, ok)
		}
		if result.Receipt.Audits[index] != (SetupWizardAuditReceipt{Key: key, ID: audit.ID}) || result.Receipt.Events[index] != (SetupWizardEventReceipt{Key: key, Type: "setting.updated"}) {
			t.Fatalf("receipt[%d]=%#v/%#v", index, result.Receipt.Audits[index], result.Receipt.Events[index])
		}
		event := events.records[index]
		if event.Type != "setting.updated" || event.IdempotencyKey != "setting.updated:"+commandID || bytes.Contains(event.Payload, []byte(input.WeComCorpID)) || bytes.Contains(event.Payload, []byte(strconv.FormatInt(input.WeComAgentID, 10))) || bytes.Contains(event.Payload, []byte("old_value")) || bytes.Contains(event.Payload, []byte("new_value")) {
			t.Fatalf("event=%#v", event)
		}
	}
	receipt, err := json.Marshal(result.Receipt)
	if err != nil || bytes.Contains(receipt, []byte(input.WeComCorpID)) || bytes.Contains(receipt, []byte(strconv.FormatInt(input.WeComAgentID, 10))) || bytes.Contains(receipt, []byte("old_value")) || bytes.Contains(receipt, []byte("new_value")) {
		t.Fatalf("receipt=%s err=%v", receipt, err)
	}
}

func TestSetupWizardGetRepresentsFreshUnconfiguredEditableState(t *testing.T) {
	service, _, _, _ := newSetupWizardServiceForTest(t, SetupWizardSecretConfigured{})
	snapshot := setupWizardSnapshotForTest(t, service)
	if snapshot.Editable != (SetupWizardEditableSettings{}) || snapshot.Configured != (SetupWizardEditableConfigured{}) {
		t.Fatalf("fresh editable/configured = %#v/%#v", snapshot.Editable, snapshot.Configured)
	}
}

func TestSetupWizardSaveReplaysOnlyWholeMatchingRequest(t *testing.T) {
	service, _, repo, events := newSetupWizardServiceForTest(t, SetupWizardSecretConfigured{})
	before := setupWizardSnapshotForTest(t, service)
	input := setupWizardInput(before.ExpectedDigest, "setup-request-replay")
	first, err := service.Save(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Save(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Receipt.Replayed || !reflect.DeepEqual(second.Receipt.Audits, first.Receipt.Audits) || len(repo.audits) != 2 || repo.upsertCalls != 2 || len(events.records) != 2 {
		t.Fatalf("replay=%#v audits=%d upserts=%d events=%d", second, len(repo.audits), repo.upsertCalls, len(events.records))
	}

	actorMismatch := input
	actorMismatch.Actor = "admin:43"
	actorMismatch.ExpectedDigest = second.Snapshot.ExpectedDigest
	if _, err := service.Save(context.Background(), actorMismatch); !errors.Is(err, ErrSetupWizardConflict) {
		t.Fatalf("actor mismatch error=%v", err)
	}
	valueMismatch := input
	valueMismatch.WeComCorpID = "different-corp"
	valueMismatch.ExpectedDigest = second.Snapshot.ExpectedDigest
	if _, err := service.Save(context.Background(), valueMismatch); !errors.Is(err, ErrSetupWizardConflict) {
		t.Fatalf("value mismatch error=%v", err)
	}
	// ExpectedDigest is part of the HTTP command, not merely a first-attempt
	// precondition. A same-key replay with a changed precondition must not be
	// mistaken for the original completed request.
	preconditionMismatch := input
	preconditionMismatch.ExpectedDigest = second.Snapshot.ExpectedDigest
	if _, err := service.Save(context.Background(), preconditionMismatch); !errors.Is(err, ErrSetupWizardConflict) {
		t.Fatalf("precondition mismatch error=%v", err)
	}
	if len(repo.audits) != 2 || repo.upsertCalls != 2 || len(events.records) != 2 {
		t.Fatalf("mismatch mutated state audits=%d upserts=%d events=%d", len(repo.audits), repo.upsertCalls, len(events.records))
	}

	later := input
	later.IdempotencyKey = "setup-request-later"
	later.ExpectedDigest = second.Snapshot.ExpectedDigest
	later.WeComCorpID, later.WeComAgentID = "later-corp", 18
	if _, err := service.Save(context.Background(), later); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Save(context.Background(), input); !errors.Is(err, ErrSetupWizardConflict) {
		t.Fatalf("replay after later write error=%v", err)
	}
	if len(repo.audits) != 4 || repo.upsertCalls != 4 || len(events.records) != 4 {
		t.Fatalf("stale replay mutated state audits=%d upserts=%d events=%d", len(repo.audits), repo.upsertCalls, len(events.records))
	}
}

func TestSetupWizardSaveRejectsMixedAuditStateWithoutPartialCommit(t *testing.T) {
	service, _, repo, events := newSetupWizardServiceForTest(t, SetupWizardSecretConfigured{})
	before := setupWizardSnapshotForTest(t, service)
	input := setupWizardInput(before.ExpectedDigest, "setup-request-mixed")
	commands, err := setupWizardCommands(input)
	if err != nil {
		t.Fatal(err)
	}
	repo.nextAuditID = 1
	repo.audits[commands[0].RequestID] = configport.Audit{
		ID: 1, Key: commands[0].Key, NewValue: append([]byte(nil), commands[0].Value...), UpdatedBy: commands[0].Actor, RequestID: commands[0].RequestID,
	}

	if _, err := service.Save(context.Background(), input); !errors.Is(err, ErrSetupWizardConflict) {
		t.Fatalf("mixed save error=%v", err)
	}
	if len(repo.audits) != 1 || len(repo.settings) != 0 || len(events.records) != 0 {
		t.Fatalf("mixed persisted audits/settings/events=%d/%d/%d", len(repo.audits), len(repo.settings), len(events.records))
	}
}

func TestSetupWizardSaveRollsBackAllSettingsAuditsAndEventsOnFailure(t *testing.T) {
	service, _, repo, events := newSetupWizardServiceForTest(t, SetupWizardSecretConfigured{})
	before := setupWizardSnapshotForTest(t, service)
	events.failOnCall, events.err = 2, errors.New("append event sentinel")
	if _, err := service.Save(context.Background(), setupWizardInput(before.ExpectedDigest, "setup-request-event-failure")); !errors.Is(err, events.err) {
		t.Fatalf("save error=%v", err)
	}
	if len(repo.settings) != 0 || len(repo.audits) != 0 || len(events.records) != 0 {
		t.Fatalf("rollback settings/audits/events=%d/%d/%d", len(repo.settings), len(repo.audits), len(events.records))
	}
}

func TestSetupWizardSaveRejectsNonEmptyMaskedInputsBeforeTransaction(t *testing.T) {
	service, uow, repo, events := newSetupWizardServiceForTest(t, SetupWizardSecretConfigured{})
	before := setupWizardSnapshotForTest(t, service)
	callsBeforeSave := uow.calls
	const sentinel = "masked-secret-sentinel"
	input := setupWizardInput(before.ExpectedDigest, "setup-request-masked")
	input.WeComSecret = sentinel
	if _, err := service.Save(context.Background(), input); !errors.Is(err, configport.ErrSecretSetting) || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("masked error=%v", err)
	}
	input.WeComSecret = " \t"
	if _, err := service.Save(context.Background(), input); !errors.Is(err, configport.ErrSecretSetting) {
		t.Fatalf("whitespace masked error=%v", err)
	}
	if uow.calls != callsBeforeSave || len(repo.settings) != 0 || len(repo.audits) != 0 || len(events.records) != 0 {
		t.Fatalf("masked reached transaction calls/settings/audits/events=%d/%d/%d/%d", uow.calls, len(repo.settings), len(repo.audits), len(events.records))
	}
}

func TestSetupWizardSaveRejectsNewRequestWithStaleDigest(t *testing.T) {
	service, _, repo, events := newSetupWizardServiceForTest(t, SetupWizardSecretConfigured{})
	before := setupWizardSnapshotForTest(t, service)
	repo.settings[configport.WeComCorpID] = configport.Setting{Key: configport.WeComCorpID, Value: []byte(`"other-corp"`), UpdatedBy: "admin:9", UpdatedAt: time.Now().UTC()}
	repo.settings[configport.WeComAgentID] = configport.Setting{Key: configport.WeComAgentID, Value: []byte(`9`), UpdatedBy: "admin:9", UpdatedAt: time.Now().UTC()}
	if _, err := service.Save(context.Background(), setupWizardInput(before.ExpectedDigest, "setup-request-stale")); !errors.Is(err, ErrSetupWizardConflict) {
		t.Fatalf("stale CAS error=%v", err)
	}
	if len(repo.audits) != 0 || len(events.records) != 0 || string(repo.settings[configport.WeComCorpID].Value) != `"other-corp"` || string(repo.settings[configport.WeComAgentID].Value) != "9" {
		t.Fatalf("stale mutated state audits=%d events=%d settings=%#v", len(repo.audits), len(events.records), repo.settings)
	}
}

func TestSetupWizardSaveFailsClosedWhenStrictReadbackDiffers(t *testing.T) {
	service, _, repo, events := newSetupWizardServiceForTest(t, SetupWizardSecretConfigured{})
	before := setupWizardSnapshotForTest(t, service)
	repo.corruptReadback = true
	if _, err := service.Save(context.Background(), setupWizardInput(before.ExpectedDigest, "setup-request-readback")); !errors.Is(err, ErrSetupWizardReadback) {
		t.Fatalf("readback error=%v", err)
	}
	if len(repo.settings) != 0 || len(repo.audits) != 0 || len(events.records) != 0 {
		t.Fatalf("readback failure persisted settings/audits/events=%d/%d/%d", len(repo.settings), len(repo.audits), len(events.records))
	}
}

func TestSetupWizardBatchAndManagerSetSerializeOnSharedAdvisoryLock(t *testing.T) {
	base := &wizardRepository{settings: map[configport.Key]configport.Setting{}, audits: map[string]configport.Audit{}, receipts: map[string]configport.RequestReceipt{}}
	repo := newSerializingWizardRepository(base)
	events := &wizardAppender{}
	uow := &serializingWizardUoW{repo: repo}
	manager := NewManager(uow, repo, events)
	manager.now = func() time.Time { return time.Date(2026, 8, 23, 8, 0, 0, 0, time.UTC) }
	emptyDigest, err := setupWizardDigest(map[configport.Key]json.RawMessage{})
	if err != nil {
		t.Fatal(err)
	}

	batchDone := make(chan error, 1)
	go func() {
		_, err := manager.saveSetupWizard(context.Background(), setupWizardInput(emptyDigest, "setup-request-lock"))
		batchDone <- err
	}()
	<-repo.agentHeld
	setDone := make(chan error, 1)
	go func() {
		_, err := manager.Set(context.Background(), configport.SetCommand{Key: configport.WeComAgentID, Value: []byte(`18`), Actor: "admin:99", RequestID: "ordinary-set-lock"})
		setDone <- err
	}()
	<-repo.agentSetAttempted
	select {
	case err := <-setDone:
		t.Fatalf("Manager.Set bypassed the shared lock: %v", err)
	default:
	}
	close(repo.releaseBatch)
	if err := <-batchDone; err != nil {
		t.Fatalf("batch error=%v", err)
	}
	if err := <-setDone; err != nil {
		t.Fatalf("set error=%v", err)
	}
	if got := string(base.settings[configport.WeComAgentID].Value); got != "18" {
		t.Fatalf("serialized final agent=%s", got)
	}
}

func setupWizardSnapshotForTest(t *testing.T, service *SetupWizardService) SetupWizardSnapshot {
	t.Helper()
	snapshot, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func setupWizardInput(digest, idempotencyKey string) SetupWizardSaveInput {
	return SetupWizardSaveInput{
		WeComCorpID: "corp-record-sentinel", WeComAgentID: 17,
		ExpectedDigest: digest, Actor: "admin:42", IdempotencyKey: idempotencyKey,
	}
}

func newSetupWizardServiceForTest(t *testing.T, configured SetupWizardSecretConfigured) (*SetupWizardService, *wizardUoW, *wizardRepository, *wizardAppender) {
	t.Helper()
	repo := &wizardRepository{settings: map[configport.Key]configport.Setting{}, audits: map[string]configport.Audit{}, receipts: map[string]configport.RequestReceipt{}}
	events := &wizardAppender{}
	uow := &wizardUoW{repo: repo, events: events}
	manager := NewManager(uow, repo, events)
	manager.now = func() time.Time { return time.Date(2026, 8, 23, 8, 0, 0, 0, time.UTC) }
	service, err := NewSetupWizardService(manager, configured)
	if err != nil {
		t.Fatal(err)
	}
	return service, uow, repo, events
}

type wizardUoW struct {
	repo   *wizardRepository
	events *wizardAppender
	calls  int
}

func (uow *wizardUoW) Within(ctx context.Context, callback func(context.Context) error) error {
	uow.calls++
	settings, audits, receipts, nextAuditID, upsertCalls := cloneWizardSettings(uow.repo.settings), cloneWizardAudits(uow.repo.audits), cloneWizardReceipts(uow.repo.receipts), uow.repo.nextAuditID, uow.repo.upsertCalls
	events := append([]configport.Event(nil), uow.events.records...)
	err := callback(ctx)
	if err != nil {
		uow.repo.settings, uow.repo.audits, uow.repo.receipts, uow.repo.nextAuditID, uow.repo.upsertCalls = settings, audits, receipts, nextAuditID, upsertCalls
		uow.events.records = events
	}
	return err
}

type wizardRepository struct {
	locks           []configport.Key
	settings        map[configport.Key]configport.Setting
	audits          map[string]configport.Audit
	nextAuditID     int64
	upsertCalls     int
	corruptReadback bool
	receipts        map[string]configport.RequestReceipt
}

func (repository *wizardRepository) ReserveCommandRequest(_ context.Context, action, actor, requestID string, digest []byte, _ time.Time) (configport.RequestReceipt, bool, error) {
	key := action + ":" + actor + ":" + requestID
	if existing, ok := repository.receipts[key]; ok {
		existing.PayloadDigest = append([]byte(nil), existing.PayloadDigest...)
		return existing, false, nil
	}
	receipt := configport.RequestReceipt{ID: int64(len(repository.receipts) + 1), PayloadDigest: append([]byte(nil), digest...), State: "reserved"}
	repository.receipts[key] = receipt
	return receipt, true, nil
}

func (repository *wizardRepository) CompleteCommandRequest(_ context.Context, id int64, _ time.Time) error {
	for key, receipt := range repository.receipts {
		if receipt.ID == id && receipt.State == "reserved" {
			receipt.State = "completed"
			repository.receipts[key] = receipt
			return nil
		}
	}
	return errors.New("receipt not reserved")
}

func (repository *wizardRepository) LockKey(_ context.Context, key configport.Key) error {
	repository.locks = append(repository.locks, key)
	return nil
}

func (repository *wizardRepository) Get(_ context.Context, key configport.Key) (configport.Setting, bool, error) {
	if repository.corruptReadback && repository.upsertCalls >= len(setupWizardEditableKeys) && key == configport.WeComCorpID {
		return configport.Setting{Key: key, Value: []byte(`"different-readback"`)}, true, nil
	}
	setting, found := repository.settings[key]
	if found {
		setting.Value = append([]byte(nil), setting.Value...)
	}
	return setting, found, nil
}

func (repository *wizardRepository) InsertAudit(_ context.Context, oldValue []byte, command configport.SetCommand, canonical []byte, updatedAt time.Time) (configport.Audit, bool, error) {
	if _, exists := repository.audits[command.RequestID]; exists {
		return configport.Audit{}, false, nil
	}
	repository.nextAuditID++
	audit := configport.Audit{
		ID: repository.nextAuditID, Key: command.Key, OldValue: append([]byte(nil), oldValue...), NewValue: append([]byte(nil), canonical...),
		UpdatedBy: command.Actor, RequestID: command.RequestID, UpdatedAt: updatedAt,
	}
	repository.audits[command.RequestID] = audit
	return audit, true, nil
}

func (repository *wizardRepository) GetAuditByRequestID(_ context.Context, requestID string) (configport.Audit, error) {
	audit, found := repository.audits[requestID]
	if !found {
		return configport.Audit{}, errors.New("audit not found")
	}
	audit.OldValue, audit.NewValue = append([]byte(nil), audit.OldValue...), append([]byte(nil), audit.NewValue...)
	return audit, nil
}

func (repository *wizardRepository) Upsert(_ context.Context, command configport.SetCommand, canonical []byte, updatedAt time.Time) (configport.Setting, error) {
	repository.upsertCalls++
	setting := configport.Setting{Key: command.Key, Value: append([]byte(nil), canonical...), UpdatedBy: command.Actor, UpdatedAt: updatedAt}
	repository.settings[command.Key] = setting
	return setting, nil
}

type wizardAppender struct {
	records    []configport.Event
	calls      int
	failOnCall int
	err        error
}

type wizardLockContextKey struct{}

type serializingWizardUoW struct{ repo *serializingWizardRepository }

func (uow *serializingWizardUoW) Within(ctx context.Context, callback func(context.Context) error) error {
	held := make([]*sync.Mutex, 0, len(setupWizardEditableKeys))
	txCtx := context.WithValue(ctx, wizardLockContextKey{}, &held)
	err := callback(txCtx)
	for index := len(held) - 1; index >= 0; index-- {
		held[index].Unlock()
	}
	return err
}

type serializingWizardRepository struct {
	*wizardRepository
	locks             map[configport.Key]*sync.Mutex
	signalMu          sync.Mutex
	agentLockCalls    int
	agentHeld         chan struct{}
	agentSetAttempted chan struct{}
	releaseBatch      chan struct{}
}

func newSerializingWizardRepository(repository *wizardRepository) *serializingWizardRepository {
	return &serializingWizardRepository{
		wizardRepository: repository,
		locks: map[configport.Key]*sync.Mutex{
			configport.WeComAgentID: {}, configport.WeComCorpID: {},
		},
		agentHeld: make(chan struct{}), agentSetAttempted: make(chan struct{}), releaseBatch: make(chan struct{}),
	}
}

func (repository *serializingWizardRepository) LockKey(ctx context.Context, key configport.Key) error {
	held, ok := ctx.Value(wizardLockContextKey{}).(*[]*sync.Mutex)
	if !ok || held == nil {
		return errors.New("missing lock transaction")
	}
	lock := repository.locks[key]
	if lock == nil {
		return errors.New("unknown lock key")
	}
	repository.signalMu.Lock()
	if key == configport.WeComAgentID {
		repository.agentLockCalls++
		switch repository.agentLockCalls {
		case 1:
			lock.Lock()
			*held = append(*held, lock)
			close(repository.agentHeld)
			repository.signalMu.Unlock()
			<-repository.releaseBatch
			return nil
		case 2:
			close(repository.agentSetAttempted)
		}
	}
	repository.signalMu.Unlock()
	lock.Lock()
	*held = append(*held, lock)
	return nil
}

func (appender *wizardAppender) Append(_ context.Context, event configport.Event) (configport.EventID, error) {
	appender.calls++
	appender.records = append(appender.records, event)
	if appender.failOnCall == appender.calls {
		return 0, appender.err
	}
	return configport.EventID(appender.calls), nil
}

func cloneWizardSettings(source map[configport.Key]configport.Setting) map[configport.Key]configport.Setting {
	clone := make(map[configport.Key]configport.Setting, len(source))
	for key, setting := range source {
		setting.Value = append([]byte(nil), setting.Value...)
		clone[key] = setting
	}
	return clone
}

func cloneWizardAudits(source map[string]configport.Audit) map[string]configport.Audit {
	clone := make(map[string]configport.Audit, len(source))
	for key, audit := range source {
		audit.OldValue, audit.NewValue = append([]byte(nil), audit.OldValue...), append([]byte(nil), audit.NewValue...)
		clone[key] = audit
	}
	return clone
}

func cloneWizardReceipts(source map[string]configport.RequestReceipt) map[string]configport.RequestReceipt {
	clone := make(map[string]configport.RequestReceipt, len(source))
	for key, receipt := range source {
		receipt.PayloadDigest = append([]byte(nil), receipt.PayloadDigest...)
		clone[key] = receipt
	}
	return clone
}
