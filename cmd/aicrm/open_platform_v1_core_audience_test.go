package main

import (
	"context"
	"encoding/json"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"testing"
)

type coreAudienceStub struct {
	calls       int
	source, key string
}

func (s *coreAudienceStub) Products(context.Context) ([]segmentport.CoreProduct, error) {
	s.calls++
	return []segmentport.CoreProduct{{ID: 1, PackageID: 2}, {ID: 2, PackageID: 3}}, nil
}
func (s *coreAudienceStub) CoreMembers(context.Context, int64, string, int) (segmentport.MemberPage, error) {
	s.calls++
	return segmentport.MemberPage{Items: []segmentport.Member{{CustomerID: 7}, {CustomerID: 8}}, NextCursor: "9"}, nil
}
func (s *coreAudienceStub) MemberHistory(context.Context, int64, int64, string, int) (segmentport.CoreAssignmentPage, error) {
	s.calls++
	return segmentport.CoreAssignmentPage{}, nil
}
func (s *coreAudienceStub) MemberDetail(context.Context, int64, int64, string, int) (segmentport.CoreMemberDetail, error) {
	s.calls++
	return segmentport.CoreMemberDetail{}, nil
}
func (s *coreAudienceStub) RecordSupervisedPush(_ context.Context, source, key string, p segmentport.CorePush) (segmentport.CorePush, error) {
	s.calls++
	s.source = source
	s.key = key
	return p, nil
}
func TestV1CoreAudienceAuthorizationAndScope(t *testing.T) {
	for _, tc := range []struct {
		name       string
		op         openplatformport.OperationID
		scope, cap string
		owner      accessdomain.OwnerScope
		input, key string
		want       openplatformport.ErrorCode
	}{
		{"legacy permission rejected", openplatformport.OperationCoreProducts, "read", "external_read", nil, `{}`, "", openplatformport.ErrorPermission},
		{"read client cannot push", openplatformport.OperationCorePushRecord, "read", "audience.push.write", nil, `{}`, "", openplatformport.ErrorPermission},
		{"missing business push identity", openplatformport.OperationCorePushRecord, "write", "audience.push.write", nil, `{"customer_id":7,"package_id":2}`, "", openplatformport.ErrorValidation},
		{"wrong package", openplatformport.OperationCoreMemberHistory, "read", "audience.member.history.read", accessdomain.OwnerScope{"package_id": {"3"}}, `{"customer_id":7,"package_id":2}`, "", openplatformport.ErrorPermission},
		{"wrong customer", openplatformport.OperationCoreMemberOperations, "read", "audience.member.operations.read", accessdomain.OwnerScope{"customer_id": {"8"}}, `{"customer_id":7,"package_id":2}`, "", openplatformport.ErrorNotFound},
		{"wrong corp", openplatformport.OperationCoreProducts, "read", "audience.product.read", accessdomain.OwnerScope{"corp_id": {"other"}}, `{}`, "", openplatformport.ErrorPermission},
		{"unknown scope", openplatformport.OperationCoreProducts, "read", "audience.product.read", accessdomain.OwnerScope{"unknown": {"value"}}, `{}`, "", openplatformport.ErrorPermission},
		{"source spoof rejected", openplatformport.OperationCorePushRecord, "write", "audience.push.write", nil, `{"customer_id":7,"package_id":2,"source":"spoofed"}`, "push-report-00000001", openplatformport.ErrorValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
			s := &coreAudienceStub{}
			e.coreAudience = s
			_, err := e.Invoke(context.Background(), openplatformport.Invocation{Operation: tc.op, Principal: accessdomain.MachinePrincipal{ClientID: "node-a", CorpID: "corp-main", Scopes: []string{tc.scope}, Capabilities: []string{tc.cap}, OwnerScope: tc.owner}, Input: json.RawMessage(tc.input), IdempotencyKey: tc.key})
			if err == nil || openplatformport.ErrorCodeOf(err) != tc.want || s.calls != 0 {
				t.Fatalf("err=%v calls=%d", err, s.calls)
			}
		})
	}
}
func TestV1CoreAudienceScopeFilteringAndPushSource(t *testing.T) {
	e := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
	s := &coreAudienceStub{}
	e.coreAudience = s
	p := accessdomain.MachinePrincipal{ClientID: "node-a", Scopes: []string{"read", "write"}, Capabilities: []string{"audience.product.read", "audience.member.read", "audience.push.write"}, OwnerScope: accessdomain.OwnerScope{"package_id": {"2"}, "customer_id": {"7"}}}
	r, err := e.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCoreProducts, Principal: p, Input: json.RawMessage(`{}`)})
	if err != nil || len(r.Data.(map[string]any)["items"].([]segmentport.CoreProduct)) != 1 {
		t.Fatalf("products=%+v err=%v", r, err)
	}
	r, err = e.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCoreMembers, Principal: p, Input: json.RawMessage(`{"package_id":2}`)})
	if err != nil {
		t.Fatal(err)
	}
	page := r.Data.(segmentport.MemberPage)
	if len(page.Items) != 1 || page.Items[0].CustomerID != 7 || page.NextCursor != "9" {
		t.Fatalf("scope pagination=%+v", page)
	}
	_, err = e.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCorePushRecord, Principal: p, Input: json.RawMessage(`{"push_id":"business-push-1","customer_id":7,"package_id":2,"materials":[{"kind":"miniprogram","id":1}],"status":"reported","status_version":1,"occurred_at":"2026-09-18T01:00:00Z"}`), IdempotencyKey: "push-report-00000001"})
	if err != nil || s.source != "node-a" || s.key != "push-report-00000001" {
		t.Fatalf("push source=%q err=%v", s.source, err)
	}
}
