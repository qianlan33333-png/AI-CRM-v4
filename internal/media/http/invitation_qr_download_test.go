package http

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	g "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	app "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

type invitationQRTransport func(*http.Request) (*http.Response, error)

func (fn invitationQRTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

type invitationQRStore struct {
	app.InvitationStore
	plan p.InvitationPlan
}

func (s *invitationQRStore) ReadInvitationPlan(context.Context, int64) (p.InvitationPlan, error) {
	return s.plan, nil
}
func (s *invitationQRStore) ReadPublicInvitation(context.Context, string) (p.InvitationPlan, error) {
	return s.plan, nil
}
func (s *invitationQRStore) ApplyInvitationEvaluation(_ context.Context, _, after p.InvitationPlan) error {
	s.plan = after
	return nil
}

type invitationQRCatalog struct {
	g.Catalog
	group g.CatalogGroup
}

func (c invitationQRCatalog) ReadCatalogGroup(context.Context, string) (g.CatalogGroup, error) {
	return c.group, nil
}

func testOfficialQR(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	img.Set(0, 0, color.Black)
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestInvitationOfficialQRDownload(t *testing.T) {
	original := testOfficialQR(t)
	requests := 0
	client := &http.Client{Transport: invitationQRTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(original)), ContentLength: int64(len(original)), Header: http.Header{"Content-Type": {"image/png"}}}, nil
	})}
	plan := p.InvitationPlan{ID: 11, Token: strings.Repeat("a", 48), Enabled: true, State: "active", CurrentChatID: "chat-a", ProviderState: "executed", ProviderConfigID: "stable-config", ProviderQRCode: "https://wework.qpic.cn/qr/0", Bindings: []p.InvitationBinding{{ChatID: "chat-a", CodeState: "executed", QRCode: "https://wework.qpic.cn/qr/0"}}}
	store := &invitationQRStore{plan: plan}
	now := time.Now().UTC()
	catalog := invitationQRCatalog{group: g.CatalogGroup{ChatID: "chat-a", ObservedAt: &now}}
	serve := func(auth string) *httptest.ResponseRecorder {
		h := InvitationHandler{Service: &app.InvitationService{Store: store, Catalog: catalog}, Security: handlerTestSecurity{}, QRCodeClient: client}
		r := httptest.NewRequest(http.MethodGet, "/api/admin/group-invitations/11/qr-download", nil)
		if auth != "" {
			r.Header.Set("X-Test-Auth", auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if got := serve("none"); got.Code != http.StatusUnauthorized || requests != 0 {
		t.Fatalf("unauthorized download: status=%d provider_reads=%d", got.Code, requests)
	}
	store.plan.ProviderState = "accepted"
	if got := serve(""); got.Code != http.StatusConflict || requests != 0 {
		t.Fatalf("unconfirmed download: status=%d provider_reads=%d", got.Code, requests)
	}
	store.plan.ProviderState = "executed"
	got := serve("")
	if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "image/png" || !strings.Contains(got.Header().Get("Content-Disposition"), "group-invitation-11.png") || requests != 1 {
		t.Fatalf("official download: status=%d headers=%v provider_reads=%d", got.Code, got.Header(), requests)
	}
	if _, err := png.Decode(bytes.NewReader(got.Body.Bytes())); err != nil {
		t.Fatalf("download was not a PNG image: %v", err)
	}
	catalog.group.MemberCount = 200
	if got := serve(""); got.Code != http.StatusConflict || requests != 1 {
		t.Fatalf("full group still downloadable: status=%d provider_reads=%d", got.Code, requests)
	}
}

func TestInvitationOfficialQRRejectsUntrustedImages(t *testing.T) {
	qr := testOfficialQR(t)
	for _, url := range []string{"http://example.com/qr", "https://example.com/qr", "https://wework.qpic.cn:8443/qr", "https://user@wework.qpic.cn/qr"} {
		if _, err := fetchOfficialQRCode(context.Background(), url, nil); err == nil {
			t.Fatalf("untrusted QR source accepted: %s", url)
		}
	}
	tlsClient := &http.Client{Transport: invitationQRTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Hostname() != "p.qpic.cn" {
			t.Fatalf("documented QR URL was not upgraded to TLS: %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(qr)), ContentLength: int64(len(qr))}, nil
	})}
	if _, err := fetchOfficialQRCode(context.Background(), "http://p.qpic.cn/wwhead/example/0", tlsClient); err != nil {
		t.Fatalf("documented official QR URL rejected: %v", err)
	}
	client := &http.Client{Transport: invitationQRTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Body: io.NopCloser(bytes.NewReader(qr)), Header: http.Header{"Location": {"https://example.com/qr"}}}, nil
	})}
	if _, err := fetchOfficialQRCode(context.Background(), "https://wework.qpic.cn/qr", client); err == nil {
		t.Fatal("redirected QR source accepted")
	}
	client.Transport = invitationQRTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("not an image"))}, nil
	})
	if _, err := fetchOfficialQRCode(context.Background(), "https://wework.qpic.cn/qr", client); err == nil {
		t.Fatal("invalid QR image accepted")
	}
}
