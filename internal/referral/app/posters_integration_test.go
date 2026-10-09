package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
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

func TestPostgreSQLCampaignPosterShareTextBindingAndAtomicPublication(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 600, 800))); err != nil {
		t.Fatal(err)
	}
	media := &posterImageStub{content: buffer.Bytes(), enabled: true}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewPosterService(h.uow, h.repository, media, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return h.clock }
	campaign, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "话术绑定", StartsAt: h.clock.Add(time.Hour), EndsAt: h.clock.Add(24 * time.Hour), IdempotencyKey: "poster-copy-create"})
	if err != nil {
		t.Fatal(err)
	}
	first, third := "  一起参加 🎉\n第二行  ", "<b>第三张原文</b>\n🙂"
	c := referralport.SetCampaignPostersCommand{CampaignID: campaign.ID, ExpectedVersion: campaign.Version, ActorAdminID: 9001, IdempotencyKey: "poster-copy-publish", Posters: []referralport.CampaignPosterSource{
		{ImageID: 1, Description: "一", ShareText: first},
		{ImageID: 2, Description: "二", ShareText: " \n\t"},
		{ImageID: 3, Description: "三", ShareText: third},
	}}
	got, err := s.SetCampaignPosters(ctx, c)
	if err != nil || len(got) != 3 || got[0].ShareText != first || got[1].ShareText != "" || got[2].ShareText != third {
		t.Fatalf("paired publication=%+v err=%v", got, err)
	}
	media.enabled = false
	if _, err = s.SetCampaignPosters(ctx, c); err != nil {
		t.Fatalf("frozen retry: %v", err)
	}
	changed := c
	changed.Posters = append([]referralport.CampaignPosterSource(nil), c.Posters...)
	changed.Posters[0].ShareText = "changed"
	if _, err = s.SetCampaignPosters(ctx, changed); err != referralport.ErrIdempotencyConflict {
		t.Fatalf("copy digest conflict: %v", err)
	}
	media.enabled = true
	c.IdempotencyKey = "poster-copy-stale"
	if _, err = s.SetCampaignPosters(ctx, c); err != referralport.ErrConflict {
		t.Fatalf("stale version: %v", err)
	}
	c.ExpectedVersion++
	c.IdempotencyKey = "poster-copy-remove-middle"
	c.Posters = []referralport.CampaignPosterSource{c.Posters[0], c.Posters[2]}
	got, err = s.SetCampaignPosters(ctx, c)
	if err != nil || len(got) != 2 || got[1].Slot != 2 || got[1].SourceImageID != 3 || got[1].ShareText != third {
		t.Fatalf("remove binding=%+v err=%v", got, err)
	}
	c.ExpectedVersion++
	c.IdempotencyKey = "poster-copy-rollback"
	c.Posters = []referralport.CampaignPosterSource{{ImageID: 2, Description: "新", ShareText: "must roll back"}}
	s.outbox = failingReferralOutbox{}
	if _, err = s.SetCampaignPosters(ctx, c); err == nil {
		t.Fatal("outbox failure accepted")
	}
	got, err = s.ListCampaignPosters(ctx, campaign.ID, true)
	if err != nil || len(got) != 2 || got[0].ShareText != first || got[1].ShareText != third {
		t.Fatalf("rollback lost copy: %+v %v", got, err)
	}
	keyDigest := sha256.Sum256([]byte(c.IdempotencyKey))
	assertCount(t, h.pool, "SELECT count(*) FROM referral_operation_receipts WHERE key_digest=$1", 0, keyDigest[:])
	var version int64
	if err = h.pool.QueryRow(ctx, "SELECT version FROM referral_campaigns WHERE id=$1", campaign.ID).Scan(&version); err != nil || version != c.ExpectedVersion {
		t.Fatalf("rollback version=%d err=%v", version, err)
	}
}

func TestPostgreSQLCampaignPosterShareTextMigrationPreservesLegacySnapshot(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 600, 800))); err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewPosterService(h.uow, h.repository, &posterImageStub{content: buffer.Bytes(), enabled: true}, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	campaign, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "旧海报迁移", StartsAt: h.clock.Add(time.Hour), EndsAt: h.clock.Add(24 * time.Hour), IdempotencyKey: "poster-legacy-create"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetCampaignPosters(ctx, referralport.SetCampaignPostersCommand{CampaignID: campaign.ID, ExpectedVersion: campaign.Version, ActorAdminID: 9001, IdempotencyKey: "poster-legacy-publish", Posters: []referralport.CampaignPosterSource{{ImageID: 1, Description: "旧说明"}}}); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the pre-migration request shape independently of the new
	// source type. Empty copy must leave existing receipt digests compatible.
	legacyBody, err := json.Marshal(struct {
		ID, Version int64
		Posters     []struct {
			ImageID     int64  `json:"image_id"`
			Description string `json:"description"`
		}
	}{campaign.ID, campaign.Version, []struct {
		ImageID     int64  `json:"image_id"`
		Description string `json:"description"`
	}{{1, "旧说明"}}})
	if err != nil {
		t.Fatal(err)
	}
	legacyDigest := sha256.Sum256(legacyBody)
	var receiptDigest []byte
	keyDigest := sha256.Sum256([]byte("poster-legacy-publish"))
	if err = h.pool.QueryRow(ctx, "SELECT payload_digest FROM referral_operation_receipts WHERE key_digest=$1", keyDigest[:]).Scan(&receiptDigest); err != nil || !bytes.Equal(receiptDigest, legacyDigest[:]) {
		t.Fatalf("legacy receipt digest changed: %v", err)
	}
	if _, err = h.pool.Exec(ctx, "ALTER TABLE referral_campaign_posters DROP COLUMN share_text"); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../migrations/0229_referral_poster_share_text.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.pool.Exec(ctx, string(body)); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListCampaignPosters(ctx, campaign.ID, true)
	if err != nil || len(got) != 1 || got[0].ShareText != "" || got[0].Description != "旧说明" {
		t.Fatalf("legacy metadata=%+v err=%v", got, err)
	}
	var actual []byte
	if err = h.pool.QueryRow(ctx, "SELECT image_bytes FROM referral_campaign_posters WHERE campaign_id=$1", campaign.ID).Scan(&actual); err != nil || !bytes.Equal(actual, buffer.Bytes()) {
		t.Fatalf("legacy image changed: %v", err)
	}
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
