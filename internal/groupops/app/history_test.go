package app

import (
	"context"
	"testing"

	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

type historyReaderStub struct{}

func (historyReaderStub) ListHistoricalPlans(context.Context, int32, int32) ([]groupopsport.HistoricalPlan, int64, error) {
	return []groupopsport.HistoricalPlan{}, 0, nil
}
func (historyReaderStub) ListHistoricalDirectory(context.Context, int32, int32) ([]groupopsport.HistoricalDirectory, int64, error) {
	return []groupopsport.HistoricalDirectory{}, 0, nil
}
func (historyReaderStub) ListHistoricalGroups(context.Context, int64, int32, int32) ([]groupopsport.HistoricalGroup, int64, error) {
	return []groupopsport.HistoricalGroup{}, 0, nil
}
func (historyReaderStub) ListHistoricalNodes(context.Context, int64, int32, int32) ([]groupopsport.HistoricalNode, int64, error) {
	return []groupopsport.HistoricalNode{}, 0, nil
}

// partialHistoryReader models a stale count/read snapshot. Returning it as a
// successful donor page would make the frozen adapter reject the response, so
// the service must fail closed with 503 instead.
type partialHistoryReader struct{}

func (partialHistoryReader) ListHistoricalPlans(context.Context, int32, int32) ([]groupopsport.HistoricalPlan, int64, error) {
	return []groupopsport.HistoricalPlan{}, 1, nil
}
func (partialHistoryReader) ListHistoricalDirectory(context.Context, int32, int32) ([]groupopsport.HistoricalDirectory, int64, error) {
	return []groupopsport.HistoricalDirectory{}, 1, nil
}
func (partialHistoryReader) ListHistoricalGroups(context.Context, int64, int32, int32) ([]groupopsport.HistoricalGroup, int64, error) {
	return []groupopsport.HistoricalGroup{}, 1, nil
}
func (partialHistoryReader) ListHistoricalNodes(context.Context, int64, int32, int32) ([]groupopsport.HistoricalNode, int64, error) {
	return []groupopsport.HistoricalNode{}, 1, nil
}

func TestHistoryServiceReturnsRealEmptyV3Pages(t *testing.T) {
	service := NewHistoryService(testUOW{}, historyReaderStub{})
	plans, err := service.ListHistoricalPlans(context.Background(), 20, 0)
	if err != nil || plans.Source != "v1_history" || !plans.ReadOnly || plans.RealExternalCallExecuted || plans.Items == nil || plans.Total != 0 {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
	groups, err := service.ListHistoricalGroups(context.Background(), 7, 20, 0)
	if err != nil || groups.PlanID != 7 || !groups.ReadOnly || groups.Items == nil {
		t.Fatalf("groups=%#v err=%v", groups, err)
	}
}

func TestHistoryServiceRejectsInvalidPage(t *testing.T) {
	service := NewHistoryService(testUOW{}, historyReaderStub{})
	if _, err := service.ListHistoricalDirectory(context.Background(), 0, 0); err != ErrInvalid {
		t.Fatalf("err=%v", err)
	}
}

func TestHistoryServiceFailsClosedForPartialPages(t *testing.T) {
	service := NewHistoryService(testUOW{}, partialHistoryReader{})
	for _, read := range []func() error{
		func() error { _, err := service.ListHistoricalPlans(context.Background(), 20, 0); return err },
		func() error { _, err := service.ListHistoricalDirectory(context.Background(), 20, 0); return err },
		func() error { _, err := service.ListHistoricalGroups(context.Background(), 7, 20, 0); return err },
		func() error { _, err := service.ListHistoricalNodes(context.Background(), 7, 20, 0); return err },
	} {
		if err := read(); err != ErrUnavailable {
			t.Fatalf("partial history page err=%v", err)
		}
	}
}
