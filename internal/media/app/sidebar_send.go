package app

import (
	"context"
	"errors"
	"strings"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

type sidebarImageSendStore interface {
	mediaport.GroupOpsMaterialSourceCapturer
	mediaport.GroupOpsMaterialPreparationReader
}

func (service *Service) ReadSidebarImageForSend(ctx context.Context, imageID int64, requiredThrough time.Time) (out mediaport.SidebarImageSendMaterial, err error) {
	if service == nil || ctx == nil || service.uow == nil || imageID < 1 || requiredThrough.IsZero() {
		return out, mediaport.ErrSidebarMaterialNotReady
	}
	store, ok := service.store.(sidebarImageSendStore)
	if !ok {
		return out, mediaport.ErrSidebarMaterialNotReady
	}
	err = service.uow.Within(ctx, func(txctx context.Context) error {
		sources, readErr := store.CaptureGroupOpsMaterialSources(txctx, mediaport.GroupOpsMaterialPlan{References: []mediaport.GroupOpsMaterialReference{{Kind: "image", ID: imageID}}})
		if readErr != nil {
			return readErr
		}
		items, readErr := store.ReadPreparedGroupOpsMaterials(txctx, sources, requiredThrough.UTC())
		if readErr != nil || len(items) != 1 || items[0].Reference.Kind != "image" || items[0].Reference.ID != imageID || items[0].Attachment.MsgType != "image" || strings.TrimSpace(items[0].Attachment.MediaID) == "" || !items[0].ReadyUntil.After(requiredThrough) {
			return mediaport.ErrSidebarMaterialNotReady
		}
		out = mediaport.SidebarImageSendMaterial{ImageID: imageID, MediaID: items[0].Attachment.MediaID, ReadyUntil: items[0].ReadyUntil.UTC()}
		return nil
	})
	if err != nil {
		if errors.Is(err, mediaport.ErrSidebarMaterialNotReady) {
			return mediaport.SidebarImageSendMaterial{}, err
		}
		return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialNotReady
	}
	return out, nil
}

var _ mediaport.SidebarImageSendReader = (*Service)(nil)
