package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// donorGridConfig is the frozen dd8 page's persisted view contract. Product
// accepts the complete configuration shape, then admits only fields whose
// current Owner can evaluate them over the full member relation.
type donorGridConfig struct {
	Base          *donorGridConfig `json:"-"`
	SchemaVersion int              `json:"schema_version"`
	Presentation  json.RawMessage  `json:"presentation,omitempty"`
	Filter        struct {
		Logic      string               `json:"logic"`
		Conditions []donorGridCondition `json:"conditions"`
	} `json:"filter"`
	Sorts  []donorGridOrder `json:"sorts"`
	Groups []donorGridOrder `json:"groups"`
}
type donorGridCondition struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value,omitempty"`
}
type donorGridOrder struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

func defaultDonorGridConfig() donorGridConfig {
	var c donorGridConfig
	c.SchemaVersion, c.Filter.Logic = 1, "and"
	c.Filter.Conditions, c.Sorts, c.Groups = []donorGridCondition{}, []donorGridOrder{}, []donorGridOrder{}
	return c
}

func decodeDonorGridConfig(raw json.RawMessage) (donorGridConfig, error) {
	if len(raw) == 0 {
		return defaultDonorGridConfig(), nil
	}
	var c donorGridConfig
	if json.Unmarshal(raw, &c) != nil || c.SchemaVersion != 1 {
		return donorGridConfig{}, errors.New("invalid member-grid config")
	}
	c.Filter.Logic = strings.ToLower(strings.TrimSpace(c.Filter.Logic))
	if c.Filter.Logic == "" {
		c.Filter.Logic = "and"
	}
	if (c.Filter.Logic != "and" && c.Filter.Logic != "or") || len(c.Filter.Conditions) > 20 || len(c.Sorts) > 8 || len(c.Groups) > 2 {
		return donorGridConfig{}, errors.New("invalid member-grid config")
	}
	sortFields, groupFields := map[string]bool{}, map[string]bool{}
	for index := range c.Sorts {
		item := &c.Sorts[index]
		item.Field, item.Direction = strings.TrimSpace(item.Field), strings.ToLower(strings.TrimSpace(item.Direction))
		if !donorGridField(item.Field) || sortFields[item.Field] || (item.Direction != "asc" && item.Direction != "desc") {
			return donorGridConfig{}, errors.New("unsupported member-grid sort")
		}
		sortFields[item.Field] = true
	}
	for index := range c.Groups {
		item := &c.Groups[index]
		item.Field, item.Direction = strings.TrimSpace(item.Field), strings.ToLower(strings.TrimSpace(item.Direction))
		if !donorGridField(item.Field) || groupFields[item.Field] || sortFields[item.Field] || (item.Direction != "asc" && item.Direction != "desc") {
			return donorGridConfig{}, errors.New("unsupported member-grid group")
		}
		groupFields[item.Field] = true
	}
	for index := range c.Filter.Conditions {
		item := c.Filter.Conditions[index]
		item.Field, item.Operator = strings.TrimSpace(item.Field), strings.ToLower(strings.TrimSpace(item.Operator))
		if !donorGridField(item.Field) {
			return donorGridConfig{}, errors.New("unsupported member-grid filter")
		}
		normalized, err := normalizeDonorGridCondition(item)
		if err != nil {
			return donorGridConfig{}, err
		}
		c.Filter.Conditions[index] = normalized
	}
	return c, nil
}

func donorGridField(field string) bool {
	switch field {
	case "member", "remaining_days", "formally_logged_in", "token_usage", "learning_plan_progress", "open_count_7d", "last_open_at", "renewal_count", "remark", "alliance":
		return true
	}
	return false
}

// orderOwnedDonorGridField remains only for the lower-level Order Port
// characterization helper. HTTP queries use the full Product composition.
func orderOwnedDonorGridField(field string) bool {
	return field == "remaining_days" || field == "renewal_count" || field == "remark"
}

