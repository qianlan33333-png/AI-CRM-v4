package app

import (
	"context"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type overviewStore interface {
	ReadOverview(context.Context, distributionport.OverviewWindow) (distributionport.Overview, error)
}

type OverviewReader struct {
	uow   platformport.UnitOfWork
	store overviewStore
}

func NewOverviewReader(uow platformport.UnitOfWork, store overviewStore) (*OverviewReader, error) {
	if uow == nil || store == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &OverviewReader{uow: uow, store: store}, nil
}

func (reader *OverviewReader) ReadOverview(ctx context.Context, window distributionport.OverviewWindow) (distributionport.Overview, error) {
	if reader == nil || reader.uow == nil || reader.store == nil || !window.Valid() {
		return distributionport.Overview{}, distributionport.ErrUnavailable
	}
	var result distributionport.Overview
	err := reader.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = reader.store.ReadOverview(tx, window)
		return readErr
	})
	return result, err
}

var _ distributionport.OverviewReader = (*OverviewReader)(nil)
