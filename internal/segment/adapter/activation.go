package adapter

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type evaluatedPaidOrders []orderport.PaidAudienceOrder

func (v evaluatedPaidOrders) PaidAudienceOrders(context.Context, time.Time) ([]orderport.PaidAudienceOrder, error) {
	return v, nil
}

// Freeze a bounded Owner read within this evaluation, not a shared cache.
type evaluatedContacts []wecomport.AudienceContact

func (v evaluatedContacts) AudienceContacts(context.Context, time.Time) ([]wecomport.AudienceContact, error) {
	return v, nil
}

type evaluatedSubmissions []surveyport.AudienceSubmission

func (v evaluatedSubmissions) AudienceSubmissions(context.Context, time.Time) ([]surveyport.AudienceSubmission, error) {
	return v, nil
}

// activationPaid filters immutable Order facts before reusing the existing
// owner/contact/payment-friend rules. The override affects only elapsed-time
// evaluation, never the original payment timestamp or its emitted evidence.
func (s LegacyTemplateSource) activationPaid(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, map[customerdomain.CustomerID]segmentport.PaidOrderFact, error) {
	v, e := segmentport.ParseActivation(p["activation"], true)
	if e != nil || s.Orders == nil {
		return nil, nil, ErrCustomerReadUnavailable
	}
	products, e := listParam(p, "product_codes")
	if e != nil {
		return nil, nil, e
	}
	orders, e := s.Orders.PaidAudienceOrders(ctx, at)
	if e != nil {
		return nil, nil, e
	}
	overridden := map[int64]bool{}
	if v.EnrollmentOverride != nil {
		for _, id := range v.EnrollmentOverride.OrderIDs {
			overridden[id] = true
		}
	}
	eligible := evaluatedPaidOrders{}
	for _, o := range orders {
		if !contains(products, o.ProductCode) || o.CustomerID < 1 {
			continue
		}
		var anchor time.Time
		if o.PaidAt != nil {
			anchor = *o.PaidAt
			if anchor.After(at) {
				continue
			}
		}
		if overridden[o.OrderID] {
			anchor = v.EnrollmentOverride.At
		}
		if !v.MatchesElapsed(anchor, at) {
			continue
		}
		eligible = append(eligible, o)
	}
	payerIDs := make([]customerdomain.CustomerID, 0, len(eligible))
	for _, o := range eligible {
		payerIDs = append(payerIDs, o.CustomerID)
	}
	orderRoots, e := s.canonicalCustomerMap(ctx, payerIDs)
	if e != nil {
		return nil, nil, e
	}
	for i := range eligible {
		eligible[i].CustomerID = orderRoots[eligible[i].CustomerID]
	}
	source := s
	source.Orders = eligible
	require, e := boolParam(p, "require_active_wecom_contact")
	if e != nil {
		return nil, nil, e
	}
	friend := false
	if _, enabled := p["require_wecom_friend_at_paid_time"]; enabled {
		friend, e = boolParam(p, "require_wecom_friend_at_paid_time")
		if e != nil {
			return nil, nil, e
		}
	}
	if require || friend {
		customers := []customerdomain.CustomerID{}
		for _, root := range orderRoots {
			customers = append(customers, root)
		}
		contacts, e := s.contactsForCustomers(ctx, at, customers)
		if e != nil {
			return nil, nil, e
		}
		source.Contacts = evaluatedContacts(contacts)
	}
	ids, paid, e := source.paid(ctx, p, at)
	if e != nil {
		return nil, nil, e
	}
	raw := make([]customerdomain.CustomerID, 0, len(ids))
	for _, id := range ids {
		raw = append(raw, customerdomain.CustomerID(id))
	}
	roots, e := s.canonicalCustomerMap(ctx, raw)
	if e != nil {
		return nil, nil, e
	}
	candidates := map[int64]bool{}
	for _, id := range raw {
		candidates[int64(roots[id])] = true
	}
	if v.RegistrationStatus != "any" {
		states, e := s.activationRegistration(ctx, idsFrom(candidates), at)
		if e != nil {
			return nil, nil, e
		}
		for id := range candidates {
			if states[id] != v.RegistrationStatus {
				delete(candidates, id)
			}
		}
	}
	if v.QuestionnaireStatus != "any" && len(candidates) > 0 {
		if s.Submissions == nil {
			return nil, nil, ErrCustomerReadUnavailable
		}
		submissions, e := s.Submissions.AudienceSubmissions(ctx, at)
		if e != nil {
			return nil, nil, e
		}
		selected := []customerdomain.CustomerID{}
		for _, f := range submissions {
			if f.CustomerID > 0 && !f.SubmittedAt.IsZero() && !f.SubmittedAt.After(at) && contains(v.QuestionnaireIDs, strconv.FormatInt(int64(f.QuestionnaireID), 10)) {
				selected = append(selected, f.CustomerID)
			}
		}
		roots, e := s.canonicalCustomerMap(ctx, selected)
		if e != nil {
			return nil, nil, e
		}
		submitted := map[int64]bool{}
		for _, root := range roots {
			submitted[int64(root)] = true
		}
		for id := range candidates {
			if submitted[id] != (v.QuestionnaireStatus == "submitted") {
				delete(candidates, id)
			}
		}
	}
	// Keep the qualified payment map exactly aligned with the final audience.
	for id := range paid {
		if !candidates[int64(id)] {
			delete(paid, id)
		}
	}
	return idsFrom(candidates), paid, nil
}

