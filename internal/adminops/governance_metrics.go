package adminops

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// RefreshReleaseEvidence imports only the trusted root bridge's bounded facts.
// No observed process/version projection is consulted. Failure is explicit;
// previously committed facts remain available without pretending full coverage.
func (s *GovernanceOutcomesService) RefreshReleaseEvidence(ctx context.Context) (platformport.GovernanceReleaseEvidence, error) {
	var evidence platformport.GovernanceReleaseEvidence
	if s.releases == nil {
		return evidence, errors.New("release evidence unavailable")
	}
	evidence, err := s.releases.ReadGovernanceReleases(ctx)
	if err != nil {
		return evidence, err
	}
	if evidence.Version != 1 || evidence.GeneratedAt.IsZero() || evidence.GeneratedAt.After(s.now().Add(time.Minute)) || !cpuProfileRelease.MatchString(evidence.CurrentRelease) || len(evidence.Facts) == 0 || len(evidence.Facts) > 2048 {
		return evidence, ErrInspectionInvalid
	}
	seen := map[int64]bool{}
	current := false
	for _, f := range evidence.Facts {
		if f.Sequence < 1 || seen[f.Sequence] || !cpuProfileRelease.MatchString(f.ReleaseSHA) || !cpuProfileSHA.MatchString(f.ReceiptDigest) || f.SucceededAt.IsZero() || f.SucceededAt.After(evidence.GeneratedAt.Add(time.Minute)) {
			return evidence, ErrInspectionInvalid
		}
		seen[f.Sequence] = true
		if f.ReleaseSHA == evidence.CurrentRelease && !f.Revoked {
			current = true
		}
	}
	if !current {
		return evidence, ErrInspectionInvalid
	}
	body, err := json.Marshal(evidence.Facts)
	if err != nil {
		return evidence, err
	}
	err = s.uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `SET LOCAL lock_timeout='1000ms';SET LOCAL statement_timeout='3000ms';SELECT pg_advisory_xact_lock(824194193)`); e != nil {
			return e
		}
		var conflict bool
		e = tx.QueryRow(txctx, `SELECT EXISTS(SELECT 1 FROM jsonb_to_recordset($1::jsonb) AS f(sequence bigint,release_sha text,succeeded_at timestamptz,receipt_digest text,revoked boolean) JOIN adminops_governance_releases r USING(sequence)
  WHERE r.release_sha<>f.release_sha OR r.succeeded_at<>f.succeeded_at OR r.receipt_digest<>f.receipt_digest OR (r.revoked AND NOT f.revoked))`, body).Scan(&conflict)
		if e != nil {
			return e
		}
		if conflict {
			return ErrInspectionConflict
		}
		_, e = tx.Exec(txctx, `INSERT INTO adminops_governance_releases(sequence,release_sha,succeeded_at,receipt_digest,revoked)
  SELECT sequence,release_sha,succeeded_at,receipt_digest,revoked FROM jsonb_to_recordset($1::jsonb) AS f(sequence bigint,release_sha text,succeeded_at timestamptz,receipt_digest text,revoked boolean) ON CONFLICT(sequence) DO NOTHING`, body)
		if e != nil {
			return e
		}
		_, e = tx.Exec(txctx, `UPDATE adminops_governance_releases r SET revoked=true FROM jsonb_to_recordset($1::jsonb) AS f(sequence bigint,revoked boolean) WHERE r.sequence=f.sequence AND f.revoked AND NOT r.revoked`, body)
		return e
	})
	return evidence, err
}
func (s *GovernanceOutcomesService) Outcomes(ctx context.Context, from, to time.Time) (out opsport.GovernanceOutcomes, err error) {
	if ctx == nil {
		return out, ErrInspectionInvalid
	}
	out.Window, err = s.window(from, to)
	if err != nil {
		return out, err
	}
	out.Cohort = "episodes_first_detected_in_window"
	out.HistoryScope = "observed_since_episode_instrumentation; no historical reconstruction"
	out.Deployments = opsport.GovernanceDeploymentMetrics{EvidenceState: "unavailable", Scope: "verified_successful_deployments_in_window_only", MissingFailedDeploymentFacts: true, Releases: []opsport.GovernanceReleaseChoice{}}
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	evidence, evidenceErr := s.RefreshReleaseEvidence(bounded)
	if evidenceErr == nil {
		out.Deployments.EvidenceState = "verified_subset"
		out.Deployments.EvidenceAt = &evidence.GeneratedAt
	}
	tx, err := s.pool.BeginTx(bounded, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(bounded, `SET LOCAL statement_timeout='3000ms'`)
	if err != nil {
		return out, err
	}
	err = tx.QueryRow(bounded, `WITH cohort AS (SELECT * FROM adminops_incident_episodes WHERE detected_at>=$1 AND detected_at<$2)
 SELECT count(*),count(*) FILTER(WHERE recovered_at IS NULL),count(*) FILTER(WHERE classification='confirmed_defect'),
 count(*) FILTER(WHERE classification='unclassified'),count(*) FILTER(WHERE classification NOT IN ('unclassified','confirmed_defect')),
 count(*) FILTER(WHERE initial_status IN ('unknown','uncovered','stale')),
 avg(EXTRACT(EPOCH FROM detected_at-fault_started_at)) FILTER(WHERE classification='confirmed_defect' AND fault_started_at IS NOT NULL),
 count(*) FILTER(WHERE classification='confirmed_defect' AND fault_started_at IS NOT NULL),
 count(*) FILTER(WHERE classification='confirmed_defect' AND fault_started_at IS NULL),
 avg(EXTRACT(EPOCH FROM recovered_at-detected_at)) FILTER(WHERE classification='confirmed_defect' AND recovered_at IS NOT NULL),
 count(*) FILTER(WHERE classification='confirmed_defect' AND recovered_at IS NOT NULL),
 count(*) FILTER(WHERE classification='confirmed_defect' AND recovered_at IS NULL),
 avg(EXTRACT(EPOCH FROM recovered_at-fault_started_at)) FILTER(WHERE classification='confirmed_defect' AND recovered_at IS NOT NULL AND fault_started_at IS NOT NULL),
 count(*) FILTER(WHERE classification='confirmed_defect' AND recovered_at IS NOT NULL AND fault_started_at IS NOT NULL),
 count(*) FILTER(WHERE classification='confirmed_defect' AND (recovered_at IS NULL OR fault_started_at IS NULL)),
 count(*) FILTER(WHERE classification='confirmed_defect' AND recovered_at IS NULL),
 count(*) FILTER(WHERE classification='confirmed_defect' AND escaped_defect='yes'),
 count(*) FILTER(WHERE classification='confirmed_defect' AND escaped_defect IN ('yes','no')),
 count(*) FILTER(WHERE classification='confirmed_defect' AND escaped_defect='unclassified'),
 count(*) FILTER(WHERE classification='confirmed_defect' AND EXISTS(SELECT 1 FROM adminops_incident_episodes prior WHERE prior.fingerprint=cohort.fingerprint AND prior.recovered_at<=cohort.detected_at AND prior.classification='confirmed_defect' AND prior.id<>cohort.id)),
 COALESCE(sum(effort_minutes),0),count(effort_minutes),count(*) FILTER(WHERE effort_minutes IS NULL)
 FROM cohort`, out.Window.From, out.Window.To).Scan(&out.EpisodeCount, &out.OpenCount, &out.ConfirmedDefects, &out.UnclassifiedEpisodes, &out.OtherClassifiedEpisodes, &out.UnknownOrUncoveredObservations, &out.MTTD.MeanSeconds, &out.MTTD.SampleCount, &out.MTTD.MissingCount, &out.DetectedToRecovered.MeanSeconds, &out.DetectedToRecovered.SampleCount, &out.DetectedToRecovered.MissingCount, &out.FaultToRecovered.MeanSeconds, &out.FaultToRecovered.SampleCount, &out.FaultToRecovered.MissingCount, &out.OpenConfirmedDefects, &out.EscapedDefects.Numerator, &out.EscapedDefects.Denominator, &out.EscapedDefects.Unclassified, &out.RepeatedDefects.Numerator, &out.EffortMinutesTotal, &out.EffortKnownCount, &out.EffortMissingCount)
	if err != nil {
		return out, err
	}
	out.RepeatedDefects.Denominator = out.ConfirmedDefects
	out.RepeatedDefects.Unclassified = out.UnclassifiedEpisodes
	setGovernanceRatio(&out.EscapedDefects)
	setGovernanceRatio(&out.RepeatedDefects)
	var gaps int64
	err = tx.QueryRow(bounded, `SELECT COALESCE(max(sequence),0)-count(*) FROM adminops_governance_releases`).Scan(&gaps)
	if err != nil {
		return out, err
	}
	if evidenceErr == nil {
		out.Deployments.HistoricalSequenceGaps = &gaps
	}
	err = tx.QueryRow(bounded, `SELECT count(*),count(*) FILTER(WHERE EXISTS(SELECT 1 FROM adminops_incident_episodes e WHERE e.caused_by_release_sequence=r.sequence AND e.classification='confirmed_defect' AND e.change_failure='yes')) FROM adminops_governance_releases r WHERE NOT revoked AND succeeded_at>=$1 AND succeeded_at<$2`, out.Window.From, out.Window.To).Scan(&out.Deployments.VerifiedSuccessfulDeployments, &out.Deployments.ConfirmedFailedDeployments)
	if err != nil {
		return out, err
	}
	err = tx.QueryRow(bounded, `SELECT count(*) FILTER(WHERE classification='confirmed_defect' AND change_failure='unclassified'),count(*) FILTER(WHERE classification='confirmed_defect' AND change_failure='yes' AND EXISTS(SELECT 1 FROM adminops_governance_releases r WHERE r.sequence=e.caused_by_release_sequence AND r.revoked)) FROM adminops_incident_episodes e WHERE detected_at>=$1 AND detected_at<$2`, out.Window.From, out.Window.To).Scan(&out.Deployments.UnclassifiedDefectAttributions, &out.Deployments.RevokedReleaseAttributions)
	if err != nil {
		return out, err
	}
	if evidenceErr == nil && out.Deployments.VerifiedSuccessfulDeployments > 0 {
		ratio := float64(out.Deployments.ConfirmedFailedDeployments) / float64(out.Deployments.VerifiedSuccessfulDeployments)
		out.Deployments.VerifiedSuccessCohortFailureRatio = &ratio
	}
	rows, err := tx.Query(bounded, `SELECT sequence,release_sha,succeeded_at FROM adminops_governance_releases WHERE NOT revoked AND succeeded_at>=$1 AND succeeded_at<$2 ORDER BY sequence DESC LIMIT 101`, out.Window.From, out.Window.To)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var choice opsport.GovernanceReleaseChoice
		if err = rows.Scan(&choice.Sequence, &choice.ReleaseSHA, &choice.SucceededAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Deployments.Releases = append(out.Deployments.Releases, choice)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Deployments.Releases) > 100 {
		out.Deployments.Releases = out.Deployments.Releases[:100]
		out.Deployments.ReleasesTruncated = true
	}
	err = tx.Commit(bounded)
	return out, err
}
func setGovernanceRatio(metric *opsport.GovernanceRatioMetric) {
	if metric.Denominator > 0 {
		value := float64(metric.Numerator) / float64(metric.Denominator)
		metric.Ratio = &value
	}
}
