package adapter

import (
	"context"
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSidebarUploadUsesApplicationCredentialAndNeverSends(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			if r.URL.Query().Get("corpsecret") != "secret value" {
				t.Error("wrong application credential")
			}
			io.WriteString(w, `{"errcode":0,"access_token":"application-token","expires_in":7200}`)
		case "/cgi-bin/media/upload":
			if r.URL.Query().Get("access_token") != "application-token" || r.URL.Query().Get("type") != "image" {
				t.Error("wrong upload scope")
			}
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			if part.FormName() != "media" || part.FileName() != "image.png" || string(data) != "image-bytes" {
				t.Error("unexpected upload body")
			}
			io.WriteString(w, `{"errcode":0,"media_id":"provider-image-id"}`)
		default:
			t.Errorf("unexpected provider operation %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "different-contact-secret"
	source := outboundport.SidebarImagePreparationSource{Scope: string(effectport.Hash("sidebar.image.scope.config.v1", "wx corp:10001")), Content: []byte("image-bytes"), FileName: "image.png", MediaType: "image/png"}
	receipt, attempted, err := client.UploadSidebarImage(context.Background(), source)
	if err != nil || !attempted || receipt.MediaID != "provider-image-id" || !receipt.ReadyUntil.Equal(testNow.Add(70*time.Hour)) || calls != 2 {
		t.Fatalf("upload receipt: attempted=%v err=%v calls=%d", attempted, err, calls)
	}
	source.Scope = "different-scope"
	_, attempted, err = client.UploadSidebarImage(context.Background(), source)
	if err == nil || attempted || calls != 2 {
		t.Fatal("scope mismatch reached provider")
	}
}
func TestSidebarUploadMissingReceiptIsNotSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/gettoken" {
			io.WriteString(w, `{"errcode":0,"access_token":"token","expires_in":7200}`)
			return
		}
		io.WriteString(w, `{"errcode":0}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	receipt, attempted, err := client.UploadSidebarImage(context.Background(), outboundport.SidebarImagePreparationSource{Scope: string(effectport.Hash("sidebar.image.scope.config.v1", "wx corp:10001")), Content: []byte("image-bytes"), FileName: "image.png", MediaType: "image/png"})
	if err == nil || !attempted || receipt.MediaID != "" {
		t.Fatal("missing media receipt reported success")
	}
}

func TestSidebarUploadClassifiesProviderEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
		unknown          bool
		provider         int64
	}{
		{"official rejection", `{"errcode":48002,"errmsg":"sensitive-token-and-identity"}`, "wecom_errcode_48002", 200, false, 48002},
		{"string rejection", `{"errcode":"40014","errmsg":"sensitive-token-and-identity"}`, "wecom_errcode_40014", 200, false, 40014},
		{"server failure", `{"errcode":-1}`, "upload_http_unknown", 503, true, 0},
		{"http rejection unproven", `sensitive-token-and-identity`, "upload_http_unknown", 403, true, 0},
		{"malformed", `sensitive-token-and-identity`, "upload_response_invalid", 200, true, 0},
		{"invalid errcode", `{"errcode":"invalid-sensitive-token-and-identity"}`, "upload_response_invalid", 200, true, 0},
		{"null errcode", `{"errcode":null,"media_id":"unproven"}`, "upload_response_invalid", 200, true, 0},
		{"missing receipt", `{"errcode":0}`, "upload_receipt_missing", 200, true, 0},
		{"contradiction", `{"errcode":48002,"media_id":"accepted-id"}`, "upload_response_conflict", 200, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var uploads atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/cgi-bin/gettoken" {
					io.WriteString(w, `{"access_token":"sensitive-token-and-identity","expires_in":7200}`)
					return
				}
				uploads.Add(1)
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client := newTestClient(t, server, func() time.Time { return testNow })
			receipt, attempted, err := client.UploadSidebarImage(context.Background(), sidebarUploadTestSource())
			var failure outboundport.SidebarImageUploadError
			if !attempted || !errors.As(err, &failure) || failure.OutcomeUnknown() != tc.unknown || failure.FailureCode() != tc.code || failure.ProviderErrorCode() != tc.provider || failure.HTTPStatusCode() != tc.status || uploads.Load() != 1 || receipt.MediaID != "" {
				t.Fatalf("classification attempted=%v failure=%v uploads=%d", attempted, err, uploads.Load())
			}
			if strings.Contains(err.Error(), "sensitive") {
				t.Fatal("diagnostic leaked raw provider text")
			}
		})
	}
}

func sidebarUploadTestSource() outboundport.SidebarImagePreparationSource {
	return outboundport.SidebarImagePreparationSource{Scope: string(effectport.Hash("sidebar.image.scope.config.v1", "wx corp:10001")), Content: []byte("image-bytes"), FileName: "image.png", MediaType: "image/png"}
}

func TestSidebarUploadDoesNotFollowRedirectOrRetryUnknown(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		t.Run(strconv.FormatBool(redirect), func(t *testing.T) {
			var uploads, redirected atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/cgi-bin/gettoken" {
					io.WriteString(w, `{"access_token":"secret-token","expires_in":7200}`)
					return
				}
				if r.URL.Path == "/other" {
					redirected.Add(1)
					return
				}
				uploads.Add(1)
				if redirect {
					w.Header().Set("Location", "/other")
					w.WriteHeader(307)
					return
				}
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				conn.Close()
			}))
			defer server.Close()
			client := newTestClient(t, server, func() time.Time { return testNow })
			_, attempted, err := client.UploadSidebarImage(context.Background(), sidebarUploadTestSource())
			var failure outboundport.SidebarImageUploadError
			if !attempted || !errors.As(err, &failure) || !failure.OutcomeUnknown() || uploads.Load() != 1 || redirected.Load() != 0 || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("unsafe retry/diagnostic: %v uploads=%d redirected=%d", err, uploads.Load(), redirected.Load())
			}
		})
	}
}

func TestSidebarUploadAcceptsReceiptWithoutErrcode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/gettoken" {
			io.WriteString(w, `{"access_token":"token","expires_in":7200}`)
			return
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		disposition := part.Header.Get("Content-Disposition")
		_, params, err := mime.ParseMediaType(disposition)
		data, _ := io.ReadAll(part)
		if err != nil || params["filelength"] != strconv.Itoa(len(data)) || !strings.Contains(disposition, `name="media"`) || !strings.Contains(disposition, `filename="image.png"`) {
			t.Error("multipart does not carry complete provider metadata")
		}
		io.WriteString(w, `{"type":"image","media_id":"accepted-image","created_at":"1700000000"}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	receipt, attempted, err := client.UploadSidebarImage(context.Background(), sidebarUploadTestSource())
	if err != nil || !attempted || receipt.MediaID != "accepted-image" {
		t.Fatalf("receipt=%+v attempted=%v err=%v", receipt, attempted, err)
	}
}
