package app

import (
	"context"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type testUOW struct{}

func (testUOW) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type testIdentity struct {
	seen      []identityport.HXCSubject
	applies   map[[32]byte]int
	completed int
}

func (resolver *testIdentity) InspectHXCSubjects(_ context.Context, subjects []identityport.HXCSubject) ([]identityport.HXCSubjectResult, error) {
	resolver.seen = append(resolver.seen, subjects...)
	out := make([]identityport.HXCSubjectResult, 0, len(subjects))
	for _, subject := range subjects {
		result := identityport.HXCSubjectResult{Position: subject.Position, Disposition: identityport.HXCUnmatched, MatchedBy: identityport.HXCMatchNone, Reason: identityport.HXCReasonMissingIdentity}
		if subject.UnionID != "" {
			result = identityport.HXCSubjectResult{Position: subject.Position, Disposition: identityport.HXCMatched, MatchedBy: identityport.HXCMatchUnionID, Reason: identityport.HXCReasonMatchedUnionID, CustomerID: customerdomain.CustomerID(77)}
		}
		out = append(out, result)
	}
	return out, nil
}
func (resolver *testIdentity) ApplyHXCSubject(_ context.Context, subject identityport.HXCSubject) (identityport.HXCSubjectResult, error) {
	result, err := resolver.InspectHXCSubjects(context.Background(), []identityport.HXCSubject{subject})
	if resolver.applies == nil {
		resolver.applies = map[[32]byte]int{}
	}
	resolver.applies[subject.SubjectDigest]++
	result[0].Replayed = resolver.applies[subject.SubjectDigest] > 1
	return result[0], err
}
func (resolver *testIdentity) CompleteHXCSnapshot(context.Context, [][32]byte) error {
	resolver.completed++
	return nil
}

func TestProjectUsesOnlyScopedUnionIDAndDetectsDuplicateAccountConflict(t *testing.T) {
	resolver := &testIdentity{}
	service := Service{Scope: "wechat-open-platform:platform-1", SubjectKey: []byte("01234567890123456789012345678901"), Identity: resolver, UnionIDVerified: true, UOW: testUOW{}}
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	projection, err := service.project(context.Background(), hxcport.Snapshot{AsOf: now, Rows: []domain.SourceRow{{HXCUserID: "a", UnionID: "u-a", Phone: "+86 138-0013-8000", SubscriptionTier: "pro", SubscriptionExpiresAt: &future, LastUsedAt: &now, SourceUpdatedAt: now}, {HXCUserID: "b", UnionID: "u-b", SubscriptionTier: "pro", SubscriptionExpiresAt: &future, SourceUpdatedAt: now}, {HXCUserID: "c", SubscriptionTier: "free", SourceUpdatedAt: now}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolver.seen) != 3 || resolver.seen[0].UnionIDScope != "wechat-open-platform:platform-1" || resolver.seen[0].Phone != "13800138000" {
		t.Fatalf("unexpected identity input: %#v", resolver.seen)
	}
	if projection.Counts.Total != 3 || projection.Counts.ActiveUsed != 1 || projection.Counts.ActiveUnused != 1 || projection.Counts.RegisteredNoActiveMembership != 1 {
		t.Fatalf("bad funnel counts: %#v", projection.Counts)
	}
	if projection.Counts.Conflict != 2 || projection.Counts.Unmatched != 1 || projection.Counts.Matched != 0 {
		t.Fatalf("bad identity counts: %#v", projection.Counts)
	}
	for _, row := range projection.Rows {
		if row.HXCUserID != "" || row.UnionID != "" || row.Phone != "" {
			t.Fatal("raw HXC identity survived projection")
		}
	}
}

func TestProjectApplyVerifiesExactReplayForEverySubject(t *testing.T) {
	resolver := &testIdentity{}
	service := Service{Scope: "wechat-open-platform:platform-1", SubjectKey: []byte("01234567890123456789012345678901"), Identity: resolver, UnionIDVerified: true, UOW: testUOW{}}
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	projection, err := service.project(context.Background(), hxcport.Snapshot{AsOf: now, Rows: []domain.SourceRow{{HXCUserID: "a", UnionID: "u-a", SourceUpdatedAt: now}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if projection.IdentityReplayVerified != 1 || resolver.completed != 1 {
		t.Fatalf("replay closure not proven: projection=%#v completed=%d", projection, resolver.completed)
	}
}

// accountIdentity exercises HXC's batch decision while Identity still owns
// matching and the ambiguity policy. Strong and phone-only results share root77.
type accountIdentity struct {
	testIdentity
}

func (r *accountIdentity) InspectHXCSubjects(_ context.Context, subjects []identityport.HXCSubject) ([]identityport.HXCSubjectResult, error) {
	r.seen = append(r.seen, subjects...)
	out := make([]identityport.HXCSubjectResult, 0, len(subjects))
	for _, subject := range subjects {
		result := identityport.HXCSubjectResult{Position: subject.Position, Disposition: identityport.HXCUnmatched, MatchedBy: identityport.HXCMatchNone, Reason: identityport.HXCReasonMissingIdentity}
		ambiguous := subject.PhoneAssociationAmbiguous && subject.ConflictReason == identityport.HXCReasonDuplicateCustomer
		switch {
		case subject.ConflictReason != "" && !ambiguous:
			result.Disposition, result.Reason = identityport.HXCConflict, subject.ConflictReason
		case subject.UnionIDVerified && (subject.UnionID == "strong" || subject.UnionID == "second"):
			result.Disposition, result.MatchedBy, result.Reason = identityport.HXCMatched, identityport.HXCMatchUnionID, identityport.HXCReasonMatchedUnionID
			result.CustomerID, result.UnionCustomerID = 77, 77
		case !ambiguous && subject.Phone != "":
			result.Disposition, result.MatchedBy, result.Reason = identityport.HXCMatched, identityport.HXCMatchPhone, identityport.HXCReasonMatchedPhone
			result.CustomerID, result.PhoneCustomerID = 77, 77
		case subject.UnionID != "":
			result.Reason = identityport.HXCReasonNoMatch
		}
		out = append(out, result)
	}
	return out, nil
}
func (r *accountIdentity) ApplyHXCSubject(ctx context.Context, subject identityport.HXCSubject) (identityport.HXCSubjectResult, error) {
	values, err := r.InspectHXCSubjects(ctx, []identityport.HXCSubject{subject})
	if r.applies == nil {
		r.applies = map[[32]byte]int{}
	}
	r.applies[subject.SubjectDigest]++
	values[0].Replayed = r.applies[subject.SubjectDigest] > 1
	return values[0], err
}
func TestProjectPreservesUniqueUnionAccountWithoutBorrowingPhone(t *testing.T) {
	cases := []struct {
		name                          string
		unions                        []string
		verified                      bool
		matched, unmatched, conflicts int64
	}{
		{"strong-and-phone", []string{"strong", ""}, true, 1, 1, 0},
		{"strong-and-unknown-union", []string{"strong", "unknown"}, true, 1, 1, 0},
		{"multiple-strong", []string{"strong", "second"}, true, 0, 0, 2},
		{"only-phone", []string{"", ""}, true, 0, 0, 2},
		{"unverified-union", []string{"strong", ""}, false, 0, 0, 2},
		{"duplicate-union", []string{"strong", "strong"}, true, 0, 0, 2},
	}
	for _, tc := range cases {
		for _, apply := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/preview", true: "/apply"}[apply], func(t *testing.T) {
				resolver := &accountIdentity{}
				service := Service{Scope: "wechat-open-platform:platform-1", SubjectKey: []byte("01234567890123456789012345678901"), Identity: resolver, UnionIDVerified: tc.verified, UOW: testUOW{}}
				now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
				snapshot := hxcport.Snapshot{AsOf: now, Complete: true, Rows: []domain.SourceRow{
					{HXCUserID: "first", UnionID: tc.unions[0], Phone: "13800138000", SourceUpdatedAt: now},
					{HXCUserID: "second", UnionID: tc.unions[1], Phone: "13900139000", SourceUpdatedAt: now},
				}}
				projection, err := service.project(context.Background(), snapshot, apply)
				if err != nil {
					t.Fatal(err)
				}
				if projection.Counts.Matched != tc.matched || projection.Counts.Unmatched != tc.unmatched || projection.Counts.Conflict != tc.conflicts {
					t.Fatalf("counts=%+v", projection.Counts)
				}
				if apply && projection.IdentityReplayVerified != 2 {
					t.Fatal("exact replay not verified")
				}
				for _, row := range projection.Rows {
					if row.Phone != "" || row.UnionID != "" || row.HXCUserID != "" {
						t.Fatal("raw identity in projection")
					}
				}
				if tc.matched == 1 {
					found := false
					for _, input := range resolver.seen {
						if input.PhoneAssociationAmbiguous {
							found = true
							if input.Position != 1 || input.Phone != "13900139000" || input.ConflictReason != identityport.HXCReasonDuplicateCustomer {
								t.Fatal("wrong original ambiguity input")
							}
						}
					}
					if !found {
						t.Fatal("weak phone was not reinspected through Identity Port")
					}
				}
			})
		}
	}
}
