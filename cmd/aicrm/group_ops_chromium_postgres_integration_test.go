package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID decision: not involved. The browser fixture uses only the authenticated
// local operator and opaque group-plan records. Persistence decision: the Host
// saves the node through the normal Group Ops PostgreSQL UoW; dispatch remains
// disabled and the journey submits no Provider write.
type groupOpsChromiumFixture struct {
	ctx                context.Context
	application        *composedApplication
	server             *httptest.Server
	script             string
	planID             int64
	ownerStaffID       int64
	replacementStaffID int64
	composerImageIDs   [2]int64
	radarImageID       int64
	radarAttachmentID  int64
	radarUploadPath    string
}

func TestPostgreSQLGroupOpsStandardHostCompositionPreflight(t *testing.T) {
	fixture := newGroupOpsChromiumFixture(t)
	session, _ := adminAccessLogin(t, fixture.application.handler, "groupops-browser-owner", "groupops-browser-owner-password")
	page := authenticatedAdminGet(t, fixture.application.handler, session, "/admin/automation-conversion/group-ops/plans/"+strconv.FormatInt(fixture.planID, 10))
	if page.Code != http.StatusOK || !bytes.Contains(page.Body.Bytes(), []byte(`data-group-ops-standard-host="true"`)) || !bytes.Contains(page.Body.Bytes(), []byte(`data-group-ops-standard-stage`)) || !bytes.Contains(page.Body.Bytes(), []byte(`<h1 class="admin-page-title">群运营计划</h1>`)) || !bytes.Contains(page.Body.Bytes(), []byte(`/groupops-assets/`)) || !bytes.Contains(page.Body.Bytes(), []byte(`/groupops-assets/assets/standard-components/operation_member_picker.js`)) {
		t.Fatalf("standard Group Ops page status=%d host=%t native_stage=%t topbar=%t assets=%t", page.Code, bytes.Contains(page.Body.Bytes(), []byte(`data-group-ops-standard-host="true"`)), bytes.Contains(page.Body.Bytes(), []byte(`data-group-ops-standard-stage`)), bytes.Contains(page.Body.Bytes(), []byte(`<h1 class="admin-page-title">群运营计划</h1>`)), bytes.Contains(page.Body.Bytes(), []byte(`/groupops-assets/`)))
	}
	detail := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/automation-conversion/group-ops/plans/"+strconv.FormatInt(fixture.planID, 10))
	if detail.Code != http.StatusOK || !bytes.Contains(detail.Body.Bytes(), []byte(`"plan"`)) || !bytes.Contains(detail.Body.Bytes(), []byte(`"plan_type":"standard"`)) {
		t.Fatalf("standard Group Ops detail status=%d plan=%t type=%t", detail.Code, bytes.Contains(detail.Body.Bytes(), []byte(`"plan"`)), bytes.Contains(detail.Body.Bytes(), []byte(`"plan_type":"standard"`)))
	}
	members := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/common/operation-members?scope=group_ops&page_size=100")
	if members.Code != http.StatusOK {
		t.Fatalf("Group Ops operation-members status=%d body=%s", members.Code, members.Body.String())
	}
	var memberPayload struct {
		Scope string `json:"scope"`
		Items []struct {
			StaffID      int64  `json:"staff_id"`
			SenderUserID string `json:"sender_userid"`
			DisplayName  string `json:"display_name"`
		} `json:"items"`
	}
	if err := json.NewDecoder(members.Body).Decode(&memberPayload); err != nil {
		t.Fatalf("decode Group Ops operation-members: %v", err)
	}
	seen := map[int64]string{}
	for _, member := range memberPayload.Items {
		seen[member.StaffID] = member.SenderUserID
	}
	if memberPayload.Scope != "group_ops" || len(memberPayload.Items) != 2 || seen[fixture.ownerStaffID] != "chromium-owner" || seen[fixture.replacementStaffID] != "chromium-replacement" {
		t.Fatalf("Group Ops eligible member projection scope=%q items=%+v", memberPayload.Scope, memberPayload.Items)
	}
}

