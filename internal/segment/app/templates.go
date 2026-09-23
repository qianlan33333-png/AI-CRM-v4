package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

var ErrUnsupportedDefinition = errors.New("audience definition capability is unavailable")

type Template struct {
	Key                string          `json:"key"`
	Available          bool            `json:"available"`
	UnavailableReason  string          `json:"unavailable_reason,omitempty"`
	TemplateVersion    int             `json:"template_version"`
	Label              string          `json:"label"`
	Description        string          `json:"description"`
	DefaultRefreshMode string          `json:"default_refresh_mode"`
	Fields             []TemplateField `json:"fields"`
}

// TemplateField is the frozen dd8 template_registry UI contract. Values are
// converted by the v3 Host to the closed local AST; it is never a SQL schema.
type TemplateField struct {
	Name        string            `json:"name"`
	Label       string            `json:"label"`
	Type        string            `json:"type"`
	Required    bool              `json:"required"`
	Enum        []string          `json:"enum,omitempty"`
	EnumLabels  map[string]string `json:"enum_labels,omitempty"`
	Default     any               `json:"default,omitempty"`
	Reference   string            `json:"reference,omitempty"`
	Minimum     *int              `json:"minimum,omitempty"`
	MinItems    *int              `json:"min_items,omitempty"`
	VisibleWhen map[string]string `json:"visible_when,omitempty"`
}

type DefinitionInput struct {
	SchemaVersion int                        `json:"schema_version"`
	TemplateKey   string                     `json:"template_key"`
	Parameters    map[string]json.RawMessage `json:"parameters"`
}

var legacyTemplates = []string{
	"hxc_registration", "wecom_contact_registration", "questionnaire_choice_answers", "questionnaire_submissions", "paid_order",
	"channel_entry", "radar_first_click_elapsed", "member_usage_status", "member_excluding_group_paid",
}

// Templates is deliberately a closed catalog. Composition reports source
// availability independently; a visible template is never treated as a SQL
// escape hatch.
func Templates() []Template {
	return templateCatalog()
}

