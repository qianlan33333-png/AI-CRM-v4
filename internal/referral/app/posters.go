package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strconv"
	"strings"
	"time"

	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
)

type posterStore interface {
	ReadCampaignWithin(context.Context, int64, bool) (referraldomain.Campaign, error)
	UpdateCampaignWithin(context.Context, referraldomain.Campaign, int64) (referraldomain.Campaign, error)
	ListCampaignPostersWithin(context.Context, int64) ([]referralstore.PublishedPoster, error)
	ReadCampaignPosterWithin(context.Context, int64, int) (referralport.CampaignPosterImage, error)
	ReplaceCampaignPostersWithin(context.Context, int64, []referralstore.PublishedPoster, time.Time) error
	LockOperationReceiptWithin(context.Context, string, string, string) error
	ReadOperationReceiptWithin(context.Context, string, string, string) (referralstore.OperationReceipt, bool, error)
	AppendOperationReceiptWithin(context.Context, string, string, string, [sha256.Size]byte, string, int64, time.Time) error
}

type PosterService struct {
	uow    platformport.UnitOfWork
	store  posterStore
	images referralport.PosterImageReader
	audit  *platformaudit.Service
	outbox platformoutbox.Appender
	now    func() time.Time
}

func NewPosterService(uow platformport.UnitOfWork, store posterStore, images referralport.PosterImageReader, audit *platformaudit.Service, outbox platformoutbox.Appender) (*PosterService, error) {
	if uow == nil || store == nil || images == nil || audit == nil || outbox == nil {
		return nil, referralport.ErrUnavailable
	}
	return &PosterService{uow: uow, store: store, images: images, audit: audit, outbox: outbox, now: time.Now}, nil
}

func (s *PosterService) ListCampaignPosters(ctx context.Context, campaignID int64, admin bool) ([]referralport.CampaignPoster, error) {
	if s == nil || campaignID < 1 {
		return nil, referralport.ErrInvalidRequest
	}
	var stored []referralstore.PublishedPoster
	err := s.uow.Within(ctx, func(tx context.Context) error {
		campaign, err := s.store.ReadCampaignWithin(tx, campaignID, false)
		if err != nil {
			return err
		}
		if !admin && campaign.State == referraldomain.CampaignDraft {
			return referralport.ErrNotFound
		}
		stored, err = s.store.ListCampaignPostersWithin(tx, campaignID)
		return err
	})
	if err != nil {
		return nil, err
	}
	result := make([]referralport.CampaignPoster, 0, len(stored))
	for _, p := range stored {
		result = append(result, referralport.CampaignPoster{Slot: p.Slot, Description: p.Description, SourceImageID: p.SourceImageID, ImageURL: "/api/v1/referral/campaigns/" + strconv.FormatInt(campaignID, 10) + "/posters/" + strconv.Itoa(p.Slot)})
	}
	return result, nil
}

func (s *PosterService) ReadCampaignPoster(ctx context.Context, campaignID int64, slot int) (referralport.CampaignPosterImage, error) {
	if s == nil || campaignID < 1 || slot < 1 || slot > 3 {
		return referralport.CampaignPosterImage{}, referralport.ErrNotFound
	}
	var result referralport.CampaignPosterImage
	err := s.uow.Within(ctx, func(tx context.Context) error {
		campaign, err := s.store.ReadCampaignWithin(tx, campaignID, false)
		if err != nil {
			return err
		}
		if campaign.State == referraldomain.CampaignDraft {
			return referralport.ErrNotFound
		}
		result, err = s.store.ReadCampaignPosterWithin(tx, campaignID, slot)
		return err
	})
	return result, err
}

