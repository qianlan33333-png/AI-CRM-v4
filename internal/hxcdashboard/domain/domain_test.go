package domain

import (
	"testing"
	"time"
)

func TestClassifyPartitionsEveryCurrentUser(t *testing.T) {
	asOf := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	future, equal := asOf.Add(time.Hour), asOf
	cases := []struct {
		name string
		row  SourceRow
		want Stage
	}{
		{"active used", SourceRow{SubscriptionTier: "pro", SubscriptionExpiresAt: &future, LastUsedAt: &asOf}, ActiveUsed},
		{"active unused", SourceRow{SubscriptionTier: "pro", SubscriptionExpiresAt: &future}, ActiveUnused},
		{"free", SourceRow{SubscriptionTier: "free", SubscriptionExpiresAt: &future, LastUsedAt: &asOf}, RegisteredNoActiveMembership},
		{"expiry boundary", SourceRow{SubscriptionTier: "pro", SubscriptionExpiresAt: &equal}, RegisteredNoActiveMembership},
		{"missing expiry", SourceRow{SubscriptionTier: "pro"}, RegisteredNoActiveMembership},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.row, asOf); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestSubjectIsStableAndOpaque(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	a, ref, err := Subject(key, "raw-user-id")
	b, _, _ := Subject(key, "raw-user-id")
	if err != nil || a != b || ref == "" || ref == "raw-user-id" {
		t.Fatalf("invalid subject: %s %v", ref, err)
	}
}
