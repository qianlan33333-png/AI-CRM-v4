package diagnostics

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testRelease = "0123456789abcdef0123456789abcdef01234567"

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&b, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &b
}
func TestCorrelationUsesLocalIDAndRejectsArbitraryWorkerFields(t *testing.T) {
	seen := Correlation{}
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = FromContext(r.Context()); w.WriteHeader(204) }), testRelease, nil)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-AICRM-Diagnostic-ID", strings.Repeat("f", 32))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if seen.RequestID == req.Header.Get("X-AICRM-Diagnostic-ID") || !hexToken(seen.RequestID, 32) || rec.Header().Get("X-AICRM-Diagnostic-ID") != seen.RequestID || seen.ReleaseSHA != testRelease {
		t.Fatalf("invalid local correlation: %+v", seen)
	}
	if FromContext(context.Background()) != (Correlation{}) {
		t.Fatal("context leaked")
	}
	if ctx, err := WithCorrelation(context.Background(), seen); err != nil || FromContext(ctx) != seen {
		t.Fatal("valid worker correlation rejected")
	}
	worker := seen
	worker.JobRef, worker.EffectRef = "river_123", "eer_456"
	if ctx, err := WithCorrelation(context.Background(), worker); err != nil || FromContext(ctx) != worker {
		t.Fatal("worker references were not propagated")
	}
	for _, ref := range []string{"river_0", "river_01", "river_token", "https://example.test/secret", "river_123/phone"} {
		worker.JobRef = ref
		if _, err := WithCorrelation(context.Background(), worker); !errors.Is(err, ErrInvalidCorrelation) {
			t.Fatal("unsafe job ref accepted")
		}
	}
	worker.JobRef, worker.EffectRef = "river_123", "eer_0"
	if _, err := WithCorrelation(context.Background(), worker); !errors.Is(err, ErrInvalidCorrelation) {
		t.Fatal("unsafe effect ref accepted")
	}
	for _, value := range []Correlation{{RequestID: "phone-13800138000", ReleaseSHA: testRelease}, {RequestID: seen.RequestID, ReleaseSHA: "secret=must-not-log"}, {RequestID: seen.RequestID, ReleaseSHA: ""}} {
		if _, err := WithCorrelation(context.Background(), value); !errors.Is(err, ErrInvalidCorrelation) {
			t.Fatal("unsafe worker correlation accepted")
		}
	}
}
func TestPanicRecordsSafeDiagnosticAndAbortsWithoutLeakingThroughNetHTTP(t *testing.T) {
	logs := captureLogs(t)
	var serverLogs bytes.Buffer
	observations := make(chan Observation, 1)
	marker := "fixture-sensitive-panic-message"
	handler := Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(marker) }), testRelease, func(ctx context.Context, o Observation) error {
		if ctx.Err() != nil {
			t.Error("diagnostic context cancelled")
		}
		observations <- o
		return nil
	})
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(&serverLogs, "", 0)
	server.Start()
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/private-path?secret=" + marker)
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("panic became successful HTTP response")
	}
	select {
	case o := <-observations:
		if o.Code != "http_panic" || o.Status != 0 || o.RouteTemplate != "/request" || o.ReleaseSHA != testRelease || !hexToken(o.Correlation, 32) {
			t.Fatalf("bad panic observation: %+v", o)
		}
	case <-time.After(time.Second):
		t.Fatal("panic bypassed diagnostic recorder")
	}
	if strings.Contains(logs.String()+serverLogs.String(), marker) || serverLogs.Len() != 0 {
		t.Fatal("panic details escaped into logs")
	}
}
func TestPostCommitPanicDoesNotOverwriteResponseAndRecorderPanicCannotMaskIt(t *testing.T) {
	logs := captureLogs(t)
	rec := httptest.NewRecorder()
	var got Observation
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(202)
		_, _ = w.Write([]byte("accepted"))
		panic("fixture-original-sensitive")
	}), testRelease, func(_ context.Context, o Observation) error { got = o; panic("fixture-recorder-sensitive") })
	var value any
	func() { defer func() { value = recover() }(); h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil)) }()
	if value != http.ErrAbortHandler || rec.Code != 202 || rec.Body.String() != "accepted" || got.Status != 202 || got.Code != "http_panic" {
		t.Fatal("panic changed committed response or lost evidence")
	}
	if strings.Contains(logs.String(), "fixture-") {
		t.Fatal("sensitive panic contents logged")
	}
}
func TestServerErrorDiagnosticSurvivesRequestCancellationWithoutLoggingInputs(t *testing.T) {
	logs := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	var got Observation
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel(); w.WriteHeader(503) }), "unsafe-release-with-token=fixture", func(ctx context.Context, o Observation) error {
		got = o
		if ctx.Err() != nil {
			t.Fatal("parent cancellation prevented record")
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > time.Second {
			t.Fatal("unbounded recorder context")
		}
		return errors.New("fixture-sensitive-recorder-error")
	})
	req := httptest.NewRequest("POST", "/13800138000?token=fixture-private", strings.NewReader("fixture-body"))
	req.Header.Set("Authorization", "fixture-secret")
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))
	if got.Code != "http_server_error" || got.Status != 503 || got.ReleaseSHA != "unknown" {
		t.Fatalf("unexpected observation %+v", got)
	}
	for _, forbidden := range []string{"fixture-", "13800138000", "token="} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatal("sensitive request/error/release contents logged")
		}
	}
}
func TestHealthyRequestsAndIntentionalAbortDoNotCreateFalseServerErrors(t *testing.T) {
	calls := 0
	record := func(context.Context, Observation) error { calls++; return nil }
	Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), testRelease, record).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	var value any
	func() {
		defer func() { value = recover() }()
		Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }), testRelease, record).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	if calls != 0 || value != http.ErrAbortHandler {
		t.Fatal("healthy or intentional abort misreported")
	}
}

