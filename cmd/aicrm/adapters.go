package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	adminopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/app"
	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	releaseport "github.com/qianlan33333-png/AI-CRM-v3/internal/release/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/sidebar"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

type accessAuthentication interface {
	Authenticate(context.Context, string) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, string, string, string) (accessdomain.Principal, error)
	LoginWithWeComUserID(context.Context, accessapp.WeComLoginCommand) (accessapp.IssuedSession, error)
}

type requestAccessSecurity struct{ authentication accessAuthentication }

// adminOpsReleaseObservationWriter keeps the release plane on its stable
// observation port while AdminOps remains the owner of the projection tables.
// No release payload, actor metadata, URL, or external-effect result crosses
// this adapter.
type adminOpsReleaseObservationWriter struct {
	projections *adminopsapp.ProjectionService
}

func (adapter adminOpsReleaseObservationWriter) RecordReleaseObservation(ctx context.Context, observation releaseport.ReleaseObservation) error {
	if adapter.projections == nil {
		return adminopsapp.ErrProjectionUnavailable
	}
	_, err := adapter.projections.RecordReleaseProjection(ctx, adminopsport.ReleaseProjection{
		ReleaseSHA: observation.ReleaseSHA,
		Status:     observation.Status,
		ObservedAt: observation.ObservedAt,
	})
	return err
}

func (adapter requestAccessSecurity) Authenticate(ctx context.Context, request *http.Request) (accessdomain.Principal, error) {
	return adapter.authentication.Authenticate(ctx, cookieValue(request, accesshttp.SessionCookieName))
}

func (adapter requestAccessSecurity) AuthorizeCSRF(ctx context.Context, request *http.Request) (accessdomain.Principal, error) {
	return adapter.authentication.AuthorizeCSRF(ctx,
		cookieValue(request, accesshttp.SessionCookieName), cookieValue(request, accesshttp.CSRFCookieName),
		strings.TrimSpace(request.Header.Get("X-CSRF-Token")))
}

type weComSessionIssuer struct{ authentication accessAuthentication }

func (adapter weComSessionIssuer) IssueWeComSession(ctx context.Context, purpose wecom.OAuthPurpose, identity wecom.OAuthIdentity) (wecom.BrowserCredentials, error) {
	if purpose != wecom.OAuthAdmin && purpose != wecom.OAuthSidebar {
		return wecom.BrowserCredentials{}, accessdomain.ErrAuthentication
	}
	issued, err := adapter.authentication.LoginWithWeComUserID(ctx, accessapp.WeComLoginCommand{WeComUserID: identity.EmployeeID, Remote: "wecom-oauth"})
	if err != nil {
		return wecom.BrowserCredentials{}, err
	}
	return wecom.BrowserCredentials{SessionToken: issued.SessionToken, CSRFToken: issued.CSRFToken, ExpiresAt: issued.ExpiresAt}, nil
}

type sidebarPrincipalResolver struct {
	authentication accessAuthentication
	users          accessUserReader
	uow            platformport.UnitOfWork
	corpID         string
}

type accessUserReader interface {
	UserByID(context.Context, int64, bool) (accessdomain.User, error)
}

func (adapter sidebarPrincipalResolver) SidebarPrincipal(ctx context.Context, sessionToken string) (wecom.SidebarPrincipal, error) {
	principal, err := adapter.authentication.Authenticate(ctx, sessionToken)
	if err != nil {
		return wecom.SidebarPrincipal{}, err
	}
	var user accessdomain.User
	err = adapter.uow.Within(ctx, func(txContext context.Context) error {
		var loadErr error
		user, loadErr = adapter.users.UserByID(txContext, principal.InternalID, false)
		return loadErr
	})
	if err != nil || !user.Active || user.WeComUserID == "" {
		return wecom.SidebarPrincipal{}, accessdomain.ErrAuthentication
	}
	return wecom.SidebarPrincipal{CorpID: adapter.corpID, EmployeeID: user.WeComUserID}, nil
}

type oneIDBridge struct{ service identityapp.OneIDService }

func (adapter oneIDBridge) ProvisionVerifiedWeComIdentity(ctx context.Context, fact identitydomain.VerifiedFact) (customerdomain.CustomerID, error) {
	result, err := adapter.service.ProvisionVerifiedIdentity(ctx, identityport.ProvisionCommand{Fact: fact})
	return result.CustomerID, err
}

