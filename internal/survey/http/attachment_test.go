package http

import (
	"bytes"
	"context"
	"encoding/json"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"mime/multipart"
	"net/http/httptest"
	"testing"
)

func TestAttachmentMultipartStrictEnvelope(t *testing.T) {
	for _, test := range []struct {
		name   string
		fields []string
		origin string
		extra  bool
		size   int
		valid  bool
	}{
		{name: "valid multiple", fields: []string{"attachment:1", "attachment:2"}, origin: "http://crm.test", size: 2, valid: true},
		{name: "duplicate file", fields: []string{"attachment:1", "attachment:1"}, origin: "http://crm.test", size: 2},
		{name: "unknown file", fields: []string{"evil"}, origin: "http://crm.test", size: 2},
		{name: "cross origin", fields: []string{"attachment:1"}, origin: "http://attacker.test", size: 2},
		{name: "missing origin", fields: []string{"attachment:1"}, size: 2},
		{name: "exact bound", fields: []string{"attachment:1"}, origin: "http://crm.test", size: surveyport.MaximumAttachmentBytes},
		{name: "unknown value", fields: []string{"attachment:1"}, origin: "http://crm.test", size: 2, extra: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var buffer bytes.Buffer
			form := multipart.NewWriter(&buffer)
			_ = form.WriteField("submission", `{"version":1,"submission_key":"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopq","answers":[]}`)
			if test.extra {
				_ = form.WriteField("url", "http://evil/image.png")
			}
			for _, name := range test.fields {
				file, _ := form.CreateFormFile(name, "image.png")
				_, _ = file.Write(make([]byte, test.size))
			}
			_ = form.Close()
			r := httptest.NewRequest("POST", "http://crm.test/api/public/questionnaires/test/submissions", &buffer)
			r.Header.Set("Content-Type", form.FormDataContentType())
			r.Header.Set("Origin", test.origin)
			body, uploads, cleanup, err := decodeSubmission(httptest.NewRecorder(), r)
			defer cleanup()
			if test.valid {
				if err != nil || len(uploads) != 2 || body.Version != 1 {
					t.Fatalf("decode=%v %+v", err, uploads)
				}
				for _, upload := range uploads {
					f, err := upload.Open()
					if err != nil {
						t.Fatal(err)
					}
					_ = f.Close()
				}
			} else if err == nil {
				t.Fatal("unsafe multipart accepted")
			}
		})
	}
}

type attachmentRoutes struct {
	routeSurvey
	reads int
}

func (s *attachmentRoutes) ReadSubmissionAttachment(_ context.Context, qid, sid, aid surveyport.ID) (surveyport.AttachmentContent, error) {
	s.reads++
	if qid != 1 || sid != 2 || aid != 3 {
		return surveyport.AttachmentContent{}, surveyport.ErrNotFound
	}
	return surveyport.AttachmentContent{FileName: "图片.png", MediaType: "image/png", Content: []byte("private")}, nil
}
func TestAttachmentReadUsesAnswerPermissionAndScope(t *testing.T) {
	for _, test := range []struct {
		name, path string
		security   RequestSecurity
		status     int
	}{
		{"anonymous", "/api/admin/questionnaires/1/submissions/2/answers/3/attachment", routeSecurity{}, 401},
		{"authorized", "/api/admin/questionnaires/1/submissions/2/answers/3/attachment", operationSecurity{}, 200},
		{"wrong scope", "/api/admin/questionnaires/9/submissions/2/answers/3/attachment", operationSecurity{}, 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &attachmentRoutes{}
			h, _ := NewHandler(&redemptionRouteDefinitions{}, service, test.security)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", test.path, nil))
			if w.Code != test.status {
				t.Fatalf("status=%d %s", w.Code, w.Body.String())
			}
			if test.status == 401 && service.reads != 0 {
				t.Fatal("anonymous read reached private storage")
			}
			if test.status == 200 && (w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff") {
				t.Fatal("unsafe private response headers")
			}
		})
	}
	// The opaque Media ref is never part of any outward answer projection.
	raw, _ := json.Marshal(surveyport.SubmissionAttachment{PrivateRef: "secret-ref", FileName: "image.png", ViewURL: "/api/admin/scoped"})
	if bytes.Contains(raw, []byte("secret-ref")) {
		t.Fatal("private Media ref escaped")
	}
}

func TestSubmissionRejectsSimpleJSONContentTypes(t *testing.T) {
	for _, contentType := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
		r := httptest.NewRequest("POST", "http://crm.test/api/public/questionnaires/test/submissions", bytes.NewBufferString(`{"version":1,"answers":[]}`))
		r.Header.Set("Content-Type", contentType)
		_, _, cleanup, err := decodeSubmission(httptest.NewRecorder(), r)
		cleanup()
		if err == nil {
			t.Fatalf("simple cross-origin content type accepted: %s", contentType)
		}
	}
}

func TestSubmissionMalformedJSONKeepsClientFailureStatus(t *testing.T) {
	for _, payload := range []string{`{"version":`, `{"url":"https://untrusted.test/image.png"}`, `{} {}`} {
		r := httptest.NewRequest("POST", "http://crm.test/api/public/questionnaires/test/submissions", bytes.NewBufferString(payload))
		r.Header.Set("Content-Type", "application/json")
		_, _, cleanup, err := decodeSubmission(httptest.NewRecorder(), r)
		cleanup()
		if err == nil {
			t.Fatal("malformed input accepted")
		}
		w := httptest.NewRecorder()
		resultError(w, err)
		if w.Code != 400 {
			t.Fatalf("malformed client input mapped to server failure: %d", w.Code)
		}
	}
}
