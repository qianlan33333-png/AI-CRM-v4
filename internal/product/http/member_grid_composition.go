package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

const (
	memberGridCandidateLimit = 10000
	memberGridOrderPageSize  = 200
)

var (
	errMemberGridCursorStale = errors.New("member-grid cursor is stale")
	errMemberGridTooLarge    = errors.New("member-grid candidate relation is too large")
)

// memberGridCursor deliberately has no display name, remark, HXC values, or
// other customer data. relationHash lets the next page prove that its complete
// recomputed relation has not changed before it resumes after LastID.
type memberGridCursor struct {
	V              int    `json:"v"`
	ProductID      int64  `json:"p"`
	ConfigHash     string `json:"c"`
	SnapshotAt     string `json:"s"`
	HXCVersion     int64  `json:"h"`
	HXCUnavailable bool   `json:"u,omitempty"`
	RelationHash   string `json:"r"`
	LastID         int64  `json:"l"`
}

type memberGridRow struct {
	entitlement orderport.Entitlement
	name        string
	nameKnown   bool
	remaining   int64

	formal       string
	token        string
	hxcKnown     bool
	progress     string
	progressRate *float64
	progressNow  *int64
	progressMax  *int64
	openCount    *int64
	lastOpen     *time.Time

	renewal       *int64
	groupCounts   []memberGridGroupCount
	alliance      *string
	allianceKnown bool
}

func (h *Handler) queryDonorGridComposed(ctx context.Context, productID int64, config donorGridConfig, rawCursor string, limit int32) ([]map[string]any, string, error) {
	return h.queryDonorGridWithMetrics(ctx, productID, config, rawCursor, limit, nil)
}

type memberGridMetrics struct {
	Total      int       `json:"total"`
	Active     int       `json:"active"`
	Expired    int       `json:"expired"`
	Expiring7D int       `json:"expiring_7d"`
	Other      int       `json:"other"`
	SnapshotAt time.Time `json:"snapshot_at"`
}

func (h *Handler) queryDonorGridWithMetrics(ctx context.Context, productID int64, config donorGridConfig, rawCursor string, limit int32, metrics *memberGridMetrics) ([]map[string]any, string, error) {
	if h == nil || h.members == nil || h.names == nil || limit < 1 || limit > 200 {
		return nil, "", errors.New("member readers unavailable")
	}
	configHash, err := donorGridConfigHash(config)
	if err != nil {
		return nil, "", err
	}
	cursor, err := h.decodeMemberGridCursor(rawCursor)
	if err != nil {
		return nil, "", errMemberGridCursorStale
	}
	snapshot := time.Now().UTC()
	factsVersion, factsUnavailable := int64(0), false
	if cursor != nil {
		if cursor.ProductID != productID || cursor.ConfigHash != configHash {
			return nil, "", errMemberGridCursorStale
		}
		snapshot, err = time.Parse(time.RFC3339Nano, cursor.SnapshotAt)
		if err != nil {
			return nil, "", errMemberGridCursorStale
		}
		snapshot = snapshot.UTC()
		factsVersion, factsUnavailable = cursor.HXCVersion, cursor.HXCUnavailable
		// The cursor keeps one HXC generation for all bounded reads, but it
		// must not silently carry a stale generation across a later published
		// source. A fresh current version therefore asks the caller to reload;
		// a retained old version alone is not treated as current facts.
		if !factsUnavailable {
			currentVersion, currentUnavailable := h.memberGridFactsVersion(ctx)
			if currentUnavailable || currentVersion != factsVersion {
				return nil, "", errMemberGridCursorStale
			}
		}
	} else {
		factsVersion, factsUnavailable = h.memberGridFactsVersion(ctx)
	}

	items, effectiveSnapshot, err := h.memberGridOrderCandidates(ctx, productID, snapshot)
	if err != nil {
		return nil, "", err
	}
	if cursor == nil {
		snapshot = effectiveSnapshot
	}
	names, err := h.memberGridDisplayNames(ctx, items)
	if err != nil {
		return nil, "", err
	}
	facts, unavailable, err := h.memberGridFacts(ctx, items, factsVersion, factsUnavailable, cursor != nil)
	if err != nil {
		return nil, "", err
	}
	rows := memberGridRows(items, names, facts, unavailable, snapshot)
	if config.Base != nil {
		rows = filterMemberGridRows(rows, *config.Base)
	}
	rows = filterMemberGridRows(rows, config)
	sortMemberGridRows(rows, config)
	attachMemberGridGroupCounts(rows, config)
	if metrics != nil {
		*metrics = summarizeMemberGrid(rows, snapshot)
	}

	relationHash, err := memberGridRelationHash(rows, configHash, snapshot, factsVersion, unavailable)
	if err != nil {
		return nil, "", err
	}
	start := 0
	if cursor != nil {
		if cursor.RelationHash != relationHash {
			return nil, "", errMemberGridCursorStale
		}
		for start < len(rows) && rows[start].entitlement.ID != cursor.LastID {
			start++
		}
		if start == len(rows) {
			return nil, "", errMemberGridCursorStale
		}
		start++
	}
	end := start + int(limit)
	if end > len(rows) {
		end = len(rows)
	}
	result := make([]map[string]any, 0, end-start)
	for _, row := range rows[start:end] {
		result = append(result, donorGridCompositeResponse(row, config))
	}
	if end == len(rows) || end == start {
		return result, "", nil
	}
	next, err := h.encodeMemberGridCursor(memberGridCursor{
		V: 1, ProductID: productID, ConfigHash: configHash,
		SnapshotAt: snapshot.Format(time.RFC3339Nano), HXCVersion: factsVersion,
		HXCUnavailable: unavailable, RelationHash: relationHash, LastID: rows[end-1].entitlement.ID,
	})
	if err != nil {
		return nil, "", err
	}
	return result, next, nil
}