func (adapter oneIDBridge) FindVerifiedWeComIdentity(ctx context.Context, fact identitydomain.VerifiedFact) (customerdomain.CustomerID, bool, error) {
	reference := fact.Reference()
	result, err := adapter.service.Resolve(ctx, identitydomain.Reference{Kind: reference.Kind, Scope: reference.Scope,
		Value: reference.NormalizedValue, Assurance: identitydomain.AssuranceVerified, Source: reference.Source})
	if err != nil {
		return 0, false, err
	}
	return result.CustomerID, result.Status == identityport.ResolveFound, nil
}

type existingWeComIdentityResolver struct {
	service identityapp.OneIDService
	uow     platformport.UnitOfWork
	corpID  string
}

func (adapter existingWeComIdentityResolver) ResolveExistingWeComIdentity(ctx context.Context, corpID, externalUserID string) (customerdomain.CustomerID, bool, error) {
	if corpID != adapter.corpID || strings.TrimSpace(externalUserID) != externalUserID || externalUserID == "" {
		return 0, false, identitydomain.ErrInvalidReference
	}
	var result identityport.ResolveResult
	err := adapter.uow.Within(ctx, func(txContext context.Context) error {
		var resolveErr error
		result, resolveErr = adapter.service.Resolve(txContext, identitydomain.Reference{
			Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:" + corpID,
			Value: externalUserID, Assurance: identitydomain.AssuranceDeclared, Source: "wecom.sidebar",
		})
		return resolveErr
	})
	if err != nil {
		return 0, false, err
	}
	return result.CustomerID, result.Status == identityport.ResolveFound, nil
}

// sidebarViewerBootstrapper combines the WeCom sidebar session, the read-only
// OneID resolver, and the context-token issuer into the sidebar package's
// bootstrap port.  It mirrors the retired /api/sidebar/context-token handler's
// security posture: the viewer principal comes exclusively from the HttpOnly
// sidebar session cookie, and identity resolution never provisions a customer.
type sidebarViewerBootstrapper struct {
	principals sidebarPrincipalResolver
	identity   existingWeComIdentityResolver
	tokens     wecom.ContextTokenService
}

func (adapter sidebarViewerBootstrapper) BootstrapViewer(ctx context.Context, request *http.Request, externalUserID string) (sidebar.Principal, customerdomain.CustomerID, string, error) {
	cookie, err := request.Cookie("aicrm_sidebar_session")
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return sidebar.Principal{}, 0, "", sidebar.ErrViewerSessionRequired
	}
	principal, err := adapter.principals.SidebarPrincipal(ctx, cookie.Value)
	if err != nil {
		return sidebar.Principal{}, 0, "", sidebar.ErrViewerSessionRequired
	}
	customerID, found, err := adapter.identity.ResolveExistingWeComIdentity(ctx, principal.CorpID, externalUserID)
	if err != nil {
		return sidebar.Principal{}, 0, "", err
	}
	if !found {
		return sidebar.Principal{}, 0, "", sidebar.ErrCustomerNotBound
	}
	token, err := adapter.tokens.Issue(ctx, principal, customerID)
	if err != nil {
		return sidebar.Principal{}, 0, "", err
	}
	return sidebar.Principal{CorpID: principal.CorpID, EmployeeID: principal.EmployeeID}, customerID, token, nil
}

func requireAdminSession(authentication accessAuthentication, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := authentication.Authenticate(request.Context(), cookieValue(request, accesshttp.SessionCookieName)); err != nil {
			target := "/login?next=" + url.QueryEscape(request.URL.RequestURI())
			http.Redirect(writer, request, target, http.StatusSeeOther)
			return
		}
		if csrfToken := cookieValue(request, accesshttp.CSRFCookieName); csrfToken != "" {
			http.SetCookie(writer, &http.Cookie{
				Name: accesshttp.CompatCSRFCookieName, Value: csrfToken, Path: "/",
				Secure: true, HttpOnly: false, SameSite: http.SameSiteLaxMode,
			})
		}
		next.ServeHTTP(writer, request)
	})
}

func cookieValue(request *http.Request, name string) string {
	cookie, err := request.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}
