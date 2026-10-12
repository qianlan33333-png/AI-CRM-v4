package adapter

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestArchiveJSONControlCharactersPreserveDecodedText(t *testing.T) {
	text := "中文 quote=\" slash=\\"
	for b := byte(0); b < 32; b++ {
		text += string(b)
	}
	encoded, _ := json.Marshal(text)
	raw := append([]byte(`{"text":`), encoded...)
	raw = append(raw, '}')
	// Replace only the encoded C0 suffix with the literal SDK variant.
	prefix, _ := json.Marshal("中文 quote=\" slash=\\")
	broken := append([]byte(`{"text":`), prefix[:len(prefix)-1]...)
	for b := byte(0); b < 32; b++ {
		broken = append(broken, b)
	}
	broken = append(broken, '"', '}')
	normalized, repaired, err := normalizeArchiveJSON(broken)
	if err != nil || !repaired {
		t.Fatalf("repair=%t err=%v", repaired, err)
	}
	var got struct {
		Text string `json:"text"`
	}
	if err = json.Unmarshal(normalized, &got); err != nil || got.Text != text {
		t.Fatal("decoded text changed")
	}
	unchanged, repaired, err := normalizeArchiveJSON(raw)
	if err != nil || repaired || !bytes.Equal(raw, unchanged) {
		t.Fatal("valid payload changed")
	}
}

func TestArchiveJSONRejectsOtherDamage(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte("{\"text\":\"x\n\""),
		[]byte("{\"text\":\"x\\\n\"}"),
		[]byte("{\"text\":\"x\n\",\"id\":\x01}"),
		[]byte("{\"text\":\"x\n\"} trailing"),
		[]byte("{\"text\":\"x\n\",\"bad\":\"\\q\"}"),
	} {
		if _, _, err := normalizeArchiveJSON(raw); err == nil {
			t.Fatal("unrelated corruption accepted")
		}
	}
}
