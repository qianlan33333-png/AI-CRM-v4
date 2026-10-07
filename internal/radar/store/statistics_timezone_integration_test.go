package store

import (
	"context"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

func TestPostgreSQLTodayStatisticsUsesShanghaiDayAcrossSessionTimezones(t *testing.T) {
	native, cleanup := radarIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	var start time.Time
	if err := native.QueryRow(ctx, `SELECT date_trunc('day',clock_timestamp(),'Asia/Shanghai')`).Scan(&start); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := native.QueryRow(ctx, `INSERT INTO radar_links(public_code,name,title,content_type,destination_url,auth_policy,status,created_by,updated_by,created_at,updated_at) VALUES('rd_1234567890abcdef','timezone','timezone','link','https://example.test/local','anonymous','enabled',1,1,$1,$1) RETURNING id`, start).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,1,'{}',1,$2)`, id, start); err != nil {
		t.Fatal(err)
	}
	for index, at := range []time.Time{start.Add(-time.Second), start.Add(time.Second)} {
		digest := make([]byte, 32)
		digest[0] = byte(index + 1)
		var session int64
		if err := native.QueryRow(ctx, `INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,attribution_status,expires_at,created_at) VALUES($1,$2,1,'anonymous',$3,$4) RETURNING id`, digest, id, start.Add(48*time.Hour), start).Scan(&session); err != nil {
			t.Fatal(err)
		}
		for _, stage := range []string{"landing", "content_opened"} {
			if _, err := native.Exec(ctx, `INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,key_digest,payload_digest,occurred_at,created_at) VALUES($1,$2,1,$3,$4,'anonymous',$5,$5,$6,$6)`, stage+string(rune('a'+index)), id, session, stage, digest, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range []string{"UTC", "America/Los_Angeles", "Asia/Shanghai"} {
		t.Run(zone, func(t *testing.T) {
			var stats radarport.Stats
			if err := uow.Within(ctx, func(txctx context.Context) error {
				tx, err := platformpostgres.RequireTransaction(txctx)
				if err != nil {
					return err
				}
				if _, err = tx.Exec(txctx, `SELECT set_config('TimeZone',$1,true)`, zone); err != nil {
					return err
				}
				stats, err = NewPostgres().Stats(txctx, radar.RadarID(id))
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if stats.TodayLandings != 1 || stats.TodayViews != 1 || stats.TotalLandings != 2 || stats.ViewCount != 2 {
				t.Fatalf("zone=%s Shanghai-day statistics=%+v; want today=1 and total=2", zone, stats)
			}
		})
	}
}
