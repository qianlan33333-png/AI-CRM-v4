package main

import (
	"errors"
	"net/http"
	"strings"
)

// mountSegmentAPI keeps the established router signature stable while Segment
// is introduced. It owns only its canonical prefix and delegates everything
// else to the existing application handler.
func mountSegmentAPI(next, audience http.Handler) (http.Handler, error) {
	if next == nil || audience == nil {
		return nil, errors.New("segment route dependencies are required")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/admin/ai-audience/") {
			audience.ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	}), nil
}

func mountAutomationRuntimeAPI(next, runtime http.Handler) (http.Handler, error) {
	if next == nil || runtime == nil {
		return nil, errors.New("automation runtime route dependencies are required")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		isPackageRuntime := strings.HasPrefix(request.URL.Path, "/api/admin/ai-audience/packages/") && (strings.HasSuffix(request.URL.Path, "/broadcast-previews") || strings.HasSuffix(request.URL.Path, "/runs"))
		isPolicyRuntime := request.URL.Path == "/api/admin/automations" || strings.HasPrefix(request.URL.Path, "/api/admin/automations/")
		isRunRuntime := request.URL.Path == "/api/admin/automation-runs" || strings.HasPrefix(request.URL.Path, "/api/admin/automation-runs/")
		if isPackageRuntime || isPolicyRuntime || isRunRuntime {
			runtime.ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	}), nil
}

func mountSegmentWebhook(next, webhook http.Handler) (http.Handler, error) {
	if next == nil || webhook == nil {
		return nil, errors.New("segment webhook route dependencies are required")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/integrations/ai-audience/") {
			webhook.ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	}), nil
}

func mountAudienceDirectPush(next, webhook http.Handler) (http.Handler, error) {
	if next == nil || webhook == nil {
		return nil, errors.New("audience direct push route dependencies are required")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/automation/audience/webhooks/") {
			webhook.ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	}), nil
}

func mountAudienceDirectPushAdmin(next, admin http.Handler) (http.Handler, error) {
	if next == nil || admin == nil {
		return nil, errors.New("audience direct push admin route dependencies are required")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/admin/ai-audience/packages/") && (strings.HasSuffix(request.URL.Path, "/direct-push") || strings.HasSuffix(request.URL.Path, "/direct-pushes")) {
			admin.ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	}), nil
}