type bareWriter struct {
	header   http.Header
	body     bytes.Buffer
	statuses []int
}

func (w *bareWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *bareWriter) WriteHeader(status int)      { w.statuses = append(w.statuses, status) }
func (w *bareWriter) Write(b []byte) (int, error) { return w.body.Write(b) }

type fullWriter struct {
	bareWriter
	flushed, pushed, hijacked, read bool
	closed                          chan bool
}

func (w *fullWriter) Flush() { w.flushed = true }
func (w *fullWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return nil, nil, errors.New("fixture unsupported socket")
}
func (w *fullWriter) Push(string, *http.PushOptions) error { w.pushed = true; return nil }
func (w *fullWriter) CloseNotify() <-chan bool             { return w.closed }
func (w *fullWriter) ReadFrom(r io.Reader) (int64, error)  { w.read = true; return io.Copy(&w.body, r) }

type flushErrorWriter struct {
	fullWriter
	err error
}

func (w *flushErrorWriter) FlushError() error { return w.err }

func TestResponseControllerPreservesFlushErrors(t *testing.T) {
	want := errors.New("fixture flush failure")
	base := &flushErrorWriter{err: want}
	wrapped := preserveInterfaces(&statusWriter{ResponseWriter: base})
	if got := http.NewResponseController(wrapped).Flush(); !errors.Is(got, want) {
		t.Fatal("flush error hidden by middleware")
	}
}

