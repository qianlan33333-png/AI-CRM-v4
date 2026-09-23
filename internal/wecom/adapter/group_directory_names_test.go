package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGroupDirectoryPreservesNamedAndUnnamedGroups(t *testing.T) {
	details := map[string]string{
		"named":          `{"errcode":0,"group_chat":{"chat_id":"named","owner":"owner-1","name":"真实群名","member_list":[{"type":2}]}}`,
		"unnamed":        `{"errcode":0,"group_chat":{"chat_id":"unnamed","owner":"owner-1","name":"","member_list":[{"type":1}]}}`,
		"missing-name":   `{"errcode":0,"group_chat":{"chat_id":"missing-name","owner":"owner-1","member_list":[]}}`,
		"missing-id":     `{"errcode":0,"group_chat":{"owner":"owner-1","name":""}}`,
		"missing-owner":  `{"errcode":0,"group_chat":{"chat_id":"missing-owner","name":""}}`,
		"provider-error": `{"errcode":48002,"errmsg":"permission denied"}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"test-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/groupchat/list":
			_, _ = w.Write([]byte(`{"errcode":0,"group_chat_list":[{"chat_id":"named","status":0},{"chat_id":"unnamed","status":0},{"chat_id":"missing-name","status":0}]}`))
		case "/cgi-bin/externalcontact/groupchat/get":
			var body struct {
				ChatID string `json:"chat_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(details[body.ChatID]))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "test-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListGroupChats(context.Background(), "owner-1", "", 100)
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("mixed directory count=%d err=%v", len(page.Items), err)
	}
	for _, item := range page.Items {
		group, err := client.GetGroupChat(context.Background(), item.ChatID)
		if err != nil || group.ChatID != item.ChatID || group.OwnerUserID != "owner-1" {
			t.Fatalf("group identity was not preserved: err=%v", err)
		}
		want := ""
		if item.ChatID == "named" {
			want = "真实群名"
		}
		if group.Name != want {
			t.Fatalf("name=%q want=%q", group.Name, want)
		}
	}
	for _, id := range []string{"missing-id", "missing-owner", "provider-error"} {
		t.Run(id, func(t *testing.T) {
			if _, err := client.GetGroupChat(context.Background(), id); !errors.Is(err, ErrResponse) {
				t.Fatalf("invalid provider result must fail, got %v", err)
			}
		})
	}
}
