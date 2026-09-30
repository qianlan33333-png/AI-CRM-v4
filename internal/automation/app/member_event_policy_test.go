package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type missingPolicyAudience struct {
	configuration segmentport.ExecutionConfiguration
}

func (audience missingPolicyAudience) AudienceExecutionConfiguration(_ context.Context, packageID segmentport.PackageID) (segmentport.ExecutionConfiguration, error) {
	configuration := audience.configuration
	if configuration.PackageID == 0 {
		configuration.PackageID = packageID
	}
	return configuration, nil
}

type missingPolicySnapshots struct{}

func (missingPolicySnapshots) PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error) {
	return segmentport.Snapshot{}, false, nil
}
func (missingPolicySnapshots) Snapshot(context.Context, segmentport.SnapshotID) (segmentport.Snapshot, bool, error) {
	return segmentport.Snapshot{}, false, nil
}
func (missingPolicySnapshots) Members(context.Context, segmentport.SnapshotID, string, int) (segmentport.MemberPage, error) {
	return segmentport.MemberPage{}, nil
}

type missingPolicyReceiptStore struct {
	RuntimeStore
	activePolicyReads int
	policyReads       [][]automationdomain.PolicyVersion
	activePolicies    []automationdomain.PolicyVersion
	receipts          map[[32]byte]RuntimeReceipt
	nextID            int64
	reserveCalls      int
	completeCalls     int
	lockCalls         int
	enrollments       map[memberEventEnrollmentKey]automationdomain.Enrollment
	nextEnrollmentID  int64
	runCalls          int
	bindEffectCalls   int
	runtimeFactCalls  int
}

type memberEventEnrollmentKey struct {
	policyVersionID int64
	eventDigest     [32]byte
	customerID      int64
}

func (s *missingPolicyReceiptStore) ActivePoliciesForPackage(context.Context, int64) ([]automationdomain.PolicyVersion, error) {
	read := s.activePolicyReads
	s.activePolicyReads++
	if len(s.policyReads) > 0 {
		if read >= len(s.policyReads) {
			read = len(s.policyReads) - 1
		}
		return append([]automationdomain.PolicyVersion(nil), s.policyReads[read]...), nil
	}
	return append([]automationdomain.PolicyVersion(nil), s.activePolicies...), nil
}

func (s *missingPolicyReceiptStore) EnrollmentForSource(_ context.Context, policyVersionID int64, eventDigest [32]byte, customerID int64) (automationdomain.Enrollment, bool, error) {
	enrollment, found := s.enrollments[memberEventEnrollmentKey{policyVersionID: policyVersionID, eventDigest: eventDigest, customerID: customerID}]
	return enrollment, found, nil
}

func (s *missingPolicyReceiptStore) CreateEnrollment(_ context.Context, enrollment automationdomain.Enrollment) (automationdomain.Enrollment, bool, error) {
	if s.enrollments == nil {
		s.enrollments = map[memberEventEnrollmentKey]automationdomain.Enrollment{}
	}
	key := memberEventEnrollmentKey{policyVersionID: enrollment.PolicyVersionID, eventDigest: enrollment.SourceEventDigest, customerID: enrollment.CustomerID}
	if existing, found := s.enrollments[key]; found {
		return existing, false, nil
	}
	s.nextEnrollmentID++
	enrollment.ID = s.nextEnrollmentID
	s.enrollments[key] = enrollment
	return enrollment, true, nil
}

func (s *missingPolicyReceiptStore) RuntimeReceipt(_ context.Context, operation, actorScope string, keyDigest, payloadDigest [32]byte) (RuntimeReceipt, bool, error) {
	receipt, found := s.receipts[keyDigest]
	if !found || receipt.Operation != operation || receipt.ActorScope != actorScope {
		return RuntimeReceipt{}, false, nil
	}
	if receipt.PayloadDigest != payloadDigest {
		return RuntimeReceipt{}, false, ErrRuntimeConflict
	}
	return receipt, true, nil
}

func (s *missingPolicyReceiptStore) LockMemberEventDispatch(_ context.Context, _ [32]byte) error {
	s.lockCalls++
	return nil
}

