package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGroupMembershipRequiresCompleteTypedSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		count      int
		ok         bool
	}{
		{"empty", `{"chat_id":"g","member_list":[]}`, 0, true},
		{"external", `{"chat_id":"g","member_list":[{"userid":"x","type":2},{"userid":"staff","type":1}]}`, 1, true},
		{"missing", `{"chat_id":"g"}`, 0, false},
		{"null", `{"chat_id":"g","member_list":null}`, 0, false},
		{"wrong-group", `{"chat_id":"other","member_list":[]}`, 0, false},
		{"unknown-type", `{"chat_id":"g","member_list":[{"userid":"x","type":3}]}`, 0, false},
		{"missing-user", `{"chat_id":"g","member_list":[{"type":2}]}`, 0, false},
		{"duplicate", `{"chat_id":"g","member_list":[{"userid":"x","type":2},{"userid":"x","type":2}]}`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/cgi-bin/gettoken" {
					w.Write([]byte(`{"errcode":0,"access_token":"test","expires_in":7200}`))
					return
				}
				w.Write([]byte(`{"errcode":0,"group_chat":` + tc.body + `}`))
			}))
			defer server.Close()
			c, e := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "test", APIBase: server.URL, HTTPClient: server.Client()})
			if e != nil {
				t.Fatal(e)
			}
			got, e := c.ReadGroupMembership(context.Background(), "g")
			if (e == nil) != tc.ok || (tc.ok && len(got.ExternalUserIDs) != tc.count) {
				t.Fatalf("complete=%v count=%d", e == nil, len(got.ExternalUserIDs))
			}
		})
	}
}
