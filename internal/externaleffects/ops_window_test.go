package externaleffects

import (
	"context"
	"testing"
	"time"
)

func TestPostgreSQLOpsReportWindowSeparatesStartedAndCompletedHours(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	repo := &Repository{pool: pool}
	hour := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	var effect int64
	if err := pool.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES('outbound','outbound_message',$1,$2,$3,$4,$5,'queued') RETURNING id`, digestForTest("window-source"), digestForTest("window-target"), digestForTest("window-payload"), digestForTest("window-policy"), digestForTest("window-envelope")).Scan(&effect); err != nil {
		t.Fatal(err)
	}
	for i, item := range []struct {
		start, end time.Time
		state      string
	}{
		{hour.Add(-time.Minute), hour, "executed"},
		{hour.Add(-2 * time.Minute), hour.Add(30 * time.Minute), "executed"},
		{hour, hour.Add(time.Minute), "executed"},
		{hour.Add(59 * time.Minute), hour.Add(61 * time.Minute), "executed"},
		{hour.Add(30 * time.Minute), hour.Add(time.Hour), "executed"},
		{hour.Add(time.Hour), hour.Add(62 * time.Minute), "executed"},
		{hour.Add(5 * time.Minute), hour.Add(6 * time.Minute), "outcome_unknown"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state,started_at,completed_at) VALUES($1,$2::integer,1,$2::bigint,$3,$4,$5)`, effect, i+1, item.state, item.start, item.end); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := repo.ReadOpsWindowCounts(ctx, hour, hour.Add(time.Hour))
	if err != nil || counts.Started != 4 || counts.Executed != 3 {
		t.Fatalf("original window: %+v %v", counts, err)
	}
	next, err := repo.ReadOpsWindowCounts(ctx, hour.Add(time.Hour), hour.Add(2*time.Hour))
	if err != nil || next.Started != 1 || next.Executed != 3 {
		t.Fatalf("completion window: %+v %v", next, err)
	}
	current, err := repo.ReadOpsDiagnosticCounts(ctx, hour.Add(65*time.Minute))
	if err != nil || current["previous_hour_attempts"] != 4 || current["previous_hour_executed"] != 3 {
		t.Fatalf("current preceding-hour view: %+v %v", current, err)
	}
	for _, window := range [][2]time.Time{{{}, hour}, {hour.Add(time.Second), hour.Add(time.Hour)}, {hour, hour.Add(2 * time.Hour)}} {
		if _, err := repo.ReadOpsWindowCounts(ctx, window[0], window[1]); err == nil {
			t.Fatalf("invalid reporting window accepted: %v", window)
		}
	}
}