func TestPostgreSQLGroupOpsStandardHostChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newGroupOpsChromiumFixture(t)
	command := exec.CommandContext(fixture.ctx, "node", fixture.script)
	command.Env = append(os.Environ(), "AICRM_GROUPOPS_TEST_URL="+fixture.server.URL, "AICRM_GROUPOPS_TEST_USERNAME=groupops-browser-owner", "AICRM_GROUPOPS_TEST_PASSWORD=groupops-browser-owner-password", "AICRM_GROUPOPS_TEST_PLAN_ID="+strconv.FormatInt(fixture.planID, 10), "AICRM_GROUPOPS_TEST_REPLACEMENT_STAFF_ID="+strconv.FormatInt(fixture.replacementStaffID, 10), "AICRM_GROUPOPS_TEST_COMPOSER_IMAGE_IDS="+strconv.FormatInt(fixture.composerImageIDs[0], 10)+","+strconv.FormatInt(fixture.composerImageIDs[1], 10), "AICRM_GROUPOPS_TEST_RADAR_IMAGE_ID="+strconv.FormatInt(fixture.radarImageID, 10), "AICRM_GROUPOPS_TEST_RADAR_ATTACHMENT_ID="+strconv.FormatInt(fixture.radarAttachmentID, 10), "AICRM_GROUPOPS_RADAR_UPLOAD="+fixture.radarUploadPath)
	output, err := command.CombinedOutput()
	if strings.Contains(string(output), "group_ops_chromium: SKIP_DEVTOOLS") {
		t.Fatalf("Group Ops Chromium DevTools unexpectedly unavailable: %s", strings.TrimSpace(string(output)))
	}
	if err != nil || !strings.Contains(string(output), "group_ops_chromium: PASS") {
		t.Fatalf("Group Ops Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	var matched int
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM group_ops_plan_nodes WHERE plan_id=$1 AND day_index=2 AND scheduled_time='09:30' AND trigger_time_label='09:30' AND action_title='Chromium 日程动作' AND node_status='active'`, fixture.planID).Scan(&matched); err != nil || matched != 1 {
		t.Fatalf("browser node persistence count=%d err=%v", matched, err)
	}
	var materialPlanRaw []byte
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT material_plan FROM group_ops_plan_nodes WHERE plan_id=$1 AND day_index=2 AND scheduled_time='09:30' AND action_title='Chromium 日程动作'`, fixture.planID).Scan(&materialPlanRaw); err != nil {
		t.Fatalf("read browser-persisted node material plan: %v", err)
	}
	var materialPlan struct {
		References []struct {
			Kind string `json:"kind"`
			ID   int64  `json:"id"`
		} `json:"references"`
	}
	if err = json.Unmarshal(materialPlanRaw, &materialPlan); err != nil {
		t.Fatalf("decode browser-persisted node material plan: %v", err)
	}
	if len(materialPlan.References) != 2 || materialPlan.References[0].Kind != "image" || materialPlan.References[0].ID != fixture.composerImageIDs[0] || materialPlan.References[1].Kind != "image" || materialPlan.References[1].ID != fixture.composerImageIDs[1] {
		t.Fatalf("browser composer did not persist its confirmed Media owner order: %+v", materialPlan.References)
	}
	var groupBindings int64
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM group_ops_plan_group_assets WHERE plan_id=$1 AND asset_reference IN ('chromium-group-1','chromium-group-2')`, fixture.planID).Scan(&groupBindings); err != nil || groupBindings != 2 {
		t.Fatalf("browser group selection persistence count=%d err=%v", groupBindings, err)
	}
	var ownerCount, ownerID int64
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*),coalesce(min(staff_id),0) FROM group_ops_plan_members WHERE plan_id=$1`, fixture.planID).Scan(&ownerCount, &ownerID); err != nil || ownerCount != 1 || ownerID != fixture.replacementStaffID {
		t.Fatalf("browser owner persistence count=%d owner=%d err=%v", ownerCount, ownerID, err)
	}
	var staleRadarImages int
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM radar_links WHERE title='Chromium V3 Radar material' AND content_type='image' AND media_id=$1`, fixture.radarImageID).Scan(&staleRadarImages); err != nil || staleRadarImages != 0 {
		t.Fatalf("browser Radar type switch retained stale image count=%d image=%d err=%v", staleRadarImages, fixture.radarImageID, err)
	}
	var radarPDFMaterials int
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM radar_links WHERE title='Chromium V3 Radar PDF material' AND content_type='pdf' AND media_id=$1`, fixture.radarAttachmentID).Scan(&radarPDFMaterials); err != nil || radarPDFMaterials != 1 {
		t.Fatalf("browser Radar PDF persistence count=%d attachment=%d err=%v", radarPDFMaterials, fixture.radarAttachmentID, err)
	}
}

