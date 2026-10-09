package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

// SetCampaignPosterLayouts changes only metadata of frozen Referral artwork.
// It never needs the Media source to remain enabled or calls a Provider.
func (s *PosterService) SetCampaignPosterLayouts(ctx context.Context, c referralport.SetCampaignPosterLayoutsCommand) (referralport.CampaignPosterLayoutConfiguration, error) {
	var result referralport.CampaignPosterLayoutConfiguration
	if s == nil || c.CampaignID < 1 || c.ExpectedVersion < 1 || c.ActorAdminID < 1 || !validKey(c.IdempotencyKey) || len(c.Posters) < 1 || len(c.Posters) > 3 {
		return result, referralport.ErrInvalidRequest
	}
	seen := map[int]bool{}
	for _, update := range c.Posters {
		if update.Slot < 1 || update.Slot > 3 || seen[update.Slot] {
			return result, referralport.ErrInvalidRequest
		}
		seen[update.Slot] = true
	}
	encoded, err := json.Marshal(struct {
		ID, Version int64
		Posters     []referralport.PosterLayoutUpdate
	}{c.CampaignID, c.ExpectedVersion, c.Posters})
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(encoded)
	actor := adminScope(c.ActorAdminID)
	err = s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "poster_layouts", actor, c.IdempotencyKey); err != nil {
			return err
		}
		receipt, replay, err := s.store.ReadOperationReceiptWithin(tx, "poster_layouts", actor, c.IdempotencyKey)
		if err != nil {
			return err
		}
		if replay && (receipt.PayloadDigest != digest || receipt.ResultKind != "campaign" || receipt.ResultID != c.CampaignID) {
			return referralport.ErrIdempotencyConflict
		}
		current, err := s.store.ReadCampaignWithin(tx, c.CampaignID, true)
		if err != nil {
			return err
		}
		stored, err := s.store.ListCampaignPostersWithin(tx, c.CampaignID)
		if err != nil {
			return err
		}
		// Populate legacy dimensions from frozen headers inside this same UoW.
		if _, err = s.projectPostersWithin(tx, c.CampaignID, stored); err != nil {
			return err
		}
		if !replay {
			if current.Version != c.ExpectedVersion {
				return referralport.ErrConflict
			}
			if current.State == referraldomain.CampaignEnded || current.State == referraldomain.CampaignDisabled {
				return referralport.ErrCampaignConfigLocked
			}
			for _, update := range c.Posters {
				found := false
				for i := range stored {
					if stored[i].Slot != update.Slot {
						continue
					}
					if !update.PosterQRLayout.Fits(stored[i].Width, stored[i].Height) {
						return referralport.ErrInvalidRequest
					}
					layout := update.PosterQRLayout
					stored[i].QRLayout = &layout
					found = true
					break
				}
				if !found {
					return referralport.ErrInvalidRequest
				}
			}
			now := s.now().UTC()
			next := current
			next.Version++
			next.UpdatedAt = now
			if _, err = s.store.UpdateCampaignWithin(tx, next, current.Version); err != nil {
				return err
			}
			for _, poster := range stored {
				if !seen[poster.Slot] {
					continue
				}
				if err = s.store.UpdateCampaignPosterLayoutWithin(tx, c.CampaignID, poster); err != nil {
					return err
				}
			}
			if err = s.store.AppendOperationReceiptWithin(tx, "poster_layouts", actor, c.IdempotencyKey, digest, "campaign", c.CampaignID, now); err != nil {
				return err
			}
			if err = appendPlatformEvent(tx, s.audit, s.outbox, "referral.campaign.poster_layouts_updated", "campaign", c.CampaignID, "admin", c.ActorAdminID, c.IdempotencyKey, map[string]any{"campaign_id": c.CampaignID, "posters": c.Posters}, now); err != nil {
				return err
			}
			current = next
		}
		posters, err := s.projectPostersWithin(tx, c.CampaignID, stored)
		if err != nil {
			return err
		}
		result = referralport.CampaignPosterLayoutConfiguration{CampaignID: c.CampaignID, Version: current.Version, Posters: posters}
		return nil
	})
	return result, err
}
