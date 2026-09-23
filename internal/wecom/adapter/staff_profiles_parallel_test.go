package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestStaffProfilesReadPageWithBoundedConcurrency(t *testing.T) {
	var active, peak atomic.Int32
	firstFour := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/gettoken" {
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
			return
		}
		if r.URL.Path != "/cgi-bin/user/get" {
			t.Errorf("unexpected path")
			w.WriteHeader(404)
			return
		}
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if n == 4 {
			select {
			case <-firstFour:
			default:
				close(firstFour)
			}
		}
		select {
		case <-firstFour:
		case <-r.Context().Done():
			return
		}
		id := r.URL.Query().Get("userid")
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userid": id, "name": "Name-" + id})
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	ids := make([]string, 42)
	for i := range ids {
		ids[i] = "staff-" + strconv.Itoa(i)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshot, err := client.ReadContactStaffProfiles(ctx, ids)
	if err != nil || snapshot.ProfileReadState != "ready" || len(snapshot.Items) != len(ids) {
		t.Fatalf("incomplete profile page: count=%d state=%s err=%v", len(snapshot.Items), snapshot.ProfileReadState, err)
	}
	if peak.Load() != 4 {
		t.Fatalf("concurrency=%d", peak.Load())
	}
	for i, item := range snapshot.Items {
		if item.UserID != ids[i] || item.DisplayName != "Name-"+ids[i] {
			t.Fatalf("profile order/identity mismatch at %d", i)
		}
	}
}

func TestStaffProfilesShareRefreshedToken(t *testing.T) {
	var tokens atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/gettoken" {
			token := "old"
			if tokens.Add(1) > 1 {
				token = "new"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": token, "expires_in": 7200})
			return
		}
		if r.URL.Query().Get("access_token") == "old" {
			_, _ = w.Write([]byte(`{"errcode":42001}`))
			return
		}
		id := r.URL.Query().Get("userid")
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userid": id, "name": "Name-" + id})
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	snapshot, err := client.ReadContactStaffProfiles(context.Background(), []string{"a", "b", "c", "d", "e", "f", "g", "h"})
	if err != nil || len(snapshot.Items) != 8 || snapshot.ProfileReadState != "ready" || tokens.Load() != 2 {
		t.Fatalf("profiles=%d state=%s token reads=%d err=%v", len(snapshot.Items), snapshot.ProfileReadState, tokens.Load(), err)
	}
}
