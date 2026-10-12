package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"
)

func TestPostgreSQLOpsArchivedRefreshFailureIsHistory(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	at := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	var activeRun int64
	for i, item := range []struct{ lifecycle, code string }{{"archived", "refresh_unavailable"}, {"active", "refresh_unavailable"}, {"active", "reference_superseded"}} {
		var pkg, config, run int64
		name := fmt.Sprintf("ops-%d", i)
		if err := pool.QueryRow(ctx, `INSERT INTO segment_audience_packages(code,name,lifecycle,archived_at,created_by,created_actor_kind,created_actor_ref,updated_by,updated_actor_kind,updated_actor_ref,created_at,updated_at) VALUES($1,$1,$2,CASE WHEN $2='archived' THEN $3::timestamptz ELSE NULL END,1,'admin','admin:1',1,'admin','admin:1',$3,$3) RETURNING id`, name, item.lifecycle, at).Scan(&pkg); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(name))
		if err := pool.QueryRow(ctx, `INSERT INTO segment_audience_configuration_versions(package_id,version,schema_version,definition,digest,refresh_mode,created_by,created_actor_kind,created_actor_ref,created_at) VALUES($1,1,1,'{}',$2,'manual',1,'admin','admin:1',$3) RETURNING id`, pkg, digest[:], at).Scan(&config); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO segment_audience_refresh_runs(package_id,configuration_version_id,source_key_digest,reference_time,state,error_code,created_at,updated_at,completed_at) VALUES($1,$2,$3,$4,'failed',$5,$4,$4,$4) RETURNING id`, pkg, config, digest[:], at.Add(-time.Hour), item.code).Scan(&run); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			activeRun = run
		}
	}
	reader := NewOpsDiagnosticReader(pool)
	counts, err := reader.ReadOpsDiagnosticCounts(ctx, at)
	if err != nil || counts["refresh_failed"] != 3 || counts["refresh_current_failed"] != 1 {
		t.Fatalf("archived and superseded history must not hide the active failure: %v %v", counts, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO segment_audience_refresh_runs(package_id,configuration_version_id,source_key_digest,reference_time,state,created_at,updated_at,completed_at) SELECT package_id,configuration_version_id,$2,$3,'published',$3,$3,$3 FROM segment_audience_refresh_runs WHERE id=$1`, activeRun, make([]byte, 32), at); err != nil {
		t.Fatal(err)
	}
	counts, err = reader.ReadOpsDiagnosticCounts(ctx, at)
	if err != nil || counts["refresh_failed"] != 3 || counts["refresh_current_failed"] != 0 {
		t.Fatalf("new successful run must clear current failure without erasing history: %v %v", counts, err)
	}
}
