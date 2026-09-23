package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type JSSDKSigner interface {
	ConfigForURL(context.Context, string) (JSSDKConfig, error)
}

type JSSDKSignature struct {
	Timestamp int64    `json:"timestamp"`
	NonceStr  string   `json:"nonceStr"`
	Signature string   `json:"signature"`
	JSAPIList []string `json:"jsApiList"`
}

// JSSDKConfig is deliberately typed: the sidebar needs distinct regular and
// agent signatures and the WeCom JS SDK expects their native JSON types.
type JSSDKConfig struct {
	CorpID      string         `json:"corp_id"`
	AgentID     string         `json:"agent_id"`
	Config      JSSDKSignature `json:"config"`
	AgentConfig JSSDKSignature `json:"agent_config"`
}

type EmployeeSessionIssuer interface {
	IssueWeComSession(context.Context, OAuthPurpose, OAuthIdentity) (BrowserCredentials, error)
}

// BrowserCredentials are write-only transport values. They must never be put
// in logs or JSON responses; the HTTP adapter turns them into secure cookies.
type BrowserCredentials struct {
	SessionToken string
	CSRFToken    string
	ExpiresAt    time.Time
}

type HTTPHandlerOptions struct {
	Callback          CallbackHandler
	OAuth             OAuthService
	ContextTokens     ContextTokenService
	JSSDKSigner       JSSDKSigner
	JSSDKOrigin       string
	PrincipalResolver SidebarPrincipalResolver
	SessionIssuer     EmployeeSessionIssuer
	ExistingIdentity  ExistingWeComIdentityResolver
	CookieSecure      bool
}

// NewHTTPHandler creates the frozen WeCom routes. The caller mounts this
// handler in cmd/aicrm; this package never registers routes globally.
func NewHTTPHandler(options HTTPHandlerOptions) (http.Handler, error) {
	if options.OAuth.Enabled && (!options.CookieSecure || options.SessionIssuer == nil || options.OAuth.StateStore == nil || options.OAuth.UOW == nil || options.OAuth.Client == nil || !providerReady(options.OAuth.Client) || options.OAuth.CorpID == "" || !options.ContextTokens.valid() || options.ContextTokens.CorpID != options.OAuth.CorpID || options.JSSDKSigner == nil || !providerReady(options.JSSDKSigner) || !validJSSDKOrigin(options.JSSDKOrigin) || options.PrincipalResolver == nil || options.ExistingIdentity == nil) {
		return nil, errors.New("enabled wecom oauth requires secure browser session dependencies")
	}
	mux := http.NewServeMux()
	mux.Handle("GET /wecom/external-contact/callback", options.Callback)
	mux.Handle("POST /wecom/external-contact/callback", options.Callback)
	// v2 exposed the same verification and callback handler at this path;
	// keep it as an exact compatibility alias without creating a second
	// protocol implementation or idempotency namespace.
	mux.Handle("GET /api/wecom/events", options.Callback)
	mux.Handle("POST /api/wecom/events", options.Callback)
	mux.HandleFunc("GET /auth/wecom/start", func(writer http.ResponseWriter, request *http.Request) {
		handleOAuthStart(writer, request, options.OAuth, OAuthAdmin, OAuthMode(request.URL.Query().Get("mode")))
	})
	mux.HandleFunc("GET /auth/wecom/callback", func(writer http.ResponseWriter, request *http.Request) {
		handleOAuthCallback(writer, request, options, OAuthAdmin)
	})
	mux.HandleFunc("GET /api/sidebar/oauth/start", func(writer http.ResponseWriter, request *http.Request) {
		mode := OAuthMode(request.URL.Query().Get("mode"))
		if mode == "" {
			mode = OAuthModeWeb
		}
		handleOAuthStart(writer, request, options.OAuth, OAuthSidebar, mode)
	})
	mux.HandleFunc("GET /api/sidebar/oauth/callback", func(writer http.ResponseWriter, request *http.Request) {
		handleOAuthCallback(writer, request, options, OAuthSidebar)
	})
	mux.HandleFunc("GET /api/sidebar/jssdk-config", func(writer http.ResponseWriter, request *http.Request) {
		handleJSSDK(writer, request, options)
	})
	mux.HandleFunc("POST /api/sidebar/context-token", func(writer http.ResponseWriter, request *http.Request) {
		handleContextIssue(writer, request, options)
	})
	return mux, nil
}

// Concrete live adapters may report readiness; fakes used by contract tests do
// not need to. This keeps the composition root fail-closed for a disabled or
// incomplete real adapter without coupling the domain to its child package.
func providerReady(value any) bool {
	ready, reports := value.(interface{ Ready() bool })
	return !reports || ready.Ready()
}

