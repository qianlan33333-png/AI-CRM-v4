package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	segmentdsl "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/dsl"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// LegacyTemplateSource composes Owner read Ports. It has no SQL, identity
// matching, Provider call, or customer write. Each condition fails closed when
// its factual Owner is unavailable.
type LegacyTemplateSource struct {
	Groups             wecomport.AudienceGroupMembershipReader
	GroupCandidates    wecomport.CandidateGroupMembershipReader
	Contacts           wecomport.AudienceContactReader
	Survey             surveyport.AudienceChoiceAnswerReader
	Submissions        surveyport.AudienceSubmissionReader
	RecognizedContacts wecomport.AudienceRecognizedContactReader
	Orders             orderport.PaidAudienceReader
	Channels           channelport.AudienceEntryReader
	Radar              radarport.AudienceFirstClickReader
	MemberFacts        hxcport.VersionedSharedFactsReader
	HXCRegistration    hxcport.RegistrationReader
	RegistrationFacts  customerport.AudienceRegistrationReader
	Owners             accessport.AudienceOwnerReferenceReader
	PrimaryOwners      wecomport.AudiencePrimaryOwnerReader
	// PrimaryOwnerCorpScope is the composition-owned active WeCom provider
	// scope. A primary from another scope cannot be compared to this audience's
	// provider userid, even when the strings happen to match.
	PrimaryOwnerCorpScope string
}

func (s LegacyTemplateSource) Evaluate(ctx context.Context, definition segmentport.Definition, reference time.Time) (segmentport.Evaluation, error) {
	var ast segmentdsl.AST
	if json.Unmarshal(definition.Expression, &ast) != nil || ast.SchemaVersion != 1 || reference.IsZero() {
		return segmentport.Evaluation{}, ErrCustomerReadUnavailable
	}
	var ids []int64
	var err error
	if ast.Parameters, err = s.ownerReferences(ctx, ast.Parameters); err != nil {
		return segmentport.Evaluation{}, err
	}
	switch ast.Template {
	case segmentdsl.HXCRegistration:
		ids, err = s.hxcRegistration(ctx, ast.Parameters, reference)
	case segmentdsl.WeComContactRegistration:
		ids, err = s.wecom(ctx, ast.Parameters, reference)
	case segmentdsl.QuestionnaireSubmissions:
		ids, err = s.submissions(ctx, ast.Parameters, reference)
	case segmentdsl.QuestionnaireChoiceAnswers:
		ids, err = s.questionnaire(ctx, ast.Parameters, reference)
	case segmentdsl.PaidOrder:
		ids, err = s.paid(ctx, ast.Parameters, reference)
	case segmentdsl.ChannelEntry:
		ids, err = s.channel(ctx, ast.Parameters, reference)
	case segmentdsl.RadarFirstClickElapsed:
		ids, err = s.radar(ctx, ast.Parameters, reference)
	case segmentdsl.MemberExcludingGroupPaid:
		ids, err = s.memberExcludingGroupPaid(ctx, ast.Parameters, reference)
	case segmentdsl.MemberUsageStatus:
		ids, err = s.member(ctx, ast.Parameters, reference)
	default:
		return segmentport.Evaluation{}, ErrCustomerReadUnavailable
	}
	if err != nil {
		return segmentport.Evaluation{}, err
	}
	customers := make([]customerdomain.CustomerID, 0, len(ids))
	for _, id := range ids {
		customers = append(customers, customerdomain.CustomerID(id))
	}
	digest := sha256.Sum256([]byte(string(ast.Template) + "\x00" + reference.UTC().Format(time.RFC3339Nano)))
	return segmentport.Evaluation{CustomerIDs: customers, ReferenceAt: reference.UTC(), Watermarks: []segmentport.SourceWatermark{{Source: "owner.audience-facts.v1", AsOf: reference.UTC(), Fresh: true, SafeDigest: digest}}}, nil
}

