package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

type compositionEntitlements struct {
	mu       sync.Mutex
	pages    [][]orderport.Entitlement
	queries  []orderport.ServicePeriodMemberQuery
	snapshot time.Time
}

func (s *compositionEntitlements) ListCustomerEntitlements(context.Context, int64, int32) (orderport.EntitlementPage, error) {
	return orderport.EntitlementPage{}, nil
}
func (s *compositionEntitlements) GetCustomerServicePeriodEntitlement(context.Context, int64, int64) (orderport.Entitlement, bool, error) {
	return orderport.Entitlement{}, false, nil
}
func (s *compositionEntitlements) UpdateEntitlementRemark(context.Context, orderport.RemarkCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, orderport.ErrNotFound
}
func (s *compositionEntitlements) UpdateEntitlementAlliance(context.Context, orderport.AllianceCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, orderport.ErrNotFound
}
func (s *compositionEntitlements) ListServicePeriodMembers(_ context.Context, query orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries = append(s.queries, query)
	pageIndex := 0
	if query.Cursor != "" {
		if _, err := fmt.Sscanf(query.Cursor, "order-page-%d", &pageIndex); err != nil || pageIndex < 1 || pageIndex >= len(s.pages) {
			return orderport.ServicePeriodMemberPage{}, orderport.ErrConflict
		}
	}
	items := append([]orderport.Entitlement(nil), s.pages[pageIndex]...)
	page := orderport.ServicePeriodMemberPage{Items: items, SnapshotAt: s.snapshot}
	if page.SnapshotAt.IsZero() {
		page.SnapshotAt = query.SnapshotAt
	}
	if pageIndex+1 < len(s.pages) {
		page.NextCursor = fmt.Sprintf("order-page-%d", pageIndex+1)
	}
	return page, nil
}

type compositionNames struct {
	mu     sync.Mutex
	values map[customerdomain.CustomerID]string
	calls  [][]customerdomain.CustomerID
}

func (s *compositionNames) DisplayNames(_ context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, append([]customerdomain.CustomerID(nil), ids...))
	out := make(map[customerdomain.CustomerID]string, len(ids))
	for _, id := range ids {
		out[id] = s.values[id]
	}
	return out, nil
}

type compositionFacts struct {
	mu         sync.Mutex
	version    int64
	gone       bool
	values     map[customerdomain.CustomerID]hxcport.SharedFacts
	retained   map[int64]map[customerdomain.CustomerID]hxcport.SharedFacts
	calls      [][]customerdomain.CustomerID
	failOnCall int
	failErr    error
}

func (s *compositionFacts) CurrentSharedFactsVersion(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.version < 1 {
		return 0, errors.New("no shared facts published")
	}
	return s.version, nil
}
func (s *compositionFacts) SharedFactsAtVersion(_ context.Context, version int64, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]hxcport.SharedFacts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gone {
		return nil, hxcport.ErrSharedFactsVersionUnavailable
	}
	values := s.values
	if s.retained != nil {
		var found bool
		values, found = s.retained[version]
		if !found {
			return nil, hxcport.ErrSharedFactsVersionUnavailable
		}
	} else if version != s.version {
		return nil, hxcport.ErrSharedFactsVersionUnavailable
	}
	s.calls = append(s.calls, append([]customerdomain.CustomerID(nil), ids...))
	if s.failOnCall == len(s.calls) {
		return nil, s.failErr
	}
	out := make(map[customerdomain.CustomerID]hxcport.SharedFacts, len(ids))
	for _, id := range ids {
		if value, ok := values[id]; ok {
			out[id] = value
		}
	}
	return out, nil
}

