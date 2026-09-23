package adminops

import (
	"context"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	"time"
)

// RetentionHealth evaluates only the bound execution allowlist independently.
// A fresh allowlist never implies complete resource coverage: coverage_* metrics
// retain gaps even when every scheduled policy succeeds.
func (s *RetentionService) RetentionHealth(ctx context.Context, at time.Time) (opsport.CheckObservation, error) {
	metrics, err := s.coverageMetrics()
	o := opsport.CheckObservation{ObservedAt: at, Metrics: metrics, Status: "ok", Code: "allowlist_policies_fresh"}
	if err != nil {
		o.Status, o.Code = "unknown", "coverage_catalog_invalid"
		return o, err
	}
	if !s.enabled {
		o.Status = "uncovered"
		o.Code = "automatic_cleanup_disabled"
		return o, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, e := s.pool.Query(ctx, `SELECT DISTINCT ON(policy) policy,state,started_at,completed_at FROM adminops_retention_runs ORDER BY policy,hour_key DESC,id DESC`)
	if e != nil {
		return o, e
	}
	defer rows.Close()
	type observation struct {
		state string
		start time.Time
		end   *time.Time
	}
	latest := map[string]observation{}
	for rows.Next() {
		var policy string
		var v observation
		if e = rows.Scan(&policy, &v.state, &v.start, &v.end); e != nil {
			return o, e
		}
		latest[policy] = v
	}
	if e = rows.Err(); e != nil {
		return o, e
	}
	for _, p := range s.Policies() {
		if !validRetentionPolicy(p.ID) {
			continue
		}
		o.Metrics["expected_policies"]++
		v, found := latest[p.ID]
		if !found {
			o.Metrics["missing_policies"]++
			continue
		}
		if v.state == "failed" {
			o.Metrics["failed_policies"]++
			continue
		}
		if v.state == "running" {
			if at.Sub(v.start) > 10*time.Minute {
				o.Metrics["stalled_policies"]++
			} else {
				o.Metrics["running_policies"]++
			}
			continue
		}
		if v.end == nil || at.Sub(*v.end) > 75*time.Minute {
			o.Metrics["stale_policies"]++
		} else {
			o.Metrics["fresh_policies"]++
		}
	}
	switch {
	case o.Metrics["failed_policies"] > 0 || o.Metrics["stalled_policies"] > 0:
		o.Status = "warning"
		o.Code = "cleanup_failed_or_stalled"
	case o.Metrics["missing_policies"] > 0:
		o.Status = "unknown"
		o.Code = "cleanup_policy_not_observed"
	case o.Metrics["stale_policies"] > 0:
		o.Status = "stale"
		o.Code = "cleanup_observation_expired"
	case o.Metrics["running_policies"] > 0:
		o.Status = "unknown"
		o.Code = "cleanup_in_progress"
	}
	return o, nil
}
