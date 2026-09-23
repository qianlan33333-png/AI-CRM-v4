package adapter

import (
	"context"
	"encoding/json"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	"time"
)

func (s LegacyTemplateSource) hxcRegistration(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	if s.HXCRegistration == nil {
		return nil, ErrCustomerReadUnavailable
	}
	var want string
	if json.Unmarshal(p["registration_status"], &want) != nil || (want != "registered" && want != "unregistered") {
		return nil, ErrCustomerReadUnavailable
	}
	contacts, e := s.contacts(ctx, at)
	if e != nil {
		return nil, e
	}
	candidates := contactsFor(contacts, []string{"active"}, p)
	ordered := idsFrom(candidates)
	out := map[int64]bool{}
	var version int64
	for start := 0; start < len(ordered); start += hxcport.MaxSharedFactsCustomerIDs {
		end := start + hxcport.MaxSharedFactsCustomerIDs
		if end > len(ordered) {
			end = len(ordered)
		}
		ids := []customerdomain.CustomerID{}
		for _, id := range ordered[start:end] {
			ids = append(ids, customerdomain.CustomerID(id))
		}
		facts, e := s.HXCRegistration.RegistrationFacts(ctx, ids, at)
		if e != nil {
			return nil, e
		}
		for _, id := range ids {
			f, ok := facts[id]
			if !ok {
				continue
			}
			if f.AsOf.IsZero() || f.AsOf.After(at) || at.Sub(f.AsOf) > 2*time.Hour {
				return nil, ErrCustomerReadUnavailable
			}
			if f.Version <= 0 {
				return nil, ErrCustomerReadUnavailable
			}
			if version != 0 && version != f.Version {
				return nil, ErrCustomerReadUnavailable
			}
			version = f.Version
			if f.State == "unknown" {
				continue
			}
			if f.State == want {
				out[int64(id)] = true
			}
		}
	}
	return idsFrom(out), nil
}