func compositionConfig(t *testing.T, raw string) donorGridConfig {
	t.Helper()
	config, err := decodeDonorGridConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func compositionRow(id, customerID int64, end time.Time) orderport.Entitlement {
	return orderport.Entitlement{ID: id, CustomerID: customerID, ServiceProductID: 7, Status: "active", StartAt: end.Add(-30 * 24 * time.Hour), EndAt: end, Version: 1, UpdatedAt: end.Add(-time.Hour), RenewalCount: 0, RenewalCountAvailable: true}
}

func compositionFact(id customerdomain.CustomerID, formal bool) hxcport.SharedFacts {
	return hxcport.SharedFacts{CustomerID: id, Availability: hxcport.SharedFactsAvailable, FormallyLoggedIn: formal, HasTokenUsage: formal, LearningPlanFound: false, CardOpenCount7D: 0}
}

func responsePrimary(t *testing.T, row map[string]any) string {
	t.Helper()
	values := row["values"].(map[string]any)
	return values["member"].(map[string]any)["primary"].(string)
}

func TestMemberGridCompositionAppliesFullRelationAcrossOwnersBeforePaging(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	snapshot := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	members := &compositionEntitlements{snapshot: snapshot, pages: [][]orderport.Entitlement{
		{compositionRow(10, 10, snapshot.Add(24*time.Hour))},
		{compositionRow(11, 11, snapshot.Add(3*24*time.Hour)), compositionRow(12, 12, snapshot.Add(3*24*time.Hour))},
	}}
	names := &compositionNames{values: map[customerdomain.CustomerID]string{10: "Zulu", 11: "Alpha", 12: "Beta"}}
	facts := &compositionFacts{version: 9, values: map[customerdomain.CustomerID]hxcport.SharedFacts{
		10: compositionFact(10, false), 11: compositionFact(11, true), 12: compositionFact(12, true),
	}}
	handler.members, handler.names, handler.sharedFacts = members, names, facts
	config := compositionConfig(t, `{"schema_version":1,"filter":{"logic":"or","conditions":[{"field":"remaining_days","operator":"equals","value":1},{"field":"formally_logged_in","operator":"in","value":["yes"]}]},"sorts":[{"field":"member","direction":"asc"}],"groups":[{"field":"formally_logged_in","direction":"asc"},{"field":"remaining_days","direction":"asc"}]}`)

	first, cursor, err := handler.queryDonorGridComposed(context.Background(), 7, config, "", 1)
	if err != nil || len(first) != 1 || responsePrimary(t, first[0]) != "Alpha" || cursor == "" {
		t.Fatalf("first=%#v cursor=%q err=%v", first, cursor, err)
	}
	if strings.Contains(cursor, "Alpha") || strings.Contains(cursor, "Zulu") {
		t.Fatalf("cursor leaked display data: %q", cursor)
	}
	path := first[0]["group_path"].([]any)
	if len(path) != 2 || path[0].(map[string]any)["count"] != int64(2) {
		t.Fatalf("complete two-level group count=%#v", path)
	}
	second, secondCursor, err := handler.queryDonorGridComposed(context.Background(), 7, config, cursor, 1)
	if err != nil || len(second) != 1 || responsePrimary(t, second[0]) != "Beta" || secondCursor == "" {
		t.Fatalf("second=%#v cursor=%q err=%v", second, secondCursor, err)
	}
	third, finalCursor, err := handler.queryDonorGridComposed(context.Background(), 7, config, secondCursor, 1)
	if err != nil || len(third) != 1 || responsePrimary(t, third[0]) != "Zulu" || finalCursor != "" {
		t.Fatalf("third=%#v cursor=%q err=%v", third, finalCursor, err)
	}
	members.mu.Lock()
	queryCount := len(members.queries)
	members.mu.Unlock()
	if queryCount < 2 {
		t.Fatalf("second source page was not scanned: %d", queryCount)
	}

	// A customer-source change is not hidden behind the cursor. Recomputing
	// the full relation produces a different signed digest and requires reload.
	names.mu.Lock()
	names.values[11] = "Aardvark"
	names.mu.Unlock()
	if _, _, err = handler.queryDonorGridComposed(context.Background(), 7, config, cursor, 1); !errors.Is(err, errMemberGridCursorStale) {
		t.Fatalf("name-source change err=%v, want cursor_stale", err)
	}
}

func TestMemberGridCompositionPinsHXCGenerationAndRejectsPrunedCursor(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	snapshot := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	members := &compositionEntitlements{snapshot: snapshot, pages: [][]orderport.Entitlement{{compositionRow(1, 1, snapshot.Add(48*time.Hour)), compositionRow(2, 2, snapshot.Add(48*time.Hour))}}}
	names := &compositionNames{values: map[customerdomain.CustomerID]string{1: "Alpha", 2: "Beta"}}
	facts := &compositionFacts{version: 4, values: map[customerdomain.CustomerID]hxcport.SharedFacts{1: compositionFact(1, true), 2: compositionFact(2, false)}}
	handler.members, handler.names, handler.sharedFacts = members, names, facts
	config := defaultDonorGridConfig()
	_, cursor, err := handler.queryDonorGridComposed(context.Background(), 7, config, "", 1)
	if err != nil || cursor == "" {
		t.Fatalf("first cursor=%q err=%v", cursor, err)
	}
	facts.mu.Lock()
	facts.gone = true
	facts.mu.Unlock()
	if _, _, err = handler.queryDonorGridComposed(context.Background(), 7, config, cursor, 1); !errors.Is(err, errMemberGridCursorStale) {
		t.Fatalf("pruned HXC generation err=%v, want cursor_stale", err)
	}
}

func TestMemberGridCompositionRejectsCursorWhenHXCGenerationAdvances(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	snapshot := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	handler.members = &compositionEntitlements{snapshot: snapshot, pages: [][]orderport.Entitlement{{
		compositionRow(1, 1, snapshot.Add(48*time.Hour)),
		compositionRow(2, 2, snapshot.Add(48*time.Hour)),
	}}}
	handler.names = &compositionNames{values: map[customerdomain.CustomerID]string{1: "Alpha", 2: "Beta"}}
	v4 := map[customerdomain.CustomerID]hxcport.SharedFacts{1: compositionFact(1, true), 2: compositionFact(2, false)}
	facts := &compositionFacts{version: 4, retained: map[int64]map[customerdomain.CustomerID]hxcport.SharedFacts{4: v4}}
	handler.sharedFacts = facts
	_, cursor, err := handler.queryDonorGridComposed(context.Background(), 7, defaultDonorGridConfig(), "", 1)
	if err != nil || cursor == "" {
		t.Fatalf("first cursor=%q err=%v", cursor, err)
	}
	facts.mu.Lock()
	facts.version = 5
	facts.retained[5] = v4 // v4 remains readable through HXC retention.
	facts.mu.Unlock()
	if _, _, err = handler.queryDonorGridComposed(context.Background(), 7, defaultDonorGridConfig(), cursor, 1); !errors.Is(err, errMemberGridCursorStale) {
		t.Fatalf("advanced retained HXC generation err=%v, want cursor_stale", err)
	}
}

func TestMemberGridCompositionProbesCandidateLimitBeforeFiltering(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	snapshot := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	pages := make([][]orderport.Entitlement, 0, 51)
	for first := 0; first < memberGridCandidateLimit+1; first += memberGridOrderPageSize {
		last := first + memberGridOrderPageSize
		if last > memberGridCandidateLimit+1 {
			last = memberGridCandidateLimit + 1
		}
		page := make([]orderport.Entitlement, 0, last-first)
		for index := first; index < last; index++ {
			page = append(page, compositionRow(int64(index+1), int64(index+1), snapshot.Add(24*time.Hour)))
		}
		pages = append(pages, page)
	}
	handler.members = &compositionEntitlements{snapshot: snapshot, pages: pages}
	handler.names = &compositionNames{values: map[customerdomain.CustomerID]string{}}
	if _, _, err := handler.queryDonorGridComposed(context.Background(), 7, defaultDonorGridConfig(), "", 1); !errors.Is(err, errMemberGridTooLarge) {
		t.Fatalf("candidate cap err=%v, want member_grid_too_large", err)
	}
}

func TestMemberGridCompositionBatchesCustomerAndHXCReads(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	snapshot := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	items := make([]orderport.Entitlement, 0, 501)
	namesByID := make(map[customerdomain.CustomerID]string, 501)
	factsByID := make(map[customerdomain.CustomerID]hxcport.SharedFacts, 501)
	for id := int64(1); id <= 501; id++ {
		items = append(items, compositionRow(id, id, snapshot.Add(24*time.Hour)))
		namesByID[customerdomain.CustomerID(id)] = fmt.Sprintf("member-%04d", id)
		factsByID[customerdomain.CustomerID(id)] = compositionFact(customerdomain.CustomerID(id), id%2 == 0)
	}
	members := &compositionEntitlements{snapshot: snapshot, pages: [][]orderport.Entitlement{items}}
	names := &compositionNames{values: namesByID}
	facts := &compositionFacts{version: 3, values: factsByID}
	handler.members, handler.names, handler.sharedFacts = members, names, facts
	if _, _, err := handler.queryDonorGridComposed(context.Background(), 7, defaultDonorGridConfig(), "", 1); err != nil {
		t.Fatal(err)
	}
	names.mu.Lock()
	for _, batch := range names.calls {
		if len(batch) > 200 {
			t.Fatalf("customer batch=%d", len(batch))
		}
	}
	nameCalls := len(names.calls)
	names.mu.Unlock()
	facts.mu.Lock()
	for _, batch := range facts.calls {
		if len(batch) > hxcport.MaxSharedFactsCustomerIDs {
			t.Fatalf("HXC batch=%d", len(batch))
		}
	}
	factCalls := len(facts.calls)
	facts.mu.Unlock()
	if nameCalls != 3 || factCalls != 2 {
		t.Fatalf("batches customer=%d HXC=%d", nameCalls, factCalls)
	}
}

func TestMemberGridCompositionKeepsUnavailableDistinctFromKnownEmptyAndZero(t *testing.T) {
	row := memberGridRow{hxcKnown: false, formal: "unavailable", token: "unavailable", progress: "unavailable"}
	if memberGridMatches(row, donorGridCondition{Field: "open_count_7d", Operator: "is_empty"}) {
		t.Fatal("unavailable HXC field matched known-empty filter")
	}
	row.hxcKnown = true
	if !memberGridMatches(row, donorGridCondition{Field: "open_count_7d", Operator: "is_empty"}) {
		t.Fatal("known absent HXC field did not match empty filter")
	}
	zero := int64(0)
	row.openCount = &zero
	if memberGridMatches(row, donorGridCondition{Field: "open_count_7d", Operator: "is_empty"}) || !memberGridMatches(row, donorGridCondition{Field: "open_count_7d", Operator: "equals", Value: float64(0)}) {
		t.Fatal("verified zero was conflated with unavailable or empty")
	}
	if memberGridMatches(row, donorGridCondition{Field: "alliance", Operator: "is_empty"}) {
		t.Fatal("unowned alliance was silently treated as empty")
	}
}

func TestMemberGridCompositionDoesNotInventFactsForMissingHXCRow(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	snapshot := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	handler.members = &compositionEntitlements{snapshot: snapshot, pages: [][]orderport.Entitlement{{
		compositionRow(1, 1, snapshot.Add(48*time.Hour)),
		compositionRow(2, 2, snapshot.Add(48*time.Hour)),
	}}}
	handler.names = &compositionNames{values: map[customerdomain.CustomerID]string{1: "缺失 HXC", 2: "已有 HXC"}}
	handler.sharedFacts = &compositionFacts{version: 6, values: map[customerdomain.CustomerID]hxcport.SharedFacts{
		2: compositionFact(2, false), // explicit false and zero are verified facts.
	}}

	rows, _, err := handler.queryDonorGridComposed(context.Background(), 7, defaultDonorGridConfig(), "", 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%#v err=%v", rows, err)
	}
	byName := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		byName[responsePrimary(t, row)] = row["values"].(map[string]any)
	}
	missing, known := byName["缺失 HXC"], byName["已有 HXC"]
	missingOpen, missingOpenOK := missing["open_count_7d"].(*int64)
	if missing["hxc_unavailable"] != true || missing["formally_logged_in"] != "unavailable" || missing["token_usage"] != "unavailable" || !missingOpenOK || missingOpen != nil {
		t.Fatalf("missing HXC row became known: %#v", missing)
	}
	knownOpen, knownOpenOK := known["open_count_7d"].(*int64)
	if known["hxc_unavailable"] != false || known["formally_logged_in"] != "no" || known["token_usage"] != "no" || !knownOpenOK || knownOpen == nil || *knownOpen != 0 {
		t.Fatalf("explicit HXC facts lost: %#v", known)
	}

	no := compositionConfig(t, `{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"formally_logged_in","operator":"in","value":["no"]}]},"sorts":[],"groups":[]}`)
	matched, _, err := handler.queryDonorGridComposed(context.Background(), 7, no, "", 10)
	if err != nil || len(matched) != 1 || responsePrimary(t, matched[0]) != "已有 HXC" {
		t.Fatalf("known false filter matched=%#v err=%v", matched, err)
	}
}

