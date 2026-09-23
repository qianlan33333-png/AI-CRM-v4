package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

type executionStoreStub struct {
	ExecutionStore
	pkg                               segmentdomain.Package
	config                            segmentdomain.ConfigurationVersion
	binding                           segmentdomain.AutomationBinding
	senders                           segmentdomain.SenderSet
	snapshot                          segmentport.Snapshot
	configErr, bindingErr, sendersErr error
}

func (s executionStoreStub) LockPackage(context.Context, int64) (segmentdomain.Package, error) {
	return s.pkg, nil
}
func (s executionStoreStub) CurrentConfiguration(context.Context, int64) (segmentdomain.ConfigurationVersion, error) {
	return s.config, s.configErr
}
func (s executionStoreStub) CurrentBinding(context.Context, int64) (segmentdomain.AutomationBinding, error) {
	return s.binding, s.bindingErr
}
func (s executionStoreStub) CurrentSenderSet(context.Context, int64) (segmentdomain.SenderSet, error) {
	return s.senders, s.sendersErr
}
func (s executionStoreStub) CreateSenderSet(_ context.Context, item segmentdomain.SenderSet) (segmentdomain.SenderSet, error) {
	item.ID = 1
	item.Version = 1
	return item, nil
}
func (s executionStoreStub) SetCurrentSenderSet(_ context.Context, packageID, _senderSetID, _expectedVersion, _actor int64, _now time.Time) (segmentdomain.Package, error) {
	item := s.pkg
	item.ID = packageID
	item.Version++
	return item, nil
}
func (s executionStoreStub) Reserve(context.Context, segmentstore.Reservation) (segmentstore.Receipt, bool, error) {
	return segmentstore.Receipt{ID: 1}, true, nil
}
func (s executionStoreStub) Complete(_ context.Context, id int64, result json.RawMessage, now time.Time) (segmentstore.Receipt, error) {
	return segmentstore.Receipt{ID: id, ResultSnapshot: result, CompletedAt: &now}, nil
}
func (s executionStoreStub) AppendMutationFacts(context.Context, segmentstore.MutationFact) (int64, error) {
	return 1, nil
}

