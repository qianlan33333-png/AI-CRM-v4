package adapter

import (
	"context"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	segmentdsl "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/dsl"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"testing"
	"time"
)

type groupExclusionFacts struct {
	f   wecomport.AudienceGroupMembership
	err error
}

func (g groupExclusionFacts) AudienceGroupMembership(context.Context, string, string, time.Time, time.Duration) (wecomport.AudienceGroupMembership, error) {
	return g.f, g.err
}
func (g groupExclusionFacts) OutsideGroupCandidates(_ context.Context, _ string, _ string, at time.Time, age time.Duration, ids []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	if g.err != nil || !g.f.Complete || at.Sub(g.f.ObservedAt) > age || g.f.UnresolvedCount > 0 {
		return nil, errors.New("unavailable")
	}
	out := []customerdomain.CustomerID{}
	for _, id := range ids {
		if id != 1 {
			out = append(out, id)
		}
	}
	return out, nil
}
func TestMemberExclusionRequiresKnownGroupAndExcludesPaid(t *testing.T) {
	at := time.Now().UTC()
	exp := at.Add(time.Hour)
	facts := legacyFacts{shared: map[customerdomain.CustomerID]hxcport.SharedFacts{}, orders: []orderport.PaidAudienceOrder{{CustomerID: 2, ProductCode: "exclude"}}}
	for i := 1; i <= 4; i++ {
		id := customerdomain.CustomerID(i)
		facts.contacts = append(facts.contacts, wecomport.AudienceContact{CustomerID: id, Status: "active"})
		facts.shared[id] = hxcport.SharedFacts{CustomerID: id, Availability: hxcport.SharedFactsAvailable, MembershipRecordFound: true, MembershipSource: "hxc", IsMember: true, MembershipStatus: "active", ExpiresAt: &exp}
	}
	def := legacyDefinition(t, segmentdsl.MemberExcludingGroupPaid, `{"owner_scope":"all","owner_staff_ids":[],"exclude_group_chat":"g","excluded_product_codes":["exclude"]}`)
	good := wecomport.AudienceGroupMembership{Complete: true, ObservedAt: at, CustomerIDs: []customerdomain.CustomerID{1}}
	s := LegacyTemplateSource{Contacts: facts, Orders: facts, MemberFacts: facts, GroupCandidates: groupExclusionFacts{f: good}}
	got, e := s.Evaluate(context.Background(), def, at)
	if e != nil || len(got.CustomerIDs) != 2 || got.CustomerIDs[0] != 3 || got.CustomerIDs[1] != 4 {
		t.Fatalf("incorrect inclusion %v err=%v", got.CustomerIDs, e)
	}
	for _, tc := range []groupExclusionFacts{{err: errors.New("missing")}, {f: wecomport.AudienceGroupMembership{Complete: true, ObservedAt: at.Add(-time.Hour)}}, {f: wecomport.AudienceGroupMembership{Complete: false, ObservedAt: at}}, {f: wecomport.AudienceGroupMembership{Complete: true, ObservedAt: at, UnresolvedCount: 1}}} {
		s.GroupCandidates = tc
		if _, e = s.Evaluate(context.Background(), def, at); e == nil {
			t.Fatal("negative membership used incomplete evidence")
		}
	}
}
