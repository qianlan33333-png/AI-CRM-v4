package http

import (
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

// This local HTTP contract never constructs or calls a Provider. Complete is a
// counted synthetic boundary so rejection must happen before any exchange or
// identity-session construction could be attempted.
type sol61BrowserProofOAuth struct {
	next, calls int
	consumed    map[string]bool
}

func (*sol61BrowserProofOAuth) Enabled() bool { return true }
func (o *sol61BrowserProofOAuth) Start(context.Context, string) (string, error) {
	o.next++
	state := strings.Repeat(string(rune('a'+o.next)), 43)
	o.consumed[state] = false
	return "https://synthetic.example/authorize?state=" + state, nil
}
func (o *sol61BrowserProofOAuth) Complete(_ context.Context, state, _ string) (string, string, error) {
	o.calls++
	consumed, found := o.consumed[state]
	if !found || consumed {
		return "", "", errors.New("synthetic state consumed or unknown")
	}
	o.consumed[state] = true
	return strings.Repeat("s", 43), "/h5/all.html?slug=growth", nil
}
func (*sol61BrowserProofOAuth) ResolveSession(context.Context, string) (surveyport.SubmissionIdentity, error) {
	return surveyport.SubmissionIdentity{State: surveyport.IdentityAnonymous}, nil
}

func sol61BrowserProofHandler(t *testing.T) (*Handler, *sol61BrowserProofOAuth) {
	t.Helper()
	oauth := &sol61BrowserProofOAuth{consumed: map[string]bool{}}
	handler, err := NewHandler(&routeDefinitions{}, &routeSurvey{}, routeSecurity{}, oauth)
	if err != nil {
		t.Fatal(err)
	}
	return handler, oauth
}

func sol61StartBrowserProof(t *testing.T, handler *Handler, resume ...string) (string, []*nethttp.Cookie) {
	t.Helper()
	path := "https://survey.example/api/h5/surveys/oauth/start?slug=growth"
	if len(resume) > 0 && resume[0] == "submit" {
		path += "&resume=submit"
	}
	request := httptest.NewRequest(nethttp.MethodGet, path, nil)
	request.Header.Set("User-Agent", "MicroMessenger local contract")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	location, err := url.Parse(response.Header().Get("Location"))
	if response.Code != nethttp.StatusSeeOther || err != nil || location.Query().Get("state") == "" {
		t.Fatalf("synthetic start did not return a state: status=%d error=%v", response.Code, err)
	}
	return location.Query().Get("state"), response.Result().Cookies()
}

func sol61CallbackBrowserProof(handler *Handler, state string, cookies []*nethttp.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(nethttp.MethodGet, "https://survey.example/api/h5/surveys/oauth/callback?state="+url.QueryEscape(state)+"&code=synthetic-code", nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func sol61HasIdentityCookie(cookies []*nethttp.Cookie) bool {
	for _, cookie := range cookies {
		if cookie.Name == "__Host-aicrm_survey_identity" && cookie.Value != "" && cookie.MaxAge >= 0 {
			return true
		}
	}
	return false
}

func sol61FlowProof(t *testing.T, cookies []*nethttp.Cookie) *nethttp.Cookie {
	t.Helper()
	var proof *nethttp.Cookie
	for _, cookie := range cookies {
		if strings.HasPrefix(cookie.Name, "__Host-aicrm_survey_return_") {
			if cookie.Domain != "" || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.SameSite != nethttp.SameSiteLaxMode || cookie.MaxAge != 600 {
				t.Fatal("flow return context must preserve the secure cookie contract")
			}
			continue
		}
		if proof != nil {
			t.Fatal("start must produce exactly one proof per flow")
		}
		proof = cookie
	}
	if proof == nil {
		t.Fatal("OAuth start omitted its initiating-browser proof")
	}
	if !strings.HasPrefix(proof.Name, "__Host-") || proof.Domain != "" || !proof.Secure || !proof.HttpOnly || proof.Path != "/" || proof.SameSite != nethttp.SameSiteLaxMode || proof.MaxAge != 600 || len(proof.Value) != 43 {
		t.Fatal("flow proof must preserve the production secure-cookie and existing state expiry contract")
	}
	return proof
}

func TestOAuthCallbackRequiresInitiatingBrowserProof(t *testing.T) {
	t.Run("missing_browser_proof", func(t *testing.T) {
		handler, oauth := sol61BrowserProofHandler(t)
		state, _ := sol61StartBrowserProof(t, handler)
		response := sol61CallbackBrowserProof(handler, state, []*nethttp.Cookie{{Name: "survey_oauth_return", Value: "growth"}})
		if oauth.calls != 0 || sol61HasIdentityCookie(response.Result().Cookies()) || response.Header().Get("Location") == "/h5/all.html?slug=growth" {
			t.Fatalf("callback reached Complete/session without initiating browser proof: calls=%d status=%d", oauth.calls, response.Code)
		}
	})
	t.Run("different_flow_proof", func(t *testing.T) {
		handler, oauth := sol61BrowserProofHandler(t)
		first, _ := sol61StartBrowserProof(t, handler)
		_, secondCookies := sol61StartBrowserProof(t, handler)
		response := sol61CallbackBrowserProof(handler, first, secondCookies)
		if oauth.calls != 0 || sol61HasIdentityCookie(response.Result().Cookies()) {
			t.Fatalf("another flow's proof authorized callback: calls=%d status=%d", oauth.calls, response.Code)
		}
	})
	t.Run("correct_name_wrong_value", func(t *testing.T) {
		handler, oauth := sol61BrowserProofHandler(t)
		first, firstCookies := sol61StartBrowserProof(t, handler)
		_, secondCookies := sol61StartBrowserProof(t, handler)
		firstProof, secondProof := sol61FlowProof(t, firstCookies), sol61FlowProof(t, secondCookies)
		wrongProof := *firstProof
		wrongProof.Value = secondProof.Value
		response := sol61CallbackBrowserProof(handler, first, []*nethttp.Cookie{&wrongProof})
		if oauth.calls != 0 || sol61HasIdentityCookie(response.Result().Cookies()) {
			t.Fatalf("proof name alone authorized callback with another flow's value: calls=%d status=%d", oauth.calls, response.Code)
		}
	})
}

func TestOAuthParallelFlowsRetainOwnBrowserProof(t *testing.T) {
	handler, oauth := sol61BrowserProofHandler(t)
	firstState, firstCookies := sol61StartBrowserProof(t, handler)
	secondState, secondCookies := sol61StartBrowserProof(t, handler)
	firstProof, secondProof := sol61FlowProof(t, firstCookies), sol61FlowProof(t, secondCookies)
	if firstProof.Name == secondProof.Name || firstProof.Value == secondProof.Value {
		t.Fatal("parallel OAuth flows overwrite one browser proof")
	}
	jar := append(firstCookies, secondProof)
	first := sol61CallbackBrowserProof(handler, firstState, jar)
	if first.Code != nethttp.StatusSeeOther || first.Header().Get("Location") != "/h5/all.html?slug=growth" || oauth.calls != 1 || !sol61HasIdentityCookie(first.Result().Cookies()) {
		t.Fatalf("first legitimate flow failed: status=%d calls=%d", first.Code, oauth.calls)
	}
	deletedOwn := false
	for _, cookie := range first.Result().Cookies() {
		if cookie.Name == secondProof.Name {
			t.Fatal("first callback modified another flow's proof")
		}
		deletedOwn = deletedOwn || cookie.Name == firstProof.Name && cookie.MaxAge < 0
	}
	if !deletedOwn {
		t.Fatal("completed flow retained its browser proof")
	}
	second := sol61CallbackBrowserProof(handler, secondState, secondCookies)
	if second.Code != nethttp.StatusSeeOther || second.Header().Get("Location") != "/h5/all.html?slug=growth" || oauth.calls != 2 || !sol61HasIdentityCookie(second.Result().Cookies()) {
		t.Fatalf("second legitimate flow failed: status=%d calls=%d", second.Code, oauth.calls)
	}
	// The browser removed firstProof. Repeated callbacks must be rejected
	// before Complete, independently of the existing database consumption gate.
	replayed := sol61CallbackBrowserProof(handler, firstState, secondCookies)
	if oauth.calls != 2 || sol61HasIdentityCookie(replayed.Result().Cookies()) {
		t.Fatal("completed flow could borrow the remaining parallel proof")
	}
}

func TestOAuthParallelFlowResumeContextIsBoundToOwnState(t *testing.T) {
	for _, order := range []string{"ordinary_then_resume", "resume_then_ordinary"} {
		t.Run(order, func(t *testing.T) {
			handler, oauth := sol61BrowserProofHandler(t)
			resumeState, resumeCookies := sol61StartBrowserProof(t, handler, "submit")
			ordinaryState, ordinaryCookies := sol61StartBrowserProof(t, handler)
			// Model the browser's real cookie-name replacement: the second Start
			// must not replace the first flow's independent return context.
			jar := map[string]*nethttp.Cookie{}
			apply := func(cookies []*nethttp.Cookie) {
				for _, cookie := range cookies {
					if cookie.MaxAge < 0 {
						delete(jar, cookie.Name)
					} else {
						jar[cookie.Name] = cookie
					}
				}
			}
			apply(resumeCookies)
			apply(ordinaryCookies)
			states := []string{ordinaryState, resumeState}
			if order == "resume_then_ordinary" {
				states[0], states[1] = states[1], states[0]
			}
			for _, state := range states {
				cookies := make([]*nethttp.Cookie, 0, len(jar))
				for _, cookie := range jar {
					cookies = append(cookies, cookie)
				}
				response := sol61CallbackBrowserProof(handler, state, cookies)
				want := "/h5/all.html?slug=growth"
				wantReturn := "growth"
				if state == resumeState {
					want += "&resume=submit"
					wantReturn += ".submit"
				}
				if response.Code != nethttp.StatusSeeOther || response.Header().Get("Location") != want || !sol61HasIdentityCookie(response.Result().Cookies()) {
					t.Fatalf("parallel flow replaced its own resume context: status=%d location=%q expected=%q", response.Code, response.Header().Get("Location"), want)
				}
				deletedReturn := 0
				for _, cookie := range response.Result().Cookies() {
					if strings.HasPrefix(cookie.Name, "__Host-aicrm_survey_return_") && cookie.MaxAge < 0 {
						deletedReturn++
						if prior := jar[cookie.Name]; prior == nil || prior.Value != wantReturn {
							t.Fatal("callback cleared another flow's return context")
						}
					}
				}
				if deletedReturn != 1 {
					t.Fatal("callback must delete only its own return context")
				}
				apply(response.Result().Cookies())
			}
			if oauth.calls != 2 {
				t.Fatalf("two valid parallel flows must complete independently: calls=%d", oauth.calls)
			}
		})
	}
}
