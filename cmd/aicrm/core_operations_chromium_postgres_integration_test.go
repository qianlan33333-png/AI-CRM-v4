package main

import (
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The fixture uses the real admin session, composition, migrations and owner
// transactions. Provider calls remain disabled; model behavior has separate tests.
func TestPostgreSQLCoreOperationsChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	f := newAudienceConfirmationChromiumFixture(t)
	session, csrf := adminAccessLogin(t, f.application.handler, "groupops-browser-owner", "groupops-browser-owner-password")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/products", strings.NewReader(`{"product_code":"core-ui-sales","name":"可选销售课程","description":"商品关联验证","price_minor":100,"currency":"CNY","stock_quantity":10,"images":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Idempotency-Key", "core-ui-sales-create-001")
	req.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	req.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	f.application.handler.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("seed sales product status=%d body=%s", response.Code, response.Body.String())
	}

	command := exec.CommandContext(f.ctx, "node", filepath.Join(filepath.Dir(f.script), "core_operations_chromium_journey.mjs"))
	command.Env = append(os.Environ(), "AICRM_AUDIENCE_CONFIRMATION_TEST_URL="+f.server.URL, "AICRM_AUDIENCE_CONFIRMATION_TEST_USERNAME=groupops-browser-owner", "AICRM_AUDIENCE_CONFIRMATION_TEST_PASSWORD=groupops-browser-owner-password", "AICRM_AUDIENCE_CONFIRMATION_TEST_PACKAGE_ID="+strconv.FormatInt(f.packageID, 10), "AICRM_AUDIENCE_CONFIRMATION_SCREENSHOT_DIR="+f.screenshots)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "core_operations_chromium: PASS") {
		t.Fatalf("journey=%v output=%s", err, output)
	}
	var reference string
	if err = f.application.pool.Native().QueryRow(f.ctx, `SELECT product_reference FROM segment_core_products WHERE id=1`).Scan(&reference); err != nil || reference != "core-ui-sales" {
		t.Fatalf("saved product reference=%q err=%v", reference, err)
	}
	var products, prompts, assignments int
	if err = f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT count(*) FROM segment_core_products),(SELECT count(*) FROM segment_core_prompt_versions),(SELECT count(*) FROM segment_core_assignments)`).Scan(&products, &prompts, &assignments); err != nil || products != 1 || prompts != 1 || assignments != 0 {
		t.Fatalf("products=%d prompts=%d assignments=%d err=%v", products, prompts, assignments, err)
	}
	t.Log(string(output))
}