func (s LegacyTemplateSource) ownerReferences(ctx context.Context, params map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	raw, exists := params["owner_staff_ids"]
	if !exists {
		return nil, ErrCustomerReadUnavailable
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	var scope string
	if json.Unmarshal(params["owner_scope"], &scope) != nil || (scope != "all" && scope != "specified") || len(ids) > 100 || (scope == "specified" && len(ids) == 0) {
		return nil, ErrCustomerReadUnavailable
	}
	seen := map[string]bool{}
	for _, value := range ids {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id < 1 || strconv.FormatInt(id, 10) != value || seen[value] {
			return nil, ErrCustomerReadUnavailable
		}
		seen[value] = true
	}
	// The all-scope empty selection is a valid frozen-form value and does not
	// depend on Access. Specified scope must resolve every local StaffID before
	// Owner facts are compared with provider userids.
	if scope == "all" {
		return params, nil
	}
	if s.Owners == nil {
		return nil, ErrCustomerReadUnavailable
	}
	values := make([]string, 0, len(ids))
	for _, value := range ids {
		id, _ := strconv.ParseInt(value, 10, 64)
		provider, found, err := s.Owners.AudienceOwnerUserID(ctx, accessport.StaffID(id))
		if err != nil {
			return nil, ErrCustomerReadUnavailable
		}
		if !found || provider == "" {
			return nil, ErrCustomerReadUnavailable
		}
		values = append(values, provider)
	}
	encoded, _ := json.Marshal(values)
	params["owner_staff_ids"] = encoded
	local, _ := json.Marshal(ids)
	params["owner_local_staff_ids"] = local
	return params, nil
}

// segmentport aliases CustomerID through customer/domain in current V3; keep
// conversion local for stable Owner-port callers.
func (s LegacyTemplateSource) contacts(ctx context.Context, reference time.Time) ([]wecomport.AudienceContact, error) {
	if s.Contacts == nil {
		return nil, ErrCustomerReadUnavailable
	}
	return s.Contacts.AudienceContacts(ctx, reference)
}
func (s LegacyTemplateSource) registrationFacts(ctx context.Context, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerport.AudienceRegistrationFact, error) {
	if s.RegistrationFacts == nil {
		return nil, ErrCustomerReadUnavailable
	}
	seen := make(map[customerdomain.CustomerID]bool, len(customerIDs))
	ids := make([]customerdomain.CustomerID, 0, len(customerIDs))
	for _, id := range customerIDs {
		if id < 1 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	out := make(map[customerdomain.CustomerID]customerport.AudienceRegistrationFact, len(ids))
	for start := 0; start < len(ids); start += customerport.MaxAudienceRegistrationCustomerIDs {
		end := start + customerport.MaxAudienceRegistrationCustomerIDs
		if end > len(ids) {
			end = len(ids)
		}
		facts, err := s.RegistrationFacts.AudienceRegistrationFacts(ctx, ids[start:end])
		if err != nil {
			return nil, ErrCustomerReadUnavailable
		}
		for id, fact := range facts {
			if id == fact.CustomerID && seen[id] {
				out[id] = fact
			}
		}
	}
	return out, nil
}

func (s LegacyTemplateSource) memberFacts(ctx context.Context, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]hxcport.SharedFacts, error) {
	if s.MemberFacts == nil {
		return nil, ErrCustomerReadUnavailable
	}
	version, err := s.MemberFacts.CurrentSharedFactsVersion(ctx)
	if err != nil || version < 1 {
		return nil, ErrCustomerReadUnavailable
	}
	seen := make(map[customerdomain.CustomerID]bool, len(customerIDs))
	ids := make([]customerdomain.CustomerID, 0, len(customerIDs))
	for _, id := range customerIDs {
		if id < 1 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	out := make(map[customerdomain.CustomerID]hxcport.SharedFacts, len(ids))
	for start := 0; start < len(ids); start += hxcport.MaxSharedFactsCustomerIDs {
		end := start + hxcport.MaxSharedFactsCustomerIDs
		if end > len(ids) {
			end = len(ids)
		}
		facts, readErr := s.MemberFacts.SharedFactsAtVersion(ctx, version, ids[start:end])
		if readErr != nil {
			return nil, ErrCustomerReadUnavailable
		}
		for customerID, fact := range facts {
			if customerID == fact.CustomerID && seen[customerID] {
				out[customerID] = fact
			}
		}
	}
	return out, nil
}
func owner(params map[string]json.RawMessage, value string) bool {
	var scope string
	var values []string
	return json.Unmarshal(params["owner_scope"], &scope) == nil && json.Unmarshal(params["owner_staff_ids"], &values) == nil && (scope == "all" || contains(values, value))
}
func ownerScoped(params map[string]json.RawMessage) (bool, error) {
	var scope string
	if json.Unmarshal(params["owner_scope"], &scope) != nil || (scope != "all" && scope != "specified") {
		return false, ErrCustomerReadUnavailable
	}
	return scope == "specified", nil
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func contactsFor(contacts []wecomport.AudienceContact, statuses []string, params map[string]json.RawMessage) map[int64]bool {
	out := map[int64]bool{}
	for _, fact := range contacts {
		if contains(statuses, fact.Status) && owner(params, fact.OwnerUserID) {
			out[int64(fact.CustomerID)] = true
		}
	}
	return out
}
func listParam(params map[string]json.RawMessage, key string) ([]string, error) {
	var out []string
	if json.Unmarshal(params[key], &out) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	return out, nil
}
func boolParam(params map[string]json.RawMessage, key string) (bool, error) {
	var out bool
	if json.Unmarshal(params[key], &out) != nil {
		return false, ErrCustomerReadUnavailable
	}
	return out, nil
}
func timeParam(params map[string]json.RawMessage, key string) (time.Time, error) {
	var raw string
	if json.Unmarshal(params[key], &raw) != nil || raw == "" {
		return time.Time{}, nil
	}
	out, e := time.Parse(time.RFC3339, raw)
	if e != nil {
		return time.Time{}, ErrCustomerReadUnavailable
	}
	return out, nil
}
func idsFrom(set map[int64]bool) []int64 {
	out := []int64{}
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (s LegacyTemplateSource) wecom(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	contacts, e := s.contacts(ctx, at)
	if e != nil {
		return nil, e
	}
	statuses, e := listParam(p, "contact_statuses")
	if e != nil {
		return nil, e
	}
	var registration string
	if json.Unmarshal(p["registration_status"], &registration) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	set := contactsFor(contacts, statuses, p)
	if registration == "any" {
		return idsFrom(set), nil
	}
	customerIDs := make([]customerdomain.CustomerID, 0, len(set))
	for id := range set {
		customerIDs = append(customerIDs, customerdomain.CustomerID(id))
	}
	facts, e := s.registrationFacts(ctx, customerIDs)
	if e != nil {
		return nil, e
	}
	for id := range set {
		fact, found := facts[customerdomain.CustomerID(id)]
		// A missing directory projection is unknown. It must not become an
		// unregistered match merely because another Owner has no HXC fact.
		if !found || !fact.Known || fact.Registered != (registration == "registered") {
			delete(set, id)
		}
	}
	return idsFrom(set), nil
}
func (s LegacyTemplateSource) questionnaire(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	if s.Survey == nil {
		return nil, ErrCustomerReadUnavailable
	}
	facts, e := s.Survey.FirstCompleteAudienceChoices(ctx, at)
	if e != nil {
		return nil, e
	}
	var questionnaire string
	if json.Unmarshal(p["questionnaire_id"], &questionnaire) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	var conditions []struct {
		QuestionID string   `json:"question_id"`
		OptionIDs  []string `json:"option_ids"`
	}
	if json.Unmarshal(p["conditions"], &conditions) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	matched := map[int64]map[string]bool{}
	for _, fact := range facts {
		if strconv.FormatInt(int64(fact.QuestionnaireID), 10) != questionnaire || !owner(p, fact.StaffID) {
			continue
		}
		for _, condition := range conditions {
			if strconv.FormatInt(int64(fact.QuestionID), 10) != condition.QuestionID {
				continue
			}
			for _, option := range fact.OptionIDs {
				if contains(condition.OptionIDs, strconv.FormatInt(int64(option), 10)) {
					if matched[int64(fact.CustomerID)] == nil {
						matched[int64(fact.CustomerID)] = map[string]bool{}
					}
					matched[int64(fact.CustomerID)][condition.QuestionID] = true
				}
			}
		}
	}
	out := map[int64]bool{}
	for customer, answers := range matched {
		if len(answers) == len(conditions) {
			out[customer] = true
		}
	}
	return idsFrom(out), nil
}
func (s LegacyTemplateSource) paid(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	if s.Orders == nil {
		return nil, ErrCustomerReadUnavailable
	}
	products, e := listParam(p, "product_codes")
	if e != nil {
		return nil, e
	}
	from, e := timeParam(p, "paid_at_from")
	if e != nil {
		return nil, e
	}
	to, e := timeParam(p, "paid_at_to")
	if e != nil {
		return nil, e
	}
	orders, e := s.Orders.PaidAudienceOrders(ctx, at)
	if e != nil {
		return nil, e
	}
	require, e := boolParam(p, "require_active_wecom_contact")
	if e != nil {
		return nil, e
	}
	scoped, e := ownerScoped(p)
	if e != nil {
		return nil, e
	}
	eligible := map[int64]bool{}
	if require {
		contacts, e := s.contacts(ctx, at)
		if e != nil {
			return nil, e
		}
		statuses := []string{"active", "deleted"}
		if require {
			statuses = []string{"active"}
		}
		eligible = contactsFor(contacts, statuses, p)
	}
	out := map[int64]bool{}
	for _, fact := range orders {
		if !contains(products, fact.ProductCode) || ((!from.IsZero() || !to.IsZero()) && fact.PaidAt == nil) || (fact.PaidAt != nil && !from.IsZero() && fact.PaidAt.Before(from)) || (fact.PaidAt != nil && !to.IsZero() && !fact.PaidAt.Before(to)) {
			continue
		}
		id := int64(fact.CustomerID)
		if scoped && !owner(p, fact.OwnerReference) {
			continue
		}
		if !require || eligible[id] {
			out[id] = true
		}
	}
	return idsFrom(out), nil
}
func (s LegacyTemplateSource) channel(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	if s.Channels == nil {
		return nil, ErrCustomerReadUnavailable
	}
	channels, e := listParam(p, "channel_codes")
	if e != nil {
		return nil, e
	}
	var minimum int
	var maximum *int
	if json.Unmarshal(p["entered_days_min"], &minimum) != nil || json.Unmarshal(p["entered_days_max"], &maximum) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	require, e := boolParam(p, "require_active_wecom_contact")
	if e != nil {
		return nil, e
	}
	eligible := map[int64]bool{}
	if require {
		contacts, e := s.contacts(ctx, at)
		if e != nil {
			return nil, e
		}
		eligible = contactsFor(contacts, []string{"active"}, p)
	}
	facts, e := s.Channels.AudienceEntries(ctx, at)
	if e != nil {
		return nil, e
	}
	out := map[int64]bool{}
	localOwners, _ := listParam(p, "owner_local_staff_ids")
	scoped, e := ownerScoped(p)
	if e != nil {
		return nil, e
	}
	for _, fact := range facts {
		age := int(at.Sub(fact.LastEnteredAt).Hours() / 24)
		ownerMatch := !scoped || owner(p, fact.OwnerReference)
		if scoped && fact.OwnerStaffID != nil {
			ownerMatch = contains(localOwners, strconv.FormatInt(*fact.OwnerStaffID, 10))
		}
		if !contains(channels, fact.ChannelCode) || age < minimum || (maximum != nil && age >= *maximum) || !ownerMatch {
			continue
		}
		id := int64(fact.CustomerID)
		if !require || eligible[id] {
			out[id] = true
		}
	}
	return idsFrom(out), nil
}
func (s LegacyTemplateSource) radar(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	if s.Radar == nil {
		return nil, ErrCustomerReadUnavailable
	}
	radars, e := listParam(p, "radar_ids")
	if e != nil {
		return nil, e
	}
	var minimum int
	var maximum *int
	var unit string
	if json.Unmarshal(p["elapsed_min"], &minimum) != nil || json.Unmarshal(p["elapsed_max"], &maximum) != nil || json.Unmarshal(p["elapsed_unit"], &unit) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	scoped, e := ownerScoped(p)
	if e != nil {
		return nil, e
	}
	facts, e := s.Radar.AudienceFirstClicks(ctx, at)
	if e != nil {
		return nil, e
	}
	primaryByCustomer := map[customerdomain.CustomerID]wecomport.AudiencePrimaryOwner{}
	if scoped && s.PrimaryOwners != nil {
		customerIDs := make([]customerdomain.CustomerID, 0, len(facts))
		for _, fact := range facts {
			customerIDs = append(customerIDs, fact.CustomerID)
		}
		primaries, primaryErr := s.PrimaryOwners.AudiencePrimaryOwners(ctx, customerIDs)
		if primaryErr != nil {
			return nil, ErrCustomerReadUnavailable
		}
		for _, primary := range primaries {
			primaryByCustomer[primary.CustomerID] = primary
		}
	}
	scale := time.Hour
	if unit == "day" {
		scale = 24 * time.Hour
	}
	out := map[int64]bool{}
	for _, fact := range facts {
		elapsed := int(at.Sub(fact.FirstClickedAt) / scale)
		id := int64(fact.CustomerID)
		// dd8 selects a trusted identity primary owner before historical click
		// staff facts. Current relationship rows are never substituted here.
		// An ambiguous primary or a primary from another provider scope cannot
		// safely fall back to a weaker owner fact.
		ownerUserID := fact.OwnerUserID
		if primary, exists := primaryByCustomer[fact.CustomerID]; exists {
			switch {
			case primary.Status == "known" && s.PrimaryOwnerCorpScope != "" && primary.CorpScope == s.PrimaryOwnerCorpScope:
				ownerUserID = primary.OwnerUserID
			case primary.Status == "ambiguous" || primary.Status == "known":
				ownerUserID = ""
			}
		}
		ownerMatch := !scoped || (ownerUserID != "" && owner(p, ownerUserID))
		if contains(radars, strconv.FormatInt(fact.RadarID, 10)) && elapsed >= minimum && (maximum == nil || elapsed < *maximum) && ownerMatch {
			out[id] = true
		}
	}
	return idsFrom(out), nil
}
func (s LegacyTemplateSource) member(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	var period, registration, usage string
	var tiers, statuses []string
	if json.Unmarshal(p["service_period"], &period) != nil || json.Unmarshal(p["registration_status"], &registration) != nil || json.Unmarshal(p["usage_status"], &usage) != nil || json.Unmarshal(p["membership_tiers"], &tiers) != nil || json.Unmarshal(p["membership_statuses"], &statuses) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	contacts, e := s.contacts(ctx, at)
	if e != nil {
		return nil, e
	}
	eligible := contactsFor(contacts, []string{"active", "deleted"}, p)
	customerIDs := make([]customerdomain.CustomerID, 0, len(eligible))
	for id := range eligible {
		customerIDs = append(customerIDs, customerdomain.CustomerID(id))
	}
	facts, e := s.memberFacts(ctx, customerIDs)
	if e != nil {
		return nil, e
	}
	// The old member projection unions its HXC registration fact with the
	// canonical directory phone-presence fact. They are independent sources:
	// a phone may make a member registered even when HXC has no registration,
	// while a missing directory projection never becomes a false phone fact.
	registrationFacts := map[customerdomain.CustomerID]customerport.AudienceRegistrationFact{}
	if registration != "any" && s.RegistrationFacts != nil {
		registrationFacts, e = s.registrationFacts(ctx, customerIDs)
		if e != nil {
			return nil, e
		}
	}
	out := map[int64]bool{}
	for id := range eligible {
		fact, found := facts[customerdomain.CustomerID(id)]
		// The frozen member template only reads rows with durable membership
		// provenance. A dashboard row by itself is registration/usage evidence,
		// not a membership record, so it cannot satisfy an "any" member filter.
		if !found || fact.Availability != hxcport.SharedFactsAvailable || !fact.MembershipRecordFound || strings.TrimSpace(fact.MembershipSource) == "" {
			continue
		}
		active := fact.ActiveAt(at)
		expired := fact.ExpiredAt(at)
		registered := fact.Registered
		if registration != "any" {
			if directory, found := registrationFacts[customerdomain.CustomerID(id)]; found {
				if !directory.Known {
					continue
				}
				registered = registered || directory.Registered
			} else if !fact.Registered {
				// There is no phone-presence result to combine and HXC does not
				// establish registration. Preserve the old unknown rather than
				// treating an absent directory row as "unregistered".
				continue
			}
		}
		if (period == "active" && !active) || (period == "expired" && !expired) || (registration == "registered" && !registered) || (registration == "unregistered" && registered) || (usage == "used" && !fact.HasRealUsage) || (usage == "unused" && fact.HasRealUsage) || (len(tiers) > 0 && !contains(tiers, fact.Tier)) || (len(statuses) > 0 && !contains(statuses, fact.MembershipStatus)) {
			continue
		}
		out[id] = true
	}
	return idsFrom(out), nil
}

var _ segmentport.DefinitionSource = LegacyTemplateSource{}