func newGroupOpsChromiumFixture(t *testing.T) *groupOpsChromiumFixture {
	t.Helper()
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate Group Ops Chromium journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	prepareGroupOpsChromiumArtifacts(t, repository)
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: "https://" + server.Listener.Addr().String(), ReleaseSHA: "groupops-standard-host-chromium", WorkerOwner: "groupops-standard-host-chromium", WorkerLimit: 1,
		GroupOps:    platformconfig.GroupOps{WebhookSecret: "groupops-browser-webhook-secret"},
		Survey:      platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(dataKey), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey)},
		AIAssistant: platformconfig.AIAssistant{UIEnabled: true},
		Bootstrap:   platformconfig.Bootstrap{Enabled: true, Username: "groupops-browser-owner", Password: "groupops-browser-owner-password", DisplayName: "Group Ops Browser Owner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	bootstrap := platformconfig.Bootstrap{Enabled: true, Username: "groupops-browser-owner", Password: "groupops-browser-owner-password", DisplayName: "Group Ops Browser Owner"}
	if err = application.bootstrap(ctx, bootstrap); err != nil {
		t.Fatal(err)
	}
	var actorID, replacementStaffID, planID int64
	if err = application.pool.Native().QueryRow(ctx, `SELECT id FROM admin_users WHERE username='groupops-browser-owner'`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	// Group Ops can select only active Access users with a verified, valid WeCom
	// sender binding. Bootstrap creates the local operator without one, so make
	// the fixture represent the same authorized local state as production.
	if _, err = application.pool.Native().Exec(ctx, `UPDATE admin_users SET wecom_userid='chromium-owner' WHERE id=$1`, actorID); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, "WITH account AS (INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled,access_granted_at) VALUES($1,$2,$3,$4,true,true,clock_timestamp()) RETURNING id), role AS (INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM account) SELECT id FROM account", "groupops-browser-replacement", "$argon2id$browser-replacement", "Chromium Replacement", "chromium-replacement").Scan(&replacementStaffID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO group_ops_plans(name,status,revision,created_by,updated_by,created_at,updated_at,plan_type) VALUES('Chromium 群运营计划','draft',1,$1,$1,$2,$2,'standard') RETURNING id`, actorID, now).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO group_ops_plan_members(plan_id,staff_id) VALUES($1,$2)`, planID, actorID); err != nil {
		t.Fatal(err)
	}
	// Keep the target outside the first picker page. The Chromium journey must
	// prove a q-filtered Owner read can find it without preloading this directory.
	for index := 1; index <= 50; index++ {
		if _, err = application.pool.Native().Exec(ctx, `INSERT INTO group_ops_directory_groups(chat_reference,owner_staff_id,display_name,member_count,source_digest,refreshed_at,external_member_count) VALUES($1,$2,$3,1,$4,$5,0)`, "chromium-a-"+strconv.Itoa(index), actorID, "Chromium 目录填充", "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO group_ops_directory_groups(chat_reference,owner_staff_id,display_name,member_count,source_digest,refreshed_at,external_member_count) VALUES ('chromium-group-1',$1,'Chromium 群一',20,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',$2,12),('chromium-group-2',$1,'Chromium 群二',18,'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',$2,10),('chromium-group-3',$1,'Chromium 群三',16,'sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',$2,8)`, actorID, now); err != nil {
		t.Fatal(err)
	}
	media := seedGroupOpsChromiumImages(t, ctx, application)
	radarAttachmentID := seedGroupOpsRadarAttachment(t, ctx, application, media.radarImageID)
	radarUploadPath := seedGroupOpsRadarUploadFile(t)
	server.Config.Handler = application.handler
	server.StartTLS()
	return &groupOpsChromiumFixture{ctx: ctx, application: application, server: server, script: filepath.Join(filepath.Dir(source), "group_ops_chromium_journey.mjs"), planID: planID, ownerStaffID: actorID, replacementStaffID: replacementStaffID, composerImageIDs: media.composerImageIDs, radarImageID: media.radarImageID, radarAttachmentID: radarAttachmentID, radarUploadPath: radarUploadPath}
}

// seedGroupOpsChromiumImages uses the normal Media tables only. The browser
// consumes authenticated, page-scoped image-library reads; it never uploads or
// writes a Provider resource. The first two IDs are fixed fixture facts used
// to assert the node's persisted material_plan order after the real composer
// removes, reopens, and reorders its local draft.
type groupOpsChromiumMediaFixture struct {
	composerImageIDs [2]int64
	radarImageID     int64
}

func seedGroupOpsChromiumImages(t *testing.T, ctx context.Context, application *composedApplication) groupOpsChromiumMediaFixture {
	t.Helper()
	var fixture groupOpsChromiumMediaFixture
	// A complete, visibly colored PNG makes the page-scoped thumbnail endpoint
	// part of the browser journey; a signature-only byte slice renders broken.
	canvas := image.NewRGBA(image.Rect(0, 0, 160, 90))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{37, 99, 235, 255}), image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(0, 60, 160, 90), image.NewUniform(color.RGBA{15, 118, 110, 255}), image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	content := encoded.Bytes()
	digestValue := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(digestValue[:])
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'image/png',$2,$3)`, digest, len(content), content); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"Chromium 群运营素材一", "Chromium 群运营素材二", "Chromium 雷达素材一", "Chromium 雷达素材二", "Chromium 雷达素材三"} {
		var imageID int64
		if err := application.pool.Native().QueryRow(ctx, `INSERT INTO media_images(blob_digest,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_by,updated_by) VALUES($1,$2,$3,'真实素材选择验收','chromium,groupops','chromium-groupops','image/png',$4,160,90,true,1,1) RETURNING id`, digest, "chromium-groupops-"+strconv.Itoa(index+1)+".png", name, len(content)).Scan(&imageID); err != nil {
			t.Fatal(err)
		}
		if index < len(fixture.composerImageIDs) {
			fixture.composerImageIDs[index] = imageID
		}
		if index == 2 {
			fixture.radarImageID = imageID
		}
	}
	if fixture.radarImageID < 1 {
		t.Fatal("Group Ops Chromium fixture did not create its Radar image")
	}
	return fixture
}

// seedGroupOpsRadarAttachment deliberately uses the same numeric ID as the
// image selected by the real Radar journey. Image and attachment IDs belong to
// different Media owners; switching the form type must clear the image instead
// of treating the equally numbered PDF as if it were the prior selection.
func seedGroupOpsRadarAttachment(t *testing.T, ctx context.Context, application *composedApplication, imageID int64) int64 {
	t.Helper()
	content := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
	digestValue := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(digestValue[:])
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'application/pdf',$2,$3)`, digest, len(content), content); err != nil {
		t.Fatal(err)
	}
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO media_attachments(id,blob_digest,file_name,name,description,tags,mime_type,byte_size,enabled,created_by,updated_by) OVERRIDING SYSTEM VALUE VALUES($1,$2,'chromium-radar-material.pdf','Chromium 雷达 PDF 素材','与同号图片验证类型边界','[]'::jsonb,'application/pdf',$3,true,1,1)`, imageID, digest, len(content)); err != nil {
		t.Fatal(err)
	}
	if _, err := application.pool.Native().Exec(ctx, `SELECT setval(pg_get_serial_sequence('media_attachments','id'), (SELECT max(id) FROM media_attachments), true)`); err != nil {
		t.Fatal(err)
	}
	return imageID
}

// seedGroupOpsRadarUploadFile creates one native image input for the actual
// Radar form. The browser uses its original upload action, then verifies the
// V3 picker reopens from that same caller draft before choosing a catalog item.
func seedGroupOpsRadarUploadFile(t *testing.T) string {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 3, 2))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{217, 70, 239, 255}), image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "chromium-radar-current-upload.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Group Ops runs against the same already-staged release closure as CI. The
// fixture must not rebuild or replace shared web/dist: packages run in
// parallel and other browser tests read the private PR01 documents there.
func prepareGroupOpsChromiumArtifacts(t *testing.T, repository string) {
	t.Helper()
	for _, relative := range []string{"asset-manifest.json"} {
		if _, err := os.Stat(filepath.Join(repository, "web", "dist", relative)); err != nil {
			t.Fatalf("Group Ops Chromium requires staged release artifact %s: %v", relative, err)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(repository, "web", "dist", "asset-manifest.json"))
	if err != nil || !bytes.Contains(manifest, []byte("\"groupopsHost\"")) || !bytes.Contains(manifest, []byte("\"groupopsStyles\"")) {
		t.Fatalf("Group Ops Chromium release manifest lacks Host closure: %v", err)
	}
}
