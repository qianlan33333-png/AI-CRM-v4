// Package runtime assembles transport-level runtime primitives.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

type Readiness interface {
	Check(context.Context) error
}

type ReadinessFunc func(context.Context) error

func (function ReadinessFunc) Check(ctx context.Context) error {
	if function == nil {
		return errors.New("readiness checker is not configured")
	}
	return function(ctx)
}

type HandlerOptions struct {
	ReleaseSHA string
	Readiness  Readiness
}

func NewHandler(options HandlerOptions) (http.Handler, error) {
	if options.ReleaseSHA == "" || options.Readiness == nil {
		return nil, errors.New("invalid runtime handler options")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"status":      "alive",
			"release_sha": options.ReleaseSHA,
		})
	})
	mux.HandleFunc("GET /readyz", func(writer http.ResponseWriter, request *http.Request) {
		if err := options.Readiness.Check(request.Context()); err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]any{
				"status":      "not_ready",
				"release_sha": options.ReleaseSHA,
			})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"status":      "ready",
			"release_sha": options.ReleaseSHA,
		})
	})
	return mux, nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "private, no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
