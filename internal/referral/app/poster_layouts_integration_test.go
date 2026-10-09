package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

func TestPostgreSQLPosterLayoutsAtomicRuntimeUpdate(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 1250, 2426))); err != nil {
		t.Fatal(err)
	}
	media := &posterImageStub{content: pngData.Bytes(), enabled: true}
	audit, _ := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	s, err := NewPosterService(h.uow, h.repository, media, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	campaign, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "运行时坐标", StartsAt: h.clock.Add(time.Hour), EndsAt: h.clock.Add(24 * time.Hour), IdempotencyKey: "layout-create"})
	if err != nil {
		t.Fatal(err)
	}
	sources := []referralport.CampaignPosterSource{{ImageID: 1, Description: "第一张", ShareText: " 原文 🎉\n  "}, {ImageID: 2, Description: "第二张", ShareText: "第二张话术"}, {ImageID: 3, Description: "第三张", ShareText: "第三张话术"}}
	if _, err = s.SetCampaignPosters(ctx, referralport.SetCampaignPostersCommand{CampaignID: campaign.ID, ExpectedVersion: campaign.Version, ActorAdminID: 9001, IdempotencyKey: "layout-publish", Posters: sources}); err != nil {
		t.Fatal(err)
	}
	media.enabled = false // Editing frozen layout must not depend on the library.
	c := referralport.SetCampaignPosterLayoutsCommand{CampaignID: campaign.ID, ExpectedVersion: campaign.Version + 1, ActorAdminID: 9001, IdempotencyKey: "layout-update", Posters: []referralport.PosterLayoutUpdate{
		{Slot: 1, PosterQRLayout: referralport.PosterQRLayout{X: 810, Y: 1932, Size: 370}},
		{Slot: 2, PosterQRLayout: referralport.PosterQRLayout{X: 0, Y: 0, Size: 300}},
		{Slot: 3, PosterQRLayout: referralport.PosterQRLayout{X: 900, Y: 2000, Size: 300}},
	}}
	got, err := s.SetCampaignPosterLayouts(ctx, c)
	if err != nil || got.Version != c.ExpectedVersion+1 || len(got.Posters) != 3 {
		t.Fatalf("save=%+v err=%v", got, err)
	}
	for i, p := range got.Posters {
		if p.QRLayout != c.Posters[i].PosterQRLayout || p.SourceImageID != sources[i].ImageID || p.Description != sources[i].Description || p.ShareText != sources[i].ShareText || p.Width != 1250 || p.Height != 2426 {
			t.Fatalf("binding changed: %+v", p)
		}
		var frozen []byte
		if err = h.pool.QueryRow(ctx, "SELECT image_bytes FROM referral_campaign_posters WHERE campaign_id=$1 AND slot=$2", campaign.ID, p.Slot).Scan(&frozen); err != nil || !bytes.Equal(frozen, pngData.Bytes()) {
			t.Fatalf("image changed: %v", err)
		}
	}
	readback, err := s.ListCampaignPosters(ctx, campaign.ID, true)
	if err != nil || readback[1].QRLayout != c.Posters[1].PosterQRLayout {
		t.Fatalf("readback=%+v err=%v", readback, err)
	}
	replayed, err := s.SetCampaignPosterLayouts(ctx, c)
	if err != nil || replayed.Version != got.Version {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	changed := c
	changed.Posters = append([]referralport.PosterLayoutUpdate(nil), c.Posters...)
	changed.Posters[0].X++
	if _, err = s.SetCampaignPosterLayouts(ctx, changed); err != referralport.ErrIdempotencyConflict {
		t.Fatalf("digest conflict: %v", err)
	}
	changed.IdempotencyKey = "layout-stale"
	if _, err = s.SetCampaignPosterLayouts(ctx, changed); err != referralport.ErrConflict {
		t.Fatalf("version conflict: %v", err)
	}
	for name, updates := range map[string][]referralport.PosterLayoutUpdate{
		"negative":  {{Slot: 1, PosterQRLayout: referralport.PosterQRLayout{X: -1, Y: 10, Size: 300}}},
		"zero":      {{Slot: 1, PosterQRLayout: referralport.PosterQRLayout{X: 0, Y: 0, Size: 0}}},
		"outside":   {{Slot: 1, PosterQRLayout: referralport.PosterQRLayout{X: 1000, Y: 2000, Size: 300}}},
		"overflow":  {{Slot: 1, PosterQRLayout: referralport.PosterQRLayout{X: int(^uint(0) >> 1), Y: 0, Size: 300}}},
		"duplicate": {c.Posters[0], c.Posters[0]},
		"atomic":    {c.Posters[0], {Slot: 2, PosterQRLayout: referralport.PosterQRLayout{X: 0, Y: 2400, Size: 300}}},
	} {
		bad := c
		bad.ExpectedVersion = got.Version
		bad.IdempotencyKey = "layout-invalid-" + name
		bad.Posters = updates
		if _, err = s.SetCampaignPosterLayouts(ctx, bad); err != referralport.ErrInvalidRequest {
			t.Fatalf("%s: %v", name, err)
		}
	}
	changed.ExpectedVersion = got.Version
	changed.IdempotencyKey = "layout-rollback"
	changed.Posters = changed.Posters[:1]
	s.outbox = failingReferralOutbox{}
	if _, err = s.SetCampaignPosterLayouts(ctx, changed); err == nil {
		t.Fatal("outbox failure accepted")
	}
	keyDigest := sha256.Sum256([]byte(changed.IdempotencyKey))
	assertCount(t, h.pool, "SELECT count(*) FROM referral_operation_receipts WHERE key_digest=$1", 0, keyDigest[:])
	assertCount(t, h.pool, "SELECT count(*) FROM audit_events WHERE action='referral.campaign.poster_layouts_updated'", 1)
	assertCount(t, h.pool, "SELECT count(*) FROM outbox_events WHERE event_type='referral.campaign.poster_layouts_updated'", 1)
	var version int64
	if err = h.pool.QueryRow(ctx, "SELECT version FROM referral_campaigns WHERE id=$1", campaign.ID).Scan(&version); err != nil || version != got.Version {
		t.Fatalf("rollback version=%d err=%v", version, err)
	}
	readback, err = s.ListCampaignPosters(ctx, campaign.ID, true)
	if err != nil || readback[0].QRLayout != got.Posters[0].QRLayout {
		t.Fatalf("rollback layout=%+v err=%v", readback, err)
	}
	s.outbox = platformoutbox.NewPostgreSQL()
	// Two writers on the same version: exactly one new commit.
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			write := changed
			write.IdempotencyKey = []string{"layout-concurrent-a", "layout-concurrent-b"}[i]
			write.Posters = []referralport.PosterLayoutUpdate{{Slot: 2, PosterQRLayout: referralport.PosterQRLayout{X: i, Y: 0, Size: 300}}}
			_, e := s.SetCampaignPosterLayouts(ctx, write)
			results <- e
		}(i)
	}
	one, two := <-results, <-results
	if !((one == nil && two == referralport.ErrConflict) || (two == nil && one == referralport.ErrConflict)) {
		t.Fatalf("concurrent: %v %v", one, two)
	}
	// Deleting the middle poster moves image, copy and coordinates together.
	media.enabled = true
	removed := []referralport.CampaignPosterSource{sources[0], sources[2]}
	removed[0].QRLayout = &c.Posters[0].PosterQRLayout
	removed[1].QRLayout = &c.Posters[2].PosterQRLayout
	final, err := s.SetCampaignPosters(ctx, referralport.SetCampaignPostersCommand{CampaignID: campaign.ID, ExpectedVersion: got.Version + 1, ActorAdminID: 9001, IdempotencyKey: "layout-remove-middle", Posters: removed})
	if err != nil || final[1].Slot != 2 || final[1].SourceImageID != 3 || final[1].ShareText != sources[2].ShareText || final[1].QRLayout != c.Posters[2].PosterQRLayout {
		t.Fatalf("remove binding=%+v err=%v", final, err)
	}
	missing := changed
	missing.ExpectedVersion = got.Version + 2
	missing.IdempotencyKey = "layout-missing-slot"
	missing.Posters = []referralport.PosterLayoutUpdate{c.Posters[2]}
	if _, err = s.SetCampaignPosterLayouts(ctx, missing); err != referralport.ErrInvalidRequest {
		t.Fatalf("missing slot: %v", err)
	}
}

