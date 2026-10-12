package app

import (
	"bytes"
	"crypto/sha256"
	"testing"

	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestArchiveNormalizationRetainsRepairedProviderEvidence(t *testing.T) {
	raw := []byte("{\"msgid\":\"repair\",\"from\":\"staff\",\"tolist\":[],\"msgtype\":\"text\",\"msgtime\":1,\"text\":{\"content\":\"a\nb\"}}")
	normalized := []byte(`{"msgid":"repair","from":"staff","tolist":[],"msgtype":"text","msgtime":1,"text":{"content":"a\nb"}}`)
	s := Service{CorpScope: "wecom-corp:fixture"}
	messages, issues, _, next, err := s.normalizePage([]wecomport.EncryptedArchiveRecord{{Seq: 17, MsgID: "repair"}}, []wecomport.PlainArchiveRecord{{Seq: 17, MsgID: "repair", Payload: normalized, OriginalPayload: raw}})
	if err != nil || next != 17 || len(messages) != 1 || messages[0].ContentText != "a\nb" || len(issues) != 1 {
		t.Fatalf("normalize result err=%v messages=%d issues=%d next=%d", err, len(messages), len(issues), next)
	}
	if !bytes.Equal(issues[0].Payload, raw) || issues[0].Digest != sha256.Sum256(raw) || issues[0].Reason != "json_control_character_escaped" {
		t.Fatal("original evidence lost")
	}
	raw[0] = 'x'
	if issues[0].Payload[0] != '{' {
		t.Fatal("evidence aliases caller buffer")
	}
}
