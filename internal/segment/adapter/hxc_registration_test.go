package adapter

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	segmentdsl "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/dsl"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"testing"
	"time"
)

type hxcRegistrationFacts struct{ at time.Time }

func (f hxcRegistrationFacts) RegistrationFacts(context.Context, []customerdomain.CustomerID, time.Time) (map[customerdomain.CustomerID]hxcport.RegistrationFact, error) {
	return map[customerdomain.CustomerID]hxcport.RegistrationFact{1: {State: "unregistered", AsOf: f.at, Version: 1}, 2: {State: "registered", AsOf: f.at, Version: 1}, 3: {State: "unknown", AsOf: f.at, Version: 1}, 4: {State: "unknown", AsOf: f.at, Version: 1}}, nil
}
func TestHXCRegistrationUnknownStaleAndInactiveNeverEnter(t *testing.T) {
	at := time.Now()
	f := legacyFacts{contacts: []wecomport.AudienceContact{{CustomerID: 1, Status: "active"}, {CustomerID: 2, Status: "active"}, {CustomerID: 3, Status: "active"}, {CustomerID: 4, Status: "active"}, {CustomerID: 5, Status: "deleted"}}}
	s := LegacyTemplateSource{Contacts: f, HXCRegistration: hxcRegistrationFacts{at}}
	d := legacyDefinition(t, segmentdsl.HXCRegistration, `{"owner_scope":"all","owner_staff_ids":[],"registration_status":"unregistered"}`)
	out, e := s.Evaluate(context.Background(), d, at)
	if e != nil || len(out.CustomerIDs) != 1 || out.CustomerIDs[0] != 1 {
		t.Fatalf("incorrect negative audience=%v error=%v", out.CustomerIDs, e)
	}
	s.HXCRegistration = hxcRegistrationFacts{at.Add(-3 * time.Hour)}
	if _, e := s.Evaluate(context.Background(), d, at); e == nil {
		t.Fatal("stale generation must fail refresh, not clear audience")
	}

}
