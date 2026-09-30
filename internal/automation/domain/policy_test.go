package domain

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
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

func TestOutboundDeferredCustomerIDsAreCanonicalAndValidated(t *testing.T) {
	approval := int64(9)
	created := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	version, err := NewPolicyVersion(1, 1, 27, automationport.TriggerAudienceMemberEnteredV1, automationport.ActionOutboundMessage, json.RawMessage(`{"agent_id":14,"deferred_customer_ids":[9002,9001,9003],"once_per_customer":true,"defer_before_paid_at":"2026-09-30T10:58:14+08:00"}`), json.RawMessage(`{}`), 100, &approval, 9, created)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(version.ActionConfig), `{"agent_id":14,"deferred_customer_ids":[9001,9002,9003],"once_per_customer":true,"defer_before_paid_at":"2026-09-30T02:58:14Z"}`; got != want {
		t.Fatalf("canonical action config=%s, want %s", got, want)
	}
	if version.Digest == ([32]byte{}) {
		t.Fatal("deferred customer list must be covered by the policy digest")
	}
	withoutOnce, err := NewPolicyVersion(1, 1, 27, automationport.TriggerAudienceMemberEnteredV1, automationport.ActionOutboundMessage, json.RawMessage(`{"agent_id":14,"deferred_customer_ids":[9001,9002,9003]}`), json.RawMessage(`{}`), 100, &approval, 9, created)
	if err != nil || withoutOnce.Digest == version.Digest {
		t.Fatalf("once_per_customer must be optional and covered by the version digest: %#v err=%v", withoutOnce, err)
	}
	for _, raw := range []string{
		`{"agent_id":14,"deferred_customer_ids":[1,1]}`,
		`{"agent_id":14,"deferred_customer_ids":[0]}`,
		`{"agent_id":14,"deferred_customer_ids":[-1]}`,
		`{"agent_id":14,"defer_before_paid_at":"2026-09-30T02:58:14Z"}`,
		`{"agent_id":14,"once_per_customer":true,"defer_before_paid_at":"0001-01-01T00:00:00Z"}`,
		`{"agent_id":14,"once_per_customer":true,"defer_before_paid_at":"not-a-time"}`,
	} {
		if _, err := NewPolicyVersion(1, 2, 27, automationport.TriggerAudienceMemberEnteredV1, automationport.ActionOutboundMessage, json.RawMessage(raw), json.RawMessage(`{}`), 100, &approval, 9, created); !errors.Is(err, ErrInvalidPolicy) {
			t.Errorf("NewPolicyVersion(%s) error=%v, want invalid policy", raw, err)
		}
	}

	// There is no business-level item cap; the existing HTTP request-size
	// limit remains the transport boundary for an audited deployment list.
	ids := make([]int64, 512)
	for index := range ids {
		ids[index] = int64(index + 1)
	}
	largeConfig, err := json.Marshal(struct {
		AgentID             int64   `json:"agent_id"`
		DeferredCustomerIDs []int64 `json:"deferred_customer_ids"`
	}{AgentID: 14, DeferredCustomerIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	largeVersion, err := NewPolicyVersion(1, 3, 27, automationport.TriggerAudienceMemberEnteredV1, automationport.ActionOutboundMessage, largeConfig, json.RawMessage(`{}`), 100, &approval, 9, created)
	if err != nil {
		t.Fatalf("NewPolicyVersion with 512 deferred IDs returned %v", err)
	}
	var canonical struct {
		DeferredCustomerIDs []int64 `json:"deferred_customer_ids"`
	}
	if err := json.Unmarshal(largeVersion.ActionConfig, &canonical); err != nil || len(canonical.DeferredCustomerIDs) != len(ids) {
		t.Fatalf("canonical deferred list has %d IDs, err=%v; want %d", len(canonical.DeferredCustomerIDs), err, len(ids))
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

func TestPolicyVersionAcceptsExplicitNoQuietHours(t *testing.T) {
	approval := int64(9)
	version, err := NewPolicyVersion(1, 1, 27, automationport.TriggerAudienceMemberEnteredV1, automationport.ActionOutboundMessage, json.RawMessage(`{"agent_id":14}`), json.RawMessage(`{}`), 100, &approval, 9, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if string(version.QuietHours) != `{}` {
		t.Fatalf("quiet hours=%s, want explicit empty object", version.QuietHours)
	}
	_, err = NewPolicyVersion(1, 2, 27, automationport.TriggerAudienceMemberEnteredV1, automationport.ActionOutboundMessage, json.RawMessage(`{"agent_id":14}`), json.RawMessage(`{"timezone":"","start":"","end":""}`), 100, &approval, 9, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("empty quiet-hour fields must not masquerade as explicit no quiet hours: %v", err)
	}
}

func TestPolicyRejectsMissingApprovalAndUnsafeLimit(t *testing.T) {
	_, err := NewPolicyVersion(1, 1, 2, automationport.TriggerAudienceMemberEnteredV1, automationport.ActionRecord, json.RawMessage(`{"record_type":"entered"}`), json.RawMessage(`{"timezone":"UTC","start":"22:00","end":"08:00"}`), 100001, nil, 3, time.Now())
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("expected rejection, got %v", err)
	}
}