func TestMemberGridCompositionDropsPartialHXCFirstRead(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	snapshot := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	items := make([]orderport.Entitlement, 0, 501)
	for id := int64(1); id <= 501; id++ {
		items = append(items, compositionRow(id, id, snapshot.Add(48*time.Hour)))
	}
	handler.members = &compositionEntitlements{snapshot: snapshot, pages: [][]orderport.Entitlement{items}}
	handler.names = &compositionNames{values: map[customerdomain.CustomerID]string{1: "Alpha"}}
	handler.sharedFacts = &compositionFacts{
		version: 8,
		values:  map[customerdomain.CustomerID]hxcport.SharedFacts{1: compositionFact(1, true)},
		// The first 500-ID batch succeeds, then the HXC Owner fails before
		// producing the second one. Product must not retain the first batch.
		failOnCall: 2,
		failErr:    errors.New("temporary HXC read failure"),
	}
	config := compositionConfig(t, `{"schema_version":1,"filter":{"logic":"and","conditions":[]},"sorts":[{"field":"member","direction":"asc"}],"groups":[]}`)
	rows, _, err := handler.queryDonorGridComposed(context.Background(), 7, config, "", 1)
	if err != nil || len(rows) != 1 || responsePrimary(t, rows[0]) != "Alpha" {
		t.Fatalf("rows=%#v err=%v", rows, err)
	}
	values := rows[0]["values"].(map[string]any)
	if values["hxc_unavailable"] != true || values["formally_logged_in"] != "unavailable" || values["token_usage"] != "unavailable" {
		t.Fatalf("partial HXC facts leaked into first page: %#v", values)
	}
}

