package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type layoutSecurityStub struct {
	securityStub
	csrfErr error
}

func (s layoutSecurityStub) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, s.csrfErr
}

func TestPosterLayoutsAdminHTTPContractAndSecurity(t *testing.T) {
	for _, test := range []struct {
		name, body       string
		authErr, csrfErr error
		status           int
		calls            int
	}{
		{"valid-zero-coordinate", `{"expected_version":2,"posters":[{"slot":1,"x":0,"y":1952,"size":330}]}`, nil, nil, 200, 1},
		{"missing-coordinate", `{"expected_version":2,"posters":[{"slot":1,"y":1952,"size":330}]}`, nil, nil, 400, 0},
		{"fractional", `{"expected_version":2,"posters":[{"slot":1,"x":830.5,"y":1952,"size":330}]}`, nil, nil, 400, 0},
		{"unknown-field", `{"expected_version":2,"posters":[{"slot":1,"x":830,"y":1952,"size":330,"extra":1}]}`, nil, nil, 400, 0},
		{"unauthorized", `{"expected_version":2,"posters":[{"slot":1,"x":830,"y":1952,"size":330}]}`, errors.New("unauthorized"), nil, 401, 0},
		{"csrf", `{"expected_version":2,"posters":[{"slot":1,"x":830,"y":1952,"size":330}]}`, nil, errors.New("csrf"), 403, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &posterHTTPStub{}
			h := referralTestHandler(t, &publicStub{})
			h.posters = s
			h.security = layoutSecurityStub{securityStub: securityStub{err: test.authErr}, csrfErr: test.csrfErr}
			r := httptest.NewRequest(http.MethodPut, "/api/admin/referral/campaigns/7/poster-layouts", strings.NewReader(test.body))
			r.Header.Set("Idempotency-Key", "layout-http-fixture-key")
			w := httptest.NewRecorder()
			h.ServeAdminHTTP(w, r)
			if w.Code != test.status || len(s.layoutCalls) != test.calls {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, len(s.layoutCalls), w.Body.String())
			}
			if test.calls > 0 && (!strings.Contains(w.Body.String(), `"version":3`) || !strings.Contains(w.Body.String(), `"x":0`)) {
				t.Fatalf("missing readback: %s", w.Body.String())
			}
		})
	}
	w := httptest.NewRecorder()
	resultError(w, referralport.ErrPosterDimensions)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "poster_dimensions_invalid") {
		t.Fatal(w.Body.String())
	}
}
