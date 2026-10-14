package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrivateMessageStopProtocolAndUnknown(t *testing.T) {
	for _, test := range []struct {
		name, body      string
		reject, unknown bool
	}{{"accepted", `{"errcode":0}`, false, false}, {"denied", `{"errcode":60011}`, true, false}, {"missing_code", `{"errmsg":"ok"}`, true, true}, {"malformed", `not-json`, true, true}} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/cgi-bin/gettoken" {
					fmt.Fprint(w, `{"errcode":0,"access_token":"fixture","expires_in":7200}`)
					return
				}
				calls++
				var b map[string]string
				if r.Method != "POST" || r.URL.Path != "/cgi-bin/externalcontact/cancel_groupmsg_send" || json.NewDecoder(r.Body).Decode(&b) != nil || len(b) != 1 || b["msgid"] != "original-task" {
					t.Error("stop did not use exact frozen task")
				}
				fmt.Fprint(w, test.body)
			}))
			defer s.Close()
			client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "fixture", APIBase: s.URL, HTTPClient: s.Client()})
			if err != nil {
				t.Fatal(err)
			}
			attempted, err := client.StopPrivateMessage(context.Background(), "original-task")
			if !attempted || calls != 1 || (err != nil) != test.reject {
				t.Fatalf("attempted=%v calls=%d err=%v", attempted, calls, err)
			}
			if err != nil {
				var classified outbound.PrivateMessageSendError
				if !errors.As(err, &classified) || classified.OutcomeUnknown() != test.unknown {
					t.Fatal("ambiguous response lost original outcome")
				}
			}
		})
	}
}
