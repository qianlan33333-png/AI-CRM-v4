package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	adminops "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

// Required stage-only acceptance. The existing fixture verifies the supplied
// installed binary/Host SHA and digest, isolates PostgreSQL, authenticates a
// disposable administrator, and rejects all real Provider traffic. Setup and
// merge/revert are synthetic fixtures; every tested business HTTP operation
// reaches the immutable installed app process, not a source HTTP handler.
func TestPriorityHealthInstalledContracts(t *testing.T) {
	if testconfig.InstalledSurvey().Binary == "" {
		t.Fatal("required installed-binary acceptance needs AICRM_INSTALLED_SURVEY_* inputs")
	}
	f := newPublicSurveyPresentationChromiumFixture(t)
	client := f.server.Client()
	call := func(method, path string, body any, cookies []*http.Cookie, key string, want int) []byte {
		t.Helper()
		var raw []byte
		if body != nil {
			var err error
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequestWithContext(f.ctx, method, f.server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Idempotency-Key", key)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
			if cookie.Name == "aicrm_admin_csrf" {
				req.Header.Set("X-CSRF-Token", cookie.Value)
			}
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err = io.ReadAll(io.LimitReader(res.Body, 2<<20))
		if err != nil || res.StatusCode != want {
			t.Fatalf("installed business HTTP status: method=%s path=%s status=%d want=%d err=%v", method, path, res.StatusCode, want, err)
		}
		return raw
	}

	wrapped, err := platformpostgres.Wrap(f.native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	var original, survivor customerdomain.CustomerID
	digest := sha256.Sum256([]byte(f.session))
	if err = f.native.QueryRow(f.ctx, `SELECT customer_id FROM survey_identity_sessions WHERE session_digest=$1`, digest[:]).Scan(&original); err != nil {
		t.Fatal(err)
	}
	fact := evidenceMergeFact(t, identitydomain.KindFirstPartyMemberID, "first-party:installed-health", "synthetic-installed-survivor")
	var merge identityapp.LinkResult
	if err = uow.Within(f.ctx, func(tx context.Context) error {
		provisioned, e := oneID.ProvisionCustomerFromVerifiedIdentity(tx, fact)
		if e != nil {
			return e
		}
		survivor = provisioned.CustomerID
		candidate, e := oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: original, Target: fact, Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "installed.health.fixture", EventID: "synthetic-installed-merge", Digest: "synthetic-installed-evidence", PolicyVersion: "test-v1"}})
		if e != nil || candidate.Candidate == nil {
			if e == nil {
				t.Fatal("expected a real fixture merge candidate")
			}
			return e
		}
		merge, e = oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivor, Operator: "synthetic-installed-reviewer"})
		return e
	}); err != nil || merge.Merge == nil {
		t.Fatalf("prepare merge fixture: present=%t err=%v", merge.Merge != nil, err)
	}
	publicCookies := []*http.Cookie{{Name: "__Host-aicrm_survey_identity", Value: f.session}}
	answers := []surveyport.SubmissionAnswer{{QuestionID: f.success.Questions[0].ID, OptionIDs: []surveyport.ID{f.success.Questions[0].Options[0].ID}}}
	command := map[string]any{"version": f.success.DefinitionVersion, "submission_key": strings.Repeat("h", 43), "answers": answers}
	path := "/api/public/questionnaires/" + f.success.Slug + "/submissions"
	firstRaw := call(http.MethodPost, path, command, publicCookies, "", 201)
	var first struct {
		Receipt surveyport.SubmissionReceipt `json:"receipt"`
		Token   string                       `json:"result_token"`
	}
	if json.Unmarshal(firstRaw, &first) != nil || first.Receipt.SubmissionID < 1 || first.Token == "" {
		t.Fatal("installed submit returned no complete receipt")
	}
	before := snapshotSurveyClosureFacts(t, f.ctx, f.native, f.success.ID, first.Receipt.SubmissionID)
	if err = uow.Within(f.ctx, func(tx context.Context) error { _, e := oneID.RevertConfirmedMerge(tx, merge.Merge.ID); return e }); err != nil {
		t.Fatal(err)
	}
	if replay := call(http.MethodPost, path, command, publicCookies, "", 201); !bytes.Equal(replay, firstRaw) {
		t.Fatal("installed merge/revert replay did not return the original complete receipt")
	}
	call(http.MethodPost, "/api/public/survey-submission-results/query", map[string]string{"result_token": first.Token}, publicCookies, "", 403)
	command["answers"] = []surveyport.SubmissionAnswer{{QuestionID: f.success.Questions[0].ID, OptionIDs: []surveyport.ID{f.success.Questions[0].Options[1].ID}}}
	call(http.MethodPost, path, command, publicCookies, "", 409)
	assertSurveyClosureOwner(t, f.ctx, f.native, f.success.ID, first.Receipt.SubmissionID, survivor)
	assertSurveyClosureFactsUnchanged(t, f.ctx, f.native, f.success.ID, first.Receipt.SubmissionID, before)
	if bytes.Contains(firstRaw, []byte("provider_identity_id")) || bytes.Contains(firstRaw, []byte("ProviderIdentityID")) {
		t.Fatal("installed API leaked private submitter handle")
	}
	t.Log("survey-successful-key-recovery: PASS exact_receipt historical_owner_retained current_result_forbidden payload_drift_rejected")

	staffID := newPriorityHealthSenderFixture(t, f.ctx, uow)
	created := call(http.MethodPost, "/api/admin/ai-audience/packages", map[string]string{"name": "Installed sender replay", "template_key": "active_contacts"}, f.adminCookies, "installed-health-package-001", 201)
	var pkg struct {
		Package segmentdomain.Package `json:"package"`
	}
	if json.Unmarshal(created, &pkg) != nil || pkg.Package.ID < 1 {
		t.Fatal("installed audience package was not created")
	}
	senderPath := "/api/admin/ai-audience/packages/" + strconv.FormatInt(pkg.Package.ID, 10) + "/senders"
	senderCommand := map[string]any{"expected_version": pkg.Package.Version, "provider_member_references": []string{"synthetic-installed-health-sender"}}
	originalSenders := call(http.MethodPut, senderPath, senderCommand, f.adminCookies, "installed-health-senders-001", 200)
	if _, err = f.native.Exec(f.ctx, `UPDATE admin_users SET is_active=FALSE,session_version=session_version+1,updated_at=clock_timestamp() WHERE id=$1`, staffID); err != nil {
		t.Fatal(err)
	}
	if replay := call(http.MethodPut, senderPath, senderCommand, f.adminCookies, "installed-health-senders-001", 200); !bytes.Equal(replay, originalSenders) {
		t.Fatal("installed directory refresh/disable changed an accepted sender result")
	}
	senderCommand["expected_version"] = pkg.Package.Version + 1
	call(http.MethodPut, senderPath, senderCommand, f.adminCookies, "installed-health-senders-001", 409)
	call(http.MethodPut, senderPath, senderCommand, f.adminCookies, "installed-health-senders-new-key", 503)
	call(http.MethodPut, senderPath, senderCommand, nil, "installed-health-senders-001", 401)
	var sets, receipts int
	if err = f.native.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM segment_audience_sender_sets),(SELECT count(*) FROM segment_audience_operation_receipts WHERE operation='replace_senders')`).Scan(&sets, &receipts); err != nil || sets != 1 || receipts != 1 {
		t.Fatalf("installed retry duplicated saved facts: sets=%d receipts=%d err=%v", sets, receipts, err)
	}
	t.Log("sender-idempotency-replay: PASS original_result_after_disable new_key_rejected drift_and_unauthenticated_rejected accepted_once")

	raw := call(http.MethodGet, "/api/admin/ops-retention/resources", nil, f.adminCookies, "", 200)
	var coverage adminops.RetentionCoverage
	if err = json.Unmarshal(raw, &coverage); err != nil {
		t.Fatal(err)
	}
	items := map[string]adminops.RetentionResource{}
	for _, item := range coverage.Items {
		items[item.Kind+":"+item.Name] = item
	}
	for _, name := range []string{"customer_contact_profiles", "invoice_applications", "invoice_audit_events", "invoice_callback_receipts", "invoice_card_setup_intents", "invoice_operation_receipts", "media_private_images", "order_external_history_facts", "order_wechat_shop_facts", "order_wechat_shop_lines", "product_variants", "product_wechat_shop_skus", "survey_redemption_codes", "survey_submission_attachments", "wechat_shop_callback_receipts", "wechat_shop_order_progress", "wechat_shop_product_refresh_items", "wechat_shop_product_refreshes"} {
		item := items["table:"+name]
		if item.Policy != "permanent" || item.CoverageStatus != "protected" || item.CleanupEntrypoint != "" || item.PolicyID != "" {
			t.Fatalf("installed catalog lost protection for %s", name)
		}
	}
	keyRoot := items["filesystem_prefix:/var/lib/aicrm/invoice-materials"]
	if keyRoot.Owner != "config" || keyRoot.Policy != "never_delete_by_retention" || keyRoot.CoverageStatus != "protected" {
		t.Fatal("installed catalog lost private material protection")
	}
	call(http.MethodGet, "/api/admin/ops-retention/preview?policy=invoice_applications", nil, f.adminCookies, "", 400)
	call(http.MethodGet, "/api/admin/ops-retention/resources", nil, nil, "", 403)
	if !reflect.DeepEqual(snapshotSurveyClosureFacts(t, f.ctx, f.native, f.success.ID, first.Receipt.SubmissionID), before) {
		t.Fatal("read-only installed coverage checks changed questionnaire business facts")
	}
	t.Log("retention-business-protection: PASS 18_tables_and_private_material_protected arbitrary_policy_and_unauthenticated_denied")
}

// Employee and its sole governed role must commit together. The fixture
// grants no login access, never modifies the existing super-administrator,
// and preserves the production deferred Access governance constraints.
func newPriorityHealthSenderFixture(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork) int64 {
	t.Helper()
	var id int64
	err := uow.Within(ctx, func(tx context.Context) error {
		db, e := platformpostgres.RequireTransaction(tx)
		if e != nil {
			return e
		}
		if e = db.QueryRow(tx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('installed-health-sender','$argon2id$test','Synthetic sender','synthetic-installed-health-sender',TRUE,FALSE) RETURNING id`).Scan(&id); e != nil {
			return e
		}
		_, e = db.Exec(tx, `INSERT INTO admin_user_roles(admin_user_id,role_code) VALUES($1,'viewer')`, id)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
