package app

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

// saleCampaignAt uses the audited lifecycle at the payment instant. A current
// disabled/ended state cannot erase a sale made while the campaign was active.
func (s *Service) saleCampaignAt(ctx context.Context, productID int64, productType string, paidAt time.Time) (referraldomain.Campaign, bool, error) {
	if s == nil || s.timeline == nil {
		return referraldomain.Campaign{}, false, referralport.ErrUnavailable
	}
	candidates, err := s.store.ProductCampaignsAtWithin(ctx, productID, productType, paidAt)
	if err != nil {
		return referraldomain.Campaign{}, false, err
	}
	var selected referraldomain.Campaign
	for _, campaign := range candidates {
		state, known, stateErr := s.campaignStateAt(ctx, campaign.ID, paidAt)
		if stateErr != nil {
			return referraldomain.Campaign{}, false, stateErr
		}
		if !known {
			return referraldomain.Campaign{}, false, referralport.ErrUnavailable
		}
		campaign.State = state
		if !campaign.AcceptingAt(paidAt) {
			continue
		}
		if selected.ID != 0 {
			return referraldomain.Campaign{}, false, referralport.ErrConflict
		}
		selected = campaign
	}
	return selected, selected.ID != 0, nil
}

func (s *Service) campaignStateAt(ctx context.Context, campaignID int64, at time.Time) (referraldomain.CampaignState, bool, error) {
	events, err := s.timeline.ResourceTimelineWithin(ctx, "campaign", strconv.FormatInt(campaignID, 10), at)
	if err != nil {
		return "", false, err
	}
	var state referraldomain.CampaignState
	for _, event := range events {
		if event.Action != "referral.campaign.created" && event.Action != "referral.campaign.state_changed" {
			continue
		}
		var payload struct {
			State referraldomain.CampaignState `json:"state"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil || !payload.State.Valid() {
			return "", false, referralport.ErrUnavailable
		}
		state = payload.State
	}
	return state, state != "", nil
}
