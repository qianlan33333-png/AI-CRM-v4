package adminops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/riverqueue/river"
)

type reportWindowFixture struct {
	from, to time.Time
	calls    int
	fail     bool
}

func (r *reportWindowFixture) ReadOpsWindowCounts(_ context.Context, from, to time.Time) (effectport.OpsWindowCounts, error) {
	r.from, r.to, r.calls = from, to, r.calls+1
	if r.fail {
		return effectport.OpsWindowCounts{}, errors.New("sensitive upstream error must not enter report")
	}
	return effectport.OpsWindowCounts{Started: 3, Executed: 2}, nil
}

func TestPostgreSQLDelayedHourlyReportKeepsWindowAndCurrentBacklog(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "observed", true: "unavailable"}[unavailable], func(t *testing.T) {
			pool, uow := inspectionTestPool(t)
			ctx := context.Background()
			hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
			now := hour.Add(2*time.Hour + time.Minute)
			var collectedAt time.Time
			reader := &reportWindowFixture{fail: unavailable}
			collector := opsport.CollectorFunc{ID: "effects.outcomes", Read: func(_ context.Context, at time.Time) (opsport.CheckObservation, error) {
				collectedAt = at
				return opsport.CheckObservation{Status: "warning", Code: "attention_required", ObservedAt: at, Metrics: map[string]int64{"retryable": 7, "previous_hour_attempts": 91, "previous_hour_executed": 89}}, nil
			}}
			accept := &inspectionTestAccepter{}
			service, err := NewInspectionService(pool, uow, []opsport.InspectionCollector{collector}, accept, InspectionOptions{ReleaseSHA: "window-fixture", Now: func() time.Time { return now }, WindowReader: reader, NotificationEnabled: true, NotificationTargetRef: "fixture-original"})
			if err != nil {
				t.Fatal(err)
			}
			worker := NewReportWorker()
			if err = worker.BindService(service); err != nil {
				t.Fatal(err)
			}
			job := &river.Job[OpsReportJobArgs]{Args: OpsReportJobArgs{Hour: hour}}
			if err = worker.Work(ctx, job); err != nil {
				t.Fatal(err)
			}
			if !reader.from.Equal(hour) || !reader.to.Equal(hour.Add(time.Hour)) || !collectedAt.Equal(now) {
				t.Fatalf("window or observation moved: reader=%+v collect=%s now=%s", reader, collectedAt, now)
			}
			reports, err := service.Reports(ctx)
			if err != nil || len(reports) != 1 {
				t.Fatalf("reports=%+v %v", reports, err)
			}
			var message struct {
				Content struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if err = json.Unmarshal(reports[0].Content, &message); err != nil {
				t.Fatal(err)
			}
			text := message.Content.Text
			if strings.Contains(text, "previous_hour_") || strings.Contains(text, "sensitive upstream") || !strings.Contains(text, "retryable（保留状态总数）=7") {
				t.Fatalf("mixed window or unsafe message: %s", text)
			}
			if unavailable {
				if !strings.Contains(text, "[unknown] 原窗口数据未取得") || strings.Contains(text, "开始尝试=0") {
					t.Fatalf("unavailable became zero: %s", text)
				}
			} else if !strings.Contains(text, "开始尝试=3，执行完成=2") {
				t.Fatalf("wrong fixed window: %s", text)
			}
			// A retry after the clock and underlying counts change must reuse
			// the already frozen content without querying a new report window.
			frozen := append([]byte(nil), reports[0].Content...)
			now = now.Add(time.Hour)
			reader.fail = !unavailable
			if err = worker.Work(ctx, job); err != nil {
				t.Fatal(err)
			}
			again, err := service.Reports(ctx)
			var hourly []opsport.OpsReport
			for _, item := range again {
				if item.NotificationKind == "hourly" {
					hourly = append(hourly, item)
				}
			}
			if err != nil || len(hourly) != 1 || !bytes.Equal(frozen, hourly[0].Content) || reader.calls != 1 {
				t.Fatalf("replay changed accepted hourly report: calls=%d hourly_count=%d err=%v", reader.calls, len(hourly), err)
			}
			var accepted int
			source := effectport.Hash("ops-report-source-v1", hour.Format(time.RFC3339))
			key := effectport.Hash("ops-report-accept-v1", string(source))
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM inspection_test_acceptances WHERE key=$1`, string(key)).Scan(&accepted); err != nil || accepted != 1 || hourly[0].EffectID == "" || hourly[0].EffectID != reports[0].EffectID {
				t.Fatalf("replay changed accepted intent: count=%d err=%v", accepted, err)
			}
		})
	}
}