func normalizeDonorGridCondition(item donorGridCondition) (donorGridCondition, error) {
	textField := item.Field == "member" || item.Field == "remark" || item.Field == "alliance"
	numberField := item.Field == "remaining_days" || item.Field == "renewal_count" || item.Field == "open_count_7d"
	enumField := item.Field == "formally_logged_in" || item.Field == "token_usage"
	if textField {
		switch item.Operator {
		case "contains", "not_contains", "equals", "not_equals":
			value, ok := item.Value.(string)
			if !ok || len([]rune(strings.TrimSpace(value))) > 200 {
				return item, errors.New("invalid member-grid text filter")
			}
			item.Value = strings.TrimSpace(value)
		case "is_empty", "is_not_empty":
			if item.Value != nil {
				return item, errors.New("invalid member-grid text filter")
			}
		default:
			return item, errors.New("invalid member-grid text filter")
		}
		return item, nil
	}
	if numberField {
		if err := validateDonorNumberCondition(&item, item.Operator); err != nil {
			return item, err
		}
		return item, nil
	}
	if enumField {
		if item.Operator != "in" && item.Operator != "not_in" {
			return item, errors.New("invalid member-grid enum filter")
		}
		values, ok := item.Value.([]any)
		if !ok || len(values) == 0 {
			return item, errors.New("invalid member-grid enum filter")
		}
		seen := map[string]bool{}
		normalized := make([]any, 0, len(values))
		for _, raw := range values {
			value, ok := raw.(string)
			if !ok || (value != "yes" && value != "no" && value != "unmatched") || seen[value] {
				return item, errors.New("invalid member-grid enum filter")
			}
			seen[value] = true
			normalized = append(normalized, value)
		}
		item.Value = normalized
		return item, nil
	}
	if item.Field == "last_open_at" {
		switch item.Operator {
		case "is_empty", "is_not_empty":
			if item.Value != nil {
				return item, errors.New("invalid member-grid datetime filter")
			}
			return item, nil
		case "before", "after":
			value, ok := item.Value.(string)
			if !ok {
				return item, errors.New("invalid member-grid datetime filter")
			}
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return item, errors.New("invalid member-grid datetime filter")
			}
			item.Value = parsed.UTC().Format(time.RFC3339Nano)
			return item, nil
		case "between":
			values, ok := item.Value.([]any)
			if !ok || len(values) != 2 {
				return item, errors.New("invalid member-grid datetime filter")
			}
			left, lok := values[0].(string)
			right, rok := values[1].(string)
			if !lok || !rok {
				return item, errors.New("invalid member-grid datetime filter")
			}
			leftAt, lerr := time.Parse(time.RFC3339Nano, left)
			rightAt, rerr := time.Parse(time.RFC3339Nano, right)
			if lerr != nil || rerr != nil {
				return item, errors.New("invalid member-grid datetime filter")
			}
			if leftAt.After(rightAt) {
				leftAt, rightAt = rightAt, leftAt
			}
			item.Value = []any{leftAt.UTC().Format(time.RFC3339Nano), rightAt.UTC().Format(time.RFC3339Nano)}
			return item, nil
		}
		return item, errors.New("invalid member-grid datetime filter")
	}
	if item.Field == "learning_plan_progress" {
		switch item.Operator {
		case "state_in":
			values, ok := item.Value.([]any)
			if !ok || len(values) == 0 {
				return item, errors.New("invalid member-grid progress filter")
			}
			allowed := map[string]bool{"unmatched": true, "no_plan": true, "not_started": true, "in_progress": true, "complete": true}
			seen := map[string]bool{}
			normalized := make([]any, 0, len(values))
			for _, raw := range values {
				value, ok := raw.(string)
				if !ok || !allowed[value] || seen[value] {
					return item, errors.New("invalid member-grid progress filter")
				}
				seen[value] = true
				normalized = append(normalized, value)
			}
			item.Value = normalized
			return item, nil
		case "ratio_equals", "ratio_gt", "ratio_gte", "ratio_lt", "ratio_lte", "ratio_between":
			if err := validateDonorNumberCondition(&item, strings.TrimPrefix(item.Operator, "ratio_")); err != nil {
				return item, err
			}
			return item, nil
		case "is_empty", "is_not_empty":
			if item.Value != nil {
				return item, errors.New("invalid member-grid progress filter")
			}
			return item, nil
		}
	}
	return item, errors.New("invalid member-grid filter")
}

