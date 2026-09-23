package main

import (
	"context"
	"errors"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
)

// audienceChannelReferenceAdapter keeps channel lookups with the Channel
// owner. It accepts an immutable code first, then one exact current catalog
// name; ambiguous or truncated directory results deliberately do not match.
type audienceChannelReferenceAdapter struct {
	channels channelport.CatalogReader
}

func (a audienceChannelReferenceAdapter) ResolveAudienceChannel(ctx context.Context, value string) (string, bool, error) {
	if a.channels == nil {
		return "", false, errors.New("channel catalog reader is required")
	}
	page, err := a.channels.List(ctx, channelport.CatalogFilter{Keyword: value, IncludeArchived: true, Limit: 100})
	if err != nil {
		return "", false, err
	}
	if page.Total > int64(len(page.Items)) {
		return "", false, nil
	}
	for _, item := range page.Items {
		if item.Code == value {
			return item.Code, true, nil
		}
	}
	exactName := ""
	for _, item := range page.Items {
		if item.Config.Name != value {
			continue
		}
		if exactName != "" && exactName != item.Code {
			return "", false, nil
		}
		exactName = item.Code
	}
	return exactName, exactName != "", nil
}