func (s *PosterService) SetCampaignPosters(ctx context.Context, c referralport.SetCampaignPostersCommand) ([]referralport.CampaignPoster, error) {
	if s == nil || c.CampaignID < 1 || c.ExpectedVersion < 1 || c.ActorAdminID < 1 || !validKey(c.IdempotencyKey) || len(c.Posters) > 3 {
		return nil, referralport.ErrInvalidRequest
	}
	encoded, _ := json.Marshal(struct {
		ID, Version int64
		Posters     []referralport.CampaignPosterSource
	}{c.CampaignID, c.ExpectedVersion, c.Posters})
	digest := sha256.Sum256(encoded)
	// A retry of a completed publication must read the frozen receipt before
	// re-reading Media, because the source image can later be disabled.
	actor := adminScope(c.ActorAdminID)
	alreadyPublished := false
	err := s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "campaign_posters", actor, c.IdempotencyKey); err != nil {
			return err
		}
		receipt, found, err := s.store.ReadOperationReceiptWithin(tx, "campaign_posters", actor, c.IdempotencyKey)
		if err != nil {
			return err
		}
		if found {
			if receipt.PayloadDigest != digest || receipt.ResultKind != "campaign" || receipt.ResultID != c.CampaignID {
				return referralport.ErrIdempotencyConflict
			}
			alreadyPublished = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if alreadyPublished {
		return s.ListCampaignPosters(ctx, c.CampaignID, true)
	}
	prepared := make([]referralstore.PublishedPoster, 0, len(c.Posters))
	for i, p := range c.Posters {
		if p.ImageID < 1 || len([]rune(p.Description)) > 80 || strings.TrimSpace(p.Description) != p.Description {
			return nil, referralport.ErrInvalidRequest
		}
		imageValue, err := s.images.ReadEnabledPosterImage(ctx, p.ImageID)
		if err != nil {
			return nil, referralport.ErrInvalidRequest
		}
		if len(imageValue.Content) == 0 || len(imageValue.Content) > 5<<20 {
			return nil, referralport.ErrInvalidRequest
		}
		config, format, err := image.DecodeConfig(bytes.NewReader(imageValue.Content))
		if err != nil || config.Width < 600 || config.Height < 800 || config.Width > 5000 || config.Height > 10000 {
			return nil, referralport.ErrInvalidRequest
		}
		mediaType := "image/" + format
		if format == "jpeg" {
			mediaType = "image/jpeg"
		}
		if mediaType != imageValue.MediaType || (mediaType != "image/png" && mediaType != "image/jpeg") {
			return nil, referralport.ErrInvalidRequest
		}
		prepared = append(prepared, referralstore.PublishedPoster{Slot: i + 1, SourceImageID: p.ImageID, Description: p.Description, MediaType: mediaType, Content: imageValue.Content})
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "campaign_posters", actor, c.IdempotencyKey); err != nil {
			return err
		}
		if receipt, found, err := s.store.ReadOperationReceiptWithin(tx, "campaign_posters", actor, c.IdempotencyKey); err != nil || found {
			if err != nil {
				return err
			}
			if receipt.PayloadDigest != digest || receipt.ResultKind != "campaign" || receipt.ResultID != c.CampaignID {
				return referralport.ErrIdempotencyConflict
			}
			return nil
		}
		current, err := s.store.ReadCampaignWithin(tx, c.CampaignID, true)
		if err != nil {
			return err
		}
		if current.Version != c.ExpectedVersion {
			return referralport.ErrConflict
		}
		if current.State == referraldomain.CampaignEnded || current.State == referraldomain.CampaignDisabled {
			return referralport.ErrCampaignConfigLocked
		}
		now := s.now().UTC()
		next := current
		next.Version++
		next.UpdatedAt = now
		if _, err = s.store.UpdateCampaignWithin(tx, next, current.Version); err != nil {
			return err
		}
		if err = s.store.ReplaceCampaignPostersWithin(tx, c.CampaignID, prepared, now); err != nil {
			return err
		}
		if err = s.store.AppendOperationReceiptWithin(tx, "campaign_posters", actor, c.IdempotencyKey, digest, "campaign", c.CampaignID, now); err != nil {
			return err
		}
		return appendPlatformEvent(tx, s.audit, s.outbox, "referral.campaign.posters_updated", "campaign", c.CampaignID, "admin", c.ActorAdminID, c.IdempotencyKey, map[string]any{"campaign_id": c.CampaignID, "poster_count": len(prepared)}, now)
	})
	if err != nil {
		return nil, err
	}
	return s.ListCampaignPosters(ctx, c.CampaignID, true)
}
