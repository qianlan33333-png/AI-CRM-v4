package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	hxcstore "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/store"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var ErrNotReady = errors.New("hxc dashboard is not ready")
var ErrConflict = errors.New("hxc dashboard refresh already active")

type JobEnqueuer interface {
	Enqueue(context.Context, int64) error
}
type Audit interface {
	Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
}

type Service struct {
	Enabled              bool
	Scope                string
	SubjectKey           []byte
	Source               hxcport.CurrentSource
	Identity             identityport.HXCIdentityCoordinator
	RegistrationCoverage identityport.HXCRegistrationCoverage
	IdentityWriteEnabled bool
	UnionIDVerified      bool
	Store                *hxcstore.PostgreSQL
	Enqueuer             JobEnqueuer
	Audit                Audit
	UOW                  platformport.UnitOfWork
	Now                  func() time.Time
}

func (s Service) Ready() bool {
	return s.Enabled && strings.HasPrefix(s.Scope, "wechat-open-platform:") && len(s.SubjectKey) >= 32 && s.Source != nil && s.Source.Ready() && s.Identity != nil && s.Store != nil && s.Enqueuer != nil && s.Audit != nil && s.UOW != nil
}
func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s Service) Create(ctx context.Context, runKey, trigger string, requestedBy int64) (domain.RefreshRun, bool, error) {
	if !s.Ready() || runKey == "" || !validTrigger(trigger, requestedBy) {
		return domain.RefreshRun{}, false, ErrNotReady
	}
	digest := sha256.Sum256([]byte(trigger + "\x00" + runKey))
	var run domain.RefreshRun
	var replay bool
	err := s.UOW.Within(ctx, func(txCtx context.Context) error {
		var err error
		identityMode := "inspect"
		if s.IdentityWriteEnabled {
			identityMode = "apply"
		}
		run, replay, err = s.Store.CreateRun(txCtx, runKey, digest, trigger, identityMode, requestedBy)
		if err != nil {
			if errors.Is(err, hxcstore.ErrActiveRefresh) {
				return ErrConflict
			}
			return err
		}
		if replay {
			return nil
		}
		if err = s.Enqueuer.Enqueue(txCtx, run.ID); err != nil {
			return err
		}
		actor, actorID := "system", ""
		if requestedBy > 0 {
			actor = "admin"
			actorID = strconv.FormatInt(requestedBy, 10)
		}
		payload, _ := json.Marshal(map[string]string{"trigger": trigger})
		_, err = s.Audit.Append(txCtx, platformaudit.Event{IdempotencyKey: idempotency.Key("hxc-dashboard-refresh-created:" + hex.EncodeToString(digest[:])), Action: "hxc.dashboard_refresh_created", ActorType: actor, ActorID: actorID, ResourceType: "hxc_dashboard_refresh", ResourceID: strconv.FormatInt(run.ID, 10), Payload: payload})
		return err
	})
	return run, replay, err
}
func validTrigger(trigger string, requestedBy int64) bool {
	return trigger == "manual" && requestedBy > 0 || (trigger == "scheduled" || trigger == "initial") && requestedBy == 0
}
func (s Service) Get(ctx context.Context, id int64) (domain.RefreshRun, error) {
	return s.Store.GetRun(ctx, id, "")
}

func (s Service) Refresh(ctx context.Context, runID int64) error {
	if !s.Ready() || runID < 1 {
		return ErrNotReady
	}
	run, err := s.Get(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == domain.RefreshSucceeded || run.Status == domain.RefreshFailed {
		return nil
	}
	applyIdentities := run.IdentityMode == "apply"
	if applyIdentities && !s.IdentityWriteEnabled {
		return s.fail(ctx, runID, "identity_writes_disabled", errors.New("HXC identity writes are disabled"))
	}
	if err = s.UOW.Within(ctx, func(txCtx context.Context) error { return s.Store.MarkRunning(txCtx, runID) }); err != nil {
		return err
	}
	if err = s.Source.Preflight(ctx); err != nil {
		return s.fail(ctx, runID, "source_preflight_failed", err)
	}
	asOf := s.now()
	snapshot, err := s.Source.ReadSnapshot(ctx, asOf)
	if err != nil {
		return s.fail(ctx, runID, "source_read_failed", err)
	}
	projection, err := s.project(ctx, snapshot, applyIdentities)
	if err != nil {
		return s.fail(ctx, runID, "identity_resolution_failed", err)
	}
	if err = s.UOW.Within(ctx, func(txCtx context.Context) error {
		return s.Store.MarkPublishing(txCtx, runID, int64(len(projection.Rows)), projection.IdentityReplayVerified)
	}); err != nil {
		return err
	}
	err = s.UOW.Within(ctx, func(txCtx context.Context) error {
		id, err := s.Store.Publish(txCtx, runID, projection)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"projection_id": id, "total": projection.Counts.Total})
		_, err = s.Audit.Append(txCtx, platformaudit.Event{IdempotencyKey: idempotency.Key(fmt.Sprintf("hxc-dashboard-published:%d", runID)), Action: "hxc.dashboard_published", ActorType: "system", ResourceType: "hxc_dashboard_projection", ResourceID: strconv.FormatInt(id, 10), Payload: payload})
		return err
	})
	if err != nil {
		return s.fail(ctx, runID, "publish_failed", err)
	}
	return nil
}

