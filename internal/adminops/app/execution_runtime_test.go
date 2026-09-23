package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
)

func TestExecutionRuntimeNormalizesAndRedactsObservationData(t *testing.T) {
	observed := time.Date(2026, 8, 16, 8, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	reader := &executionRuntimeReaderStub{snapshot: adminopsport.RuntimeSnapshot{
		Control:      &adminopsport.RuntimeControl{Name: "runtime-control", State: "ready", Details: map[string]string{"api_token": "must-not-leak"}, ObservedAt: observed},
		Observations: []adminopsport.RuntimeObservation{{Source: "channel_entry", Queue: "channel", Status: "observed", Attempt: 2, StatusURL: "https://media.example.test/status?id=1&token=secret", Details: map[string]string{"external_userid": "wx-user", "queue_depth": "3"}, ObservedAt: observed}},
		ObservedAt:   observed,
	}}
	result, err := NewExecutionRuntimeService(reader).Runtime(context.Background())
	if err != nil {
		t.Fatalf("Runtime() error = %v", err)
	}
	if !result.OK || result.Control == nil || result.Control.Details["api_token"] != "[REDACTED]" {
		t.Fatalf("control = %+v", result.Control)
	}
	item := result.Observations[0]
	if item.StatusURL != "https://media.example.test/status" || item.Details["external_userid"] != "[REDACTED]" || item.Details["queue_depth"] != "3" {
		t.Fatalf("observation = %+v", item)
	}
	if result.ObservedAt.Location() != time.UTC || item.ObservedAt.Location() != time.UTC {
		t.Fatalf("timestamps not UTC: runtime=%s item=%s", result.ObservedAt, item.ObservedAt)
	}
	result.Observations[0].Details["queue_depth"] = "changed"
	if reader.snapshot.Observations[0].Details["queue_depth"] != "3" {
		t.Fatal("runtime response leaked reader-owned map")
	}
}

func TestExecutionRuntimeMissingControlIsSuccessfulFalse(t *testing.T) {
	result, err := NewExecutionRuntimeService(&executionRuntimeReaderStub{snapshot: adminopsport.RuntimeSnapshot{ObservedAt: time.Now()}}).Runtime(context.Background())
	if err != nil || result.OK || result.Control != nil {
		t.Fatalf("Runtime() = %+v, %v; want successful ok:false", result, err)
	}
}

func TestExecutionRuntimeBoundsObservationList(t *testing.T) {
	items := make([]adminopsport.RuntimeObservation, ExecutionRuntimeMaximumItems+1)
	result, err := NewExecutionRuntimeService(&executionRuntimeReaderStub{snapshot: adminopsport.RuntimeSnapshot{Observations: items}}).Runtime(context.Background())
	if err != nil || !result.Truncated || len(result.Observations) != ExecutionRuntimeMaximumItems {
		t.Fatalf("Runtime() = %+v, %v", result, err)
	}
}

func TestExecutionRuntimeBoundsTextAndMarksItTruncated(t *testing.T) {
	longStatus := strings.Repeat("x", ExecutionRuntimeMaximumText+1)
	result, err := NewExecutionRuntimeService(&executionRuntimeReaderStub{snapshot: adminopsport.RuntimeSnapshot{Observations: []adminopsport.RuntimeObservation{{Status: longStatus}}}}).Runtime(context.Background())
	if err != nil || !result.Truncated || len(result.Observations[0].Status) != ExecutionRuntimeMaximumText {
		t.Fatalf("Runtime() = %+v, %v", result, err)
	}
}

func TestExecutionRuntimeFailsClosedWhenReadFails(t *testing.T) {
	for _, service := range []*ExecutionRuntimeService{
		NewExecutionRuntimeService(nil),
		NewExecutionRuntimeService(&executionRuntimeReaderStub{runtimeErr: errors.New("database unavailable")}),
	} {
		if _, err := service.Runtime(context.Background()); !errors.Is(err, ErrExecutionRuntimeUnavailable) {
			t.Fatalf("Runtime() error = %v", err)
		}
	}
}

func TestExecutionTimelineNormalizesIDAndUsesUnifiedNotFound(t *testing.T) {
	reader := &executionRuntimeReaderStub{timeline: adminopsport.ExecutionTimeline{ExecutionID: "wrong", ObservedAt: time.Now()}, found: true}
	service := NewExecutionRuntimeService(reader)
	result, err := service.Timeline(context.Background(), "  exe_valid  ")
	if err != nil || reader.executionID != "exe_valid" || result.ExecutionID != "exe_valid" {
		t.Fatalf("Timeline() = %+v, %v reader ID=%q", result, err, reader.executionID)
	}
	for _, value := range []string{"", " exe_ ", "job_1", "exe_" + strings.Repeat("x", ExecutionRuntimeMaximumID)} {
		if _, err := service.Timeline(context.Background(), value); !errors.Is(err, ErrExecutionNotFound) {
			t.Fatalf("Timeline(%q) error = %v, want unified not found", value, err)
		}
	}
	reader.found = false
	if _, err := service.Timeline(context.Background(), "exe_missing"); !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("missing Timeline() error = %v", err)
	}
}

