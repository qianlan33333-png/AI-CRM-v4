package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	surveydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/domain"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

const editorProductionCreatePayload = `{"name":"测试新能力","title":"123新能力","description":"123","answer_display_mode":"one_by_one","assessment_enabled":false,"assessment_config":{},"slug":"","is_disabled":false,"questions":[{"type":"shipping_address","title":"收货人","sidebar_profile_field":"","required":false,"sort_order":0,"placeholder_text":""},{"type":"redemption_code","title":"兑换码","sidebar_profile_field":"","required":true,"sort_order":1,"placeholder_text":""},{"type":"attachment","title":"图片","sidebar_profile_field":"","required":true,"sort_order":2,"placeholder_text":""}],"score_rules":[]}`

func TestEditorProductionCreatePayloadOnlyNeedsAssignedSlug(t *testing.T) {
	var body definitionRequest
	request := httptest.NewRequest("POST", "/api/admin/questionnaires", strings.NewReader(editorProductionCreatePayload))
	if err := decode(request, &body); err != nil {
		t.Fatalf("production DTO decoding: %v", err)
	}
	q := body.questionnaire()
	if q.Slug != "" || surveydomain.ValidateQuestionnaire(q) == nil {
		t.Fatal("expected unassigned public identifier")
	}
	q.Slug = "q-reproduction-only"
	if err := surveydomain.ValidateQuestionnaire(q); err != nil {
		t.Fatalf("same production shape with only slug assigned: %v", err)
	}
}

func TestEditorCreateKeepsAuthenticationCSRFAndStrictDTOBoundary(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		security RequestSecurity
		payload  string
		status   int
	}{
		{"anonymous", editorCreateUnauthenticated{}, editorProductionCreatePayload, 401},
		{"viewer", principalSecurity{principal: accessdomain.Principal{InternalID: 8, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}, editorProductionCreatePayload, 403},
		{"csrf", operationSecurity{csrfErr: errors.New("rejected csrf")}, editorProductionCreatePayload, 403},
		{"unknown property", operationSecurity{}, strings.Replace(editorProductionCreatePayload, `"score_rules":[]`, `"score_rules":[],"customer_id":42`, 1), 400},
		{"unknown question property", operationSecurity{}, strings.Replace(editorProductionCreatePayload, `"title":"图片"`, `"title":"图片","private_ref":"forged"`, 1), 400},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			definitions := &editorCreateBoundaryDefinitions{}
			handler, err := NewHandler(definitions, &routeSurvey{}, scenario.security)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/admin/questionnaires", strings.NewReader(scenario.payload))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "editor-rejected-create-contract")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != scenario.status || definitions.calls != 0 {
				t.Fatalf("rejected create status=%d calls=%d body=%s", w.Code, definitions.calls, w.Body.String())
			}
		})
	}
}

type editorCreateBoundaryDefinitions struct {
	surveyport.DefinitionApplication
	calls int
}

func (d *editorCreateBoundaryDefinitions) Create(context.Context, surveyport.CreateCommand) (surveyport.Questionnaire, error) {
	d.calls++
	return surveyport.Questionnaire{}, surveyport.ErrInvalid
}

type editorCreateUnauthenticated struct{ RequestSecurity }

func (editorCreateUnauthenticated) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, errors.New("no session")
}
