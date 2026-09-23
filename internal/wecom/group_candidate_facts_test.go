package wecom

import (
	"context"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"testing"
)

type candidateIdentity struct{ fail bool }

func (c candidateIdentity) VerifiedWeComIdentityForCustomer(_ context.Context, id customerdomain.CustomerID, corp string) (string, bool, error) {
	if c.fail {
		return "", false, errors.New("unavailable")
	}
	if corp != "corp" {
		return "", false, nil
	}
	switch id {
	case 1:
		return "member", true, nil
	case 2:
		return "outside", true, nil
	}
	return "", false, nil
}
func TestGroupCandidatesUnknownMembersDoNotBlockKnownCandidates(t *testing.T) {
	hashes := []string{groupExternalHash("wecom-corp:corp", "member"), groupExternalHash("wecom-corp:corp", "unresolved-member")}
	got, e := outsideGroupCandidates(context.Background(), candidateIdentity{}, "wecom-corp:corp", hashes, []customerdomain.CustomerID{1, 2, 3, 2})
	if e != nil || len(got) != 1 || got[0] != 2 {
		t.Fatalf("bad candidate result %v %v", got, e)
	}
	if groupExternalHash("wecom-corp:other", "member") == hashes[0] {
		t.Fatal("scope not bound")
	}
	if _, e = outsideGroupCandidates(context.Background(), candidateIdentity{fail: true}, "wecom-corp:corp", hashes, []customerdomain.CustomerID{2}); e == nil {
		t.Fatal("identity failure allowed exclusion")
	}
}
