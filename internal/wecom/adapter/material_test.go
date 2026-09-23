package adapter

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

func TestMaterialUploaderClassifiesConfirmedRejectAndSuccess(t *testing.T) {
	for _, test := range []struct {
		name      string
		response  string
		wantError bool
		retryable bool
	}{
		{name: "official string timestamp success", response: fmt.Sprintf(`{"errcode":0,"errmsg":"ok","type":"image","media_id":"media-1","created_at":"%d"}`, testNow.Unix())},
		{name: "numeric timestamp compatibility", response: fmt.Sprintf(`{"errcode":0,"media_id":"media-1","created_at":%d}`, testNow.Unix())},
		{name: "rate limited", response: `{"errcode":45009}`, wantError: true, retryable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
				case "/cgi-bin/media/upload":
					if r.Method != http.MethodPost || r.URL.Query().Get("access_token") != "token" || r.URL.Query().Get("type") != "image" {
						t.Fatalf("request=%s?%s", r.URL.Path, r.URL.RawQuery)
					}
					_, _ = w.Write([]byte(test.response))
				default:
					t.Fatalf("path=%s", r.URL.Path)
				}
			}))
			defer server.Close()
			client := newTestClient(t, server, func() time.Time { return testNow })
			uploader, err := NewMaterialUploader(client, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			if err != nil {
				t.Fatal(err)
			}
			content := []byte("image bytes")
			digest := sha256.Sum256(content)
			receipt, called, err := uploader.UploadMaterial(context.Background(), outboundport.MaterialSourceSnapshot{SourceRef: "image:1", SourceType: "image", ContentDigest: digest, FileName: "cover.png", MediaType: "image/png", SizeBytes: int64(len(content)), SnapshotVersion: 1}, outboundport.MaterialSourceContent{Bytes: content, FileName: "cover.png", MediaType: "image/png"}, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			if !test.wantError {
				if err != nil || !called || receipt.MediaID != "media-1" || !receipt.ProviderCreatedAt.Equal(testNow.UTC()) {
					t.Fatalf("receipt=%+v called=%t err=%v", receipt, called, err)
				}
				return
			}
			var classified outboundport.MaterialUploadError
			if !called || !errors.As(err, &classified) || classified.OutcomeUnknown() || classified.Retryable() != test.retryable || classified.FailureCode() != "wecom_errcode_45009" {
				t.Fatalf("called=%t err=%v classified=%v", called, err, classified)
			}
		})
	}
}

func TestParseMaterialCreatedAtRejectsUntrustedTimestamps(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "missing"},
		{name: "null", raw: `null`},
		{name: "empty string", raw: `""`},
		{name: "zero number", raw: `0`},
		{name: "negative number", raw: `-1`},
		{name: "negative string", raw: `"-1"`},
		{name: "leading plus", raw: `"+1"`},
		{name: "surrounding spaces", raw: `" 1 "`},
		{name: "decimal", raw: `1.5`},
		{name: "exponent", raw: `1e9`},
		{name: "non decimal string", raw: `"1.5"`},
		{name: "overflow", raw: `"9223372036854775808"`},
		{name: "max int number", raw: `9223372036854775807`},
		{name: "max int string", raw: `"9223372036854775807"`},
		{name: "future", raw: fmt.Sprintf(`"%d"`, testNow.Add(5*time.Minute+time.Second).Unix())},
	} {
		t.Run(test.name, func(t *testing.T) {
			if createdAt, ok := parseMaterialCreatedAt([]byte(test.raw), testNow); ok || !createdAt.IsZero() {
				t.Fatalf("created_at=%q parsed=%s ok=%t", test.raw, createdAt, ok)
			}
		})
	}
	boundary := testNow.Add(5 * time.Minute).Unix()
	for _, raw := range []string{strconv.FormatInt(boundary, 10), fmt.Sprintf(`"%d"`, boundary)} {
		if createdAt, ok := parseMaterialCreatedAt([]byte(raw), testNow); !ok || createdAt.Unix() != boundary {
			t.Fatalf("boundary created_at=%q parsed=%s ok=%t", raw, createdAt, ok)
		}
	}
}

func TestMaterialUploaderClassifiesUntrustedCreatedAtAsUnknown(t *testing.T) {
	for _, createdAt := range []string{`null`, `""`, `"-1"`, `1.5`, `"not-a-timestamp"`, `9223372036854775807`, `"9223372036854775807"`, fmt.Sprintf(`"%d"`, testNow.Add(5*time.Minute+time.Second).Unix())} {
		t.Run(createdAt, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
				case "/cgi-bin/media/upload":
					_, _ = fmt.Fprintf(w, `{"errcode":0,"media_id":"media-1","created_at":%s}`, createdAt)
				default:
					t.Fatalf("path=%s", r.URL.Path)
				}
			}))
			defer server.Close()
			client := newTestClient(t, server, func() time.Time { return testNow })
			uploader, err := NewMaterialUploader(client, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			if err != nil {
				t.Fatal(err)
			}
			content := []byte("image bytes")
			digest := sha256.Sum256(content)
			_, called, err := uploader.UploadMaterial(context.Background(), outboundport.MaterialSourceSnapshot{SourceRef: "image:1", SourceType: "image", ContentDigest: digest, FileName: "cover.png", MediaType: "image/png", SizeBytes: int64(len(content)), SnapshotVersion: 1}, outboundport.MaterialSourceContent{Bytes: content, FileName: "cover.png", MediaType: "image/png"}, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			var classified outboundport.MaterialUploadError
			if !called || !errors.As(err, &classified) || !classified.OutcomeUnknown() || classified.Retryable() || classified.FailureCode() != "upload_created_at_missing" {
				t.Fatalf("created_at=%s called=%t err=%v classified=%v", createdAt, called, err, classified)
			}
		})
	}
}

func TestMaterialUploaderUsesDedicatedTimeoutInsteadOfClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
		case "/cgi-bin/media/upload":
			time.Sleep(30 * time.Millisecond)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"media_id":"media-1","created_at":%d}`, testNow.Unix())))
		default:
			t.Fatalf("path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.http.Timeout = 10 * time.Millisecond
	client.config.UploadTimeout = 100 * time.Millisecond
	uploader, err := NewMaterialUploader(client, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("image bytes")
	digest := sha256.Sum256(content)
	receipt, called, err := uploader.UploadMaterial(context.Background(), outboundport.MaterialSourceSnapshot{SourceRef: "image:1", SourceType: "image", ContentDigest: digest, FileName: "cover.png", MediaType: "image/png", SizeBytes: int64(len(content)), SnapshotVersion: 1}, outboundport.MaterialSourceContent{Bytes: content, FileName: "cover.png", MediaType: "image/png"}, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil || !called || receipt.MediaID != "media-1" {
		t.Fatalf("receipt=%+v called=%t err=%v", receipt, called, err)
	}
}
