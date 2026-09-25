package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type posterImageStub struct {
	content []byte
	enabled bool
}

func (s *posterImageStub) ReadEnabledPosterImage(_ context.Context, id int64) (referralport.CampaignPosterImage, error) {
	if !s.enabled || id < 1 || id > 3 {
		return referralport.CampaignPosterImage{}, referralport.ErrNotFound
	}
	return referralport.CampaignPosterImage{Content: s.content, MediaType: "image/png"}, nil
}

func TestPostgreSQLCampaignPostersPublishSnapshotAndIdempotentRetry(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	canvas := image.NewRGBA(image.Rect(0, 0, 600, 800))
	canvas.Set(5, 5, color.RGBA{R: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatal(err)
	}
	media := &posterImageStub{content: buffer.Bytes(), enabled: true}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	posters, err := NewPosterService(h.uow, h.repository, media, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	posters.now = func() time.Time { return h.clock }
	campaign, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "海报测试", StartsAt: h.clock.Add(time.Hour), EndsAt: h.clock.Add(24 * time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationFreeSignup, IdempotencyKey: "poster-campaign-create"})
	if err != nil {
		t.Fatal(err)
	}
	command := referralport.SetCampaignPostersCommand{CampaignID: campaign.ID, ExpectedVersion: campaign.Version, ActorAdminID: 9001, IdempotencyKey: "poster-publish-once", Posters: []referralport.CampaignPosterSource{{ImageID: 1, Description: "邀请海报"}}}
	published, err := posters.SetCampaignPosters(ctx, command)
	if err != nil || len(published) != 1 || published[0].Slot != 1 {
		t.Fatalf("publish=%+v err=%v", published, err)
	}
	media.enabled = false
	if _, err = posters.SetCampaignPosters(ctx, command); err != nil {
		t.Fatalf("idempotent retry after source disabled: %v", err)
	}
	if _, err = posters.ListCampaignPosters(ctx, campaign.ID, false); err != referralport.ErrNotFound {
		t.Fatalf("draft poster visible publicly: %v", err)
	}
	campaign, err = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{CampaignID: campaign.ID, ExpectedVersion: campaign.Version + 1, ActorAdminID: 9001, Target: referraldomain.CampaignScheduled, IdempotencyKey: "poster-schedule"})
	if err != nil {
		t.Fatal(err)
	}
	public, err := posters.ListCampaignPosters(ctx, campaign.ID, false)
	if err != nil || len(public) != 1 || public[0].ImageURL == "" {
		t.Fatalf("public poster=%+v err=%v", public, err)
	}
	imageValue, err := posters.ReadCampaignPoster(ctx, campaign.ID, 1)
	if err != nil || !bytes.Equal(imageValue.Content, buffer.Bytes()) {
		t.Fatalf("snapshot changed after Media was disabled: err=%v", err)
	}
	if _, err = posters.SetCampaignPosters(ctx, referralport.SetCampaignPostersCommand{CampaignID: campaign.ID, ExpectedVersion: campaign.Version, ActorAdminID: 9001, IdempotencyKey: "poster-invalid-count", Posters: []referralport.CampaignPosterSource{{ImageID: 1}, {ImageID: 2}, {ImageID: 3}, {ImageID: 4}}}); err != referralport.ErrInvalidRequest {
		t.Fatalf("poster cap err=%v", err)
	}
}