func handleOAuthStart(writer http.ResponseWriter, request *http.Request, service OAuthService, purpose OAuthPurpose, mode OAuthMode) {
	if purpose == OAuthAdmin && mode == "" {
		mode = OAuthModeQR
	}
	if !validPurposeMode(purpose, mode) {
		writeWeComError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	start, err := service.Start(request.Context(), purpose, mode, request.URL.Query().Get("next"))
	if err != nil {
		writeOAuthError(writer, err)
		return
	}
	http.Redirect(writer, request, start.AuthorizationURL, http.StatusFound)
}

func handleOAuthCallback(writer http.ResponseWriter, request *http.Request, options HTTPHandlerOptions, purpose OAuthPurpose) {
	if options.SessionIssuer == nil {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	identity, state, err := options.OAuth.ConsumeAndExchange(request.Context(), purpose, request.URL.Query().Get("state"), request.URL.Query().Get("code"))
	if err != nil {
		writeOAuthError(writer, err)
		return
	}
	credentials, err := options.SessionIssuer.IssueWeComSession(request.Context(), purpose, identity)
	if err != nil || !credentials.valid(time.Now().UTC()) {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	writeBrowserCookies(writer, purpose, credentials, options.CookieSecure)
	http.Redirect(writer, request, state.Redirect, http.StatusFound)
}

func handleJSSDK(writer http.ResponseWriter, request *http.Request, options HTTPHandlerOptions) {
	if !options.OAuth.Enabled || options.JSSDKSigner == nil || options.PrincipalResolver == nil {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	cookie, cookieErr := request.Cookie("aicrm_sidebar_session")
	if cookieErr != nil || cookie.Value == "" {
		writeWeComError(writer, http.StatusUnauthorized, "authentication_required")
		return
	}
	principal, err := options.PrincipalResolver.SidebarPrincipal(request.Context(), cookie.Value)
	if err != nil || principal.CorpID != options.OAuth.CorpID || strings.TrimSpace(principal.EmployeeID) != principal.EmployeeID || principal.EmployeeID == "" {
		writeWeComError(writer, http.StatusUnauthorized, "authentication_required")
		return
	}
	if !validJSSDKURL(request.URL.Query().Get("url"), options.JSSDKOrigin) {
		writeWeComError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	config, err := options.JSSDKSigner.ConfigForURL(request.Context(), request.URL.Query().Get("url"))
	if err != nil {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, config)
}

func validJSSDKURL(value, origin string) bool {
	parsed, err := url.Parse(value)
	base, baseErr := url.Parse(origin)
	return err == nil && baseErr == nil && base.IsAbs() && base.Host != "" && parsed.Scheme == base.Scheme && parsed.Host == base.Host && parsed.Fragment == "" && parsed.Path != ""
}

func validJSSDKOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	return err == nil && parsed.IsAbs() && parsed.Host != "" && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

func handleContextIssue(writer http.ResponseWriter, request *http.Request, options HTTPHandlerOptions) {
	if !options.OAuth.Enabled || options.PrincipalResolver == nil || options.ExistingIdentity == nil {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 4096)
	var input struct {
		ExternalUserID string `json:"external_userid"`
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || strings.TrimSpace(input.ExternalUserID) != input.ExternalUserID || input.ExternalUserID == "" {
		writeWeComError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		writeWeComError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	cookie, cookieErr := request.Cookie("aicrm_sidebar_session")
	if cookieErr != nil || cookie.Value == "" {
		writeWeComError(writer, http.StatusUnauthorized, "authentication_required")
		return
	}
	principal, err := options.PrincipalResolver.SidebarPrincipal(request.Context(), cookie.Value)
	if err != nil {
		writeWeComError(writer, http.StatusUnauthorized, "authentication_required")
		return
	}
	customerID, found, err := options.ExistingIdentity.ResolveExistingWeComIdentity(request.Context(), principal.CorpID, input.ExternalUserID)
	if err != nil {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	if !found {
		writeWeComError(writer, http.StatusNotFound, "identity_not_found")
		return
	}
	token, err := options.ContextTokens.Issue(request.Context(), principal, customerID)
	if err != nil {
		writeContextError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"context_token": token})
}

// ExistingWeComIdentityResolver is deliberately read-only. It must resolve an
// existing OneID mapping and must never provision a customer or mint a fact.
type ExistingWeComIdentityResolver interface {
	ResolveExistingWeComIdentity(ctx context.Context, corpID, externalUserID string) (customerdomain.CustomerID, bool, error)
}

func (credentials BrowserCredentials) valid(now time.Time) bool {
	return credentials.SessionToken != "" && credentials.CSRFToken != "" && credentials.ExpiresAt.After(now)
}

func writeBrowserCookies(writer http.ResponseWriter, purpose OAuthPurpose, credentials BrowserCredentials, secure bool) {
	sessionName, csrfName := "aicrm_admin_session", "aicrm_admin_csrf"
	if purpose == OAuthSidebar {
		sessionName, csrfName = "aicrm_sidebar_session", "aicrm_sidebar_csrf"
	}
	base := http.Cookie{Path: "/", Secure: secure, SameSite: http.SameSiteLaxMode, Expires: credentials.ExpiresAt}
	session := base
	session.Name, session.Value, session.HttpOnly = sessionName, credentials.SessionToken, true
	csrf := base
	csrf.Name, csrf.Value = csrfName, credentials.CSRFToken
	http.SetCookie(writer, &session)
	http.SetCookie(writer, &csrf)
}

func writeOAuthError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrProviderDisabled):
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
	case errors.Is(err, ErrOpenRedirect), errors.Is(err, ErrInvalidOAuth):
		writeWeComError(writer, http.StatusBadRequest, "invalid_oauth_state")
	default:
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
	}
}

func writeContextError(writer http.ResponseWriter, err error) {
	writeWeComError(writer, http.StatusUnauthorized, "authentication_required")
}

func bearerToken(value string) string {
	const prefix = "Bearer "
	if strings.HasPrefix(value, prefix) {
		return strings.TrimPrefix(value, prefix)
	}
	return ""
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