func TestPrecheckReportsMissingSetupWithoutTurningItIntoAReadFailure(t *testing.T) {
	store := executionStoreStub{
		pkg:        segmentdomain.Package{ID: 1, Lifecycle: segmentdomain.Paused},
		config:     segmentdomain.ConfigurationVersion{ID: 2, Definition: []byte(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`)},
		snapshot:   segmentport.Snapshot{ID: 4},
		bindingErr: segmentstore.ErrNotFound,
		sendersErr: segmentstore.ErrNotFound,
	}
	service, _ := NewExecutionService(directUOW{}, store, publishedAgentStub{}, staffReaderStub{}, false)
	check, err := service.Precheck(context.Background(), 1)
	if err != nil {
		t.Fatalf("precheck err=%v", err)
	}
	want := []string{"automation_binding_missing", "sender_set_missing", "provider_disabled"}
	if check.Ready || !reflect.DeepEqual(check.Reasons, want) {
		t.Fatalf("check=%+v want reasons=%v", check, want)
	}
}

func TestPrecheckReportsMissingConfigurationAsReadinessReason(t *testing.T) {
	store := executionStoreStub{
		pkg:        segmentdomain.Package{ID: 1, Lifecycle: segmentdomain.Paused},
		configErr:  segmentstore.ErrNotFound,
		bindingErr: segmentstore.ErrNotFound,
		sendersErr: segmentstore.ErrNotFound,
	}
	service, _ := NewExecutionService(directUOW{}, store, publishedAgentStub{}, staffReaderStub{}, false)
	check, err := service.Precheck(context.Background(), 1)
	if err != nil {
		t.Fatalf("precheck err=%v", err)
	}
	want := []string{"configuration_missing", "automation_binding_missing", "sender_set_missing", "published_snapshot_missing", "provider_disabled"}
	if check.Ready || !reflect.DeepEqual(check.Reasons, want) {
		t.Fatalf("check=%+v want reasons=%v", check, want)
	}
}
func (s executionStoreStub) PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error) {
	return s.snapshot, s.snapshot.ID > 0, nil
}

type publishedAgentStub struct {
	value automationport.PublishedAgent
	found bool
}

func (s publishedAgentStub) PublishedAgent(context.Context, automationport.AgentID) (automationport.PublishedAgent, bool, error) {
	return s.value, s.found, nil
}

type staffReaderStub struct{ value accessport.StaffEligibility }

func (s staffReaderStub) ResolveAutomationSender(context.Context, string) (accessport.StaffEligibility, bool, error) {
	return s.value, true, nil
}
func (s staffReaderStub) AutomationSender(context.Context, accessport.StaffID) (accessport.StaffEligibility, bool, error) {
	return s.value, true, nil
}
func TestBindingRejectsPublishedVersionDriftBeforeWriting(t *testing.T) {
	agent := automationport.PublishedAgent{AgentID: 3, PublishedVersion: 2, AutomationType: automationport.AutomationTypeFixedScript, Status: automationport.AgentStatusActive, ContentDigest: sha256.Sum256([]byte("c")), MaterialsDigest: sha256.Sum256([]byte("m"))}
	service, _ := NewExecutionService(directUOW{}, executionStoreStub{}, publishedAgentStub{agent, true}, staffReaderStub{}, false)
	digest := sha256.Sum256(append(append([]byte{}, agent.ContentDigest[:]...), agent.MaterialsDigest[:]...))
	_, err := service.PutBinding(context.Background(), BindingCommand{PackageID: 1, ExpectedPackageVersion: 1, AgentID: 3, ExpectedPublishedVersion: 1, ExpectedAgentDigest: hex.EncodeToString(digest[:]), Actor: 1, IdempotencyKey: "binding-command-01"})
	if err != ErrConflict {
		t.Fatalf("err=%v", err)
	}
}
func TestSendersRejectDuplicateInternalStaffAndPrecheckReportsProviderDisabled(t *testing.T) {
	now := time.Now().UTC()
	staff := staffReaderStub{accessport.StaffEligibility{StaffID: 9, Active: true, Eligible: true, EligibilityVersion: 2, RefreshedAt: now}}
	agent := automationport.PublishedAgent{AgentID: 3, PublishedVersion: 2, AutomationType: automationport.AutomationTypeFixedScript, Status: automationport.AgentStatusActive}
	store := executionStoreStub{pkg: segmentdomain.Package{ID: 1, Lifecycle: segmentdomain.Paused}, config: segmentdomain.ConfigurationVersion{ID: 2}, binding: segmentdomain.AutomationBinding{AgentID: 3, AutomationType: agent.AutomationType, AgentPublishedVersion: 2}, senders: segmentdomain.SenderSet{Version: 1, Members: []segmentdomain.Sender{{StaffID: 9, EligibilityVersion: 2, EligibilityRefreshedAt: now}}}, snapshot: segmentport.Snapshot{ID: 4}}
	service, _ := NewExecutionService(directUOW{}, store, publishedAgentStub{agent, true}, staff, false)
	_, err := service.ReplaceSenders(context.Background(), SendersCommand{PackageID: 1, ExpectedPackageVersion: 1, ProviderMemberIDs: []string{"one", "two"}, Actor: 1, IdempotencyKey: "senders-command-01"})
	if err != ErrConflict {
		t.Fatalf("duplicate err=%v", err)
	}
	check, err := service.Precheck(context.Background(), 1)
	if err != nil || check.Ready || len(check.Reasons) == 0 || check.Reasons[len(check.Reasons)-1] != "provider_disabled" {
		t.Fatalf("check=%+v err=%v", check, err)
	}
}

func TestSendersCanReplaceWhitelistForActivePackage(t *testing.T) {
	now := time.Now().UTC()
	staff := staffReaderStub{accessport.StaffEligibility{StaffID: 9, Active: true, Eligible: true, EligibilityVersion: 2, RefreshedAt: now}}
	store := executionStoreStub{pkg: segmentdomain.Package{ID: 1, Version: 1, Lifecycle: segmentdomain.Active}}
	service, _ := NewExecutionService(directUOW{}, store, publishedAgentStub{}, staff, false)
	_, err := service.ReplaceSenders(context.Background(), SendersCommand{PackageID: 1, ExpectedPackageVersion: 1, ProviderMemberIDs: []string{"QianLan"}, Actor: 1, IdempotencyKey: "active-senders-command-01"})
	if err != nil {
		t.Fatalf("active package sender replacement err=%v, want lifecycle gate to allow whitelist updates", err)
	}
}

// OneID decision: the test uses an already-persisted Segment snapshot and
// internal staff facts; it does not resolve or provision customers.
// Persistence/provider decision: Precheck stays read-only. It proves an
// active published Agent may reach the manual dynamic-generation path, while
// automatic policy sends retain their fixed-content restriction.
func TestPrecheckAllowsPublishedDynamicAgentForManualRun(t *testing.T) {
	now := time.Now().UTC()
	content := sha256.Sum256([]byte("dynamic-content"))
	materials := sha256.Sum256([]byte("dynamic-materials"))
	agent := automationport.PublishedAgent{AgentID: 3, PublishedVersion: 2, AutomationType: automationport.AutomationTypeAgent, Status: automationport.AgentStatusActive, ContentDigest: content, MaterialsDigest: materials}
	staff := staffReaderStub{accessport.StaffEligibility{StaffID: 9, Active: true, Eligible: true, EligibilityVersion: 2, RefreshedAt: now}}
	store := executionStoreStub{
		pkg:      segmentdomain.Package{ID: 1, Lifecycle: segmentdomain.Paused},
		config:   segmentdomain.ConfigurationVersion{ID: 2, Definition: []byte(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`)},
		binding:  segmentdomain.AutomationBinding{AgentID: agent.AgentID, AutomationType: agent.AutomationType, AgentPublishedVersion: agent.PublishedVersion, ContentDigest: content, MaterialsDigest: materials, Version: 1},
		senders:  segmentdomain.SenderSet{Version: 1, Members: []segmentdomain.Sender{{StaffID: 9, EligibilityVersion: 2, EligibilityRefreshedAt: now}}},
		snapshot: segmentport.Snapshot{ID: 4},
	}
	service, err := NewExecutionService(directUOW{}, store, publishedAgentStub{agent, true}, staff, true)
	if err != nil {
		t.Fatal(err)
	}
	check, err := service.Precheck(context.Background(), 1)
	if err != nil || !check.Ready || len(check.Reasons) != 0 {
		t.Fatalf("dynamic Agent precheck=%+v err=%v", check, err)
	}
}