func interfaceMask(w http.ResponseWriter) int {
	n := 0
	if _, ok := w.(http.Flusher); ok {
		n |= 1
	}
	if _, ok := w.(http.Hijacker); ok {
		n |= 2
	}
	if _, ok := w.(http.Pusher); ok {
		n |= 4
	}
	if _, ok := w.(http.CloseNotifier); ok {
		n |= 8
	}
	if _, ok := w.(io.ReaderFrom); ok {
		n |= 16
	}
	return n
}
func TestResponseWriterPreservesOptionalCapabilitiesAndDelegates(t *testing.T) {
	underlying := &fullWriter{closed: make(chan bool)}
	for _, original := range []http.ResponseWriter{&bareWriter{}, httptest.NewRecorder(), underlying, struct {
		http.ResponseWriter
		http.Hijacker
	}{&bareWriter{}, underlying}, struct {
		http.ResponseWriter
		http.Pusher
		io.ReaderFrom
	}{&bareWriter{}, underlying, underlying}} {
		wrapped := preserveInterfaces(&statusWriter{ResponseWriter: original})
		if interfaceMask(wrapped) != interfaceMask(original) {
			t.Fatalf("optional interfaces changed %d -> %d", interfaceMask(original), interfaceMask(wrapped))
		}
		if wrapped.(interface{ Unwrap() http.ResponseWriter }).Unwrap() != original {
			t.Fatal("ResponseController unwrap lost")
		}
	}
	status := &statusWriter{ResponseWriter: underlying}
	wrapped := preserveInterfaces(status)
	if err := http.NewResponseController(wrapped).Flush(); err != nil || !underlying.flushed || status.status != 200 {
		t.Fatal("flush lost or implicit status not recorded")
	}
	_, _, _ = wrapped.(http.Hijacker).Hijack()
	_ = wrapped.(http.Pusher).Push("/asset", nil)
	if wrapped.(http.CloseNotifier).CloseNotify() != underlying.closed {
		t.Fatal("CloseNotify lost")
	}
	n, err := wrapped.(io.ReaderFrom).ReadFrom(strings.NewReader("response body"))
	if err != nil || n != 13 || !underlying.read || !underlying.pushed || !underlying.hijacked || underlying.body.String() != "response body" {
		t.Fatal("optional operation did not delegate")
	}
	if err := http.NewResponseController(preserveInterfaces(&statusWriter{ResponseWriter: &bareWriter{}})).Flush(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatal("unsupported flush was falsely advertised")
	}
}
func TestInformationalHeadersDoNotReplaceCommittedStatus(t *testing.T) {
	base := &bareWriter{}
	w := &statusWriter{ResponseWriter: base}
	w.WriteHeader(103)
	w.WriteHeader(503)
	w.WriteHeader(200)
	if w.status != 503 || len(base.statuses) != 2 {
		t.Fatal("final status observation drifted")
	}
	upgraded := &statusWriter{ResponseWriter: &bareWriter{}}
	upgraded.WriteHeader(101)
	upgraded.WriteHeader(200)
	if upgraded.status != 101 {
		t.Fatal("upgrade status overwritten")
	}
}

func TestRegisteredRouteKeepsTemplateWithoutCustomerPath(t *testing.T) {
	logs := captureLogs(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /customers/{customerID}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	var got Observation
	Middleware(mux, testRelease, func(_ context.Context, o Observation) error { got = o; return nil }).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/customers/private-customer?token=fixture-secret", nil))
	if got.RouteTemplate != "/customers/{customerID}" {
		t.Fatalf("route=%q", got.RouteTemplate)
	}
	if strings.Contains(logs.String(), "private-customer") || strings.Contains(logs.String(), "fixture-secret") {
		t.Fatal("request values leaked")
	}
}

func TestRegisteredRouteNormalizesOnlyStaticSupportedTemplates(t *testing.T) {
	for input, want := range map[string]string{
		"GET /customers/{customerID}": "/customers/{customerID}",
		"POST /jobs/{job_id}/retry":   "/jobs/{job_id}/retry",
		"/api/admin/ops":              "/api/admin/ops",
		"":                            "/request",
		"GET /":                       "/request",
		"CONNECT /secret":             "/request",
		"GET /private?token=secret":   "/request",
		"GET  /double-space":          "/request",
		"GET /private secret":         "/request",
	} {
		if got := registeredRoute(input); got != want {
			t.Errorf("registeredRoute(%q)=%q, want %q", input, got, want)
		}
	}
}
