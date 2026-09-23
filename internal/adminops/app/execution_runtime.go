package app

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
)

const (
	ExecutionRuntimeMaximumDepth = 12
	ExecutionRuntimeMaximumNodes = 256
	ExecutionRuntimeMaximumItems = 1024
	ExecutionRuntimeMaximumID    = 100
	ExecutionRuntimeMaximumText  = 1024
)

var (
	ErrExecutionRuntimeUnavailable  = errors.New("execution_runtime_unavailable")
	ErrExecutionTimelineUnavailable = errors.New("execution_timeline_unavailable")
	ErrExecutionNotFound            = errors.New("execution_not_found")
)

// ExecutionRuntimeService prepares the local, read-only diagnostic model for
// the legacy execution-runtime routes. HTTP status mapping stays with the
// future adapter: a missing control is a successful Runtime result with OK
// false; the unavailable sentinels map to 503; ErrExecutionNotFound maps to
// the same 404 for invalid and absent execution IDs.
type ExecutionRuntimeService struct {
	reader adminopsport.ExecutionRuntimeReader
}

func NewExecutionRuntimeService(reader adminopsport.ExecutionRuntimeReader) *ExecutionRuntimeService {
	return &ExecutionRuntimeService{reader: reader}
}

type ExecutionRuntime struct {
	OK           bool
	Control      *adminopsport.RuntimeControl
	Observations []adminopsport.RuntimeObservation
	Truncated    bool
	ObservedAt   time.Time
}

func (service *ExecutionRuntimeService) Runtime(ctx context.Context) (ExecutionRuntime, error) {
	if ctx == nil || ctx.Err() != nil || service == nil || service.reader == nil {
		return ExecutionRuntime{}, ErrExecutionRuntimeUnavailable
	}
	snapshot, err := service.reader.ReadExecutionRuntime(ctx)
	if err != nil {
		return ExecutionRuntime{}, ErrExecutionRuntimeUnavailable
	}
	observations, truncated := redactObservations(snapshot.Observations, ExecutionRuntimeMaximumItems)
	truncated = truncated || controlTruncated(snapshot.Control)
	return ExecutionRuntime{
		OK:           snapshot.Control != nil,
		Control:      redactControl(snapshot.Control),
		Observations: observations,
		Truncated:    truncated,
		ObservedAt:   snapshot.ObservedAt.UTC(),
	}, nil
}

func (service *ExecutionRuntimeService) Timeline(ctx context.Context, executionID string) (adminopsport.ExecutionTimeline, error) {
	executionID = strings.TrimSpace(executionID)
	if !validExecutionID(executionID) {
		return adminopsport.ExecutionTimeline{}, ErrExecutionNotFound
	}
	if ctx == nil || ctx.Err() != nil || service == nil || service.reader == nil {
		return adminopsport.ExecutionTimeline{}, ErrExecutionTimelineUnavailable
	}
	timeline, found, err := service.reader.ReadExecutionTimeline(ctx, executionID)
	if err != nil {
		return adminopsport.ExecutionTimeline{}, ErrExecutionTimelineUnavailable
	}
	if !found {
		return adminopsport.ExecutionTimeline{}, ErrExecutionNotFound
	}
	timeline.ExecutionID = executionID
	timeline.ObservedAt = timeline.ObservedAt.UTC()
	timeline.Graph = boundedGraph(timeline.Graph)
	return timeline, nil
}

func validExecutionID(value string) bool {
	return strings.HasPrefix(value, "exe_") && len(value) > len("exe_") && len(value) <= ExecutionRuntimeMaximumID
}

func boundedGraph(graph adminopsport.ExecutionGraph) adminopsport.ExecutionGraph {
	result := adminopsport.ExecutionGraph{Truncated: graph.Truncated}
	nodes := 0
	for _, root := range graph.Roots {
		copy, truncated := boundedNode(root, 1, &nodes)
		if truncated {
			result.Truncated = true
		}
		if copy.ID != "" {
			result.Roots = append(result.Roots, copy)
		}
	}
	if len(graph.Items) > ExecutionRuntimeMaximumItems {
		result.Truncated = true
	}
	items, truncated := redactObservations(graph.Items, ExecutionRuntimeMaximumItems)
	result.Items = items
	result.Truncated = result.Truncated || truncated
	return result
}

