package app

import (
	"context"

	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// HistoryService is the v3-owned, read-only host binding for the frozen
// Group Ops history screens. OneID is not involved: these sealed operational
// records neither resolve customers nor express current ownership. Provider
// execution is not involved: all reads are local PostgreSQL facts.
type HistoryService struct {
	uow    platformport.UnitOfWork
	reader groupopsport.HistoricalReader
}

func NewHistoryService(uow platformport.UnitOfWork, reader groupopsport.HistoricalReader) *HistoryService {
	return &HistoryService{uow: uow, reader: reader}
}

func (s *HistoryService) ListHistoricalPlans(ctx context.Context, limit, offset int32) (groupopsport.HistoricalPlanPage, error) {
	if !historyReady(s) || !validPage(limit, offset) {
		return groupopsport.HistoricalPlanPage{}, historyInvalidOrUnavailable(s)
	}
	result := groupopsport.HistoricalPlanPage{Source: "v1_history", ReadOnly: true, Items: []groupopsport.HistoricalPlan{}, Limit: limit, Offset: offset}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result.Items, result.Total, err = s.reader.ListHistoricalPlans(tx, limit, offset)
		return err
	})
	if err != nil {
		return groupopsport.HistoricalPlanPage{}, classifyHistory(err)
	}
	if !validHistoricalPage(result.Items, result.Total, limit, offset) {
		return groupopsport.HistoricalPlanPage{}, ErrUnavailable
	}
	return result, nil
}

func (s *HistoryService) ListHistoricalDirectory(ctx context.Context, limit, offset int32) (groupopsport.HistoricalDirectoryPage, error) {
	if !historyReady(s) || !validPage(limit, offset) {
		return groupopsport.HistoricalDirectoryPage{}, historyInvalidOrUnavailable(s)
	}
	result := groupopsport.HistoricalDirectoryPage{Source: "v1_history", ReadOnly: true, Items: []groupopsport.HistoricalDirectory{}, Limit: limit, Offset: offset}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result.Items, result.Total, err = s.reader.ListHistoricalDirectory(tx, limit, offset)
		return err
	})
	if err != nil {
		return groupopsport.HistoricalDirectoryPage{}, classifyHistory(err)
	}
	if !validHistoricalPage(result.Items, result.Total, limit, offset) {
		return groupopsport.HistoricalDirectoryPage{}, ErrUnavailable
	}
	return result, nil
}

func (s *HistoryService) ListHistoricalGroups(ctx context.Context, planID int64, limit, offset int32) (groupopsport.HistoricalGroupPage, error) {
	if !historyReady(s) || planID < 1 || !validPage(limit, offset) {
		return groupopsport.HistoricalGroupPage{}, historyInvalidOrUnavailable(s)
	}
	result := groupopsport.HistoricalGroupPage{Source: "v1_history", ReadOnly: true, Items: []groupopsport.HistoricalGroup{}, Limit: limit, Offset: offset, PlanID: planID}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result.Items, result.Total, err = s.reader.ListHistoricalGroups(tx, planID, limit, offset)
		return err
	})
	if err != nil {
		return groupopsport.HistoricalGroupPage{}, classifyHistory(err)
	}
	if !validHistoricalPage(result.Items, result.Total, limit, offset) {
		return groupopsport.HistoricalGroupPage{}, ErrUnavailable
	}
	return result, nil
}

func (s *HistoryService) ListHistoricalNodes(ctx context.Context, planID int64, limit, offset int32) (groupopsport.HistoricalNodePage, error) {
	if !historyReady(s) || planID < 1 || !validPage(limit, offset) {
		return groupopsport.HistoricalNodePage{}, historyInvalidOrUnavailable(s)
	}
	result := groupopsport.HistoricalNodePage{Source: "v1_history", ReadOnly: true, Items: []groupopsport.HistoricalNode{}, Limit: limit, Offset: offset, PlanID: planID}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result.Items, result.Total, err = s.reader.ListHistoricalNodes(tx, planID, limit, offset)
		return err
	})
	if err != nil {
		return groupopsport.HistoricalNodePage{}, classifyHistory(err)
	}
	if !validHistoricalPage(result.Items, result.Total, limit, offset) {
		return groupopsport.HistoricalNodePage{}, ErrUnavailable
	}
	return result, nil
}

func historyReady(s *HistoryService) bool { return s != nil && s.uow != nil && s.reader != nil }

func historyInvalidOrUnavailable(s *HistoryService) error {
	if !historyReady(s) {
		return ErrUnavailable
	}
	return ErrInvalid
}

func classifyHistory(err error) error {
	if err == nil {
		return nil
	}
	if err == groupopsport.ErrHistoryInvalid {
		return ErrInvalid
	}
	return ErrUnavailable
}

func validHistoricalPage[T any](items []T, total int64, limit, offset int32) bool {
	if total < 0 || limit < 1 || offset < 0 {
		return false
	}
	available := total - int64(offset)
	if available < 0 {
		available = 0
	}
	expected := int64(limit)
	if available < expected {
		expected = available
	}
	return int64(len(items)) == expected
}