func (s Service) project(ctx context.Context, snapshot hxcport.Snapshot, applyIdentities bool) (domain.Projection, error) {
	projection := domain.Projection{AsOf: snapshot.AsOf, Watermark: snapshot.Watermark, SourceDigest: snapshot.Digest, SharedFactsAvailable: true, Rows: make([]domain.ProjectionRow, len(snapshot.Rows))}
	subjects := make([]identityport.HXCSubject, len(snapshot.Rows))
	unionOwners := make(map[string][]int)
	phoneOwners := make(map[string][]int)
	for i, row := range snapshot.Rows {
		digest, ref, err := domain.Subject(s.SubjectKey, row.HXCUserID)
		if err != nil {
			return domain.Projection{}, err
		}
		phone, validPhone := normalizeCN11(row.Phone)
		payloadDigest := hxcPayloadDigest(s.SubjectKey, row)
		subjects[i] = identityport.HXCSubject{Position: i, SubjectDigest: digest, PayloadDigest: payloadDigest, UnionIDScope: s.Scope, UnionID: strings.TrimSpace(row.UnionID), UnionIDVerified: s.UnionIDVerified, Phone: phone, SourceUpdatedAt: row.SourceUpdatedAt, RuleVersion: domain.RuleVersion}
		if row.Phone != "" && !validPhone {
			subjects[i].ConflictReason = identityport.HXCReasonInvalidPhone
		}
		if subjects[i].UnionID != "" {
			unionOwners[subjects[i].UnionID] = append(unionOwners[subjects[i].UnionID], i)
		}
		if validPhone && phone != "" {
			phoneOwners[phone] = append(phoneOwners[phone], i)
		}
		projection.Rows[i] = domain.ProjectionRow{SubjectDigest: digest, UserRef: ref, Stage: domain.Classify(row, snapshot.AsOf), SourceRow: row, IdentityState: domain.Unmatched, MatchedBy: string(identityport.HXCMatchNone), IdentityReasonCode: string(identityport.HXCReasonNoMatch)}
		projection.Rows[i].HXCUserID = ""
		projection.Rows[i].UnionID = ""
		projection.Rows[i].Phone = ""
	}
	markDuplicateSubjects(subjects, unionOwners, identityport.HXCReasonDuplicateUnionID)
	markDuplicateSubjects(subjects, phoneOwners, identityport.HXCReasonDuplicatePhone)
	results := make([]identityport.HXCSubjectResult, len(subjects))
	for start := 0; start < len(subjects); start += 1000 {
		end := start + 1000
		if end > len(subjects) {
			end = len(subjects)
		}
		var batch []identityport.HXCSubjectResult
		err := s.UOW.Within(ctx, func(txCtx context.Context) error {
			var err error
			batch, err = s.Identity.InspectHXCSubjects(txCtx, subjects[start:end])
			return err
		})
		if err != nil {
			return domain.Projection{}, err
		}
		if len(batch) != end-start {
			return domain.Projection{}, errors.New("HXC identity result count mismatch")
		}
		for _, result := range batch {
			results[result.Position] = result
		}
	}
	owners := map[int64][]int{}
	for i, result := range results {
		if result.Disposition == identityport.HXCMatched {
			owners[int64(result.CustomerID)] = append(owners[int64(result.CustomerID)], i)
		}
	}
	for _, positions := range owners {
		if len(positions) > 1 {
			for _, i := range positions {
				subjects[i].ConflictReason = identityport.HXCReasonDuplicateCustomer
				results[i] = identityport.HXCSubjectResult{Position: i, Disposition: identityport.HXCConflict, MatchedBy: identityport.HXCMatchNone, Reason: identityport.HXCReasonDuplicateCustomer}
			}
		}
	}
	if applyIdentities {
		for i := range subjects {
			err := s.UOW.Within(ctx, func(txCtx context.Context) error {
				var applyErr error
				results[i], applyErr = s.Identity.ApplyHXCSubject(txCtx, subjects[i])
				return applyErr
			})
			if err != nil {
				return domain.Projection{}, err
			}
			var replay identityport.HXCSubjectResult
			err = s.UOW.Within(ctx, func(txCtx context.Context) error {
				var replayErr error
				replay, replayErr = s.Identity.ApplyHXCSubject(txCtx, subjects[i])
				return replayErr
			})
			if err != nil || !sameHXCResult(results[i], replay) || !replay.Replayed {
				if err != nil {
					return domain.Projection{}, err
				}
				return domain.Projection{}, errors.New("HXC identity replay verification failed")
			}
			projection.IdentityReplayVerified++
		}
		seen := make([][32]byte, 0, len(subjects))
		for _, subject := range subjects {
			seen = append(seen, subject.SubjectDigest)
		}
		if err := s.UOW.Within(ctx, func(txCtx context.Context) error {
			return s.Identity.CompleteHXCSnapshot(txCtx, seen)
		}); err != nil {
			return domain.Projection{}, err
		}
	}
	if s.RegistrationCoverage != nil {
		err := s.UOW.Within(ctx, func(txCtx context.Context) error {
			states, e := s.RegistrationCoverage.InspectHXCRegistrationCoverage(txCtx, subjects, snapshot.Complete)
			if e != nil {
				return e
			}
			projection.RegistrationCoverage = map[int64]string{}
			for id, state := range states {
				projection.RegistrationCoverage[int64(id)] = string(state)
			}
			return nil
		})
		if err != nil {
			return domain.Projection{}, err
		}
	}
	for i, result := range results {
		applyIdentityResult(&projection.Rows[i], result)
	}
	h := sha256.New()
	sort.Slice(projection.Rows, func(i, j int) bool {
		return string(projection.Rows[i].SubjectDigest[:]) < string(projection.Rows[j].SubjectDigest[:])
	})
	for _, row := range projection.Rows {
		encoded, _ := json.Marshal(row)
		h.Write(encoded)
		projection.Counts.Total++
		switch row.Stage {
		case domain.ActiveUsed:
			projection.Counts.ActiveUsed++
		case domain.ActiveUnused:
			projection.Counts.ActiveUnused++
		default:
			projection.Counts.RegisteredNoActiveMembership++
		}
		switch row.IdentityState {
		case domain.Matched:
			projection.Counts.Matched++
			switch row.MatchedBy {
			case string(identityport.HXCMatchUnionID):
				projection.Counts.MatchedByUnionID++
			case string(identityport.HXCMatchPhone):
				projection.Counts.MatchedByPhone++
			case string(identityport.HXCMatchBoth):
				projection.Counts.MatchedByBoth++
			}
		case domain.Conflict:
			projection.Counts.Conflict++
		default:
			projection.Counts.Unmatched++
			if row.IdentityReasonCode == string(identityport.HXCReasonInvalidPhone) || row.IdentityReasonCode == string(identityport.HXCReasonInvalidUnionID) {
				projection.Counts.InvalidIdentity++
			} else {
				projection.Counts.PendingObservation++
			}
		}
	}
	coverageJSON, _ := json.Marshal(projection.RegistrationCoverage)
	h.Write(coverageJSON)
	copy(projection.ProjectionDigest[:], h.Sum(nil))
	return projection, nil
}

