package domain

import (
	"encoding/json"
	"errors"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"testing"
	"time"
)

func TestPolicyVersionClosesTriggerActionAndExecutionPolicy(t *testing.T) {
	approval := int64(9)
	created := time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC)
	v, err := NewPolicyVersion(1, 1, segmentport.PackageID(2), automationport.TriggerAudienceMemberEnteredV1, automationport.ActionOutboundMessage, json.RawMessage(`{"agent_id":7}`), json.RawMessage(`{"timezone":"Asia/Shanghai","start":"22:00","end":"08:00"}`), 1000, &approval, 3, created)
	if err != nil {
		t.Fatal(err)
	}
	if !v.TriggerEnabled || v.Digest == ([32]byte{}) {
		t.Fatalf("unexpected version: %#v", v)
	}
	_, err = NewPolicyVersion(1, 2, 2, automationport.TriggerAudienceMemberEnteredV1, automationport.ActionOutboundMessage, json.RawMessage(`{"agent_id":7,"message":"must not be embedded"}`), json.RawMessage(`{"timezone":"Asia/Shanghai","start":"22:00","end":"08:00"}`), 1000, &approval, 3, created)
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("expected closed action schema, got %v", err)
	}
	_, err = NewPolicyVersion(1, 2, 2, automationport.TriggerAudienceMemberEnteredV1, automationport.ActionRecord, json.RawMessage(`{"record_type":"entered"}`), json.RawMessage(`{"timezone":"Mars/Olympus","start":"22:00","end":"08:00"}`), 1000, &approval, 3, created)
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("expected valid IANA timezone, got %v", err)
	}
}

func TestCustomerTagTriggerIsPersistedDisabled(t *testing.T) {
	approval := int64(4)
	v, err := NewPolicyVersion(1, 1, 2, automationport.TriggerCustomerTagAppliedV1, automationport.ActionRecord, json.RawMessage(`{"record_type":"tag"}`), json.RawMessage(`{"timezone":"UTC","start":"22:00","end":"08:00"}`), 10, &approval, 3, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if v.TriggerEnabled {
		t.Fatal("tag trigger must remain fail-closed until a production event exists")
	}
}

func TestPolicyRejectsMissingApprovalAndUnsafeLimit(t *testing.T) {
	_, err := NewPolicyVersion(1, 1, 2, automationport.TriggerAudienceMemberEnteredV1, automationport.ActionRecord, json.RawMessage(`{"record_type":"entered"}`), json.RawMessage(`{"timezone":"UTC","start":"22:00","end":"08:00"}`), 100001, nil, 3, time.Now())
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("expected rejection, got %v", err)
	}
}
