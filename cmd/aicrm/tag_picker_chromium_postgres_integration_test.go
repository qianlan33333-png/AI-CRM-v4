package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLTagPickerChromiumJourney uses the actual Access login and the
// three shipped V3 tag callers—customer filtering, ordinary-product form draft
// and Channel Center entry-tag form draft—plus the Channel Center's scoped
// staff-add seam. OneID is not involved: these dialogs only change existing
// local page drafts. Persistence remains with the original product/channel
// save commands; selection issues no tag CRUD, customer-tag command, Provider
// read, or Provider write.
func TestPostgreSQLTagPickerChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newTagPickerChromiumFixture(t)
	screenshotDir := t.TempDir()
	if configured := platformconfig.TagPickerScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatal("AICRM_TAG_PICKER_SCREENSHOT_DIR must be absolute")
		}
		if err := os.MkdirAll(configured, 0o700); err != nil {
			t.Fatalf("create tag picker screenshot directory: %v", err)
		}
		screenshotDir = configured
	}
	command := exec.CommandContext(fixture.ctx, "node", fixture.script)
	command.Env = append(os.Environ(),
		"AICRM_TAG_PICKER_TEST_URL="+fixture.server.URL,
		"AICRM_TAG_PICKER_TEST_USERNAME=tag-picker-browser-owner",
		"AICRM_TAG_PICKER_TEST_PASSWORD=tag-picker-browser-owner-password",
		"AICRM_TAG_PICKER_TEST_PRODUCT_ID="+strconv.FormatInt(fixture.productID, 10),
		"AICRM_TAG_PICKER_TEST_CHANNEL_ID="+strconv.FormatInt(fixture.channelID, 10),
		"AICRM_TAG_PICKER_TEST_TAG_ID="+strconv.FormatInt(fixture.tagID, 10),
		"AICRM_TAG_PICKER_TEST_CHANNEL_STAFF_ID="+strconv.FormatInt(fixture.channelStaffID, 10),
		"AICRM_TAG_PICKER_SCREENSHOT_DIR="+screenshotDir,
	)
	output, err := command.CombinedOutput()
	if strings.Contains(string(output), "tag_picker_chromium: SKIP_DEVTOOLS") {
		t.Fatalf("Tag picker Chromium DevTools unexpectedly unavailable: %s", strings.TrimSpace(string(output)))
	}
	if err != nil || !strings.Contains(string(output), "tag_picker_chromium: PASS") {
		t.Fatalf("Tag picker Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}

	var productProjection []byte
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT legacy_admin_projection FROM products WHERE id=$1`, fixture.productID).Scan(&productProjection); err != nil {
		t.Fatal(err)
	}
	var product struct {
		WeComTagging struct {
			Enabled bool    `json:"enabled"`
			TagIDs  []int64 `json:"tag_ids"`
		} `json:"wecom_tagging"`
	}
	if err = json.Unmarshal(productProjection, &product); err != nil || !product.WeComTagging.Enabled || len(product.WeComTagging.TagIDs) != 1 || product.WeComTagging.TagIDs[0] != fixture.tagID {
		t.Fatalf("browser product tag persistence projection=%s parsed=%+v err=%v", productProjection, product.WeComTagging, err)
	}

	var entryTagID int64
	var entryTagName, entryTagGroup string
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT entry_tag_id,entry_tag_name,entry_tag_group_name FROM channel_config_versions WHERE channel_id=$1 AND config_version=(SELECT current_config_version FROM channels WHERE id=$1)`, fixture.channelID).Scan(&entryTagID, &entryTagName, &entryTagGroup); err != nil {
		t.Fatal(err)
	}
	if entryTagID != fixture.tagID || entryTagName != "Chromium 标签" || entryTagGroup != "Chromium 新客" {
		t.Fatalf("browser channel tag persistence id=%d name=%q group=%q", entryTagID, entryTagName, entryTagGroup)
	}
	var selectedStaff int
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM channel_assignees WHERE channel_id=$1 AND config_version=(SELECT current_config_version FROM channels WHERE id=$1) AND staff_id=$2`, fixture.channelID, fixture.channelStaffID).Scan(&selectedStaff); err != nil || selectedStaff != 1 {
		t.Fatalf("browser channel staff selection count=%d staff=%d err=%v", selectedStaff, fixture.channelStaffID, err)
	}
}

type tagPickerChromiumFixture struct {
	ctx            context.Context
	application    *composedApplication
	server         *httptest.Server
	script         string
	productID      int64
	channelID      int64
	tagID          int64
	channelStaffID int64
}

func newTagPickerChromiumFixture(t *testing.T) *tagPickerChromiumFixture {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate tag picker Chromium journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	prepareTagPickerChromiumArtifacts(t, repository)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: "https://" + server.Listener.Addr().String(), ReleaseSHA: "tag-picker-chromium-journey", WorkerOwner: "tag-picker-chromium-journey", WorkerLimit: 1,
		GroupOps:    platformconfig.GroupOps{WebhookSecret: "tag-picker-chromium-webhook-secret"},
		Survey:      platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(dataKey), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey)},
		AIAssistant: platformconfig.AIAssistant{UIEnabled: true},
		Bootstrap:   platformconfig.Bootstrap{Enabled: true, Username: "tag-picker-browser-owner", Password: "tag-picker-browser-owner-password", DisplayName: "Tag Picker Browser Owner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	bootstrap := platformconfig.Bootstrap{Enabled: true, Username: "tag-picker-browser-owner", Password: "tag-picker-browser-owner-password", DisplayName: "Tag Picker Browser Owner"}
	if err = application.bootstrap(ctx, bootstrap); err != nil {
		t.Fatal(err)
	}
	productID, channelID, tagID, channelStaffID := seedTagPickerChromiumFixture(t, ctx, application)
	server.Config.Handler = application.handler
	server.StartTLS()
	return &tagPickerChromiumFixture{ctx: ctx, application: application, server: server, script: filepath.Join(filepath.Dir(source), "tag_picker_chromium_journey.mjs"), productID: productID, channelID: channelID, tagID: tagID, channelStaffID: channelStaffID}
}

func prepareTagPickerChromiumArtifacts(t *testing.T, repository string) {
	t.Helper()
	manifest, err := os.ReadFile(filepath.Join(repository, "web", "dist", "asset-manifest.json"))
	if err != nil {
		t.Fatalf("tag picker Chromium requires staged release manifest: %v", err)
	}
	for _, name := range []string{"customerHost", "productHost", "channelCenterHost", "standardComponentsHost", "selectionDialogStyles"} {
		if !strings.Contains(string(manifest), `"`+name+`"`) {
			t.Fatalf("tag picker Chromium release manifest lacks %s", name)
		}
	}
}

func seedTagPickerChromiumFixture(t *testing.T, ctx context.Context, application *composedApplication) (productID, channelID, tagID, channelStaffID int64) {
	t.Helper()
	pool := application.pool.Native()
	var adminID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM admin_users WHERE username='tag-picker-browser-owner'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	// Access governance requires every account and its single role to enter in
	// one transaction.  This fixture staff is a directory row, not a second
	// authenticated actor or super administrator.
	if err := pool.QueryRow(ctx, `WITH account AS (
		INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled,access_granted_at)
		VALUES('tag-picker-channel-staff','$argon2id$fixture','Chromium 渠道客服','chromium-channel-staff',true,true,clock_timestamp())
		RETURNING id
	), role AS (
		INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM account
	) SELECT id FROM account`).Scan(&channelStaffID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active');
		INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,last_synced_at,updated_at) VALUES(1,'active','Chromium 标签客户','customer #1','active','tag_picker_chromium',clock_timestamp(),clock_timestamp());
		INSERT INTO tag_groups(group_name,sort_order) VALUES('Chromium 新客',0),('Chromium 复购',1);`); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES((SELECT id FROM tag_groups WHERE group_name='Chromium 新客'),'Chromium 标签',0) RETURNING id`).Scan(&tagID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES
		((SELECT id FROM tag_groups WHERE group_name='Chromium 新客'),'Chromium 初访',1),
		((SELECT id FROM tag_groups WHERE group_name='Chromium 复购'),'Chromium 活跃',0),
		((SELECT id FROM tag_groups WHERE group_name='Chromium 复购'),'Chromium 沉默',1)`); err != nil {
		t.Fatal(err)
	}
	projection := `{"schema_version":1,"status":"enabled","enabled":true,"buy_button_text":"立即购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`
	if err := pool.QueryRow(ctx, `INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('tag-picker-chromium-product','Chromium 标签商品','真实标签选择器浏览器夹具',9900,'CNY',10,$1,$2::jsonb) RETURNING id`, adminID, projection).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// channels -> config_versions has a deferred circular foreign key. Seed the
	// same one-transaction shape as the Channel Owner rather than leaving an
	// impossible standalone channel row between statements.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = tx.QueryRow(ctx, `INSERT INTO channels(code,status,current_config_version,version,created_at,updated_at) VALUES('tag-picker-chromium-channel','active',1,1,$1,$1) RETURNING id`, now).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("tag-picker-chromium-channel-config"))
	if _, err = tx.Exec(ctx, `INSERT INTO channel_config_versions(channel_id,config_version,channel_type,carrier_type,name,scene_value,qrcode_url,customer_channel,link_url,final_url,welcome_message,welcome_image_ids,welcome_miniprogram_ids,welcome_attachment_ids,welcome_group_invite_ids,auto_accept_friend,entry_tag_id,entry_tag_name,entry_tag_group_name,assignment_mode,assignment_strategy,overflow_policy,config_digest,created_by,created_at)
		VALUES($1,1,'qrcode','qrcode','Chromium 标签渠道','','','','','','','{}','{}','{}','{}',false,NULL,'','', 'single_owner','ratio','',$2,$3,$4)`, channelID, digest[:], adminID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO channel_assignees(channel_id,config_version,staff_id,priority,ratio_percent,max_scans_24h,created_at) VALUES($1,1,$2,1,100,NULL,$3)`, channelID, adminID, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return productID, channelID, tagID, channelStaffID
}
