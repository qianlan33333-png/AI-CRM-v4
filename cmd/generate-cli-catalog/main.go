// Regenerates a reviewed, compiled route inventory. Runtime callers cannot add URLs.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	cliport "github.com/qianlan33333-png/AI-CRM-v3/internal/clicatalog/port"
	openport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	"gopkg.in/yaml.v3"
)

func object(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
func array(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}
func str(v any) string { s, _ := v.(string); return s }

// A route prefix is not authentication evidence. Accept a human binding only
// when a declared alternative can be satisfied by the actual session/CSRF
// protocol; independent integration signatures must retain their own identity.
func supportsHumanSession(operation, spec map[string]any) bool {
	security, exists := operation["security"]
	if !exists {
		security = spec["security"]
	}
	for _, raw := range array(security) {
		requirements := object(raw)
		if _, ok := requirements["adminSession"]; !ok {
			continue
		}
		compatible := true
		for name := range requirements {
			if name != "adminSession" && name != "csrfHeader" {
				compatible = false
			}
		}
		if compatible {
			return true
		}
	}
	return false
}

var groups = []struct{ Prefix, Group, Owner string }{
	{"/api/admin/customers/owner-handoffs", "owners", "customer"},
	{"/api/admin/automation-conversion/group-ops", "group-ops", "groupops"},
	{"/api/admin/ai-assistant", "ai", "aiplan"}, {"/api/admin/ai-assist", "ai", "aiplan"},
	{"/api/admin/attachment-library", "materials", "media"}, {"/api/admin/image-library", "materials", "media"}, {"/api/admin/miniprogram-library", "materials", "media"},
	{"/api/admin/message-archive", "wecom", "messagearchive"}, {"/api/admin/survey-history", "questionnaires", "survey"},
	{"/api/admin/exports", "orders", "order"}, {"/api/admin/order-imports", "orders", "order"},
	{"/api/admin/wechat-pay/order-exports", "orders", "order"},
	{"/api/admin/wechat-pay/payments", "payments", "payment"}, {"/api/admin/wechat-pay/refunds", "refunds", "refund"},
	{"/api/admin/wechat-shop/refunds", "refunds", "refund"}, {"/api/admin/alipay", "payments", "payment"},
	{"/api/admin/wechat-pay/orders", "orders", "order"}, {"/api/admin/common/operation-members", "group-ops", "groupops"},
	{"/api/admin/setup-wizard", "config", "config"},
	{"/api/admin/ops-", "ops", "operations"}, {"/api/admin/open-platform/", "access", "access"},
	{"/api/admin/access/", "access", "access"}, {"/api/admin/admin-access", "access", "access"},
	{"/api/admin/overview", "overview", "customer"}, {"/api/admin/hxc-dashboard", "dashboard", "hxc"},
	{"/api/admin/customer", "customers", "customer"}, {"/api/admin/oneid", "identity", "identity"},
	{"/api/admin/wecom", "wecom", "wecom"}, {"/api/v1/wecom", "wecom", "wecom"},
	{"/api/admin/ai-audience", "audience", "audience"}, {"/api/admin/ai-plans", "ai", "aiplan"},
	{"/api/admin/cloud-orchestrator", "ai", "aiplan"}, {"/api/admin/automation-agents", "automation", "automation"},
	{"/api/admin/automation", "automation", "automation"}, {"/api/admin/operation", "cycles", "operationcycle"},
	{"/api/admin/excel-operation", "cycles", "operationcycle"}, {"/api/admin/group-ops", "group-ops", "groupops"},
	{"/api/admin/group-invit", "group-invitations", "groupinvitation"}, {"/api/admin/channels", "channels", "channel"},
	{"/api/admin/channel-", "channels", "channel"}, {"/api/admin/orders", "orders", "order"},
	{"/api/admin/payments", "payments", "payment"}, {"/api/admin/refunds", "refunds", "refund"},
	{"/api/admin/wechat-pay/products", "products", "product"}, {"/api/admin/products", "products", "product"},
	{"/api/admin/service-period", "period-products", "serviceperiod"}, {"/api/admin/coupons", "coupons", "coupon"},
	{"/api/admin/distribution", "distribution", "distribution"}, {"/api/admin/referral", "referral", "referral"},
	{"/api/admin/questionnaire", "questionnaires", "survey"}, {"/api/admin/radar", "radar", "radar"},
	{"/api/admin/media", "materials", "media"}, {"/api/admin/material", "materials", "media"},
	{"/api/admin/group-invite-material", "materials", "media"}, {"/api/admin/owner", "owners", "ownerhandoff"},
	{"/api/admin/config", "config", "config"}, {"/api/admin/model", "config", "config"},
	{"/api/admin/push-center", "ops", "pushcenter"}, {"/api/admin/external-effect", "ops", "externaleffects"},
}

func group(path string) (string, string) {
	for _, item := range groups {
		if strings.HasPrefix(path, item.Prefix) {
			return item.Group, item.Owner
		}
	}
	return "api", "route-owner"
}

var word = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func command(id string) string {
	return strings.ReplaceAll(strings.ToLower(word.ReplaceAllString(id, "${1}-${2}")), "_", "-")
}

func nestedCommand(value string) string {
	parts := strings.SplitN(value, "-", 2)
	if len(parts) != 2 {
		return value + " invoke"
	}
	verbs := map[string]bool{"get": true, "list": true, "create": true, "update": true, "delete": true, "patch": true, "preview": true, "confirm": true, "refresh": true, "enable": true, "disable": true, "activate": true, "pause": true, "resume": true, "archive": true, "copy": true, "export": true, "import": true, "upload": true, "download": true, "publish": true, "stop": true, "cancel": true, "reconcile": true, "rotate": true, "reset": true, "provision": true, "record": true, "resolve": true, "request": true, "validate": true, "rollback": true, "save": true, "add": true, "remove": true, "bind": true, "unbind": true, "set": true, "grant": true, "revoke": true}
	if !verbs[parts[0]] {
		return value + " invoke"
	}
	resource := strings.TrimPrefix(parts[1], "admin-")
	return resource + " " + parts[0]
}

func main() {
	data, err := os.ReadFile("api/openapi.yaml")
	if err != nil {
		panic(err)
	}
	var spec map[string]any
	if err = yaml.Unmarshal(data, &spec); err != nil {
		panic(err)
	}
	plan, err := os.ReadFile("docs/cli/modules.json")
	if err != nil {
		panic(err)
	}
	c := cliport.Catalog{Version: 2, Schemas: object(object(spec["components"])["schemas"])}
	cliData, err := os.ReadFile("api/cli-v2.yaml")
	if err != nil {
		panic(err)
	}
	if err = yaml.Unmarshal(cliData, &c.CLIContract); err != nil {
		panic(err)
	}
	// The historical documentation schema has a narrower enum than the
	// current frozen native operation vocabulary. Describe only those current
	// grants and already-declared extensions; this does not grant anything.
	capabilities := map[string]bool{}
	for _, descriptor := range openport.OperationCatalog() {
		capabilities[string(descriptor.Capability)] = true
	}
	for _, name := range []string{"OpenPlatformMachineClientCreateRequest", "OpenPlatformMachineClientPatchRequest", "OpenPlatformMachineClientSummary"} {
		property := object(object(object(c.Schemas[name])["properties"])["capabilities"])
		values := map[string]bool{}
		for _, value := range array(object(property["items"])["enum"]) {
			values[str(value)] = true
		}
		for _, value := range array(property["x-aicrm-additional-capabilities"]) {
			values[str(value)] = true
		}
		for value := range capabilities {
			values[value] = true
		}
		names := make([]string, 0, len(values))
		for value := range values {
			names = append(names, value)
		}
		sort.Strings(names)
		enum := make([]any, 0, len(names))
		for _, value := range names {
			enum = append(enum, value)
		}
		object(property["items"])["enum"] = enum
	}
	if err = json.Unmarshal(plan, &c.Modules); err != nil {
		panic(err)
	}
	seen := map[string]bool{}
	for path, value := range object(spec["paths"]) {
		if strings.HasPrefix(path, "/api/admin/access/cli/") || strings.HasPrefix(path, "/api/admin/cli/") {
			continue // First-class CLI authentication and discovery commands own these paths.
		}
		if !strings.HasPrefix(path, "/api/admin/") && !strings.HasPrefix(path, "/api/v1/wecom") {
			continue
		}
		for method, raw := range object(value) {
			if method != "get" && method != "post" && method != "put" && method != "patch" && method != "delete" {
				continue
			}
			v := object(raw)
			id := str(v["operationId"])
			if id == "" {
				continue
			}
			g, owner := group(path)
			binding := &cliport.Binding{Method: strings.ToUpper(method), Path: path, Auth: "human"}
			binding.BodyRequired = object(v["requestBody"])["required"] == true
			binding.CursorOnly = path == "/api/admin/overview/paid-records"
			for _, p := range append(array(object(value)["parameters"]), array(v["parameters"])...) {
				parameter := object(p)
				if ref := str(parameter["$ref"]); strings.HasPrefix(ref, "#/components/parameters/") {
					parameter = object(object(object(spec["components"])["parameters"])[strings.TrimPrefix(ref, "#/components/parameters/")])
				}
				binding.Parameters = append(binding.Parameters, parameter)
				if str(parameter["name"]) == "Idempotency-Key" && parameter["required"] == true {
					binding.IdempotencyRequired = true
				}
			}
			content := object(object(v["requestBody"])["content"])
			for _, typ := range []string{"application/json", "multipart/form-data", "application/octet-stream"} {
				if body, ok := content[typ]; ok {
					binding.ContentType = typ
					binding.Body = object(object(body)["schema"])
					break
				}
			}
			execution := "read"
			if method != "get" {
				execution = "owner_contract"
			}
			op := cliport.Operation{ID: "admin." + id, Group: g, Command: command(id), Summary: str(v["summary"]), Owner: owner, Execution: execution, Implementation: "admin_contract", Evidence: "source_contract_only", Prerequisites: []string{"current human session", "owner route authorization and business conditions"}, Binding: binding}
			op.Version = 1
			op.Responses = object(v["responses"])
			if !supportsHumanSession(v, spec) {
				op.Binding = nil
				op.BlockedReason = "route requires an independent authentication protocol; human sessions cannot execute it"
				op.Prerequisites = []string{"owner-specific integration authentication; not supported by this CLI"}
				if id == "createLegacyAIAssistantReviewPlan" {
					op.BlockedReason += "; use the independently authorized machine V1 ai.review_plan.create operation"
				}
			}
			if seen[op.ID] {
				panic("duplicate operation: " + op.ID)
			}
			seen[op.ID] = true
			c.Operations = append(c.Operations, op)
		}
	}
	for _, d := range openport.OperationCatalog() {
		g := "customers"
		path := d.RESTPath
		for _, p := range []struct{ Part, Group string }{{"/identities", "identity"}, {"/orders", "orders"}, {"/questionnaire", "questionnaires"}, {"/radar", "radar"}, {"/chat-records", "wecom"}, {"/audience", "audience"}, {"/ai/", "ai"}, {"/operations", "operations"}, {"/capabilities", "capabilities"}} {
			if strings.Contains(path, p.Part) {
				g = p.Group
				break
			}
		}
		binding := &cliport.Binding{Method: d.RESTMethod, Path: path, Auth: "machine", Capability: string(d.Capability), Scope: d.RequiredScope}
		if v := object(object(object(spec["paths"])[path])[strings.ToLower(d.RESTMethod)]); len(v) > 0 {
			for _, p := range array(v["parameters"]) {
				binding.Parameters = append(binding.Parameters, object(p))
			}
			binding.Body = object(object(object(object(v["requestBody"])["content"])["application/json"])["schema"])
		}
		if d.RESTMethod == "POST" {
			binding.BodyRequired = true
			binding.ContentType = "application/json"
			binding.IdempotencyRequired = d.RequiredScope == "write"
		}
		c.Operations = append(c.Operations, cliport.Operation{ID: string(d.OperationID), Group: g, Command: command(d.MCPTool), Summary: d.MCPTool, Owner: g, Version: 1, Responses: object(object(object(object(spec["paths"])[path])[strings.ToLower(d.RESTMethod)])["responses"]), Execution: map[bool]string{true: "owner_contract", false: "read"}[d.RequiredScope == "write"], Implementation: "machine_v1", Evidence: "source_contract_only", Prerequisites: []string{"current client grant", "composed Owner Port", "server resource and field authorization"}, Binding: binding})
	}
	var gaps []cliport.Operation
	var extra []cliport.Operation
	if b, e := os.ReadFile("docs/cli/owner-routes.json"); e == nil {
		if e = json.Unmarshal(b, &extra); e != nil {
			panic(e)
		}
	}
	for _, op := range extra {
		op.Version = 1
		if !seen[op.ID] {
			c.Operations = append(c.Operations, op)
			seen[op.ID] = true
		}
	}
	if b, e := os.ReadFile("docs/cli/gaps.json"); e == nil {
		if e = json.Unmarshal(b, &gaps); e != nil {
			panic(e)
		}
	}
	c.Operations = append(c.Operations, gaps...)
	sort.Slice(c.Operations, func(i, j int) bool { return c.Operations[i].ID < c.Operations[j].ID })
	commands := map[string]bool{}
	for i := range c.Operations {
		op := &c.Operations[i]
		op.Module = op.Group
		if parent, ok := map[string]string{"identity": "customers", "payments": "orders", "refunds": "orders", "access": "config", "operations": "ops", "capabilities": "schema"}[op.Group]; ok {
			op.Module = parent
		}
		op.Command = nestedCommand(op.Command)
		key := op.Group + " " + op.Command
		if commands[key] {
			op.Command += "-" + strings.ReplaceAll(op.ID, ".", "-")
		}
		commands[op.Group+" "+op.Command] = true
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile("internal/clicatalog/port/catalog.json", append(b, '\n'), 0644); err != nil {
		panic(err)
	}
	var index strings.Builder
	index.WriteString("# CLI 能力索引\n\n由当前编译目录生成；绑定表示源码接口存在，权限、安装、Provider 就绪和验收分别核验。候选无可执行路径。\n\n")
	for _, module := range c.Modules {
		fmt.Fprintf(&index, "## %s / %s\n\n小板块：%s。\n\n| 操作 ID | CLI 命令 | 身份 / 执行状态 | Owner | 源码证据 |\n|---|---|---|---|---|\n", module.Category, module.Name, strings.Join(module.Sections, "、"))
		for _, op := range c.Operations {
			if op.Module != module.Group {
				continue
			}
			status := "不可执行：" + op.BlockedReason
			if op.Binding != nil {
				status = op.Binding.Auth
			}
			cells := []string{op.ID, "aicrm-cli " + op.Group + " " + op.Command, status, op.Owner, op.Evidence}
			for i, value := range cells {
				cells[i] = strings.NewReplacer("|", "／", "\n", " ").Replace(value)
			}
			fmt.Fprintf(&index, "| %s |\n", strings.Join(cells, " | "))
		}
		if module.Group == "schema" {
			index.WriteString("| 内置 | aicrm-cli schema get OPERATION_ID / schema cli / capabilities list / doctor | 本地目录或认证诊断 | clicatalog | 不授予业务权限 |\n")
		}
		index.WriteString("\n")
	}
	if err = os.WriteFile("docs/cli/capability-index.md", []byte(strings.TrimRight(index.String(), "\n")+"\n"), 0644); err != nil {
		panic(err)
	}
	fmt.Printf("catalog: %d operations, %d sidebar modules\n", len(c.Operations), len(c.Modules))
}