func validateDonorNumberCondition(item *donorGridCondition, operator string) error {
	switch operator {
	case "equals", "not_equals", "gt", "gte", "lt", "lte", "between", "is_empty", "is_not_empty":
	default:
		return errors.New("invalid member-grid number filter")
	}
	if operator == "is_empty" || operator == "is_not_empty" {
		if item.Value != nil {
			return errors.New("invalid member-grid number filter")
		}
		return nil
	}
	values := []any{item.Value}
	if operator == "between" {
		raw, ok := item.Value.([]any)
		if !ok || len(raw) != 2 {
			return errors.New("invalid member-grid number filter")
		}
		values = raw
	}
	numbers := make([]any, 0, len(values))
	for _, raw := range values {
		value, ok := raw.(float64)
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("invalid member-grid number filter")
		}
		numbers = append(numbers, value)
	}
	if operator == "between" && numbers[0].(float64) > numbers[1].(float64) {
		numbers[0], numbers[1] = numbers[1], numbers[0]
	}
	if operator == "between" {
		item.Value = numbers
	} else {
		item.Value = numbers[0]
	}
	return nil
}

func donorGridOrderQuery(productID int64, c donorGridConfig, cursor string, limit int32) (orderport.ServicePeriodMemberQuery, error) {
	for _, item := range c.Sorts {
		if !orderOwnedDonorGridField(item.Field) {
			return orderport.ServicePeriodMemberQuery{}, errors.New("composite member-grid field requires Product composition")
		}
	}
	for _, item := range c.Groups {
		if !orderOwnedDonorGridField(item.Field) {
			return orderport.ServicePeriodMemberQuery{}, errors.New("composite member-grid field requires Product composition")
		}
	}
	for _, item := range c.Filter.Conditions {
		if !orderOwnedDonorGridField(item.Field) {
			return orderport.ServicePeriodMemberQuery{}, errors.New("composite member-grid field requires Product composition")
		}
	}
	query := orderport.ServicePeriodMemberQuery{ServiceProductID: productID, Cursor: cursor, Limit: limit, FilterLogic: c.Filter.Logic, GridSorts: make([]orderport.MemberGridOrder, 0, len(c.Sorts)), GridGroups: make([]orderport.MemberGridOrder, 0, len(c.Groups)), GridFilters: make([]orderport.MemberGridFilter, 0, len(c.Filter.Conditions))}
	for _, item := range c.Sorts {
		query.GridSorts = append(query.GridSorts, orderport.MemberGridOrder{Field: item.Field, Direction: item.Direction})
	}
	for _, item := range c.Groups {
		query.GridGroups = append(query.GridGroups, orderport.MemberGridOrder{Field: item.Field, Direction: item.Direction})
	}
	for _, filter := range c.Filter.Conditions {
		if filter.Field == "remark" {
			value, _ := filter.Value.(string)
			query.GridFilters = append(query.GridFilters, orderport.MemberGridFilter{Field: filter.Field, Operator: filter.Operator, Text: value})
			continue
		}
		values := []float64{}
		if filter.Operator == "is_empty" || filter.Operator == "is_not_empty" {
			query.GridFilters = append(query.GridFilters, orderport.MemberGridFilter{Field: filter.Field, Operator: filter.Operator})
			continue
		}
		if filter.Operator == "between" {
			for _, raw := range filter.Value.([]any) {
				values = append(values, raw.(float64))
			}
		} else {
			values = append(values, filter.Value.(float64))
		}
		query.GridFilters = append(query.GridFilters, orderport.MemberGridFilter{Field: filter.Field, Operator: filter.Operator, Numbers: values})
	}
	return query, nil
}

