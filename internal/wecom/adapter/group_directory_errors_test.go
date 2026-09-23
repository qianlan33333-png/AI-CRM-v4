package adapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type groupDirectoryRoundTrip func(*http.Request) (*http.Response, error)

func (f groupDirectoryRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGroupDirectorySafeFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		timeout          bool
	}{
		{"permission", `{"errcode":48002,"errmsg":"secret-provider-text"}`, "provider_permission_denied", false},
		{"invalid", `not-json-secret-provider-text`, "provider_response_invalid", false},
		{"timeout", "", "provider_timeout", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/cgi-bin/gettoken" {
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"test-token","expires_in":7200}`))
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			hc := server.Client()
			if tc.timeout {
				transport := hc.Transport
				hc.Transport = groupDirectoryRoundTrip(func(r *http.Request) (*http.Response, error) {
					if strings.Contains(r.URL.Path, "/groupchat/") {
						return nil, context.DeadlineExceeded
					}
					return transport.RoundTrip(r)
				})
			}
			client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "test-secret", APIBase: server.URL, HTTPClient: hc})
			if err != nil {
				t.Fatal(err)
			}
			for _, read := range []func() error{
				func() error { _, e := client.ListGroupChats(context.Background(), "owner-1", "", 100); return e },
				func() error { _, e := client.GetGroupChat(context.Background(), "chat-1"); return e },
			} {
				err := read()
				var classified wecomport.DirectoryFailure
				if !errors.As(err, &classified) || classified.DirectoryFailureCode() != tc.code {
					t.Fatalf("classification=%v want=%s", err, tc.code)
				}
				if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "http") {
					t.Fatal("provider information leaked")
				}
			}
		})
	}
}
