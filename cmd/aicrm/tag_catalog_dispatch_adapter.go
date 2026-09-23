package main

import (
	"context"
	"errors"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

// Read the immutable owner snapshot in a short transaction. Outbound performs
// its provider call only after Within returns and releases this transaction.
type tagCatalogDispatchReader struct {
	uow interface {
		Within(context.Context, func(context.Context) error) error
	}
	reader tagport.CatalogMutationDispatchReader
}

func (r tagCatalogDispatchReader) ReadCatalogMutationDispatch(ctx context.Context, source string) (tagport.CatalogMutationDispatch, error) {
	if r.uow == nil || r.reader == nil {
		return tagport.CatalogMutationDispatch{}, errors.New("tag catalog dispatch reader unavailable")
	}
	var snapshot tagport.CatalogMutationDispatch
	err := r.uow.Within(ctx, func(tx context.Context) error {
		var err error
		snapshot, err = r.reader.ReadCatalogMutationDispatch(tx, source)
		return err
	})
	return snapshot, err
}

var _ tagport.CatalogMutationDispatchReader = tagCatalogDispatchReader{}