func donorGridSchema(editable bool) map[string]any {
	operator := func(id, label, kind string) map[string]any {
		return map[string]any{"id": id, "label": label, "value_kind": kind}
	}
	textOps := []any{operator("contains", "包含", "text"), operator("not_contains", "不包含", "text"), operator("equals", "等于", "scalar"), operator("not_equals", "不等于", "scalar"), operator("is_empty", "为空", "none"), operator("is_not_empty", "不为空", "none")}
	numberOps := []any{operator("equals", "等于", "scalar"), operator("not_equals", "不等于", "scalar"), operator("gt", "大于", "number"), operator("gte", "大于等于", "number"), operator("lt", "小于", "number"), operator("lte", "小于等于", "number"), operator("between", "介于", "range"), operator("is_empty", "为空", "none"), operator("is_not_empty", "不为空", "none")}
	enumOps := []any{operator("in", "属于", "multi_select"), operator("not_in", "不属于", "multi_select")}
	dateOps := []any{operator("before", "早于", "datetime"), operator("after", "晚于", "datetime"), operator("between", "介于", "range"), operator("is_empty", "为空", "none"), operator("is_not_empty", "不为空", "none")}
	progressOps := []any{operator("state_in", "状态属于", "multi_select"), operator("ratio_equals", "完成率等于", "number"), operator("ratio_gt", "完成率大于", "number"), operator("ratio_gte", "完成率大于等于", "number"), operator("ratio_lt", "完成率小于", "number"), operator("ratio_lte", "完成率小于等于", "number"), operator("ratio_between", "完成率介于", "range"), operator("is_empty", "为空", "none"), operator("is_not_empty", "不为空", "none")}
	field := func(id, label, typ, icon string, ops []any, sortable, groupable, writable bool, options []any) map[string]any {
		return map[string]any{"id": id, "label": label, "type": typ, "icon": icon, "filter_operators": ops, "sortable": sortable, "groupable": groupable, "editable": writable, "options": options}
	}
	triOptions := []any{map[string]any{"value": "yes", "label": "是"}, map[string]any{"value": "no", "label": "否"}, map[string]any{"value": "unmatched", "label": "未匹配"}}
	progressOptions := []any{map[string]any{"value": "unmatched", "label": "未匹配"}, map[string]any{"value": "no_plan", "label": "无计划"}, map[string]any{"value": "not_started", "label": "未开始"}, map[string]any{"value": "in_progress", "label": "进行中"}, map[string]any{"value": "complete", "label": "已完成"}}
	fields := []any{
		field("member", "会员", "text", "text", textOps, true, true, false, []any{}),
		field("remaining_days", "剩余有效期", "number", "number", numberOps, true, true, false, []any{}),
		field("formally_logged_in", "正式登录", "tri_state", "person", enumOps, true, true, false, triOptions),
		field("token_usage", "token 消耗", "tri_state", "check", enumOps, true, true, false, triOptions),
		field("learning_plan_progress", "学习计划进度", "progress", "progress", progressOps, true, true, false, progressOptions),
		field("open_count_7d", "近 7 天打开次数", "number", "number", numberOps, true, true, false, []any{}),
		field("last_open_at", "最后打开时间", "datetime", "datetime", dateOps, true, true, false, []any{}),
		field("renewal_count", "续费次数", "number", "number", numberOps, true, true, false, []any{}),
		field("remark", "备注", "text", "text", textOps, true, true, editable, []any{}),
		field("alliance", "联盟", "text", "text", textOps, true, true, editable, []any{}),
	}
	return map[string]any{"schema_version": 1, "fields": fields, "limits": map[string]any{"filter_conditions": 20, "sorts": 8, "groups": 2, "page_size": 100}}
}

