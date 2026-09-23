package http

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type audienceOpenAPISpec struct {
	Paths      map[string]yaml.Node `yaml:"paths"`
	Components map[string]yaml.Node `yaml:"components"`
}

type audienceOpenAPIOperation struct {
	Security    []map[string][]string `yaml:"security"`
	Description string                `yaml:"description"`
	Parameters  []audienceOpenAPIRef  `yaml:"parameters"`
	RequestBody struct {
		Required bool `yaml:"required"`
		Content  map[string]struct {
			Schema audienceOpenAPIRef `yaml:"schema"`
		} `yaml:"content"`
	} `yaml:"requestBody"`
	Responses map[string]struct {
		Content map[string]struct {
			Schema audienceOpenAPIRef `yaml:"schema"`
		} `yaml:"content"`
	} `yaml:"responses"`
}

type audienceOpenAPIRef struct {
	Ref string `yaml:"$ref"`
}

type audienceOpenAPISecurityScheme struct {
	Type string `yaml:"type"`
	In   string `yaml:"in"`
	Name string `yaml:"name"`
}

type audienceOpenAPIParameter struct {
	Name     string `yaml:"name"`
	In       string `yaml:"in"`
	Required bool   `yaml:"required"`
	Schema   struct {
		Pattern string `yaml:"pattern"`
		Min     int    `yaml:"minLength"`
		Max     int    `yaml:"maxLength"`
	} `yaml:"schema"`
}

type audienceOpenAPISchema struct {
	AdditionalProperties bool                               `yaml:"additionalProperties"`
	Required             []string                           `yaml:"required"`
	Properties           map[string]audienceOpenAPIProperty `yaml:"properties"`
}

type audienceOpenAPIProperty struct {
	Enum []string `yaml:"enum"`
}

func TestAudienceWebhookOpenAPIContractMatchesCanonicalHandler(t *testing.T) {
	document, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec audienceOpenAPISpec
	if err := yaml.Unmarshal(document, &spec); err != nil {
		t.Fatal(err)
	}
	canonicalPath := webhookPathPrefix + "{package_key}" + webhookPathSuffix
	if _, stale := spec.Paths["/api/ai/audience/packages/{package_key}/webhook"]; stale {
		t.Fatal("retired audience webhook path remains in OpenAPI")
	}
	pathNode, ok := spec.Paths[canonicalPath]
	if !ok {
		t.Fatalf("missing canonical POST %s", canonicalPath)
	}
	var operations map[string]audienceOpenAPIOperation
	if err := pathNode.Decode(&operations); err != nil {
		t.Fatal(err)
	}
	operation, ok := operations["post"]
	if !ok {
		t.Fatalf("missing canonical POST %s", canonicalPath)
	}
	if len(operation.Security) != 1 || len(operation.Security[0]) != 1 || operation.Security[0]["automationOpsWebhook"] == nil {
		t.Fatalf("webhook security=%#v", operation.Security)
	}
	var schemes map[string]audienceOpenAPISecurityScheme
	securityNode := spec.Components["securitySchemes"]
	if err := securityNode.Decode(&schemes); err != nil {
		t.Fatal(err)
	}
	scheme := schemes["automationOpsWebhook"]
	if scheme.Type != "apiKey" || scheme.In != "header" || scheme.Name != webhookSignatureHeader {
		t.Fatalf("signature scheme=%#v", scheme)
	}
	var parameterNodes map[string]yaml.Node
	parametersNode := spec.Components["parameters"]
	if err := parametersNode.Decode(&parameterNodes); err != nil {
		t.Fatal(err)
	}
	parameters := map[string]audienceOpenAPIParameter{}
	for _, reference := range operation.Parameters {
		name := strings.TrimPrefix(reference.Ref, "#/components/parameters/")
		parameterNode := parameterNodes[name]
		var parameter audienceOpenAPIParameter
		if err := parameterNode.Decode(&parameter); err != nil {
			t.Fatal(err)
		}
		parameters[name] = parameter
	}
	assertAudienceWebhookParameter(t, parameters["AutomationOpsPackageKey"], "package_key", "path", packageKeyPattern.String(), 0, 0)
	assertAudienceWebhookParameter(t, parameters["AutomationOpsWebhookTimestamp"], webhookTimestampHeader, "header", "^[0-9]{1,20}$", 0, 0)
	assertAudienceWebhookParameter(t, parameters["AutomationOpsWebhookEventID"], webhookEventIDHeader, "header", "", 1, 200)
	if !operation.RequestBody.Required || operation.RequestBody.Content["application/json"].Schema.Ref != "#/components/schemas/AutomationOpsWebhookFact" {
		t.Fatalf("request body=%#v", operation.RequestBody)
	}
	var schemaNodes map[string]yaml.Node
	schemasNode := spec.Components["schemas"]
	if err := schemasNode.Decode(&schemaNodes); err != nil {
		t.Fatal(err)
	}
	var fact audienceOpenAPISchema
	factNode := schemaNodes["AutomationOpsWebhookFact"]
	if err := factNode.Decode(&fact); err != nil {
		t.Fatal(err)
	}
	if fact.AdditionalProperties || !sameStrings(fact.Required, []string{"kind", "scope", "value", "source"}) || !sameStrings(fact.Properties["kind"].Enum, []string{"wecom_external_userid", "mp_openid", "oa_openid", "unionid"}) {
		t.Fatalf("fact schema=%#v", fact)
	}
	if operation.Responses["202"].Content["application/json"].Schema.Ref != "#/components/schemas/AutomationOpsWebhookReceipt" {
		t.Fatalf("202 receipt=%#v", operation.Responses["202"])
	}
	for _, status := range []string{"400", "401", "404", "409", "413", "503"} {
		if _, ok := operation.Responses[status]; !ok {
			t.Fatalf("missing handler-visible response %s", status)
		}
	}
	if !strings.Contains(operation.Description, "package_key") || !strings.Contains(operation.Description, "unmodified request body") {
		t.Fatalf("signing description=%q", operation.Description)
	}
}

func assertAudienceWebhookParameter(t *testing.T, got audienceOpenAPIParameter, wantName, wantIn, wantPattern string, wantMin, wantMax int) {
	t.Helper()
	if got.Name != wantName || got.In != wantIn || !got.Required || got.Schema.Pattern != wantPattern || got.Schema.Min != wantMin || got.Schema.Max != wantMax {
		t.Fatalf("parameter=%#v want name=%s in=%s pattern=%s min=%d max=%d", got, wantName, wantIn, wantPattern, wantMin, wantMax)
	}
}

func sameStrings(got, want []string) bool {
	got, want = append([]string(nil), got...), append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	return strings.Join(got, "\x00") == strings.Join(want, "\x00")
}
