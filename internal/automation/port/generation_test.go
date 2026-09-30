package port

import (
	"strings"
	"testing"
)

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
