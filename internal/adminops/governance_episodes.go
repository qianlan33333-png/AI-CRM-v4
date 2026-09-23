package adminops

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type GovernanceOutcomesOptions struct{ Now func() time.Time }
type GovernanceOutcomesService struct {
	pool     *pgxpool.Pool
	uow      platformport.UnitOfWork
	releases platformport.GovernanceReleaseReader
	now      func() time.Time
}

func NewGovernanceOutcomesService(pool *pgxpool.Pool, uow platformport.UnitOfWork, releases platformport.GovernanceReleaseReader, options GovernanceOutcomesOptions) (*GovernanceOutcomesService, error) {
	if pool == nil || uow == nil {
		return nil, ErrInspectionInvalid
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &GovernanceOutcomesService{pool, uow, releases, options.Now}, nil
}

// Called only inside the inspection completion UoW, after supersession checks.
// Never infer a historical start from an existing issue's cumulative first_seen.
func observeIncidentEpisode(ctx context.Context, tx pgx.Tx, issueID int64, r opsport.CheckResult, at time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO adminops_incident_episodes(issue_id,fingerprint,check_id,code,detected_release,detection_origin,initial_status,latest_status,detected_at,last_observed_at)
 SELECT i.id,i.fingerprint,i.check_id,i.code,
 COALESCE((SELECT CASE WHEN release_sha ~ '^[a-f0-9]{40}$' THEN release_sha ELSE 'unknown' END FROM adminops_inspection_runs WHERE id=$3),'unknown'),
 CASE WHEN i.first_seen<$4 AND NOT EXISTS(SELECT 1 FROM adminops_incident_episodes WHERE issue_id=i.id) THEN 'first_observed_existing_issue' ELSE 'new_observation' END,
 $2,$2,$4,$4 FROM adminops_inspection_issues i WHERE i.id=$1
 ON CONFLICT(issue_id) WHERE recovered_at IS NULL DO UPDATE SET
 last_observed_at=EXCLUDED.last_observed_at,latest_status=EXCLUDED.latest_status,version=adminops_incident_episodes.version+1
 WHERE adminops_incident_episodes.last_observed_at<EXCLUDED.last_observed_at`, issueID, r.Status, r.RunID, at)
	return err
}
func recoverIncidentEpisodes(ctx context.Context, tx pgx.Tx, checkID string, at time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE adminops_incident_episodes SET recovered_at=$2,latest_status='ok',version=version+1 WHERE check_id=$1 AND recovered_at IS NULL AND last_observed_at<=$2`, checkID, at)
	return err
}

const incidentColumns = `id,issue_id,check_id,code,detected_release,detection_origin,initial_status,latest_status,detected_at,last_observed_at,recovered_at,version,classification,escaped_defect,change_failure,caused_by_release_sequence,root_cause,remediation,fault_started_at,fault_start_basis,effort_minutes,attributed_at`