func boundedNode(node adminopsport.ExecutionGraphNode, depth int, count *int) (adminopsport.ExecutionGraphNode, bool) {
	if depth > ExecutionRuntimeMaximumDepth || *count >= ExecutionRuntimeMaximumNodes {
		return adminopsport.ExecutionGraphNode{}, true
	}
	*count += 1
	result := adminopsport.ExecutionGraphNode{
		ID:         boundedText(node.ID),
		Kind:       boundedText(node.Kind),
		Status:     boundedText(node.Status),
		Message:    redactText("message", node.Message),
		Details:    redactDetails(node.Details),
		ObservedAt: node.ObservedAt.UTC(),
	}
	truncated := nodeTruncated(node)
	for _, child := range node.Children {
		copy, childTruncated := boundedNode(child, depth+1, count)
		if childTruncated {
			truncated = true
		}
		if copy.ID != "" {
			result.Children = append(result.Children, copy)
		}
	}
	return result, truncated
}

func redactControl(control *adminopsport.RuntimeControl) *adminopsport.RuntimeControl {
	if control == nil {
		return nil
	}
	return &adminopsport.RuntimeControl{
		Name:       boundedText(control.Name),
		State:      boundedText(control.State),
		Details:    redactDetails(control.Details),
		ObservedAt: control.ObservedAt.UTC(),
	}
}

func controlTruncated(control *adminopsport.RuntimeControl) bool {
	return control != nil && (textTruncated(control.Name) || textTruncated(control.State) || detailsTruncated(control.Details))
}

func redactObservations(values []adminopsport.RuntimeObservation, limit int) ([]adminopsport.RuntimeObservation, bool) {
	truncated := len(values) > limit
	if len(values) > limit {
		values = values[:limit]
	}
	result := make([]adminopsport.RuntimeObservation, 0, len(values))
	for _, value := range values {
		truncated = truncated || observationTruncated(value)
		result = append(result, adminopsport.RuntimeObservation{
			Source:     boundedText(value.Source),
			Queue:      boundedText(value.Queue),
			Status:     boundedText(value.Status),
			Attempt:    value.Attempt,
			StatusURL:  redactURL(value.StatusURL),
			Details:    redactDetails(value.Details),
			ObservedAt: value.ObservedAt.UTC(),
		})
	}
	return result, truncated
}

func observationTruncated(value adminopsport.RuntimeObservation) bool {
	return textTruncated(value.Source) || textTruncated(value.Queue) || textTruncated(value.Status) || textTruncated(value.StatusURL) || detailsTruncated(value.Details)
}

func nodeTruncated(node adminopsport.ExecutionGraphNode) bool {
	return textTruncated(node.ID) || textTruncated(node.Kind) || textTruncated(node.Status) || detailsTruncated(node.Details)
}

func detailsTruncated(values map[string]string) bool {
	for key, value := range values {
		if textTruncated(key) || safeObservationDetail(key) && textTruncated(value) {
			return true
		}
	}
	return false
}

func redactDetails(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = boundedText(key)
		if safeObservationDetail(key) {
			result[key] = boundedText(value)
		} else {
			result[key] = "[REDACTED]"
		}
	}
	return result
}

func redactText(key, value string) string {
	if value == "" {
		return ""
	}
	lower := strings.ToLower(strings.TrimSpace(key))
	for _, sensitive := range []string{"message", "token", "secret", "password", "authorization", "cookie", "external_user", "user_id", "mobile", "phone", "email", "openid", "unionid", "userid"} {
		if strings.Contains(lower, sensitive) {
			return "[REDACTED]"
		}
	}
	if strings.Contains(lower, "url") {
		return redactURL(value)
	}
	return boundedText(value)
}

func safeObservationDetail(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "attempt", "available", "code", "depth", "failed", "lag_ms", "node_count", "pending", "queue_depth", "ready", "retry", "running", "state", "status", "worker_count":
		return true
	}
	return false
}

func redactURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "[REDACTED]"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return boundedText(parsed.String())
}

func boundedText(value string) string {
	if len(value) <= ExecutionRuntimeMaximumText {
		return value
	}
	return value[:ExecutionRuntimeMaximumText]
}

func textTruncated(value string) bool { return len(value) > ExecutionRuntimeMaximumText }
