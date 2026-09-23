package adminops

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

const cpuProfileLock int64 = 824194021
const cpuProfileCapacity int64 = platformport.CPUProfileCapacityBytes

var cpuProfileIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var cpuProfileSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)
var cpuProfileRelease = regexp.MustCompile(`^[a-f0-9]{40}$`)
var (
	ErrCPUProfileDisabled = errors.New("CPU sampling disabled")
	ErrCPUProfileBusy     = errors.New("CPU sampling busy")
	ErrCPUProfileCapacity = errors.New("CPU sampling capacity reached")
	ErrCPUProfileExpired  = errors.New("CPU profile expired")
	ErrCPUProfileUnknown  = errors.New("CPU profile result unavailable")
)

type CPUProfileRecord struct {
	ID              string     `json:"id"`
	Target          string     `json:"target"`
	ReleaseSHA      string     `json:"release_sha"`
	DurationSeconds int        `json:"duration_seconds"`
	State           string     `json:"state"`
	FailureCode     string     `json:"failure_code,omitempty"`
	Bytes           int64      `json:"bytes"`
	AcceptedAt      time.Time  `json:"accepted_at"`
	ExpiresAt       time.Time  `json:"expires_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	Replay          bool       `json:"replay"`
	SHA256          string     `json:"-"`
}

type CPUProfileService struct {
	pool     *pgxpool.Pool
	profiler platformport.CPUProfiler
	release  string
	enabled  bool
}

func NewCPUProfileService(pool *pgxpool.Pool, profiler platformport.CPUProfiler, release string, enabled bool) (*CPUProfileService, error) {
	if pool == nil || profiler == nil {
		return nil, ErrInspectionInvalid
	}
	// Keep optional diagnostics from breaking existing startup/test roles, and
	// never put arbitrary release metadata into the profile receipt or response.
	if !cpuProfileRelease.MatchString(release) {
		release = "unknown"
	}
	return &CPUProfileService{pool: pool, profiler: profiler, release: release, enabled: enabled}, nil
}

const profileRecordColumns = `id,release_sha,
 CASE WHEN state='sampling' AND accepted_at<clock_timestamp()-interval '30 seconds' THEN 'outcome_unknown' ELSE state END,
 failure_code,artifact_bytes,accepted_at,expires_at,completed_at,artifact_sha256`

func scanCPUProfile(row pgx.Row) (CPUProfileRecord, error) {
	r := CPUProfileRecord{Target: "api", DurationSeconds: 5}
	err := row.Scan(&r.ID, &r.ReleaseSHA, &r.State, &r.FailureCode, &r.Bytes, &r.AcceptedAt, &r.ExpiresAt, &r.CompletedAt, &r.SHA256)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrInspectionNotFound
	}
	return r, err
}

// Sampling is intentionally synchronous and never resumes. Short acceptance and
// completion transactions surround it; the exclusive connection keeps a session
// advisory lock, not an open transaction, during the five-second observation.
func (s *CPUProfileService) Capture(ctx context.Context, actor int64, key string) (out CPUProfileRecord, err error) {
	if ctx == nil || actor < 1 || len(key) < 8 || len(key) > 160 || strings.TrimSpace(key) != key {
		return out, ErrInspectionInvalid
	}
	if !s.enabled {
		return out, ErrCPUProfileDisabled
	}
	digest := sha256.Sum256([]byte("adminops.cpu-profile.v1\x00" + key))
	request := hex.EncodeToString(digest[:])
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := s.pool.Acquire(bounded)
	if err != nil {
		return out, err
	}
	held := false
	defer releaseCPUProfileConnection(conn, &held)
	var previousActor int64
	lookup := func() (CPUProfileRecord, error) {
		r, e := scanCPUProfile(conn.QueryRow(bounded, `SELECT `+profileRecordColumns+` FROM adminops_cpu_profile_receipts WHERE request_digest=$1`, request))
		if e != nil {
			return r, e
		}
		if e = conn.QueryRow(bounded, `SELECT actor_id FROM adminops_cpu_profile_receipts WHERE request_digest=$1`, request).Scan(&previousActor); e != nil {
			return r, e
		}
		if previousActor != actor {
			return r, ErrInspectionConflict
		}
		r.Replay = true
		return r, nil
	}
	if r, e := lookup(); !errors.Is(e, ErrInspectionNotFound) {
		return r, e
	}
	held = true // Until confirmed otherwise, cancellation may hide a granted lock.
	var acquired bool
	if err = conn.QueryRow(bounded, `SELECT pg_try_advisory_lock($1)`, cpuProfileLock).Scan(&acquired); err != nil {
		return out, err
	}
	held = acquired
	if !held {
		return out, ErrCPUProfileBusy
	}
	if r, e := lookup(); !errors.Is(e, ErrInspectionNotFound) {
		return r, e
	}
	tx, err := conn.Begin(bounded)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(bounded, `SET LOCAL statement_timeout='2000ms';SET LOCAL lock_timeout='250ms'`); err != nil {
		return out, err
	}
	var global, personal, active int
	var reserved int64
	if err = tx.QueryRow(bounded, `SELECT count(*) FILTER(WHERE accepted_at>clock_timestamp()-interval '1 hour'),count(*) FILTER(WHERE actor_id=$1 AND accepted_at>clock_timestamp()-interval '1 hour'),count(*) FILTER(WHERE state='sampling' AND accepted_at>clock_timestamp()-interval '30 seconds'),COALESCE(sum(CASE WHEN state='failed' THEN 0 WHEN state='completed' THEN artifact_bytes ELSE 2097152 END),0) FROM adminops_cpu_profile_receipts WHERE expires_at>clock_timestamp()`, actor).Scan(&global, &personal, &active, &reserved); err != nil {
		return out, err
	}
	// A lost database connection can release the session lock before the local
	// fixed-duration profiler notices. A recent acceptance conservatively blocks
	// other processes beyond the entire bounded synchronous request budget.
	if active > 0 {
		return out, ErrCPUProfileBusy
	}
	if global >= 6 || personal >= 3 {
		return out, ErrInspectionRateLimited
	}
	if reserved > cpuProfileCapacity-platformport.CPUProfileMaxBytes {
		return out, ErrCPUProfileCapacity
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return out, err
	}
	id := hex.EncodeToString(random[:])
	out, err = scanCPUProfile(tx.QueryRow(bounded, `INSERT INTO adminops_cpu_profile_receipts(id,request_digest,actor_id,release_sha,state,accepted_at,expires_at)
 VALUES($1,$2,$3,$4,'sampling',statement_timestamp(),statement_timestamp()+interval '720 hours') RETURNING `+profileRecordColumns, id, request, actor, s.release))
	if err != nil {
		return out, err
	}
	if err = tx.Commit(bounded); err != nil {
		return out, err
	}
	artifact, captureErr := s.profiler.Capture(bounded, id)
	if errors.Is(captureErr, platformport.ErrCPUProfileUncertain) {
		out.State = "outcome_unknown"
		return out, ErrCPUProfileUnknown
	}
	state, code := "completed", ""
	if captureErr != nil {
		state, code = "failed", "capture_failed"
		artifact = platformport.CPUProfileArtifact{}
		if errors.Is(captureErr, platformport.ErrCPUProfileBusy) {
			code = "profile_busy"
		}
		if errors.Is(captureErr, platformport.ErrCPUProfileTooLarge) {
			code = "profile_too_large"
		}
		if errors.Is(captureErr, platformport.ErrCPUProfileCapacity) {
			code = "profile_capacity"
		}
	} else if artifact.Bytes < 1 || artifact.Bytes > platformport.CPUProfileMaxBytes || !cpuProfileSHA.MatchString(artifact.SHA256) {
		// An invalid success may have produced an artifact. Preserve its full
		// reservation and unknown result, never free capacity or permit replay.
		out.State = "outcome_unknown"
		return out, ErrCPUProfileUnknown
	}
	finish, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer finishCancel()
	out, err = scanCPUProfile(conn.QueryRow(finish, `UPDATE adminops_cpu_profile_receipts SET state=$2,failure_code=$3,artifact_sha256=$4,artifact_bytes=$5,completed_at=clock_timestamp() WHERE id=$1 AND state='sampling' RETURNING `+profileRecordColumns, id, state, code, artifact.SHA256, artifact.Bytes))
	if err != nil {
		return CPUProfileRecord{ID: id, Target: "api", ReleaseSHA: s.release, DurationSeconds: 5, State: "outcome_unknown"}, ErrCPUProfileUnknown
	}
	return out, nil
}

func releaseCPUProfileConnection(conn *pgxpool.Conn, held *bool) {
	if *held {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1)`, cpuProfileLock).Scan(&unlocked); err != nil || !unlocked {
			// Never return a possibly still-locked session to the pool.
			_ = conn.Hijack().Close(ctx)
			return
		}
	}
	conn.Release()
}