func TestExecutionTimelineBoundsGraphAndRedacts(t *testing.T) {
	root := graphNode("root", 1)
	for index := 0; index < ExecutionRuntimeMaximumNodes+20; index++ {
		root.Children = append(root.Children, graphNode("child-"+string(rune('a'+index%26)), 2))
	}
	deep := graphNode("deep", 1)
	current := &deep
	for index := 0; index < ExecutionRuntimeMaximumDepth+2; index++ {
		current.Children = []adminopsport.ExecutionGraphNode{graphNode("nested", index+2)}
		current = &current.Children[0]
	}
	items := make([]adminopsport.RuntimeObservation, ExecutionRuntimeMaximumItems+1)
	items[0] = adminopsport.RuntimeObservation{StatusURL: "https://wecom.example.test/media/status?access_token=leak", Details: map[string]string{"secret_ref": "leak"}}
	reader := &executionRuntimeReaderStub{timeline: adminopsport.ExecutionTimeline{Graph: adminopsport.ExecutionGraph{Roots: []adminopsport.ExecutionGraphNode{deep, root}, Items: items}}, found: true}
	result, err := NewExecutionRuntimeService(reader).Timeline(context.Background(), "exe_graph")
	if err != nil {
		t.Fatalf("Timeline() error = %v", err)
	}
	if !result.Graph.Truncated || countGraphNodes(result.Graph.Roots) > ExecutionRuntimeMaximumNodes || graphDepth(result.Graph.Roots) != ExecutionRuntimeMaximumDepth || len(result.Graph.Items) != ExecutionRuntimeMaximumItems {
		t.Fatalf("bounded graph = %+v", result.Graph)
	}
	if got := result.Graph.Items[0]; got.StatusURL != "https://wecom.example.test/media/status" || got.Details["secret_ref"] != "[REDACTED]" {
		t.Fatalf("item was not redacted: %+v", got)
	}
}

func TestExecutionTimelineUnavailableAndConcurrentReads(t *testing.T) {
	reader := &executionRuntimeReaderStub{timeline: adminopsport.ExecutionTimeline{}, found: true}
	service := NewExecutionRuntimeService(reader)
	var group sync.WaitGroup
	errCh := make(chan error, 32)
	for index := 0; index < cap(errCh); index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := service.Timeline(context.Background(), "exe_concurrent")
			errCh <- err
		}()
	}
	group.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent Timeline() error = %v", err)
		}
	}
	reader.timelineErr = errors.New("store failure")
	if _, err := service.Timeline(context.Background(), "exe_failure"); !errors.Is(err, ErrExecutionTimelineUnavailable) {
		t.Fatalf("Timeline() error = %v", err)
	}
}

type executionRuntimeReaderStub struct {
	mu          sync.Mutex
	snapshot    adminopsport.RuntimeSnapshot
	timeline    adminopsport.ExecutionTimeline
	found       bool
	runtimeErr  error
	timelineErr error
	executionID string
}

func (stub *executionRuntimeReaderStub) ReadExecutionRuntime(context.Context) (adminopsport.RuntimeSnapshot, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return stub.snapshot, stub.runtimeErr
}

func (stub *executionRuntimeReaderStub) ReadExecutionTimeline(_ context.Context, executionID string) (adminopsport.ExecutionTimeline, bool, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.executionID = executionID
	return stub.timeline, stub.found, stub.timelineErr
}

func graphNode(id string, depth int) adminopsport.ExecutionGraphNode {
	return adminopsport.ExecutionGraphNode{ID: id, Kind: "attempt", Status: "observed", Message: strings.Repeat("x", depth)}
}

func countGraphNodes(nodes []adminopsport.ExecutionGraphNode) int {
	count := 0
	for _, node := range nodes {
		count += 1 + countGraphNodes(node.Children)
	}
	return count
}

func graphDepth(nodes []adminopsport.ExecutionGraphNode) int {
	maximum := 0
	for _, node := range nodes {
		depth := 1 + graphDepth(node.Children)
		if depth > maximum {
			maximum = depth
		}
	}
	return maximum
}