func donorGridViewResponse(v productport.MemberGridView) map[string]any {
	c, err := decodeDonorGridConfig(v.Config)
	if err != nil {
		c = defaultDonorGridConfig()
	}
	raw, _ := json.Marshal(c)
	return map[string]any{"id": strconvID(v.ID), "view_id": v.ID, "service_product_id": v.ProductID, "name": v.Name, "position": v.Position, "is_default": false, "version": v.Version, "config": json.RawMessage(raw), "created_at": v.CreatedAt.UTC(), "updated_at": v.UpdatedAt.UTC()}
}
func defaultDonorGridView() map[string]any {
	raw, _ := json.Marshal(defaultDonorGridConfig())
	return map[string]any{"id": "default", "name": "默认视图", "position": 0, "is_default": true, "version": 1, "config": json.RawMessage(raw)}
}
func strconvID(id productport.ID) string { return fmt.Sprintf("%d", id) }

func donorGridRemainingDays(endAt, snapshot time.Time) int {
	seconds := endAt.Sub(snapshot).Seconds()
	if seconds <= 0 {
		return 0
	}
	return max(1, int(math.Ceil(seconds/86400)))
}

func (h *Handler) donorGridRows(ctx context.Context, items []orderport.Entitlement, c donorGridConfig) ([]map[string]any, error) {
	return h.donorGridRowsAt(ctx, items, c, time.Now().UTC())
}

func (h *Handler) donorGridRowsAt(ctx context.Context, items []orderport.Entitlement, c donorGridConfig, snapshot time.Time) ([]map[string]any, error) {
	ids := make([]customerdomain.CustomerID, 0, len(items))
	for _, item := range items {
		ids = append(ids, customerdomain.CustomerID(item.CustomerID))
	}
	names, err := h.names.DisplayNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		remaining := donorGridRemainingDays(item.EndAt, snapshot)
		name := names[customerdomain.CustomerID(item.CustomerID)]
		if name == "" {
			name = "客户"
		}
		path := donorGridGroupPath(item, c, remaining)
		var renewalCount any
		if item.RenewalCountAvailable {
			renewalCount = item.RenewalCount
		}
		out = append(out, map[string]any{"record_id": memberGridMemberRef(item.ID), "unionid": memberGridMemberRef(item.ID), "version": item.Version, "values": map[string]any{"member": map[string]any{"primary": name, "secondary": ""}, "remaining_days": remaining, "formally_logged_in": "unmatched", "token_usage": "unmatched", "learning_plan_progress": map[string]any{"state": "unmatched"}, "open_count_7d": nil, "last_open_at": nil, "renewal_count": renewalCount, "renewal_count_unavailable": !item.RenewalCountAvailable, "remark": strings.TrimSpace(item.Remark), "alliance": item.Alliance, "alliance_unavailable": item.Alliance == nil}, "group_path": path})
	}
	return out, nil
}

func donorGridGroupPath(item orderport.Entitlement, c donorGridConfig, remaining int) []any {
	path := make([]any, 0, len(c.Groups))
	for index, group := range c.Groups {
		var value any
		switch group.Field {
		case "remaining_days":
			value = remaining
		case "renewal_count":
			if item.RenewalCountAvailable {
				value = item.RenewalCount
			}
		case "remark":
			if text := strings.ToLower(strings.TrimSpace(item.Remark)); text != "" {
				value = text
			}
		case "alliance":
			if item.Alliance != nil {
				if text := strings.ToLower(strings.TrimSpace(*item.Alliance)); text != "" {
					value = text
				}
			}
		}
		count := int64(0)
		if index < len(item.MemberGridGroupCounts) {
			count = item.MemberGridGroupCounts[index]
		} else if index == 0 {
			// Compatibility test doubles from before the two-level contract carry
			// the original first-level count only. PostgreSQL always supplies the
			// full ordered slice above.
			count = item.MemberGridGroupCount
		}
		path = append(path, map[string]any{"field": group.Field, "value": value, "label": donorGridGroupLabel(group.Field, value), "count": count})
	}
	return path
}

