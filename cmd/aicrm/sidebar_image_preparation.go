package main

import (
	"context"
	"errors"
	"strconv"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

// sidebarImagePreparation freezes an enabled Media-owned variant before
// submitting the upload intent to Outbound. It never calls a Provider and
// never uses the local image ID as a WeCom media ID.
type sidebarImagePreparation struct {
	sources     outboundport.MaterialSourceReader
	preparer    outboundport.MaterialPreparer
	scopeDigest string
	enabled     bool
}

func (a sidebarImagePreparation) ReadSidebarImageForSend(ctx context.Context, id int64, through time.Time) (mediaport.SidebarImageSendMaterial, error) {
	if !a.enabled || a.sources == nil || a.preparer == nil {
		return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialNotReady
	}
	source, err := a.sources.GetSourceSnapshot(ctx, "image:"+strconv.FormatInt(id, 10))
	if err != nil {
		return mediaport.SidebarImageSendMaterial{}, err
	}
	if source.SourceType != "image" {
		return mediaport.SidebarImageSendMaterial{}, errors.New("image cannot be prepared for sidebar")
	}
	if margin := time.Now().UTC().Add(30 * time.Second); through.Before(margin) {
		through = margin
	}
	prepared, err := a.preparer.ReadyForSend(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: source, CorpScopeDigest: a.scopeDigest, ValidThrough: through})
	if err != nil {
		var terminal outboundport.MediaPreparationTerminalError
		if errors.As(err, &terminal) && terminal.State == "outcome_unknown" {
			return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialOutcomeUnknown
		}
		return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialPreparationFailed
	}
	switch prepared.State {
	case "ready":
		if prepared.MediaID == "" || !prepared.ExpiresAt.After(through) {
			return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialNotReady
		}
		return mediaport.SidebarImageSendMaterial{ImageID: id, MediaID: prepared.MediaID, ReadyUntil: prepared.ExpiresAt}, nil
	case "queued", "accepted", "attempted", "retryable_failed":
		return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialPreparing
	case "outcome_unknown":
		return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialOutcomeUnknown
	default:
		return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialPreparationFailed
	}
}
