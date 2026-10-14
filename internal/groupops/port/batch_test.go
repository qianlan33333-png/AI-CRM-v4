package port

import (
	"encoding/json"
	"testing"
)

func TestFrozenExecutionTargetsVersionedBatchAndLegacy(t *testing.T) {
	targets, err := FrozenExecutionTargets(json.RawMessage(`{"schema_version":3,"kind":"webhook_message","target_chat_references":["chat-a","chat-b"]}`), "chat-a")
	if err != nil || len(targets) != 2 || targets[1] != "chat-b" {
		t.Fatal(targets, err)
	}
	for _, version := range []string{"1", "2"} {
		targets, err = FrozenExecutionTargets(json.RawMessage(`{"schema_version":`+version+`,"kind":"message"}`), "chat-a")
		if err != nil || len(targets) != 1 || targets[0] != "chat-a" {
			t.Fatal(targets, err)
		}
	}
}
func TestFrozenExecutionTargetsRejectsUnversionedOrAmbiguousScope(t *testing.T) {
	for _, snapshot := range []string{
		`{"schema_version":2,"target_chat_references":["chat-a"]}`,
		`{"schema_version":3,"kind":"webhook_message","target_chat_references":[]}`,
		`{"schema_version":3,"kind":"webhook_message","target_chat_references":["chat-a","chat-a"]}`,
		`{"schema_version":3,"kind":"webhook_message","target_chat_references":["chat-b","chat-a"]}`,
		`{"schema_version":3,"kind":"message","target_chat_references":["chat-a"]}`,
		`{"schema_version":3,"kind":"webhook_message","target_chat_references":["chat-a","invalid/group"]}`,
	} {
		if _, err := FrozenExecutionTargets(json.RawMessage(snapshot), "chat-a"); err == nil {
			t.Fatal(snapshot)
		}
	}
}