func templateCatalog() []Template {
	minimumOne := 1
	owner := func() []TemplateField {
		return []TemplateField{
			{Name: "owner_scope", Label: "负责人范围", Type: "enum", Required: true, Enum: []string{"specified", "all"}, EnumLabels: map[string]string{"specified": "指定负责人", "all": "全部负责人"}},
			{Name: "owner_userids", Label: "负责人 UserID", Type: "string_list", Default: []string{}, VisibleWhen: map[string]string{"owner_scope": "specified"}},
		}
	}
	withOwner := func(fields []TemplateField) []TemplateField { return append(fields, owner()...) }
	return []Template{
		{Key: "member_excluding_group_paid", Available: true, TemplateVersion: 1, Label: "有效会员排除群与已购商品", Description: "有效会员，排除指定群成员与指定已购商品；群事实缺失或过期时停止刷新。", DefaultRefreshMode: "daily_0200", Fields: append(owner(), TemplateField{Name: "exclude_group_chat", Label: "排除的企微群引用", Type: "string", Required: true}, TemplateField{Name: "excluded_product_codes", Label: "排除的已购商品编码", Type: "string_list", Required: true, MinItems: &minimumOne})},
		{Key: "hxc_registration", Available: true, TemplateVersion: 1, Label: "黄小璨注册状态", Description: "有效企微联系人，按完整黄小璨来源的注册覆盖证据筛选；未知不纳入。", DefaultRefreshMode: "every_3m", Fields: append(owner(), TemplateField{Name: "registration_status", Label: "黄小璨注册状态", Type: "enum", Required: true, Enum: []string{"registered", "unregistered"}, Default: "unregistered"})},
		{Key: "wecom_contact_registration", Available: true, TemplateVersion: 1, Label: "企微联系人与注册状态", Description: "按负责人、企微联系人状态和注册状态圈选。", DefaultRefreshMode: "every_3m", Fields: append(owner(), TemplateField{Name: "contact_statuses", Label: "联系人状态", Type: "enum_list", Required: true, Enum: []string{"active", "deleted"}, Default: []string{"active"}}, TemplateField{Name: "registration_status", Label: "注册状态", Type: "enum", Required: true, Enum: []string{"any", "registered", "unregistered"}, Default: "any"})},
		{Key: "questionnaire_submissions", Available: true, TemplateVersion: 1, Label: "问卷提交", Description: "提交任一所选问卷且已归属客户；不限题型、答案或提交次数。", DefaultRefreshMode: "every_3m", Fields: append(withOwner([]TemplateField{{Name: "questionnaires", Label: "问卷", Type: "reference_list", Required: true, Reference: "questionnaire", MinItems: &minimumOne}}), TemplateField{Name: "require_wecom_identity", Label: "要求已识别企微身份（不限制联系人状态）", Type: "boolean", Required: true, Default: true})},
		{Key: "questionnaire_choice_answers", Available: true, TemplateVersion: 1, Label: "问卷选择题答案", Description: "按首次完整提交的选择题答案圈选；题间 AND、题内选项 OR。", DefaultRefreshMode: "every_3m", Fields: withOwner([]TemplateField{{Name: "questionnaire", Label: "问卷", Type: "reference", Required: true, Reference: "questionnaire"}, {Name: "conditions", Label: "题目条件", Type: "condition_list", Required: true, MinItems: &minimumOne}})},
		{Key: "paid_order", Available: true, TemplateVersion: 1, Label: "已支付订单", Description: "按商品、支付时间、负责人和有效企微联系人圈选。", DefaultRefreshMode: "every_3m", Fields: append(withOwner([]TemplateField{{Name: "products", Label: "商品", Type: "reference_list", Required: true, Reference: "product", MinItems: &minimumOne}, {Name: "paid_at_from", Label: "支付时间起点", Type: "datetime"}, {Name: "paid_at_to", Label: "支付时间终点（不含）", Type: "datetime"}}), TemplateField{Name: "require_active_wecom_contact", Label: "要求有效企微联系人", Type: "boolean", Required: true, Default: true})},
		{Key: "channel_entry", Available: true, TemplateVersion: 1, Label: "渠道进入", Description: "按渠道和距进入时间窗口圈选。", DefaultRefreshMode: "every_3m", Fields: append(withOwner([]TemplateField{{Name: "channels", Label: "渠道", Type: "reference_list", Required: true, Reference: "channel", MinItems: &minimumOne}, {Name: "entered_days_min", Label: "距进入最少天数", Type: "integer", Required: true, Default: 0, Minimum: intPointer(0)}, {Name: "entered_days_max", Label: "距进入最大天数（不含）", Type: "integer", Minimum: intPointer(1)}}), TemplateField{Name: "require_active_wecom_contact", Label: "要求有效企微联系人", Type: "boolean", Required: true, Default: true})},
		{Key: "radar_first_click_elapsed", Available: true, TemplateVersion: 1, Label: "雷达首次点击距今", Description: "多个雷达 OR，以首次可归因点击为时间锚点。", DefaultRefreshMode: "every_3m", Fields: withOwner([]TemplateField{{Name: "radars", Label: "雷达", Type: "reference_list", Required: true, Reference: "radar", MinItems: &minimumOne}, {Name: "elapsed_min", Label: "最小经过时间", Type: "integer", Required: true, Default: 0, Minimum: intPointer(0)}, {Name: "elapsed_max", Label: "最大经过时间（不含）", Type: "integer", Minimum: intPointer(1)}, {Name: "elapsed_unit", Label: "时间单位", Type: "enum", Required: true, Enum: []string{"hour", "day"}, Default: "day"}})},
		{Key: "member_usage_status", Available: true, TemplateVersion: 1, Label: "会员与真实使用状态", Description: "按负责人、服务期、注册、真实使用和会员层级圈选。", DefaultRefreshMode: "daily_0200", Fields: append(owner(), []TemplateField{{Name: "service_period", Label: "服务期", Type: "enum", Required: true, Enum: []string{"any", "active", "expired"}, Default: "active"}, {Name: "registration_status", Label: "注册状态", Type: "enum", Required: true, Enum: []string{"any", "registered", "unregistered"}, Default: "any"}, {Name: "usage_status", Label: "真实使用状态", Type: "enum", Required: true, Enum: []string{"any", "used", "unused"}, Default: "any"}, {Name: "membership_tiers", Label: "会员层级", Type: "string_list", Default: []string{}}, {Name: "membership_statuses", Label: "会员状态", Type: "string_list", Default: []string{}}}...)},
	}
}

