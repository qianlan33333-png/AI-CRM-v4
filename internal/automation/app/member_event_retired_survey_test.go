package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type retiredSurveyMemberEventAudience struct {
	missingPolicyAudience
	retired map[segmentport.ConfigurationVersionID]bool
	current bool
}

func (reader retiredSurveyMemberEventAudience) SurveyOwnerSelectionRetiredAtWithin(_ context.Context, id segmentport.ConfigurationVersionID) (bool, error) {
	return reader.retired[id], nil
}

func (reader retiredSurveyMemberEventAudience) SurveyOwnerSelectionRetiredForPackageWithin(context.Context, segmentport.PackageID) (bool, error) {
	return reader.current, nil
}

type retiredSurveyMemberEventSnapshots struct{ snapshot segmentport.Snapshot }

func (reader retiredSurveyMemberEventSnapshots) PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error) {
	return reader.snapshot, true, nil
}

func (reader retiredSurveyMemberEventSnapshots) Snapshot(_ context.Context, id segmentport.SnapshotID) (segmentport.Snapshot, bool, error) {
	return reader.snapshot, reader.snapshot.ID == id, nil
}

func (retiredSurveyMemberEventSnapshots) Members(context.Context, segmentport.SnapshotID, string, int) (segmentport.MemberPage, error) {
	return segmentport.MemberPage{}, nil
}

func TestRetiredSurveyMemberEventCannotReachOutboundAcceptance(t *testing.T) {
	contentDigest := sha256.Sum256([]byte("published outbound content"))
	configuration := segmentport.ExecutionConfiguration{
		PackageID: 27, PackageVersion: 3, ConfigurationVersionID: 44, Ready: true,
		Snapshot: segmentport.Snapshot{ID: 903, PackageID: 27, ConfigurationVersionID: 44, State: segmentport.SnapshotPublished},
		AgentID:  73, AgentPublishedVersion: 2, ContentDigest: contentDigest,
		BindingVersion: 5, SenderSetVersion: 6, SenderStaffIDs: []int64{17},
	}
	retiredID := segmentport.ConfigurationVersionID(42)
	audience := retiredSurveyMemberEventAudience{
		missingPolicyAudience: missingPolicyAudience{configuration: configuration},
		retired:               map[segmentport.ConfigurationVersionID]bool{retiredID: true},
	}
	store := &missingPolicyReceiptStore{activePolicies: []automationdomain.PolicyVersion{{
		ID: 71, PolicyID: 19, Version: 1, PackageID: 27,
		TriggerKind: automationport.TriggerAudienceMemberEnteredV1, TriggerEnabled: true,
		ActionKind: automationport.ActionOutboundMessage, ActionConfig: json.RawMessage(`{"agent_id":73}`),
		QuietHours: json.RawMessage(`{}`), SingleRunLimit: 10, Digest: [32]byte{7}, CreatedBy: 17,
	}}}
	snapshots := retiredSurveyMemberEventSnapshots{snapshot: segmentport.Snapshot{
		ID: 902, PackageID: 27, ConfigurationVersionID: retiredID, State: segmentport.SnapshotPublished,
	}}
	service, err := NewRuntimeService(directRuntimeUOW{}, store, audience, snapshots, 10)
	if err != nil {
		t.Fatal(err)
	}
	published := automationport.OutboundPublishedContent{
		AgentID: 73, PublishedVersion: 2,
		Content: automationport.FixedContentPackage{ContentText: "member entered"}, ContentDigest: contentDigest,
	}
	reads := 0
	service.content = countingPublishedContent{content: published, calls: &reads}
	service.contentFreezer = countingContentFreezer{calls: new(int)}
	messages := &missingPolicyMessageAccepter{}
	service.messages = messages
	event := segmentport.MemberEnteredV1{EventID: "audmem_retired_survey_001", PackageID: 27, SnapshotID: 902, ConfigurationVersionID: retiredID, CustomerID: 9901, OccurredAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}

	enrollments, err := service.EnrollAudienceMember(context.Background(), event)
	if len(enrollments) != 0 || !errors.Is(err, ErrRuntimeNotReady) {
		t.Fatalf("retired Survey event enrollments=%+v err=%v; want a retryable pre-acceptance block", enrollments, err)
	}
	if reads != 1 || messages.calls != 0 || store.runCalls != 0 || len(store.enrollments) != 0 || len(store.receipts) != 0 {
		t.Fatalf("retired Survey event reached outbound or wrote runtime state: content reads=%d messages=%d runs=%d enrollments=%d receipts=%d", reads, messages.calls, store.runCalls, len(store.enrollments), len(store.receipts))
	}
}

func TestMemberEventSurveyRetirementChecksEventAndSnapshotConfiguration(t *testing.T) {
	configuration := segmentport.ExecutionConfiguration{PackageID: 27, ConfigurationVersionID: 44}
	compatibleID := segmentport.ConfigurationVersionID(44)
	retiredID := segmentport.ConfigurationVersionID(42)
	tests := []struct {
		name    string
		eventID segmentport.ConfigurationVersionID
		snapID  segmentport.ConfigurationVersionID
		retired map[segmentport.ConfigurationVersionID]bool
		wantErr bool
	}{
		{name: "compatible legacy all empty remains usable", eventID: compatibleID, snapID: compatibleID, retired: map[segmentport.ConfigurationVersionID]bool{}},
		{name: "retired event configuration", eventID: retiredID, snapID: retiredID, retired: map[segmentport.ConfigurationVersionID]bool{retiredID: true}, wantErr: true},
		{name: "retired snapshot configuration cannot be hidden by event field", eventID: compatibleID, snapID: retiredID, retired: map[segmentport.ConfigurationVersionID]bool{retiredID: true}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			audience := retiredSurveyMemberEventAudience{missingPolicyAudience: missingPolicyAudience{configuration: configuration}, retired: test.retired}
			snapshots := retiredSurveyMemberEventSnapshots{snapshot: segmentport.Snapshot{ID: 902, PackageID: 27, ConfigurationVersionID: test.snapID, State: segmentport.SnapshotPublished}}
			service, err := NewRuntimeService(directRuntimeUOW{}, &missingPolicyReceiptStore{}, audience, snapshots, 10)
			if err != nil {
				t.Fatal(err)
			}
			err = service.rejectRetiredSurveyMemberEventWithin(context.Background(), segmentport.MemberEnteredV1{PackageID: 27, SnapshotID: 902, ConfigurationVersionID: test.eventID}, snapshots.snapshot)
			if test.wantErr && !errors.Is(err, ErrRuntimeNotReady) {
				t.Fatalf("error=%v; want ErrRuntimeNotReady", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("error=%v; want compatible Survey definition to remain allowed", err)
			}
		})
	}
}
