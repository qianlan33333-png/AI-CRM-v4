package app

import (
	"encoding/json"
	"testing"

	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestNormalizeTextPreservesOriginalWhitespaceAndUnicode(t *testing.T) {
	for _, text := range []string{"\n  客户提供的说明🙂\t\n", "\u3000中文空格\u3000", " \n\t "} {
		payload, err := json.Marshal(map[string]any{
			"msgid": "format-message", "from": "staff", "tolist": []string{"wm_customer"},
			"msgtype": "text", "msgtime": int64(1791379200000), "text": map[string]string{"content": text},
		})
		if err != nil {
			t.Fatal(err)
		}
		message, err := NormalizeArchiveRecord("wecom-corp:local-fixture", wecomport.PlainArchiveRecord{Seq: 1, MsgID: "format-message", Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		var normalized struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(message.Normalized, &normalized); err != nil {
			t.Fatal(err)
		}
		if message.ContentText != text || normalized.Content != text {
			t.Fatalf("archive changed original text: input=%q content=%q normalized=%q", text, message.ContentText, normalized.Content)
		}
	}
}
