package app

import (
	"context"
	"errors"
	"testing"
	"time"

	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

func TestExpiredActiveCampaignCannotBeDisabledBeforeCloseSnapshot(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()

	preDeadline, err := h.admin.CreateCampaign(context.Background(), referralport.CreateCampaignCommand{
		ActorAdminID: 9001, Name: "before deadline disable", StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(time.Hour), IdempotencyKey: "create-before-deadline-disable",
	})
	if err != nil {
		t.Fatal(err)
	}
	preDeadline, err = h.admin.SetCampaignState(context.Background(), referralport.SetCampaignStateCommand{
		ActorAdminID: 9001, CampaignID: preDeadline.ID, ExpectedVersion: preDeadline.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "activate-before-deadline-disable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.admin.SetCampaignState(context.Background(), referralport.SetCampaignStateCommand{
		ActorAdminID: 9001, CampaignID: preDeadline.ID, ExpectedVersion: preDeadline.Version, Target: referraldomain.CampaignDisabled, IdempotencyKey: "disable-before-deadline",
	}); err != nil {
		t.Fatalf("disabling before EndsAt must remain available: %v", err)
	}

	campaign, err := h.admin.CreateCampaign(context.Background(), referralport.CreateCampaignCommand{
		ActorAdminID: 9001, Name: "expired disable snapshot", StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(time.Hour), IdempotencyKey: "create-expired-disable-snapshot",
	})
	if err != nil {
		t.Fatal(err)
	}
	campaign, err = h.admin.SetCampaignState(context.Background(), referralport.SetCampaignStateCommand{
		ActorAdminID: 9001, CampaignID: campaign.ID, ExpectedVersion: campaign.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "activate-expired-disable-snapshot",
	})
	if err != nil {
		t.Fatal(err)
	}
	h.clock = campaign.EndsAt.Add(time.Second)
	if _, err = h.admin.SetCampaignState(context.Background(), referralport.SetCampaignStateCommand{
		ActorAdminID: 9001, CampaignID: campaign.ID, ExpectedVersion: campaign.Version, Target: referraldomain.CampaignDisabled, IdempotencyKey: "disable-after-deadline-before-close-job",
	}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("expired campaign state change error=%v, want conflict", err)
	}
	if err = h.admin.RunCampaignClose(context.Background(), campaign.ID); err != nil {
		t.Fatal(err)
	}
	var state string
	var snapshots int64
	if err = h.pool.QueryRow(context.Background(), `SELECT c.state,(SELECT count(*) FROM referral_campaign_snapshots s WHERE s.campaign_id=c.id) FROM referral_campaigns c WHERE c.id=$1`, campaign.ID).Scan(&state, &snapshots); err != nil {
		t.Fatal(err)
	}
	if state != string(referraldomain.CampaignEnded) || snapshots != 1 {
		t.Fatalf("expired active campaign close state=%q snapshots=%d, want ended/1", state, snapshots)
	}
}