func TestMemberGridCompositionDoesNotUseDisplayPlaceholderAsCustomerName(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	snapshot := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	handler.members = &compositionEntitlements{snapshot: snapshot, pages: [][]orderport.Entitlement{{
		compositionRow(1, 1, snapshot.Add(48*time.Hour)),
		compositionRow(2, 2, snapshot.Add(48*time.Hour)),
	}}}
	// Customer 1 has no directory display fact. Customer 2 really is named
	// "客户", so a UI placeholder must never make customer 1 match its filter.
	handler.names = &compositionNames{values: map[customerdomain.CustomerID]string{2: "客户"}}
	contains := compositionConfig(t, `{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"member","operator":"contains","value":"客户"}]},"sorts":[{"field":"member","direction":"asc"}],"groups":[]}`)
	rows, _, err := handler.queryDonorGridComposed(context.Background(), 7, contains, "", 10)
	if err != nil || len(rows) != 1 || rows[0]["record_id"] != memberGridMemberRef(2) {
		t.Fatalf("placeholder name filter rows=%#v err=%v", rows, err)
	}
	empty := compositionConfig(t, `{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"member","operator":"is_empty"}]},"sorts":[],"groups":[]}`)
	rows, _, err = handler.queryDonorGridComposed(context.Background(), 7, empty, "", 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("unknown customer name became empty filter match: %#v err=%v", rows, err)
	}
}

