package http

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type redemptionRouteDefinitions struct {
	surveyport.DefinitionApplication
	reads, imports int
}

func (d *redemptionRouteDefinitions) Get(context.Context, surveyport.ID) (surveyport.Questionnaire, error) {
	return surveyport.Questionnaire{ID: 1}, nil
}
func (d *redemptionRouteDefinitions) ListRedemptionCodes(context.Context, surveyport.ID, int32, int32) (surveyport.RedemptionPage, error) {
	d.reads++
	return surveyport.RedemptionPage{Items: []surveyport.RedemptionCode{}, Total: 0}, nil
}
func (d *redemptionRouteDefinitions) ImportRedemptionCodes(context.Context, surveyport.ID, int64, string, []byte) (surveyport.RedemptionImportResult, error) {
	d.imports++
	return surveyport.RedemptionImportResult{Imported: 1}, nil
}
func TestRedemptionAdministrationRequiresOperatorAndCSRF(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		security     RequestSecurity
		method, path string
		want         int
	}{
		{"anonymous", routeSecurity{}, "GET", "/api/admin/questionnaires/1/redemption-codes", 401},
		{"viewer", principalSecurity{accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}, "GET", "/api/admin/questionnaires/1/redemption-codes", 403},
		{"operator", operationSecurity{}, "GET", "/api/admin/questionnaires/1/redemption-codes", 200},
		{"csrf", operationSecurity{csrfErr: errors.New("invalid csrf")}, "POST", "/api/admin/questionnaires/1/redemption-codes/imports", 403},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			definitions := &redemptionRouteDefinitions{}
			h, e := NewHandler(definitions, &routeSurvey{}, scenario.security)
			if e != nil {
				t.Fatal(e)
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(scenario.method, scenario.path, nil))
			if response.Code != scenario.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if scenario.want != 200 && (definitions.reads != 0 || definitions.imports != 0) {
				t.Fatal("unauthorized library access")
			}
		})
	}
}
func TestRedemptionImportRejectsDuplicateFileFields(t *testing.T) {
	definitions := &redemptionRouteDefinitions{}
	h, _ := NewHandler(definitions, &routeSurvey{}, operationSecurity{})
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for i := 0; i < 2; i++ {
		file, e := form.CreateFormFile("file", "codes.xlsx")
		if e != nil {
			t.Fatal(e)
		}
		_, _ = file.Write([]byte("duplicate"))
	}
	_ = form.Close()
	request := httptest.NewRequest("POST", "/api/admin/questionnaires/1/redemption-codes/imports", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 400 || definitions.imports != 0 {
		t.Fatalf("status=%d imports=%d", response.Code, definitions.imports)
	}
}
func TestRedemptionFieldFeedbackDoesNotRevealCode(t *testing.T) {
	for _, err := range []error{surveyport.CodeRequired(7), surveyport.CodeInvalid(7), surveyport.CodeUsed(7)} {
		response := httptest.NewRecorder()
		resultError(response, err)
		if response.Code != 400 && response.Code != 409 {
			t.Fatal(response.Code)
		}
		if !bytes.Contains(response.Body.Bytes(), []byte(`"question_id":7`)) {
			t.Fatal("field location missing")
		}
		if !bytes.Contains(response.Body.Bytes(), []byte(`"message":`)) {
			t.Fatal("safe feedback missing")
		}
	}
}
