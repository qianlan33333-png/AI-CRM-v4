package store

import (
	"encoding/json"
	"testing"
)

func TestWebhookMaterialResultIDKeepsInt64Precision(t *testing.T) {
	value, err := webhookMaterialResultID(json.RawMessage(`{"id":9007199254740993}`))
	if err != nil || value != 9007199254740993 {
		t.Fatalf("value=%d err=%v", value, err)
	}
	if _, err = webhookMaterialResultID(json.RawMessage(`{"id":"9007199254740993"}`)); err == nil {
		t.Fatal("string replay ID accepted")
	}
}
