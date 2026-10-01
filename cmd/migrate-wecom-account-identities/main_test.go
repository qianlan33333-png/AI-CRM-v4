package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type fakeContacts struct {
	contacts map[string]wecomport.ExternalContact
	fail     bool
}

func (f fakeContacts) ReadExternalContact(_ context.Context, id string) (wecomport.ExternalContact, error) {
	if f.fail {
		return wecomport.ExternalContact{}, errors.New("Provider unavailable")
	}
	return f.contacts[id], nil
}

func TestCorrectionProviderProof(t *testing.T) {
	valid := fakeContacts{contacts: map[string]wecomport.ExternalContact{"a": {ExternalUserID: "a", UnionID: "union-a"}, "b": {ExternalUserID: "b", UnionID: "union-b"}}}
	for _, tc := range []struct {
		name     string
		reader   fakeContacts
		external [2]string
		scope    string
		ok       bool
	}{
		{"distinct Provider accounts", valid, [2]string{"a", "b"}, "wechat-open-platform:test", true},
		{"same external account", valid, [2]string{"a", "a"}, "wechat-open-platform:test", false},
		{"mismatched external response", fakeContacts{contacts: map[string]wecomport.ExternalContact{"a": {ExternalUserID: "other", UnionID: "union-a"}}}, [2]string{"a", "b"}, "wechat-open-platform:test", false},
		{"missing UnionID", fakeContacts{contacts: map[string]wecomport.ExternalContact{"a": {ExternalUserID: "a"}}}, [2]string{"a", "b"}, "wechat-open-platform:test", false},
		{"same UnionID", fakeContacts{contacts: map[string]wecomport.ExternalContact{"a": {ExternalUserID: "a", UnionID: "same"}, "b": {ExternalUserID: "b", UnionID: "same"}}}, [2]string{"a", "b"}, "wechat-open-platform:test", false},
		{"scope unavailable", valid, [2]string{"a", "b"}, "wechat-open-platform:", false},
		{"read unavailable", fakeContacts{fail: true}, [2]string{"a", "b"}, "wechat-open-platform:test", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := verifyAccounts(context.Background(), tc.reader, identityport.AccountCorrectionPlan{}, "corp", tc.scope, tc.external)
			if (err == nil) != tc.ok {
				t.Fatalf("unexpected proof outcome %v", err)
			}
			if tc.ok && (!cmd.LeftUnion.Valid() || !cmd.RightUnion.Valid()) {
				t.Fatal("missing opaque verified facts")
			}
		})
	}
}

func TestCorrectionPlanRefusesRawIdentitiesAndUnknownFields(t *testing.T) {
	plan := identityport.AccountCorrectionPlan{RunKey: "test", Operator: "reviewer", LeftCustomerID: 1, RightCustomerID: 2, LeftVersion: 1, RightVersion: 1, WrongIdentityID: 3, WrongIdentityVersion: 1, CandidateID: 4, CandidateVersion: 1, ConflictIDs: []int64{5}, HXCSubjectID: 6, HXCSubjectVersion: 1}
	raw, _ := json.Marshal(plan)
	if _, err := loadPlan(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte(`{"unionid":"declared-caller-value"}`), append(append([]byte{}, raw...), []byte(` {}`)...), []byte(`{"left_customer_id":1}`)} {
		if _, err := loadPlan(bad); err == nil {
			t.Fatal("unreviewable plan accepted")
		}
	}
}
