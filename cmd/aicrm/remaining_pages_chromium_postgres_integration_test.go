package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLRemainingPagesChromiumJourney covers the final route-evidence
// gap with the composed server, isolated PostgreSQL and actual Chromium. It
// only creates disposable fixture facts; the browser performs no Provider,
// payment or production write.
func TestPostgreSQLRemainingPagesChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run required Chromium acceptance")
	}
	fixture := newProductExternalPushChromiumFixtureWithTimeout(t, 3*time.Minute)
	adminSession, csrf := adminAccessLogin(t, fixture.application.handler, "product-browser-owner", "product-browser-owner-password")
	archiveCustomerID := seedRemainingPagesArchive(t, fixture)
	assertRemainingPagesArchiveRead(t, fixture, adminSession, csrf, archiveCustomerID)
	radarCode := seedRemainingPagesRadar(t, fixture)
	couponSlug := seedRemainingPagesCoupon(t, fixture)
	assertRemainingPagesCouponClaimWindow(t, fixture, couponSlug)
	seedRemainingPagesGridMember(t, fixture)
	gridToken := enableRemainingPagesGridShare(t, fixture, adminSession, csrf)

	unauthorized := httptest.NewRecorder()
	fixture.application.handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/admin/message-archive/customers/"+strconv.FormatInt(archiveCustomerID, 10), nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("message archive unauthenticated status=%d", unauthorized.Code)
	}

	screenshots := t.TempDir()
	if configured := platformconfig.RemainingPagesChromiumScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatal("AICRM_REMAINING_PAGES_CHROMIUM_SCREENSHOT_DIR must be absolute")
		}
		if err := os.MkdirAll(configured, 0o700); err != nil {
			t.Fatal(err)
		}
		screenshots = configured
	}
	revision := remainingPagesChromiumRevision(t)
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "remaining_pages_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_REMAINING_PAGES_URL="+fixture.server.URL,
		"AICRM_REMAINING_PAGES_ADMIN_SESSION="+adminSession,
		"AICRM_REMAINING_PAGES_CSRF="+csrf,
		"AICRM_REMAINING_PAGES_ARCHIVE_CUSTOMER_ID="+strconv.FormatInt(archiveCustomerID, 10),
		"AICRM_REMAINING_PAGES_RADAR_CODE="+radarCode,
		"AICRM_REMAINING_PAGES_COUPON_SLUG="+couponSlug,
		"AICRM_REMAINING_PAGES_GRID_TOKEN="+gridToken,
		"AICRM_REMAINING_PAGES_SCREENSHOT_DIR="+screenshots,
		"AICRM_REMAINING_PAGES_REVISION="+revision,
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "remaining_pages_chromium: PASS") {
		t.Fatalf("remaining pages Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	assertRemainingPagesRadarImageReceipt(t, fixture, radarCode)
	prefix := "remaining-pages-" + revision[:12] + "-"
	for _, name := range []string{
		"archive-entry-1280.png", "archive-entry-1440.png", "archive-detail-1280.png", "archive-detail-1440.png",
		"radar-375.png", "radar-390.png", "radar-430.png",
		"coupon-375.png", "coupon-390.png", "coupon-430.png",
		"member-grid-375.png", "member-grid-390.png", "member-grid-430.png",
	} {
		info, statErr := os.Stat(filepath.Join(screenshots, prefix+name))
		if statErr != nil || info.Size() < 512 {
			t.Fatalf("remaining page screenshot=%s exists=%t size=%d", name, statErr == nil, func() int64 {
				if info == nil {
					return 0
				}
				return info.Size()
			}())
		}
	}
}

func seedRemainingPagesArchive(t *testing.T, fixture *productExternalPushChromiumFixture) int64 {
	t.Helper()
	var customerID, staffID, messageID int64
	now := time.Now().UTC().Add(-time.Minute)
	pool := fixture.application.pool.Native()
	if err := pool.QueryRow(fixture.ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(fixture.ctx, `SELECT id FROM admin_users WHERE username='product-browser-owner'`).Scan(&staffID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(fixture.ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,activation_status,source,source_version,last_synced_at,updated_at) VALUES($1,'active','存档 Chromium 客户','active','remaining-pages',1,$2,$2)`, customerID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(fixture.ctx, `INSERT INTO message_archive_sync_state(corp_scope) VALUES('wecom-corp:remaining-pages')`); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(fixture.ctx, `INSERT INTO message_archive_messages(corp_scope,seq,msgid,msgtype,conversation_type,msgtime_ms,occurred_at,content_text) VALUES('wecom-corp:remaining-pages',1,'remaining-pages-archive-1','text','private',1760000000000,$1,'存档浏览器夹具消息') RETURNING id`, now).Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(fixture.ctx, `INSERT INTO message_archive_participants(message_id,participant_role,actor_type,provider_value,provider_value_digest,staff_user_id,customer_id_at_ingest,resolution_status) VALUES($1,'recipient','external_customer','remaining-pages-customer',decode(repeat('01',32),'hex'),NULL,$2,'found'),($1,'sender','staff','product-browser-owner',decode(repeat('02',32),'hex'),$3,NULL,'not_applicable')`, messageID, customerID, staffID); err != nil {
		t.Fatal(err)
	}
	return customerID
}

func assertRemainingPagesArchiveRead(t *testing.T, fixture *productExternalPushChromiumFixture, session, csrf string, customerID int64) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/message-archive/customers/"+strconv.FormatInt(customerID, 10), nil)
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	fixture.application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "存档浏览器夹具消息") {
		t.Fatalf("message archive authenticated read status=%d has_fixture=%t body=%s", response.Code, strings.Contains(response.Body.String(), "存档浏览器夹具消息"), response.Body.String())
	}
}

func seedRemainingPagesRadar(t *testing.T, fixture *productExternalPushChromiumFixture) string {
	t.Helper()
	const code = "rd_remainingpageschromium"
	now := time.Now().UTC()
	imageID := seedRemainingPagesRadarImage(t, fixture)
	var id int64
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `INSERT INTO radar_links(public_code,name,title,description,content_type,media_id,auth_policy,status,created_by,updated_by,created_at,updated_at) VALUES($1,'剩余页面雷达','剩余页面雷达图片','Chromium public fixture','image',$2,'anonymous','enabled',1,1,$3,$3) RETURNING id`, code, imageID, now).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.application.pool.Native().Exec(fixture.ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,1,'{}'::jsonb,1,$2)`, id, now); err != nil {
		t.Fatal(err)
	}
	return code
}

func seedRemainingPagesRadarImage(t *testing.T, fixture *productExternalPushChromiumFixture) int64 {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 320, 180))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{R: 35, G: 99, B: 235, A: 255}), image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(0, 90, 320, 180), image.NewUniform(color.RGBA{R: 16, G: 145, B: 117, A: 255}), image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	content := encoded.Bytes()
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	pool := fixture.application.pool.Native()
	if _, err := pool.Exec(fixture.ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'image/png',$2,$3)`, digest, len(content), content); err != nil {
		t.Fatal(err)
	}
	var imageID int64
	if err := pool.QueryRow(fixture.ctx, `INSERT INTO media_images(blob_digest,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_by,updated_by) VALUES($1,'remaining-pages-radar-visible.png','剩余页面雷达可见图','Chromium public visible image fixture','chromium,radar','chromium-radar','image/png',$2,320,180,true,1,1) RETURNING id`, digest, len(content)).Scan(&imageID); err != nil {
		t.Fatal(err)
	}
	return imageID
}

func seedRemainingPagesCoupon(t *testing.T, fixture *productExternalPushChromiumFixture) string {
	t.Helper()
	const slug = "remaining-pages-coupon"
	now := time.Now().UTC()
	var couponID int64
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `INSERT INTO coupon_rules(name,discount_amount_total,currency,status,total_issue_limit,per_user_issue_limit,issued_count,claim_starts_at,claim_ends_at,validity_mode,relative_validity_days,instructions,created_by,updated_by,created_at,updated_at,public_slug) VALUES('剩余页面公开优惠券',1000,'CNY','published',100,1,0,$1,$2,'relative_days',30,'Chromium 公开优惠券夹具',1,1,$3,$3,$4) RETURNING id`, now.Add(-time.Hour), now.Add(24*time.Hour), now, slug).Scan(&couponID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.application.pool.Native().Exec(fixture.ctx, `INSERT INTO coupon_rule_targets(coupon_id,target_ref,position) VALUES($1,$2,0)`, couponID, fmt.Sprintf("standard_product:%d", fixture.productID)); err != nil {
		t.Fatal(err)
	}
	return slug
}

func assertRemainingPagesCouponClaimWindow(t *testing.T, fixture *productExternalPushChromiumFixture, slug string) {
	t.Helper()
	var startsAt, endsAt time.Time
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT claim_starts_at,claim_ends_at FROM coupon_rules WHERE public_slug=$1`, slug).Scan(&startsAt, &endsAt); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if startsAt.After(now) || !endsAt.After(now) {
		t.Fatalf("coupon fixture must be inside its claim window now=%s starts_at=%s ends_at=%s", now.Format(time.RFC3339), startsAt.Format(time.RFC3339), endsAt.Format(time.RFC3339))
	}
}