func (h *Handler) memberGridOrderCandidates(ctx context.Context, productID int64, snapshot time.Time) ([]orderport.Entitlement, time.Time, error) {
	items := make([]orderport.Entitlement, 0, memberGridOrderPageSize)
	cursor := ""
	frozen := snapshot.UTC()
	for {
		page, err := h.members.ListServicePeriodMembers(ctx, orderport.ServicePeriodMemberQuery{
			ServiceProductID: productID, Cursor: cursor, Limit: memberGridOrderPageSize, SnapshotAt: frozen,
		})
		if err != nil {
			return nil, time.Time{}, err
		}
		if !page.SnapshotAt.IsZero() {
			if cursor != "" && !page.SnapshotAt.Equal(frozen) {
				return nil, time.Time{}, errMemberGridCursorStale
			}
			frozen = page.SnapshotAt.UTC()
		}
		for _, item := range page.Items {
			items = append(items, item)
			if len(items) > memberGridCandidateLimit {
				return nil, time.Time{}, errMemberGridTooLarge
			}
		}
		if page.NextCursor == "" {
			return items, frozen, nil
		}
		cursor = page.NextCursor
	}
}

func (h *Handler) memberGridDisplayNames(ctx context.Context, items []orderport.Entitlement) (map[customerdomain.CustomerID]string, error) {
	ids := make([]customerdomain.CustomerID, 0, len(items))
	seen := make(map[customerdomain.CustomerID]bool, len(items))
	for _, item := range items {
		id := customerdomain.CustomerID(item.CustomerID)
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	out := make(map[customerdomain.CustomerID]string, len(ids))
	for first := 0; first < len(ids); first += 200 {
		last := first + 200
		if last > len(ids) {
			last = len(ids)
		}
		batch, err := h.names.DisplayNames(ctx, ids[first:last])
		if err != nil {
			return nil, err
		}
		for id, value := range batch {
			out[id] = strings.TrimSpace(value)
		}
	}
	return out, nil
}

func (h *Handler) memberGridFactsVersion(ctx context.Context) (int64, bool) {
	if h.sharedFacts == nil {
		return 0, true
	}
	version, err := h.sharedFacts.CurrentSharedFactsVersion(ctx)
	// HXC facts are optional to this member relation. Before its first
	// publication (and during an independent HXC read outage) Product still
	// renders Order and Customer facts, marking only HXC columns unavailable.
	if err != nil || version < 1 {
		return 0, true
	}
	return version, false
}

func (h *Handler) memberGridFacts(ctx context.Context, items []orderport.Entitlement, version int64, unavailable, pinned bool) (map[customerdomain.CustomerID]hxcport.SharedFacts, bool, error) {
	out := make(map[customerdomain.CustomerID]hxcport.SharedFacts)
	if unavailable || version < 1 || h.sharedFacts == nil {
		return out, true, nil
	}
	ids := make([]customerdomain.CustomerID, 0, len(items))
	seen := make(map[customerdomain.CustomerID]bool, len(items))
	for _, item := range items {
		id := customerdomain.CustomerID(item.CustomerID)
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for first := 0; first < len(ids); first += hxcport.MaxSharedFactsCustomerIDs {
		last := first + hxcport.MaxSharedFactsCustomerIDs
		if last > len(ids) {
			last = len(ids)
		}
		batch, err := h.sharedFacts.SharedFactsAtVersion(ctx, version, ids[first:last])
		if err != nil {
			if pinned && errors.Is(err, hxcport.ErrSharedFactsVersionUnavailable) {
				return nil, false, errMemberGridCursorStale
			}
			if !pinned {
				// A first-page HXC read is optional. Do not combine successful
				// earlier batches with a failed later one: HXC zero/false values
				// would otherwise depend on a batch boundary. Discard every
				// partial result and mark this evaluation's HXC columns unknown.
				return map[customerdomain.CustomerID]hxcport.SharedFacts{}, true, nil
			}
			return nil, false, err
		}
		for id, fact := range batch {
			out[id] = fact
		}
	}
	return out, false, nil
}

func memberGridRows(items []orderport.Entitlement, names map[customerdomain.CustomerID]string, facts map[customerdomain.CustomerID]hxcport.SharedFacts, unavailable bool, snapshot time.Time) []memberGridRow {
	result := make([]memberGridRow, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(names[customerdomain.CustomerID(item.CustomerID)])
		row := memberGridRow{entitlement: item, name: name, nameKnown: name != "", remaining: int64(donorGridRemainingDays(item.EndAt, snapshot)), formal: "unavailable", token: "unavailable", progress: "unavailable", alliance: item.Alliance, allianceKnown: item.Alliance != nil}
		if item.RenewalCountAvailable {
			value := item.RenewalCount
			row.renewal = &value
		}
		fact, found := facts[customerdomain.CustomerID(item.CustomerID)]
		// A published HXC generation is not evidence that every canonical
		// customer has a row in it. Missing, unavailable, and ambiguous rows
		// all remain unknown; only an explicit available row can supply false
		// and zero facts to the member grid.
		if !unavailable && found && fact.Availability == hxcport.SharedFactsAvailable {
			row.hxcKnown = true
			row.formal, row.token, row.progress = "unmatched", "unmatched", "unmatched"
			if found {
				row.formal = memberGridYesNo(fact.FormallyLoggedIn)
				row.token = memberGridYesNo(fact.HasTokenUsage)
				row.progress, row.progressRate = memberGridProgress(fact)
				row.progressNow, row.progressMax = fact.LearningPlanCurrent, fact.LearningPlanTotal
				count := fact.CardOpenCount7D
				row.openCount = &count
				row.lastOpen = fact.CardLastOpenedAt
			}
		}
		result = append(result, row)
	}
	return result
}

func memberGridYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func memberGridProgress(fact hxcport.SharedFacts) (string, *float64) {
	if !fact.LearningPlanFound || fact.LearningPlanCurrent == nil || fact.LearningPlanTotal == nil || *fact.LearningPlanTotal <= 0 {
		return "no_plan", nil
	}
	ratio := math.Round(math.Min(100, math.Max(0, float64(*fact.LearningPlanCurrent)/float64(*fact.LearningPlanTotal)*100))*10000) / 10000
	if *fact.LearningPlanCurrent <= 0 {
		return "not_started", &ratio
	}
	if *fact.LearningPlanCurrent >= *fact.LearningPlanTotal {
		return "complete", &ratio
	}
	return "in_progress", &ratio
}

func filterMemberGridRows(rows []memberGridRow, config donorGridConfig) []memberGridRow {
	if len(config.Filter.Conditions) == 0 {
		return rows
	}
	out := make([]memberGridRow, 0, len(rows))
	for _, row := range rows {
		matched := config.Filter.Logic == "and"
		for _, condition := range config.Filter.Conditions {
			ok := memberGridMatches(row, condition)
			if config.Filter.Logic == "or" && ok {
				matched = true
				break
			}
			if config.Filter.Logic != "or" && !ok {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, row)
		}
	}
	return out
}

func memberGridMatches(row memberGridRow, condition donorGridCondition) bool {
	field, operator := condition.Field, condition.Operator
	if field == "member" || field == "remark" || field == "alliance" {
		value, known := row.memberGridText(field)
		if !known {
			return false
		}
		target, _ := condition.Value.(string)
		target, value = strings.ToLower(strings.TrimSpace(target)), strings.ToLower(strings.TrimSpace(value))
		switch operator {
		case "contains":
			return strings.Contains(value, target)
		case "not_contains":
			return !strings.Contains(value, target)
		case "equals":
			return value == target
		case "not_equals":
			return value != target
		case "is_empty":
			return value == ""
		case "is_not_empty":
			return value != ""
		}
	}
	if field == "formally_logged_in" || field == "token_usage" {
		if !row.hxcKnown {
			return false
		}
		value := row.formal
		if field == "token_usage" {
			value = row.token
		}
		values, _ := condition.Value.([]any)
		found := false
		for _, candidate := range values {
			found = found || candidate == value
		}
		return (operator == "in" && found) || (operator == "not_in" && !found)
	}
	if field == "learning_plan_progress" {
		if !row.hxcKnown {
			return false
		}
		if operator == "state_in" {
			values, _ := condition.Value.([]any)
			for _, candidate := range values {
				if candidate == row.progress {
					return true
				}
			}
			return false
		}
		return memberGridNumberMatch(row.progressRate, true, strings.TrimPrefix(operator, "ratio_"), condition.Value)
	}
	if field == "last_open_at" {
		return memberGridTimeMatch(row.lastOpen, row.hxcKnown, operator, condition.Value)
	}
	var value *float64
	known := true
	switch field {
	case "remaining_days":
		v := float64(row.remaining)
		value = &v
	case "renewal_count":
		known = row.renewal != nil
		if row.renewal != nil {
			v := float64(*row.renewal)
			value = &v
		}
	case "open_count_7d":
		known = row.hxcKnown
		if row.openCount != nil {
			v := float64(*row.openCount)
			value = &v
		}
	}
	return memberGridNumberMatch(value, known, operator, condition.Value)
}

func (row memberGridRow) memberGridText(field string) (string, bool) {
	switch field {
	case "member":
		return row.name, row.nameKnown
	case "remark":
		return row.entitlement.Remark, true
	case "alliance":
		if row.alliance == nil {
			return "", false
		}
		return *row.alliance, true
	}
	return "", false
}

func memberGridNumberMatch(value *float64, known bool, operator string, raw any) bool {
	if !known {
		return false
	}
	if operator == "is_empty" {
		return value == nil
	}
	if operator == "is_not_empty" {
		return value != nil
	}
	if value == nil {
		return false
	}
	values, ok := memberGridNumbers(raw, operator)
	if !ok {
		return false
	}
	switch operator {
	case "equals":
		return *value == values[0]
	case "not_equals":
		return *value != values[0]
	case "gt":
		return *value > values[0]
	case "gte":
		return *value >= values[0]
	case "lt":
		return *value < values[0]
	case "lte":
		return *value <= values[0]
	case "between":
		return *value >= values[0] && *value <= values[1]
	}
	return false
}

func memberGridNumbers(raw any, operator string) ([]float64, bool) {
	if operator == "between" || operator == "ratio_between" {
		values, ok := raw.([]any)
		if !ok || len(values) != 2 {
			return nil, false
		}
		left, lok := values[0].(float64)
		right, rok := values[1].(float64)
		return []float64{left, right}, lok && rok
	}
	value, ok := raw.(float64)
	return []float64{value}, ok
}

func memberGridTimeMatch(value *time.Time, known bool, operator string, raw any) bool {
	if !known {
		return false
	}
	if operator == "is_empty" {
		return value == nil
	}
	if operator == "is_not_empty" {
		return value != nil
	}
	if value == nil {
		return false
	}
	if operator == "between" {
		values, ok := raw.([]any)
		if !ok || len(values) != 2 {
			return false
		}
		leftRaw, leftOK := values[0].(string)
		rightRaw, rightOK := values[1].(string)
		if !leftOK || !rightOK {
			return false
		}
		left, lerr := time.Parse(time.RFC3339Nano, leftRaw)
		right, rerr := time.Parse(time.RFC3339Nano, rightRaw)
		return lerr == nil && rerr == nil && !value.Before(left) && !value.After(right)
	}
	target, ok := raw.(string)
	if !ok {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, target)
	if err != nil {
		return false
	}
	if operator == "before" {
		return value.Before(parsed)
	}
	return operator == "after" && value.After(parsed)
}

func sortMemberGridRows(rows []memberGridRow, config donorGridConfig) {
	sort.SliceStable(rows, func(left, right int) bool {
		for _, item := range config.Groups {
			if result := compareMemberGridField(rows[left], rows[right], item.Field, true); result != 0 {
				return memberGridDirection(result, item.Direction) < 0
			}
		}
		for _, item := range config.Sorts {
			if result := compareMemberGridField(rows[left], rows[right], item.Field, false); result != 0 {
				return memberGridDirection(result, item.Direction) < 0
			}
		}
		if len(config.Sorts) == 0 {
			if !rows[left].entitlement.EndAt.Equal(rows[right].entitlement.EndAt) {
				return rows[left].entitlement.EndAt.After(rows[right].entitlement.EndAt)
			}
		}
		return rows[left].entitlement.ID > rows[right].entitlement.ID
	})
}

func memberGridDirection(result int, direction string) int {
	if direction == "desc" {
		return -result
	}
	return result
}

func compareMemberGridField(left, right memberGridRow, field string, group bool) int {
	lv, lknown := left.memberGridOrderValue(field, group)
	rv, rknown := right.memberGridOrderValue(field, group)
	if lknown != rknown {
		if lknown {
			return -1
		}
		return 1
	}
	if !lknown {
		return 0
	}
	if lv == nil || rv == nil {
		if lv == nil && rv == nil {
			return 0
		}
		if lv == nil {
			return 1
		}
		return -1
	}
	switch l := lv.(type) {
	case int64:
		r := rv.(int64)
		if l < r {
			return -1
		}
		if l > r {
			return 1
		}
	case float64:
		r := rv.(float64)
		if l < r {
			return -1
		}
		if l > r {
			return 1
		}
	case string:
		r := rv.(string)
		if l < r {
			return -1
		}
		if l > r {
			return 1
		}
	case time.Time:
		r := rv.(time.Time)
		if l.Before(r) {
			return -1
		}
		if l.After(r) {
			return 1
		}
	}
	return 0
}

func (row memberGridRow) memberGridOrderValue(field string, group bool) (any, bool) {
	switch field {
	case "member":
		if !row.nameKnown {
			return nil, false
		}
		return strings.ToLower(row.name), true
	case "remaining_days":
		return row.remaining, true
	case "formally_logged_in":
		return int64(memberGridTriRank(row.formal)), row.hxcKnown
	case "token_usage":
		return int64(memberGridTriRank(row.token)), row.hxcKnown
	case "learning_plan_progress":
		if !row.hxcKnown {
			return nil, false
		}
		if group {
			return int64(memberGridProgressRank(row.progress)), true
		}
		if row.progressRate == nil {
			return nil, true
		}
		return *row.progressRate, true
	case "open_count_7d":
		if !row.hxcKnown {
			return nil, false
		}
		if row.openCount == nil {
			return nil, true
		}
		return *row.openCount, true
	case "last_open_at":
		if !row.hxcKnown {
			return nil, false
		}
		if row.lastOpen == nil {
			return nil, true
		}
		if group {
			return row.lastOpen.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02"), true
		}
		return row.lastOpen.UTC(), true
	case "renewal_count":
		if row.renewal == nil {
			return nil, false
		}
		return *row.renewal, true
	case "remark":
		value := strings.ToLower(strings.TrimSpace(row.entitlement.Remark))
		if value == "" {
			return nil, true
		}
		return value, true
	case "alliance":
		if row.alliance == nil {
			return nil, false
		}
		value := strings.ToLower(strings.TrimSpace(*row.alliance))
		if value == "" {
			return nil, true
		}
		return value, true
	}
	return nil, false
}

func memberGridTriRank(value string) int {
	switch value {
	case "yes":
		return 0
	case "no":
		return 1
	case "unmatched":
		return 2
	default:
		return 3
	}
}
func memberGridProgressRank(value string) int {
	switch value {
	case "unmatched":
		return 0
	case "no_plan":
		return 1
	case "not_started":
		return 2
	case "in_progress":
		return 3
	case "complete":
		return 4
	default:
		return 5
	}
}

type memberGridGroupCount struct {
	value any
	known bool
	count int64
}

func attachMemberGridGroupCounts(rows []memberGridRow, config donorGridConfig) {
	if len(config.Groups) == 0 {
		return
	}
	counts := make([]map[string]int64, len(config.Groups))
	for index := range counts {
		counts[index] = make(map[string]int64)
	}
	for _, row := range rows {
		path := ""
		for index, group := range config.Groups {
			value, known := row.memberGridGroupValue(group.Field)
			path += "|" + memberGridGroupKey(value, known)
			counts[index][path]++
		}
	}
	for index := range rows {
		rows[index].setGroupCounts(config, counts)
	}
}

func (row *memberGridRow) setGroupCounts(config donorGridConfig, counts []map[string]int64) {
	path := ""
	values := make([]memberGridGroupCount, 0, len(config.Groups))
	for index, group := range config.Groups {
		value, known := row.memberGridGroupValue(group.Field)
		path += "|" + memberGridGroupKey(value, known)
		values = append(values, memberGridGroupCount{value: value, known: known, count: counts[index][path]})
	}
	row.groupCounts = values
}

func (row memberGridRow) memberGridGroupValue(field string) (any, bool) {
	if field == "member" {
		if !row.nameKnown {
			return nil, false
		}
		// The donor groups by the display value while ordering names without
		// case sensitivity. Keep the original display case in the group label.
		return row.name, true
	}
	if field == "formally_logged_in" {
		return row.formal, row.hxcKnown
	}
	if field == "token_usage" {
		return row.token, row.hxcKnown
	}
	if field == "learning_plan_progress" {
		return row.progress, row.hxcKnown
	}
	if field == "remark" {
		value := strings.ToLower(strings.TrimSpace(row.entitlement.Remark))
		if value == "" {
			return nil, true
		}
		return value, true
	}
	return row.memberGridOrderValue(field, true)
}

func memberGridGroupKey(value any, known bool) string {
	if !known {
		return "unavailable"
	}
	return fmt.Sprintf("%T:%v", value, value)
}

func donorGridCompositeResponse(row memberGridRow, config donorGridConfig) map[string]any {
	values := map[string]any{
		"member":                    map[string]any{"primary": memberGridDisplayName(row), "secondary": ""},
		"remaining_days":            row.remaining,
		"formally_logged_in":        row.formal,
		"token_usage":               row.token,
		"learning_plan_progress":    map[string]any{"state": row.progress, "current": row.progressNow, "total": row.progressMax, "ratio": row.progressRate},
		"open_count_7d":             row.openCount,
		"last_open_at":              row.lastOpen,
		"remark":                    strings.TrimSpace(row.entitlement.Remark),
		"alliance":                  row.alliance,
		"alliance_unavailable":      !row.allianceKnown,
		"hxc_unavailable":           !row.hxcKnown,
		"renewal_count_unavailable": false,
	}
	if row.renewal != nil {
		values["renewal_count"] = *row.renewal
	} else {
		values["renewal_count"] = nil
		values["renewal_count_unavailable"] = true
	}
	paths := make([]any, 0, len(config.Groups))
	for index, group := range config.Groups {
		entry := memberGridGroupCount{known: false}
		if index < len(row.groupCounts) {
			entry = row.groupCounts[index]
		}
		paths = append(paths, map[string]any{"field": group.Field, "value": entry.value, "label": memberGridGroupLabel(group.Field, entry.value, entry.known), "count": entry.count, "unavailable": !entry.known})
	}
	return map[string]any{"record_id": memberGridMemberRef(row.entitlement.ID), "unionid": memberGridMemberRef(row.entitlement.ID), "version": row.entitlement.Version, "values": values, "group_path": paths}
}

// memberGridDisplayName is presentation-only. A missing Customer projection
// must not become the literal name used for matching, ordering, grouping, or
// cursor facts.
func memberGridDisplayName(row memberGridRow) string {
	if row.nameKnown {
		return row.name
	}
	return "客户"
}

func memberGridGroupLabel(field string, value any, known bool) string {
	if !known {
		return "不可用"
	}
	if value == nil || value == "" {
		return "空值"
	}
	switch field {
	case "remaining_days":
		return fmt.Sprintf("%d 天", value)
	case "renewal_count":
		return fmt.Sprintf("%d 次", value)
	case "formally_logged_in", "token_usage":
		if value == "yes" {
			return "是"
		}
		if value == "no" {
			return "否"
		}
		return "未匹配"
	case "learning_plan_progress":
		return map[string]string{"unmatched": "未匹配", "no_plan": "无计划", "not_started": "未开始", "in_progress": "进行中", "complete": "已完成"}[fmt.Sprint(value)]
	}
	return fmt.Sprint(value)
}

func memberGridRelationHash(rows []memberGridRow, configHash string, snapshot time.Time, hxcVersion int64, unavailable bool) (string, error) {
	type digestRow struct {
		ID, CustomerID, Version, Remaining    int64
		End, Updated                          string
		Name, Remark, Formal, Token, Progress string
		Alliance                              *string
		NameKnown                             bool
		ProgressRate                          *float64
		Open                                  *int64
		Last                                  string
		Renewal                               *int64
		HXCKnown                              bool
	}
	digest := make([]digestRow, 0, len(rows))
	for _, row := range rows {
		last := ""
		if row.lastOpen != nil {
			last = row.lastOpen.UTC().Format(time.RFC3339Nano)
		}
		digest = append(digest, digestRow{ID: row.entitlement.ID, CustomerID: row.entitlement.CustomerID, Version: row.entitlement.Version, Remaining: row.remaining, End: row.entitlement.EndAt.UTC().Format(time.RFC3339Nano), Updated: row.entitlement.UpdatedAt.UTC().Format(time.RFC3339Nano), Name: row.name, NameKnown: row.nameKnown, Remark: row.entitlement.Remark, Alliance: row.alliance, Formal: row.formal, Token: row.token, Progress: row.progress, ProgressRate: row.progressRate, Open: row.openCount, Last: last, Renewal: row.renewal, HXCKnown: row.hxcKnown})
	}
	raw, err := json.Marshal(struct {
		Config, Snapshot string
		HXC              int64
		Unavailable      bool
		Rows             []digestRow
	}{configHash, snapshot.UTC().Format(time.RFC3339Nano), hxcVersion, unavailable, digest})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func donorGridConfigHash(config donorGridConfig) (string, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func (h *Handler) encodeMemberGridCursor(cursor memberGridCursor) (string, error) {
	if len(h.memberGridCursorKey) < 16 {
		return "", errors.New("member-grid cursor signing key unavailable")
	}
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, h.memberGridCursorKey)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (h *Handler) decodeMemberGridCursor(raw string) (*memberGridCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(h.memberGridCursorKey) < 16 {
		return nil, errors.New("cursor signing key unavailable")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return nil, errors.New("invalid cursor")
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, h.memberGridCursorKey)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return nil, errors.New("invalid cursor signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	var cursor memberGridCursor
	if json.Unmarshal(payload, &cursor) != nil || cursor.V != 1 || cursor.ProductID < 1 || cursor.ConfigHash == "" || cursor.RelationHash == "" || cursor.LastID < 1 {
		return nil, errors.New("invalid cursor")
	}
	return &cursor, nil
}

// productMemberGridQueryError maps the composition's explicit read contracts
// without altering unrelated Product error handling.
func productMemberGridQueryError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errMemberGridCursorStale):
		writeError(w, 409, "cursor_stale")
	case errors.Is(err, errMemberGridTooLarge):
		writeError(w, 422, "member_grid_too_large")
	default:
		return false
	}
	return true
}

func summarizeMemberGrid(rows []memberGridRow, at time.Time) memberGridMetrics {
	result := memberGridMetrics{Total: len(rows), SnapshotAt: at}
	for _, row := range rows {
		if row.entitlement.Status == "active" && row.entitlement.EndAt.After(at) {
			result.Active++
			if !row.entitlement.EndAt.After(at.Add(7 * 24 * time.Hour)) {
				result.Expiring7D++
			}
		} else if row.entitlement.Status == "expired" || (row.entitlement.Status == "active" && !row.entitlement.EndAt.After(at)) {
			result.Expired++
		} else {
			result.Other++
		}
	}
	return result
}
