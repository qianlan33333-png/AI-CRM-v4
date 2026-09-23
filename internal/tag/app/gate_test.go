package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

type gateReader struct {
	uow     *catalogUOW
	status  tagport.ExecutionStatus
	calls   int
	readErr error
}

func (reader *gateReader) ReadExecutionStatus(_ context.Context) (tagport.ExecutionStatus, error) {
	if reader.uow == nil || !reader.uow.in {
		return tagport.ExecutionStatus{}, errors.New("status read outside uow")
	}
	reader.calls++
	if reader.readErr != nil {
		return tagport.ExecutionStatus{}, reader.readErr
	}
	return reader.status, nil
}

func validGateStatus(t *testing.T) tagport.ExecutionStatus {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"mode":                        "provider_execution_unavailable",
		"accepted":                    true,
		"queued":                      true,
		"attempted":                   false,
		"executed":                    false,
		"outcome_unknown":             false,
		"reconciled":                  false,
		"real_external_call_executed": false,
		"sync_executed":               false,
		"future_private_field":        "discard",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tagport.ExecutionStatus{
		Payload:    payload,
		ObservedAt: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
	}
}

func TestExecutionStatusServiceProjectsOnlyFailClosedGate(t *testing.T) {
	uow := &catalogUOW{}
	reader := &gateReader{uow: uow, status: validGateStatus(t)}
	service := NewExecutionStatusService(uow, reader)
	got, err := service.Get(context.Background())
	want := domain.ExecutionGate{
		LocalCommandAcceptanceAvailable: true,
		LocalQueueAvailable:             true,
		ObservedAt:                      time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
	}
	if err != nil || got != want || !got.Valid() {
		t.Fatalf("Get() = %#v, %v", got, err)
	}
	if reader.calls != 1 || uow.calls != 1 {
		t.Fatalf("reader/uow calls = %d/%d", reader.calls, uow.calls)
	}
}

func TestExecutionStatusServiceRejectsProviderOrMalformedStatus(t *testing.T) {
	base := validGateStatus(t)
	for name, mutate := range map[string]func(map[string]any){
		"provider mode": func(payload map[string]any) { payload["mode"] = "provider_enabled" },
		"attempted":     func(payload map[string]any) { payload["attempted"] = true },
		"sync executed": func(payload map[string]any) { payload["sync_executed"] = true },
		"malformed":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			uow := &catalogUOW{}
			status := base
			if mutate == nil {
				status.Payload = []byte("not-json")
			} else {
				var payload map[string]any
				if err := json.Unmarshal(status.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				mutate(payload)
				status.Payload, _ = json.Marshal(payload)
			}
			_, err := NewExecutionStatusService(uow, &gateReader{uow: uow, status: status}).Get(context.Background())
			if !errors.Is(err, ErrInvalidExecutionStatus) {
				t.Fatalf("Get() error = %v, want invalid status", err)
			}
		})
	}
}

func TestExecutionStatusServiceWrapsReaderFailureAndSkipsCanceledContext(t *testing.T) {
	uow := &catalogUOW{}
	reader := &gateReader{uow: uow, status: validGateStatus(t), readErr: errors.New("status store unavailable")}
	_, err := NewExecutionStatusService(uow, reader).Get(context.Background())
	if !errors.Is(err, ErrExecutionUnavailable) || !errors.Is(err, reader.readErr) {
		t.Fatalf("reader error = %v", err)
	}
	reader.calls = 0
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = NewExecutionStatusService(uow, reader).Get(canceled)
	if !errors.Is(err, ErrExecutionUnavailable) || reader.calls != 0 || uow.calls != 1 {
		t.Fatalf("canceled gate = %v calls reader/uow %d/%d", err, reader.calls, uow.calls)
	}
}