func sameHXCResult(left, right identityport.HXCSubjectResult) bool {
	return left.Position == right.Position && left.Disposition == right.Disposition && left.MatchedBy == right.MatchedBy && left.Reason == right.Reason && left.CustomerID == right.CustomerID && left.ConflictID == right.ConflictID && left.MergeCandidateID == right.MergeCandidateID
}

func hxcPayloadDigest(key []byte, row domain.SourceRow) [32]byte {
	encoded, _ := json.Marshal(row)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("hxc-payload-v2\x00"))
	_, _ = mac.Write(encoded)
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	return digest
}

func normalizeCN11(value string) (string, bool) {
	if value == "" {
		return "", true
	}
	var compact strings.Builder
	for _, character := range strings.TrimSpace(value) {
		switch {
		case character >= '0' && character <= '9':
			compact.WriteRune(character)
		case character == '+' || character == '-' || character == '(' || character == ')' || character == '.' || character == ' ' || character == '\t':
		default:
			return "", false
		}
	}
	normalized := compact.String()
	if len(normalized) == 13 && strings.HasPrefix(normalized, "86") {
		normalized = normalized[2:]
	}
	if len(normalized) != 11 || normalized[0] != '1' || normalized[1] < '3' || normalized[1] > '9' {
		return "", false
	}
	return normalized, true
}

func markDuplicateSubjects(subjects []identityport.HXCSubject, owners map[string][]int, reason identityport.HXCReason) {
	for _, positions := range owners {
		if len(positions) < 2 {
			continue
		}
		for _, position := range positions {
			if subjects[position].ConflictReason == "" {
				subjects[position].ConflictReason = reason
			}
		}
	}
}

func applyIdentityResult(row *domain.ProjectionRow, result identityport.HXCSubjectResult) {
	row.MatchedBy = string(result.MatchedBy)
	row.IdentityReasonCode = string(result.Reason)
	row.IdentityCaseID = result.ConflictID
	row.MergeCandidateID = result.MergeCandidateID
	row.CustomerID = result.CustomerID
	switch result.Disposition {
	case identityport.HXCMatched:
		row.IdentityState = domain.Matched
	case identityport.HXCConflict:
		row.IdentityState = domain.Conflict
	default:
		row.IdentityState = domain.Unmatched
	}
}
func (s Service) fail(ctx context.Context, id int64, code string, cause error) error {
	_ = s.UOW.Within(context.WithoutCancel(ctx), func(txCtx context.Context) error { return s.Store.Fail(txCtx, id, code) })
	return fmt.Errorf("%s: %w", code, cause)
}
