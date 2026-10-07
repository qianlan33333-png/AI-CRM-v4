package crmcli

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

type waitDeadlineTransport func(*http.Request) (*http.Response, error)

func (f waitDeadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWaitTimeoutBoundsStatusRequestAndPolling(t *testing.T) {
	keyring.MockInit()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("AICRM_CLI_TOKEN", "")
	const origin = "https://synthetic.invalid"
	if err := SaveConfig(Config{Current: "test", Profiles: map[string]Profile{"test": {Origin: origin}}}); err != nil {
		t.Fatal(err)
	}
	if err := StoreCredential(Credential{Origin: origin, Kind: "machine", Token: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, tc := range []struct {
		name, status string
		delay        time.Duration
		code         int
	}{
		{"slow_first_response", "completed", 200 * time.Millisecond, 6},
		{"pending_poll", "running", 0, 6},
		{"completed_before_deadline", "completed", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			http.DefaultTransport = waitDeadlineTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if tc.delay > 0 {
					select {
					case <-r.Context().Done():
						return nil, r.Context().Err()
					case <-time.After(tc.delay):
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":{"status":"` + tc.status + `"}}`)), Request: r}, nil
			})
			var out, stderr bytes.Buffer
			code := Run([]string{"invoke", "customer.list", "--wait", "--wait-timeout=20ms", "--timeout=2s"}, strings.NewReader(""), &out, &stderr)
			if code != tc.code || (tc.code == 6 && !strings.Contains(out.String(), `"code":"pending"`)) {
				t.Fatalf("wait must retain pending outcome at its deadline: code=%d output=%s", code, out.String())
			}
			if calls != 1 {
				t.Fatalf("expired wait made another status request: calls=%d", calls)
			}
		})
	}
}
