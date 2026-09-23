package main

import (
	"errors"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/cutoverproof"
	wp "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"testing"
)

func TestLiveVerificationFailClosed(t *testing.T) {
	r := p.Row{UnionID: "opaque", PrimaryExternalID: "external"}
	c := wp.ExternalContact{UnionID: "opaque", ExternalUserID: "external", FollowInfo: []wp.ExternalContactFollowInfo{{EmployeeID: "employee"}}}
	if verificationState(r, c, nil) != "verified" {
		t.Fatal("exact live relation rejected")
	}
	if verificationState(r, c, errors.New("provider unavailable")) != "provider_failed" {
		t.Fatal("provider error accepted")
	}
	c.UnionID = "different"
	if verificationState(r, c, nil) != "provider_mismatch" {
		t.Fatal("cross subject response accepted")
	}
	c.UnionID = "opaque"
	c.ExternalUserID = "different"
	if verificationState(r, c, nil) != "provider_mismatch" {
		t.Fatal("cross external response accepted")
	}
	c.ExternalUserID = "external"
	c.FollowInfo = nil
	if verificationState(r, c, nil) != "provider_mismatch" {
		t.Fatal("missing follow evidence accepted")
	}
}
