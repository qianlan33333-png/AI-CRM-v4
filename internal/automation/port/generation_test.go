package port

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCustomerNamePromptAndLegacyContext(t *testing.T) {
	var legacy GenerationContext
	if err := json.Unmarshal([]byte(`{"questionnaire":"旧问卷"}`), &legacy); err != nil || !legacy.Valid() || legacy.CustomerName != "" {
		t.Fatal("legacy frozen context no longer readable")
	}
	if value := RenderCustomerNamePrompt("{{用户名}}，{{客户名}}", "名字{{用户名}}"); value != "名字{{用户名}}，名字{{用户名}}" {
		t.Fatal("recursive name expansion", value)
	}
	if value := RenderCustomerNamePrompt("你好{{用户名}}", " \t"); value != "你好朋友" {
		t.Fatal("missing-name fallback", value)
	}
	if ValidGeneratedText("【用户名】\n小明", "角色", "任务") {
		t.Fatal("accepted name context echo")
	}
}

func TestPublishedGenerationPromptContract(t *testing.T) {
	p := PublishedGeneration{AgentID: 1, PublishedVersion: 2, AgentCode: "long_prompt", RolePrompt: strings.Repeat("😀", 24000), TaskPrompt: strings.Repeat("任务", 24000)}
	if !p.Valid() {
		t.Fatal("complete published prompt rejected by size")
	}
	p.RolePrompt = ""
	if p.Valid() {
		t.Fatal("empty role prompt accepted")
	}
	p.RolePrompt = "role"
	p.TaskPrompt = ""
	if p.Valid() {
		t.Fatal("empty task prompt accepted")
	}
	p.TaskPrompt = "task"
	p.PublishedVersion = 0
	if p.Valid() {
		t.Fatal("unpublished version accepted")
	}
}