func intPointer(value int) *int { return &value }

func DefaultDefinition(templateKey string) (json.RawMessage, error) {
	var parameters map[string]any
	switch templateKey {
	case "member_excluding_group_paid":
		parameters = map[string]any{"owner_scope": "all", "owner_staff_ids": []string{}, "exclude_group_chat": "__configure__", "excluded_product_codes": []string{"__configure__"}}
	case "active_contacts":
		parameters = map[string]any{"within_days": "30"}
	case "stage_any":
		parameters = map[string]any{"stages": []string{"__configure__"}}
	case "tag_any":
		parameters = map[string]any{"tag_codes": []string{"__configure__"}}
	case "owner_any":
		parameters = map[string]any{"staff_ids": []string{"__configure__"}}
	case "channel_any":
		parameters = map[string]any{"channels": []string{"__configure__"}}
	case "hxc_registration":
		parameters = map[string]any{"owner_scope": "all", "owner_staff_ids": []string{}, "registration_status": "unregistered"}
	case "wecom_contact_registration":
		parameters = map[string]any{"owner_scope": "all", "owner_staff_ids": []string{}, "contact_statuses": []string{"active"}, "registration_status": "any"}
	case "questionnaire_submissions":
		parameters = map[string]any{"questionnaire_ids": []string{"__configure__"}, "owner_scope": "all", "owner_staff_ids": []string{}, "require_wecom_identity": true}
	case "questionnaire_choice_answers":
		parameters = map[string]any{"questionnaire_id": "__configure__", "conditions": []any{map[string]any{"question_id": "__configure__", "option_ids": []string{"__configure__"}}}, "owner_scope": "all", "owner_staff_ids": []string{}}
	case "paid_order":
		parameters = map[string]any{"product_codes": []string{"__configure__"}, "paid_at_from": "", "paid_at_to": "", "owner_scope": "all", "owner_staff_ids": []string{}, "require_active_wecom_contact": true}
	case "channel_entry":
		parameters = map[string]any{"channel_codes": []string{"__configure__"}, "entered_days_min": 0, "entered_days_max": nil, "owner_scope": "all", "owner_staff_ids": []string{}, "require_active_wecom_contact": true}
	case "radar_first_click_elapsed":
		parameters = map[string]any{"radar_ids": []string{"__configure__"}, "elapsed_min": 0, "elapsed_max": nil, "elapsed_unit": "day", "owner_scope": "all", "owner_staff_ids": []string{}}
	case "member_usage_status":
		parameters = map[string]any{"owner_scope": "all", "owner_staff_ids": []string{}, "service_period": "active", "registration_status": "any", "usage_status": "any", "membership_tiers": []string{}, "membership_statuses": []string{}}
	default:
		return nil, ErrUnsupportedDefinition
	}
	raw, _ := json.Marshal(DefinitionInput{SchemaVersion: 1, TemplateKey: templateKey, Parameters: rawParameters(parameters)})
	return CanonicalDefinition(raw)
}

func rawParameters(values map[string]any) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		out[key], _ = json.Marshal(value)
	}
	return out
}

func CanonicalDefinition(raw json.RawMessage) (json.RawMessage, error) {
	var input DefinitionInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		return nil, ErrUnsupportedDefinition
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || input.SchemaVersion != 1 || !validDefinition(input) {
		return nil, ErrUnsupportedDefinition
	}
	canonical, err := json.Marshal(input)
	if err != nil || len(canonical) > 16*1024 {
		return nil, ErrUnsupportedDefinition
	}
	return canonical, nil
}