func (s LegacyTemplateSource) activationRegistration(ctx context.Context, ids []int64, at time.Time) (map[int64]string, error) {
	if s.HXCRegistration == nil {
		return nil, ErrCustomerReadUnavailable
	}
	out := map[int64]string{}
	var version int64
	for start := 0; start < len(ids); start += hxcport.MaxSharedFactsCustomerIDs {
		end := start + hxcport.MaxSharedFactsCustomerIDs
		if end > len(ids) {
			end = len(ids)
		}
		batch := []customerdomain.CustomerID{}
		for _, id := range ids[start:end] {
			batch = append(batch, customerdomain.CustomerID(id))
		}
		facts, e := s.HXCRegistration.RegistrationFacts(ctx, batch, at)
		if e != nil {
			return nil, e
		}
		for _, id := range batch {
			f, ok := facts[id]
			if !ok {
				continue
			}
			// Reuse the existing HXC registration freshness/version contract.
			if f.AsOf.IsZero() || f.AsOf.After(at) || at.Sub(f.AsOf) > 2*time.Hour || f.Version <= 0 || (version != 0 && version != f.Version) {
				return nil, ErrCustomerReadUnavailable
			}
			version = f.Version
			if f.State == "registered" || f.State == "unregistered" {
				out[int64(id)] = f.State
			}
		}
	}
	return out, nil
}

func (s LegacyTemplateSource) activationSubmissions(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	v, e := segmentport.ParseActivation(p["activation"], false)
	if e != nil || s.Submissions == nil {
		return nil, ErrCustomerReadUnavailable
	}
	facts, e := s.Submissions.AudienceSubmissions(ctx, at)
	if e != nil {
		return nil, e
	}
	qids, e := listParam(p, "questionnaire_ids")
	if e != nil {
		return nil, e
	}
	raw := []customerdomain.CustomerID{}
	selected := evaluatedSubmissions{}
	for _, f := range facts {
		if f.CustomerID > 0 && !f.SubmittedAt.IsZero() && !f.SubmittedAt.After(at) && contains(qids, strconv.FormatInt(int64(f.QuestionnaireID), 10)) && owner(p, f.StaffID) {
			raw = append(raw, f.CustomerID)
			selected = append(selected, f)
		}
	}
	roots, e := s.canonicalCustomerMap(ctx, raw)
	if e != nil {
		return nil, e
	}
	for i := range selected {
		selected[i].CustomerID = roots[selected[i].CustomerID]
	}
	source := s
	source.Submissions = selected
	ids, e := source.submissions(ctx, p, at)
	if e != nil {
		return nil, e
	}
	first := map[customerdomain.CustomerID]time.Time{}
	for _, f := range facts {
		if !contains(qids, strconv.FormatInt(int64(f.QuestionnaireID), 10)) || !owner(p, f.StaffID) || f.SubmittedAt.IsZero() || f.SubmittedAt.After(at) {
			continue
		}
		root := roots[f.CustomerID]
		if root > 0 && (first[root].IsZero() || f.SubmittedAt.Before(first[root])) {
			first[root] = f.SubmittedAt
		}
	}
	candidateRoots := []customerdomain.CustomerID{}
	for _, id := range ids {
		candidateRoots = append(candidateRoots, customerdomain.CustomerID(id))
	}
	mapping, e := s.canonicalCustomerMap(ctx, candidateRoots)
	if e != nil {
		return nil, e
	}
	candidates := map[int64]bool{}
	for _, root := range mapping {
		if v.MatchesElapsed(first[root], at) {
			candidates[int64(root)] = true
		}
	}
	if v.TokenUsageStatus == "unused_7d" && len(candidates) > 0 {
		ordered := []customerdomain.CustomerID{}
		for _, id := range idsFrom(candidates) {
			ordered = append(ordered, customerdomain.CustomerID(id))
		}
		shared, e := s.memberFacts(ctx, ordered)
		if e != nil {
			return nil, e
		}
		for _, id := range ordered {
			f, ok := shared[id]
			if !ok || f.Availability != hxcport.SharedFactsAvailable || f.TokenUsageObservedAt == nil || (f.HasTokenUsage && f.LastTokenUsedAt == nil) {
				delete(candidates, int64(id))
				continue
			}
			observed := *f.TokenUsageObservedAt
			if observed.IsZero() || observed.After(at) || at.Sub(observed) > 2*time.Hour {
				return nil, ErrCustomerReadUnavailable
			}
			if f.LastTokenUsedAt != nil && (!f.LastTokenUsedAt.Before(at.Add(-7*24*time.Hour)) || f.LastTokenUsedAt.After(observed)) {
				delete(candidates, int64(id))
			}
		}
	}
	return idsFrom(candidates), nil
}