func TestDecodeDonorGridConfigAcceptsFrozenFieldOperators(t *testing.T) {
	config := compositionConfig(t, `{"schema_version":1,"filter":{"logic":"and","conditions":[
{"field":"member","operator":"contains","value":"Alpha"},
{"field":"remaining_days","operator":"between","value":[9,1]},
{"field":"formally_logged_in","operator":"in","value":["yes","unmatched"]},
{"field":"token_usage","operator":"not_in","value":["no"]},
{"field":"learning_plan_progress","operator":"ratio_between","value":[80,20]},
{"field":"open_count_7d","operator":"gte","value":0},
{"field":"last_open_at","operator":"after","value":"2026-09-05T00:00:00Z"},
{"field":"renewal_count","operator":"is_not_empty"},
{"field":"remark","operator":"is_empty"},
{"field":"alliance","operator":"contains","value":"联盟"}]},
"sorts":[{"field":"member","direction":"asc"},{"field":"open_count_7d","direction":"desc"}],
"groups":[{"field":"formally_logged_in","direction":"asc"},{"field":"learning_plan_progress","direction":"desc"}]}`)
	if got := config.Filter.Conditions[1].Value.([]any); got[0] != float64(1) || got[1] != float64(9) {
		t.Fatalf("number range=%#v", got)
	}
	if got := config.Filter.Conditions[4].Value.([]any); got[0] != float64(20) || got[1] != float64(80) {
		t.Fatalf("ratio range=%#v", got)
	}
	if len(config.Filter.Conditions) != 10 || len(config.Sorts) != 2 || len(config.Groups) != 2 {
		t.Fatalf("config=%+v", config)
	}
}