func validDefinition(input DefinitionInput) bool {
	owner := validOwner(input.Parameters)
	switch input.TemplateKey {
	case "core_ai_product":
		var id int64
		return len(input.Parameters) == 1 && json.Unmarshal(input.Parameters["core_product_id"], &id) == nil && id >= 1 && id <= 5
	case "member_excluding_group_paid":
		group, ok := stringOf(input.Parameters, "exclude_group_chat")
		products, pok := stringsOf(input.Parameters, "excluded_product_codes", 1, 100)
		return owner && ok && validStrings([]string{group}, 1, 1) && pok && validStrings(products, 1, 100) && keys(input.Parameters, "owner_scope", "owner_staff_ids", "exclude_group_chat", "excluded_product_codes")
	case "active_contacts":
		// Read-only compatibility for already-persisted definitions. It is not
		// exposed in the PRD05 catalog because directory updates are not usage.
		value, ok := stringOf(input.Parameters, "within_days")
		return ok && value != "" && keys(input.Parameters, "within_days") && positiveInt(value)
	case "stage_any":
		value, ok := stringsOf(input.Parameters, "stages", 1, 100)
		return ok && validStrings(value, 1, 100) && keys(input.Parameters, "stages")
	case "tag_any":
		value, ok := stringsOf(input.Parameters, "tag_codes", 1, 100)
		return ok && validStrings(value, 1, 100) && keys(input.Parameters, "tag_codes")
	case "owner_any":
		value, ok := stringsOf(input.Parameters, "staff_ids", 1, 100)
		return ok && validStrings(value, 1, 100) && keys(input.Parameters, "staff_ids")
	case "channel_any":
		value, ok := stringsOf(input.Parameters, "channels", 1, 100)
		return ok && validStrings(value, 1, 100) && keys(input.Parameters, "channels")
	case "hxc_registration":
		return owner && enum(input.Parameters, "registration_status", "registered", "unregistered") && keys(input.Parameters, "owner_scope", "owner_staff_ids", "registration_status")
	case "wecom_contact_registration":
		statuses, ok := stringsOf(input.Parameters, "contact_statuses", 1, 2)
		return owner && ok && allowed(statuses, "active", "deleted") && enum(input.Parameters, "registration_status", "any", "registered", "unregistered") && keys(input.Parameters, "owner_scope", "owner_staff_ids", "contact_statuses", "registration_status")
	case "questionnaire_submissions":
		ids, ok := stringsOf(input.Parameters, "questionnaire_ids", 1, 100)
		_, boolOK := boolOf(input.Parameters, "require_wecom_identity")
		return owner && ok && validStrings(ids, 1, 100) && boolOK && keys(input.Parameters, "questionnaire_ids", "owner_scope", "owner_staff_ids", "require_wecom_identity")
	case "questionnaire_choice_answers":
		questionnaire, ok := stringOf(input.Parameters, "questionnaire_id")
		if !owner || !ok || questionnaire == "" || !keys(input.Parameters, "questionnaire_id", "conditions", "owner_scope", "owner_staff_ids") {
			return false
		}
		var conditions []struct {
			QuestionID string   `json:"question_id"`
			OptionIDs  []string `json:"option_ids"`
		}
		if json.Unmarshal(input.Parameters["conditions"], &conditions) != nil || len(conditions) < 1 || len(conditions) > 50 {
			return false
		}
		seen := map[string]bool{}
		for _, condition := range conditions {
			if condition.QuestionID == "" || seen[condition.QuestionID] || !validStrings(condition.OptionIDs, 1, 100) {
				return false
			}
			seen[condition.QuestionID] = true
		}
		return true
	case "paid_order":
		products, ok := stringsOf(input.Parameters, "product_codes", 1, 100)
		from, fromOK := stringOf(input.Parameters, "paid_at_from")
		to, toOK := stringOf(input.Parameters, "paid_at_to")
		_, contactOK := boolOf(input.Parameters, "require_active_wecom_contact")
		return owner && ok && validStrings(products, 1, 100) && fromOK && toOK && validWindow(from, to) && contactOK && keys(input.Parameters, "product_codes", "paid_at_from", "paid_at_to", "owner_scope", "owner_staff_ids", "require_active_wecom_contact")
	case "channel_entry":
		channels, ok := stringsOf(input.Parameters, "channel_codes", 1, 100)
		minimum, minOK := nonNegative(input.Parameters, "entered_days_min")
		maximum, maxOK := optionalNonNegative(input.Parameters, "entered_days_max")
		_, contactOK := boolOf(input.Parameters, "require_active_wecom_contact")
		return owner && ok && validStrings(channels, 1, 100) && minOK && maxOK && (maximum == nil || *maximum > minimum) && contactOK && keys(input.Parameters, "channel_codes", "entered_days_min", "entered_days_max", "owner_scope", "owner_staff_ids", "require_active_wecom_contact")
	case "radar_first_click_elapsed":
		radars, ok := stringsOf(input.Parameters, "radar_ids", 1, 100)
		minimum, minOK := nonNegative(input.Parameters, "elapsed_min")
		maximum, maxOK := optionalNonNegative(input.Parameters, "elapsed_max")
		return owner && ok && validStrings(radars, 1, 100) && minOK && maxOK && (maximum == nil || *maximum > minimum) && enum(input.Parameters, "elapsed_unit", "hour", "day") && keys(input.Parameters, "radar_ids", "elapsed_min", "elapsed_max", "elapsed_unit", "owner_scope", "owner_staff_ids")
	case "member_usage_status":
		tiers, tiersOK := stringsOf(input.Parameters, "membership_tiers", 0, 100)
		statuses, statusesOK := stringsOf(input.Parameters, "membership_statuses", 0, 100)
		return owner && tiersOK && statusesOK && validStrings(tiers, 0, 100) && validStrings(statuses, 0, 100) && enum(input.Parameters, "service_period", "any", "active", "expired") && enum(input.Parameters, "registration_status", "any", "registered", "unregistered") && enum(input.Parameters, "usage_status", "any", "used", "unused") && keys(input.Parameters, "owner_scope", "owner_staff_ids", "service_period", "registration_status", "usage_status", "membership_tiers", "membership_statuses")
	default:
		return false
	}
}

