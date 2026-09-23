package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

// TestPostgreSQLSidebarBootstrapUsesBoundedSurveyReadAndActualTotal proves the
// outer Composition route accepts an authenticated, already-bound sidebar
// viewer even when Survey has more entries than its profile window permits.
// OneID is used only to resolve the existing scoped external identity. The
// journey performs no Provider write or customer provisioning.
func TestPostgreSQLSidebarBootstrapUsesBoundedSurveyReadAndActualTotal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	provider := newCustomerTagChromiumProvider()
	defer provider.Close()
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://sidebar-bootstrap.example.test",
		ReleaseSHA:   "sidebar-bootstrap-survey-total",
		WorkerOwner:  "sidebar-bootstrap-survey-total",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "sidebar-bootstrap-survey-total-webhook-secret"},
		Survey: platformconfig.Survey{
			DataKey:              base64.RawStdEncoding.EncodeToString(dataKey),
			IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey),
		},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "sidebar-survey-owner", Password: "sidebar-survey-owner-password", DisplayName: "Sidebar Survey Owner"},
		Effects:   platformconfig.Effects{ProviderEnabled: false},
		WeCom:     platformconfig.WeCom{Enabled: true, CorpID: "fixture-corp", AgentID: "fixture-agent", Secret: "fixture-secret", ContactSecret: "fixture-contact-secret", ContextSigningKey: "sidebar-bootstrap-context-key-32", APIBase: provider.URL(), HTTPClient: provider.Client()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "sidebar-survey-owner", Password: "sidebar-survey-owner-password", DisplayName: "Sidebar Survey Owner"}); err != nil {
		t.Fatal(err)
	}
	if err = seedSidebarThumbnailChromiumJourney(ctx, application); err != nil {
		t.Fatal(err)
	}
	if err = seedSidebarBootstrapSurveySubmissions(ctx, application); err != nil {
		t.Fatal(err)
	}
	// Exercise every projection independently with the same signed context
	// bootstrap will mint. If the aggregate bootstrap fails, this keeps the
	// integration failure attached to its owning Port rather than reducing it
	// to the intentionally non-sensitive section_unavailable response.
	contextToken, err := (wecom.ContextTokenService{CorpID: "fixture-corp", SigningKey: []byte("sidebar-bootstrap-context-key-32")}).Issue(ctx, wecom.SidebarPrincipal{CorpID: "fixture-corp", EmployeeID: "fixture-staff"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the normal Customer-owned annotation CAS through the Sidebar
	// adapter. This creates no identity and makes the bootstrap profile's
	// version/assurance contract observable from the real Composition root.
	if _, err = application.pool.Native().Exec(ctx, `UPDATE customer_directory_projection SET phone_masked='138****5678',phone_assurance='declared',contact_type=1 WHERE customer_id=1`); err != nil {
		t.Fatal(err)
	}
	profileUpdate := httptest.NewRequest(http.MethodPut, "/api/sidebar/v2/profile", strings.NewReader(`{"source":"sidebar-fixture","industry":"education","industry_description":"profile contract","needs_blockers_followup":"follow up","expected_profile_version":0}`))
	profileUpdate.Header.Set("Content-Type", "application/json")
	profileUpdate.Header.Set("X-Sidebar-Context-Token", contextToken)
	profileUpdate.Header.Set("Idempotency-Key", "sidebar-profile-annotations-0001")
	profileUpdateResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(profileUpdateResponse, profileUpdate)
	if profileUpdateResponse.Code != http.StatusOK {
		t.Fatalf("sidebar profile first annotation status=%d body=%s", profileUpdateResponse.Code, profileUpdateResponse.Body.String())
	}
	staleProfileUpdate := httptest.NewRequest(http.MethodPut, "/api/sidebar/v2/profile", strings.NewReader(`{"source":"must-not-overwrite","expected_profile_version":0}`))
	staleProfileUpdate.Header.Set("Content-Type", "application/json")
	staleProfileUpdate.Header.Set("X-Sidebar-Context-Token", contextToken)
	staleProfileUpdate.Header.Set("Idempotency-Key", "sidebar-profile-annotations-0002")
	staleProfileUpdateResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(staleProfileUpdateResponse, staleProfileUpdate)
	if staleProfileUpdateResponse.Code != http.StatusConflict {
		t.Fatalf("sidebar profile stale annotation status=%d body=%s", staleProfileUpdateResponse.Code, staleProfileUpdateResponse.Body.String())
	}
	for _, path := range []string{"/api/sidebar/v2/workbench", "/api/sidebar/v2/questionnaires", "/api/sidebar/v2/orders", "/api/sidebar/v2/periodic-orders", "/api/sidebar/v2/materials"} {
		sectionRequest := httptest.NewRequest(http.MethodGet, path, nil)
		sectionRequest.Header.Set("X-Sidebar-Context-Token", contextToken)
		sectionResponse := httptest.NewRecorder()
		application.handler.ServeHTTP(sectionResponse, sectionRequest)
		if sectionResponse.Code != http.StatusOK {
			t.Fatalf("sidebar section %s status=%d body=%s", path, sectionResponse.Code, sectionResponse.Body.String())
		}
	}
	readQuestionnairePage := func(path string) struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Total      int64  `json:"total"`
		Limit      int    `json:"limit"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-Sidebar-Context-Token", contextToken)
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("sidebar questionnaire page path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		var page struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
			Total      int64  `json:"total"`
			Limit      int    `json:"limit"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	firstPage := readQuestionnairePage("/api/sidebar/v2/questionnaires?limit=50")
	secondPage := readQuestionnairePage("/api/sidebar/v2/questionnaires?limit=50&cursor=" + firstPage.NextCursor)
	thirdPage := readQuestionnairePage("/api/sidebar/v2/questionnaires?limit=50&cursor=" + secondPage.NextCursor)
	seen := map[int64]bool{}
	for _, items := range [][]struct {
		ID int64 `json:"id"`
	}{firstPage.Items, secondPage.Items, thirdPage.Items} {
		for _, item := range items {
			if seen[item.ID] {
				t.Fatalf("sidebar questionnaire cursor repeated submission id=%d", item.ID)
			}
			seen[item.ID] = true
		}
	}
	if firstPage.Total != 102 || firstPage.Limit != 50 || len(firstPage.Items) != 50 || !firstPage.HasMore || firstPage.NextCursor == "" || len(secondPage.Items) != 50 || !secondPage.HasMore || secondPage.NextCursor == "" || len(thirdPage.Items) != 2 || thirdPage.HasMore || thirdPage.NextCursor != "" || len(seen) != 102 {
		t.Fatalf("sidebar questionnaire cursor pages first=%+v second=%+v third=%+v unique=%d", firstPage, secondPage, thirdPage, len(seen))
	}

	issued, err := application.authentication.LoginWithWeComUserID(ctx, accessapp.WeComLoginCommand{WeComUserID: "fixture-staff", Remote: "sidebar-bootstrap-survey-total"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/sidebar/v2/bootstrap", strings.NewReader(`{"external_userid":"sidebar-thumbnail-external"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "aicrm_sidebar_session", Value: issued.SessionToken})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("sidebar bootstrap status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		State     string `json:"state"`
		Workbench struct {
			QuestionnaireCount int64 `json:"questionnaire_count"`
			Profile            struct {
				PhoneAssurance        string `json:"phone_assurance"`
				ContactType           int16  `json:"contact_type"`
				ProfileSource         string `json:"profile_source"`
				ProfileVersion        int64  `json:"profile_version"`
				Industry              string `json:"industry"`
				IndustryDescription   string `json:"industry_description"`
				NeedsBlockersFollowup string `json:"needs_blockers_followup"`
			} `json:"profile"`
		} `json:"workbench"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.State != "ready" || body.Workbench.QuestionnaireCount != 102 || body.Workbench.Profile.PhoneAssurance != "declared" || body.Workbench.Profile.ContactType != 1 || body.Workbench.Profile.ProfileSource != "sidebar-fixture" || body.Workbench.Profile.ProfileVersion != 1 || body.Workbench.Profile.Industry != "education" || body.Workbench.Profile.IndustryDescription != "profile contract" || body.Workbench.Profile.NeedsBlockersFollowup != "follow up" {
		t.Fatalf("sidebar bootstrap=%+v", body)
	}
	writes, reads := provider.Counts()
	if writes != 0 || reads != 0 {
		t.Fatalf("sidebar bootstrap must not call WeCom provider: writes=%d reads=%d", writes, reads)
	}
}

func seedSidebarBootstrapSurveySubmissions(ctx context.Context, application *composedApplication) error {
	pool := application.pool.Native()
	now := time.Now().UTC().Add(-time.Minute)
	var questionnaireID, versionID int64
	if err := pool.QueryRow(ctx, `INSERT INTO survey_questionnaires(
		name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at
	) VALUES(
		'sidebar-bootstrap-count','Sidebar bootstrap count','','survey','all_in_one','sidebar-bootstrap-count','published',1,1,$1,$1
	) RETURNING id`, now).Scan(&questionnaireID); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `INSERT INTO survey_definition_versions(
		questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at
	) VALUES($1,1,'survey','all_in_one','Sidebar bootstrap count','', '{}'::jsonb,decode(repeat('5a',32),'hex'),true,$2,1,$2) RETURNING id`, questionnaireID, now).Scan(&versionID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$2 WHERE id=$1`, questionnaireID, versionID); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, `INSERT INTO survey_submissions(
		questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,
		submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,
		result_snapshot,submitted_at,created_at
	) SELECT $1,$2,1,1,'resolved',
		decode(lpad(to_hex(series),64,'0'),'hex'),decode(lpad(to_hex(series + 1024),64,'0'),'hex'),
		'sidebar-bootstrap-count','Sidebar bootstrap count','survey','{}'::jsonb,$3::timestamptz - make_interval(secs => series),$3::timestamptz
	FROM generate_series(1,102) AS series`, questionnaireID, versionID, now)
	return err
}
