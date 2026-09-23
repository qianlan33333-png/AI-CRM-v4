package domain

import "testing"

func TestValidGroupInviteJoinURLAcceptsPublicGroupLandingPage(t *testing.T) {
	if !ValidGroupInviteJoinURL("https://www.youcangogogo.com/gi/40e73b50a900267cfc3a384609e884a4ea77b49139bae075") {
		t.Fatal("public group landing page should be a valid group invite URL")
	}
}

func TestValidGroupInviteJoinURLRejectsUnsafePublicVariants(t *testing.T) {
	for _, value := range []string{
		"https://www.youcangogogo.com/gi/token?next=https://example.com",
		"https://www.youcangogogo.com/gi/token/extra",
		"https://user@www.youcangogogo.com/gi/token",
	} {
		if ValidGroupInviteJoinURL(value) {
			t.Fatalf("unsafe public group invite URL accepted: %s", value)
		}
	}
}
