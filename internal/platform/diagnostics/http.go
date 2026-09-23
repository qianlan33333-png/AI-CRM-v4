// Package diagnostics carries opaque local correlation references only.
package diagnostics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type key struct{}
type Correlation struct{ RequestID, ReleaseSHA, JobRef, EffectRef string }

var ErrInvalidCorrelation = errors.New("invalid diagnostic correlation")

var routePattern = regexp.MustCompile(`^(/[a-zA-Z_{}][a-zA-Z0-9_{}.-]*)+/?$`)

func registeredRoute(pattern string) string {
	if method, path, found := strings.Cut(pattern, " "); found {
		switch method {
		case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
			pattern = path
		default:
			return "/request"
		}
	}
	if len(pattern) <= 190 && routePattern.MatchString(pattern) {
		return pattern
	}
	return "/request"
}

func FromContext(ctx context.Context) Correlation { v, _ := ctx.Value(key{}).(Correlation); return v }

// WithCorrelation is for trusted composition/worker code. HTTP middleware always
// generates a fresh ID instead of accepting a caller's correlation header.
func WithCorrelation(ctx context.Context, value Correlation) (context.Context, error) {
	if ctx == nil || !hexToken(value.RequestID, 32) || !validRelease(value.ReleaseSHA) || !opaqueReference(value.JobRef, "river_") || !opaqueReference(value.EffectRef, "eer_") {
		return ctx, ErrInvalidCorrelation
	}
	return context.WithValue(ctx, key{}, value), nil
}
func opaqueReference(value, prefix string) bool {
	if value == "" {
		return true
	}
	if !strings.HasPrefix(value, prefix) || len(value) > len(prefix)+19 {
		return false
	}
	number := strings.TrimPrefix(value, prefix)
	if number == "" || number[0] == '0' {
		return false
	}
	for _, c := range number {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func hexToken(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
func validRelease(value string) bool {
	return value == "unknown" || hexToken(value, 40) || hexToken(value, 64)
}

type Observation struct {
	Code, Correlation, RouteTemplate, ReleaseSHA, JobRef, EffectRef string
	// JobAttempt is River's attempt number, not the Provider attempt number.
	// Zero means no job or a legacy observation without attempt evidence.
	JobAttempt int
	Status     int
	Duration   time.Duration
}
type Recorder func(context.Context, Observation) error

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Middleware never trusts inbound IDs, URLs, bodies, error/panic text or stack
// traces. Invalid release metadata becomes unknown instead of entering logs.
func Middleware(next http.Handler, release string, record Recorder) http.Handler {
	if !validRelease(release) {
		release = "unknown"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var bytes [16]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			http.Error(w, "request unavailable", http.StatusServiceUnavailable)
			return
		}
		id := hex.EncodeToString(bytes[:])
		w.Header().Set("X-AICRM-Diagnostic-ID", id)
		ctx, _ := WithCorrelation(r.Context(), Correlation{RequestID: id, ReleaseSHA: release})
		r = r.WithContext(ctx)
		writer := &statusWriter{ResponseWriter: w}
		start := time.Now()
		defer func() {
			recovered := recover()
			if recovered == http.ErrAbortHandler {
				panic(http.ErrAbortHandler)
			}
			duration := time.Since(start)
			status := writer.status
			code := "http_slow"
			if recovered != nil {
				code = "http_panic"
				// Status 0 means no response was committed, not an invented 500.
			} else {
				if status == 0 {
					status = http.StatusOK
				}
				if status < 500 && duration < 3*time.Second {
					return
				}
				if status >= 500 {
					code = "http_server_error"
				}
			}
			observation := Observation{Code: code, Correlation: id, RouteTemplate: registeredRoute(r.Pattern), ReleaseSHA: release, Status: status, Duration: duration}
			emit(ctx, record, observation)
			if recovered != nil {
				// net/http logs arbitrary panic values and stack traces by default.
				// This sentinel preserves connection-abort semantics without leaking
				// those values, and never overwrites a partially committed response.
				panic(http.ErrAbortHandler)
			}
		}()
		next.ServeHTTP(preserveInterfaces(writer), r)
	})
}
func emit(ctx context.Context, record Recorder, observation Observation) {
	// Diagnostics must not turn an otherwise completed request into a panic.
	defer func() {
		if recover() != nil {
			slog.WarnContext(ctx, "diagnostic storage unavailable", "code", "diagnostic_store_unavailable", "diagnostic_id", observation.Correlation)
		}
	}()
	slog.WarnContext(ctx, "request diagnostic", "diagnostic_id", observation.Correlation, "release_sha", observation.ReleaseSHA, "code", observation.Code, "route_template", observation.RouteTemplate, "status", observation.Status, "duration_ms", observation.Duration.Milliseconds())
	if record == nil {
		return
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	if err := record(recordCtx, observation); err != nil {
		slog.WarnContext(ctx, "diagnostic storage unavailable", "code", "diagnostic_store_unavailable", "diagnostic_id", observation.Correlation)
	}
}