func donorGridGroupLabel(field string, value any) string {
	if value == nil || value == "" {
		return "空值"
	}
	switch field {
	case "remaining_days":
		return fmt.Sprintf("%d 天", value)
	case "renewal_count":
		return fmt.Sprintf("%d 次", value)
	default:
		return fmt.Sprint(value)
	}
}

func (h *Handler) queryDonorGrid(ctx context.Context, productID int64, c donorGridConfig, cursor string, limit int32) ([]map[string]any, string, error) {
	if h.members == nil || h.names == nil {
		return nil, "", errors.New("member readers unavailable")
	}
	query, err := donorGridOrderQuery(productID, c, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	// Freeze the same instant for Order's SQL filter and the displayed rows.
	// This preserves dd8's ceil-and-clamp remaining-day behavior even at a
	// day boundary.
	query.SnapshotAt = time.Now().UTC()
	page, err := h.members.ListServicePeriodMembers(ctx, query)
	if err != nil {
		return nil, "", err
	}
	snapshot := page.SnapshotAt
	if snapshot.IsZero() {
		snapshot = query.SnapshotAt
	}
	rows, err := h.donorGridRowsAt(ctx, page.Items, c, snapshot)
	return rows, page.NextCursor, err
}

func (h *Handler) publicMemberGridShare(ctx context.Context, token string) (productport.MemberGridShare, bool) {
	if h == nil || h.workspace == nil || strings.TrimSpace(token) == "" {
		return productport.MemberGridShare{}, false
	}
	share, err := h.workspace.ResolveShare(ctx, strings.TrimSpace(token))
	if err != nil || !share.Enabled || share.ProductID < 1 {
		return productport.MemberGridShare{}, false
	}
	return share, true
}

func (h *Handler) publicMemberGridBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	share, ok := h.publicMemberGridShare(r.Context(), r.Header.Get("X-AICRM-Grid-Share-Token"))
	if !ok {
		writeError(w, http.StatusGone, "share_gone")
		return
	}
	product, err := h.service.GetServicePeriodProduct(r.Context(), share.ProductID)
	if err != nil {
		writeError(w, http.StatusGone, "share_gone")
		return
	}
	views, err := h.workspace.ListViews(r.Context(), share.ProductID)
	if err != nil {
		resultError(w, err)
		return
	}
	items := []any{defaultDonorGridView()}
	for _, view := range views {
		item := donorGridViewResponse(view)
		delete(item, "config")
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service_product_id": share.ProductID, "product": map[string]any{"title": product.Name}, "schema": donorGridSchema(false), "views": items})
}

func (h *Handler) publicMemberGridQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	share, ok := h.publicMemberGridShare(r.Context(), r.Header.Get("X-AICRM-Grid-Share-Token"))
	if !ok {
		writeError(w, http.StatusGone, "share_gone")
		return
	}
	var body struct {
		ViewID string `json:"view_id"`
		Cursor string `json:"cursor"`
		Limit  int32  `json:"limit"`
	}
	if decodeJSON(r, &body) != nil || body.ViewID == "" || body.Limit < 1 || body.Limit > 200 || len(body.Cursor) > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	config := defaultDonorGridConfig()
	if body.ViewID != "default" {
		id, err := parseID(body.ViewID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		views, err := h.workspace.ListViews(r.Context(), share.ProductID)
		if err != nil {
			resultError(w, err)
			return
		}
		found := false
		for _, view := range views {
			if view.ID == productport.ID(id) {
				config, err = decodeDonorGridConfig(view.Config)
				if err != nil {
					writeError(w, http.StatusServiceUnavailable, "unavailable")
					return
				}
				found = true
				break
			}
		}
		if !found {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
	}
	var metrics memberGridMetrics
	rows, next, err := h.queryDonorGridWithMetrics(r.Context(), int64(share.ProductID), config, body.Cursor, body.Limit, &metrics)
	if err != nil {
		if !productMemberGridQueryError(w, err) {
			resultError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "limit": body.Limit, "next_cursor": next, "has_more": next != ""})
}