func keys(values map[string]json.RawMessage, expected ...string) bool {
	if len(values) != len(expected) {
		return false
	}
	for _, key := range expected {
		if _, ok := values[key]; !ok {
			return false
		}
	}
	return true
}
func stringOf(values map[string]json.RawMessage, key string) (string, bool) {
	var value string
	return value, json.Unmarshal(values[key], &value) == nil && strings.TrimSpace(value) == value && len(value) <= 200
}
func stringsOf(values map[string]json.RawMessage, key string, min, max int) ([]string, bool) {
	var value []string
	return value, json.Unmarshal(values[key], &value) == nil && len(value) >= min && len(value) <= max
}
func validStrings(values []string, min, max int) bool {
	if len(values) < min || len(values) > max {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value || len(value) > 200 || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}
func allowed(values []string, options ...string) bool {
	for _, value := range values {
		found := false
		for _, option := range options {
			if value == option {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return validStrings(values, 1, len(options))
}
func enum(values map[string]json.RawMessage, key string, options ...string) bool {
	value, ok := stringOf(values, key)
	if !ok {
		return false
	}
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
func boolOf(values map[string]json.RawMessage, key string) (bool, bool) {
	var value bool
	return value, json.Unmarshal(values[key], &value) == nil
}
func nonNegative(values map[string]json.RawMessage, key string) (int, bool) {
	var value int
	return value, json.Unmarshal(values[key], &value) == nil && value >= 0 && value <= 36500
}
func optionalNonNegative(values map[string]json.RawMessage, key string) (*int, bool) {
	if bytes.Equal(values[key], []byte("null")) {
		return nil, true
	}
	value, ok := nonNegative(values, key)
	return &value, ok
}
func validWindow(from, to string) bool {
	if from != "" {
		if _, err := time.Parse(time.RFC3339, from); err != nil {
			return false
		}
	}
	if to != "" {
		if _, err := time.Parse(time.RFC3339, to); err != nil {
			return false
		}
	}
	return from == "" || to == "" || from < to
}
func validOwner(values map[string]json.RawMessage) bool {
	scope, ok := stringOf(values, "owner_scope")
	ids, idsOK := stringsOf(values, "owner_staff_ids", 0, 100)
	return ok && idsOK && validStaffIDs(ids) && ((scope == "all") || (scope == "specified" && len(ids) > 0))
}

// validStaffIDs keeps a persisted definition in the local Access namespace.
// Provider userids, including numeric userids, are never valid here.
func validStaffIDs(ids []string) bool {
	if !validStrings(ids, 0, 100) {
		return false
	}
	for _, value := range ids {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id < 1 || strconv.FormatInt(id, 10) != value {
			return false
		}
	}
	return true
}
func positiveInt(value string) bool {
	if len(value) > 3 {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return value != "" && value != "0"
}
