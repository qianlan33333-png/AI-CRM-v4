package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/crmcli"
	"github.com/zalando/go-keyring"
)

type cliLoginAuth struct {
	fakeAccessAuthentication
	logins, logouts int
}

func (a *cliLoginAuth) Login(_ context.Context, input accessapp.LoginCommand) (accessapp.IssuedSession, error) {
	if input.Username != "operator" || input.Password != "PASSWORD" {
		return accessapp.IssuedSession{}, accessdomain.ErrInvalidCredentials
	}
	a.logins++
	return accessapp.IssuedSession{SessionToken: "SESSION", CSRFToken: "CSRF", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (a *cliLoginAuth) Logout(ctx context.Context, session, cookie, request string) error {
	if _, err := a.AuthorizeCSRF(ctx, session, cookie, request); err != nil {
		return err
	}
	a.logouts++
	return nil
}

type cliNoPageRenderer struct{}

func (cliNoPageRenderer) Render(context.Context, http.ResponseWriter, int, string, map[string]any) error {
	panic("CLI must use JSON login")
}

type cliNoManagement struct{ accesshttp.Management }

// Uses the actual Access HTTP handler rather than recreating its cookie and
// login protocol in a test server. Authentication storage is a fixture, not PG.
func TestCLIExistingAccessHTTPLoginAndLogoutJourney(t *testing.T) {
	keyring.MockInit()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("AICRM_CLI_TOKEN", "")
	t.Setenv("AICRM_CLI_PASSWORD", "")
	auth := &cliLoginAuth{fakeAccessAuthentication: fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleViewer}, SessionVersion: 3}}}
	h, err := accesshttp.NewHandler(accesshttp.Config{Renderer: cliNoPageRenderer{}, Auth: auth, Management: cliNoManagement{}, CookieSecure: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(h.Routes())
	defer server.Close()
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = original }()
	var out, stderr bytes.Buffer
	run := func(args []string, input string) int {
		out.Reset()
		stderr.Reset()
		return crmcli.Run(args, strings.NewReader(input), &out, &stderr)
	}
	if code := run([]string{"profile", "add", "fixture", server.URL}, ""); code != 0 {
		t.Fatal(out.String())
	}
	if code := run([]string{"auth", "login", "--username", "operator", "--secret-stdin"}, "PASSWORD"); code != 0 || auth.logins != 1 {
		t.Fatalf("login=%d code=%d %s", auth.logins, code, out.String())
	}
	if strings.Contains(out.String(), "PASSWORD") || strings.Contains(out.String(), "SESSION") || strings.Contains(out.String(), "CSRF") {
		t.Fatal("login exposed credentials")
	}
	if code := run([]string{"auth", "whoami"}, ""); code != 0 || !strings.Contains(out.String(), `"actor_id":7`) || auth.session != "SESSION" {
		t.Fatalf("read code=%d %s", code, out.String())
	}
	if code := run([]string{"auth", "logout"}, ""); code != 0 || auth.logouts != 1 || auth.csrf != [3]string{"SESSION", "CSRF", "CSRF"} {
		t.Fatalf("logout=%d code=%d csrf=%v %s", auth.logouts, code, auth.csrf, out.String())
	}
	if code := run([]string{"auth", "whoami"}, ""); code != 3 {
		t.Fatalf("credential still present after logout: %d", code)
	}
}
