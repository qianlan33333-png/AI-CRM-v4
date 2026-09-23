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
