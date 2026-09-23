package http

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The expected hashes are the approved AI-CRM dd8 member-grid sources. This
// test changes the process working directory before rendering, proving the
// release binary reads only its embedded frozen files.
func TestMemberGridUIEmbedsFrozenDD8AssetsAndUsesOnlyHostSeam(t *testing.T) {
	t.Chdir(t.TempDir())
	expected := map[string]string{
		"templates/service_period_member_grid.html":              "0d0175b133aba389517a90b0f3a6b1451dd0b945e4ef62d3db4818f71c752126",
		"templates/service_period_member_grid_compact_base.html": "74559d756f22dd8e3d09d8f484da5db89c0da299dba782eff2b520da10fa84ee",
		"templates/service_period_member_grid_public.html":       "4acf763fd31069031a50dd202a7b264c9b7b659a9447161cb9bdfe6e795d46a5",
		"static/admin_console/member_grid.css":                   "7afe530614c717939e9be265ce4aa82b597a1c6b186a3e31af2388ea642a79b4",
		"static/admin_console/member_grid.js":                    "711cea994e69fe3ca261bb44c5dbc01b1dcc3406ac8e2a6da23d093fbe70ca0c",
		"static/admin_console/member_grid_share.js":              "9751fef50814f978547a278904fa2035113f783661be8b95ffe018df17bdc1d5",
		"static/admin_console/member_grid_state.js":              "0a0fea5426c093939f72227b1aae1c6ec61a0fd9aa32d4c34fa38af047c5f637",
	}
	for name, want := range expected {
		raw, err := memberGridDonorContent(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		got := sha256.Sum256(raw)
		if hex.EncodeToString(got[:]) != want {
			t.Fatalf("frozen source changed: %s got=%x want=%s", name, got, want)
		}
	}

	ui := NewMemberGridUI()
	for _, name := range []string{"member_grid.js", "member_grid_state.js", "member_grid_share.js", "member_grid.css"} {
		want, err := memberGridDonorContent("static/admin_console/" + name)
		if err != nil {
			t.Fatal(err)
		}
		got := httptest.NewRecorder()
		ui.ServeHTTP(got, httptest.NewRequest(http.MethodGet, "/service-period-member-grid-assets/"+name, nil))
		if got.Code != http.StatusOK || string(want) != got.Body.String() {
			t.Fatalf("asset=%s status=%d", name, got.Code)
		}
	}
	for name := range memberGridIconNames {
		want, err := memberGridDonorContent("static/icons/" + name)
		if err != nil {
			t.Fatal(err)
		}
		got := httptest.NewRecorder()
		ui.ServeHTTP(got, httptest.NewRequest(http.MethodGet, "/static/service-period/icons/"+name, nil))
		if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "image/svg+xml" || string(want) != got.Body.String() {
			t.Fatalf("icon=%s status=%d type=%q", name, got.Code, got.Header().Get("Content-Type"))
		}
	}
	host := httptest.NewRecorder()
	ui.ServeHTTP(host, httptest.NewRequest(http.MethodGet, "/service-period-member-grid-assets/member_grid_host.js", nil))
	if host.Code != http.StatusOK || !strings.Contains(host.Body.String(), "X-CSRF-Token") || !strings.Contains(host.Body.String(), "Idempotency-Key") || !strings.Contains(host.Body.String(), "memberGridStaffURL") {
		t.Fatalf("host=%d body=%s", host.Code, host.Body.String())
	}
	internal := httptest.NewRecorder()
	if err := RenderMemberGridInternal(internal, httptest.NewRequest(http.MethodGet, "/admin/spProductData.html?id=7", nil), "7"); err != nil {
		t.Fatal(err)
	}
	if internal.Code != http.StatusOK || !strings.Contains(internal.Body.String(), `id="spMemberGrid"`) || !strings.Contains(internal.Body.String(), `data-service-product-id="7"`) || !strings.Contains(internal.Body.String(), "operation_member_picker.js") || !strings.Contains(internal.Body.String(), "member_grid.js") {
		t.Fatalf("internal=%d body=%s", internal.Code, internal.Body.String())
	}
	public := httptest.NewRecorder()
	ui.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/shared/service-period-member-grid", nil))
	body := public.Body.String()
	if public.Code != http.StatusOK || !strings.Contains(body, "member_grid.js") || !strings.Contains(body, "member_grid_host.js") || strings.Contains(body, "/static/service-period/admin_console/") || strings.Index(body, "member_grid_host.js") > strings.Index(body, "member_grid_state.js") {
		t.Fatalf("public=%d body=%s", public.Code, public.Body.String())
	}
}
