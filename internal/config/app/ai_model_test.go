package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	"testing"
)

type modelMemory struct {
	row   AIModelRecord
	fail  bool
	saves int
}

func (m *modelMemory) ReadAIModelRecord(context.Context, bool) (AIModelRecord, error) {
	return m.row, nil
}
func (m *modelMemory) SaveAIModelRecord(_ context.Context, r AIModelRecord, expected, actor int64) error {
	if m.fail {
		return ErrAIModelUnavailable
	}
	if expected != m.row.Version {
		return ErrAIModelConflict
	}
	m.row = r
	m.saves++
	return nil
}
func TestAIModelKeyStorageAndProviderSwitch(t *testing.T) {
	ctx := context.Background()
	repo := &modelMemory{}
	master := bytes.Repeat([]byte("x"), 32)
	s, e := NewAIModelService(&fakeUoW{}, repo, master)
	if e != nil {
		t.Fatal(e)
	}
	command := configport.AIModelCommand{Provider: "deepseek", Model: "deepseek-chat", APIKey: "test-private-key", ActorID: 1}
	saved, e := s.SaveAIModel(ctx, command)
	if e != nil || saved.Version != 1 || !saved.Configured {
		t.Fatalf("save=%+v err=%v", saved, e)
	}
	if bytes.Contains(repo.row.Ciphertext, []byte(command.APIKey)) {
		t.Fatal("plaintext key persisted")
	}
	public, _ := json.Marshal(saved)
	if bytes.Contains(public, []byte(command.APIKey)) {
		t.Fatal("key exposed")
	}
	restarted, e := NewAIModelService(&fakeUoW{}, repo, master)
	if e != nil {
		t.Fatal(e)
	}
	runtime, found, e := restarted.ReadAIModelRuntime(ctx)
	if e != nil || !found || runtime.APIKey != command.APIKey || runtime.Model != command.Model {
		t.Fatal("restart must recover configured model")
	}
	wire, _ := json.Marshal(runtime)
	if bytes.Contains(wire, []byte(command.APIKey)) {
		t.Fatal("runtime must not serialize key")
	}
	command.ExpectedVersion = 1
	command.APIKey = ""
	command.Model = "deepseek-reasoner"
	if _, e = s.SaveAIModel(ctx, command); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveAIModel(ctx, command); !errors.Is(e, ErrAIModelConflict) {
		t.Fatalf("stale version: %v", e)
	}
	command.ExpectedVersion = 2
	command.Provider = "qwen"
	if _, e = s.SaveAIModel(ctx, command); !errors.Is(e, ErrAIModelInvalid) {
		t.Fatal("provider switch reused key")
	}
	command.APIKey = "new-private-key"
	command.Model = "qwen-plus"
	repo.fail = true
	if _, e = s.SaveAIModel(ctx, command); e == nil || repo.row.Version != 2 {
		t.Fatal("failed save mutated state")
	}
	repo.fail = false
	if _, e = s.SaveAIModel(ctx, command); e != nil {
		t.Fatal(e)
	}
	repo.row.Provider = "glm"
	if _, _, e = s.ReadAIModelRuntime(ctx); !errors.Is(e, ErrAIModelUnavailable) {
		t.Fatal("ciphertext provider binding not verified")
	}
}