func TestPostgreSQLPosterLayoutMigrationAndCustomDimensions(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 600, 800))); err != nil {
		t.Fatal(err)
	}
	media := &posterImageStub{content: pngData.Bytes(), enabled: true}
	audit, _ := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	s, err := NewPosterService(h.uow, h.repository, media, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	campaign, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "旧图规格", StartsAt: h.clock.Add(time.Hour), EndsAt: h.clock.Add(24 * time.Hour), IdempotencyKey: "layout-legacy-create"})
	if err != nil {
		t.Fatal(err)
	}
	c := referralport.SetCampaignPostersCommand{CampaignID: campaign.ID, ExpectedVersion: campaign.Version, ActorAdminID: 9001, IdempotencyKey: "layout-legacy-publish", Posters: []referralport.CampaignPosterSource{{ImageID: 1, Description: "旧说明", ShareText: "旧话术🙂\n保留"}}}
	if _, err = s.SetCampaignPosters(ctx, c); err != referralport.ErrPosterDimensions {
		t.Fatalf("default size: %v", err)
	}
	custom := referralport.PosterQRLayout{X: 350, Y: 550, Size: 200}
	c.Posters[0].QRLayout = &custom
	if _, err = s.SetCampaignPosters(ctx, c); err != nil {
		t.Fatal(err)
	}
	changed := c
	changed.Posters = append([]referralport.CampaignPosterSource(nil), c.Posters...)
	different := custom
	different.X++
	changed.Posters[0].QRLayout = &different
	if _, err = s.SetCampaignPosters(ctx, changed); err != referralport.ErrIdempotencyConflict {
		t.Fatalf("publish layout digest: %v", err)
	}
	if _, err = h.pool.Exec(ctx, "ALTER TABLE referral_campaign_posters DROP COLUMN qr_layout, DROP COLUMN image_width, DROP COLUMN image_height"); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../migrations/0231_referral_poster_layout.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.pool.Exec(ctx, string(body)); err != nil {
		t.Fatal(err)
	}
	readback, err := s.ListCampaignPosters(ctx, campaign.ID, true)
	if err != nil || readback[0].Width != 600 || readback[0].Height != 800 || readback[0].QRLayout != referralport.DefaultPosterQRLayout(600, 800) || readback[0].ShareText != c.Posters[0].ShareText || readback[0].Description != c.Posters[0].Description {
		t.Fatalf("legacy=%+v err=%v", readback, err)
	}
	var actual []byte
	if err = h.pool.QueryRow(ctx, "SELECT image_bytes FROM referral_campaign_posters WHERE campaign_id=$1", campaign.ID).Scan(&actual); err != nil || !bytes.Equal(actual, pngData.Bytes()) {
		t.Fatalf("migration changed bytes: %v", err)
	}
	got, err := s.SetCampaignPosterLayouts(ctx, referralport.SetCampaignPosterLayoutsCommand{CampaignID: campaign.ID, ExpectedVersion: campaign.Version + 1, ActorAdminID: 9001, IdempotencyKey: "layout-legacy-update", Posters: []referralport.PosterLayoutUpdate{{Slot: 1, PosterQRLayout: custom}}})
	if err != nil || got.Posters[0].QRLayout != custom {
		t.Fatalf("legacy update=%+v err=%v", got, err)
	}
	var width int
	if err = h.pool.QueryRow(ctx, "SELECT image_width FROM referral_campaign_posters WHERE campaign_id=$1", campaign.ID).Scan(&width); err != nil || width != 600 {
		t.Fatalf("legacy header persistence=%d err=%v", width, err)
	}
}