func scanIncident(row pgx.Row) (opsport.IncidentEpisode, error) {
	var out opsport.IncidentEpisode
	err := row.Scan(&out.ID, &out.IssueID, &out.CheckID, &out.Code, &out.DetectedRelease, &out.DetectionOrigin, &out.InitialStatus, &out.LatestStatus, &out.DetectedAt, &out.LastObservedAt, &out.RecoveredAt, &out.Version, &out.Classification, &out.EscapedDefect, &out.ChangeFailure, &out.CausedByReleaseSequence, &out.RootCause, &out.Remediation, &out.FaultStartedAt, &out.FaultStartBasis, &out.EffortMinutes, &out.AttributedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrInspectionNotFound
	}
	return out, err
}
func (s *GovernanceOutcomesService) Episode(ctx context.Context, id int64) (opsport.IncidentEpisode, error) {
	if ctx == nil || id < 1 {
		return opsport.IncidentEpisode{}, ErrInspectionInvalid
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return scanIncident(s.pool.QueryRow(bounded, `SELECT `+incidentColumns+` FROM adminops_incident_episodes WHERE id=$1`, id))
}
func (s *GovernanceOutcomesService) window(from, to time.Time) (opsport.GovernanceWindow, error) {
	now := s.now().UTC()
	if to.IsZero() {
		to = now
	}
	if from.IsZero() {
		from = to.Add(-720 * time.Hour)
	}
	w := opsport.GovernanceWindow{From: from.UTC(), To: to.UTC()}
	if !from.Before(to) || to.After(now.Add(time.Second)) || to.Sub(from) > 366*24*time.Hour {
		return w, ErrInspectionInvalid
	}
	return w, nil
}
func (s *GovernanceOutcomesService) Episodes(ctx context.Context, from, to time.Time, before int64, limit int) (opsport.IncidentEpisodePage, error) {
	out := opsport.IncidentEpisodePage{Items: []opsport.IncidentEpisode{}}
	if ctx == nil || before < 0 || limit < 1 || limit > 100 {
		return out, ErrInspectionInvalid
	}
	w, err := s.window(from, to)
	if err != nil {
		return out, err
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := s.pool.Query(bounded, `SELECT `+incidentColumns+` FROM adminops_incident_episodes WHERE detected_at>=$1 AND detected_at<$2 AND ($3::bigint=0 OR id<$3) ORDER BY id DESC LIMIT $4`, w.From, w.To, before, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		r, e := scanIncident(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, r)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.HasMore = true
		out.Items = out.Items[:limit]
		out.NextBeforeID = out.Items[len(out.Items)-1].ID
	}
	return out, nil
}
func inGovernanceEnum(value string, allowed ...string) bool {
	for _, s := range allowed {
		if value == s {
			return true
		}
	}
	return false
}
func validateAttribution(c opsport.IncidentAttributionCommand) bool {
	a := c.IncidentAttribution
	if c.ExpectedVersion < 1 || !inGovernanceEnum(a.Classification, "unclassified", "confirmed_defect", "expected_rejection", "legacy_backlog", "observation_gap", "non_defect") || !inGovernanceEnum(a.EscapedDefect, "unclassified", "yes", "no") || !inGovernanceEnum(a.ChangeFailure, "unclassified", "yes", "no") || !inGovernanceEnum(a.RootCause, "unknown", "code", "configuration", "dependency", "data", "infrastructure", "expected_behavior", "instrumentation") || !inGovernanceEnum(a.Remediation, "unknown", "code_fix", "configuration_fix", "rollback", "hotfix", "data_reconciliation", "dependency_recovery", "no_action") || !inGovernanceEnum(a.FaultStartBasis, "unknown", "monitor_evidence", "provider_receipt", "operator_confirmed") {
		return false
	}
	if a.EffortMinutes != nil && (*a.EffortMinutes < 0 || *a.EffortMinutes > 525600) {
		return false
	}
	if (a.FaultStartedAt == nil) != (a.FaultStartBasis == "unknown") {
		return false
	}
	if a.Classification != "confirmed_defect" && (a.EscapedDefect != "unclassified" || a.ChangeFailure != "unclassified" || a.FaultStartedAt != nil) {
		return false
	}
	if (a.ChangeFailure == "yes") != (a.CausedByReleaseSequence != nil) {
		return false
	}
	if a.CausedByReleaseSequence != nil && *a.CausedByReleaseSequence < 1 {
		return false
	}
	return true
}
func (s *GovernanceOutcomesService) Attribute(ctx context.Context, id, actor int64, key string, c opsport.IncidentAttributionCommand) (out opsport.IncidentAttributionReceipt, err error) {
	if ctx == nil || id < 1 || actor < 1 || len(key) < 8 || len(key) > 160 || strings.TrimSpace(key) != key || !validateAttribution(c) {
		return out, ErrInspectionInvalid
	}
	if c.FaultStartedAt != nil {
		t := c.FaultStartedAt.UTC()
		c.FaultStartedAt = &t
	}
	body, err := json.Marshal(c)
	if err != nil {
		return out, ErrInspectionInvalid
	}
	payload := string(effectport.Hash("ops-incident-attribution-payload-v1", strconv.FormatInt(id, 10), string(body)))
	request := string(effectport.Hash("ops-incident-attribution-v1", strconv.FormatInt(actor, 10), key))
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = s.uow.Within(bounded, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(txctx, `SET LOCAL lock_timeout='1500ms';SET LOCAL statement_timeout='3000ms'`); e != nil {
			return e
		}
		// Serialize identical keys before reading the receipt. Different keys still
		// compete through the episode row lock and expected-version comparison.
		if _, e = tx.Exec(txctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,7193))`, request); e != nil {
			return e
		}
		var oldPayload string
		e = tx.QueryRow(txctx, `SELECT episode_id,result_version,id,payload_digest FROM adminops_incident_attributions WHERE request_digest=$1`, request).Scan(&out.EpisodeID, &out.Version, &out.ActionID, &oldPayload)
		if e == nil {
			if oldPayload != payload {
				return ErrInspectionConflict
			}
			out.Replay = true
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		current, e := scanIncident(tx.QueryRow(txctx, `SELECT `+incidentColumns+` FROM adminops_incident_episodes WHERE id=$1 FOR UPDATE`, id))
		if e != nil {
			return e
		}
		if current.Version != c.ExpectedVersion {
			return ErrInspectionConflict
		}
		now := s.now().UTC()
		if c.FaultStartedAt != nil && (c.FaultStartedAt.After(current.DetectedAt) || c.FaultStartedAt.After(now)) {
			return ErrInspectionInvalid
		}
		if c.CausedByReleaseSequence != nil {
			var succeeded time.Time
			e = tx.QueryRow(txctx, `SELECT succeeded_at FROM adminops_governance_releases WHERE sequence=$1 AND NOT revoked FOR SHARE`, *c.CausedByReleaseSequence).Scan(&succeeded)
			if errors.Is(e, pgx.ErrNoRows) {
				return ErrInspectionInvalid
			}
			if e != nil {
				return e
			}
			start := current.DetectedAt
			if c.FaultStartedAt != nil {
				start = *c.FaultStartedAt
			}
			if succeeded.After(start) {
				return ErrInspectionInvalid
			}
		}
		_, e = tx.Exec(txctx, `UPDATE adminops_incident_episodes SET classification=$2,escaped_defect=$3,change_failure=$4,caused_by_release_sequence=$5,root_cause=$6,remediation=$7,fault_started_at=$8,fault_start_basis=$9,effort_minutes=$10,attributed_at=$11,version=version+1 WHERE id=$1`, id, c.Classification, c.EscapedDefect, c.ChangeFailure, c.CausedByReleaseSequence, c.RootCause, c.Remediation, c.FaultStartedAt, c.FaultStartBasis, c.EffortMinutes, now)
		if e != nil {
			return e
		}
		out.EpisodeID = id
		out.Version = c.ExpectedVersion + 1
		return tx.QueryRow(txctx, `INSERT INTO adminops_incident_attributions(episode_id,actor_id,request_digest,payload_digest,expected_version,result_version,attribution,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, id, actor, request, payload, c.ExpectedVersion, out.Version, body, now).Scan(&out.ActionID)
	})
	return out, err
}