func (s *CPUProfileService) List(ctx context.Context) ([]CPUProfileRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT `+profileRecordColumns+` FROM adminops_cpu_profile_receipts WHERE expires_at>clock_timestamp() ORDER BY accepted_at DESC,id DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CPUProfileRecord{}
	for rows.Next() {
		r, e := scanCPUProfile(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *CPUProfileService) Get(ctx context.Context, id string) (CPUProfileRecord, error) {
	if !cpuProfileIDPattern.MatchString(id) {
		return CPUProfileRecord{}, ErrInspectionInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	r, err := scanCPUProfile(s.pool.QueryRow(ctx, `SELECT `+profileRecordColumns+` FROM adminops_cpu_profile_receipts WHERE id=$1`, id))
	if err != nil {
		return r, err
	}
	var fresh bool
	if err = s.pool.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM adminops_cpu_profile_receipts WHERE id=$1`, id).Scan(&fresh); err != nil {
		return CPUProfileRecord{}, err
	}
	if !fresh {
		return CPUProfileRecord{}, ErrCPUProfileExpired
	}
	return r, nil
}
func (s *CPUProfileService) Download(ctx context.Context, id string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	r, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var fresh bool
	if err = s.pool.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM adminops_cpu_profile_receipts WHERE id=$1`, id).Scan(&fresh); err != nil {
		return nil, err
	}
	if !fresh {
		return nil, ErrCPUProfileExpired
	}
	if r.State != "completed" {
		return nil, ErrCPUProfileUnknown
	}
	payload, err := s.profiler.Read(ctx, id)
	if err != nil {
		return nil, ErrCPUProfileUnknown
	}
	digest := sha256.Sum256(payload)
	if int64(len(payload)) != r.Bytes || hex.EncodeToString(digest[:]) != r.SHA256 {
		return nil, ErrCPUProfileUnknown
	}
	// Recheck after the file read; a request cannot open just before expiry and
	// publish a profile that has already crossed the online retention boundary.
	if err = s.pool.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM adminops_cpu_profile_receipts WHERE id=$1`, id).Scan(&fresh); err != nil {
		return nil, err
	}
	if !fresh {
		return nil, ErrCPUProfileExpired
	}
	return payload, nil
}
