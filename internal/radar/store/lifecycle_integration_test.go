package store

import (
	"context"
	"errors"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarapp "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/app"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

func TestPostgreSQLRadarFirstAndConsecutiveSaves(t *testing.T) {
	pool, cleanup := radarIntegrationPool(t)
	defer cleanup()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewPostgres()
	service, err := radarapp.NewService(uow, repo, repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	content := radar.Content{Type: radar.ContentTypeLink, DestinationURL: "https://console.cloud.tencent.com/lighthouse/instance/detail?searchParams=rid%3D4&rid=4&tab=domain"}
	created, err := service.Create(ctx, radarport.CreateCommand{Name: "测试雷达", Title: "测试雷达", Content: content, AuthPolicy: radar.AuthPolicyUnionIDRequired, ActorID: 1, IdempotencyKey: "radar-first-save-create"})
	if err != nil {
		t.Fatal(err)
	}
	enable := radarport.SetStatusCommand{RadarID: created.Link.ID, Expected: created.Link.Version, Target: radar.StatusEnabled, ActorID: 1, IdempotencyKey: "radar-first-save-enable"}
	enabled, err := service.SetStatus(ctx, enable)
	if err != nil {
		t.Fatalf("first save enable: %v", err)
	}
	if enabled.Link.Status != radar.StatusEnabled || enabled.Link.Version != 2 {
		t.Fatalf("first save=%+v", enabled.Link)
	}
	replay, err := service.SetStatus(ctx, enable)
	if err != nil || replay.Link.Version != 2 {
		t.Fatalf("enable replay=%+v err=%v", replay, err)
	}
	update := radarport.UpdateCommand{RadarID: created.Link.ID, Expected: 2, Revision: radar.Revision{Name: "测试雷达修改", Title: "测试雷达修改", Content: content, AuthPolicy: radar.AuthPolicyUnionIDRequired}, ActorID: 1, IdempotencyKey: "radar-second-save-update"}
	updated, err := service.Update(ctx, update)
	if err != nil || updated.Link.Version != 3 {
		t.Fatalf("second save=%+v err=%v", updated, err)
	}
	for i, target := range []radar.Status{radar.StatusDisabled, radar.StatusEnabled} {
		updated, err = service.SetStatus(ctx, radarport.SetStatusCommand{RadarID: created.Link.ID, Expected: updated.Link.Version, Target: target, ActorID: 1, IdempotencyKey: "radar-consecutive-save-" + string(target)})
		if err != nil || updated.Link.Version != radar.LinkVersion(4+i) {
			t.Fatalf("consecutive %s=%+v err=%v", target, updated, err)
		}
	}
	update.IdempotencyKey = "radar-stale-version-save"
	if _, err = service.Update(ctx, update); !errors.Is(err, radar.ErrVersionConflict) {
		t.Fatalf("stale save error=%v", err)
	}
	for table, want := range map[string]int{"radar_links": 1, "radar_link_versions": 5, "radar_operation_receipts": 5, "radar_audit_events": 5, "radar_outbox": 5} {
		var count int
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
	var lifecycleEvents int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM radar_outbox WHERE event_type IN ('radar.link_enabled','radar.link_disabled')").Scan(&lifecycleEvents); err != nil || lifecycleEvents != 3 {
		t.Fatalf("lifecycle events=%d err=%v", lifecycleEvents, err)
	}
}
