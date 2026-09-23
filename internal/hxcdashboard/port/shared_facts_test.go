package port

import (
	"testing"
	"time"
)

func TestMembershipWindowKeepsFreeAndUnknownFactsOutOfExpired(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Second), now.Add(time.Second)
	cases := []struct {
		name            string
		facts           SharedFacts
		active, expired bool
	}{
		{"active without expiry", SharedFacts{Availability: SharedFactsAvailable, MembershipRecordFound: true, IsMember: true}, true, false},
		{"active subscription source", SharedFacts{Availability: SharedFactsAvailable, MembershipRecordFound: true, MembershipSource: "subscription", MembershipStatus: "standard", IsMember: true, ExpiresAt: &future}, true, false},
		{"membership expiry equality", SharedFacts{Availability: SharedFactsAvailable, MembershipRecordFound: true, IsMember: true, ExpiresAt: &past}, false, true},
		{"free no member", SharedFacts{Availability: SharedFactsAvailable, Tier: "free", ExpiresAt: &past}, false, false},
		{"explicit expired no date", SharedFacts{Availability: SharedFactsAvailable, MembershipRecordFound: true, MembershipStatus: "expired"}, false, true},
		{"explicit expired legacy casing and whitespace", SharedFacts{Availability: SharedFactsAvailable, MembershipRecordFound: true, MembershipStatus: " EXPIRED "}, false, true},
		{"unavailable", SharedFacts{Availability: SharedFactsUnavailable, MembershipRecordFound: true, IsMember: true, ExpiresAt: &future}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.facts.ActiveAt(now) != tc.active || tc.facts.ExpiredAt(now) != tc.expired {
				t.Fatalf("facts=%+v active=%v expired=%v", tc.facts, tc.facts.ActiveAt(now), tc.facts.ExpiredAt(now))
			}
		})
	}
}
