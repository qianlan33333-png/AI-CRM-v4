package channel

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type assetHTTPTestApplication struct {
	assets    []AcquisitionAsset
	listCalls int
}

func (app *assetHTTPTestApplication) Publish(context.Context, int64, int64, AcquisitionAssetKind, string) (AcquisitionAsset, error) {
	return AcquisitionAsset{}, ErrAssetUnavailable
}

func (app *assetHTTPTestApplication) Mutate(context.Context, int64, int64, string, string, string) (AcquisitionAsset, error) {
	return AcquisitionAsset{}, ErrAssetUnavailable
}

func (app *assetHTTPTestApplication) List(context.Context, int64, int, int64) ([]AcquisitionAsset, error) {
	app.listCalls++
	return app.assets, nil
}

func (app *assetHTTPTestApplication) Get(context.Context, int64, string) (AcquisitionAsset, error) {
	return AcquisitionAsset{}, ErrAssetUnavailable
}

type assetHTTPRoundTripper func(*http.Request) (*http.Response, error)

func (roundTripper assetHTTPRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func TestAssetHTTPQRCodeDownloadRequiresAdministrator(t *testing.T) {
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	app := &assetHTTPTestApplication{assets: []AcquisitionAsset{{
		ID: 1, ChannelID: 42, AssetVersion: 1, Kind: AcquisitionAssetQRCode,
		Operation: "create", State: "executed", ResultURL: "https://wework.qpic.cn/qrcode.png",
		CreatedAt: now, UpdatedAt: now,
	}}}
	security := &catalogHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}
	handler, err := NewAssetHTTPHandler(app, security)
	if err != nil {
		t.Fatal(err)
	}
	handler.download = &http.Client{Transport: assetHTTPRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://wework.qpic.cn/qrcode.png" {
			t.Fatalf("download URL=%s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"image/png"}}, ContentLength: 3, Body: io.NopCloser(strings.NewReader("png"))}, nil
	})}

	for _, test := range []struct {
		name       string
		roles      []accessdomain.Role
		wantStatus int
	}{
		{name: "viewer cannot download", roles: []accessdomain.Role{accessdomain.RoleViewer}, wantStatus: http.StatusForbidden},
		{name: "admin can download", roles: []accessdomain.Role{accessdomain.RoleAdmin}, wantStatus: http.StatusOK},
		{name: "super admin can download", roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}, wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			security.principal.Roles = test.roles
			response := catalogHTTPRequest(handler, http.MethodGet, "/api/admin/channels/42/qrcode/download", "", nil)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.wantStatus == http.StatusOK && (response.Body.String() != "png" || response.Header().Get("Content-Disposition") == "") {
				t.Fatalf("download response headers=%v body=%q", response.Header(), response.Body.String())
			}
		})
	}
	if app.listCalls != 2 {
		t.Fatalf("asset list calls=%d, viewer request must not resolve a download asset", app.listCalls)
	}
}
