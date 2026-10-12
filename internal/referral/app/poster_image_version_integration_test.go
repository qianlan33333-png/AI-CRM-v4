package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

func TestPostgreSQLPosterImageVersionAndFrozenSnapshot(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	var original bytes.Buffer
	pic := image.NewRGBA(image.Rect(0, 0, 1250, 2426))
	png.Encode(&original, pic)
	media := &posterImageStub{content: original.Bytes(), enabled: true}
	audit, _ := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	s, e := NewPosterService(h.uow, h.repository, media, audit, platformoutbox.NewPostgreSQL())
	if e != nil {
		t.Fatal(e)
	}
	s.now = func() time.Time { return h.clock }
	campaign, e := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "version", StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationFreeSignup, IdempotencyKey: "version-create"})
	if e != nil {
		t.Fatal(e)
	}
	publish := func(version int64, key string) []referralport.CampaignPoster {
		t.Helper()
		p, e := s.SetCampaignPosters(ctx, referralport.SetCampaignPostersCommand{CampaignID: campaign.ID, ActorAdminID: 9001, ExpectedVersion: version, IdempotencyKey: key, Posters: []referralport.CampaignPosterSource{{ImageID: 1, ShareText: "exact copy"}}})
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	first := publish(campaign.Version, "version-publish")
	if _, e := s.ReadCampaignPosterMetadata(ctx, campaign.ID, 1); e != referralport.ErrNotFound {
		t.Fatal("draft metadata exposed")
	}
	campaign, e = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{CampaignID: campaign.ID, ActorAdminID: 9001, ExpectedVersion: campaign.Version + 1, Target: referraldomain.CampaignActive, IdempotencyKey: "version-active"})
	if e != nil {
		t.Fatal(e)
	}
	old, e := s.ReadCampaignPosterMetadata(ctx, campaign.ID, 1)
	if e != nil || old.Digest != sha256.Sum256(original.Bytes()) {
		t.Fatalf("meta=%+v %v", old, e)
	}
	if !strings.HasSuffix(first[0].ImageURL, "?v="+referralport.PosterImageRevision(old.Digest)) {
		t.Fatal("URL version differs")
	}
	frozen, e := s.ReadCampaignPosterSnapshot(ctx, campaign.ID, 1, old.Digest)
	if e != nil || !bytes.Equal(frozen.Content, original.Bytes()) {
		t.Fatal("original snapshot changed")
	}
	layout, e := s.SetCampaignPosterLayouts(ctx, referralport.SetCampaignPosterLayoutsCommand{CampaignID: campaign.ID, ActorAdminID: 9001, ExpectedVersion: campaign.Version, IdempotencyKey: "version-layout", Posters: []referralport.PosterLayoutUpdate{{Slot: 1, PosterQRLayout: referralport.PosterQRLayout{X: 800, Y: 1900, Size: 350}}}})
	if e != nil || layout.Posters[0].ImageURL != first[0].ImageURL {
		t.Fatalf("layout incorrectly changes source: %v", e)
	}
	pic.SetRGBA(2, 2, color.RGBA{255, 255, 255, 255})
	var replacement bytes.Buffer
	png.Encode(&replacement, pic)
	media.content = replacement.Bytes()
	second := publish(layout.Version, "version-replace")
	if second[0].ImageURL == first[0].ImageURL || second[0].Width != first[0].Width || second[0].Height != first[0].Height {
		t.Fatal("same size replacement did not invalidate source")
	}
	if _, e := s.ReadCampaignPosterSnapshot(ctx, campaign.ID, 1, old.Digest); e != referralport.ErrConflict {
		t.Fatalf("stale snapshot accepted: %v", e)
	}
	now, e := s.ReadCampaignPosterMetadata(ctx, campaign.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReadCampaignPosterSnapshot(ctx, campaign.ID, 1, now.Digest); e != nil {
		t.Fatal(e)
	}
	if _, e := h.pool.Exec(ctx, "UPDATE referral_campaign_posters SET image_bytes=$1 WHERE campaign_id=$2", original.Bytes(), campaign.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReadCampaignPosterSnapshot(ctx, campaign.ID, 1, now.Digest); e != referralport.ErrUnavailable {
		t.Fatalf("corrupt snapshot served: %v", e)
	}
}