func assertRemainingPagesRadarImageReceipt(t *testing.T, fixture *productExternalPushChromiumFixture, code string) {
	t.Helper()
	var receipts int
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM radar_events event JOIN radar_links link ON link.id=event.radar_id WHERE link.public_code=$1 AND event.stage='image_loaded'`, code).Scan(&receipts); err != nil || receipts < 1 {
		t.Fatalf("radar image-loaded local receipt count=%d err=%v", receipts, err)
	}
}

func seedRemainingPagesGridMember(t *testing.T, fixture *productExternalPushChromiumFixture) {
	t.Helper()
	now := time.Now().UTC().Add(-time.Minute)
	pool := fixture.application.pool.Native()
	var customerID int64
	if err := pool.QueryRow(fixture.ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(fixture.ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,activation_status,source,source_version,last_synced_at,updated_at) VALUES($1,'active','周期会员 Chromium 夹具','active','remaining-pages',1,$2,$2)`, customerID, now); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("remaining-pages-member-grid-entitlement"))
	if _, err := pool.Exec(fixture.ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('remaining-pages','member-grid-1',$1,$2,'浏览器周期外推商品','active',$3,$4,'Chromium public share fixture',$5,$3,$3)`, customerID, fixture.serviceProductID, now.Add(-24*time.Hour), now.AddDate(0, 0, 30), digest[:]); err != nil {
		t.Fatal(err)
	}
}

func enableRemainingPagesGridShare(t *testing.T, fixture *productExternalPushChromiumFixture, session, csrf string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, "/api/admin/service-period-products/"+strconv.FormatInt(fixture.serviceProductID, 10)+"/member-grid/external-share", strings.NewReader(`{"enabled":true,"version":0}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "remaining-pages-member-grid-share-0001")
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	fixture.application.handler.ServeHTTP(response, request)
	var payload struct {
		ExternalShare struct {
			URL string `json:"url"`
		} `json:"external_share"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || response.Code != http.StatusOK || !strings.HasPrefix(payload.ExternalShare.URL, "/shared/service-period-member-grid#mgshare1.") {
		t.Fatalf("enable member-grid share status=%d decode=%v body=%s", response.Code, err, response.Body.String())
	}
	return strings.TrimPrefix(payload.ExternalShare.URL, "/shared/service-period-member-grid#")
}

func remainingPagesChromiumRevision(t *testing.T) string {
	t.Helper()
	output, err := exec.Command("git", "rev-parse", "HEAD").Output()
	revision := strings.TrimSpace(string(output))
	if err != nil || len(revision) != 40 {
		t.Fatalf("resolve evaluated revision err=%v revision=%q", err, revision)
	}
	return revision
}