func (s *missingPolicyReceiptStore) ReserveRuntime(_ context.Context, in RuntimeReservation) (RuntimeReceipt, bool, error) {
	s.reserveCalls++
	if s.receipts == nil {
		s.receipts = map[[32]byte]RuntimeReceipt{}
	}
	if prior, ok := s.receipts[in.KeyDigest]; ok {
		if prior.PayloadDigest != in.PayloadDigest {
			return RuntimeReceipt{}, false, ErrRuntimeConflict
		}
		return prior, false, nil
	}
	s.nextID++
	receipt := RuntimeReceipt{ID: s.nextID, Operation: in.Operation, ActorScope: in.ActorScope, State: "reserved", KeyDigest: in.KeyDigest, PayloadDigest: in.PayloadDigest}
	s.receipts[in.KeyDigest] = receipt
	return receipt, true, nil
}

func (s *missingPolicyReceiptStore) CompleteRuntime(_ context.Context, id int64, result json.RawMessage, _ time.Time) error {
	s.completeCalls++
	for key, receipt := range s.receipts {
		if receipt.ID == id && receipt.State == "reserved" {
			receipt.State = "completed"
			receipt.Result = append(json.RawMessage(nil), result...)
			s.receipts[key] = receipt
			return nil
		}
	}
	return ErrRuntimeConflict
}

func (s *missingPolicyReceiptStore) ListMemberEventDispatchDiagnostics(_ context.Context, packageID, _ int64, _ int) ([]MemberEventDispatchDiagnostic, string, error) {
	items := []MemberEventDispatchDiagnostic{}
	for _, receipt := range s.receipts {
		if receipt.Operation != MemberEventMissingPolicyOperation || receipt.State != "completed" {
			continue
		}
		var item MemberEventDispatchDiagnostic
		if err := json.Unmarshal(receipt.Result, &item); err != nil {
			return nil, "", err
		}
		if item.PackageID == packageID {
			item.ID = receipt.ID
			items = append(items, item)
		}
	}
	return items, "", nil
}

func (s *missingPolicyReceiptStore) AppendRuntimeFact(context.Context, RuntimeFact) error {
	s.runtimeFactCalls++
	return nil
}

func (s *missingPolicyReceiptStore) CreateRun(_ context.Context, run automationdomain.RuntimeRun, recipients []automationdomain.RuntimeRecipient) (automationdomain.RuntimeRun, []automationdomain.RuntimeRecipient, error) {
	s.runCalls++
	run.ID = int64(s.runCalls)
	for index := range recipients {
		recipients[index].ID = int64(index + 1)
		recipients[index].RunID = run.ID
	}
	return run, recipients, nil
}

func (s *missingPolicyReceiptStore) BindRecipientEffect(context.Context, int64, string, time.Time) error {
	s.bindEffectCalls++
	return nil
}

type missingPolicyPublishedContent struct {
	content automationport.OutboundPublishedContent
}

func (reader missingPolicyPublishedContent) OutboundPublishedContent(_ context.Context, agentID automationport.AgentID, version int64) (automationport.OutboundPublishedContent, bool, error) {
	return reader.content, reader.content.AgentID == agentID && reader.content.PublishedVersion == version, nil
}

type missingPolicyContentFreezer struct{}

func (missingPolicyContentFreezer) FreezeOutboundContent(context.Context, automationport.OutboundPublishedContent) (json.RawMessage, [32]byte, error) {
	content := json.RawMessage(`{"content_text":"member entered"}`)
	return content, sha256.Sum256(content), nil
}

type missingPolicyMessageAccepter struct{ calls int }

func (accepter *missingPolicyMessageAccepter) AcceptMessageWithin(context.Context, outboundport.MessageIntent) (outboundport.MessageAcceptance, error) {
	accepter.calls++
	return outboundport.MessageAcceptance{MessageIntentID: int64(accepter.calls), EffectID: "effect-member-event"}, nil
}