func TestMemberGridHTTPReturnsExplicitCursorStale(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	snapshot := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	members := &compositionEntitlements{snapshot: snapshot, pages: [][]orderport.Entitlement{{compositionRow(1, 1, snapshot.Add(48*time.Hour)), compositionRow(2, 2, snapshot.Add(48*time.Hour))}}}
	names := &compositionNames{values: map[customerdomain.CustomerID]string{1: "Alpha", 2: "Beta"}}
	handler.members, handler.names = members, names
	body := `{"config":{"schema_version":1,"filter":{"logic":"and","conditions":[]},"sorts":[{"field":"member","direction":"asc"}],"groups":[]},"limit":1}`
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/api/admin/service-period-products/7/member-grid/query", strings.NewReader(body)))
	if first.Code != http.StatusOK {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	var payload struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil || payload.NextCursor == "" {
		t.Fatalf("first cursor=%q err=%v", payload.NextCursor, err)
	}
	names.mu.Lock()
	names.values[2] = "Aardvark"
	names.mu.Unlock()
	secondBody := strings.TrimSuffix(body, "}") + `,"cursor":"` + payload.NextCursor + `"}`
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/api/admin/service-period-products/7/member-grid/query", strings.NewReader(secondBody)))
	if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), `"code":"CURSOR_STALE"`) {
		t.Fatalf("stale=%d %s", second.Code, second.Body.String())
	}
}
