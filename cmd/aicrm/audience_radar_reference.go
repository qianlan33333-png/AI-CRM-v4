package main

import (
	"context"
	"errors"
	"strconv"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

// audienceRadarReferenceAdapter leaves Radar identity and event semantics in
// the Radar owner. This adapter only resolves a stable numeric Radar ID or a
// single exact Radar title to the persisted numeric ID.
type audienceRadarReferenceAdapter struct {
	radars radarport.LinkReader
}

func (a audienceRadarReferenceAdapter) ResolveAudienceRadar(ctx context.Context, value string) (string, bool, error) {
	if a.radars == nil {
		return "", false, errors.New("radar link reader is required")
	}
	if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 && strconv.FormatInt(id, 10) == value {
		detail, getErr := a.radars.Get(ctx, radar.RadarID(id))
		if errors.Is(getErr, radarport.ErrNotFound) {
			return "", false, nil
		}
		if getErr != nil {
			return "", false, getErr
		}
		return strconv.FormatInt(int64(detail.Link.ID), 10), true, nil
	}
	page, err := a.radars.List(ctx, radarport.ListQuery{Search: value, Limit: radarport.MaximumLimit})
	if err != nil {
		return "", false, err
	}
	if page.Total > int64(len(page.Items)) {
		return "", false, nil
	}
	resolved := ""
	for _, item := range page.Items {
		if item.Link.Title != value {
			continue
		}
		id := strconv.FormatInt(int64(item.Link.ID), 10)
		if resolved != "" && resolved != id {
			return "", false, nil
		}
		resolved = id
	}
	return resolved, resolved != "", nil
}