func TestMemberEnteredWithoutActivePolicyPersistsIdempotentDiagnosticOnly(t *testing.T) {
	store := &missingPolicyReceiptStore{}
	service, err := NewRuntimeService(directRuntimeUOW{}, store, missingPolicyAudience{}, missingPolicySnapshots{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	event := segmentport.MemberEnteredV1{
		EventID:                "audmem_902_87654321",
		PackageID:              27,
		SnapshotID:             902,
		ConfigurationVersionID: 43,
		CustomerID:             87654321,
		OccurredAt:             time.Date(2026, 9, 29, 15, 9, 0, 0, time.UTC),
	}
	if enrollments, err := service.EnrollAudienceMember(context.Background(), event); err != nil || len(enrollments) != 0 {
		t.Fatalf("first dispatch enrollments=%v err=%v", enrollments, err)
	}
	if enrollments, err := service.EnrollAudienceMember(context.Background(), event); err != nil || len(enrollments) != 0 {
		t.Fatalf("replayed dispatch enrollments=%v err=%v", enrollments, err)
	}
	if store.activePolicyReads != 2 || store.reserveCalls != 1 || store.completeCalls != 1 || store.lockCalls != 1 || len(store.receipts) != 1 {
		t.Fatalf("active reads/reserve/complete/locks/receipts=%d/%d/%d/%d/%d", store.activePolicyReads, store.reserveCalls, store.completeCalls, store.lockCalls, len(store.receipts))
	}
	items, next, err := service.MemberEventDispatchDiagnostics(context.Background(), int64(event.PackageID), 0, 50)
	if err != nil || next != "" || len(items) != 1 {
		t.Fatalf("diagnostics=%+v next=%q err=%v", items, next, err)
	}
	item := items[0]
	wantDigest := sha256.Sum256([]byte(event.EventID))
	if item.ID < 1 || item.PackageID != int64(event.PackageID) || item.SnapshotID != int64(event.SnapshotID) || item.ConfigurationVersionID != int64(event.ConfigurationVersionID) || item.EventDigest != hex.EncodeToString(wantDigest[:]) || item.State != "unconfigured" || item.Reason != "no_active_policy" || !item.OccurredAt.Equal(event.OccurredAt) || item.RecordedAt.IsZero() {
		t.Fatalf("diagnostic=%+v", item)
	}
	stored := ""
	for _, receipt := range store.receipts {
		stored = string(receipt.Result)
	}
	if strings.Contains(stored, event.EventID) || strings.Contains(stored, "87654321") {
		t.Fatalf("diagnostic receipt contains raw event/customer identifier: %s", stored)
	}
}

func TestMemberEventMissingPolicyReceiptBlocksReplayAfterPolicyActivation(t *testing.T) {
	contentDigest := sha256.Sum256([]byte("published outbound content"))
	configuration := segmentport.ExecutionConfiguration{
		PackageID: 27, PackageVersion: 3, ConfigurationVersionID: 43, Ready: true,
		AgentID: 73, AgentPublishedVersion: 2, ContentDigest: contentDigest,
		BindingVersion: 5, SenderSetVersion: 6, SenderStaffIDs: []int64{17},
	}
	store := &missingPolicyReceiptStore{}
	service, err := NewRuntimeService(directRuntimeUOW{}, store, missingPolicyAudience{configuration: configuration}, missingPolicySnapshots{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	published := automationport.OutboundPublishedContent{
		AgentID: automationport.AgentID(configuration.AgentID), PublishedVersion: configuration.AgentPublishedVersion,
		Content: automationport.FixedContentPackage{ContentText: "member entered"}, ContentDigest: contentDigest,
	}
	service.content = missingPolicyPublishedContent{content: published}
	service.contentFreezer = missingPolicyContentFreezer{}
	messages := &missingPolicyMessageAccepter{}
	service.messages = messages

	event := segmentport.MemberEnteredV1{
		EventID: "audmem_release_desk_001", PackageID: 27, SnapshotID: 902,
		ConfigurationVersionID: 43, CustomerID: 87654321,
		OccurredAt: time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC),
	}
	if enrollments, dispatchErr := service.EnrollAudienceMember(context.Background(), event); dispatchErr != nil || len(enrollments) != 0 {
		t.Fatalf("unconfigured dispatch enrollments=%v err=%v", enrollments, dispatchErr)
	}
	if len(store.receipts) != 1 || store.completeCalls != 1 || store.runCalls != 0 || messages.calls != 0 {
		t.Fatalf("initial receipt/enrollments/runs/outbound=%d/%d/%d/%d", len(store.receipts), len(store.enrollments), store.runCalls, messages.calls)
	}

	approval := int64(17)
	store.activePolicies = []automationdomain.PolicyVersion{{
		ID: 51, PolicyID: 9, Version: 1, PackageID: event.PackageID,
		TriggerKind: automationport.TriggerAudienceMemberEnteredV1, TriggerEnabled: true,
		ActionKind: automationport.ActionOutboundMessage, ActionConfig: json.RawMessage(`{"agent_id":73}`),
		QuietHours: json.RawMessage(`{}`), SingleRunLimit: 10, ApprovalStaffID: &approval,
		Digest: [32]byte{1}, CreatedBy: approval,
	}}
	if enrollments, dispatchErr := service.EnrollAudienceMember(context.Background(), event); dispatchErr != nil || len(enrollments) != 0 {
		t.Fatalf("same-event replay after activation enrollments=%v err=%v", enrollments, dispatchErr)
	}
	changedPayload := event
	changedPayload.CustomerID++
	if enrollments, dispatchErr := service.EnrollAudienceMember(context.Background(), changedPayload); len(enrollments) != 0 || dispatchErr == nil || !errors.Is(dispatchErr, ErrRuntimeConflict) {
		t.Fatalf("same EventID with changed payload enrollments=%v err=%v", enrollments, dispatchErr)
	}
	if store.activePolicyReads != 2 || store.lockCalls != 1 || len(store.enrollments) != 0 || store.runCalls != 0 || messages.calls != 0 {
		t.Fatalf("replay activated policy reads/locks/enrollments/runs/outbound=%d/%d/%d/%d/%d", store.activePolicyReads, store.lockCalls, len(store.enrollments), store.runCalls, messages.calls)
	}

	newEvent := event
	newEvent.EventID = "audmem_release_desk_002"
	enrollments, dispatchErr := service.EnrollAudienceMember(context.Background(), newEvent)
	if dispatchErr != nil || len(enrollments) != 1 {
		t.Fatalf("new event enrollments=%v err=%v", enrollments, dispatchErr)
	}
	if len(store.enrollments) != 1 || store.runCalls != 1 || messages.calls != 1 || store.bindEffectCalls != 1 || store.activePolicyReads != 4 || store.lockCalls != 2 {
		t.Fatalf("new event enrollment/run/outbound/effect-bind/policy-reads/locks=%d/%d/%d/%d/%d/%d", len(store.enrollments), store.runCalls, messages.calls, store.bindEffectCalls, store.activePolicyReads, store.lockCalls)
	}
	items, _, diagnosticsErr := service.MemberEventDispatchDiagnostics(context.Background(), int64(event.PackageID), 0, 50)
	if diagnosticsErr != nil || len(items) != 1 || items[0].Reason != "no_active_policy" {
		t.Fatalf("diagnostics after activation=%+v err=%v", items, diagnosticsErr)
	}
}

func TestMemberEnteredPolicyPausedDuringDispatchStillGetsDiagnostic(t *testing.T) {
	store := &missingPolicyReceiptStore{policyReads: [][]automationdomain.PolicyVersion{{{
		ID: 4, PolicyID: 3, Version: 1, PackageID: 27, TriggerEnabled: true,
		ActionKind: automationport.ActionRecord, ActionConfig: json.RawMessage(`{"record_type":"entered"}`),
		Digest: [32]byte{1}, CreatedBy: 7,
	}}, nil}}
	service, err := NewRuntimeService(directRuntimeUOW{}, store, missingPolicyAudience{}, missingPolicySnapshots{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	event := segmentport.MemberEnteredV1{EventID: "audmem_904_87654323", PackageID: 27, SnapshotID: 904, ConfigurationVersionID: 43, CustomerID: 87654323, OccurredAt: time.Date(2026, 9, 29, 15, 11, 0, 0, time.UTC)}
	if enrollments, err := service.EnrollAudienceMember(context.Background(), event); err != nil || len(enrollments) != 0 {
		t.Fatalf("dispatch enrollments=%v err=%v", enrollments, err)
	}
	if store.activePolicyReads != 3 || store.completeCalls != 1 {
		t.Fatalf("active policy reads=%d diagnostic receipts=%d", store.activePolicyReads, store.completeCalls)
	}
	items, _, err := service.MemberEventDispatchDiagnostics(context.Background(), int64(event.PackageID), 0, 50)
	if err != nil || len(items) != 1 || items[0].Reason != "no_active_policy" {
		t.Fatalf("diagnostics=%+v err=%v", items, err)
	}
}
