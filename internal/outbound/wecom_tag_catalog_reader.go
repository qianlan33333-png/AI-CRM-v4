package outbound

import (
	"context"
	"errors"

	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// weComCatalogReader is the only adapter from the WeCom connector's
// read-only catalog facts to outbound's immutable effect result. It contains
// no customer targeting and offers no mutation methods.
type weComCatalogReader struct{ client wecomport.TagCatalogReader }

func NewWeComTagCatalogReader(client wecomport.TagCatalogReader) (CatalogReader, error) {
	if client == nil {
		return nil, errors.New("wecom tag catalog client is required")
	}
	return &weComCatalogReader{client: client}, nil
}

func (r *weComCatalogReader) ListCatalog(ctx context.Context) (CatalogSnapshot, error) {
	groups, err := r.client.ListTagCatalog(ctx)
	if err != nil {
		var providerErr wecomport.ProviderCallFailure
		if errors.As(err, &providerErr) {
			return CatalogSnapshot{}, &ReadError{Err: err, CallAttempted: providerErr.ProviderCallAttempted()}
		}
		return CatalogSnapshot{}, &ReadError{Err: err, CallAttempted: true}
	}
	snapshot := CatalogSnapshot{Groups: make([]CatalogGroup, 0, len(groups))}
	for _, group := range groups {
		value := CatalogGroup{ID: group.ID, Name: group.Name, Order: group.Order, Tags: make([]CatalogTag, 0, len(group.Tags))}
		for _, tag := range group.Tags {
			value.Tags = append(value.Tags, CatalogTag{ID: tag.ID, Name: tag.Name, Order: tag.Order, Deleted: tag.Deleted})
		}
		snapshot.Groups = append(snapshot.Groups, value)
	}
	return snapshot, nil
}
