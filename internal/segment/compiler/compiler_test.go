package compiler

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCompileIsStableAndContainsNoSQL(t *testing.T) {
	raw := json.RawMessage(`{"schema_version":1,"template_key":"channel_any","parameters":{"channels":["wecom","wechat"]}}`)
	left, err := (Compiler{}).Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	right, err := (Compiler{}).Compile(raw)
	if err != nil || left.Digest != right.Digest || !bytes.Equal(left.Expression, right.Expression) {
		t.Fatalf("unstable compile left=%+v right=%+v err=%v", left, right, err)
	}
	if bytes.Contains(bytes.ToLower(left.Expression), []byte("select")) {
		t.Fatalf("SQL escaped into plan: %s", left.Expression)
	}
}
