package adapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrivateSendResultRetainsSafeProtocolNumbers(t *testing.T) {
	for _, code := range []int64{0, 48002} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/cgi-bin/gettoken" {
				fmt.Fprint(w, `{"errcode":0,"access_token":"secret-token","expires_in":7200}`)
				return
			}
			if r.Method != "POST" || r.URL.Path != "/cgi-bin/externalcontact/get_groupmsg_send_result" {
				t.Error("unexpected Provider operation")
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, `{"errcode":%d,"send_list":[]}`, code)
		}))
		client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "secret", APIBase: server.URL, HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		page, err := client.GetPrivateMessageSendResult(context.Background(), "msg-original", "sender", "")
		server.Close()
		if page.HTTPStatusCode != 200 || page.ProviderErrorCode == nil || *page.ProviderErrorCode != code || (err != nil) != (code != 0) {
			t.Fatalf("code=%d page=%+v err=%v", code, page, err)
		}
	}
}
